package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	versioned "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	fakepacClientset "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned/fake"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	fakeTektonClientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Mock QueueManager for testing.
type mockQueueManager struct {
	resetAllCalled                  bool
	rebuildQueuesForNamespaceCalled bool
	rebuildError                    error
	resetStats                      map[string]int
	rebuildStats                    map[string]any
}

func (m *mockQueueManager) InitQueues(_ context.Context, _ tektonVersionedClient.Interface, _ versioned.Interface) error {
	return nil
}

func (m *mockQueueManager) RemoveRepository(_ *apipac.Repository) {
}

func (m *mockQueueManager) QueuedPipelineRuns(_ *apipac.Repository) []string {
	return []string{}
}

func (m *mockQueueManager) RunningPipelineRuns(_ *apipac.Repository) []string {
	return []string{}
}

func (m *mockQueueManager) AddListToRunningQueue(_ *apipac.Repository, _ []string) ([]string, error) {
	return []string{}, nil
}

func (m *mockQueueManager) AddToPendingQueue(_ *apipac.Repository, _ []string) error {
	return nil
}

func (m *mockQueueManager) RemoveFromQueue(_, _ string) bool {
	return false
}

func (m *mockQueueManager) RemoveAndTakeItemFromQueue(_ *apipac.Repository, _ *tektonv1.PipelineRun) string {
	return ""
}

func (m *mockQueueManager) ResetAll() map[string]int {
	m.resetAllCalled = true
	if m.resetStats == nil {
		return make(map[string]int)
	}
	return m.resetStats
}

func (m *mockQueueManager) RebuildQueuesForNamespace(_ context.Context, _ string, _ tektonVersionedClient.Interface, _ versioned.Interface) (map[string]any, error) {
	m.rebuildQueuesForNamespaceCalled = true
	if m.rebuildStats == nil {
		return make(map[string]any), m.rebuildError
	}
	return m.rebuildStats, m.rebuildError
}

func TestQueueEndpoints_RegisterHandlers(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	mux := http.NewServeMux()
	endpoints.RegisterHandlers(mux)

	// Test that handlers are registered
	req := httptest.NewRequest(http.MethodPost, "/api/v1/queue/reset", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should not get 404 (handler is registered)
	if w.Code == http.StatusNotFound {
		t.Error("Expected reset handler to be registered")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/queue/rebuild?repository=test-repo", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should not get 404 (handler is registered)
	if w.Code == http.StatusNotFound {
		t.Error("Expected rebuild handler to be registered")
	}
}

func TestQueueEndpoints_HandleQueueReset(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	tests := []struct {
		name           string
		method         string
		queueManager   *mockQueueManager
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "successful reset",
			method:         http.MethodPost,
			queueManager:   &mockQueueManager{resetStats: map[string]int{"reset": 5}},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:           "method not allowed",
			method:         http.MethodGet,
			queueManager:   &mockQueueManager{},
			expectedStatus: http.StatusMethodNotAllowed,
			expectError:    true,
		},
		{
			name:           "no queue manager registered",
			method:         http.MethodPost,
			queueManager:   nil,
			expectedStatus: http.StatusServiceUnavailable,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.queueManager != nil {
				RegisterQueueManager(tt.queueManager)
			} else {
				RegisterQueueManager(nil)
			}

			// Cleanup after test
			defer RegisterQueueManager(nil)

			req := httptest.NewRequest(tt.method, "/api/v1/queue/reset", nil)
			w := httptest.NewRecorder()

			// Execute
			endpoints.handleQueueReset(w, req)

			// Assert
			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !tt.expectError && tt.queueManager != nil {
				if !tt.queueManager.resetAllCalled {
					t.Error("Expected ResetAll to be called")
				}

				// Check response body for success case
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Errorf("Failed to unmarshal response: %v", err)
				}

				if response["status"] != "success" {
					t.Errorf("Expected success status, got %v", response["status"])
				}
			}
		})
	}
}

