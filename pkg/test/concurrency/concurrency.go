package concurrency

import (
	"context"
	"time"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacVersionedClient "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
)

type TestQMI struct {
	QueuedPrs             []string
	RunningQueue          []string
	RemoveFromQueueResult bool
	RemovedFromQueue      *[]string
}

func (TestQMI) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ pacVersionedClient.Interface) error {
	return nil
}

func (TestQMI) RecoveryInterval() time.Duration {
	return 0
}

func (TestQMI) RemoveRepository(_ *pacv1alpha1.Repository) {
}

func (t TestQMI) QueuedPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return t.QueuedPrs
}

func (t TestQMI) RunningPipelineRuns(_ *pacv1alpha1.Repository) []string {
	return t.RunningQueue
}

func (t TestQMI) AddListToRunningQueue(_ context.Context, _ *pacv1alpha1.Repository, _ []string) ([]string, error) {
	return t.RunningQueue, nil
}

func (TestQMI) AddToPendingQueue(_ *pacv1alpha1.Repository, _ []string) error {
	return nil
}

func (t TestQMI) RemoveFromQueue(_ context.Context, _ *pacv1alpha1.Repository, prKey string) bool {
	if t.RemovedFromQueue != nil {
		*t.RemovedFromQueue = append(*t.RemovedFromQueue, prKey)
	}
	return t.RemoveFromQueueResult
}

func (TestQMI) RemoveAndTakeItemFromQueue(_ context.Context, _ *pacv1alpha1.Repository, _ *tektonv1.PipelineRun) string {
	return ""
}
