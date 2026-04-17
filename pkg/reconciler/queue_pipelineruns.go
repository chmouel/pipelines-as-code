package reconciler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacAPIv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
	if !exist {
		// if the pipelineRun doesn't have order label then wait
		return nil
	}

	// check if annotation exist
	repoName, exist := pr.GetAnnotations()[keys.Repository]
	if !exist {
		return fmt.Errorf("no %s annotation found", keys.Repository)
	}
	if repoName == "" {
		return fmt.Errorf("annotation %s is empty", keys.Repository)
	}
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&pacAPIv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: pr.Namespace,
				},
			})
			return nil
		}
		return fmt.Errorf("error getting PipelineRun: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		logger.Info("Merging global repository settings with local repository settings")
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		logger.Debugf("concurrency disabled for repository %s; promoting queued pipelinerun %s immediately", repo.GetName(), pr.GetName())
		_ = r.qm.RemoveAndTakeItemFromQueue(ctx, repo, pr)
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	var processed bool
	var itered int
	maxIterations := 5

	orderedList := queuepkg.FilterPipelineRunByState(ctx, r.run.Clients.Tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	logger.Debugf(
		"processing queued pipelinerun %s/%s for repository %s with concurrency limit %v and ordered candidates %v",
		pr.Namespace, pr.Name, repo.GetName(), repo.Spec.ConcurrencyLimit, orderedList,
	)
	for {
		logger.Debugf("attempting queue acquisition loop %d for repository %s and pipelinerun %s/%s", itered+1, repo.GetName(), pr.Namespace, pr.Name)
		acquired, err := r.qm.AddListToRunningQueue(ctx, repo, orderedList)
		if err != nil {
			if r.eventEmitter != nil && strings.Contains(err.Error(), "timed out acquiring concurrency lease") {
				r.eventEmitter.EmitMessage(repo, zap.WarnLevel, "QueueLeaseAcquireTimeout",
					"timed out acquiring the lease-backed concurrency lock for repository "+queuepkg.RepoKey(repo))
			}
			return fmt.Errorf("failed to add to queue: %s: %w", pr.GetName(), err)
		}
		logger.Debugf("queue acquisition for repository %s returned candidates %v", repo.GetName(), acquired)
		if len(acquired) == 0 {
			logger.Infof("no new PipelineRun acquired for repo %s", repo.GetName())
			break
		}

		for i, prKeys := range acquired {
			logger.Debugf("attempting to promote queued pipelinerun %s for repository %s", prKeys, repo.GetName())
			if r.eventEmitter != nil {
				r.eventEmitter.EmitMessage(repo, zap.InfoLevel, "QueueClaimedForPromotion",
					"claimed queued PipelineRun "+prKeys+" for promotion in repository "+queuepkg.RepoKey(repo))
			}
			nsName := strings.Split(prKeys, "/")
			pr, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
			if err != nil {
				logger.Info("failed to get pr with namespace and name: ", nsName[0], nsName[1])
				logger.Debugf("clearing queue claim for missing pipelinerun %s after failed fetch", prKeys)
				_ = r.qm.RemoveFromQueue(ctx, repo, prKeys)
			} else {
				if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
					started, startedErr := r.pipelineRunReachedStartedState(ctx, pr)
					if startedErr != nil {
						logger.Warnf("failed to verify whether pipelineRun %s reached started state: %v", pr.GetName(), startedErr)
					}
					if started {
						logger.Warnf("pipelineRun %s already reached started state despite promotion error: %v", pr.GetName(), err)
						processed = true
						continue
					}
					logger.Errorf("failed to update pipelineRun to in_progress: %w", err)
					logger.Debugf("recording queue promotion failure for pipelinerun %s after promotion error", queuepkg.PrKey(pr))
					cleanupKeys := append([]string{prKeys}, acquired[i+1:]...)
					retryErr := r.recordQueuePromotionFailure(ctx, logger, repo, pr, err)
					if r.eventEmitter != nil {
						r.eventEmitter.EmitMessage(repo, zap.WarnLevel, "QueuePromotionFailed",
							"failed to promote queued PipelineRun "+queuepkg.PrKey(pr)+": "+err.Error())
					}
					r.clearQueueClaims(ctx, logger, repo, cleanupKeys, "promotion failure")
					if retryErr != nil {
						return fmt.Errorf("failed to record queue promotion failure for %s after promotion error: %w", pr.GetName(), retryErr)
					}
					return fmt.Errorf("failed to update pipelineRun to in_progress: %w", err)
				}
				logger.Debugf("successfully promoted queued pipelinerun %s for repository %s", queuepkg.PrKey(pr), repo.GetName())
				processed = true
			}
		}
		if processed {
			break
		}
		if itered >= maxIterations {
			return fmt.Errorf("max iterations reached of %d times trying to get a pipelinerun started for %s", maxIterations, repo.GetName())
		}
		itered++
	}
	return nil
}

