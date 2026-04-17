package queue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonclient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

const (
	defaultLeaseClaimTTL       = 30 * time.Second
	defaultLeaseDuration       = int32(30)
	defaultLeaseAcquireRetries = 20
	defaultLeaseRetryDelay     = 100 * time.Millisecond
)

type LeaseManager struct {
	logger         *zap.SugaredLogger
	kube           kubernetes.Interface
	tekton         tektonclient.Interface
	pipelineLister tektonv1lister.PipelineRunLister
	leaseNamespace string
	identity       string
	claimTTL       time.Duration
	leaseDuration  int32
	now            func() time.Time
	wait           func(context.Context, time.Duration) error
}

var (
	_ ManagerInterface       = (*LeaseManager)(nil)
	_ PipelineRunListerAware = (*LeaseManager)(nil)
)

func NewManagerForBackend(
	logger *zap.SugaredLogger,
	kube kubernetes.Interface,
	tekton tektonclient.Interface,
	leaseNamespace, backend string,
) ManagerInterface {
	if backend == settings.ConcurrencyBackendLease {
		logger.Debugf("initializing lease-backed concurrency manager in namespace %s", leaseNamespace)
		return NewLeaseManager(logger, kube, tekton, leaseNamespace)
	}
	logger.Debugf("initializing in-memory concurrency manager for backend %q", backend)
	return NewManager(logger)
}

func NewLeaseManager(
	logger *zap.SugaredLogger,
	kube kubernetes.Interface,
	tekton tektonclient.Interface,
	leaseNamespace string,
) *LeaseManager {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "pac-watcher"
	}

	return &LeaseManager{
		logger:         logger,
		kube:           kube,
		tekton:         tekton,
		leaseNamespace: leaseNamespace,
		identity:       fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano()),
		claimTTL:       defaultLeaseClaimTTL,
		leaseDuration:  defaultLeaseDuration,
		now:            time.Now,
		wait:           waitForLeaseRetry,
	}
}

func (m *LeaseManager) InitQueues(context.Context, tektonclient.Interface, versioned.Interface) error {
	return nil
}

func (m *LeaseManager) RecoveryInterval() time.Duration {
	return m.claimTTL
}

func DefaultLeaseClaimTTL() time.Duration {
	return defaultLeaseClaimTTL
}

func (m *LeaseManager) SetPipelineRunLister(lister tektonv1lister.PipelineRunLister) {
	m.pipelineLister = lister
}

func (m *LeaseManager) RemoveRepository(repo *v1alpha1.Repository) {
	releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	m.logger.Debugf("deleting concurrency lease for repository %s", RepoKey(repo))
	if err := m.kube.CoordinationV1().Leases(m.leaseNamespace).Delete(releaseCtx, repoLeaseName(RepoKey(repo)), metav1.DeleteOptions{}); err != nil &&
		!apierrors.IsNotFound(err) {
		m.logger.Warnf("failed to delete queue lease for repository %s: %v", RepoKey(repo), err)
	}
}

func (m *LeaseManager) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := m.getRepoQueueState(ctx, repo, nil, "")
	if err != nil {
		m.logger.Warnf("failed to compute queued pipelineruns for repository %s: %v", RepoKey(repo), err)
		return []string{}
	}

	keys := make([]string, 0, len(state.claimed)+len(state.queued))
	for i := range state.claimed {
		keys = append(keys, PrKey(&state.claimed[i]))
	}
	for i := range state.queued {
		keys = append(keys, PrKey(&state.queued[i]))
	}
	return keys
}

func (m *LeaseManager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	state, err := m.getRepoQueueState(ctx, repo, nil, "")
	if err != nil {
		m.logger.Warnf("failed to compute running pipelineruns for repository %s: %v", RepoKey(repo), err)
		return []string{}
	}

	keys := make([]string, 0, len(state.running))
	for i := range state.running {
		keys = append(keys, PrKey(&state.running[i]))
	}
	return keys
}

