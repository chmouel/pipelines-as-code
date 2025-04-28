package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// healthHandler handles the health check endpoint
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "OK")
}

// handleListRepositoryRuns processes requests to list PipelineRuns for a repository
func (s *Server) handleListRepositoryRuns(w http.ResponseWriter, req MCPRequest) {
	// Extract parameters
	params := req.Params

	// Get required parameters
	owner, ok := params["owner"].(string)
	if !ok || owner == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Missing required parameter: owner", nil)
		return
	}

	repo, ok := params["repo"].(string)
	if !ok || repo == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Missing required parameter: repo", nil)
		return
	}

	// Optional parameters with defaults
	limit := 100
	if limitParam, ok := params["limit"].(float64); ok {
		limit = int(limitParam)
	}

	var statusFilter []string
	if statusParam, ok := params["status"].(string); ok && statusParam != "" {
		statusFilter = strings.Split(statusParam, ",")
	}

	var sinceUnix int64
	if sinceParam, ok := params["since"].(float64); ok {
		sinceUnix = int64(sinceParam)
	}

	ctx := context.Background()

	// Find the repository
	repoFullName := fmt.Sprintf("%s/%s", owner, repo)
	repository, err := matcher.GetRepo(ctx, s.run, repoFullName)
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeNotFound, "Repository not found", err.Error())
		return
	}

	// List PipelineRuns for this repository
	labelSelector := fmt.Sprintf("%s=%s", keys.Repository, formatting.CleanValueKubernetes(repoFullName))

	listOpts := metav1.ListOptions{
		LabelSelector: labelSelector,
		Limit:         int64(limit),
	}

	if sinceUnix > 0 {
		// If since parameter is provided, create a time-based selector
		sinceTime := time.Unix(sinceUnix, 0).Format(time.RFC3339)
		listOpts.FieldSelector = fmt.Sprintf("metadata.creationTimestamp>=%s", sinceTime)
	}

	prs, err := s.run.Clients.Tekton.TektonV1().PipelineRuns(repository.GetNamespace()).List(ctx, listOpts)
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeServerError, "Error listing PipelineRuns", err.Error())
		return
	}

	// Process and filter PipelineRuns
	runs, summary := s.processPipelineRuns(prs, statusFilter)

	// Send the response
	s.sendSuccessResponse(w, req.ID, map[string]interface{}{
		"runs":    runs,
		"summary": summary,
	})
}

// handleListRunTasks processes requests to list TaskRuns for a PipelineRun
func (s *Server) handleListRunTasks(w http.ResponseWriter, req MCPRequest) {
	// Extract parameters
	params := req.Params

	// Get required parameters
	runID, ok := params["runID"].(string)
	if !ok || runID == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Missing required parameter: runID", nil)
		return
	}

	// Parse namespace and name from runID (format: namespace:name)
	parts := strings.Split(runID, ":")
	if len(parts) != 2 {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Invalid runID format, expected 'namespace:name'", nil)
		return
	}

	namespace, name := parts[0], parts[1]
	ctx := context.Background()

	// Get the PipelineRun
	pr, err := s.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeNotFound, "PipelineRun not found", err.Error())
		return
	}

	// List TaskRuns for this PipelineRun
	trList, err := s.run.Clients.Tekton.TektonV1().TaskRuns(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("tekton.dev/pipelineRun=%s", pr.Name),
	})
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeServerError, "Error listing TaskRuns", err.Error())
		return
	}

	// Process TaskRuns
	taskRuns := s.processTaskRuns(trList)

	// Send the response
	s.sendSuccessResponse(w, req.ID, map[string]interface{}{
		"taskRuns": taskRuns,
	})
}

