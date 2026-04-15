package queue

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestLeaseManagerClaimsOnlyAvailableCapacity(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_000, 0)
	repo := newTestRepo(1)

	first := newLeaseTestPR("first", now, repo, nil)
	second := newLeaseTestPR("second", now.Add(time.Second), repo, nil)
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{first, second},
	})

	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	claimed, err := manager.AddListToRunningQueue(ctx, repo, nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, claimed, []string{PrKey(first)})

	claimed, err = manager.AddListToRunningQueue(ctx, repo, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(claimed), 0)

	firstUpdated, err := stdata.Pipeline.TektonV1().PipelineRuns(first.Namespace).Get(ctx, first.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, firstUpdated.GetAnnotations()[keys.QueueDecision], QueueDecisionClaimActive)
	assert.Assert(t, firstUpdated.GetAnnotations()[keys.QueueDebugSummary] != "")

	secondUpdated, err := stdata.Pipeline.TektonV1().PipelineRuns(second.Namespace).Get(ctx, second.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, secondUpdated.GetAnnotations()[keys.QueueDecision], QueueDecisionWaitingForSlot)
	assert.Assert(t, secondUpdated.GetAnnotations()[keys.QueueDebugSummary] != "")
}

func TestLeaseManagerReclaimsStaleClaims(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_100, 0)
	repo := newTestRepo(1)

	staleClaim := map[string]string{
		keys.QueueClaimedBy: "old-watcher",
		keys.QueueClaimedAt: now.Add(-2 * defaultLeaseClaimTTL).Format(time.RFC3339Nano),
	}
	first := newLeaseTestPR("first", now, repo, staleClaim)
	second := newLeaseTestPR("second", now.Add(time.Second), repo, nil)
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{first, second},
	})

	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	claimed, err := manager.AddListToRunningQueue(ctx, repo, nil)
	assert.NilError(t, err)
	assert.DeepEqual(t, claimed, []string{PrKey(first)})
}

func TestLeaseManagerPromotesNextAfterCompletion(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_200, 0)
	repo := newTestRepo(1)

	running := newLeaseStartedPR("first", now, repo)
	queued := newLeaseTestPR("second", now.Add(time.Second), repo, nil)
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{running, queued},
	})

	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	next := manager.RemoveAndTakeItemFromQueue(ctx, repo, running)
	assert.Equal(t, next, PrKey(queued))

	updated, err := stdata.Pipeline.TektonV1().PipelineRuns(queued.Namespace).Get(ctx, queued.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updated.GetAnnotations()[keys.QueueClaimedBy], manager.identity)
}

func TestRepoLeaseNameIsDeterministic(t *testing.T) {
	assert.Equal(t, repoLeaseName("ns/repo"), repoLeaseName("ns/repo"))
	assert.Assert(t, repoLeaseName("ns/repo") != repoLeaseName("other/repo"))
}

func TestLeaseManagerPrefersExecutionOrderWithinCurrentGroup(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_250, 0)
	repo := newTestRepo(1)

	first := newLeaseQueuedPRWithOrder("bbb", now.Add(2*time.Second), repo, "test-ns/aaa,test-ns/bbb", nil)
	second := newLeaseQueuedPRWithOrder("aaa", now.Add(3*time.Second), repo, "test-ns/aaa,test-ns/bbb", nil)
	other := newLeaseQueuedPRWithOrder("ccc", now, repo, "test-ns/ccc", nil)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{first, second, other},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	claimed, err := manager.AddListToRunningQueue(ctx, repo, []string{"test-ns/aaa", "test-ns/bbb"})
	assert.NilError(t, err)
	assert.DeepEqual(t, claimed, []string{"test-ns/aaa"})
}

func TestLeaseManagerIgnoresMalformedExecutionOrder(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_275, 0)
	repo := newTestRepo(1)

	malformed := newLeaseQueuedPRWithOrder("aaa", now, repo, "test-ns/zzz", nil)
	valid := newLeaseQueuedPRWithOrder("bbb", now.Add(time.Second), repo, "test-ns/bbb", nil)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{malformed, valid},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	claimed, err := manager.AddListToRunningQueue(ctx, repo, []string{"test-ns/aaa", "test-ns/bbb"})
	assert.NilError(t, err)
	assert.DeepEqual(t, claimed, []string{"test-ns/bbb"})

	updatedMalformed, err := stdata.Pipeline.TektonV1().PipelineRuns(malformed.Namespace).Get(ctx, malformed.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updatedMalformed.GetAnnotations()[keys.QueueDecision], QueueDecisionMissingOrder)
}

