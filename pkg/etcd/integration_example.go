package etcd

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// IntegrationFactory creates etcd-based components when enabled.
type IntegrationFactory struct {
	logger *zap.SugaredLogger
}

// NewIntegrationFactory creates a new integration factory.
func NewIntegrationFactory(logger *zap.SugaredLogger) *IntegrationFactory {
	return &IntegrationFactory{
		logger: logger,
	}
}

// CreateQueueManager creates an appropriate queue manager based on configuration.
func (f *IntegrationFactory) CreateQueueManager(settings map[string]string) (sync.QueueManagerInterface, error) {
	if IsEtcdEnabled(settings) {
		f.logger.Info("creating etcd-based queue manager")
		etcdClient, err := NewClientByMode(settings, f.logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create etcd client: %w", err)
		}
		return NewQueueManager(etcdClient, f.logger), nil
	}

	f.logger.Info("etcd disabled, using traditional queue manager")
	// Return nil to indicate traditional system should be used
	return nil, nil
}

// CreateReconcilerIntegration creates reconciler integration.
func (f *IntegrationFactory) CreateReconcilerIntegration(settings map[string]string) (*ReconcilerIntegration, error) {
	return NewReconcilerIntegration(settings, f.logger)
}

// CreatePipelineAsCodeIntegration creates PipelineAsCode integration.
func (f *IntegrationFactory) CreatePipelineAsCodeIntegration(settings map[string]string) (*PipelineAsCodeIntegration, error) {
	return NewPipelineAsCodeIntegration(settings, f.logger)
}

// IntegratedReconciler wraps the existing reconciler with etcd support.
// Original reconciler fields would be embedded here.
type IntegratedReconciler struct {
	run         *params.Run
	logger      *zap.SugaredLogger
	integration *ReconcilerIntegration
	etcdEnabled bool
}

// NewIntegratedReconciler creates a reconciler with optional etcd support.
func NewIntegratedReconciler(run *params.Run, logger *zap.SugaredLogger) (*IntegratedReconciler, error) {
	// Get settings from the run
	pacSettings := run.Info.GetPacOpts()
	settingsMap := settings.ConvertPacStructToConfigMap(&pacSettings.Settings)

	integration, err := NewReconcilerIntegration(settingsMap, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create reconciler integration: %w", err)
	}

	return &IntegratedReconciler{
		run:         run,
		logger:      logger,
		integration: integration,
		etcdEnabled: integration.IsEnabled(),
	}, nil
}

// GetPipelineRunState gets state using etcd or annotations.
func (r *IntegratedReconciler) GetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun) (string, bool, error) {
	if r.etcdEnabled {
		return r.integration.GetPipelineRunState(ctx, pr)
	}

	// Fallback to annotations
	if annotations := pr.GetAnnotations(); annotations != nil {
		if state, exists := annotations["pipelinesascode.tekton.dev/state"]; exists {
			return state, true, nil
		}
	}
	return "", false, nil
}

// QueuePipelineRun handles queueing with etcd or traditional system.
func (r *IntegratedReconciler) QueuePipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) (bool, error) {
	if r.etcdEnabled {
		return r.integration.QueuePipelineRun(ctx, repo, pr)
	}

	// Traditional system would handle this
	return false, nil
}

// CompletePipelineRun handles completion with proper cleanup.
func (r *IntegratedReconciler) CompletePipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, finalState string) error {
	if r.etcdEnabled {
		return r.integration.CompletePipelineRun(ctx, repo, pr, finalState)
	}

	// Traditional system handles this
	return nil
}

// Close cleans up resources.
func (r *IntegratedReconciler) Close() error {
	if r.integration != nil {
		return r.integration.Close()
	}
	return nil
}

// Example usage in reconciler code:

// func NewReconciler(...) *Reconciler {
//     integrationFactory := etcd.NewIntegrationFactory(logger)
//
//     // Try to create etcd-based queue manager
//     queueManager, err := integrationFactory.CreateQueueManager()
//     if err != nil {
//         logger.Errorf("failed to create etcd queue manager, falling back: %v", err)
//         queueManager = sync.NewQueueManager(logger) // traditional
//     } else if queueManager == nil {
//         queueManager = sync.NewQueueManager(logger) // traditional
//     }
//
//     // Create reconciler integration
//     reconcilerIntegration, err := integrationFactory.CreateReconcilerIntegration()
//     if err != nil {
//         logger.Errorf("failed to create reconciler integration: %v", err)
//     }
//
//     return &Reconciler{
//         // ... other fields
//         qm: queueManager,
//         etcdIntegration: reconcilerIntegration,
//     }
// }

// Example usage in PipelineAsCode controller:

// func (p *PacRun) CreatePipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
//     // Create etcd integration
//     pacIntegration, err := etcd.NewPipelineAsCodeIntegration(p.logger)
//     if err != nil {
//         p.logger.Errorf("failed to create etcd integration: %v", err)
//         // Fall back to traditional approach
//     }
//
//     if pacIntegration != nil && pacIntegration.IsEnabled() {
//         // Use etcd-based approach
//         if err := pacIntegration.PrepareNewPipelineRun(ctx, repo, pr); err != nil {
//             return fmt.Errorf("failed to prepare pipeline run with etcd: %w", err)
//         }
//     } else {
//         // Traditional annotation-based approach
//         if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit != 0 {
//             pr.Spec.Status = tektonv1.PipelineRunSpecStatusPending
//             pr.Labels[keys.State] = kubeinteraction.StateQueued
//             pr.Annotations[keys.State] = kubeinteraction.StateQueued
//         }
//     }
//
//     // Create the PipelineRun
//     _, err = p.run.Clients.Tekton.TektonV1().PipelineRuns(repo.GetNamespace()).Create(ctx, pr, metav1.CreateOptions{})
//     return err
// }
