package sync

import (
	"os"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/stretchr/testify/assert"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSQLiteQueueManager_RemoveAndTakeItemFromQueue(t *testing.T) {
	dbPath := "/tmp/test-remove-queue.db"
	defer os.Remove(dbPath)

	db, err := NewSQLiteQueueManager(dbPath)
	assert.NoError(t, err)
	defer db.Close()

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}
	repoKey := RepoKey(repo)

	// Set up the queue with 3 PipelineRuns
	pr1 := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr1",
			Namespace: "test-namespace",
		},
	}
	pr2 := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr2",
			Namespace: "test-namespace",
		},
	}
	pr3 := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr3",
			Namespace: "test-namespace",
		},
	}

	pr1Key := PrKey(pr1)
	pr2Key := PrKey(pr2)
	pr3Key := PrKey(pr3)

	// Set concurrency limit
	err = db.SetLimit(repoKey, 1)
	assert.NoError(t, err)

	// Add all PRs to queue
	err = db.AddToQueue(repoKey, pr1Key, time.Now().UnixNano(), time.Now())
	assert.NoError(t, err)
	err = db.AddToQueue(repoKey, pr2Key, time.Now().UnixNano(), time.Now())
	assert.NoError(t, err)
	err = db.AddToQueue(repoKey, pr3Key, time.Now().UnixNano(), time.Now())
	assert.NoError(t, err)

	// Set states
	err = db.SyncPipelineRunState(repoKey, pr1Key, kubeinteraction.StateQueued)
	assert.NoError(t, err)
	err = db.SyncPipelineRunState(repoKey, pr2Key, kubeinteraction.StateQueued)
	assert.NoError(t, err)
	err = db.SyncPipelineRunState(repoKey, pr3Key, kubeinteraction.StateQueued)
	assert.NoError(t, err)

	// Acquire first PR (should be pr1)
	acquired, err := db.AcquireNext(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, acquired, pr1Key)

	// Update state to started
	err = db.SyncPipelineRunState(repoKey, pr1Key, kubeinteraction.StateStarted)
	assert.NoError(t, err)

	// Check initial state: 1 running, 2 queued
	running, err := db.GetCurrentRunning(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, len(running), 1)
	assert.Equal(t, running[0], pr1Key)

	pending, err := db.GetCurrentPending(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, len(pending), 2)
	assert.True(t, contains(pending, pr2Key))
	assert.True(t, contains(pending, pr3Key))

	// Now test RemoveAndTakeItemFromQueue by removing pr1
	// This should remove pr1 and promote pr2 to running
	// First release pr1, then remove it, then acquire next
	err = db.Release(repoKey, pr1Key)
	assert.NoError(t, err)
	err = db.RemoveFromQueue(repoKey, pr1Key)
	assert.NoError(t, err)
	next, err := db.AcquireNext(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, next, pr2Key, "Expected pr2 to be promoted to running")

	// Check final state: 1 running (pr2), 1 queued (pr3)
	running, err = db.GetCurrentRunning(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, len(running), 1)
	assert.Equal(t, running[0], pr2Key)

	pending, err = db.GetCurrentPending(repoKey)
	assert.NoError(t, err)
	assert.Equal(t, len(pending), 1)
	assert.Equal(t, pending[0], pr3Key)

	// Verify pr1 is completely removed
	state, err := db.GetPipelineRunState(repoKey, pr1Key)
	assert.Error(t, err, "PR1 should be completely removed and not found")
	assert.Equal(t, state, "")
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