func TestLeaseManagerStillConsidersPreviouslyBlockedQueuedRuns(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_290, 0)
	repo := newTestRepo(1)

	blocked := newLeaseQueuedPRWithOrder("aaa", now, repo, "test-ns/aaa", map[string]string{
		keys.QueuePromotionBlocked: "true",
	})
	valid := newLeaseQueuedPRWithOrder("bbb", now.Add(time.Second), repo, "test-ns/bbb", nil)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{blocked, valid},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	claimed, err := manager.AddListToRunningQueue(ctx, repo, []string{"test-ns/aaa", "test-ns/bbb"})
	assert.NilError(t, err)
	assert.DeepEqual(t, claimed, []string{"test-ns/aaa"})
}

func TestLeaseManagerFiltersBySanitizedRepositoryLabel(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_295, 0)
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "org/repo",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(1),
		},
	}
	repositoryLabel := formatting.CleanValueKubernetes(repo.Name)

	matching := newTestPR("matching", now, map[string]string{
		keys.Repository: repositoryLabel,
		keys.State:      kubeinteraction.StateQueued,
	}, map[string]string{
		keys.Repository:     repo.Name,
		keys.State:          kubeinteraction.StateQueued,
		keys.ExecutionOrder: "test-ns/matching",
	}, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})
	collision := newTestPR("collision", now.Add(-time.Second), map[string]string{
		keys.Repository: repositoryLabel,
		keys.State:      kubeinteraction.StateStarted,
	}, map[string]string{
		keys.Repository: "org:repo",
		keys.State:      kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{matching, collision},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	state, err := manager.getRepoQueueState(ctx, repo, nil, "")
	assert.NilError(t, err)
	assert.Equal(t, len(state.running), 0)
	assert.Equal(t, len(state.queued), 1)
	assert.Equal(t, PrKey(&state.queued[0]), PrKey(matching))

	listSelector := ""
	for _, action := range stdata.Pipeline.Actions() {
		if action.GetVerb() != "list" || action.GetResource().Resource != "pipelineruns" {
			continue
		}
		listAction, ok := action.(ktesting.ListAction)
		assert.Assert(t, ok)
		listSelector = listAction.GetListRestrictions().Labels.String()
	}

	assert.Equal(t, listSelector, keys.Repository+"="+repositoryLabel)
}

func TestLeaseManagerReleaseKeepsLeaseAndClearsHolder(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_300, 0)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	leaseName := repoLeaseName("test-ns/test")
	acquired, err := manager.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, acquired)

	manager.releaseLease(leaseName)

	lease, err := stdata.Kube.CoordinationV1().Leases("pac").Get(ctx, leaseName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, lease.Spec.HolderIdentity == nil)
	assert.Assert(t, lease.Spec.RenewTime != nil)
	assert.Equal(t, lease.Spec.RenewTime.Time, now)
}

func TestLeaseManagerReusesLeaseAcrossAcquireCycles(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_400, 0)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	leaseName := repoLeaseName("test-ns/test")
	acquired, err := manager.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, acquired)
	manager.releaseLease(leaseName)

	acquired, err = manager.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, acquired)
	manager.releaseLease(leaseName)

	var createCount, deleteCount int
	for _, action := range stdata.Kube.Actions() {
		if action.GetResource().Resource != "leases" {
			continue
		}
		if action.GetVerb() == "create" {
			createCount++
		}
		if action.GetVerb() == "delete" {
			deleteCount++
		}
	}

	assert.Equal(t, createCount, 1)
	assert.Equal(t, deleteCount, 0)
}

func TestLeaseManagerReleaseIsNoOpForForeignHolder(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_500, 0)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	leaseName := repoLeaseName("test-ns/test")
	foreignHolder := "other-manager"
	_, err := stdata.Kube.CoordinationV1().Leases("pac").Create(ctx, &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: "pac",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &foreignHolder,
			RenewTime:      &metav1.MicroTime{Time: now.Add(-time.Second)},
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)
	stdata.Kube.ClearActions()

	manager.releaseLease(leaseName)

	lease, err := stdata.Kube.CoordinationV1().Leases("pac").Get(ctx, leaseName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, lease.Spec.HolderIdentity != nil)
	assert.Equal(t, *lease.Spec.HolderIdentity, foreignHolder)

	for _, action := range stdata.Kube.Actions() {
		if action.GetResource().Resource == "leases" {
			assert.Assert(t, action.GetVerb() != "update")
		}
	}
}

