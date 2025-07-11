package sync

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	fake "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned/fake"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tektonfake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ktesting "k8s.io/client-go/testing"
)

// mockQueueManagerForHealthChecker implements the QueueManagerInterface for testing.
type mockQueueManagerForHealthChecker struct {
	rebuildCalls []string
	rebuildError error
}

func (m *mockQueueManagerForHealthChecker) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ versioned.Interface) error {
	return nil
}

func (m *mockQueueManagerForHealthChecker) RemoveRepository(_ *apipac.Repository) {
	// Not used in health checker tests
}

func (m *mockQueueManagerForHealthChecker) QueuedPipelineRuns(_ *apipac.Repository) []string {
	return []string{}
}

func (m *mockQueueManagerForHealthChecker) RunningPipelineRuns(_ *apipac.Repository) []string {
	return []string{}
}

func (m *mockQueueManagerForHealthChecker) AddListToRunningQueue(_ *apipac.Repository, _ []string) ([]string, error) {
	return []string{}, nil
}

func (m *mockQueueManagerForHealthChecker) AddToPendingQueue(_ *apipac.Repository, _ []string) error {
	return nil
}

func (m *mockQueueManagerForHealthChecker) RemoveFromQueue(_, _ string) bool {
	return true
}

func (m *mockQueueManagerForHealthChecker) RemoveAndTakeItemFromQueue(_ *apipac.Repository, _ *tektonv1.PipelineRun) string {
	return ""
}

func (m *mockQueueManagerForHealthChecker) ResetAll() map[string]int {
	return make(map[string]int)
}

func (m *mockQueueManagerForHealthChecker) RebuildQueuesForNamespace(_ context.Context, namespace string, _ tektonVersionedClient.Interface, _ versioned.Interface) (map[string]any, error) {
	m.rebuildCalls = append(m.rebuildCalls, namespace)
	if m.rebuildError != nil {
		return nil, m.rebuildError
	}
	return map[string]any{
		"namespace":              namespace,
		"repositories_processed": 1,
		"repositories_rebuilt": map[string]any{
			"test-repo": map[string]any{
				"rebuilt_pending": 5,
			},
		},
	}, nil
}

func createTestRepository(name string, concurrencyLimit *int) *apipac.Repository {
	repo := &apipac.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
		},
		Spec: apipac.RepositorySpec{},
	}
	if concurrencyLimit != nil {
		repo.Spec.ConcurrencyLimit = concurrencyLimit
	}
	return repo
}

func createTestQueueHealthChecker(logger *zap.SugaredLogger, tektonClient tektonVersionedClient.Interface, pacClient versioned.Interface, qm QueueManagerInterface) *QueueHealthChecker {
	qhc := NewQueueHealthChecker(logger)
	qhc.SetClients(tektonClient, pacClient)
	qhc.SetQueueManager(qm)

	// Enable health checker with test settings
	testSettings := &settings.Settings{
		QueueHealthCheckInterval:  10,
		QueueHealthStuckThreshold: 120,
	}
	qhc.UpdateSettings(testSettings)

	return qhc
}

func createTestPipelineRun(name, repoName, state string, status tektonv1.PipelineRunSpecStatus, creationTime time.Time) *tektonv1.PipelineRun {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(creationTime),
			Labels: map[string]string{
				keys.Repository: repoName,
				keys.State:      state,
			},
			Annotations: map[string]string{},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: status,
		},
	}
	return pr
}

func TestNewQueueHealthChecker(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()
	mockQM := &mockQueueManagerForHealthChecker{}

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	assert.Assert(t, qhc != nil)
	assert.Assert(t, qhc.logger == logger)
	assert.Assert(t, qhc.stopCh != nil)
	assert.Assert(t, qhc.lastTriggerTimes != nil)
}

func TestQueueHealthChecker_StartStop(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()
	mockQM := &mockQueueManagerForHealthChecker{}

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the health checker
	qhc.Start(ctx)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Stop the health checker
	qhc.Stop()

	// Verify it stopped cleanly - the stopCh should be closed
	select {
	case <-qhc.stopCh:
		// Expected - stopCh is closed
	default:
		t.Fatal("stopCh should be closed after Stop()")
	}
}