func (m *LeaseManager) AddListToRunningQueue(ctx context.Context, repo *v1alpha1.Repository, orderedList []string) ([]string, error) {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		m.logger.Debugf("skipping lease queue claim for repository %s because concurrency limit is disabled", RepoKey(repo))
		return []string{}, nil
	}
	m.logger.Debugf(
		"attempting to claim queued pipelineruns for repository %s with concurrency limit %d and preferred order %v",
		RepoKey(repo), *repo.Spec.ConcurrencyLimit, orderedList,
	)
	return m.claimNextQueued(ctx, repo, orderedList, "", 0)
}

func (m *LeaseManager) AddToPendingQueue(*v1alpha1.Repository, []string) error {
	return nil
}

func (m *LeaseManager) RemoveFromQueue(ctx context.Context, repo *v1alpha1.Repository, prKey string) bool {
	m.logger.Debugf("clearing queue claim for pipelinerun %s in repository %s", prKey, RepoKey(repo))
	if err := m.clearClaim(ctx, repo, prKey); err != nil {
		m.logger.Warnf("failed to clear queue claim for %s: %v", prKey, err)
		return false
	}
	return true
}

func (m *LeaseManager) RemoveAndTakeItemFromQueue(ctx context.Context, repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		m.logger.Debugf("not attempting queue handoff for repository %s because concurrency limit is disabled", RepoKey(repo))
		return ""
	}

	orderedList := executionOrderList(run)
	m.logger.Debugf(
		"removing pipelinerun %s from repository %s and attempting to claim next queued item from order %v",
		PrKey(run), RepoKey(repo), orderedList,
	)
	claimed, err := m.claimNextQueued(ctx, repo, orderedList, PrKey(run), 1)
	if err != nil {
		m.logger.Warnf("failed to claim next queued pipelinerun for repository %s: %v", RepoKey(repo), err)
		return ""
	}
	if len(claimed) == 0 {
		m.logger.Debugf("no queued pipelinerun available to claim for repository %s after removing %s", RepoKey(repo), PrKey(run))
		return ""
	}
	m.logger.Debugf("claimed next queued pipelinerun %s for repository %s", claimed[0], RepoKey(repo))
	return claimed[0]
}

type repoQueueState struct {
	running []tektonv1.PipelineRun
	claimed []tektonv1.PipelineRun
	queued  []tektonv1.PipelineRun
}

type pipelineOrderMeta struct {
	groupTime time.Time
	position  int
}

func (m *LeaseManager) claimNextQueued(
	ctx context.Context,
	repo *v1alpha1.Repository,
	preferredOrder []string,
	excludeKey string,
	maxClaims int,
) ([]string, error) {
	claimed := []string{}
	var debugState *repoQueueState
	var debugNewlyClaimed map[string]struct{}

	err := m.withRepoLease(ctx, repo, func(lockCtx context.Context) error {
		state, err := m.getRepoQueueState(lockCtx, repo, preferredOrder, excludeKey)
		if err != nil {
			return err
		}

		occupied := len(state.running) + len(state.claimed)
		available := *repo.Spec.ConcurrencyLimit - occupied
		m.logger.Debugf(
			"lease queue state for repository %s: running=%d claimed=%d queued=%d occupied=%d available=%d exclude=%q preferred=%v",
			RepoKey(repo), len(state.running), len(state.claimed), len(state.queued), occupied, available, excludeKey, preferredOrder,
		)
		if maxClaims > 0 && available > maxClaims {
			available = maxClaims
		}
		remainingQueued := make([]tektonv1.PipelineRun, 0, len(state.queued))
		newlyClaimed := map[string]struct{}{}
		for _, pr := range state.queued {
			if available <= 0 {
				remainingQueued = append(remainingQueued, pr)
				continue
			}

			m.logger.Debugf("attempting to claim queued pipelinerun %s for repository %s", PrKey(&pr), RepoKey(repo))
			ok, err := m.claimPipelineRun(lockCtx, &pr)
			if err != nil {
				return err
			}
			if ok {
				prKey := PrKey(&pr)
				annotations := pr.GetAnnotations()
				if annotations == nil {
					annotations = map[string]string{}
				}
				annotations[keys.QueueClaimedBy] = m.identity
				annotations[keys.QueueClaimedAt] = m.now().UTC().Format(time.RFC3339Nano)
				pr.SetAnnotations(annotations)
				claimed = append(claimed, prKey)
				newlyClaimed[prKey] = struct{}{}
				state.claimed = append(state.claimed, pr)
				available--
				m.logger.Debugf(
					"claimed queued pipelinerun %s for repository %s; remaining available slots %d",
					prKey, RepoKey(repo), available,
				)
				continue
			}

			remainingQueued = append(remainingQueued, pr)
			m.logger.Debugf("pipelinerun %s could not be claimed for repository %s", PrKey(&pr), RepoKey(repo))
		}
		state.queued = remainingQueued
		debugState = cloneRepoQueueState(state)
		debugNewlyClaimed = cloneStringSet(newlyClaimed)

		if available <= 0 && len(claimed) == 0 {
			m.logger.Debugf("repository %s has no available concurrency slots", RepoKey(repo))
		}
		return nil
	})

	if err == nil {
		if debugState != nil {
			if err := m.syncQueueDebugState(ctx, repo, debugState, debugNewlyClaimed); err != nil {
				m.logger.Warnf("failed to sync queue debug state for repository %s: %v", RepoKey(repo), err)
			}
		}
		m.logger.Debugf("finished lease queue claim for repository %s; claimed=%v", RepoKey(repo), claimed)
	}
	return claimed, err
}

