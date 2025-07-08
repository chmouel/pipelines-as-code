package reconciler

import (
	"context"
	"path"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/injection/informers/pipelinesascode/v1alpha1/repository"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/metrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
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

		// Perform initial config sync before creating concurrency manager
		log.Info("Performing initial config sync...")
		if err := run.UpdatePacConfig(ctx); err != nil {
			log.Warnf("Initial config sync failed, will retry: %v", err)
			// Retry a few times with exponential backoff
			for i := 0; i < 3; i++ {
				time.Sleep(time.Duration(1<<i) * time.Second)
				if err := run.UpdatePacConfig(ctx); err == nil {
					log.Info("Initial config sync completed successfully")
					break
				}
				log.Warnf("Config sync retry %d failed: %v", i+1, err)
			}
		} else {
			log.Info("Initial config sync completed successfully")
		}

		// Start pac config syncer for ongoing updates
		go params.StartConfigSync(ctx, run)

		pipelineRunInformer := tektonPipelineRunInformerv1.Get(ctx)
		metrics, err := metrics.NewRecorder()
		if err != nil {
			log.Fatalf("Failed to create pipeline as code metrics recorder %v", err)
		}

		// Initialize the concurrency system with settings from configmap
		pacSettings := run.Info.GetPacOpts()
		settingsMap := settings.ConvertPacStructToConfigMap(&pacSettings.Settings)
		concurrencyManager, err := concurrency.CreateManagerFromSettings(settingsMap, run.Clients.Log)
		if err != nil {
			log.Fatalf("Failed to initialize concurrency system: %v", err)
		}
		log.Infof("Initialized concurrency system with driver: %s", concurrencyManager.GetDriverType())

		r := &Reconciler{
			run:                run,
			kinteract:          kinteract,
			pipelineRunLister:  pipelineRunInformer.Lister(),
			repoLister:         repository.Get(ctx).Lister(),
			metrics:            metrics,
			eventEmitter:       events.NewEventEmitter(run.Clients.Kube, run.Clients.Log),
			concurrencyManager: concurrencyManager,
			activeLeases:       make(map[string]concurrency.LeaseID),
		}
		impl := tektonPipelineRunReconcilerv1.NewImpl(ctx, r, ctrlOpts())

		if err := concurrencyManager.GetQueueManager().InitQueues(ctx, run.Clients.Tekton, run.Clients.PipelineAsCode); err != nil {
			log.Fatal("failed to init queues", err)
		}

		// Set up watchers for all repositories to enable event-driven processing
		go func() {
			// Get all repositories with concurrency state from the driver
			driver := concurrencyManager.GetDriver()
			repos, err := driver.GetAllRepositoriesWithState(ctx)
			if err != nil {
				log.Errorf("Failed to get repositories for watchers: %v", err)
				return
			}

			for _, repo := range repos {
				// Set up watcher for each repository
				concurrencyManager.GetQueueManager().SetupWatcher(ctx, repo, func() {
					// Trigger processing of queued PipelineRuns when slots become available
					r.processQueuedPipelineRuns(ctx, repo)
				})
				log.Infof("Set up watcher for repository %s/%s", repo.Namespace, repo.Name)
			}
			log.Infof("Set up watchers for %d repositories", len(repos))
		}()

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
