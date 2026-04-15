package reconciler

import (
	"context"
	"path"
	"sort"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/injection/informers/pipelinesascode/v1alpha1/repository"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonclient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tektonPipelineRunInformerv1 "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1/pipelinerun"
	tektonPipelineRunReconcilerv1 "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/system"
)

const leaseQueueRecoveryBuffer = time.Second

func NewController() func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, _ configmap.Watcher) *controller.Impl {
		ctx = info.StoreNS(ctx, system.Namespace())
		log := logging.FromContext(ctx)

		run := params.New()
		err := run.Clients.NewClients(ctx, &run.Info)
		if err != nil {
			log.Fatal("failed to init clients : ", err)
		}

		kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
		if err != nil {
			log.Fatal("failed to init kinit client : ", err)
		}

		if err := run.UpdatePacConfig(ctx); err != nil {
			log.Fatal("failed to load pac config: ", err)
		}

		pipelineRunInformer := tektonPipelineRunInformerv1.Get(ctx)
		metrics, err := prmetrics.NewRecorder()
		if err != nil {
			log.Fatalf("Failed to create pipeline as code metrics recorder %v", err)
		}
		pacInfo := run.Info.GetPacOpts()
		log.Infof("using concurrency backend %q; changing this setting requires restarting the watcher", pacInfo.ConcurrencyBackend)

		r := &Reconciler{
			run:               run,
			kinteract:         kinteract,
			pipelineRunLister: pipelineRunInformer.Lister(),
			repoLister:        repository.Get(ctx).Lister(),
			qm:                queuepkg.NewManagerForBackend(run.Clients.Log, run.Clients.Kube, run.Clients.Tekton, system.Namespace(), pacInfo.ConcurrencyBackend),
			metrics:           metrics,
			eventEmitter:      events.NewEventEmitter(run.Clients.Kube, run.Clients.Log),
		}
		impl := tektonPipelineRunReconcilerv1.NewImpl(ctx, r, ctrlOpts())

		if err := r.qm.InitQueues(ctx, run.Clients.Tekton, run.Clients.PipelineAsCode); err != nil {
			log.Fatal("failed to init queues", err)
		}

		if _, err := pipelineRunInformer.Informer().AddEventHandler(controller.HandleAll(checkStateAndEnqueue(impl))); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register PipelineRun informer event handler: %w", err)
		}

		if recoveryInterval := r.qm.RecoveryInterval(); recoveryInterval > 0 {
			startLeaseQueueRecoveryLoop(ctx, log, impl, r.pipelineRunLister, run.Clients.Tekton, r.eventEmitter, recoveryInterval+leaseQueueRecoveryBuffer)
		}

		// Start pac config syncer after the initial settings have been loaded.
		go params.StartConfigSync(ctx, run)

		return impl
	}
}

func startLeaseQueueRecoveryLoop(
	ctx context.Context,
	logger *zap.SugaredLogger,
	impl *controller.Impl,
	lister tektonv1lister.PipelineRunLister,
	tekton tektonclient.Interface,
	eventEmitter *events.EventEmitter,
	interval time.Duration,
) {
	if interval <= 0 {
		return
	}

	logger.Infof("starting lease queue recovery loop with interval %s", interval)
	runLeaseQueueRecovery(ctx, logger, impl, lister, tekton, eventEmitter)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runLeaseQueueRecovery(ctx, logger, impl, lister, tekton, eventEmitter)
			}
		}
	}()
}