func (m *LeaseManager) getRepoQueueState(ctx context.Context, repo *v1alpha1.Repository, preferredOrder []string, excludeKey string) (*repoQueueState, error) {
	prs, err := m.listRepositoryPipelineRuns(ctx, repo)
	if err != nil {
		return nil, err
	}

	state := &repoQueueState{}
	now := m.now()
	groupMinTime := map[string]time.Time{}
	preferredIndex := make(map[string]int, len(preferredOrder))
	for i, key := range preferredOrder {
		preferredIndex[key] = i
	}

	for i := range prs {
		pr := prs[i]
		if pr.GetAnnotations()[keys.Repository] != repo.Name {
			continue
		}
		order := pr.GetAnnotations()[keys.ExecutionOrder]
		if order == "" {
			continue
		}
		if existing, ok := groupMinTime[order]; !ok || pr.CreationTimestamp.Time.Before(existing) {
			groupMinTime[order] = pr.CreationTimestamp.Time
		}
	}

	orderMeta := map[string]pipelineOrderMeta{}

	for i := range prs {
		pr := prs[i]
		if pr.GetAnnotations()[keys.Repository] != repo.Name {
			continue
		}
		if excludeKey != "" && PrKey(&pr) == excludeKey {
			continue
		}

		switch pr.GetAnnotations()[keys.State] {
		case kubeinteraction.StateStarted:
			if !pr.IsDone() && !pr.IsCancelled() {
				state.running = append(state.running, pr)
			}
		case kubeinteraction.StateQueued:
			if !IsRecoverableQueuedPipelineRun(&pr) {
				decision := QueueDecisionNotRecoverable
				if _, ok := executionOrderIndex(&pr); !ok {
					decision = QueueDecisionMissingOrder
				}
				if err := SyncQueueDebugAnnotations(ctx, m.logger, m.tekton, &pr, DebugSnapshot{
					Backend:      settings.ConcurrencyBackendLease,
					RepoKey:      RepoKey(repo),
					Position:     unknownQueueDebugValue,
					Running:      len(state.running),
					Claimed:      len(state.claimed),
					Queued:       len(state.queued),
					Limit:        repositoryConcurrencyLimit(repo),
					LastDecision: decision,
				}); err != nil {
					m.logger.Warnf("failed to record queue debug state for pipelinerun %s: %v", PrKey(&pr), err)
				}
				m.logger.Debugf(
					"skipping queued pipelinerun %s for repository %s because it is not recoverable",
					PrKey(&pr), RepoKey(repo),
				)
				continue
			}
			position, ok := executionOrderIndex(&pr)
			if !ok {
				m.logger.Warnf("ignoring queued pipelinerun %s because execution-order does not include itself", PrKey(&pr))
				continue
			}
			orderMeta[PrKey(&pr)] = pipelineOrderMeta{
				groupTime: groupMinTime[pr.GetAnnotations()[keys.ExecutionOrder]],
				position:  position,
			}
			if m.hasActiveClaim(&pr, now) {
				m.logger.Debugf("queued pipelinerun %s for repository %s already has an active claim", PrKey(&pr), RepoKey(repo))
				state.claimed = append(state.claimed, pr)
			} else {
				m.logger.Debugf("queued pipelinerun %s for repository %s is available for claiming", PrKey(&pr), RepoKey(repo))
				state.queued = append(state.queued, pr)
			}
		}
	}

	sort.Slice(state.running, func(i, j int) bool {
		return comparePipelineRuns(&state.running[i], &state.running[j])
	})
	sort.Slice(state.claimed, func(i, j int) bool {
		return compareQueueCandidates(&state.claimed[i], &state.claimed[j], preferredIndex, orderMeta)
	})
	sort.Slice(state.queued, func(i, j int) bool {
		return compareQueueCandidates(&state.queued[i], &state.queued[j], preferredIndex, orderMeta)
	})

	m.logger.Debugf(
		"computed lease queue state for repository %s with running=%v claimed=%v queued=%v",
		RepoKey(repo), pipelineRunKeys(state.running), pipelineRunKeys(state.claimed), pipelineRunKeys(state.queued),
	)
	return state, nil
}

