package reconciler

import (
	"context"
	"maps"
	"path"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"knative.dev/pkg/controller"
	rtesting "knative.dev/pkg/reconciler/testing"
)

type fakeReconciler struct{}

func (r *fakeReconciler) Reconcile(_ context.Context, _ string) error {
	return nil
}

func TestCheckStateAndEnqueue(t *testing.T) {
	observer, catcher := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	// set debug level
	wh := &fakeReconciler{}
	// Create a new controller implementation.
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "ValidationWebhook",
		Logger:        logger.Named("ValidationWebhook"),
	})

	// Create a new PipelineRun object with the "started" state label.
	testPR := tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
		tektontest.MakeChildStatusReference("first"),
		tektontest.MakeChildStatusReference("last"),
		tektontest.MakeChildStatusReference("middle"),
	}, nil)
	testPR.SetAnnotations(map[string]string{
		keys.State: "started",
	})

	// Call the checkStateAndEnqueue function with the PipelineRun object.
	checkStateAndEnqueue(impl)(testPR)
	assert.Equal(t, impl.Name, "ValidationWebhook")
	assert.Equal(t, impl.Concurrency, 2)
	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue namespace/force-me").Len(), 1)
}

func TestCtrlOpts(t *testing.T) {
	observer, _ := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	// Create a new controller implementation.
	wh := &fakeReconciler{}
	// Create a new controller implementation.
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "ValidationWebhook",
		Logger:        logger.Named("ValidationWebhook"),
	})
	// Call the ctrlOpts function to get the controller options.
	opts := ctrlOpts()(impl)

	// Assert that the finalizer name is set correctly.
	assert.Equal(t, path.Join(pipelinesascode.GroupName, pipelinesascode.FinalizerName), opts.FinalizerName)

	// Create a new PipelineRun object with the "started" state label.
	pr := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pipeline-run",
			Namespace:   "test-namespace",
			Annotations: map[string]string{keys.State: "started"},
		},
	}

	// Call the promote filter function with the PipelineRun object.
	promote := opts.PromoteFilterFunc(pr)

	// Assert that the promote filter function returns true.
	assert.Assert(t, promote)
}