func TestQueueHealthChecker_checkRepositoryHealth(t *testing.T) {
	tests := []struct {
		name                    string
		repo                    *apipac.Repository
		runningPRs              []*tektonv1.PipelineRun
		pendingPRs              []*tektonv1.PipelineRun
		expectedTriggered       int
		expectedRebuildCalls    int
		expectedAnnotationCalls int
		expectedError           string
	}{
		{
			name:                    "no concurrency limit set",
			repo:                    createTestRepository("test-repo", nil),
			expectedTriggered:       0,
			expectedRebuildCalls:    0,
			expectedAnnotationCalls: 0,
		},
		{
			name: "repository at capacity",
			repo: createTestRepository("test-repo", &[]int{2}[0]),
			runningPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("running-pr-1", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
				createTestPipelineRun("running-pr-2", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
			},
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedTriggered:       0,
			expectedRebuildCalls:    0,
			expectedAnnotationCalls: 0,
		},
		{
			name: "has capacity with fresh pending PipelineRuns",
			repo: createTestRepository("test-repo", &[]int{3}[0]),
			runningPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("running-pr", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
			},
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedTriggered:       1,
			expectedRebuildCalls:    0,
			expectedAnnotationCalls: 1,
		},
		{
			name: "has capacity with stuck PipelineRuns",
			repo: createTestRepository("test-repo", &[]int{3}[0]),
			runningPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("running-pr", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
			},
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("stuck-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute)),
			},
			expectedTriggered:       1,
			expectedRebuildCalls:    1,
			expectedAnnotationCalls: 1,
		},
		{
			name: "no capacity but has pending PipelineRuns",
			repo: createTestRepository("test-repo", &[]int{2}[0]),
			runningPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("running-pr-1", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
				createTestPipelineRun("running-pr-2", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now()),
			},
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedTriggered:       0,
			expectedRebuildCalls:    0,
			expectedAnnotationCalls: 0,
		},
		{
			name: "multiple stuck PipelineRuns with capacity",
			repo: createTestRepository("test-repo", &[]int{5}[0]),
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("stuck-pr-1", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute)),
				createTestPipelineRun("stuck-pr-2", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-3*time.Minute)),
				createTestPipelineRun("fresh-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedTriggered:       3,
			expectedRebuildCalls:    1,
			expectedAnnotationCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := observer.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			// Create fake clients
			tektonClient := tektonfake.NewSimpleClientset()
			pacClient := fake.NewSimpleClientset()

			// Track annotation calls
			annotationCalls := 0
			tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
				annotationCalls++
				return false, nil, nil
			})

			// Track rebuild calls
			mockQM := &mockQueueManagerForHealthChecker{}

			// Add test data to clients
			for _, pr := range tt.runningPRs {
				_, _ = tektonClient.TektonV1().PipelineRuns(tt.repo.Namespace).Create(context.TODO(), pr, metav1.CreateOptions{})
			}
			for _, pr := range tt.pendingPRs {
				_, _ = tektonClient.TektonV1().PipelineRuns(tt.repo.Namespace).Create(context.TODO(), pr, metav1.CreateOptions{})
			}

			qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

			triggered, err := qhc.checkRepositoryHealth(context.TODO(), tt.repo)

			if tt.expectedError != "" {
				assert.Error(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}

			assert.Equal(t, triggered, tt.expectedTriggered)
			assert.Equal(t, len(mockQM.rebuildCalls), tt.expectedRebuildCalls)
			assert.Equal(t, annotationCalls, tt.expectedAnnotationCalls)
		})
	}
}

func TestQueueHealthChecker_checkAllRepositories(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repositories
	repoWithLimit := createTestRepository("repo-with-limit", &[]int{2}[0])
	repoWithoutLimit := createTestRepository("repo-without-limit", nil)

	// Create test PipelineRuns - make one stuck and one not stuck
	stuckPR := createTestPipelineRun("stuck-pr", "test-ns", "repo-with-limit", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute))
	pendingPR := createTestPipelineRun("pending-pr", "test-ns", "repo-with-limit", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now())

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Track annotation calls
	annotationCalls := 0
	tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
		annotationCalls++
		return false, nil, nil
	})

	// Track rebuild calls
	mockQM := &mockQueueManagerForHealthChecker{}

	// Add test data to clients
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), stuckPR, metav1.CreateOptions{})
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), pendingPR, metav1.CreateOptions{})
	_, _ = pacClient.PipelinesascodeV1alpha1().Repositories("test-ns").Create(context.TODO(), repoWithLimit, metav1.CreateOptions{})
	_, _ = pacClient.PipelinesascodeV1alpha1().Repositories("test-ns").Create(context.TODO(), repoWithoutLimit, metav1.CreateOptions{})

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	qhc.checkAllRepositories(context.TODO())

	// Should have triggered once for the repo with limit and stuck PipelineRuns
	assert.Equal(t, len(mockQM.rebuildCalls), 1)
	assert.Equal(t, annotationCalls, 1)
}

