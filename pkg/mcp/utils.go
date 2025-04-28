package mcp

import (
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	v1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

// createPipelineRunListResponse processes PipelineRuns into a structured response
func (s *Server) createPipelineRunListResponse(prs *tektonv1.PipelineRunList, statusFilter string) PipelineRunListResponse {
	runs := []PipelineRunSummary{}
	summary := StatusSummary{}

	// Initialize counts
	summary.Total = len(prs.Items)

	// Define status filters
	statusFilters := []RunStatus{}
	if statusFilter != "" {
		for _, status := range strings.Split(statusFilter, ",") {
			statusFilters = append(statusFilters, RunStatus(status))
		}
	}

	// Process each PipelineRun
	for _, pr := range prs.Items {
		// Create the summary
		prSummary := PipelineRunSummary{
			UID:  string(pr.UID),
			Name: pr.Name,
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

		// Extract branch and SHA from labels
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

		// Determine status
		prStatus := determineRunStatus(&pr)
		prSummary.Status = prStatus

		// Update summary counters
		updateStatusSummary(&summary, prStatus)

		// Apply status filters if any
		if len(statusFilters) > 0 {
			matched := false
			for _, filter := range statusFilters {
				if prStatus == filter {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		runs = append(runs, prSummary)
	}

	return PipelineRunListResponse{
		Runs:    runs,
		Summary: summary,
	}
}

// createTaskRunListResponse processes TaskRuns into a structured response
func (s *Server) createTaskRunListResponse(trs *tektonv1.TaskRunList) TaskRunListResponse {
	taskRuns := []TaskRunSummary{}

	for _, tr := range trs.Items {
		// Create the summary
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
		trStatus := determineRunStatus(&tr)
		trSummary.Status = trStatus

		// Get reason
		if len(tr.Status.Conditions) > 0 {
			trSummary.Reason = tr.Status.Conditions[0].Reason
		}

		// Process steps
		trSummary.Steps = extractSteps(&tr)

		taskRuns = append(taskRuns, trSummary)
	}

	return TaskRunListResponse{
		TaskRuns: taskRuns,
	}
}

// updateStatusSummary updates the status summary counters
func updateStatusSummary(summary *StatusSummary, status RunStatus) {
	switch status {
	case RunStatusSucceeded:
		summary.Succeeded++
	case RunStatusFailed:
		summary.Failed++
	case RunStatusCancelled:
		summary.Cancelled++
	case RunStatusTimedOut:
		summary.TimedOut++
	case RunStatusRunning:
		summary.Running++
	case RunStatusQueued:
		summary.Queued++
	case RunStatusPending:
		summary.Pending++
	}
}

// determineRunStatus determines the status of a PipelineRun or TaskRun
func determineRunStatus(obj interface{}) RunStatus {
	var condition *apis.Condition

	switch o := obj.(type) {
	case *tektonv1.PipelineRun:
		if len(o.Status.Conditions) > 0 {
			condition = &o.Status.Conditions[0]
		}
		if o.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
			return RunStatusPending
		}
		// Check for queued state annotation
		if state, ok := o.Annotations[keys.State]; ok && state == "queued" {
			return RunStatusQueued
		}
	case *tektonv1.TaskRun:
		if len(o.Status.Conditions) > 0 {
			condition = &o.Status.Conditions[0]
		}
	}

	if condition == nil {
		return RunStatusUnknown
	}

	switch condition.Status {
	case v1.ConditionTrue:
		return RunStatusSucceeded
	case v1.ConditionFalse:
		// Check specific failure reasons
		if condition.Reason == "PipelineRunCancelled" || condition.Reason == "TaskRunCancelled" {
			return RunStatusCancelled
		} else if condition.Reason == "PipelineRunTimeout" || condition.Reason == "TaskRunTimeout" {
			return RunStatusTimedOut
		}
		return RunStatusFailed
	case v1.ConditionUnknown:
		// If reason is "Running", consider it running
		if condition.Reason == "Running" {
			return RunStatusRunning
		} else if condition.Reason == "Pending" {
			return RunStatusPending
		}
		return RunStatusUnknown
	default:
		return RunStatusUnknown
	}
}

// extractSteps extracts step information from a TaskRun
func extractSteps(tr *tektonv1.TaskRun) []Step {
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
				step.Status = RunStatusSucceeded
			} else {
				step.Status = RunStatusFailed
				step.ErrorMsg = stepStatus.Terminated.Message
			}
		} else if stepStatus.Running != nil {
			step.Status = RunStatusRunning

			if stepStatus.Running.StartedAt.Time.Unix() > 0 {
				startTime := stepStatus.Running.StartedAt.Time
				step.StartTime = &startTime
			}
		} else if stepStatus.Waiting != nil {
			step.Status = RunStatusPending
			step.ErrorMsg = stepStatus.Waiting.Message
		} else {
			step.Status = RunStatusUnknown
		}

		steps = append(steps, step)
	}

	return steps
}
