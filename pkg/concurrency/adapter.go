package concurrency

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
)

// QueueManagerAdapter adapts the new concurrency.QueueManager to sync.QueueManagerInterface
type QueueManagerAdapter struct {
	queueManager QueueManager
	logger       *zap.SugaredLogger
}

// NewQueueManagerAdapter creates a new adapter
func NewQueueManagerAdapter(queueManager QueueManager, logger *zap.SugaredLogger) sync.QueueManagerInterface {
	return &QueueManagerAdapter{
		queueManager: queueManager,
		logger:       logger,
	}
}

// InitQueues initializes queues for all repositories
func (a *QueueManagerAdapter) InitQueues(ctx context.Context, tektonClient tektonVersionedClient.Interface, pacClient versioned.Interface) error {
	return a.queueManager.InitQueues(ctx, tektonClient, pacClient)
}

// RemoveRepository cleans up all state for a repository
func (a *QueueManagerAdapter) RemoveRepository(repo *v1alpha1.Repository) {
	a.queueManager.RemoveRepository(repo)
}

// QueuedPipelineRuns returns the list of queued PipelineRuns for a repository
func (a *QueueManagerAdapter) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	return a.queueManager.QueuedPipelineRuns(repo)
}

// RunningPipelineRuns returns the list of running PipelineRuns for a repository
func (a *QueueManagerAdapter) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	return a.queueManager.RunningPipelineRuns(repo)
}

// AddListToRunningQueue attempts to acquire concurrency slots for the provided PipelineRuns
func (a *QueueManagerAdapter) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	return a.queueManager.AddListToRunningQueue(repo, list)
}

// AddToPendingQueue adds PipelineRuns to the pending queue
func (a *QueueManagerAdapter) AddToPendingQueue(repo *v1alpha1.Repository, list []string) error {
	return a.queueManager.AddToPendingQueue(repo, list)
}

// RemoveFromQueue removes a PipelineRun from the queue and releases its slot
func (a *QueueManagerAdapter) RemoveFromQueue(repoKey, prKey string) bool {
	return a.queueManager.RemoveFromQueue(repoKey, prKey)
}

// RemoveAndTakeItemFromQueue removes a PipelineRun and returns the next item to process
func (a *QueueManagerAdapter) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	return a.queueManager.RemoveAndTakeItemFromQueue(repo, run)
}
