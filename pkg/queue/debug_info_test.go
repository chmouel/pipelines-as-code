package queue

import (
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestDebugSnapshotSummary(t *testing.T) {
	summary := DebugSnapshot{
		Backend:      "lease",
		RepoKey:      "ns/repo",
		Position:     2,
		Running:      1,
		Claimed:      1,
		Queued:       3,
		Limit:        2,
		ClaimedBy:    "watcher-a",
		ClaimAge:     5 * time.Second,
		LastDecision: QueueDecisionClaimActive,
	}.Summary()

	for _, fragment := range []string{
		"backend=lease",
		"repo=ns/repo",
		"position=2",
		"running=1",
		"claimed=1",
		"queued=3",
		"limit=2",
		"claimedBy=watcher-a",
		"claimAge=5s",
		"lastDecision=claim_active",
	} {
		assert.Assert(t, strings.Contains(summary, fragment), "missing fragment %q in %q", fragment, summary)
	}
}

func TestSyncQueueDebugAnnotationsSkipsUnchangedPatch(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo",
			Namespace: "test-ns",
		},
	}
	pipelineRun := newLeaseQueuedPRWithOrder("first", time.Unix(1_700_002_000, 0), repo, "test-ns/first", map[string]string{
		keys.QueueDecision:     QueueDecisionWaitingForSlot,
		keys.QueueDebugSummary: DebugSnapshot{RepoKey: "test-ns/repo", LastDecision: QueueDecisionWaitingForSlot}.Summary(),
	})
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pipelineRun},
	})

	err := SyncQueueDebugAnnotations(ctx, logger, stdata.Pipeline, pipelineRun, DebugSnapshot{
		RepoKey:      "test-ns/repo",
		LastDecision: QueueDecisionWaitingForSlot,
	})
	assert.NilError(t, err)

	patchCount := 0
	for _, action := range stdata.Pipeline.Actions() {
		if action.GetVerb() == "patch" && action.GetResource().Resource == "pipelineruns" {
			patchCount++
		}
	}
	assert.Equal(t, patchCount, 0)
}

func TestSyncQueueDebugAnnotationsSkipsLiveNonQueuedPipelineRun(t *testing.T) {
	tests := []struct {
		name   string
		livePR *tektonv1.PipelineRun
	}{
		{
			name: "latest started",
			livePR: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateStarted,
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
			},
		},
		{
			name: "latest completed",
			livePR: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateCompleted,
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{
							Type:   "Succeeded",
							Status: "True",
						}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "repo",
					Namespace: "test-ns",
				},
			}
			staleQueued := newLeaseQueuedPRWithOrder("first", time.Unix(1_700_002_100, 0), repo, "test-ns/first", nil)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{tt.livePR},
			})

			err := SyncQueueDebugAnnotations(ctx, logger, stdata.Pipeline, staleQueued, DebugSnapshot{
				RepoKey:      "test-ns/repo",
				LastDecision: QueueDecisionClaimActive,
			})
			assert.NilError(t, err)

			updated, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(ctx, "first", metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, updated.GetAnnotations()[keys.QueueDecision], "")
			assert.Equal(t, updated.GetAnnotations()[keys.QueueDebugSummary], "")

			patchCount := 0
			for _, action := range stdata.Pipeline.Actions() {
				if action.GetVerb() == "patch" && action.GetResource().Resource == "pipelineruns" {
					patchCount++
				}
			}
			assert.Equal(t, patchCount, 0)
		})
	}
}

func TestSyncQueueDebugAnnotationsClearsStaleAnnotationsOnAdvancedPipelineRun(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo",
			Namespace: "test-ns",
		},
	}
	staleQueued := newLeaseQueuedPRWithOrder("first", time.Unix(1_700_002_200, 0), repo, "test-ns/first", nil)
	liveCompleted := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "first",
			Namespace: "test-ns",
			Annotations: map[string]string{
				keys.State:             kubeinteraction.StateCompleted,
				keys.QueueDecision:     QueueDecisionClaimActive,
				keys.QueueDebugSummary: "backend=lease repo=test-ns/repo",
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{{
					Type:   "Succeeded",
					Status: "True",
				}},
			},
		},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{liveCompleted},
	})

	err := SyncQueueDebugAnnotations(ctx, logger, stdata.Pipeline, staleQueued, DebugSnapshot{
		RepoKey:      "test-ns/repo",
		LastDecision: QueueDecisionWaitingForSlot,
	})
	assert.NilError(t, err)

	updated, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(ctx, "first", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updated.GetAnnotations()[keys.QueueDecision], "")
	assert.Equal(t, updated.GetAnnotations()[keys.QueueDebugSummary], "")

	patchCount := 0
	for _, action := range stdata.Pipeline.Actions() {
		if action.GetVerb() == "patch" && action.GetResource().Resource == "pipelineruns" {
			patchCount++
		}
	}
	assert.Equal(t, patchCount, 1)
}