func TestQueueHealthChecker_RateLimiting(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repository
	testRepo := createTestRepository("test-repo", &[]int{2}[0])

	// Create stuck PipelineRun for first test
	stuckPR := createTestPipelineRun("stuck-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute))

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Track rebuild calls
	mockQM := &mockQueueManagerForHealthChecker{}

	// Add test data to clients
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), stuckPR, metav1.CreateOptions{})

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	// First call should trigger rebuild
	triggered1, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.NilError(t, err)
	assert.Equal(t, triggered1, 1)
	assert.Equal(t, len(mockQM.rebuildCalls), 1)

	// Second call immediately after should be rate limited for rebuilds
	// but should still trigger if there's capacity
	triggered2, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.NilError(t, err)
	assert.Equal(t, triggered2, 1)               // Should still trigger
	assert.Equal(t, len(mockQM.rebuildCalls), 1) // But no additional rebuild

	// Wait for rate limit to expire and try again
	time.Sleep(200 * time.Millisecond) // Longer than our test interval
	triggered3, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.NilError(t, err)
	assert.Equal(t, triggered3, 1)
	// Should still be rate limited since we didn't wait the full 3 minutes
	assert.Equal(t, len(mockQM.rebuildCalls), 1)
}

func TestQueueHealthChecker_triggerReconciliationForPendingPipelineRuns(t *testing.T) {
	tests := []struct {
		name          string
		pendingPRs    []*tektonv1.PipelineRun
		patchError    error
		expectedError string
		expectedCalls int
	}{
		{
			name: "successful trigger",
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedCalls: 1,
		},
		{
			name:          "no pending PipelineRuns",
			pendingPRs:    []*tektonv1.PipelineRun{},
			expectedCalls: 0,
		},
		{
			name: "multiple pending PipelineRuns - triggers first one",
			pendingPRs: []*tektonv1.PipelineRun{
				createTestPipelineRun("pending-pr-1", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
				createTestPipelineRun("pending-pr-2", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now()),
			},
			expectedCalls: 1, // Should only trigger the first one
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := observer.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			// Create fake clients
			tektonClient := tektonfake.NewSimpleClientset()
			pacClient := fake.NewSimpleClientset()

			// Track patch calls
			patchCalls := 0
			tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
				patchCalls++
				if tt.patchError != nil {
					return true, nil, tt.patchError
				}
				return false, nil, nil
			})

			// Add test data to clients
			for _, pr := range tt.pendingPRs {
				_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), pr, metav1.CreateOptions{})
			}

			mockQM := &mockQueueManagerForHealthChecker{}
			qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

			err := qhc.triggerReconciliationForPendingPipelineRuns(context.TODO(), "test-ns", "test-repo")

			if tt.expectedError != "" {
				assert.Error(t, err, tt.expectedError)
			} else {
				assert.NilError(t, err)
			}

			assert.Equal(t, patchCalls, tt.expectedCalls)
		})
	}
}

func TestQueueHealthChecker_ErrorHandling(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repository
	testRepo := createTestRepository("test-repo", &[]int{2}[0])

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Mock queue manager that returns an error
	mockQM := &mockQueueManagerForHealthChecker{
		rebuildError: fmt.Errorf("rebuild failed"),
	}

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	// Test with list error
	tektonClient.PrependReactor("list", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("list failed")
	})

	triggered, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.ErrorContains(t, err, "list failed")
	assert.Equal(t, triggered, 0)
}

func TestQueueHealthChecker_ActivityDetection(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repository
	testRepo := createTestRepository("test-repo", &[]int{5}[0])

	// Create many recently started PipelineRuns (high activity)
	recentPRs := []*tektonv1.PipelineRun{}
	for i := range [10]struct{}{} {
		pr := createTestPipelineRun(fmt.Sprintf("recent-pr-%d", i), "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now().Add(-30*time.Second))
		recentPRs = append(recentPRs, pr)
	}

	// Create some pending PipelineRuns
	pendingPR := createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now())

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Track annotation calls
	annotationCalls := 0
	tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
		annotationCalls++
		return false, nil, nil
	})

	// Add test data to clients
	for _, pr := range recentPRs {
		_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), pr, metav1.CreateOptions{})
	}
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), pendingPR, metav1.CreateOptions{})

	mockQM := &mockQueueManagerForHealthChecker{}
	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	triggered, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.NilError(t, err)

	// Should detect high activity and skip triggering
	assert.Equal(t, triggered, 0)
	assert.Equal(t, annotationCalls, 0)
}

