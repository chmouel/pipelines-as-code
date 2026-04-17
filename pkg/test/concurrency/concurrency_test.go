package concurrency

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestTestQMIHelpers(t *testing.T) {
	removed := []string{}
	qm := TestQMI{
		QueuedPrs:             []string{"test/queued"},
		RunningQueue:          []string{"test/running"},
		RemoveFromQueueResult: true,
		RemovedFromQueue:      &removed,
	}

	assert.NilError(t, qm.InitQueues(context.Background(), nil, nil))
	assert.DeepEqual(t, qm.QueuedPipelineRuns(nil), []string{"test/queued"})
	assert.DeepEqual(t, qm.RunningPipelineRuns(nil), []string{"test/running"})

	running, err := qm.AddListToRunningQueue(context.Background(), nil, nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, running, []string{"test/running"})

	assert.NilError(t, qm.AddToPendingQueue(nil, []string{"test/pending"}))
	assert.Equal(t, qm.RemoveFromQueue(context.Background(), nil, "test/running"), true)
	assert.DeepEqual(t, removed, []string{"test/running"})
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(context.Background(), nil, nil), "")
}
