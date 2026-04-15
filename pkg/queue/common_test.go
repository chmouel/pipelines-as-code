package queue

import (
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

func TestIsRecoverableQueuedPipelineRun(t *testing.T) {
	tests := []struct {
		name string
		pr   *tektonv1.PipelineRun
		want bool
	}{
		{
			name: "queued pending with execution order",
			pr: newTestPR("queued", time.Unix(1_700_001_000, 0), nil, map[string]string{
				keys.State:          kubeinteraction.StateQueued,
				keys.ExecutionOrder: "test-ns/queued",
			}, tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			}),
			want: true,
		},
		{
			name: "nil pipelineRun",
			pr:   nil,
			want: false,
		},
		{
			name: "non queued state",
			pr: newTestPR("started", time.Unix(1_700_001_000, 0), nil, map[string]string{
				keys.State:          kubeinteraction.StateStarted,
				keys.ExecutionOrder: "test-ns/started",
			}, tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			}),
			want: false,
		},
		{
			name: "missing pending status",
			pr: newTestPR("running", time.Unix(1_700_001_000, 0), nil, map[string]string{
				keys.State:          kubeinteraction.StateQueued,
				keys.ExecutionOrder: "test-ns/running",
			}, tektonv1.PipelineRunSpec{}),
			want: false,
		},
		{
			name: "done pipelineRun",
			pr: &tektonv1.PipelineRun{
				ObjectMeta: newTestPR("done", time.Unix(1_700_001_000, 0), nil, map[string]string{
					keys.State:          kubeinteraction.StateQueued,
					keys.ExecutionOrder: "test-ns/done",
				}, tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				}).ObjectMeta,
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
				Status: tektonv1.PipelineRunStatus{
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{
							Type:   "Succeeded",
							Status: "True",
						}},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, IsRecoverableQueuedPipelineRun(tt.pr), tt.want)
		})
	}
}

func TestHasActiveLeaseQueueClaim(t *testing.T) {
	now := time.Unix(1_700_001_500, 0)
	tests := []struct {
		name string
		pr   *tektonv1.PipelineRun
		want bool
	}{
		{
			name: "active claim within ttl",
			pr: newTestPR("active", now, nil, map[string]string{
				keys.QueueClaimedBy: "watcher-a",
				keys.QueueClaimedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
			}, tektonv1.PipelineRunSpec{}),
			want: true,
		},
		{
			name: "expired claim",
			pr: newTestPR("expired", now, nil, map[string]string{
				keys.QueueClaimedBy: "watcher-a",
				keys.QueueClaimedAt: now.Add(-10 * time.Minute).Format(time.RFC3339Nano),
			}, tektonv1.PipelineRunSpec{}),
			want: false,
		},
		{
			name: "malformed timestamp",
			pr: newTestPR("malformed", now, nil, map[string]string{
				keys.QueueClaimedBy: "watcher-a",
				keys.QueueClaimedAt: "not-a-time",
			}, tektonv1.PipelineRunSpec{}),
			want: false,
		},
		{
			name: "missing claimant",
			pr: newTestPR("missing", now, nil, map[string]string{
				keys.QueueClaimedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
			}, tektonv1.PipelineRunSpec{}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, HasActiveLeaseQueueClaim(tt.pr, now, 5*time.Minute), tt.want)
		})
	}
}

func TestExecutionOrderHelpers(t *testing.T) {
	pr := newTestPR("second", time.Unix(1_700_001_800, 0), nil, map[string]string{
		keys.ExecutionOrder: "test-ns/first,test-ns/second",
	}, tektonv1.PipelineRunSpec{})

	assert.DeepEqual(t, ExecutionOrderList(pr), []string{"test-ns/first", "test-ns/second"})

	index, ok := ExecutionOrderIndex(pr)
	assert.Assert(t, ok)
	assert.Equal(t, index, 1)

	assert.DeepEqual(t, ExecutionOrderList(newTestPR("empty", time.Unix(1_700_001_800, 0), nil, map[string]string{}, tektonv1.PipelineRunSpec{})), []string(nil))

	index, ok = ExecutionOrderIndex(newTestPR("missing", time.Unix(1_700_001_800, 0), nil, map[string]string{
		keys.ExecutionOrder: "test-ns/first",
	}, tektonv1.PipelineRunSpec{}))
	assert.Equal(t, index, 0)
	assert.Assert(t, !ok)
}
