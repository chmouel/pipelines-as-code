package concurrency

import (
	"context"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency"
	pacVersionedClient "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
)

type TestQMI struct {
	QueuedPrs    []string
	RunningQueue []string
}

func (TestQMI) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ pacVersionedClient.Interface) error {
	// TODO implement me
	panic("implement me")
}

func (TestQMI) RemoveRepository(_ *pacv1alpha1.Repository) {
}

func (t TestQMI) QueuedPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return t.QueuedPrs
}

func (TestQMI) RunningPipelineRuns(_ *pacv1alpha1.Repository) []string {
	// TODO implement me
	panic("implement me")
}

func (t TestQMI) AddListToRunningQueue(_ *pacv1alpha1.Repository, _ []string) ([]string, error) {
	return t.RunningQueue, nil
}

func (TestQMI) AddToPendingQueue(_ *pacv1alpha1.Repository, _ []string) error {
	// TODO implement me
	panic("implement me")
}

func (t TestQMI) RemoveFromQueue(_, _ string) bool {
	return false
}

func (TestQMI) RemoveAndTakeItemFromQueue(_ *pacv1alpha1.Repository, _ *tektonv1.PipelineRun) string {
	// TODO implement me
	panic("implement me")
}

// TestConcurrencyManager is a test implementation of the concurrency manager.
type TestConcurrencyManager struct {
	driverType   string
	queueManager concurrency.QueueManager
}

// NewTestConcurrencyManager creates a new test concurrency manager.
func NewTestConcurrencyManager() *TestConcurrencyManager {
	return &TestConcurrencyManager{
		driverType:   "test",
		queueManager: &TestQueueManager{},
	}
}

func (t *TestConcurrencyManager) GetDriverType() string {
	return t.driverType
}

func (t *TestConcurrencyManager) GetQueueManager() concurrency.QueueManager {
	return t.queueManager
}

func (t *TestConcurrencyManager) Close() error {
	return nil
}

// TestQueueManager is a test implementation of the queue manager.
type TestQueueManager struct{}

// AcquireSlot tries to acquire a concurrency slot for a PipelineRun in a repository.
func (t *TestQueueManager) AcquireSlot(_ context.Context, _ *pacv1alpha1.Repository, _ string) (bool, concurrency.LeaseID, error) {
	return true, 1, nil
}

// ReleaseSlot releases a concurrency slot.
func (t *TestQueueManager) ReleaseSlot(_ context.Context, _ concurrency.LeaseID, _, _ string) error {
	return nil
}

// GetCurrentSlots returns the current number of slots in use for a repository.
func (t *TestQueueManager) GetCurrentSlots(_ context.Context, _ *pacv1alpha1.Repository) (int, error) {
	return 0, nil
}

// GetRunningPipelineRuns returns the list of currently running PipelineRuns for a repository.
func (t *TestQueueManager) GetRunningPipelineRuns(_ context.Context, _ *pacv1alpha1.Repository) ([]string, error) {
	return []string{}, nil
}

// WatchSlotAvailability watches for slot availability changes.
func (t *TestQueueManager) WatchSlotAvailability(_ context.Context, _ *pacv1alpha1.Repository, _ func()) {
	// No-op for testing
}

// SetRepositoryState sets the state of a repository.
func (t *TestQueueManager) SetRepositoryState(_ context.Context, _ *pacv1alpha1.Repository, _ string) error {
	return nil
}

// GetRepositoryState gets the state of a repository.
func (t *TestQueueManager) GetRepositoryState(_ context.Context, _ *pacv1alpha1.Repository) (string, error) {
	return "", nil
}

// SetPipelineRunState sets the state of a PipelineRun.
func (t *TestQueueManager) SetPipelineRunState(_ context.Context, _, _ string) error {
	return nil
}

// GetPipelineRunState gets the state of a PipelineRun.
func (t *TestQueueManager) GetPipelineRunState(_ context.Context, _ string) (string, error) {
	return "", nil
}

// CleanupRepository removes all data for a repository.
func (t *TestQueueManager) CleanupRepository(_ context.Context, _ *pacv1alpha1.Repository) error {
	return nil
}

// InitQueues initializes the queue manager.
func (t *TestQueueManager) InitQueues(_ context.Context, _, _ interface{}) error {
	return nil
}

// RemoveRepository removes a repository from the queue manager.
func (t *TestQueueManager) RemoveRepository(_ *pacv1alpha1.Repository) {
	// No-op for testing
}

// QueuedPipelineRuns returns the list of queued PipelineRuns for a repository.
func (t *TestQueueManager) QueuedPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return []string{}
}

// RunningPipelineRuns returns the list of running PipelineRuns for a repository.
func (t *TestQueueManager) RunningPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return []string{}
}

// AddListToRunningQueue adds a list of PipelineRuns to the running queue.
func (t *TestQueueManager) AddListToRunningQueue(_ *pacv1alpha1.Repository, list []string) ([]string, error) {
	return list, nil
}

// AddToPendingQueue adds a list of PipelineRuns to the pending queue.
func (t *TestQueueManager) AddToPendingQueue(_ *pacv1alpha1.Repository, _ []string) error {
	return nil
}

// RemoveFromQueue removes a PipelineRun from the queue.
func (t *TestQueueManager) RemoveFromQueue(_, _ string) bool {
	return true
}

// RemoveAndTakeItemFromQueue removes and takes an item from the queue.
func (t *TestQueueManager) RemoveAndTakeItemFromQueue(_ *pacv1alpha1.Repository, _ interface{}) string {
	return ""
}

// TryAcquireSlot tries to acquire a concurrency slot.
func (t *TestQueueManager) TryAcquireSlot(_ context.Context, _ *pacv1alpha1.Repository, _ string) (bool, concurrency.LeaseID, error) {
	return true, 1, nil
}

// SetupWatcher sets up a watcher for slot availability.
func (t *TestQueueManager) SetupWatcher(_ context.Context, _ *pacv1alpha1.Repository, _ func()) {
	// No-op for testing
}
