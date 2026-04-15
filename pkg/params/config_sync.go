package params

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/system"
)

var terminateProcessForConfigChange = func() {
	_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
}

func StartConfigSync(ctx context.Context, run *Run) {
	// init pac config
	_ = run.UpdatePacConfig(ctx)

	informerFactory := informers.NewSharedInformerFactoryWithOptions(run.Clients.Kube, 0,
		informers.WithNamespace(system.Namespace()),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fmt.Sprintf("metadata.name=%s", run.Info.Controller.Configmap)
		}))
	informer := informerFactory.Core().V1().ConfigMaps().Informer()
	_, _ = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(_ any) {
			// nothing to do
		},
		UpdateFunc: func(_, _ any) {
			oldBackend, newBackend, changed, err := updatePacConfigAndDetectBackendChange(ctx, run)
			if err != nil {
				return
			}
			if changed {
				if run.Clients.Log != nil {
					run.Clients.Log.Infof(
						"concurrency-backend changed from %q to %q; restarting process so the queue backend is recreated",
						oldBackend, newBackend,
					)
				}
				terminateProcessForConfigChange()
			}
		},
		DeleteFunc: func(_ any) {
			// nothing to do
		},
	})

	stopCh := make(chan struct{})
	defer close(stopCh)

	// start the informer
	informer.Run(stopCh)

	// Wait for termination signal to stop the informer
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}

func updatePacConfigAndDetectBackendChange(ctx context.Context, run *Run) (string, string, bool, error) {
	oldBackend := run.Info.GetPacOpts().ConcurrencyBackend
	if err := run.UpdatePacConfig(ctx); err != nil {
		return oldBackend, oldBackend, false, err
	}

	newBackend := run.Info.GetPacOpts().ConcurrencyBackend
	return oldBackend, newBackend, oldBackend != "" && newBackend != "" && oldBackend != newBackend, nil
}