// handleGetTaskLogs processes requests to get logs for a TaskRun step (non-streaming)
func (s *Server) handleGetTaskLogs(w http.ResponseWriter, req MCPRequest) {
	// Extract parameters
	params := req.Params

	// Get required parameters
	taskID, ok := params["taskID"].(string)
	if !ok || taskID == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Missing required parameter: taskID", nil)
		return
	}

	step, ok := params["step"].(string)
	if !ok || step == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Missing required parameter: step", nil)
		return
	}

	// Optional parameters
	lines := 100
	if linesParam, ok := params["lines"].(float64); ok {
		lines = int(linesParam)
	}

	follow := false
	if followParam, ok := params["follow"].(bool); ok {
		follow = followParam
	}

	// Parse namespace and name from taskID (format: namespace:name)
	parts := strings.Split(taskID, ":")
	if len(parts) != 2 {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Invalid taskID format, expected 'namespace:name'", nil)
		return
	}

	namespace, name := parts[0], parts[1]
	ctx := context.Background()

	// Get the TaskRun
	tr, err := s.run.Clients.Tekton.TektonV1().TaskRuns(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeNotFound, "TaskRun not found", err.Error())
		return
	}

	if tr.Status.PodName == "" {
		s.sendErrorResponse(w, req.ID, ErrorCodeNotFound, "TaskRun has no associated pod", nil)
		return
	}

	// Check if the container/step exists
	containerFound := false
	for _, stepStatus := range tr.Status.Steps {
		if stepStatus.Container == step {
			containerFound = true
			break
		}
	}

	if !containerFound {
		s.sendErrorResponse(w, req.ID, ErrorCodeNotFound, fmt.Sprintf("Step '%s' not found in TaskRun", step), nil)
		return
	}

	// Set up log options
	logOpts := &corev1.PodLogOptions{
		Container: step,
		TailLines: &[]int64{int64(lines)}[0],
		Follow:    follow,
	}

	// Get logs
	podLogsReq := s.run.Clients.Kube.CoreV1().Pods(namespace).GetLogs(tr.Status.PodName, logOpts)
	podLogs, err := podLogsReq.Stream(ctx)
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeServerError, "Error getting logs", err.Error())
		return
	}
	defer podLogs.Close()

	// Read logs
	logs, err := io.ReadAll(podLogs)
	if err != nil {
		s.sendErrorResponse(w, req.ID, ErrorCodeServerError, "Error reading logs", err.Error())
		return
	}

	// Send the response
	s.sendSuccessResponse(w, req.ID, map[string]interface{}{
		"logs": string(logs),
	})
}

// handleStreamTaskLogs streams logs for a TaskRun step over WebSocket
func (s *Server) handleStreamTaskLogs(conn *websocket.Conn, req MCPStreamRequest) {
	// Extract parameters
	params := req.Params

	// Get required parameters
	taskID, ok := params["taskID"].(string)
	if !ok || taskID == "" {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeInvalidParameters, "Missing required parameter: taskID", nil)
		return
	}

	step, ok := params["step"].(string)
	if !ok || step == "" {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeInvalidParameters, "Missing required parameter: step", nil)
		return
	}

	// Parse namespace and name from taskID (format: namespace:name)
	parts := strings.Split(taskID, ":")
	if len(parts) != 2 {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeInvalidParameters, "Invalid taskID format, expected 'namespace:name'", nil)
		return
	}

	namespace, name := parts[0], parts[1]
	ctx := context.Background()

	// Get the TaskRun
	tr, err := s.run.Clients.Tekton.TektonV1().TaskRuns(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeNotFound, "TaskRun not found", err.Error())
		return
	}

	if tr.Status.PodName == "" {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeNotFound, "TaskRun has no associated pod", nil)
		return
	}

	// Check if the container/step exists
	containerFound := false
	for _, stepStatus := range tr.Status.Steps {
		if stepStatus.Container == step {
			containerFound = true
			break
		}
	}

	if !containerFound {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeNotFound, fmt.Sprintf("Step '%s' not found in TaskRun", step), nil)
		return
	}

	// Set up log options with follow enabled for streaming
	logOpts := &corev1.PodLogOptions{
		Container: step,
		Follow:    true,
	}

	// Get logs
	podLogsReq := s.run.Clients.Kube.CoreV1().Pods(namespace).GetLogs(tr.Status.PodName, logOpts)
	podLogs, err := podLogsReq.Stream(ctx)
	if err != nil {
		s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeServerError, "Error getting logs", err.Error())
		return
	}
	defer podLogs.Close()

	// Stream logs
	buffer := make([]byte, 4096)
	for {
		n, err := podLogs.Read(buffer)
		if err != nil {
			if err == io.EOF {
				// Send final message
				s.sendStreamResponse(conn, req.ID, req.StreamID, string(buffer[:n]), true)
				break
			}
			s.sendStreamErrorResponse(conn, req.ID, req.StreamID, ErrorCodeServerError, "Error reading logs", err)
			break
		}

		if n > 0 {
			// Send log chunk
			s.sendStreamResponse(conn, req.ID, req.StreamID, string(buffer[:n]), false)
		}
	}
}

// sendStreamResponse sends a success response over a WebSocket
func (s *Server) sendStreamResponse(conn *websocket.Conn, requestID string, streamID string, data string, isFinal bool) {
	resp := MCPStreamResponse{
		MCPResponse: MCPResponse{
			ID:     requestID,
			Type:   TypeResponse,
			Status: StatusSuccess,
			Data: map[string]interface{}{
				"logs": data,
			},
		},
		StreamID: streamID,
		IsFinal:  isFinal,
	}

	if err := conn.WriteJSON(resp); err != nil {
		s.logger.Errorf("Error sending WebSocket response: %v", err)
	}
}