func TestLeaseManagerContentionAfterInPlaceRelease(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_600, 0)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	first := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	second := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }

	leaseName := repoLeaseName("test-ns/test")
	acquired, err := first.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, acquired)

	acquired, err = second.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, !acquired)

	first.releaseLease(leaseName)

	acquired, err = second.tryAcquireLease(ctx, leaseName)
	assert.NilError(t, err)
	assert.Assert(t, acquired)
}

func TestLeaseManagerWithRepoLeaseCreatesOnlyOneLease(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_700, 0)
	repo := newTestRepo(1)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	err := manager.withRepoLease(context.Background(), repo, func(context.Context) error { return nil })
	assert.NilError(t, err)
	err = manager.withRepoLease(context.Background(), repo, func(context.Context) error { return nil })
	assert.NilError(t, err)

	var createCount, deleteCount, updateCount int
	for _, action := range stdata.Kube.Actions() {
		if action.GetResource().Resource != "leases" {
			continue
		}
		switch action.GetVerb() {
		case "create":
			createCount++
		case "delete":
			deleteCount++
		case "update":
			updateCount++
		}
	}

	assert.Equal(t, createCount, 1)
	assert.Equal(t, deleteCount, 0)
	assert.Assert(t, updateCount >= 3)
}

func TestLeaseManagerReleaseHandlesMissingLease(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")

	manager.releaseLease(repoLeaseName("test-ns/missing"))

	for _, action := range stdata.Kube.Actions() {
		if action.GetResource().Resource == "leases" {
			assert.Assert(t, action.GetVerb() != "update")
		}
	}
}

func newLeaseTestPR(name string, ts time.Time, repo *v1alpha1.Repository, annotations map[string]string) *tektonv1.PipelineRun {
	return newLeaseQueuedPRWithOrder(name, ts, repo, "test-ns/"+name, annotations)
}

func newLeaseQueuedPRWithOrder(name string, ts time.Time, repo *v1alpha1.Repository, order string, annotations map[string]string) *tektonv1.PipelineRun {
	mergedAnnotations := map[string]string{
		keys.Repository: repo.Name,
		keys.State:      kubeinteraction.StateQueued,
	}
	if order != "" {
		mergedAnnotations[keys.ExecutionOrder] = order
	}
	for key, value := range annotations {
		mergedAnnotations[key] = value
	}

	return newTestPR(name, ts, map[string]string{
		keys.Repository: formatting.CleanValueKubernetes(repo.Name),
		keys.State:      kubeinteraction.StateQueued,
	}, mergedAnnotations, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})
}

func newLeaseStartedPR(name string, ts time.Time, repo *v1alpha1.Repository) *tektonv1.PipelineRun {
	return newTestPR(name, ts, map[string]string{
		keys.Repository: formatting.CleanValueKubernetes(repo.Name),
		keys.State:      kubeinteraction.StateStarted,
	}, map[string]string{
		keys.Repository: repo.Name,
		keys.State:      kubeinteraction.StateStarted,
	}, tektonv1.PipelineRunSpec{})
}

func TestIsLeaseExpired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	duration := int32(30)

	tests := []struct {
		name  string
		lease *coordinationv1.Lease
		want  bool
	}{
		{
			name: "nil duration seconds",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: nil,
				},
			},
			want: true,
		},
		{
			name: "renew time set and not expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
					RenewTime:            &metav1.MicroTime{Time: now.Add(-10 * time.Second)},
				},
			},
			want: false,
		},
		{
			name: "renew time set and expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
					RenewTime:            &metav1.MicroTime{Time: now.Add(-60 * time.Second)},
				},
			},
			want: true,
		},
		{
			name: "acquire time only not expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
					AcquireTime:          &metav1.MicroTime{Time: now.Add(-10 * time.Second)},
				},
			},
			want: false,
		},
		{
			name: "acquire time only expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
					AcquireTime:          &metav1.MicroTime{Time: now.Add(-60 * time.Second)},
				},
			},
			want: true,
		},
		{
			name: "both times nil",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
				},
			},
			want: true,
		},
		{
			name: "exact expiry boundary is not expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: &duration,
					RenewTime:            &metav1.MicroTime{Time: now.Add(-30 * time.Second)},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, isLeaseExpired(tt.lease, now), tt.want)
		})
	}
}

