package etcd

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// PipelineAsCodeIntegration provides etcd integration for PipelineAsCode controller.
type PipelineAsCodeIntegration struct {
	reconcilerIntegration *ReconcilerIntegration
	logger                *zap.SugaredLogger
}

// NewPipelineAsCodeIntegration creates a new PipelineAsCode integration.
func NewPipelineAsCodeIntegration(settings map[string]string, logger *zap.SugaredLogger) (*PipelineAsCodeIntegration, error) {
	reconcilerIntegration, err := NewReconcilerIntegration(settings, logger)
	if err != nil {
		return nil, err
	}

	return &PipelineAsCodeIntegration{
		reconcilerIntegration: reconcilerIntegration,
		logger:                logger,
	}, nil
}

// IsEnabled returns whether etcd integration is enabled.
func (p *PipelineAsCodeIntegration) IsEnabled() bool {
	return p.reconcilerIntegration.IsEnabled()
}

// PrepareNewPipelineRun prepares a new PipelineRun with appropriate state.
// This replaces the logic where annotations are set in the main controller.
func (p *PipelineAsCodeIntegration) PrepareNewPipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	if !p.IsEnabled() {
		// Use traditional annotation-based approach
		if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit != 0 {
			pr.Spec.Status = tektonv1.PipelineRunSpecStatusPending
			pr.Labels[keys.State] = kubeinteraction.StateQueued
			pr.Annotations[keys.State] = kubeinteraction.StateQueued
		}
		return nil
	}

	// Use etcd-based approach
	if p.reconcilerIntegration.ShouldUseConcurrency(repo) {
		// Try to acquire slot immediately
		acquired, leaseID, err := p.reconcilerIntegration.TryAcquireSlot(ctx, repo, pr)
		if err != nil {
			return fmt.Errorf("failed to try acquire slot: %w", err)
		}

		if acquired {
			// Got a slot, can start immediately
			if err := p.reconcilerIntegration.SetPipelineRunState(ctx, pr, kubeinteraction.StateStarted); err != nil {
				p.logger.Errorf("failed to set state to started: %v", err)
			}

			// Store lease ID for cleanup
			if leaseID != 0 {
				prKey := PipelineRunKey(pr.Namespace, pr.Name)
				if err := p.reconcilerIntegration.GetStateManager().GetConcurrencyManager().SetPipelineRunLease(ctx, repo, prKey, leaseID); err != nil {
					p.logger.Errorf("failed to store lease ID: %v", err)
				}
			}
		} else {
			// No slot available, set as pending
			pr.Spec.Status = tektonv1.PipelineRunSpecStatusPending
			if err := p.reconcilerIntegration.SetPipelineRunState(ctx, pr, kubeinteraction.StateQueued); err != nil {
				p.logger.Errorf("failed to set state to queued: %v", err)
			}
		}
	} else {
		// No concurrency control, start immediately
		if err := p.reconcilerIntegration.SetPipelineRunState(ctx, pr, kubeinteraction.StateStarted); err != nil {
			p.logger.Errorf("failed to set state to started: %v", err)
		}
	}

	return nil
}

// HandlePipelineRunCompletion handles completion of a PipelineRun.
// This handles the case where a PipelineRun already exists and needs to be updated.
func (p *PipelineAsCodeIntegration) HandlePipelineRunCompletion(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, finalState string) error {
	if !p.IsEnabled() {
		return nil // Let traditional system handle it
	}

	return p.reconcilerIntegration.CompletePipelineRun(ctx, repo, pr, finalState)
}

// GetPipelineRunState gets the current state of a PipelineRun.
func (p *PipelineAsCodeIntegration) GetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun) (string, bool, error) {
	return p.reconcilerIntegration.GetPipelineRunState(ctx, pr)
}

// SetPipelineRunState sets the state of a PipelineRun.
func (p *PipelineAsCodeIntegration) SetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun, state string) error {
	return p.reconcilerIntegration.SetPipelineRunState(ctx, pr, state)
}

// UpdateExistingPipelineRun updates an existing PipelineRun when needed.
// This handles the case where a PipelineRun already exists and needs to be updated.
func (p *PipelineAsCodeIntegration) UpdateExistingPipelineRun(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	if !p.IsEnabled() {
		// Use traditional annotation approach
		// This would be handled by existing patch logic in the main controller
		return nil
	}

	// For etcd-based approach, check if we need to queue or start the PipelineRun
	currentState, exists, err := p.GetPipelineRunState(ctx, pr)
	if err != nil {
		return fmt.Errorf("failed to get current state: %w", err)
	}

	if !exists || currentState == "" {
		// No state yet, treat as new PipelineRun
		return p.PrepareNewPipelineRun(ctx, repo, pr)
	}

	// PipelineRun already has a state, check if it needs to be transitioned
	if currentState == kubeinteraction.StateQueued && p.reconcilerIntegration.ShouldUseConcurrency(repo) {
		// Try to acquire slot for queued PipelineRun
		acquired, leaseID, err := p.reconcilerIntegration.TryAcquireSlot(ctx, repo, pr)
		if err != nil {
			return fmt.Errorf("failed to try acquire slot for queued pipeline run: %w", err)
		}

		if acquired {
			// Got a slot, transition to started
			if err := p.SetPipelineRunState(ctx, pr, kubeinteraction.StateStarted); err != nil {
				return fmt.Errorf("failed to transition to started: %w", err)
			}

			// Store lease ID
			if leaseID != 0 {
				prKey := PipelineRunKey(pr.Namespace, pr.Name)
				if err := p.reconcilerIntegration.GetStateManager().GetConcurrencyManager().SetPipelineRunLease(ctx, repo, prKey, leaseID); err != nil {
					p.logger.Errorf("failed to store lease ID: %v", err)
				}
			}
		}
	}

	return nil
}

// GetReconcilerIntegration returns the underlying reconciler integration.
func (p *PipelineAsCodeIntegration) GetReconcilerIntegration() *ReconcilerIntegration {
	return p.reconcilerIntegration
}

// GetRunningPipelineRuns returns currently running PipelineRuns for a repository.
func (p *PipelineAsCodeIntegration) GetRunningPipelineRuns(repo *v1alpha1.Repository) []string {
	return p.reconcilerIntegration.GetRunningPipelineRuns(repo)
}

// SetupWatcher sets up a watcher for slot availability.
func (p *PipelineAsCodeIntegration) SetupWatcher(ctx context.Context, repo *v1alpha1.Repository, callback func()) {
	p.reconcilerIntegration.SetupWatcher(ctx, repo, callback)
}

// GetQueueManager returns the queue manager interface.
func (p *PipelineAsCodeIntegration) GetQueueManager() interface{} {
	return p.reconcilerIntegration.GetQueueManager()
}

// Close closes connections.
func (p *PipelineAsCodeIntegration) Close() error {
	return p.reconcilerIntegration.Close()
}

// CleanupPipelineRun removes all state for a PipelineRun.
func (p *PipelineAsCodeIntegration) CleanupPipelineRun(ctx context.Context, pr *tektonv1.PipelineRun) error {
	return p.reconcilerIntegration.CleanupPipelineRun(ctx, pr)
}

// Helper function to check if we should acquire slot on creation vs reconciliation.
func (p *PipelineAsCodeIntegration) ShouldAcquireSlotOnCreation() bool {
	// With etcd, we can try to acquire slots immediately on creation
	// since we have atomic operations
	return p.IsEnabled()
}