func compareQueueCandidates(
	left, right *tektonv1.PipelineRun,
	preferredIndex map[string]int,
	orderMeta map[string]pipelineOrderMeta,
) bool {
	leftKey := PrKey(left)
	rightKey := PrKey(right)

	leftPreferred, leftOK := preferredIndex[leftKey]
	rightPreferred, rightOK := preferredIndex[rightKey]
	switch {
	case leftOK && rightOK && leftPreferred != rightPreferred:
		return leftPreferred < rightPreferred
	case leftOK != rightOK:
		return leftOK
	}

	leftMeta, leftMetaOK := orderMeta[leftKey]
	rightMeta, rightMetaOK := orderMeta[rightKey]
	switch {
	case leftMetaOK && rightMetaOK:
		if !leftMeta.groupTime.Equal(rightMeta.groupTime) {
			return leftMeta.groupTime.Before(rightMeta.groupTime)
		}
		if leftMeta.position != rightMeta.position {
			return leftMeta.position < rightMeta.position
		}
	case leftMetaOK != rightMetaOK:
		return leftMetaOK
	}

	return comparePipelineRuns(left, right)
}

func comparePipelineRuns(left, right *tektonv1.PipelineRun) bool {
	if left.CreationTimestamp.Equal(&right.CreationTimestamp) {
		return left.GetName() < right.GetName()
	}
	return left.CreationTimestamp.Before(&right.CreationTimestamp)
}

func (m *LeaseManager) hasActiveClaim(pr *tektonv1.PipelineRun, now time.Time) bool {
	active := HasActiveLeaseQueueClaim(pr, now, m.claimTTL)
	claimedBy, age := LeaseQueueClaimInfo(pr, now)
	m.logger.Debugf(
		"evaluated queue claim for pipelinerun %s: claimedBy=%s age=%s ttl=%s active=%t",
		PrKey(pr), claimedBy, age, m.claimTTL, active,
	)
	return active
}

func (m *LeaseManager) claimPipelineRun(ctx context.Context, pr *tektonv1.PipelineRun) (bool, error) {
	claimedAt := m.now().UTC().Format(time.RFC3339Nano)
	m.logger.Debugf("patching pipelinerun %s with queue claim owned by %s at %s", PrKey(pr), m.identity, claimedAt)
	mergePatch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				keys.QueueClaimedBy: m.identity,
				keys.QueueClaimedAt: claimedAt,
			},
		},
	}

	if _, err := action.PatchPipelineRun(ctx, m.logger, "queue claim", m.tekton, pr, mergePatch); err != nil {
		if apierrors.IsNotFound(err) {
			m.logger.Debugf("pipelinerun %s disappeared before queue claim could be recorded", PrKey(pr))
			return false, nil
		}
		return false, err
	}
	m.logger.Debugf("successfully recorded queue claim for pipelinerun %s", PrKey(pr))
	return true, nil
}

