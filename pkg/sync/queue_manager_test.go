package sync

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"knative.dev/pkg/apis"

	"github.com/jonboulle/clockwork"
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

	qm := NewQueueManager(logger)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
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

	qm := NewQueueManager(logger)
	repo := newTestRepo(1)
	// unset concurrency limit
	repo.Spec.ConcurrencyLimit = nil

	pr := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	// set to pending
	pr.Status.Conditions = duckv1.Conditions{
		{
			Type:   apis.ConditionSucceeded,
			Reason: v1beta1.PipelineRunReasonPending.String(),
		},
	}
	err := qm.AddToPendingQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)

	sema := qm.queues[RepoKey(repo)]
	assert.Equal(t, len(sema.getCurrentPending()), 1)
}

func TestNewQueueManagerForList(t *testing.T) {
	// Skip if we are running on OSX, there is a problem with ordering only happening on arm64
	skipOnOSX64(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewQueueManager(logger)

	// repository for which pipelineRun are created
	repo := newTestRepo(1)

	// first pipelineRun
	prFirst := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})

	// added to queue, as there is only one should start
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(prFirst)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)

	// removing the running from queue
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prFirst), "")

	// adding another 2 pipelineRun, limit is 1 so this will be added to pending queue and
	// then one will be started
	prSecond := newTestPR("second", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prThird := newTestPR("third", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSecond), PrKey(prThird)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
	// as per the list, 2nd must be started
	assert.Equal(t, started[0], PrKey(prSecond))

	// adding 2 more, will be going to pending queue
	prFourth := newTestPR("fourth", time.Now().Add(5*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prFifth := newTestPR("fifth", time.Now().Add(4*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prFourth), PrKey(prFifth)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 0)

	// removing 2nd from queue, which means it should start 3rd
	assert.Equal(t, qm.RemoveAndTakeItemFromQueue(repo, prSecond), PrKey(prThird))

	// changing the concurrency limit to 2
	repo.Spec.ConcurrencyLimit = intPtr(2)

	prSixth := newTestPR("sixth", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prSeventh := newTestPR("seventh", time.Now().Add(5*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prEight := newTestPR("eight", time.Now().Add(4*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo, []string{PrKey(prSixth), PrKey(prSeventh), PrKey(prEight)})
	assert.NilError(t, err)
	// third is running, but limit is changed now, so one more should be moved to running
	assert.Equal(t, len(started), 1)
	assert.Equal(t, started[0], PrKey(prFourth))
}

func TestNewQueueManagerReListing(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewQueueManager(logger)

	// repository for which pipelineRun are created
	repo := newTestRepo(2)

	prFirst := newTestPR("first", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	prSecond := newTestPR("second", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prThird := newTestPR("third", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

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
	prFourth := newTestPR("fourth", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	prFifth := newTestPR("fifth", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	prSixths := newTestPR("sixth", time.Now().Add(7*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

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

func newTestPR(name string, time time.Time, labels, annotations map[string]string, spec tektonv1.PipelineRunSpec) *tektonv1.PipelineRun {
	return &tektonv1.PipelineRun{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "test-ns",
			CreationTimestamp: metav1.Time{Time: time},
			Labels:            labels,
			Annotations:       annotations,
		},
		Spec:   spec,
		Status: tektonv1.PipelineRunStatus{},
	}
}

func TestQueueManager_InitQueues(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	cw := clockwork.NewFakeClock()

	startedLabel := map[string]string{
		keys.State: kubeinteraction.StateStarted,
	}
	queuedLabel := map[string]string{
		keys.State: kubeinteraction.StateQueued,
	}

	repo := newTestRepo(1)

	queuedAnnotations := map[string]string{
		keys.ExecutionOrder: "test-ns/first,test-ns/second,test-ns/third",
		keys.State:          kubeinteraction.StateQueued,
	}
	startedAnnotations := map[string]string{
		keys.ExecutionOrder: "test-ns/first,test-ns/second,test-ns/third",
		keys.State:          kubeinteraction.StateStarted,
	}
	firstPR := newTestPR("first", cw.Now(), startedLabel, startedAnnotations, tektonv1.PipelineRunSpec{})
	secondPR := newTestPR("second", cw.Now().Add(5*time.Second), queuedLabel, queuedAnnotations, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})
	thirdPR := newTestPR("third", cw.Now().Add(3*time.Second), queuedLabel, queuedAnnotations, tektonv1.PipelineRunSpec{
		Status: tektonv1.PipelineRunSpecStatusPending,
	})

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
		PipelineRuns: []*tektonv1.PipelineRun{firstPR, secondPR, thirdPR},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	qm := NewQueueManager(logger)

	err := qm.InitQueues(ctx, stdata.Pipeline, stdata.PipelineAsCode)
	assert.NilError(t, err)

	// queues are built
	sema := qm.queues[RepoKey(repo)]
	assert.Equal(t, len(sema.getCurrentPending()), 2)
	assert.Equal(t, len(sema.getCurrentRunning()), 1)

	// now if first is completed and removed from running queue
	// then second must start as per execution order
	qm.RemoveAndTakeItemFromQueue(repo, firstPR)
	assert.Equal(t, sema.getCurrentRunning()[0], PrKey(secondPR))
	assert.Equal(t, sema.getCurrentPending()[0], PrKey(thirdPR))

	// list current running pipelineRuns for repo
	runs := qm.RunningPipelineRuns(repo)
	assert.Equal(t, len(runs), 1)
	// list current pending pipelineRuns for repo
	runs = qm.QueuedPipelineRuns(repo)
	assert.Equal(t, len(runs), 1)
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
	filtered := FilterPipelineRunByState(ctx, stdata.Pipeline, orderList, tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	expected := []string{"test-ns/pr1"}
	assert.DeepEqual(t, filtered, expected)
}

func TestQueueManagerResetAll(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewQueueManager(logger)

	// Create multiple repositories with queues
	repo1 := newTestRepo(2)
	repo1.Name = "repo1"
	repo2 := newTestRepo(1)
	repo2.Name = "repo2"

	// Add items to repo1
	pr1 := newTestPR("pr1", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	pr2 := newTestPR("pr2", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})
	pr3 := newTestPR("pr3", time.Now().Add(2*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err := qm.AddListToRunningQueue(repo1, []string{PrKey(pr1), PrKey(pr2), PrKey(pr3)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 2) // repo1 has limit 2

	// Add items to repo2
	pr4 := newTestPR("pr4", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	pr5 := newTestPR("pr5", time.Now().Add(1*time.Second), nil, nil, tektonv1.PipelineRunSpec{})

	started, err = qm.AddListToRunningQueue(repo2, []string{PrKey(pr4), PrKey(pr5)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1) // repo2 has limit 1

	// Verify initial state
	assert.Equal(t, len(qm.RunningPipelineRuns(repo1)), 2)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo1)), 1)
	assert.Equal(t, len(qm.RunningPipelineRuns(repo2)), 1)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo2)), 1)

	// Reset all queues
	resetStats := qm.ResetAll()

	// Verify reset statistics
	assert.Equal(t, len(resetStats), 2) // 2 repositories
	repo1Key := RepoKey(repo1)
	repo2Key := RepoKey(repo2)
	assert.Equal(t, resetStats[repo1Key], 3) // 2 running + 1 pending
	assert.Equal(t, resetStats[repo2Key], 2) // 1 running + 1 pending

	// Verify all queues are empty
	assert.Equal(t, len(qm.RunningPipelineRuns(repo1)), 0)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo1)), 0)
	assert.Equal(t, len(qm.RunningPipelineRuns(repo2)), 0)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo2)), 0)

	// Verify we can add new items after reset
	pr6 := newTestPR("pr6", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})
	started, err = qm.AddListToRunningQueue(repo1, []string{PrKey(pr6)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)
	assert.Equal(t, len(qm.RunningPipelineRuns(repo1)), 1)
}

func TestQueueManagerRegistry(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Initially, QueueManager should not be registered
	assert.Equal(t, IsQueueManagerRegistered(), false)
	registeredQM := GetRegisteredQueueManager()
	assert.Equal(t, registeredQM, nil)

	// Create and register a QueueManager
	qm := NewQueueManager(logger)
	RegisterQueueManager(qm)

	// Now QueueManager should be registered
	assert.Equal(t, IsQueueManagerRegistered(), true)
	registeredQM = GetRegisteredQueueManager()
	assert.Assert(t, registeredQM != nil)

	// Test that it's the same instance
	repo := newTestRepo(1)
	pr := newTestPR("test-pr", time.Now(), nil, nil, tektonv1.PipelineRunSpec{})

	// Add something to the original QueueManager
	started, err := qm.AddListToRunningQueue(repo, []string{PrKey(pr)})
	assert.NilError(t, err)
	assert.Equal(t, len(started), 1)

	// Verify it's accessible through the registered instance
	runningPRs := registeredQM.RunningPipelineRuns(repo)
	assert.Equal(t, len(runningPRs), 1)
	assert.Equal(t, runningPRs[0], PrKey(pr))

	// Test ResetAll through registered instance
	resetStats := registeredQM.ResetAll()
	assert.Equal(t, len(resetStats), 1)
	assert.Equal(t, resetStats[RepoKey(repo)], 1)

	// Verify reset worked
	assert.Equal(t, len(registeredQM.RunningPipelineRuns(repo)), 0)

	// Reset the registry for other tests
	RegisterQueueManager(nil)
}

func TestRebuildQueuesForNamespace(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	qm := NewQueueManager(logger)

	// Create test repositories
	repo1 := newTestRepo(2)
	repo1.Name = "repo1"
	repo1.Namespace = "test-ns"

	repo2 := newTestRepo(1)
	repo2.Name = "repo2"
	repo2.Namespace = "test-ns"

	// Create test PipelineRuns
	startedPR1 := newTestPR("started-pr1", time.Now(),
		map[string]string{keys.State: kubeinteraction.StateStarted, keys.Repository: "repo1"},
		map[string]string{keys.State: kubeinteraction.StateStarted, keys.Repository: "repo1", keys.ExecutionOrder: "test-ns/started-pr1"},
		tektonv1.PipelineRunSpec{})
	startedPR1.Namespace = "test-ns"

	queuedPR1 := newTestPR("queued-pr1", time.Now().Add(1*time.Second),
		map[string]string{keys.State: kubeinteraction.StateQueued, keys.Repository: "repo1"},
		map[string]string{keys.State: kubeinteraction.StateQueued, keys.Repository: "repo1", keys.ExecutionOrder: "test-ns/queued-pr1"},
		tektonv1.PipelineRunSpec{Status: tektonv1.PipelineRunSpecStatusPending})
	queuedPR1.Namespace = "test-ns"

	startedPR2 := newTestPR("started-pr2", time.Now(),
		map[string]string{keys.State: kubeinteraction.StateStarted, keys.Repository: "repo2"},
		map[string]string{keys.State: kubeinteraction.StateStarted, keys.Repository: "repo2", keys.ExecutionOrder: "test-ns/started-pr2"},
		tektonv1.PipelineRunSpec{})
	startedPR2.Namespace = "test-ns"

	// Create test data
	testData := testclient.Data{
		Repositories: []*v1alpha1.Repository{repo1, repo2},
		PipelineRuns: []*tektonv1.PipelineRun{startedPR1, queuedPR1, startedPR2},
	}
	stdata, _ := testclient.SeedTestData(t, ctx, testData)

	// Test rebuild functionality
	rebuildStats, err := qm.RebuildQueuesForNamespace(ctx, "test-ns", stdata.Pipeline, stdata.PipelineAsCode)
	assert.NilError(t, err)

	// Verify rebuild statistics
	assert.Equal(t, rebuildStats["namespace"], "test-ns")
	assert.Equal(t, rebuildStats["repositories_processed"], 2)

	rebuiltRepos, ok := rebuildStats["repositories_rebuilt"].(map[string]map[string]int)
	if !ok {
		t.Fatalf("expected repositories_rebuilt to be map[string]map[string]int, got %T", rebuildStats["repositories_rebuilt"])
	}
	assert.Equal(t, len(rebuiltRepos), 2)

	// Check repo1 stats
	repo1Key := RepoKey(repo1)
	repo1Stats := rebuiltRepos[repo1Key]
	assert.Equal(t, repo1Stats["rebuilt_running"], 1) // started-pr1
	assert.Equal(t, repo1Stats["rebuilt_pending"], 1) // queued-pr1

	// Check repo2 stats
	repo2Key := RepoKey(repo2)
	repo2Stats := rebuiltRepos[repo2Key]
	assert.Equal(t, repo2Stats["rebuilt_running"], 1) // started-pr2

	// Verify queue states
	assert.Equal(t, len(qm.RunningPipelineRuns(repo1)), 1)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo1)), 1)
	assert.Equal(t, len(qm.RunningPipelineRuns(repo2)), 1)
	assert.Equal(t, len(qm.QueuedPipelineRuns(repo2)), 0)

	// Verify no errors occurred
	errors, ok := rebuildStats["errors"].([]string)
	if !ok {
		t.Fatalf("expected errors to be []string, got %T", rebuildStats["errors"])
	}
	assert.Equal(t, len(errors), 0)
}