// processPipelineRuns processes PipelineRuns into summaries
func (s *Server) processPipelineRuns(prs *tektonv1.PipelineRunList, statusFilter []string) ([]PipelineRunSummary, map[string]int) {
	runs := []PipelineRunSummary{}
	summary := map[string]int{
		"total":     len(prs.Items),
		"succeeded": 0,
		"failed":    0,
		"running":   0,
		"pending":   0,
		"cancelled": 0,
		"timedOut":  0,
		"unknown":   0,
	}

	// Define status filters map for quick lookup
	statusFilterMap := make(map[string]bool)
	for _, status := range statusFilter {
		statusFilterMap[strings.TrimSpace(status)] = true
	}

	for _, pr := range prs.Items {
		// Determine status
		status := determineRunStatus(&pr.Status)
		statusString := string(status)

		// Update summary counter
		if _, ok := summary[strings.ToLower(statusString)]; ok {
			summary[strings.ToLower(statusString)]++
		}

		// Apply status filter if any
		if len(statusFilterMap) > 0 && !statusFilterMap[statusString] {
			continue
		}

		// Create summary
		prSummary := PipelineRunSummary{
			UID:    string(pr.UID),
			Name:   pr.Name,
			Status: status,
		}

		// Process timestamps
		if pr.Status.StartTime != nil {
			startTime := pr.Status.StartTime.Time
			prSummary.StartTime = &startTime
		}
		if pr.Status.CompletionTime != nil {
			endTime := pr.Status.CompletionTime.Time
			prSummary.EndTime = &endTime
		}

		// Extract branch and SHA from labels/annotations
		if sha, ok := pr.Labels[keys.SHA]; ok {
			prSummary.SHA = sha
		}

		if branch, ok := pr.Annotations[keys.SourceBranch]; ok {
			prSummary.Branch = branch
		}

		// Get console URL if available
		if url, ok := pr.Annotations[keys.LogURL]; ok {
			prSummary.URL = url
		}

		runs = append(runs, prSummary)
	}

	return runs, summary
}

// processTaskRuns processes TaskRuns into summaries
func (s *Server) processTaskRuns(trs *tektonv1.TaskRunList) []TaskRunSummary {
	taskRuns := []TaskRunSummary{}

	for _, tr := range trs.Items {
		trSummary := TaskRunSummary{
			UID:     string(tr.UID),
			Name:    tr.Name,
			PodName: tr.Status.PodName,
		}

		// Process timestamps
		if tr.Status.StartTime != nil {
			startTime := tr.Status.StartTime.Time
			trSummary.StartTime = &startTime
		}
		if tr.Status.CompletionTime != nil {
			endTime := tr.Status.CompletionTime.Time
			trSummary.EndTime = &endTime
		}

		// Determine status
		status := determineRunStatus(&tr.Status)
		trSummary.Status = status

		// Get reason
		if len(tr.Status.Conditions) > 0 {
			trSummary.Reason = tr.Status.Conditions[0].Reason
		}

		// Process steps
		trSummary.Steps = processSteps(&tr)

		taskRuns = append(taskRuns, trSummary)
	}

	return taskRuns
}

// convertConditionToStatus converts condition status and reason to RunStatus
func convertConditionToStatus(status corev1.ConditionStatus, reason string) RunStatus {
	switch status {
	case corev1.ConditionTrue:
		return StatusSucceeded
	case corev1.ConditionFalse:
		if reason == "PipelineRunCancelled" || reason == "TaskRunCancelled" {
			return StatusCancelled
		}
		if reason == "PipelineRunTimeout" || reason == "TaskRunTimeout" {
			return StatusTimedOut
		}
		return StatusFailed
	case corev1.ConditionUnknown:
		if reason == "Running" {
			return StatusRunning
		}
		if reason == "Pending" {
			return StatusPending
		}
		return StatusUnknown
	default:
		return StatusUnknown
	}
}

// processSteps extracts step information from a TaskRun
func processSteps(tr *tektonv1.TaskRun) []Step {
	steps := []Step{}

	for _, stepStatus := range tr.Status.Steps {
		step := Step{
			Name:      stepStatus.Name,
			Container: stepStatus.Container,
		}

		// Process step status
		if stepStatus.Terminated != nil {
			exitCode := int(stepStatus.Terminated.ExitCode)
			step.ExitCode = &exitCode

			if stepStatus.Terminated.StartedAt.Time.Unix() > 0 {
				startTime := stepStatus.Terminated.StartedAt.Time
				step.StartTime = &startTime
			}

			if stepStatus.Terminated.FinishedAt.Time.Unix() > 0 {
				endTime := stepStatus.Terminated.FinishedAt.Time
				step.EndTime = &endTime
			}

			if exitCode == 0 {
				step.Status = StatusSucceeded
			} else {
				step.Status = StatusFailed
				step.ErrorMsg = stepStatus.Terminated.Message
			}
		} else if stepStatus.Running != nil {
			step.Status = StatusRunning

			if stepStatus.Running.StartedAt.Time.Unix() > 0 {
				startTime := stepStatus.Running.StartedAt.Time
				step.StartTime = &startTime
			}
		} else if stepStatus.Waiting != nil {
			step.Status = StatusPending
			step.ErrorMsg = stepStatus.Waiting.Message
		} else {
			step.Status = StatusUnknown
		}

		steps = append(steps, step)
	}

	return steps
}