func TestQueueEndpoints_HandleQueueRebuild(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	// Create test repository
	testRepo := &apipac.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-namespace",
		},
		Spec: apipac.RepositorySpec{
			URL: "https://github.com/test/repo",
		},
	}

	tests := []struct {
		name                string
		method              string
		url                 string
		queueManager        *mockQueueManager
		hasRunningPRs       bool
		expectedStatus      int
		expectError         bool
		expectRebuildCalled bool
	}{
		{
			name:                "successful rebuild",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild?repository=test-repo",
			queueManager:        &mockQueueManager{rebuildStats: map[string]any{"rebuilt": 3}},
			hasRunningPRs:       false,
			expectedStatus:      http.StatusOK,
			expectError:         false,
			expectRebuildCalled: true,
		},
		{
			name:                "method not allowed",
			method:              http.MethodGet,
			url:                 "/api/v1/queue/rebuild?repository=test-repo",
			queueManager:        &mockQueueManager{},
			expectedStatus:      http.StatusMethodNotAllowed,
			expectError:         true,
			expectRebuildCalled: false,
		},
		{
			name:                "missing repository parameter",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild",
			queueManager:        &mockQueueManager{},
			expectedStatus:      http.StatusBadRequest,
			expectError:         true,
			expectRebuildCalled: false,
		},
		{
			name:                "repository not found",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild?repository=nonexistent-repo",
			queueManager:        &mockQueueManager{},
			expectedStatus:      http.StatusNotFound,
			expectError:         true,
			expectRebuildCalled: false,
		},
		{
			name:                "running pipelineruns conflict",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild?repository=test-repo",
			queueManager:        &mockQueueManager{},
			hasRunningPRs:       true,
			expectedStatus:      http.StatusConflict,
			expectError:         true,
			expectRebuildCalled: false,
		},
		{
			name:                "force rebuild with running pipelineruns",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild?repository=test-repo&force=true",
			queueManager:        &mockQueueManager{rebuildStats: map[string]any{"rebuilt": 2}},
			hasRunningPRs:       true,
			expectedStatus:      http.StatusOK,
			expectError:         false,
			expectRebuildCalled: true,
		},
		{
			name:                "rebuild error",
			method:              http.MethodPost,
			url:                 "/api/v1/queue/rebuild?repository=test-repo",
			queueManager:        &mockQueueManager{rebuildError: fmt.Errorf("rebuild failed")},
			hasRunningPRs:       false,
			expectedStatus:      http.StatusInternalServerError,
			expectError:         true,
			expectRebuildCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup clients
			var tektonObjects []runtime.Object
			var pacObjects []runtime.Object

			// Add test repository to PAC client
			pacObjects = append(pacObjects, testRepo)

			// Add running PipelineRuns if needed
			if tt.hasRunningPRs {
				runningPR := &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "running-pr",
						Namespace: "test-namespace",
						Labels: map[string]string{
							keys.Repository: "test-repo",
							keys.State:      kubeinteraction.StateStarted,
						},
					},
					Spec: tektonv1.PipelineRunSpec{
						Status: tektonv1.PipelineRunSpecStatusPending,
					},
				}
				tektonObjects = append(tektonObjects, runningPR)
			}

			// Add pending PipelineRuns for reconciliation trigger test
			pendingPR := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pr",
					Namespace: "test-namespace",
					Labels: map[string]string{
						keys.Repository: "test-repo",
						keys.State:      kubeinteraction.StateQueued,
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
			}
			tektonObjects = append(tektonObjects, pendingPR)

			tektonClient := fakeTektonClientset.NewSimpleClientset(tektonObjects...)
			pacClient := fakepacClientset.NewSimpleClientset(pacObjects...)

			// Register clients and queue manager
			RegisterClients(tektonClient, pacClient)
			RegisterQueueManager(tt.queueManager)

			// Cleanup after test
			defer func() {
				RegisterQueueManager(nil)
				RegisterClients(nil, nil)
			}()

			req := httptest.NewRequest(tt.method, tt.url, nil)
			w := httptest.NewRecorder()

			// Execute
			endpoints.handleQueueRebuild(w, req)

			// Assert
			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d. Response: %s", tt.expectedStatus, w.Code, w.Body.String())
			}

			if tt.expectRebuildCalled != tt.queueManager.rebuildQueuesForNamespaceCalled {
				t.Errorf("Expected rebuildQueuesForNamespace called: %v, got: %v", tt.expectRebuildCalled, tt.queueManager.rebuildQueuesForNamespaceCalled)
			}

			if !tt.expectError && tt.queueManager != nil {
				// Check response body for success case
				var response map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Errorf("Failed to unmarshal response: %v", err)
				}

				if response["status"] != "success" {
					t.Errorf("Expected success status, got %v", response["status"])
				}
			}
		})
	}
}

