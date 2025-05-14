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

// Test cross-run caching functionality
func TestCrossRunCaching(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()

	// Create RemoteTasks for testing
	rt := matcher.NewRemoteTasks(&params.Run{}, &info.Event{}, nil, logger)

	// Test data structures to simulate in-memory maps
	fetchedTasks := map[string]*tektonv1.Task{}
	fetchedPipelines := map[string]*tektonv1.Pipeline{}

	// Test alreadyFetchedTaskWithCache with empty cache initially
	task, found := alreadyFetchedTaskWithCache(ctx, rt, fetchedTasks, "test-task")
	assert.Assert(t, !found, "Should not find task in empty cache")
	assert.Assert(t, task == nil, "Task should be nil when not found")

	// Add a task to in-memory map
	testTask := &tektonv1.Task{
		ObjectMeta: metav1.ObjectMeta{Name: "test-task"},
	}
	fetchedTasks["test-task"] = testTask

	// Test alreadyFetchedTaskWithCache with in-memory task
	task, found = alreadyFetchedTaskWithCache(ctx, rt, fetchedTasks, "test-task")
	assert.Assert(t, found, "Should find task in in-memory map")
	assert.Equal(t, task.Name, "test-task", "Should return correct task")

	// Test alreadyFetchedPipelineWithCache with empty cache initially
	pipeline, found := alreadyFetchedPipelineWithCache(ctx, rt, fetchedPipelines, "test-pipeline")
	assert.Assert(t, !found, "Should not find pipeline in empty cache")
	assert.Assert(t, pipeline == nil, "Pipeline should be nil when not found")

	// Add a pipeline to in-memory map
	testPipeline := &tektonv1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pipeline"},
	}
	fetchedPipelines["test-pipeline"] = testPipeline

	// Test alreadyFetchedPipelineWithCache with in-memory pipeline
	pipeline, found = alreadyFetchedPipelineWithCache(ctx, rt, fetchedPipelines, "test-pipeline")
	assert.Assert(t, found, "Should find pipeline in in-memory map")
	assert.Equal(t, pipeline.Name, "test-pipeline", "Should return correct pipeline")
}

// Test that the original alreadyFetchedResource function still works for generic cases
func TestAlreadyFetchedResourceGeneric(t *testing.T) {
	// Test with tasks
	tasks := map[string]*tektonv1.Task{
		"task1": {ObjectMeta: metav1.ObjectMeta{Name: "task1"}},
	}

	assert.Assert(t, alreadyFetchedResource(tasks, "task1"), "Should find existing task")
	assert.Assert(t, !alreadyFetchedResource(tasks, "task2"), "Should not find non-existing task")

	// Test with pipelines
	pipelines := map[string]*tektonv1.Pipeline{
		"pipeline1": {ObjectMeta: metav1.ObjectMeta{Name: "pipeline1"}},
	}

	assert.Assert(t, alreadyFetchedResource(pipelines, "pipeline1"), "Should find existing pipeline")
	assert.Assert(t, !alreadyFetchedResource(pipelines, "pipeline2"), "Should not find non-existing pipeline")
}
