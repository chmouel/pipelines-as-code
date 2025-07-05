package etcd

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// StateManager manages PipelineRun states using etcd instead of annotations.
type StateManager struct {
	concurrencyManager *ConcurrencyManager
	logger             *zap.SugaredLogger
}

// NewStateManager creates a new etcd-based state manager.
func NewStateManager(etcdClient Client, logger *zap.SugaredLogger) *StateManager {
	return &StateManager{
		concurrencyManager: NewConcurrencyManager(etcdClient, logger),
		logger:             logger,
	}
}

// SetPipelineRunState sets the state for a PipelineRun in etcd.
func (sm *StateManager) SetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun, state string) error {
	prKey := PipelineRunKey(pr.Namespace, pr.Name)

	err := sm.concurrencyManager.SetPipelineRunState(ctx, prKey, state)
	if err != nil {
		return fmt.Errorf("failed to set pipeline run state: %w", err)
	}

	sm.logger.Debugf("set state %s for pipeline run %s", state, prKey)
	return nil
}

// GetPipelineRunState gets the state for a PipelineRun from etcd.
func (sm *StateManager) GetPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun) (string, error) {
	prKey := PipelineRunKey(pr.Namespace, pr.Name)

	state, err := sm.concurrencyManager.GetPipelineRunState(ctx, prKey)
	if err != nil {
		return "", fmt.Errorf("failed to get pipeline run state: %w", err)
	}

	if state == "" {
		// Default to started if no state is found
		return kubeinteraction.StateStarted, nil
	}

	return state, nil
}

// UpdatePipelineRunToQueued sets a PipelineRun state to queued.
func (sm *StateManager) UpdatePipelineRunToQueued(ctx context.Context, pr *tektonv1.PipelineRun) error {
	return sm.SetPipelineRunState(ctx, pr, kubeinteraction.StateQueued)
}

// UpdatePipelineRunToStarted sets a PipelineRun state to started.
func (sm *StateManager) UpdatePipelineRunToStarted(ctx context.Context, pr *tektonv1.PipelineRun) error {
	return sm.SetPipelineRunState(ctx, pr, kubeinteraction.StateStarted)
}

// UpdatePipelineRunToCompleted sets a PipelineRun state to completed.
func (sm *StateManager) UpdatePipelineRunToCompleted(ctx context.Context, pr *tektonv1.PipelineRun) error {
	return sm.SetPipelineRunState(ctx, pr, kubeinteraction.StateCompleted)
}

// UpdatePipelineRunToFailed sets a PipelineRun state to failed.
func (sm *StateManager) UpdatePipelineRunToFailed(ctx context.Context, pr *tektonv1.PipelineRun) error {
	return sm.SetPipelineRunState(ctx, pr, kubeinteraction.StateFailed)
}

// IsPipelineRunQueued checks if a PipelineRun is in queued state.
func (sm *StateManager) IsPipelineRunQueued(ctx context.Context, pr *tektonv1.PipelineRun) (bool, error) {
	state, err := sm.GetPipelineRunState(ctx, pr)
	if err != nil {
		return false, err
	}
	return state == kubeinteraction.StateQueued, nil
}

// IsPipelineRunStarted checks if a PipelineRun is in started state.
func (sm *StateManager) IsPipelineRunStarted(ctx context.Context, pr *tektonv1.PipelineRun) (bool, error) {
	state, err := sm.GetPipelineRunState(ctx, pr)
	if err != nil {
		return false, err
	}
	return state == kubeinteraction.StateStarted, nil
}

// IsPipelineRunCompleted checks if a PipelineRun is in completed state.
func (sm *StateManager) IsPipelineRunCompleted(ctx context.Context, pr *tektonv1.PipelineRun) (bool, error) {
	state, err := sm.GetPipelineRunState(ctx, pr)
	if err != nil {
		return false, err
	}
	return state == kubeinteraction.StateCompleted, nil
}

// CleanupPipelineRunState removes all etcd state for a PipelineRun.
func (sm *StateManager) CleanupPipelineRunState(ctx context.Context, pr *tektonv1.PipelineRun) error {
	prKey := PipelineRunKey(pr.Namespace, pr.Name)

	// Remove state
	stateKey := fmt.Sprintf("/pac/concurrency/pr/%s/state", prKey)
	_, err := sm.concurrencyManager.client.Delete(ctx, stateKey)
	if err != nil {
		sm.logger.Errorf("failed to cleanup state for %s: %v", prKey, err)
		return err
	}

	sm.logger.Debugf("cleaned up state for pipeline run %s", prKey)
	return nil
}

// GetConcurrencyManager returns the underlying concurrency manager.
func (sm *StateManager) GetConcurrencyManager() *ConcurrencyManager {
	return sm.concurrencyManager
}
