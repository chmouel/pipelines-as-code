package etcd

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/etcd/test"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConcurrencyManager(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockClient := test.NewMockClient(logger)
	cm := NewConcurrencyManager(mockClient, logger)
	ctx := context.Background()

	// Create test repository
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-repo",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: &[]int{2}[0], // limit of 2
		},
	}

	t.Run("acquire slots within limit", func(t *testing.T) {
		// First slot should be acquired
		acquired1, lease1, err := cm.AcquireSlot(ctx, repo, "pr1")
		assert.NilError(t, err)
		assert.Equal(t, acquired1, true)
		assert.Assert(t, lease1 != 0)

		// Second slot should be acquired
		acquired2, lease2, err := cm.AcquireSlot(ctx, repo, "pr2")
		assert.NilError(t, err)
		assert.Equal(t, acquired2, true)
		assert.Assert(t, lease2 != 0)

		// Third slot should be rejected (limit reached)
		acquired3, lease3, err := cm.AcquireSlot(ctx, repo, "pr3")
		assert.NilError(t, err)
		assert.Equal(t, acquired3, false)
		assert.Equal(t, lease3, clientv3.LeaseID(0))

		// Release first slot
		err = cm.ReleaseSlot(ctx, lease1, "pr1", "test-ns/test-repo")
		assert.NilError(t, err)

		// Now third slot should be acquirable
		acquired3, lease3, err = cm.AcquireSlot(ctx, repo, "pr3")
		assert.NilError(t, err)
		assert.Equal(t, acquired3, true)
		assert.Assert(t, lease3 != 0)

		// Cleanup
		err = cm.ReleaseSlot(ctx, lease2, "pr2", "test-ns/test-repo")
		assert.NilError(t, err)
		err = cm.ReleaseSlot(ctx, lease3, "pr3", "test-ns/test-repo")
		assert.NilError(t, err)
	})

	t.Run("no concurrency limit", func(t *testing.T) {
		repoNoLimit := &v1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "test-repo-no-limit",
			},
			Spec: v1alpha1.RepositorySpec{
				ConcurrencyLimit: nil,
			},
		}

		// Should always acquire when no limit
		acquired, lease, err := cm.AcquireSlot(ctx, repoNoLimit, "pr1")
		assert.NilError(t, err)
		assert.Equal(t, acquired, true)
		assert.Equal(t, lease, clientv3.LeaseID(0)) // No lease when no limit
	})

	t.Run("get current slots", func(t *testing.T) {
		// Clear mock state to ensure fresh start
		mockClient.Data = make(map[string]string)
		mockClient.Leases = make(map[clientv3.LeaseID]time.Time)
		mockClient.LeaseToKey = make(map[clientv3.LeaseID]string)
		mockClient.LeaseQueue = []clientv3.LeaseID{}

		// Acquire some slots
		_, lease1, err := cm.AcquireSlot(ctx, repo, "pr1")
		assert.NilError(t, err)
		_, lease2, err := cm.AcquireSlot(ctx, repo, "pr2")
		assert.NilError(t, err)

		// Check count
		count, err := cm.GetCurrentSlots(ctx, repo)
		assert.NilError(t, err)
		assert.Equal(t, count, 2)

		// Get running list
		running, err := cm.GetRunningPipelineRuns(ctx, repo)
		assert.NilError(t, err)
		assert.Equal(t, len(running), 2)
		assert.Assert(t, contains(running, "pr1"))
		assert.Assert(t, contains(running, "pr2"))

		// Cleanup
		err = cm.ReleaseSlot(ctx, lease1, "pr1", "test-ns/test-repo")
		assert.NilError(t, err)
		err = cm.ReleaseSlot(ctx, lease2, "pr2", "test-ns/test-repo")
		assert.NilError(t, err)
	})

	t.Run("state management", func(t *testing.T) {
		prKey := "test-ns/test-pr"

		// Set state
		err := cm.SetPipelineRunState(ctx, prKey, "queued")
		assert.NilError(t, err)

		// Get state
		state, err := cm.GetPipelineRunState(ctx, prKey)
		assert.NilError(t, err)
		assert.Equal(t, state, "queued")

		// Update state
		err = cm.SetPipelineRunState(ctx, prKey, "started")
		assert.NilError(t, err)

		state, err = cm.GetPipelineRunState(ctx, prKey)
		assert.NilError(t, err)
		assert.Equal(t, state, "started")
	})
}