func TestQueueEndpoints_CheckForRunningPipelineRuns(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	tests := []struct {
		name           string
		hasRunningPRs  bool
		expectedStatus int
		expectError    bool
	}{
		{
			name:           "no running pipelineruns",
			hasRunningPRs:  false,
			expectedStatus: 0, // No HTTP response expected
			expectError:    false,
		},
		{
			name:           "has running pipelineruns",
			hasRunningPRs:  true,
			expectedStatus: http.StatusConflict,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tektonObjects []runtime.Object

			if tt.hasRunningPRs {
				runningPR := &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "running-pr",
						Namespace: "test-namespace",
						Labels: map[string]string{
							keys.Repository: "test-repo",
							keys.State:      kubeinteraction.StateStarted,
						},
					},
				}
				tektonObjects = append(tektonObjects, runningPR)
			}

			tektonClient := fakeTektonClientset.NewSimpleClientset(tektonObjects...)
			w := httptest.NewRecorder()

			// Execute
			err := endpoints.checkForRunningPipelineRuns(context.Background(), tektonClient, "test-namespace", "test-repo", w)

			// Assert
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectedStatus != 0 && w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestQueueEndpoints_TriggerReconciliationForPendingPipelineRuns(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	tests := []struct {
		name             string
		hasPendingPRs    bool
		expectError      bool
		expectAnnotation bool
	}{
		{
			name:             "has pending pipelineruns",
			hasPendingPRs:    true,
			expectError:      false,
			expectAnnotation: true,
		},
		{
			name:             "no pending pipelineruns",
			hasPendingPRs:    false,
			expectError:      false,
			expectAnnotation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tektonObjects []runtime.Object

			if tt.hasPendingPRs {
				pendingPR := &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pending-pr",
						Namespace: "test-namespace",
						Labels: map[string]string{
							keys.Repository: "test-repo",
							keys.State:      kubeinteraction.StateQueued,
						},
					},
					Spec: tektonv1.PipelineRunSpec{
						Status: tektonv1.PipelineRunSpecStatusPending,
					},
				}
				tektonObjects = append(tektonObjects, pendingPR)
			}

			tektonClient := fakeTektonClientset.NewSimpleClientset(tektonObjects...)

			// Execute
			err := endpoints.triggerReconciliationForPendingPipelineRuns(context.Background(), tektonClient, "test-namespace", "test-repo")

			// Assert
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectAnnotation {
				// Check if the PipelineRun was updated with the reconciliation trigger annotation
				updatedPR, err := tektonClient.TektonV1().PipelineRuns("test-namespace").Get(context.Background(), "pending-pr", metav1.GetOptions{})
				if err != nil {
					t.Errorf("Failed to get updated PipelineRun: %v", err)
				}

				if updatedPR.Annotations == nil {
					t.Error("Expected annotations to be set")
				} else if _, exists := updatedPR.Annotations["pipelinesascode.tekton.dev/reconcile-trigger"]; !exists {
					t.Error("Expected reconcile-trigger annotation to be set")
				}
			}
		})
	}
}

func TestQueueEndpoints_WriteResponses(t *testing.T) {
	logger := zap.NewNop().Sugar()
	endpoints := NewQueueEndpoints(logger)

	t.Run("writeErrorResponse", func(t *testing.T) {
		w := httptest.NewRecorder()
		endpoints.writeErrorResponse(w, http.StatusBadRequest, "test error")

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
		}

		if w.Body.String() != "test error" {
			t.Errorf("Expected body 'test error', got '%s'", w.Body.String())
		}
	})

	t.Run("writeJSONErrorResponse", func(t *testing.T) {
		w := httptest.NewRecorder()
		endpoints.writeJSONErrorResponse(w, http.StatusInternalServerError, "json error")

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}

		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}

		if response["status"] != "error" {
			t.Errorf("Expected status 'error', got %v", response["status"])
		}

		if response["message"] != "json error" {
			t.Errorf("Expected message 'json error', got %v", response["message"])
		}
	})

	t.Run("writeJSONResponse", func(t *testing.T) {
		w := httptest.NewRecorder()
		testResponse := map[string]any{
			"status": "success",
			"data":   "test data",
		}
		endpoints.writeJSONResponse(w, http.StatusOK, testResponse)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}

		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type 'application/json', got '%s'", w.Header().Get("Content-Type"))
		}

		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}

		if response["status"] != "success" {
			t.Errorf("Expected status 'success', got %v", response["status"])
		}

		if response["data"] != "test data" {
			t.Errorf("Expected data 'test data', got %v", response["data"])
		}
	})
}
