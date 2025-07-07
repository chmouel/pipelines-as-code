package concurrency

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestQueueManager_InitQueues_StateBased(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	sugar := logger.Sugar()

	// Create a memory driver for testing
	config := &DriverConfig{
		Driver: "memory",
		MemoryConfig: &MemoryConfig{
			LeaseTTL: 30 * time.Minute,
		},
	}

	driver, err := NewDriver(config, sugar)
	assert.NilError(t, err)

	queueManager, err := NewQueueManager(driver, sugar)
	assert.NilError(t, err)

	// Create test repositories
	repo1 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo-1",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
		},
	}

	repo2 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo-2",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: func() *int { limit := 1; return &limit }(),
		},
	}

	// Pre-populate the driver with some state
	ctx := context.Background()

	// Add some queued PipelineRuns to repo1
	err = driver.SetPipelineRunState(ctx, "test-namespace/pr1-queued-1", "queued", repo1)
	assert.NilError(t, err)
	err = driver.SetPipelineRunState(ctx, "test-namespace/pr1-queued-2", "queued", repo1)
	assert.NilError(t, err)

	// Add some running PipelineRuns to repo1
	_, _, err = driver.AcquireSlot(ctx, repo1, "test-namespace/pr1-running-1")
	assert.NilError(t, err)
	_, _, err = driver.AcquireSlot(ctx, repo1, "test-namespace/pr1-running-2")
	assert.NilError(t, err)

	// Add a queued PipelineRun to repo2
	err = driver.SetPipelineRunState(ctx, "test-namespace/pr2-queued-1", "queued", repo2)
	assert.NilError(t, err)

	// Mock PAC client that returns our test repositories
	mockPACClient := &mockPACClient{
		repos: []*v1alpha1.Repository{repo1, repo2},
	}

	// Initialize queues
	err = queueManager.InitQueues(ctx, nil, mockPACClient)
	assert.NilError(t, err)

	// Verify that the queues were properly initialized
	// Note: We can't type assert to *queueManager as it's not exported
	// Instead, we'll test the functionality through the interface

	// Check repo1 queue
	queuedPRs := queueManager.QueuedPipelineRuns(repo1)
	assert.Equal(t, len(queuedPRs), 2, "Expected 2 queued PipelineRuns for repo1")

	// Check that the queued PipelineRuns are in the queue
	foundQueued1 := false
	foundQueued2 := false
	for _, pr := range queuedPRs {
		if pr == "test-namespace/pr1-queued-1" {
			foundQueued1 = true
		}
		if pr == "test-namespace/pr1-queued-2" {
			foundQueued2 = true
		}
	}
	assert.Assert(t, foundQueued1, "Expected to find pr1-queued-1 in queue")
	assert.Assert(t, foundQueued2, "Expected to find pr1-queued-2 in queue")

	// Check repo2 queue
	queuedPRs2 := queueManager.QueuedPipelineRuns(repo2)
	assert.Equal(t, len(queuedPRs2), 1, "Expected 1 queued PipelineRun for repo2")
	assert.Equal(t, queuedPRs2[0], "test-namespace/pr2-queued-1")

	// Verify running PipelineRuns are tracked
	runningPRs := queueManager.RunningPipelineRuns(repo1)
	assert.Equal(t, len(runningPRs), 2, "Expected 2 running PipelineRuns for repo1")

	// Test that we can acquire slots for queued PipelineRuns
	acquired, err := queueManager.AddListToRunningQueue(repo1, []string{"test-namespace/pr1-queued-1"})
	assert.NilError(t, err)
	assert.Equal(t, len(acquired), 0, "Should not acquire more slots when at limit")

	// Release a slot and try again
	success := queueManager.RemoveFromQueue("test-namespace/test-repo-1", "test-namespace/pr1-running-1")
	assert.Assert(t, success, "Should successfully remove from queue")

	// Now we should be able to acquire a slot
	acquired, err = queueManager.AddListToRunningQueue(repo1, []string{"test-namespace/pr1-queued-1"})
	assert.NilError(t, err)
	assert.Equal(t, len(acquired), 1, "Should acquire 1 slot after releasing")
	assert.Equal(t, acquired[0], "test-namespace/pr1-queued-1")
}

// Mock PAC client for testing
type mockPACClient struct {
	repos []*v1alpha1.Repository
}

func (m *mockPACClient) PipelinesascodeV1alpha1() interface {
	Repositories(namespace string) interface {
		List(ctx context.Context, opts interface{}) (interface{}, error)
	}
} {
	return &mockRepositoriesAPI{repos: m.repos}
}

type mockRepositoriesAPI struct {
	repos []*v1alpha1.Repository
}

func (m *mockRepositoriesAPI) Repositories(namespace string) interface {
	List(ctx context.Context, opts interface{}) (interface{}, error)
} {
	return &mockRepositoryLister{repos: m.repos}
}

type mockRepositoryLister struct {
	repos []*v1alpha1.Repository
}

func (m *mockRepositoryLister) List(ctx context.Context, opts interface{}) (interface{}, error) {
	return &mockRepositoryList{repos: m.repos}, nil
}

type mockRepositoryList struct {
	repos []*v1alpha1.Repository
}

func (m *mockRepositoryList) Items() []interface{} {
	items := make([]interface{}, len(m.repos))
	for i, repo := range m.repos {
		items[i] = repo
	}
	return items
}