func (m *LeaseManager) clearClaim(ctx context.Context, repo *v1alpha1.Repository, prKey string) error {
	nameParts := strings.Split(prKey, "/")
	if len(nameParts) != 2 {
		return fmt.Errorf("invalid pipelinerun key %q", prKey)
	}

	pr, err := m.tekton.TektonV1().PipelineRuns(nameParts[0]).Get(ctx, nameParts[1], metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			m.logger.Debugf("pipelinerun %s was already deleted before queue claim cleanup", prKey)
			return nil
		}
		return err
	}

	if pr.GetAnnotations()[keys.Repository] != repo.Name {
		m.logger.Debugf(
			"skipping queue claim cleanup for pipelinerun %s because it belongs to repository %s instead of %s",
			prKey, pr.GetAnnotations()[keys.Repository], repo.Name,
		)
		return nil
	}

	m.logger.Debugf("removing queue claim annotations from pipelinerun %s", prKey)
	mergePatch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				keys.QueueClaimedBy: nil,
				keys.QueueClaimedAt: nil,
			},
		},
	}

	_, err = action.PatchPipelineRun(ctx, m.logger, "queue claim cleanup", m.tekton, pr, mergePatch)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	m.logger.Debugf("queue claim cleanup completed for pipelinerun %s", prKey)
	return nil
}

func (m *LeaseManager) withRepoLease(ctx context.Context, repo *v1alpha1.Repository, fn func(context.Context) error) error {
	leaseName := repoLeaseName(RepoKey(repo))
	leaseWaitBudget := time.Duration(m.leaseDuration)*time.Second + defaultLeaseRetryDelay
	if leaseWaitBudget <= 0 {
		leaseWaitBudget = defaultLeaseRetryDelay
	}
	deadline := m.now().Add(leaseWaitBudget)
	maxAttempts := int(leaseWaitBudget/defaultLeaseRetryDelay) + 1

	for attempt := 1; ; attempt++ {
		m.logger.Debugf(
			"attempting to acquire concurrency lease %s for repository %s (attempt %d/%d)",
			leaseName, RepoKey(repo), attempt, maxAttempts,
		)
		acquired, err := m.tryAcquireLease(ctx, leaseName)
		if err != nil {
			return err
		}
		if acquired {
			m.logger.Debugf("acquired concurrency lease %s for repository %s", leaseName, RepoKey(repo))
			leaseCtx, cancel := context.WithCancel(ctx)
			renewErrCh := make(chan error, 1)
			renewDone := make(chan struct{})

			go func() {
				defer close(renewDone)
				if err := m.keepLeaseRenewed(leaseCtx, leaseName); err != nil {
					select {
					case renewErrCh <- err:
					default:
					}
					cancel()
				}
			}()

			callbackErr := fn(leaseCtx)
			cancel()
			<-renewDone
			m.releaseLease(leaseName)

			var renewErr error
			select {
			case renewErr = <-renewErrCh:
			default:
			}

			if callbackErr != nil {
				if renewErr != nil && stderrors.Is(callbackErr, context.Canceled) {
					return renewErr
				}
				return callbackErr
			}
			if renewErr != nil {
				return renewErr
			}
			return nil
		}
		m.logger.Debugf("concurrency lease %s for repository %s is currently held by another watcher", leaseName, RepoKey(repo))
		if !m.now().Before(deadline) {
			break
		}
		if err := m.wait(ctx, defaultLeaseRetryDelay); err != nil {
			return err
		}
	}

	return fmt.Errorf("timed out acquiring concurrency lease %s after waiting %s", leaseName, leaseWaitBudget)
}

func (m *LeaseManager) listRepositoryPipelineRuns(ctx context.Context, repo *v1alpha1.Repository) ([]tektonv1.PipelineRun, error) {
	selector := labels.SelectorFromSet(map[string]string{
		keys.Repository: formatting.CleanValueKubernetes(repo.Name),
	})

	if m.pipelineLister != nil {
		prs, err := m.pipelineLister.PipelineRuns(repo.Namespace).List(selector)
		if err != nil {
			return nil, err
		}

		items := make([]tektonv1.PipelineRun, 0, len(prs))
		for _, pr := range prs {
			if pr == nil {
				continue
			}
			items = append(items, *pr)
		}
		return items, nil
	}

	prs, err := m.tekton.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return nil, err
	}

	return prs.Items, nil
}

func waitForLeaseRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (m *LeaseManager) leaseRenewInterval() time.Duration {
	interval := time.Duration(m.leaseDuration) * time.Second / 3
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func (m *LeaseManager) keepLeaseRenewed(ctx context.Context, leaseName string) error {
	interval := m.leaseRenewInterval()
	for {
		if err := m.wait(ctx, interval); err != nil {
			if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := m.renewLease(ctx, leaseName); err != nil {
			return err
		}
	}
}

func (m *LeaseManager) renewLease(ctx context.Context, leaseName string) error {
	leases := m.kube.CoordinationV1().Leases(m.leaseNamespace)
	now := metav1.MicroTime{Time: m.now()}
	var holderErr error

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		holder := ""
		if lease.Spec.HolderIdentity != nil {
			holder = *lease.Spec.HolderIdentity
		}
		if holder != m.identity {
			holderErr = fmt.Errorf("cannot renew concurrency lease %s because it is held by %q", leaseName, holder)
			return holderErr
		}

		updated := lease.DeepCopy()
		updated.Spec.RenewTime = &now
		updated.Spec.LeaseDurationSeconds = &m.leaseDuration

		_, err = leases.Update(ctx, updated, metav1.UpdateOptions{})
		return err
	})
	if holderErr != nil {
		return holderErr
	}
	if err != nil {
		return err
	}

	m.logger.Debugf("renewed concurrency lease %s for holder %s", leaseName, m.identity)
	return nil
}

func (m *LeaseManager) tryAcquireLease(ctx context.Context, leaseName string) (bool, error) {
	leases := m.kube.CoordinationV1().Leases(m.leaseNamespace)
	now := metav1.MicroTime{Time: m.now()}

	lease, err := leases.Get(ctx, leaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		m.logger.Debugf("creating new concurrency lease %s for identity %s", leaseName, m.identity)
		_, err = leases.Create(ctx, &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: m.leaseNamespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &m.identity,
				LeaseDurationSeconds: &m.leaseDuration,
				AcquireTime:          &now,
				RenewTime:            &now,
			},
		}, metav1.CreateOptions{})
		if err == nil {
			m.logger.Debugf("created and acquired concurrency lease %s", leaseName)
			return true, nil
		}
		if apierrors.IsAlreadyExists(err) {
			m.logger.Debugf("concurrency lease %s was created by another watcher before acquisition completed", leaseName)
			return false, nil
		}
		return false, err
	}
	if err != nil {
		return false, err
	}

	if !m.canTakeLease(lease, now.Time) {
		holder := ""
		if lease.Spec.HolderIdentity != nil {
			holder = *lease.Spec.HolderIdentity
		}
		m.logger.Debugf("cannot acquire concurrency lease %s because it is still held by %s", leaseName, holder)
		return false, nil
	}

	updated := lease.DeepCopy()
	updated.Spec.HolderIdentity = &m.identity
	updated.Spec.LeaseDurationSeconds = &m.leaseDuration
	updated.Spec.RenewTime = &now
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != m.identity {
		updated.Spec.AcquireTime = &now
	}

	if _, err := leases.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsConflict(err) {
			m.logger.Debugf("conflict while updating concurrency lease %s; another watcher won the race", leaseName)
			return false, nil
		}
		return false, err
	}

	m.logger.Debugf("updated concurrency lease %s to holder %s", leaseName, m.identity)
	return true, nil
}

func (m *LeaseManager) canTakeLease(lease *coordinationv1.Lease, now time.Time) bool {
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
		return true
	}
	if *lease.Spec.HolderIdentity == m.identity {
		return true
	}
	return isLeaseExpired(lease, now)
}

func isLeaseExpired(lease *coordinationv1.Lease, now time.Time) bool {
	if lease.Spec.LeaseDurationSeconds == nil {
		return true
	}

	base := time.Time{}
	if lease.Spec.RenewTime != nil {
		base = lease.Spec.RenewTime.Time
	} else if lease.Spec.AcquireTime != nil {
		base = lease.Spec.AcquireTime.Time
	}
	if base.IsZero() {
		return true
	}

	return base.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second).Before(now)
}