func (r *Reconciler) clearQueueClaims(
	ctx context.Context,
	logger *zap.SugaredLogger,
	repo *pacAPIv1alpha1.Repository,
	prKeys []string,
	reason string,
) {
	for _, prKey := range prKeys {
		if prKey == "" {
			continue
		}
		logger.Debugf("removing queue claim for pipelinerun %s after %s", prKey, reason)
		_ = r.qm.RemoveFromQueue(ctx, repo, prKey)
	}
}

func (r *Reconciler) pipelineRunReachedStartedState(ctx context.Context, pr *tektonv1.PipelineRun) (bool, error) {
	latest, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	started := latest.GetAnnotations()[keys.State] == kubeinteraction.StateStarted
	r.run.Clients.Log.Debugf("checked latest state for pipelinerun %s/%s while handling queue promotion: state=%s started=%t",
		pr.Namespace, pr.Name, latest.GetAnnotations()[keys.State], started)
	return started, nil
}

func (r *Reconciler) recordQueuePromotionFailure(
	ctx context.Context,
	logger *zap.SugaredLogger,
	repo *pacAPIv1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	cause error,
) error {
	retries := 0
	if current := pr.GetAnnotations()[keys.QueuePromotionRetries]; current != "" {
		parsed, err := strconv.Atoi(current)
		if err == nil {
			retries = parsed
		}
	}
	retries++
	logger.Debugf(
		"recording queue promotion failure for pipelinerun %s/%s with retry=%d cause=%v",
		pr.Namespace, pr.Name, retries, cause,
	)

	annotations := map[string]any{
		keys.QueuePromotionRetries: strconv.Itoa(retries),
		keys.QueuePromotionLastErr: cause.Error(),
	}
	snapshot := r.queueDebugSnapshotForPipelineRun(repo, pr, queuepkg.QueueDecisionPromotionFailed)
	annotations[keys.QueueDecision] = snapshot.LastDecision
	annotations[keys.QueueDebugSummary] = snapshot.Summary()

	_, err := action.PatchPipelineRun(ctx, logger, "queue promotion failure", r.run.Clients.Tekton, pr, map[string]any{
		"metadata": map[string]any{
			"annotations": annotations,
		},
	})
	if err != nil {
		return err
	}
	logger.Debugf("recorded queue promotion failure annotations for pipelinerun %s/%s", pr.Namespace, pr.Name)
	return nil
}

func (r *Reconciler) queueDebugSnapshotForPipelineRun(
	repo *pacAPIv1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	decision string,
) queuepkg.DebugSnapshot {
	position := -1
	if index, ok := queuepkg.ExecutionOrderIndex(pr); ok {
		position = index + 1
	}

	limit := -1
	if repo != nil && repo.Spec.ConcurrencyLimit != nil {
		limit = *repo.Spec.ConcurrencyLimit
	}

	claimedBy, claimAge := queuepkg.LeaseQueueClaimInfo(pr, time.Now())
	repoKey := ""
	if repo != nil {
		repoKey = queuepkg.RepoKey(repo)
	}
	backend := settings.ConcurrencyBackendMemory
	if r != nil && r.run != nil && r.run.Info.Pac != nil {
		backend = r.run.Info.GetPacOpts().ConcurrencyBackend
		if backend == "" {
			backend = settings.ConcurrencyBackendMemory
		}
	}

	return queuepkg.DebugSnapshot{
		Backend:      backend,
		RepoKey:      repoKey,
		Position:     position,
		Running:      -1,
		Claimed:      -1,
		Queued:       -1,
		Limit:        limit,
		ClaimedBy:    claimedBy,
		ClaimAge:     claimAge,
		LastDecision: decision,
	}
}