func runLeaseQueueRecovery(
	ctx context.Context,
	logger *zap.SugaredLogger,
	impl *controller.Impl,
	lister tektonv1lister.PipelineRunLister,
	tekton tektonclient.Interface,
	eventEmitter *events.EventEmitter,
) {
	recoveryCandidates, err := leaseQueueRecoveryCandidates(lister)
	if err != nil {
		logger.Warnf("failed to list queued PipelineRuns for lease recovery: %v", err)
		return
	}
	recoveryKeys := selectLeaseQueueRecoveryKeys(recoveryCandidates)
	logger.Debugf("lease queue recovery selected %d repository candidate(s): %v", len(recoveryKeys), recoveryKeys)

	for _, pipelineRun := range recoveryCandidates {
		latest, err := tekton.TektonV1().PipelineRuns(pipelineRun.GetNamespace()).Get(ctx, pipelineRun.GetName(), metav1.GetOptions{})
		if err != nil {
			logger.Warnf("failed to re-fetch queued pipelinerun %s/%s for lease recovery: %v", pipelineRun.GetNamespace(), pipelineRun.GetName(), err)
			continue
		}
		if !queuepkg.IsRecoverableQueuedPipelineRun(latest) {
			logger.Debugf(
				"skipping stale lease recovery candidate %s/%s because latest state=%s spec.status=%s done=%t cancelled=%t",
				latest.GetNamespace(), latest.GetName(), latest.GetAnnotations()[keys.State], latest.Spec.Status, latest.IsDone(), latest.IsCancelled(),
			)
			if err := queuepkg.ClearQueueDebugAnnotations(ctx, logger, tekton, latest); err != nil {
				logger.Warnf("failed to clear stale queue debug state for %s/%s during lease recovery: %v", latest.GetNamespace(), latest.GetName(), err)
			}
			continue
		}

		if err := queuepkg.SyncQueueDebugAnnotations(ctx, logger, tekton, latest, queuepkg.DebugSnapshot{
			Backend:      settings.ConcurrencyBackendLease,
			RepoKey:      types.NamespacedName{Namespace: latest.GetNamespace(), Name: latest.GetAnnotations()[keys.Repository]}.String(),
			Position:     1,
			Running:      -1,
			Claimed:      -1,
			Queued:       -1,
			Limit:        -1,
			LastDecision: queuepkg.QueueDecisionRecoveryRequeued,
		}); err != nil {
			logger.Warnf("failed to record queue recovery debug state for %s/%s: %v", latest.GetNamespace(), latest.GetName(), err)
		}

		repoName := latest.GetAnnotations()[keys.Repository]
		if eventEmitter != nil && repoName != "" {
			eventEmitter.EmitMessage(&pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: latest.GetNamespace(),
				},
			}, zap.InfoLevel, "QueueRecoveryRequeued",
				"recovery loop re-enqueued queued PipelineRun "+latest.GetNamespace()+"/"+latest.GetName())
		}

		key := types.NamespacedName{Namespace: latest.GetNamespace(), Name: latest.GetName()}
		logger.Debugf("enqueuing queued pipelinerun %s for lease recovery", key.String())
		impl.EnqueueKey(key)
	}
}

func leaseQueueRecoveryCandidates(lister tektonv1lister.PipelineRunLister) ([]*tektonv1.PipelineRun, error) {
	pipelineRuns, err := lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	return selectLeaseQueueRecoveryCandidates(pipelineRuns), nil
}

func selectLeaseQueueRecoveryCandidates(pipelineRuns []*tektonv1.PipelineRun) []*tektonv1.PipelineRun {
	return selectLeaseQueueRecoveryCandidatesAt(pipelineRuns, time.Now(), queuepkg.DefaultLeaseClaimTTL())
}

