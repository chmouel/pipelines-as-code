package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// QueueEndpoints provides HTTP handlers for queue management operations.
type QueueEndpoints struct {
	logger *zap.SugaredLogger
}

// NewQueueEndpoints creates a new QueueEndpoints instance.
func NewQueueEndpoints(logger *zap.SugaredLogger) *QueueEndpoints {
	return &QueueEndpoints{
		logger: logger,
	}
}

// RegisterHandlers registers the queue management endpoints with the provided mux.
func (qe *QueueEndpoints) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/queue/reset", qe.handleQueueReset)
	mux.HandleFunc("/api/v1/queue/rebuild", qe.handleQueueRebuild)
}

// handleQueueReset handles the queue reset endpoint.
func (qe *QueueEndpoints) handleQueueReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		qe.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get the registered QueueManager instance
	queueManager := GetRegisteredQueueManager()
	if queueManager == nil {
		qe.logger.Error("QueueManager not registered yet")
		qe.writeJSONErrorResponse(w, http.StatusServiceUnavailable, "QueueManager not registered yet")
		return
	}

	// Call the queue reset functionality
	resetStats := queueManager.ResetAll()

	// Create response with statistics
	response := map[string]any{
		"status":  "success",
		"message": "All queues reset",
		"stats":   resetStats,
	}

	qe.writeJSONResponse(w, http.StatusOK, response)
}

// handleQueueRebuild handles the queue rebuild endpoint.
func (qe *QueueEndpoints) handleQueueRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		qe.writeErrorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get repository name from query parameter
	repoName := r.URL.Query().Get("repository")
	if repoName == "" {
		qe.writeJSONErrorResponse(w, http.StatusBadRequest, "repository query parameter is required")
		return
	}

	// Check if force parameter is provided
	force := r.URL.Query().Get("force") == "true"

	// Get the registered QueueManager instance
	queueManager := GetRegisteredQueueManager()
	if queueManager == nil {
		qe.logger.Error("QueueManager not registered yet")
		qe.writeJSONErrorResponse(w, http.StatusServiceUnavailable, "QueueManager not registered yet")
		return
	}

	// Get the registered clients
	tektonClient, pacClient := GetRegisteredClients()
	if tektonClient == nil || pacClient == nil {
		qe.logger.Error("Clients not registered yet")
		qe.writeJSONErrorResponse(w, http.StatusServiceUnavailable, "Tekton and PAC clients not registered yet")
		return
	}

	// Find the repository CR by name across all namespaces
	cs := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: pacClient,
		},
	}
	repo, err := matcher.GetRepo(r.Context(), cs, repoName)
	if err != nil {
		qe.logger.Errorf("Failed to find repository %s: %v", repoName, err)
		qe.writeJSONErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to find repository: %v", err))
		return
	}
	if repo == nil {
		qe.writeJSONErrorResponse(w, http.StatusNotFound, fmt.Sprintf("Repository %s not found", repoName))
		return
	}

	namespace := repo.GetNamespace()
	qe.logger.Infof("Found repository %s in namespace %s", repoName, namespace)

	// Safety check: ensure there are no running PipelineRuns for this repository before rebuilding (unless force=true)
	if !force {
		if err := qe.checkForRunningPipelineRuns(r.Context(), tektonClient, namespace, repoName, w); err != nil {
			return // Response already written by checkForRunningPipelineRuns
		}
	} else {
		qe.logger.Warnf("Queue rebuild requested for repository %s with force=true - proceeding despite any running PipelineRuns", repoName)
	}

	// Call the queue rebuild functionality for the specific namespace
	qe.logger.Infof("Starting queue rebuild for repository %s in namespace %s", repoName, namespace)
	rebuildStats, err := queueManager.RebuildQueuesForNamespace(r.Context(), namespace, tektonClient, pacClient)
	if err != nil {
		qe.logger.Errorf("Failed to rebuild queues for repository %s: %v", repoName, err)
		qe.writeJSONErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to rebuild queues: %v", err))
		return
	}

	// After successful rebuild, trigger reconciliation for pending PipelineRuns of this repository
	// This ensures that the queue processing starts immediately
	err = qe.triggerReconciliationForPendingPipelineRuns(r.Context(), tektonClient, namespace, repoName)
	if err != nil {
		qe.logger.Warnf("Failed to trigger reconciliation for pending PipelineRuns: %v", err)
		// Don't fail the request, just log the warning
	}

	// Create success response with rebuild statistics
	response := map[string]any{
		"status":  "success",
		"message": fmt.Sprintf("Successfully rebuilt queues for repository %s", repoName),
		"stats":   rebuildStats,
	}

	qe.writeJSONResponse(w, http.StatusOK, response)
	qe.logger.Infof("Completed queue rebuild for repository %s", repoName)
}

