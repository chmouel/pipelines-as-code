package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// Processor handles LLM-based comment processing and pipeline matching.
type Processor struct {
	client *Client
	logger *zap.SugaredLogger
}

// NewProcessor creates a new LLM processor.
func NewProcessor(client *Client, logger *zap.SugaredLogger) *Processor {
	return &Processor{
		client: client,
		logger: logger,
	}
}

// ProcessLLMComment processes an LLM comment and returns the intended pipeline actions.
func (p *Processor) ProcessLLMComment(ctx context.Context, comment string, event *info.Event, availablePipelines []*tektonv1.PipelineRun) (*Result, error) {
	// Extract the LLM command from the comment
	llmCommand := opscomments.GetLLMCommand(comment)
	if llmCommand == "" {
		return nil, fmt.Errorf("failed to extract LLM command from comment: %s", comment)
	}

	// Convert available pipelines to PipelineInfo for LLM analysis
	pipelineInfos := p.convertPipelinesToInfo(availablePipelines)

	// Create analysis request
	req := &AnalysisRequest{
		UserComment:        llmCommand,
		AvailablePipelines: pipelineInfos,
		Repository:         event.Repository,
		Organization:       event.Organization,
		EventType:          event.EventType,
	}

	// Analyze the comment with LLM
	resp, err := p.client.AnalyzeComment(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze comment with LLM: %w", err)
	}

	// Convert LLM response to pipeline actions
	result := &Result{
		OriginalCommand: llmCommand,
		Action:          resp.Action,
		Confidence:      resp.Confidence,
		Explanation:     resp.Explanation,
		QueryResponse:   resp.QueryResponse,
		TargetPipelines: []*tektonv1.PipelineRun{},
	}

	// Find matching pipelines based on LLM analysis
	if len(resp.TargetPipelines) == 0 {
		// Target all pipelines
		result.TargetPipelines = availablePipelines
	} else {
		// Target specific pipelines
		for _, targetName := range resp.TargetPipelines {
			for _, pipeline := range availablePipelines {
				if p.matchesPipelineName(pipeline, targetName) {
					result.TargetPipelines = append(result.TargetPipelines, pipeline)
					break
				}
			}
		}
	}

	p.logger.Infof("LLM analysis result: action=%s, confidence=%.2f, explanation=%s, target_pipelines=%d, has_query_response=%t",
		result.Action, result.Confidence, result.Explanation, len(result.TargetPipelines), result.QueryResponse != "")

	return result, nil
}

// Result represents the result of LLM comment processing.
type Result struct {
	OriginalCommand string                  `json:"original_command"`
	Action          string                  `json:"action"`           // "test", "retest", "cancel", "query", "unknown"
	Confidence      float64                 `json:"confidence"`       // 0-1 confidence score
	Explanation     string                  `json:"explanation"`      // LLM explanation
	QueryResponse   string                  `json:"query_response"`   // Response for informational queries
	TargetPipelines []*tektonv1.PipelineRun `json:"target_pipelines"` // pipelines to act on
}

// convertPipelinesToInfo converts PipelineRun objects to PipelineInfo for LLM analysis.
func (p *Processor) convertPipelinesToInfo(pipelines []*tektonv1.PipelineRun) []PipelineInfo {
	infos := []PipelineInfo{}

	for _, pipeline := range pipelines {
		info := PipelineInfo{
			Name:        p.getPipelineName(pipeline),
			Description: p.getPipelineDescription(pipeline),
			Annotations: pipeline.GetAnnotations(),
			Labels:      pipeline.GetLabels(),
			Tasks:       p.extractTaskNames(pipeline),
		}
		infos = append(infos, info)
	}

	return infos
}

// getPipelineName returns the name of the pipeline.
func (p *Processor) getPipelineName(pipeline *tektonv1.PipelineRun) string {
	name := pipeline.GetGenerateName()
	if name == "" {
		name = pipeline.GetName()
	}
	// Remove trailing dash from generateName
	return strings.TrimSuffix(name, "-")
}

// getPipelineDescription extracts a description from pipeline annotations or generates one.
func (p *Processor) getPipelineDescription(pipeline *tektonv1.PipelineRun) string {
	annotations := pipeline.GetAnnotations()

	// Check for event annotations to generate description
	var events []string
	if event, ok := annotations[keys.OnEvent]; ok {
		events = append(events, event)
	}
	if targetBranch, ok := annotations[keys.OnTargetBranch]; ok {
		events = append(events, fmt.Sprintf("branch: %s", targetBranch))
	}

	if len(events) > 0 {
		return fmt.Sprintf("Pipeline for %s", strings.Join(events, ", "))
	}

	return "Pipeline for CI/CD"
}

// extractTaskNames extracts task names from the pipeline.
func (p *Processor) extractTaskNames(pipeline *tektonv1.PipelineRun) []string {
	var tasks []string

	// Extract from PipelineSpec if available
	if pipeline.Spec.PipelineSpec != nil {
		for _, task := range pipeline.Spec.PipelineSpec.Tasks {
			if task.TaskRef != nil {
				tasks = append(tasks, task.TaskRef.Name)
			}
		}
	}

	// Extract from annotations
	if taskAnnotation, ok := pipeline.GetAnnotations()[keys.Task]; ok {
		tasks = append(tasks, taskAnnotation)
	}

	return tasks
}

// matchesPipelineName checks if a pipeline name matches the target name.
func (p *Processor) matchesPipelineName(pipeline *tektonv1.PipelineRun, targetName string) bool {
	pipelineName := p.getPipelineName(pipeline)

	// Exact match
	if pipelineName == targetName {
		return true
	}

	// Case-insensitive match
	if strings.EqualFold(pipelineName, targetName) {
		return true
	}

	// Partial match (e.g., "go-test" matches "go-test-pipeline")
	if strings.Contains(strings.ToLower(pipelineName), strings.ToLower(targetName)) {
		return true
	}

	return false
}
