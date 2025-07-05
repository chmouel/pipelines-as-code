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

// TestConcurrencyManager is a test implementation of the concurrency manager
type TestConcurrencyManager struct {
	driverType   string
	queueManager concurrency.QueueManager
}

// NewTestConcurrencyManager creates a new test concurrency manager
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

// TestQueueManager is a test implementation of the queue manager
type TestQueueManager struct{}

func (t *TestQueueManager) AcquireSlot(ctx context.Context, repo *pacv1alpha1.Repository, pipelineRunName string) (bool, concurrency.LeaseID, error) {
	// Always succeed for testing
	leaseID := "test-lease-1"
	return true, leaseID, nil
}

func (t *TestQueueManager) ReleaseSlot(ctx context.Context, leaseID concurrency.LeaseID, pipelineRunName, repoKey string) error {
	return nil
}

func (t *TestQueueManager) GetCurrentSlots(ctx context.Context, repo *pacv1alpha1.Repository) (int, error) {
	return 0, nil
}

func (t *TestQueueManager) GetRunningPipelineRuns(ctx context.Context, repo *pacv1alpha1.Repository) ([]string, error) {
	return []string{}, nil
}

func (t *TestQueueManager) WatchSlotAvailability(ctx context.Context, repo *pacv1alpha1.Repository, callback func()) {
	// No-op for testing
}

func (t *TestQueueManager) SetRepositoryState(ctx context.Context, repo *pacv1alpha1.Repository, state string) error {
	return nil
}

func (t *TestQueueManager) GetRepositoryState(ctx context.Context, repo *pacv1alpha1.Repository) (string, error) {
	return "", nil
}

func (t *TestQueueManager) SetPipelineRunState(ctx context.Context, pipelineRunKey, state string) error {
	return nil
}

func (t *TestQueueManager) GetPipelineRunState(ctx context.Context, pipelineRunKey string) (string, error) {
	return "", nil
}

func (t *TestQueueManager) CleanupRepository(ctx context.Context, repo *pacv1alpha1.Repository) error {
	return nil
}

func (t *TestQueueManager) InitQueues(ctx context.Context, tektonClient, pacClient interface{}) error {
	return nil
}

func (t *TestQueueManager) RemoveRepository(repo *pacv1alpha1.Repository) {
	// No-op for testing
}

func (t *TestQueueManager) QueuedPipelineRuns(repo *pacv1alpha1.Repository) []string {
	return []string{}
}

func (t *TestQueueManager) RunningPipelineRuns(repo *pacv1alpha1.Repository) []string {
	return []string{}
}

func (t *TestQueueManager) AddListToRunningQueue(repo *pacv1alpha1.Repository, list []string) ([]string, error) {
	return list, nil
}

func (t *TestQueueManager) AddToPendingQueue(repo *pacv1alpha1.Repository, list []string) error {
	return nil
}

func (t *TestQueueManager) RemoveFromQueue(repoKey, prKey string) bool {
	return true
}

func (t *TestQueueManager) RemoveAndTakeItemFromQueue(repo *pacv1alpha1.Repository, run interface{}) string {
	return ""
}

func (t *TestQueueManager) TryAcquireSlot(ctx context.Context, repo *pacv1alpha1.Repository, prKey string) (bool, concurrency.LeaseID, error) {
	return true, "test-lease", nil
}

func (t *TestQueueManager) SetupWatcher(ctx context.Context, repo *pacv1alpha1.Repository, callback func()) {
	// No-op for testing
}
