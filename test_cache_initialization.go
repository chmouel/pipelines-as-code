package main

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"go.uber.org/zap"
)

func main() {
	logger := zap.NewNop().Sugar()

	// Create RemoteTasks using the new constructor
	rt := matcher.NewRemoteTasks(&params.Run{}, &info.Event{}, nil, logger)

	// Test cache functionality
	ctx := context.Background()

	// Check if we can use the cache methods without nil pointer issues
	task, found := rt.GetCachedTask(ctx, "test-task")
	fmt.Printf("Cache search for task: found=%v, task=%v\n", found, task)

	pipeline, found := rt.GetCachedPipeline(ctx, "test-pipeline")
	fmt.Printf("Cache search for pipeline: found=%v, pipeline=%v\n", found, pipeline)

	fmt.Println("✅ Cache is properly initialized and accessible!")
}
