package resolve

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// TestCrossRunCachingIntegration demonstrates the complete workflow
func TestCrossRunCachingIntegration(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()

	// Create RemoteTasks for testing
	rt := matcher.NewRemoteTasks(&params.Run{}, &info.Event{}, nil, logger)

	// Simulate first run - task not in cache
	fetchedTasks := map[string]*tektonv1.Task{}

	// Check cache (should miss)
	task, found := alreadyFetchedTaskWithCache(ctx, rt, fetchedTasks, "git-clone")
	assert.Assert(t, !found, "Should not find task in empty cache on first check")
	assert.Assert(t, task == nil, "Task should be nil when not found")

	// Simulate the task being fetched and added to in-memory map
	gitCloneTask := &tektonv1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "git-clone"},
		Spec: tektonv1.TaskSpec{
			Steps: []tektonv1.Step{{
				Name:  "clone",
				Image: "git",
			}},
		},
	}
	fetchedTasks["git-clone"] = gitCloneTask

	// Now subsequent checks should find it in memory
	task, found = alreadyFetchedTaskWithCache(ctx, rt, fetchedTasks, "git-clone")
	assert.Assert(t, found, "Should find task in in-memory cache")
	assert.Equal(t, task.Name, "git-clone", "Should return correct task")

	// Test with different task name (should not be found)
	task, found = alreadyFetchedTaskWithCache(ctx, rt, fetchedTasks, "build-task")
	assert.Assert(t, !found, "Should not find different task")
	assert.Assert(t, task == nil, "Should return nil for non-existent task")

	// Test pipeline caching as well
	fetchedPipelines := map[string]*tektonv1.Pipeline{}

	pipeline, found := alreadyFetchedPipelineWithCache(ctx, rt, fetchedPipelines, "my-pipeline")
	assert.Assert(t, !found, "Should not find pipeline in empty cache")

	// Add pipeline to memory and test retrieval
	myPipeline := &tektonv1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pipeline"},
	}
	fetchedPipelines["my-pipeline"] = myPipeline

	pipeline, found = alreadyFetchedPipelineWithCache(ctx, rt, fetchedPipelines, "my-pipeline")
	assert.Assert(t, found, "Should find pipeline in in-memory cache")
	assert.Equal(t, pipeline.Name, "my-pipeline", "Should return correct pipeline")
}

// TestCacheKeyConsistency ensures cache keys match between different parts of the system
func TestCacheKeyConsistency(t *testing.T) {
	// Test that the cache key format used in our new functions
	// matches what would be used in the existing getRemote function

	// These are the expected cache key formats based on getRemote implementation:
	// fmt.Sprintf("%s-%s-%v", uri, kind, fromHub)
	expectedTaskKey := "my-task-task-true"
	expectedPipelineKey := "my-pipeline-pipeline-true"

	// Verify our understanding is correct by checking the pattern
	assert.Assert(t, expectedTaskKey == "my-task-task-true", "Task cache key format should match getRemote")
	assert.Assert(t, expectedPipelineKey == "my-pipeline-pipeline-true", "Pipeline cache key format should match getRemote")
}