func TestCanTakeLease(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	duration := int32(30)
	myIdentity := "my-watcher"
	otherIdentity := "other-watcher"
	empty := ""

	tests := []struct {
		name  string
		lease *coordinationv1.Lease
		want  bool
	}{
		{
			name: "nil holder",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{HolderIdentity: nil},
			},
			want: true,
		},
		{
			name: "empty holder",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{HolderIdentity: &empty},
			},
			want: true,
		},
		{
			name: "same identity",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{HolderIdentity: &myIdentity},
			},
			want: true,
		},
		{
			name: "different identity expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &otherIdentity,
					LeaseDurationSeconds: &duration,
					RenewTime:            &metav1.MicroTime{Time: now.Add(-60 * time.Second)},
				},
			},
			want: true,
		},
		{
			name: "different identity not expired",
			lease: &coordinationv1.Lease{
				Spec: coordinationv1.LeaseSpec{
					HolderIdentity:       &otherIdentity,
					LeaseDurationSeconds: &duration,
					RenewTime:            &metav1.MicroTime{Time: now.Add(-10 * time.Second)},
				},
			},
			want: false,
		},
	}

	manager := &LeaseManager{identity: myIdentity}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, manager.canTakeLease(tt.lease, now), tt.want)
		})
	}
}

func TestClearClaim(t *testing.T) {
	tests := []struct {
		name        string
		prKey       string
		seedPRs     []*tektonv1.PipelineRun
		wantErr     string
		wantPatched bool
	}{
		{
			name:    "invalid key format",
			prKey:   "no-slash",
			wantErr: "invalid pipelinerun key",
		},
		{
			name:  "pipeline run not found",
			prKey: "test-ns/missing",
		},
		{
			name:  "repository mismatch",
			prKey: "test-ns/wrong-repo",
			seedPRs: []*tektonv1.PipelineRun{
				newTestPR("wrong-repo", time.Unix(1_700_000_000, 0), nil, map[string]string{
					keys.Repository: "other-repo",
				}, tektonv1.PipelineRunSpec{}),
			},
		},
		{
			name:  "successful cleanup",
			prKey: "test-ns/claimed",
			seedPRs: []*tektonv1.PipelineRun{
				newTestPR("claimed", time.Unix(1_700_000_000, 0), nil, map[string]string{
					keys.Repository:     "test",
					keys.QueueClaimedBy: "watcher-1",
					keys.QueueClaimedAt: "2025-01-01T00:00:00Z",
				}, tektonv1.PipelineRunSpec{}),
			},
			wantPatched: true,
		},
	}

	repo := newTestRepo(1)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: tt.seedPRs,
			})
			manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")

			err := manager.clearClaim(ctx, repo, tt.prKey)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			if tt.wantPatched {
				patched := false
				for _, action := range stdata.Pipeline.Actions() {
					if action.GetVerb() == "patch" {
						patched = true
					}
				}
				assert.Assert(t, patched)
			}
		})
	}
}

func TestRemoveFromQueueLease(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	repo := newTestRepo(1)

	pr := newTestPR("claimed", time.Unix(1_700_000_000, 0), nil, map[string]string{
		keys.Repository:     repo.Name,
		keys.QueueClaimedBy: "watcher-1",
		keys.QueueClaimedAt: "2025-01-01T00:00:00Z",
	}, tektonv1.PipelineRunSpec{})
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{pr},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")

	assert.Assert(t, manager.RemoveFromQueue(ctx, repo, PrKey(pr)))
	assert.Assert(t, !manager.RemoveFromQueue(ctx, repo, "bad-key"))
}

func TestRemoveRepository(t *testing.T) {
	tests := []struct {
		name      string
		seedLease bool
	}{
		{
			name:      "deletes existing lease",
			seedLease: true,
		},
		{
			name:      "no op when lease missing",
			seedLease: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := newTestRepo(1)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")

			if tt.seedLease {
				leaseName := repoLeaseName(RepoKey(repo))
				_, err := stdata.Kube.CoordinationV1().Leases("pac").Create(ctx, &coordinationv1.Lease{
					ObjectMeta: metav1.ObjectMeta{
						Name:      leaseName,
						Namespace: "pac",
					},
					Spec: coordinationv1.LeaseSpec{
						HolderIdentity: &manager.identity,
					},
				}, metav1.CreateOptions{})
				assert.NilError(t, err)
			}

			manager.RemoveRepository(repo)

			if tt.seedLease {
				deleted := false
				for _, action := range stdata.Kube.Actions() {
					if action.GetResource().Resource == "leases" && action.GetVerb() == "delete" {
						deleted = true
					}
				}
				assert.Assert(t, deleted)
			}
		})
	}
}

