package reconciler

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testconcurrency "github.com/openshift-pipelines/pipelines-as-code/pkg/test/concurrency"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestQueuePipelineRun(t *testing.T) {
	tests := []struct {
		name          string
		wantErrString string
		wantLog       string
		pipelineRun   *tektonv1.PipelineRun
		testRepo      *pacv1alpha1.Repository
		globalRepo    *pacv1alpha1.Repository
		runningQueue  []string
	}{
		{
			name: "no existing order annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "no repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
					},
				},
			},
			wantErrString: fmt.Sprintf("no %s annotation found", keys.Repository),
		},
		{
			name: "empty repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "",
					},
				},
			},
			wantErrString: fmt.Sprintf("annotation %s is empty", keys.Repository),
		},
		{
			name: "no repo found",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "foo",
					},
				},
			},
		},
		{
			name: "merging global repository settings",
			globalRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global",
					Namespace: "global",
				},
				Spec: pacv1alpha1.RepositorySpec{
					Settings: &pacv1alpha1.Settings{
						PipelineRunProvenance: "somewhere",
					},
				},
			},
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "Merging global repository settings with local repository settings",
		},
		{
			name:         "no new PR acquired",
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "no new PipelineRun acquired for repo test",
		},
		{
			name:         "failed to get PR from the Q after many iterations",
			runningQueue: []string{"test/test2"},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog:       "failed to get PR",
			wantErrString: "max iterations reached of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, logcatch := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			repos := []*pacv1alpha1.Repository{}
			if tt.testRepo != nil {
				repos = append(repos, tt.testRepo)
			}
			if tt.globalRepo != nil {
				repos = append(repos, tt.globalRepo)
			}
			testData := testclient.Data{
				Repositories: repos,
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					RunningQueue: tt.runningQueue,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube: &info.KubeOpts{
							Namespace: "global",
						},
						Controller: &info.ControllerInfo{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}
			if tt.globalRepo != nil {
				r.run.Info.Controller.GlobalRepository = tt.globalRepo.GetName()
			}
			err := r.queuePipelineRun(ctx, fakelogger, tt.pipelineRun)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)

			if tt.wantLog != "" {
				assert.Assert(t, logcatch.FilterMessage(tt.wantLog).Len() != 0, "We didn't get the expected log message", logcatch.All())
			}
		})
	}
}

func TestRecordQueuePromotionFailure(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	tests := []struct {
		name        string
		annotations map[string]string
		wantRetries string
	}{
		{
			name:        "first failure records retry metadata",
			annotations: map[string]string{},
			wantRetries: "1",
		},
		{
			name: "later failures keep incrementing retries without blocking promotion",
			annotations: map[string]string{
				keys.QueuePromotionRetries: "4",
			},
			wantRetries: "5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			repo := &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					ConcurrencyLimit: func() *int { limit := 2; return &limit }(),
				},
			}
			pipelineRun := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "test",
					Name:        "test-pr",
					Annotations: tt.annotations,
				},
			}
			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pipelineRun},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)

			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
					},
				},
			}

			err := r.recordQueuePromotionFailure(ctx, fakelogger, repo, pipelineRun, errors.New("cannot patch"))
			assert.NilError(t, err)

			updatedPR, err := stdata.Pipeline.TektonV1().PipelineRuns("test").Get(ctx, "test-pr", metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, updatedPR.GetAnnotations()[keys.QueuePromotionRetries], tt.wantRetries)
			assert.Equal(t, updatedPR.GetAnnotations()[keys.QueuePromotionLastErr], "cannot patch")
			assert.Equal(t, updatedPR.GetAnnotations()[keys.QueueDecision], queuepkg.QueueDecisionPromotionFailed)
			assert.Assert(t, strings.Contains(updatedPR.GetAnnotations()[keys.QueueDebugSummary], "lastDecision="+queuepkg.QueueDecisionPromotionFailed))
			_, exists := updatedPR.GetAnnotations()[keys.QueuePromotionBlocked]
			assert.Assert(t, !exists, "QueuePromotionBlocked should not be set when promotion fails")
		})
	}
}

func TestQueuePipelineRunStopsAfterSinglePromotionFailure(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)

	pipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "queued",
			Namespace: "test",
			Annotations: map[string]string{
				keys.ExecutionOrder: "test/queued",
				keys.Repository:     "test",
				keys.State:          kubeinteraction.StateQueued,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: tektonv1.PipelineRunSpecStatusPending,
		},
	}
	laterPipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "later",
			Namespace: "test",
			Annotations: map[string]string{
				keys.ExecutionOrder: "test/later",
				keys.Repository:     "test",
				keys.State:          kubeinteraction.StateQueued,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: tektonv1.PipelineRunSpecStatusPending,
		},
	}
	testRepo := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL: randomURL,
		},
	}

	testData := testclient.Data{
		Repositories: []*pacv1alpha1.Repository{testRepo},
		PipelineRuns: []*tektonv1.PipelineRun{pipelineRun, laterPipelineRun},
	}
	stdata, informers := testclient.SeedTestData(t, ctx, testData)
	patchCalls := 0
	stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		patchCalls++
		if patchCalls == 1 {
			return true, nil, errors.New("boom")
		}
		return false, nil, nil
	})
	removedClaims := []string{}

	r := &Reconciler{
		qm: testconcurrency.TestQMI{
			RunningQueue:          []string{"test/queued", "test/later"},
			RemoveFromQueueResult: true,
			RemovedFromQueue:      &removedClaims,
		},
		repoLister: informers.Repository.Lister(),
		run: &params.Run{
			Info: info.Info{
				Kube: &info.KubeOpts{
					Namespace: "global",
				},
				Controller: &info.ControllerInfo{},
			},
			Clients: clients.Clients{
				PipelineAsCode: stdata.PipelineAsCode,
				Tekton:         stdata.Pipeline,
				Kube:           stdata.Kube,
				Log:            fakelogger,
			},
		},
	}

	err := r.queuePipelineRun(ctx, fakelogger, pipelineRun)
	assert.ErrorContains(t, err, "failed to update pipelineRun to in_progress")

	updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns("test").Get(ctx, "queued", metav1.GetOptions{})
	assert.NilError(t, getErr)
	assert.Equal(t, updatedPR.GetAnnotations()[keys.QueuePromotionRetries], "1")
	assert.Assert(t, updatedPR.GetAnnotations()[keys.QueuePromotionLastErr] != "")
	assert.Equal(t, updatedPR.GetAnnotations()[keys.QueueDecision], queuepkg.QueueDecisionPromotionFailed)
	assert.Assert(t, strings.Contains(updatedPR.GetAnnotations()[keys.QueueDebugSummary], "lastDecision="+queuepkg.QueueDecisionPromotionFailed))
	assert.Assert(t, strings.Contains(updatedPR.GetAnnotations()[keys.QueuePromotionLastErr], "cannot update state"))
	assert.Assert(t, strings.Contains(updatedPR.GetAnnotations()[keys.QueuePromotionLastErr], "boom"))
	_, exists := updatedPR.GetAnnotations()[keys.QueuePromotionBlocked]
	assert.Assert(t, !exists, "QueuePromotionBlocked should not be set when queuePipelineRun returns after a failed promotion")
	assert.DeepEqual(t, removedClaims, []string{"test/queued", "test/later"})
}