func TestQueueHealthChecker_NilDependencies(_ *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Test with nil dependencies
	qhc := NewQueueHealthChecker(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic and should log error
	qhc.Start(ctx)
	qhc.Stop()
}

func TestQueueHealthChecker_ConcurrentAccess(_ *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()
	mockQM := &mockQueueManagerForHealthChecker{}

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	// Test concurrent access to lastTriggerTimes map
	done := make(chan bool, 10)
	for i := range [10]struct{}{} {
		go func(id int) {
			defer func() { done <- true }()
			repoName := fmt.Sprintf("test-repo-%d", id)
			testRepo := createTestRepository(repoName, "test-ns", &[]int{2}[0])
			_, _ = qhc.checkRepositoryHealth(context.TODO(), testRepo)
		}(i)
	}

	// Wait for all goroutines to complete
	for range [10]struct{}{} {
		<-done
	}

	// Should not panic due to concurrent map access
}

func TestQueueHealthChecker_StuckPipelineRunDetection(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repository
	testRepo := createTestRepository("test-repo", &[]int{3}[0])

	// Create mix of stuck and fresh PipelineRuns
	stuckPR1 := createTestPipelineRun("stuck-pr-1", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute))
	stuckPR2 := createTestPipelineRun("stuck-pr-2", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-3*time.Minute))
	freshPR := createTestPipelineRun("fresh-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-30*time.Second))

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Add test data to clients
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), stuckPR1, metav1.CreateOptions{})
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), stuckPR2, metav1.CreateOptions{})
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), freshPR, metav1.CreateOptions{})

	mockQM := &mockQueueManagerForHealthChecker{}
	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	triggered, err := qhc.checkRepositoryHealth(context.TODO(), testRepo)
	assert.NilError(t, err)

	// Should trigger all 3 PipelineRuns and rebuild queue due to stuck ones
	assert.Equal(t, triggered, 3)
	assert.Equal(t, len(mockQM.rebuildCalls), 1)
}

func TestQueueHealthChecker_RepositoryListError(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Make repository list fail
	pacClient.PrependReactor("list", "repositories", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("repository list failed")
	})

	mockQM := &mockQueueManagerForHealthChecker{}
	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	// Should handle error gracefully
	qhc.checkAllRepositories(context.TODO())

	// Should not have called rebuild
	assert.Equal(t, len(mockQM.rebuildCalls), 0)
}

func TestQueueHealthChecker_PatchRetryLogic(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create pending PipelineRun
	pendingPR := createTestPipelineRun("pending-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now())

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Track patch attempts
	patchAttempts := 0
	tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
		patchAttempts++
		if patchAttempts < 3 {
			// Simulate conflict error for first 2 attempts
			return true, nil, fmt.Errorf("Operation cannot be fulfilled on pipelineruns.tekton.dev \"pending-pr\": the object has been modified")
		}
		// Success on 3rd attempt
		return false, nil, nil
	})

	// Add test data to clients
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), pendingPR, metav1.CreateOptions{})

	mockQM := &mockQueueManagerForHealthChecker{}
	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	err := qhc.triggerReconciliationForPendingPipelineRuns(context.TODO(), "test-ns", "test-repo")
	assert.NilError(t, err)

	// Should have retried 3 times
	assert.Equal(t, patchAttempts, 3)
}

func TestQueueHealthChecker_Integration(t *testing.T) {
	observer, _ := observer.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Create test repository
	testRepo := createTestRepository("test-repo", &[]int{2}[0])

	// Create a mix of PipelineRuns
	runningPR := createTestPipelineRun("running-pr", "test-ns", "test-repo", kubeinteraction.StateStarted, "", time.Now())
	stuckPR := createTestPipelineRun("stuck-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now().Add(-5*time.Minute))
	freshPR := createTestPipelineRun("fresh-pr", "test-ns", "test-repo", kubeinteraction.StateQueued, tektonv1.PipelineRunSpecStatusPending, time.Now())

	// Create fake clients
	tektonClient := tektonfake.NewSimpleClientset()
	pacClient := fake.NewSimpleClientset()

	// Track all operations
	var operations []string
	tektonClient.PrependReactor("patch", "pipelineruns", func(_ ktesting.Action) (bool, runtime.Object, error) {
		operations = append(operations, "patch")
		return false, nil, nil
	})

	mockQM := &mockQueueManagerForHealthChecker{}
	mockQM.rebuildError = nil // Ensure rebuild succeeds

	// Add test data to clients
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), runningPR, metav1.CreateOptions{})
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), stuckPR, metav1.CreateOptions{})
	_, _ = tektonClient.TektonV1().PipelineRuns("test-ns").Create(context.TODO(), freshPR, metav1.CreateOptions{})
	_, _ = pacClient.PipelinesascodeV1alpha1().Repositories("test-ns").Create(context.TODO(), testRepo, metav1.CreateOptions{})

	qhc := createTestQueueHealthChecker(logger, tektonClient, pacClient, mockQM)

	// Run full health check
	qhc.checkAllRepositories(context.TODO())

	// Should have rebuilt queue due to stuck PipelineRuns
	assert.Equal(t, len(mockQM.rebuildCalls), 1)
	assert.Equal(t, mockQM.rebuildCalls[0], "test-ns")

	// Should have triggered reconciliation
	assert.Equal(t, len(operations), 1)
	assert.Equal(t, operations[0], "patch")
}