func TestSelectLeaseQueueRecoveryKeys(t *testing.T) {
	now := time.Unix(1_700_001_000, 0)

	tests := []struct {
		name         string
		pipelineRuns []*pipelinev1.PipelineRun
		want         []string
	}{
		{
			name: "selects one oldest queued pending run per repository",
			pipelineRuns: []*pipelinev1.PipelineRun{
				newLeaseRecoveryPR("later", "test-ns", "repo-a", now.Add(2*time.Second), map[string]string{}, false),
				newLeaseRecoveryPR("earlier", "test-ns", "repo-a", now.Add(time.Second), map[string]string{}, false),
				newLeaseRecoveryPR("missing-order", "test-ns", "repo-b", now, map[string]string{
					keys.ExecutionOrder: "",
				}, false),
				newLeaseRecoveryPR("malformed-order", "test-ns", "repo-c", now.Add(250*time.Millisecond), map[string]string{
					keys.ExecutionOrder: "test-ns/some-other-pr",
				}, false),
				newLeaseRecoveryPR("valid", "test-ns", "repo-b", now.Add(3*time.Second), map[string]string{}, false),
				newLeaseRecoveryPR("valid-after-malformed", "test-ns", "repo-c", now.Add(4*time.Second), map[string]string{}, false),
				newLeaseRecoveryPR("other-namespace", "other-ns", "repo-a", now.Add(4*time.Second), map[string]string{}, false),
				newLeaseRecoveryPR("waiting-behind-started", "test-ns", "repo-e", now.Add(4*time.Second), map[string]string{}, false),
				newLeaseRecoveryStartedPR("running", "repo-e", now.Add(5*time.Second)),
				newLeaseRecoveryPR("actively-claimed", "test-ns", "repo-f", now.Add(6*time.Second), map[string]string{
					keys.QueueClaimedBy: "watcher-1",
					keys.QueueClaimedAt: now.Format(time.RFC3339Nano),
				}, false),
				newLeaseRecoveryPR("missing-repo", "test-ns", "", now.Add(5*time.Second), map[string]string{}, false),
				newLeaseRecoveryStartedPR("started", "repo-d", now.Add(6*time.Second)),
				newLeaseRecoveryPR("done", "test-ns", "repo-d", now.Add(7*time.Second), map[string]string{}, true),
			},
			want: []string{
				"test-ns/earlier",
				"test-ns/valid",
				"other-ns/other-namespace",
				"test-ns/valid-after-malformed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recoveryKeys := selectLeaseQueueRecoveryKeysAt(tt.pipelineRuns, now, queuepkg.DefaultLeaseClaimTTL())
			got := make([]string, 0, len(recoveryKeys))
			for _, key := range recoveryKeys {
				got = append(got, key.String())
			}
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestRunLeaseQueueRecovery(t *testing.T) {
	observer, catcher := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	wh := &fakeReconciler{}
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "LeaseRecovery",
		Logger:        logger.Named("LeaseRecovery"),
	})

	now := time.Unix(1_700_001_100, 0)
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, pipelineRun := range []*pipelinev1.PipelineRun{
		newLeaseRecoveryPR("first", "test-ns", "repo-a", now, map[string]string{}, false),
		newLeaseRecoveryPR("second", "test-ns", "repo-a", now.Add(time.Second), map[string]string{}, false),
		newLeaseRecoveryPR("third", "test-ns", "repo-b", now.Add(2*time.Second), map[string]string{}, false),
		newLeaseRecoveryStartedPR("started", "repo-c", now.Add(3*time.Second)),
	} {
		assert.NilError(t, indexer.Add(pipelineRun))
		_, err := stdata.Pipeline.TektonV1().PipelineRuns(pipelineRun.Namespace).Create(ctx, pipelineRun, metav1.CreateOptions{})
		assert.NilError(t, err)
	}

	emitter := events.NewEventEmitter(stdata.Kube, logger)
	runLeaseQueueRecovery(ctx, logger, impl, tektonv1lister.NewPipelineRunLister(indexer), stdata.Pipeline, emitter)

	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue test-ns/first").Len(), 1)
	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue test-ns/third").Len(), 1)
	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue").Len(), 2)

	first, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(ctx, "first", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, first.GetAnnotations()[keys.QueueDecision], "recovery_requeued")

	events, err := stdata.Kube.CoreV1().Events("test-ns").List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(events.Items) >= 1)
	recoveryEvents := 0
	for _, event := range events.Items {
		if event.Reason == "QueueRecoveryRequeued" {
			recoveryEvents++
			assert.Equal(t, event.Type, corev1.EventTypeNormal)
		}
	}
	assert.Assert(t, recoveryEvents >= 1)
}

func TestRunLeaseQueueRecoverySkipsStaleAdvancedLivePipelineRun(t *testing.T) {
	tests := []struct {
		name   string
		livePR *pipelinev1.PipelineRun
	}{
		{
			name: "latest started",
			livePR: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.Repository: "repo-a",
						keys.State:      kubeinteraction.StateStarted,
					},
				},
				Spec: pipelinev1.PipelineRunSpec{
					Status: pipelinev1.PipelineRunSpecStatusPending,
				},
			},
		},
		{
			name: "latest completed",
			livePR: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.Repository: "repo-a",
						keys.State:      kubeinteraction.StateCompleted,
					},
				},
				Status: pipelinev1.PipelineRunStatus{
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
			observer, catcher := zapobserver.New(zap.DebugLevel)
			logger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*pipelinev1.PipelineRun{tt.livePR},
			})
			wh := &fakeReconciler{}
			impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
				WorkQueueName: "LeaseRecovery",
				Logger:        logger.Named("LeaseRecovery"),
			})

			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			staleQueued := newLeaseRecoveryPR("first", "test-ns", "repo-a", time.Unix(1_700_001_200, 0), map[string]string{}, false)
			assert.NilError(t, indexer.Add(staleQueued))

			emitter := events.NewEventEmitter(stdata.Kube, logger)
			runLeaseQueueRecovery(ctx, logger, impl, tektonv1lister.NewPipelineRunLister(indexer), stdata.Pipeline, emitter)

			assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue test-ns/first").Len(), 0)
			assert.Assert(t, catcher.FilterMessageSnippet("skipping stale lease recovery candidate test-ns/first").Len() >= 1)

			updated, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(ctx, "first", metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, updated.GetAnnotations()[keys.QueueDecision], "")

			events, err := stdata.Kube.CoreV1().Events("test-ns").List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)
			recoveryEvents := 0
			for _, event := range events.Items {
				if event.Reason == "QueueRecoveryRequeued" {
					recoveryEvents++
				}
			}
			assert.Equal(t, recoveryEvents, 0)
		})
	}
}

