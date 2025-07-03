package sync

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"knative.dev/pkg/apis"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func skipOnOSX64(t *testing.T) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		t.Skip("Skipping test on OSX arm64")
	}
}

func TestSomeoneElseSetPendingWithNoConcurrencyLimit(t *testing.T) {
	// Skip if we are running on OSX, there is a problem with ordering only happening on arm64
	skipOnOSX64(t)

	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create temporary DB for test
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NilError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	db, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NilError(t, err)
	defer db.Close()

	qm := NewQueueManager(logger, db)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now())
	// set to pending
	pr.Status.Conditions = duckv1.Conditions{
		{
			Type:   apis.ConditionSucceeded,
			Reason: v1beta1.PipelineRunReasonPending.String(),
		},
	}
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
}

func TestAddToPendingQueueDirectly(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create temporary DB for test
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NilError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	db, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NilError(t, err)
	defer db.Close()

	qm := NewQueueManager(logger, db)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now())
	// set to pending
	pr.Status.Conditions = duckv1.Conditions{
		{
			Type:   apis.ConditionSucceeded,
			Reason: v1beta1.PipelineRunReasonPending.String(),
		},
	}
	err = qm.AddToPendingQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)

	pending := qm.QueuedPipelineRuns(repo)
	assert.Equal(t, len(pending), 1)
}

func TestNewQueueManagerForList(t *testing.T) {
	// Skip if we are running on OSX, there is a problem with ordering only happening on arm64
	skipOnOSX64(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create temporary DB for test
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NilError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	db, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NilError(t, err)
	defer db.Close()

	qm := NewQueueManager(logger, db)

	// repository for which pipelineRun are created
	repo := newTestRepo(1)

	// first pipelineRun
	prFirst := newTestPR("first", time.Now())

	// added to queue, as there is only one should start
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(prFirst)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)

	// removing the running from queue
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prFirst), "")

	// adding another 2 pipelineRun, limit is 1 so this will be added to pending queue and
	// then one will be started
	prSecond := newTestPR("second", time.Now().Add(1*time.Second))
	prThird := newTestPR("third", time.Now().Add(7*time.Second))

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
	// as per the list, 2nd must be started
	assert.Equal(t, started[0], PrKey(prSecond))

	// adding 2 more, will be going to pending queue
	prFourth := newTestPR("fourth", time.Now().Add(5*time.Second))
	prFifth := newTestPR("fifth", time.Now().Add(4*time.Second))

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFourth), PrKey(prFifth)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// removing 2nd from queue, which means it should start 3rd
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prSecond), PrKey(prThird))

	// changing the concurrency limit to 2
	repo.Spec.ConcurrencyLimit = intPtr(2)

	prSixth := newTestPR("sixth", time.Now().Add(7*time.Second))
	prSeventh := newTestPR("seventh", time.Now().Add(5*time.Second))
	prEight := newTestPR("eight", time.Now().Add(4*time.Second))

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSixth), PrKey(prSeventh), PrKey(prEight)})
	assert.NilError(t, err)
	// third is running, but limit is changed now, so one more should be moved to running
	assert.Equal(t, len(started), 1)
	assert.Equal(t, started[0], PrKey(prFourth))
}

func TestNewQueueManagerReListing(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create temporary DB for test
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NilError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	db, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NilError(t, err)
	defer db.Close()

	qm := NewQueueManager(logger, db)

	// repository for which pipelineRun are created
	repo := newTestRepo(2)

	prFirst := newTestPR("first", time.Now())
	prSecond := newTestPR("second", time.Now().Add(1*time.Second))
	prThird := newTestPR("third", time.Now().Add(7*time.Second))

	// added to queue, as there is only one should start
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 2)

	// if first is running and other pipelineRuns are reconciling
	// then adding again shouldn't have any effect
	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// again
	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFirst), PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// still there should only one running and 2 in pending
	assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 2)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo)), 1)
	// assert.Equal(t, qm.QueuedPipelineRuns(repo)[0], "test-ns/third")

	// a new request comes
	prFourth := newTestPR("fourth", time.Now())
	prFifth := newTestPR("fifth", time.Now().Add(1*time.Second))
	prSixths := newTestPR("sixth", time.Now().Add(7*time.Second))

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFourth), PrKey(prFifth), PrKey(prSixths)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	assert.Equal(t, len(qm.RunningPipelineRuns(repo)), 2)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo)), 4)
}

func newTestRepo(limit int) *v1alpha1.Repository {
	return &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: intPtr(limit),
		},
	}
}

var intPtr = func(val int) *int { return &val }

func newTestPR(name string, time time.Time) *tektonv1.PipelineRun {
	return &tektonv1.PipelineRun{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "test-ns",
			CreationTimestamp: metav1.Time{Time: time},
		},
		Spec:   tektonv1.PipelineRunSpec{},
		Status: tektonv1.PipelineRunStatus{},
	}
}

func TestQueueManager_InitQueues(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create temporary DB for test
	tmpfile, err := os.CreateTemp("", "test_queue_*.db")
	assert.NilError(t, err)
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	db, err := NewSQLiteQueueManager(tmpfile.Name())
	assert.NilError(t, err)
	defer db.Close()

	repo := newTestRepo(1)

	// Test that InitQueues doesn't crash (it's now a no-op)
	qm := NewQueueManager(logger, db)
	err = qm.InitQueues(ctx, nil, nil)
	assert.NilError(t, err)

	// Test that the queue manager works correctly with the new SQLite system
	pr := newTestPR("test", time.Now())

	// Add to queue
	err = qm.AddToPendingQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)

	// Verify it's in pending
	pending := qm.QueuedPipelineRuns(repo)
	assert.Equal(t, len(pending), 1)
	assert.Equal(t, pending[0], PrKey(pr))
}

func TestFilterPipelineRunByInProgress(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ns := "test-ns"

	// Create a fake Tekton client
	pipelineRuns := []*tektonv1.PipelineRun{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr1",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateQueued,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr2",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateCompleted,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusPending,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pr3",
				Namespace: ns,
				Annotations: map[string]string{
					keys.State: kubeinteraction.StateQueued,
				},
			},
			Spec: tektonv1.PipelineRunSpec{
				Status: tektonv1.PipelineRunSpecStatusCancelled,
			},
		},
	}

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns,
				},
			},
		},
		PipelineRuns: pipelineRuns,
	}

	orderList := []string{}
	for _, pr := range pipelineRuns {
		orderList = append(orderList, fmt.Sprintf("%s/%s", ns, pr.GetName()))
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	// FilterPipelineRunByState is now a no-op that returns the input list
	filtered := FilterPipelineRunByState(ctx, stdata.Pipeline, orderList, tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	expected := []string{"test-ns/pr1", "test-ns/pr2", "test-ns/pr3"}
	assert.DeepEqual(t, filtered, expected)
}
