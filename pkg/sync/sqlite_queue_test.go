package sync

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSQLiteQueueManager_Basic(t *testing.T) {
	// Create temporary DB file
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	// Create queue manager
	mgr, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NoError(t, err)
	defer mgr.Close()

	// Set limit
	err = mgr.SetLimit("test-repo", 2)
	assert.NoError(t, err)

	// Add items to queue
	now := time.Now()
	err = mgr.AddToQueue("test-repo", "item1", now.UnixNano(), now)
	assert.NoError(t, err)
	err = mgr.AddToQueue("test-repo", "item2", now.Add(time.Second).UnixNano(), now.Add(time.Second))
	assert.NoError(t, err)
	err = mgr.AddToQueue("test-repo", "item3", now.Add(2*time.Second).UnixNano(), now.Add(2*time.Second))
	assert.NoError(t, err)

	// Check pending
	pending, err := mgr.GetCurrentPending("test-repo")
	assert.NoError(t, err)
	assert.Len(t, pending, 3)

	// Acquire items
	id1, err := mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "item1", id1)

	id2, err := mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "item2", id2)

	// Should not acquire more (limit reached)
	id3, err := mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "", id3)

	// Check running
	running, err := mgr.GetCurrentRunning("test-repo")
	assert.NoError(t, err)
	assert.Len(t, running, 2)

	// Release one
	err = mgr.Release("test-repo", "item1")
	assert.NoError(t, err)

	// Should be able to acquire again
	id3, err = mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "item3", id3)
}

func TestSQLiteQueueManager_Limit(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test_limit_*.db")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	mgr, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NoError(t, err)
	defer mgr.Close()

	// Set limit
	err = mgr.SetLimit("test-repo", 1)
	assert.NoError(t, err)

	// Verify limit
	limit, err := mgr.GetLimit("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, 1, limit)

	// Update limit
	err = mgr.SetLimit("test-repo", 3)
	assert.NoError(t, err)

	limit, err = mgr.GetLimit("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, 3, limit)
}

func TestSQLiteQueueManager_Remove(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test_remove_*.db")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	mgr, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NoError(t, err)
	defer mgr.Close()

	err = mgr.SetLimit("test-repo", 1)
	assert.NoError(t, err)

	now := time.Now()
	err = mgr.AddToQueue("test-repo", "item1", now.UnixNano(), now)
	assert.NoError(t, err)

	// Remove from queue
	err = mgr.RemoveFromQueue("test-repo", "item1")
	assert.NoError(t, err)

	// Should not be able to acquire
	id, err := mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "", id)
}

func TestSQLiteQueueManager_ResetRunning(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test_reset_*.db")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	mgr, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NoError(t, err)
	defer mgr.Close()

	err = mgr.SetLimit("test-repo", 1)
	assert.NoError(t, err)

	now := time.Now()
	err = mgr.AddToQueue("test-repo", "item1", now.UnixNano(), now)
	assert.NoError(t, err)

	// Acquire
	id, err := mgr.AcquireNext("test-repo")
	assert.NoError(t, err)
	assert.Equal(t, "item1", id)

	// Reset running (simulate recovery)
	err = mgr.ResetRunning("test-repo")
	assert.NoError(t, err)

	// Should be back in pending
	pending, err := mgr.GetCurrentPending("test-repo")
	assert.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Equal(t, "item1", pending[0])

	// Should not be running
	running, err := mgr.GetCurrentRunning("test-repo")
	assert.NoError(t, err)
	assert.Len(t, running, 0)
}

func TestSQLiteQueueManager_StateSync(t *testing.T) {
	tmpfile, err := os.CreateTemp("", "test-*.db")
	assert.NoError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	mgr, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NoError(t, err)
	defer mgr.Close()

	repo := "test-repo"
	prID := "namespace/pipeline-run-1"

	testCases := []struct {
		annotationState string
		expectedSQLite  QueueState
	}{
		{"queued", StatePending},
		{"started", StateRunning},
		{"running", StateRunning},
		{"completed", StateFinished},
		{"failed", StateFinished},
		{"cancelled", StateFinished},
		{"unknown", StatePending},
	}

	var allIDs []string
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("sync_%s", tc.annotationState), func(t *testing.T) {
			testPrID := fmt.Sprintf("%s-%d", prID, i)
			allIDs = append(allIDs, testPrID)
			err := mgr.SyncPipelineRunState(repo, testPrID, tc.annotationState)
			assert.NoError(t, err)

			state, err := mgr.GetPipelineRunState(repo, testPrID)
			assert.NoError(t, err)
			expectedAnnotation := mgr.convertSQLiteStateToAnnotation(string(tc.expectedSQLite))
			assert.Equal(t, state, expectedAnnotation)
		})
	}

	states, err := mgr.GetAllPipelineRunStates(repo)
	assert.NoError(t, err)
	assert.Equal(t, len(states), len(testCases))
	for i, tc := range testCases {
		testPrID := fmt.Sprintf("%s-%d", prID, i)
		expectedAnnotation := mgr.convertSQLiteStateToAnnotation(string(tc.expectedSQLite))
		assert.Equal(t, states[testPrID], expectedAnnotation)
	}
}