func TestRunLeaseQueueRecoverySkipsHealthyQueuedRuns(t *testing.T) {
	observer, catcher := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)
	now := time.Unix(1_700_001_300, 0)
	claimedAt := time.Now().UTC().Format(time.RFC3339Nano)

	started := newLeaseRecoveryStartedPR("running", "repo-a", now)
	waiting := newLeaseRecoveryPR("waiting", "test-ns", "repo-a", now.Add(time.Second), map[string]string{}, false)
	claimed := newLeaseRecoveryPR("claimed", "test-ns", "repo-b", now.Add(2*time.Second), map[string]string{
		keys.QueueClaimedBy: "watcher-1",
		keys.QueueClaimedAt: claimedAt,
	}, false)

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		PipelineRuns: []*pipelinev1.PipelineRun{started, waiting, claimed},
	})
	wh := &fakeReconciler{}
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "LeaseRecovery",
		Logger:        logger.Named("LeaseRecovery"),
	})

	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, pipelineRun := range []*pipelinev1.PipelineRun{started, waiting, claimed} {
		assert.NilError(t, indexer.Add(pipelineRun))
	}

	emitter := events.NewEventEmitter(stdata.Kube, logger)
	runLeaseQueueRecovery(ctx, logger, impl, tektonv1lister.NewPipelineRunLister(indexer), stdata.Pipeline, emitter)

	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue").Len(), 0)

	for _, name := range []string{"waiting", "claimed"} {
		updated, err := stdata.Pipeline.TektonV1().PipelineRuns("test-ns").Get(ctx, name, metav1.GetOptions{})
		assert.NilError(t, err)
		assert.Equal(t, updated.GetAnnotations()[keys.QueueDecision], "")
	}

	events, err := stdata.Kube.CoreV1().Events("test-ns").List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	recoveryEvents := 0
	for _, event := range events.Items {
		if event.Reason == "QueueRecoveryRequeued" {
			recoveryEvents++
		}
	}
	assert.Equal(t, recoveryEvents, 0)
}

func selectLeaseQueueRecoveryKeysAt(
	pipelineRuns []*pipelinev1.PipelineRun,
	now time.Time,
	claimTTL time.Duration,
) []types.NamespacedName {
	selected := selectLeaseQueueRecoveryCandidatesAt(pipelineRuns, now, claimTTL)

	recoveryKeys := make([]types.NamespacedName, 0, len(selected))
	for _, pipelineRun := range selected {
		recoveryKeys = append(recoveryKeys, types.NamespacedName{
			Namespace: pipelineRun.GetNamespace(),
			Name:      pipelineRun.GetName(),
		})
	}
	return recoveryKeys
}

func newLeaseRecoveryPR(
	name, namespace, repo string,
	createdAt time.Time,
	extraAnnotations map[string]string,
	done bool,
) *pipelinev1.PipelineRun {
	annotations := map[string]string{
		keys.State:          kubeinteraction.StateQueued,
		keys.ExecutionOrder: namespace + "/" + name,
	}
	if repo != "" {
		annotations[keys.Repository] = repo
	}
	maps.Copy(annotations, extraAnnotations)

	pipelineRun := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.Time{Time: createdAt},
			Annotations:       annotations,
		},
		Spec: pipelinev1.PipelineRunSpec{
			Status: pipelinev1.PipelineRunSpecStatusPending,
		},
	}
	if done {
		pipelineRun.Status.Conditions = duckv1.Conditions{{
			Type:   "Succeeded",
			Status: "True",
		}}
	}
	return pipelineRun
}

func newLeaseRecoveryStartedPR(name, repo string, createdAt time.Time) *pipelinev1.PipelineRun {
	return &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "test-ns",
			CreationTimestamp: metav1.Time{Time: createdAt},
			Annotations: map[string]string{
				keys.Repository: repo,
				keys.State:      kubeinteraction.StateStarted,
			},
		},
		Spec: pipelinev1.PipelineRunSpec{
			Status: pipelinev1.PipelineRunSpecStatusPending,
		},
	}
}

