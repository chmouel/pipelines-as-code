package reconciler

import (
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/concurrency"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var (
	concurrencyLimit = 1
	finalizeTestRepo = &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-app",
			Namespace: "pac-app-pipelines",
		},
		Spec: v1alpha1.RepositorySpec{
			URL:              "https://github.com/sm43/pac-app",
			ConcurrencyLimit: &concurrencyLimit,
		},
	}
)

func getTestPR(name, state string) *tektonv1.PipelineRun {
	var status tektonv1.PipelineRunSpecStatus
	if state == kubeinteraction.StateQueued {
		status = tektonv1.PipelineRunSpecStatusPending
	}
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: finalizeTestRepo.Namespace,
			Annotations: map[string]string{
				keys.State:      state,
				keys.Repository: finalizeTestRepo.Name,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: status,
		},
	}
}

func TestReconciler_FinalizeKind(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	tests := []struct {
		name           string
		pipelinerun    *tektonv1.PipelineRun
		addToQueue     []*tektonv1.PipelineRun
		skipAddingRepo bool
	}{
		{
			name: "completed pipelinerun",
			pipelinerun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateCompleted,
					},
				},
			},
		},
		{
			name:        "queued pipelinerun",
			pipelinerun: getTestPR("pr3", kubeinteraction.StateQueued),
			addToQueue: []*tektonv1.PipelineRun{
				getTestPR("pr1", kubeinteraction.StateQueued),
				getTestPR("pr2", kubeinteraction.StateQueued),
				getTestPR("pr3", kubeinteraction.StateQueued),
			},
		},
		{
			name:        "repo was deleted",
			pipelinerun: getTestPR("pr3", kubeinteraction.StateQueued),
			addToQueue: []*tektonv1.PipelineRun{
				getTestPR("pr1", kubeinteraction.StateStarted),
				getTestPR("pr2", kubeinteraction.StateQueued),
				getTestPR("pr3", kubeinteraction.StateQueued),
			},
			skipAddingRepo: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{finalizeTestRepo},
			}
			if len(tt.addToQueue) != 0 {
				testData.PipelineRuns = tt.addToQueue
			}
			if tt.skipAddingRepo {
				testData.Repositories = []*v1alpha1.Repository{}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			// Create a mock concurrency manager for testing
			mockConfig := &concurrency.DriverConfig{
				Driver: "memory",
				MemoryConfig: &concurrency.MemoryConfig{
					LeaseTTL: 30 * time.Minute,
				},
			}
			mockManager, err := concurrency.NewManager(mockConfig, fakelogger)
			assert.NilError(t, err)

			r := Reconciler{
				repoLister:         informers.Repository.Lister(),
				concurrencyManager: mockManager,
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
					},
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "pac-app-pipelines"},
						Controller: &info.ControllerInfo{GlobalRepository: "pac-app"},
						Pac:        info.NewPacOpts(),
					},
				},
			}

			if len(tt.addToQueue) != 0 && !tt.skipAddingRepo {
				for _, pr := range tt.addToQueue {
					// Don't add the PipelineRun that's being finalized to the queue
					if pr.GetName() == tt.pipelinerun.GetName() {
						continue
					}
					prKey := pr.GetNamespace() + "/" + pr.GetName()
					// Add to pending queue using the new concurrency system
					addErr := r.concurrencyManager.GetQueueManager().AddToPendingQueue(finalizeTestRepo, []string{prKey})
					assert.NilError(t, addErr)
				}
			}
			finalizeErr := r.FinalizeKind(ctx, tt.pipelinerun)
			assert.NilError(t, finalizeErr)

			// if repo was deleted then no queue will be there
			if tt.skipAddingRepo {
				assert.Equal(t, len(r.concurrencyManager.GetQueueManager().RunningPipelineRuns(finalizeTestRepo)), 0)
				assert.Equal(t, len(r.concurrencyManager.GetQueueManager().QueuedPipelineRuns(finalizeTestRepo)), 0)
				return
			}

			// if queue was populated then number of elements in it should
			// be the same as total added (since one was finalized and another promoted)
			if len(tt.addToQueue) != 0 {
				totalInQueue := len(r.concurrencyManager.GetQueueManager().QueuedPipelineRuns(finalizeTestRepo)) + len(r.concurrencyManager.GetQueueManager().RunningPipelineRuns(finalizeTestRepo))
				assert.Equal(t, totalInQueue, len(tt.addToQueue))
			}
		})
	}
}