// checkForRunningPipelineRuns checks if there are any running PipelineRuns for the specific repository
// Returns an error if there are running PipelineRuns, and writes the appropriate HTTP response.
func (qe *QueueEndpoints) checkForRunningPipelineRuns(ctx context.Context, tektonClient tektonVersionedClient.Interface, namespace, repoName string, w http.ResponseWriter) error {
	runningPRs, err := tektonClient.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repoName, keys.State, kubeinteraction.StateStarted),
	})
	if err != nil {
		qe.logger.Errorf("Failed to check for running PipelineRuns for repository %s: %v", repoName, err)
		qe.writeJSONErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("Failed to check for running PipelineRuns: %v", err))
		return err
	}

	if len(runningPRs.Items) > 0 {
		qe.logger.Warnf("Queue rebuild requested for repository %s but %d PipelineRuns are currently running", repoName, len(runningPRs.Items))

		runningNames := make([]string, len(runningPRs.Items))
		for i, pr := range runningPRs.Items {
			runningNames[i] = pr.Name
		}

		response := map[string]any{
			"status":               "error",
			"message":              fmt.Sprintf("Cannot rebuild queue: %d PipelineRuns are currently running for repository %s. Wait for them to complete or use force=true parameter.", len(runningPRs.Items), repoName),
			"running_pipelineruns": runningNames,
		}

		qe.writeJSONResponse(w, http.StatusConflict, response)
		return fmt.Errorf("running PipelineRuns found")
	}

	return nil
}

// triggerReconciliationForPendingPipelineRuns updates the state annotation on one pending PipelineRun
// for the specific repository to trigger reconciliation by the controller. The reconciler will then
// process the queue and move items from pending to running based on concurrency limits.
func (qe *QueueEndpoints) triggerReconciliationForPendingPipelineRuns(ctx context.Context, tektonClient tektonVersionedClient.Interface, namespace, repoName string) error {
	// Get all PipelineRuns in "queued" state with pending status for this specific repository
	pipelineRuns, err := tektonClient.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s,%s=%s", keys.Repository, repoName, keys.State, kubeinteraction.StateQueued),
	})
	if err != nil {
		return fmt.Errorf("failed to list queued PipelineRuns for repository %s: %w", repoName, err)
	}

	// Find the first pending PipelineRun and trigger reconciliation for it only
	// The reconciler will then process the entire queue and move items from pending to running
	// based on the repository's concurrency limit
	for _, pr := range pipelineRuns.Items {
		// Only trigger for PipelineRuns that are actually pending
		if pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
			// Update the state annotation to trigger reconciliation
			// We add a small timestamp to ensure the annotation value changes and triggers the controller
			timestamp := fmt.Sprintf("%d", time.Now().Unix())

			// Create a patch to update the state annotation and add a reconciliation trigger
			patch := fmt.Sprintf(`{"metadata":{"annotations":{"pipelinesascode.tekton.dev/state":"%s","pipelinesascode.tekton.dev/reconcile-trigger":"%s"}}}`, kubeinteraction.StateQueued, timestamp)

			_, err := tektonClient.TektonV1().PipelineRuns(namespace).Patch(ctx, pr.Name,
				"application/merge-patch+json", []byte(patch), metav1.PatchOptions{})
			if err != nil {
				qe.logger.Warnf("Failed to trigger reconciliation for PipelineRun %s: %v", pr.Name, err)
				continue // Try the next one if this fails
			}
			qe.logger.Infof("Triggered reconciliation for PipelineRun %s (repository %s) - this will kick off queue processing", pr.Name, repoName)
			return nil // Successfully triggered one, let the queue management handle the rest
		}
	}

	qe.logger.Infof("No pending PipelineRuns found to trigger reconciliation for repository %s", repoName)
	return nil
}

// Helper methods for HTTP responses

func (qe *QueueEndpoints) writeErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	w.WriteHeader(statusCode)
	_, _ = fmt.Fprint(w, message)
}

func (qe *QueueEndpoints) writeJSONErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := map[string]any{
		"status":  "error",
		"message": message,
	}
	qe.writeJSONResponse(w, statusCode, response)
}

func (qe *QueueEndpoints) writeJSONResponse(w http.ResponseWriter, statusCode int, response map[string]any) {
	responseJSON, err := json.Marshal(response)
	if err != nil {
		qe.logger.Errorf("Failed to marshal JSON response: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"status": "error", "message": "Failed to marshal response"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write(responseJSON)
}