func TestHasHealthyLeaseQueueProgress(t *testing.T) {
	now := time.Unix(1_700_001_000, 0)
	claimTTL := 5 * time.Minute

	tests := []struct {
		name string
		pr   *pipelinev1.PipelineRun
		want bool
	}{
		{
			name: "nil pipeline run",
			pr:   nil,
			want: false,
		},
		{
			name: "started not done not cancelled",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "running",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateStarted},
				},
			},
			want: true,
		},
		{
			name: "started and done",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "done",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateStarted},
				},
				Status: pipelinev1.PipelineRunStatus{
					Status: duckv1.Status{
						Conditions: duckv1.Conditions{{Type: "Succeeded", Status: "True"}},
					},
				},
			},
			want: false,
		},
		{
			name: "started and cancelled",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "cancelled",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateStarted},
				},
				Spec: pipelinev1.PipelineRunSpec{
					Status: pipelinev1.PipelineRunSpecStatusCancelled,
				},
			},
			want: false,
		},
		{
			name: "queued with active claim",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "claimed",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State:          kubeinteraction.StateQueued,
						keys.QueueClaimedBy: "watcher-1",
						keys.QueueClaimedAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
					},
				},
			},
			want: true,
		},
		{
			name: "queued with expired claim",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "expired",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State:          kubeinteraction.StateQueued,
						keys.QueueClaimedBy: "watcher-1",
						keys.QueueClaimedAt: now.Add(-10 * time.Minute).Format(time.RFC3339Nano),
					},
				},
			},
			want: false,
		},
		{
			name: "queued with no claim",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "unclaimed",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateQueued},
				},
			},
			want: false,
		},
		{
			name: "unknown state",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "completed",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateCompleted},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, hasHealthyLeaseQueueProgress(tt.pr, now, claimTTL), tt.want)
		})
	}
}

func TestIsEligibleLeaseQueueRecoveryCandidate(t *testing.T) {
	tests := []struct {
		name string
		pr   *pipelinev1.PipelineRun
		want bool
	}{
		{
			name: "nil pipeline run",
			pr:   nil,
			want: false,
		},
		{
			name: "empty repository annotation",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "no-repo",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.State: kubeinteraction.StateQueued},
				},
			},
			want: false,
		},
		{
			name: "eligible queued pending run",
			pr:   newLeaseRecoveryPR("eligible", "test-ns", "repo-a", time.Unix(1_700_001_000, 0), map[string]string{}, false),
			want: true,
		},
		{
			name: "started state is not eligible",
			pr:   newLeaseRecoveryStartedPR("started", "repo-a", time.Unix(1_700_001_000, 0)),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, isEligibleLeaseQueueRecoveryCandidate(tt.pr), tt.want)
		})
	}
}

func TestLeaseQueueRecoveryRepoKey(t *testing.T) {
	tests := []struct {
		name    string
		pr      *pipelinev1.PipelineRun
		wantKey string
		wantOK  bool
	}{
		{
			name:   "nil pipeline run",
			pr:     nil,
			wantOK: false,
		},
		{
			name: "empty repository annotation",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "no-repo",
					Namespace:   "test-ns",
					Annotations: map[string]string{},
				},
			},
			wantOK: false,
		},
		{
			name: "valid key extraction",
			pr: &pipelinev1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "my-pr",
					Namespace:   "test-ns",
					Annotations: map[string]string{keys.Repository: "my-repo"},
				},
			},
			wantKey: "test-ns/my-repo",
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := leaseQueueRecoveryRepoKey(tt.pr)
			assert.Equal(t, ok, tt.wantOK)
			if tt.wantOK {
				assert.Equal(t, key, tt.wantKey)
			}
		})
	}
}

func TestStartLeaseQueueRecoveryLoopEarlyReturn(t *testing.T) {
	observer, catcher := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startLeaseQueueRecoveryLoop(ctx, logger, nil, nil, nil, nil, 0)
	startLeaseQueueRecoveryLoop(ctx, logger, nil, nil, nil, nil, -1*time.Second)

	assert.Equal(t, catcher.FilterMessageSnippet("starting lease queue recovery loop").Len(), 0)
}

func TestCtrlOptsPromoteFilterMissingState(t *testing.T) {
	observer, _ := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	wh := &fakeReconciler{}
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "Test",
		Logger:        logger.Named("Test"),
	})
	opts := ctrlOpts()(impl)

	pr := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-state",
			Namespace: "test-ns",
		},
	}
	assert.Assert(t, !opts.PromoteFilterFunc(pr))
}