func TestQueueManager(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockClient := test.NewMockClient(logger)
	qm := NewQueueManager(mockClient, logger)

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-repo",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: &[]int{1}[0],
		},
	}

	t.Run("add to running queue", func(t *testing.T) {
		prList := []string{"test-ns/pr1", "test-ns/pr2"}

		acquired, err := qm.AddListToRunningQueue(repo, prList)
		assert.NilError(t, err)

		// Only one should be acquired due to limit
		assert.Equal(t, len(acquired), 1)
		assert.Equal(t, acquired[0], "test-ns/pr1")

		// Check running list
		running := qm.RunningPipelineRuns(repo)
		assert.Equal(t, len(running), 1)
		assert.Equal(t, running[0], "test-ns/pr1")

		// Remove from queue
		removed := qm.RemoveFromQueue("test-ns/test-repo", "test-ns/pr1")
		assert.Equal(t, removed, true)

		// Now should be empty
		running = qm.RunningPipelineRuns(repo)
		assert.Equal(t, len(running), 0)
	})
}

func TestStateManager(t *testing.T) {
	logger := zap.NewNop().Sugar()
	mockClient := test.NewMockClient(logger)
	sm := NewStateManager(mockClient, logger)
	ctx := context.Background()

	// Create test PipelineRun
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-pr",
		},
	}

	t.Run("state transitions", func(t *testing.T) {
		// Set to queued
		err := sm.UpdatePipelineRunToQueued(ctx, pr)
		assert.NilError(t, err)

		// Check state
		isQueued, err := sm.IsPipelineRunQueued(ctx, pr)
		assert.NilError(t, err)
		assert.Equal(t, isQueued, true)

		// Transition to started
		err = sm.UpdatePipelineRunToStarted(ctx, pr)
		assert.NilError(t, err)

		// Check state
		isStarted, err := sm.IsPipelineRunStarted(ctx, pr)
		assert.NilError(t, err)
		assert.Equal(t, isStarted, true)

		isQueued, err = sm.IsPipelineRunQueued(ctx, pr)
		assert.NilError(t, err)
		assert.Equal(t, isQueued, false)

		// Transition to completed
		err = sm.UpdatePipelineRunToCompleted(ctx, pr)
		assert.NilError(t, err)

		// Check state
		isCompleted, err := sm.IsPipelineRunCompleted(ctx, pr)
		assert.NilError(t, err)
		assert.Equal(t, isCompleted, true)

		// Cleanup
		err = sm.CleanupPipelineRunState(ctx, pr)
		assert.NilError(t, err)
	})
}

func TestReconcilerIntegration(t *testing.T) {
	logger := zap.NewNop().Sugar()

	// Use mock mode with settings
	settings := map[string]string{
		"etcd-enabled": "true",
		"etcd-mode":    "mock",
	}

	integration, err := NewReconcilerIntegration(settings, logger)
	assert.NilError(t, err)
	assert.Equal(t, integration.IsEnabled(), true)

	ctx := context.Background()

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-repo",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: &[]int{1}[0],
		},
	}

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test-ns",
			Name:      "test-pr",
		},
	}

	t.Run("queue pipeline run", func(t *testing.T) {
		canProceed, err := integration.QueuePipelineRun(ctx, repo, pr)
		assert.NilError(t, err)
		assert.Equal(t, canProceed, true) // First one should proceed

		// Second PipelineRun should be queued
		pr2 := &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "test-pr2",
			},
		}

		canProceed2, err := integration.QueuePipelineRun(ctx, repo, pr2)
		assert.NilError(t, err)
		assert.Equal(t, canProceed2, false) // Should be queued

		// Complete first PipelineRun
		err = integration.CompletePipelineRun(ctx, repo, pr, "completed")
		assert.NilError(t, err)

		// Cleanup
		err = integration.CleanupPipelineRun(ctx, pr)
		assert.NilError(t, err)
		err = integration.CleanupPipelineRun(ctx, pr2)
		assert.NilError(t, err)
	})

	// Cleanup
	err = integration.Close()
	assert.NilError(t, err)
}

// Helper function to check if slice contains element.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