func selectLeaseQueueRecoveryCandidatesAt(
	pipelineRuns []*tektonv1.PipelineRun,
	now time.Time,
	claimTTL time.Duration,
) []*tektonv1.PipelineRun {
	candidatesByRepo := map[string]*tektonv1.PipelineRun{}
	healthyRepos := map[string]struct{}{}

	for _, pipelineRun := range pipelineRuns {
		repoKey, ok := leaseQueueRecoveryRepoKey(pipelineRun)
		if !ok {
			continue
		}

		if hasHealthyLeaseQueueProgress(pipelineRun, now, claimTTL) {
			healthyRepos[repoKey] = struct{}{}
		}
		if !isEligibleLeaseQueueRecoveryCandidate(pipelineRun) {
			continue
		}
		if existing, ok := candidatesByRepo[repoKey]; !ok || shouldPreferLeaseQueueRecoveryCandidate(pipelineRun, existing) {
			candidatesByRepo[repoKey] = pipelineRun
		}
	}

	selected := make([]*tektonv1.PipelineRun, 0, len(candidatesByRepo))
	for repoKey, pipelineRun := range candidatesByRepo {
		if _, ok := healthyRepos[repoKey]; ok {
			continue
		}
		selected = append(selected, pipelineRun)
	}

	sort.Slice(selected, func(i, j int) bool {
		return shouldPreferLeaseQueueRecoveryCandidate(selected[i], selected[j])
	})

	return selected
}

func selectLeaseQueueRecoveryKeys(pipelineRuns []*tektonv1.PipelineRun) []types.NamespacedName {
	selected := selectLeaseQueueRecoveryCandidates(pipelineRuns)

	recoveryKeys := make([]types.NamespacedName, 0, len(selected))
	for _, pipelineRun := range selected {
		recoveryKeys = append(recoveryKeys, types.NamespacedName{
			Namespace: pipelineRun.GetNamespace(),
			Name:      pipelineRun.GetName(),
		})
	}
	return recoveryKeys
}

func isEligibleLeaseQueueRecoveryCandidate(pipelineRun *tektonv1.PipelineRun) bool {
	if pipelineRun == nil {
		return false
	}
	if pipelineRun.GetAnnotations()[keys.Repository] == "" {
		return false
	}
	return queuepkg.IsRecoverableQueuedPipelineRun(pipelineRun)
}

func leaseQueueRecoveryRepoKey(pipelineRun *tektonv1.PipelineRun) (string, bool) {
	if pipelineRun == nil {
		return "", false
	}

	repoName := pipelineRun.GetAnnotations()[keys.Repository]
	if repoName == "" {
		return "", false
	}

	return types.NamespacedName{
		Namespace: pipelineRun.GetNamespace(),
		Name:      repoName,
	}.String(), true
}

func hasHealthyLeaseQueueProgress(
	pipelineRun *tektonv1.PipelineRun,
	now time.Time,
	claimTTL time.Duration,
) bool {
	if pipelineRun == nil {
		return false
	}

	switch pipelineRun.GetAnnotations()[keys.State] {
	case kubeinteraction.StateStarted:
		return !pipelineRun.IsDone() && !pipelineRun.IsCancelled()
	case kubeinteraction.StateQueued:
		return queuepkg.HasActiveLeaseQueueClaim(pipelineRun, now, claimTTL)
	default:
		return false
	}
}

func shouldPreferLeaseQueueRecoveryCandidate(left, right *tektonv1.PipelineRun) bool {
	if left.CreationTimestamp.Equal(&right.CreationTimestamp) {
		return left.GetName() < right.GetName()
	}
	return left.CreationTimestamp.Before(&right.CreationTimestamp)
}

// enqueue only the pipelineruns which are in `started` state
// pipelinerun will have a label `pipelinesascode.tekton.dev/state` to describe the state.
func checkStateAndEnqueue(impl *controller.Impl) func(obj any) {
	return func(obj any) {
		object, err := kmeta.DeletionHandlingAccessor(obj)
		if err == nil {
			_, exist := object.GetAnnotations()[keys.State]
			if exist {
				impl.EnqueueKey(types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()})
			}
		}
	}
}

func ctrlOpts() func(impl *controller.Impl) controller.Options {
	return func(_ *controller.Impl) controller.Options {
		return controller.Options{
			FinalizerName: path.Join(pipelinesascode.GroupName, pipelinesascode.FinalizerName),
			PromoteFilterFunc: func(obj any) bool {
				_, exist := obj.(*tektonv1.PipelineRun).GetAnnotations()[keys.State]
				return exist
			},
		}
	}
}