func TestQueuedAndRunningPipelineRunsLease(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_000, 0)
	repo := newTestRepo(1)

	running := newLeaseStartedPR("running", now, repo)
	queued := newLeaseTestPR("queued", now.Add(time.Second), repo, nil)
	claimed := newLeaseTestPR("claimed", now.Add(2*time.Second), repo, map[string]string{
		keys.QueueClaimedBy: "watcher-1",
		keys.QueueClaimedAt: now.Format(time.RFC3339Nano),
	})

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*tektonv1.PipelineRun{running, queued, claimed},
	})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	runningKeys := manager.RunningPipelineRuns(repo)
	assert.DeepEqual(t, runningKeys, []string{PrKey(running)})

	queuedKeys := manager.QueuedPipelineRuns(repo)
	assert.Assert(t, len(queuedKeys) >= 1)
}

func TestCloneRepoQueueStateAndCloneStringSet(t *testing.T) {
	t.Run("nil state returns nil", func(t *testing.T) {
		assert.Assert(t, cloneRepoQueueState(nil) == nil)
	})

	t.Run("deep copies populated state", func(t *testing.T) {
		original := &repoQueueState{
			running: []tektonv1.PipelineRun{{ObjectMeta: metav1.ObjectMeta{Name: "r1"}}},
			claimed: []tektonv1.PipelineRun{{ObjectMeta: metav1.ObjectMeta{Name: "c1"}}},
			queued:  []tektonv1.PipelineRun{{ObjectMeta: metav1.ObjectMeta{Name: "q1"}}},
		}
		cloned := cloneRepoQueueState(original)
		assert.Equal(t, len(cloned.running), 1)
		assert.Equal(t, len(cloned.claimed), 1)
		assert.Equal(t, len(cloned.queued), 1)

		original.running = append(original.running, tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "r2"}})
		assert.Equal(t, len(cloned.running), 1)
	})

	t.Run("empty set returns nil", func(t *testing.T) {
		assert.Assert(t, cloneStringSet(nil) == nil)
		assert.Assert(t, cloneStringSet(map[string]struct{}{}) == nil)
	})

	t.Run("deep copies populated set", func(t *testing.T) {
		original := map[string]struct{}{"a": {}, "b": {}}
		cloned := cloneStringSet(original)
		assert.Equal(t, len(cloned), 2)

		original["c"] = struct{}{}
		assert.Equal(t, len(cloned), 2)
	})
}

func TestRepositoryConcurrencyLimit(t *testing.T) {
	tests := []struct {
		name string
		repo *v1alpha1.Repository
		want int
	}{
		{
			name: "nil repo",
			repo: nil,
			want: -1,
		},
		{
			name: "nil concurrency limit",
			repo: &v1alpha1.Repository{},
			want: -1,
		},
		{
			name: "valid limit",
			repo: newTestRepo(5),
			want: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, repositoryConcurrencyLimit(tt.repo), tt.want)
		})
	}
}

func TestAddListAndRemoveNilConcurrencyLimit(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")

	nilLimitRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
	}
	zeroLimitRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "test-ns"},
		Spec:       v1alpha1.RepositorySpec{ConcurrencyLimit: intPtr(0)},
	}

	claimed, err := manager.AddListToRunningQueue(ctx, nilLimitRepo, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(claimed), 0)

	claimed, err = manager.AddListToRunningQueue(ctx, zeroLimitRepo, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(claimed), 0)

	pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "pr", Namespace: "test-ns"}}
	assert.Equal(t, manager.RemoveAndTakeItemFromQueue(ctx, nilLimitRepo, pr), "")
	assert.Equal(t, manager.RemoveAndTakeItemFromQueue(ctx, zeroLimitRepo, pr), "")
}

func TestWithRepoLeaseContextCancellation(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	now := time.Unix(1_700_000_000, 0)
	repo := newTestRepo(1)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	manager := NewLeaseManager(logger, stdata.Kube, stdata.Pipeline, "pac")
	manager.now = func() time.Time { return now }

	foreignHolder := "other-watcher"
	leaseName := repoLeaseName(RepoKey(repo))
	duration := int32(600)
	_, err := stdata.Kube.CoordinationV1().Leases("pac").Create(ctx, &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "pac"},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &foreignHolder,
			LeaseDurationSeconds: &duration,
			RenewTime:            &metav1.MicroTime{Time: now},
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	err = manager.withRepoLease(cancelCtx, repo, func(context.Context) error {
		t.Fatal("callback should not be invoked")
		return nil
	})
	assert.ErrorContains(t, err, "context canceled") //nolint:misspell
}
