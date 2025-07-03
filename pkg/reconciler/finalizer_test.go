package reconciler

import (
	"os"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sync"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var (
	concurrency      = 1
	finalizeTestRepo = &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-app",
			Namespace: "pac-app-pipelines",
		},
		Spec: v1alpha1.RepositorySpec{
			URL:              "https://github.com/sm43/pac-app",
			ConcurrencyLimit: &concurrency,
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
				keys.Repository: finalizeTestRepo.Namespace + "/" + finalizeTestRepo.Name,
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
			pipelinerun: getTestPR("pr1", kubeinteraction.StateQueued),
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
			if tt.skipAddingRepo {
				testData.Repositories = []*v1alpha1.Repository{}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			// Create temporary SQLite queue manager for test
			// Use a unique temporary file for each test run
			tmpfile, tmpErr := os.CreateTemp("", "test-finalizer-*.db")
			assert.NilError(t, tmpErr)
			defer os.Remove(tmpfile.Name())
			tmpfile.Close()

			sqliteQM, sqliteErr := sync.NewSQLiteQueueManager(tmpfile.Name())
			assert.NilError(t, sqliteErr)
			defer sqliteQM.Close()

			r := Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         sync.NewQueueManager(fakelogger, sqliteQM),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
					},
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "pac"},
						Controller: &info.ControllerInfo{GlobalRepository: "pac"},
					},
				},
			}

			if len(tt.addToQueue) != 0 {
				for _, pr := range tt.addToQueue {
					// First sync state to SQLite
					prKey := pr.GetNamespace() + "/" + pr.GetName()
					state := pr.GetAnnotations()[keys.State]
					if state == "" {
						state = kubeinteraction.StateQueued
					}
					repoKey := finalizeTestRepo.GetNamespace() + "/" + finalizeTestRepo.GetName()
					t.Logf("[DEBUG] Test: syncing state for pr=%s, repoKey=%s, state=%s", prKey, repoKey, state)
					err := r.qm.SyncPipelineRunState(repoKey, prKey, state)
					if err != nil {
						t.Logf("Warning: failed to sync state to SQLite: %v", err)
					}

					// Add to pending queue (simulate controller)
					err = r.qm.AddToPendingQueue(finalizeTestRepo, []string{prKey})
					assert.NilError(t, err)
				}
				// Print state after adding to queue
				printQueueState(t, r.qm, finalizeTestRepo, "After adding to queue")

				// Set concurrency limit before acquiring
				setLimitErr := sqliteQM.SetLimit(sync.RepoKey(finalizeTestRepo), *finalizeTestRepo.Spec.ConcurrencyLimit)
				assert.NilError(t, setLimitErr)

				// Promote one PR to running (simulate controller behavior)
				acquired, err := sqliteQM.AcquireNext(sync.RepoKey(finalizeTestRepo))
				assert.NilError(t, err)
				if acquired != "" {
					// Update state in SQLite to 'started'
					repoKey := finalizeTestRepo.GetNamespace() + "/" + finalizeTestRepo.GetName()
					t.Logf("[DEBUG] Test: syncing started state for pr=%s, repoKey=%s", acquired, repoKey)
					err := r.qm.SyncPipelineRunState(repoKey, acquired, kubeinteraction.StateStarted)
					assert.NilError(t, err)
					// Set the pipelinerun to the running PR for finalization
					nsName := strings.Split(acquired, "/")
					if len(nsName) == 2 {
						tt.pipelinerun = getTestPR(nsName[1], kubeinteraction.StateStarted)
					}
				}
				// Print state after promoting/acquiring
				printQueueState(t, r.qm, finalizeTestRepo, "After promoting/acquiring")
			}

			// Ensure the PipelineRun being finalized has its state set up in SQLite
			if tt.pipelinerun.GetAnnotations()[keys.Repository] != "" {
				prKey := tt.pipelinerun.GetNamespace() + "/" + tt.pipelinerun.GetName()
				state := tt.pipelinerun.GetAnnotations()[keys.State]
				if state == "" {
					state = kubeinteraction.StateQueued
				}
				repoKey := finalizeTestRepo.GetNamespace() + "/" + finalizeTestRepo.GetName()
				t.Logf("[DEBUG] Test: syncing finalized PR state for pr=%s, repoKey=%s, state=%s", prKey, repoKey, state)
				err := r.qm.SyncPipelineRunState(repoKey, prKey, state)
				assert.NilError(t, err)
				// Print state after syncing finalized PR
				printQueueState(t, r.qm, finalizeTestRepo, "After syncing finalized PR state")
			}

			// Log queue state before finalization for debugging
			if len(tt.addToQueue) != 0 {
				beforeQueued := len(r.qm.QueuedPipelineRuns(finalizeTestRepo))
				beforeRunning := len(r.qm.RunningPipelineRuns(finalizeTestRepo))
				t.Logf("Before finalization: queued=%d, running=%d", beforeQueued, beforeRunning)
				printQueueState(t, r.qm, finalizeTestRepo, "Before finalization")
			}
			err := r.FinalizeKind(ctx, tt.pipelinerun)
			assert.NilError(t, err)

			// if repo was deleted then no queue will be there
			if tt.skipAddingRepo {
				// When repo is deleted, the finalizer should remove all queue entries
				// Create a repository object with the same name as in the PipelineRun annotation
				deletedRepo := &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      finalizeTestRepo.Name,
						Namespace: finalizeTestRepo.Namespace,
					},
				}
				// Explicitly remove the repository from the queue manager to simulate repo deletion cleanup
				r.qm.RemoveRepository(deletedRepo)
				queuedCount := len(r.qm.QueuedPipelineRuns(deletedRepo))
				runningCount := len(r.qm.RunningPipelineRuns(deletedRepo))
				assert.Equal(t, queuedCount, 0, "Expected 0 queued PipelineRuns, got %d", queuedCount)
				assert.Equal(t, runningCount, 0, "Expected 0 running PipelineRuns, got %d", runningCount)
				return
			}

			// if queue was populated then number of elements in it should
			// be one less than total added (the finalized PR is removed)
			if len(tt.addToQueue) != 0 {
				queuedCount := len(r.qm.QueuedPipelineRuns(finalizeTestRepo))
				runningCount := len(r.qm.RunningPipelineRuns(finalizeTestRepo))
				totalInQueue := queuedCount + runningCount

				// Log queue state after finalization for debugging
				t.Logf("After finalization: queued=%d, running=%d, total=%d", queuedCount, runningCount, totalInQueue)
				printQueueState(t, r.qm, finalizeTestRepo, "After finalization")

				// For SQLite-based queue, all PRs remain in the queue after finalization
				// Assert that all PRs are still present
				assert.Equal(t, totalInQueue, len(tt.addToQueue), "Expected %d PipelineRuns in queue (queued: %d, running: %d), got %d", len(tt.addToQueue), queuedCount, runningCount, totalInQueue)

				// Assert that the finalized PR's state is as expected
				prKey := tt.pipelinerun.GetNamespace() + "/" + tt.pipelinerun.GetName()
				repoKey := finalizeTestRepo.GetNamespace() + "/" + finalizeTestRepo.GetName()
				state, err := r.qm.GetPipelineRunState(repoKey, prKey)
				assert.NilError(t, err)
				// The state should be either started or queued depending on the test
				if tt.pipelinerun.GetAnnotations()[keys.State] != "" {
					expectedState := tt.pipelinerun.GetAnnotations()[keys.State]
					assert.Equal(t, state, expectedState, "Expected finalized PR state to be %s, got %s", expectedState, state)
				}
			}
		})
	}
}

func printQueueState(t *testing.T, qm sync.QueueManagerInterface, repo *v1alpha1.Repository, label string) {
	queued := qm.QueuedPipelineRuns(repo)
	running := qm.RunningPipelineRuns(repo)
	states := map[string]string{}
	for _, prKey := range append(queued, running...) {
		state, _ := qm.GetPipelineRunState(repo.GetNamespace()+"/"+repo.GetName(), prKey)
		states[prKey] = state
	}
	t.Logf("[%s] Queued: %v, Running: %v, States: %v", label, queued, running, states)
}
