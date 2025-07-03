package reconciler

import (
	"context"
	"path"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/injection/informers/pipelinesascode/v1alpha1/repository"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/metrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonPipelineRunInformerv1 "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1/pipelinerun"
	tektonPipelineRunReconcilerv1 "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/system"
)

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

		// Start pac config syncer
		go params.StartConfigSync(ctx, run)

		pipelineRunInformer := tektonPipelineRunInformerv1.Get(ctx)
		metrics, err := metrics.NewRecorder()
		if err != nil {
			log.Fatalf("Failed to create pipeline as code metrics recorder %v", err)
		}

		r := &Reconciler{
			run:               run,
			kinteract:         kinteract,
			pipelineRunLister: pipelineRunInformer.Lister(),
			repoLister:        repository.Get(ctx).Lister(),
			qm:                sync.NewQueueManager(run.Clients.Log),
			metrics:           metrics,
			eventEmitter:      events.NewEventEmitter(run.Clients.Kube, run.Clients.Log),
		}
		impl := tektonPipelineRunReconcilerv1.NewImpl(ctx, r, ctrlOpts())

		if err := r.qm.InitQueues(ctx, run.Clients.Tekton, run.Clients.PipelineAsCode); err != nil {
			log.Fatal("failed to init queues", err)
		}

		// Start periodic queue validation
		go r.startPeriodicQueueValidation(ctx)

		if _, err := pipelineRunInformer.Informer().AddEventHandler(controller.HandleAll(checkStateAndEnqueue(impl))); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register PipelineRun informer event handler: %w", err)
		}

		return impl
	}
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

// startPeriodicQueueValidation starts a goroutine that periodically validates queue consistency
// and optionally repairs issues. This helps detect and fix queue inconsistencies that can
// occur due to controller restarts or partial failures.
func (r *Reconciler) startPeriodicQueueValidation(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute) // Run every 5 minutes
	defer ticker.Stop()

	logger := r.run.Clients.Log

	for {
		select {
		case <-ctx.Done():
			logger.Info("Stopping periodic queue validation")
			return
		case <-ticker.C:
			logger.Debug("Running periodic queue validation")

			// Validate queue consistency
			results, err := r.qm.ValidateQueueConsistency(ctx, r.run.Clients.Tekton, r.run.Clients.PipelineAsCode)
			if err != nil {
				logger.Errorf("Failed to validate queue consistency: %v", err)
				continue
			}

			// Log validation results
			hasErrors := false
			for _, result := range results {
				if !result.IsValid {
					hasErrors = true
					logger.Warnf("Queue validation failed for %s: %v", result.RepositoryKey, result.Errors)
					if len(result.Warnings) > 0 {
						logger.Warnf("Queue warnings for %s: %v", result.RepositoryKey, result.Warnings)
					}
				} else if len(result.Warnings) > 0 {
					logger.Infof("Queue warnings for %s: %v", result.RepositoryKey, result.Warnings)
				}
			}

			// Auto-repair if there are errors (can be made configurable)
			if hasErrors {
				logger.Info("Queue inconsistencies detected, attempting auto-repair")
				if err := r.qm.RepairQueue(ctx, r.run.Clients.Tekton, r.run.Clients.PipelineAsCode); err != nil {
					logger.Errorf("Failed to repair queue: %v", err)
				} else {
					logger.Info("Queue auto-repair completed successfully")
				}
			}
		}
	}
}
