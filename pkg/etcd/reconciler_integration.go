package etcd

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
)

// ReconcilerIntegration provides etcd integration for the reconciler.
type ReconcilerIntegration struct {
	queueManager *QueueManager
	stateManager *StateManager
	logger       *zap.SugaredLogger
	etcdEnabled  bool
}

// NewReconcilerIntegration creates a new reconciler integration with etcd support.
func NewReconcilerIntegration(settings map[string]string, logger *zap.SugaredLogger) (*ReconcilerIntegration, error) {
	etcdEnabled := IsEtcdEnabled(settings)

	if !etcdEnabled {
		// Return a disabled integration
		return &ReconcilerIntegration{
			logger:      logger,
			etcdEnabled: false,
		}, nil
	}

	// Create etcd client
	etcdClient, err := NewClientByMode(settings, logger)
	if err != nil {
		return nil, err
	}

	// Create managers
	queueManager := NewQueueManager(etcdClient, logger)
	stateManager := NewStateManager(etcdClient, logger)

	return &ReconcilerIntegration{
		queueManager: queueManager,
		stateManager: stateManager,
		logger:       logger,
		etcdEnabled:  true,
	}, nil
}

// IsEnabled returns whether etcd integration is enabled.
func (r *ReconcilerIntegration) IsEnabled() bool {
	return r.etcdEnabled
}

// GetQueueManager returns the etcd-based queue manager.
func (r *ReconcilerIntegration) GetQueueManager() sync.QueueManagerInterface {
	if !r.etcdEnabled {
		return nil
	}
	return r.queueManager
}

// GetStateManager returns the etcd-based state manager.
func (r *ReconcilerIntegration) GetStateManager() *StateManager {
	if !r.etcdEnabled {
		return nil
	}
	return r.stateManager
}

// GetPipelineRunState gets the state from etcd, falling back to annotations if etcd is disabled.
func (r *ReconcilerIntegration) GetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun) (string, bool, error) {
	if !r.etcdEnabled {
		// Fall back to annotation-based state
		if annotations := pr.GetAnnotations(); annotations != nil {
			if state, exists := annotations["pipelinesascode.tekton.dev/state"]; exists {
				return state, true, nil
			}
		}
		return "", false, nil
	}

	state, err := r.stateManager.GetPipelineRunState(ctx, pr)
	if err != nil {
		return "", false, err
	}

	return state, state != "", nil
}

// SetPipelineRunState sets the state in etcd or annotations depending on configuration.
func (r *ReconcilerIntegration) SetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun, state string) error {
	if !r.etcdEnabled {
		// This is handled by the existing annotation-based system
		return nil
	}

	return r.stateManager.SetPipelineRunState(ctx, pr, state)
}

// TryAcquireSlot tries to acquire a concurrency slot using etcd.
func (r *ReconcilerIntegration) TryAcquireSlot(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) (bool, clientv3.LeaseID, error) {
	if !r.etcdEnabled {
		return true, 0, nil // No concurrency control when etcd is disabled
	}

	prKey := PipelineRunKey(pr.Namespace, pr.Name)
	return r.queueManager.TryAcquireSlot(ctx, repo, prKey)
}

// ReleaseSlot releases a concurrency slot.
func (r *ReconcilerIntegration) ReleaseSlot(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, leaseID clientv3.LeaseID) error {
	if !r.etcdEnabled {
		return nil
	}

	prKey := PipelineRunKey(pr.Namespace, pr.Name)
	repoKey := fmt.Sprintf("%s/%s", repo.Namespace, repo.Name)

	return r.stateManager.GetConcurrencyManager().ReleaseSlot(ctx, leaseID, prKey, repoKey)
}

// SetupWatcher sets up a watcher for concurrency slot availability.
func (r *ReconcilerIntegration) SetupWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	if !r.etcdEnabled {
		return
	}

	r.queueManager.SetupWatcher(ctx, repo, callback)
}

// ShouldUseConcurrency checks if concurrency control should be applied.
func (r *ReconcilerIntegration) ShouldUseConcurrency(repo *v1alpha1.Repository) bool {
	return repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit > 0
}

// GetRunningPipelineRuns returns currently running PipelineRuns for a repository.
func (r *ReconcilerIntegration) GetRunningPipelineRuns(repo *v1alpha1.Repository) []string {
	if !r.etcdEnabled {
		return []string{}
	}

	return r.queueManager.RunningPipelineRuns(repo)
}

// QueuePipelineRun handles queueing logic for a PipelineRun.
func (r *ReconcilerIntegration) QueuePipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) (bool, error) {
	if !r.etcdEnabled {
		// Use the existing queue system
		return false, nil
	}

	if !r.ShouldUseConcurrency(repo) {
		// No concurrency limit, can proceed immediately
		return true, nil
	}

	// Try to acquire a slot
	acquired, leaseID, err := r.TryAcquireSlot(ctx, repo, pr)
	if err != nil {
		return false, err
	}

	if acquired {
		// Store lease ID for later cleanup
		if leaseID != 0 {
			prKey := PipelineRunKey(pr.Namespace, pr.Name)
			if err := r.stateManager.GetConcurrencyManager().SetPipelineRunLease(ctx, repo, prKey, leaseID); err != nil {
				r.logger.Errorf("failed to store lease ID for %s: %v", prKey, err)
			}
		}

		// Update state to started
		if err := r.SetPipelineRunState(ctx, pr, kubeinteraction.StateStarted); err != nil {
			r.logger.Errorf("failed to set state to started for %s: %v", pr.Name, err)
		}

		return true, nil
	}

	// Slot not available, set to queued
	if err := r.SetPipelineRunState(ctx, pr, kubeinteraction.StateQueued); err != nil {
		r.logger.Errorf("failed to set state to queued for %s: %v", pr.Name, err)
	}

	return false, nil
}

// CompletePipelineRun handles completion logic for a PipelineRun.
func (r *ReconcilerIntegration) CompletePipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, state string) error {
	if !r.etcdEnabled {
		return nil
	}

	// Set final state
	if err := r.SetPipelineRunState(ctx, pr, state); err != nil {
		r.logger.Errorf("failed to set final state for %s: %v", pr.Name, err)
	}

	// Release slot if we have one
	prKey := PipelineRunKey(pr.Namespace, pr.Name)
	leaseID, err := r.stateManager.GetConcurrencyManager().GetPipelineRunLease(ctx, repo, prKey)
	if err != nil {
		r.logger.Errorf("failed to get lease for %s: %v", prKey, err)
		return err
	}

	if leaseID != 0 {
		if err := r.ReleaseSlot(ctx, repo, pr, leaseID); err != nil {
			r.logger.Errorf("failed to release slot for %s: %v", prKey, err)
			return err
		}
	}

	return nil
}

// CleanupPipelineRun removes all etcd state for a PipelineRun.
func (r *ReconcilerIntegration) CleanupPipelineRun(ctx context.Context, pr *tektonv1.PipelineRun) error {
	if !r.etcdEnabled {
		return nil
	}

	return r.stateManager.CleanupPipelineRunState(ctx, pr)
}

// Close closes etcd connections.
func (r *ReconcilerIntegration) Close() error {
	if !r.etcdEnabled {
		return nil
	}

	// Close etcd client connections
	if r.stateManager != nil && r.stateManager.concurrencyManager != nil {
		return r.stateManager.concurrencyManager.client.Close()
	}

	return nil
}
