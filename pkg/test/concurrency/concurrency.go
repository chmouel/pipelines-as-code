package concurrency

import (
	"context"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacVersionedClient "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
)

type TestQMI struct {
	QueuedPrs    []string
	RunningQueue []string
	// Removed records the "repoKey|prKey" pairs passed to RemoveFromQueue, so
	// tests can assert whether a queue slot was released.
	Removed *[]string
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

func (t TestQMI) RemoveFromQueue(repoKey, prKey string) bool {
	if t.Removed != nil {
		*t.Removed = append(*t.Removed, repoKey+"|"+prKey)
	}
	return false
}

func (TestQMI) RemoveAndTakeItemFromQueue(_ *pacv1alpha1.Repository, _ *tektonv1.PipelineRun) string {
	// TODO implement me
	panic("implement me")
}