func (m *LeaseManager) releaseLease(leaseName string) {
	releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	leases := m.kube.CoordinationV1().Leases(m.leaseNamespace)
	now := metav1.MicroTime{Time: m.now()}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		lease, err := leases.Get(releaseCtx, leaseName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}

		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
			return nil
		}
		if *lease.Spec.HolderIdentity != m.identity {
			m.logger.Debugf("skipping release for concurrency lease %s held by %s", leaseName, *lease.Spec.HolderIdentity)
			return nil
		}

		updated := lease.DeepCopy()
		updated.Spec.HolderIdentity = nil
		updated.Spec.RenewTime = &now

		_, err = leases.Update(releaseCtx, updated, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		m.logger.Warnf("failed to release concurrency lease %s: %v", leaseName, err)
	}
}

func repoLeaseName(repoKey string) string {
	sum := sha256.Sum256([]byte(repoKey))
	return fmt.Sprintf("pac-concurrency-%s", hex.EncodeToString(sum[:8]))
}

func pipelineRunKeys(prs []tektonv1.PipelineRun) []string {
	keys := make([]string, 0, len(prs))
	for i := range prs {
		keys = append(keys, PrKey(&prs[i]))
	}
	return keys
}

func cloneRepoQueueState(state *repoQueueState) *repoQueueState {
	if state == nil {
		return nil
	}

	cloned := &repoQueueState{}
	if len(state.running) > 0 {
		cloned.running = append([]tektonv1.PipelineRun(nil), state.running...)
	}
	if len(state.claimed) > 0 {
		cloned.claimed = append([]tektonv1.PipelineRun(nil), state.claimed...)
	}
	if len(state.queued) > 0 {
		cloned.queued = append([]tektonv1.PipelineRun(nil), state.queued...)
	}
	return cloned
}

func cloneStringSet(values map[string]struct{}) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}

	cloned := make(map[string]struct{}, len(values))
	for key := range values {
		cloned[key] = struct{}{}
	}
	return cloned
}

func (m *LeaseManager) syncQueueDebugState(
	ctx context.Context,
	repo *v1alpha1.Repository,
	state *repoQueueState,
	newlyClaimed map[string]struct{},
) error {
	queueOrder := make([]tektonv1.PipelineRun, 0, len(state.claimed)+len(state.queued))
	queueOrder = append(queueOrder, state.claimed...)
	queueOrder = append(queueOrder, state.queued...)

	positions := make(map[string]int, len(queueOrder))
	for i := range queueOrder {
		positions[PrKey(&queueOrder[i])] = i + 1
	}

	limit := repositoryConcurrencyLimit(repo)
	for i := range state.claimed {
		pr := &state.claimed[i]
		decision := QueueDecisionClaimActive
		if _, ok := newlyClaimed[PrKey(pr)]; ok {
			decision = QueueDecisionClaimedForPromote
		}
		claimedBy, claimAge := LeaseQueueClaimInfo(pr, m.now())
		if err := SyncQueueDebugAnnotations(ctx, m.logger, m.tekton, pr, DebugSnapshot{
			Backend:      settings.ConcurrencyBackendLease,
			RepoKey:      RepoKey(repo),
			Position:     positions[PrKey(pr)],
			Running:      len(state.running),
			Claimed:      len(state.claimed),
			Queued:       len(state.queued),
			Limit:        limit,
			ClaimedBy:    claimedBy,
			ClaimAge:     claimAge,
			LastDecision: decision,
		}); err != nil {
			return err
		}
	}

	for i := range state.queued {
		pr := &state.queued[i]
		claimedBy, claimAge := LeaseQueueClaimInfo(pr, m.now())
		if err := SyncQueueDebugAnnotations(ctx, m.logger, m.tekton, pr, DebugSnapshot{
			Backend:      settings.ConcurrencyBackendLease,
			RepoKey:      RepoKey(repo),
			Position:     positions[PrKey(pr)],
			Running:      len(state.running),
			Claimed:      len(state.claimed),
			Queued:       len(state.queued),
			Limit:        limit,
			ClaimedBy:    claimedBy,
			ClaimAge:     claimAge,
			LastDecision: QueueDecisionWaitingForSlot,
		}); err != nil {
			return err
		}
	}

	return nil
}

func repositoryConcurrencyLimit(repo *v1alpha1.Repository) int {
	if repo == nil || repo.Spec.ConcurrencyLimit == nil {
		return unknownQueueDebugValue
	}
	return *repo.Spec.ConcurrencyLimit
}
