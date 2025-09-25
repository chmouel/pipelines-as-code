package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// Client defines the interface for LLM providers.
type Client interface {
	Analyze(ctx context.Context, prompt string, context map[string]any) (string, error)
}

// NewClient creates a new LLM client based on provider type.
func NewClient(provider, apiKey string) (Client, error) {
	switch strings.ToLower(provider) {
	case "openai":
		return NewOpenAIClient(apiKey)
	case "gemini":
		return NewGeminiClient(apiKey)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", provider)
	}
}

// AnalyzeFailure performs LLM analysis on a failed pipeline run.
func AnalyzeFailure(ctx context.Context, run *params.Run, kinteract kubeinteraction.Interface, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, event *info.Event, config *v1alpha1.AIAnalysisConfig, namespace string) (string, error) {
	// Check if analysis should run
	if !shouldAnalyze(pr, config) {
		return "", nil
	}

	// Get API key from secret
	apiKey, err := getAPIKeyFromSecret(ctx, kinteract, config.SecretRef, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get API key: %w", err)
	}

	// Create LLM client
	client, err := NewClient(config.Provider, apiKey)
	if err != nil {
		return "", fmt.Errorf("failed to create LLM client: %w", err)
	}

	// Build context
	analysisContext := buildContext(pr, event)

	// Get prompt
	prompt := getPrompt(config)

	// Perform analysis
	result, err := client.Analyze(ctx, prompt, analysisContext)
	if err != nil {
		return "", fmt.Errorf("LLM analysis failed: %w", err)
	}

	logger.Infof("LLM analysis completed successfully for pipeline %s/%s", pr.Namespace, pr.Name)
	return result, nil
}

// shouldAnalyze determines if analysis should run based on pipeline status and config.
func shouldAnalyze(pr *tektonv1.PipelineRun, config *v1alpha1.AIAnalysisConfig) bool {
	if pr.Status.Conditions == nil || len(pr.Status.Conditions) == 0 {
		return false
	}

	// Check if pipeline failed
	isFailed := pr.Status.Conditions[0].Reason == "Failed"

	// If OnFailureOnly is not set or is true, only analyze failures
	onFailureOnly := config.OnFailureOnly == nil || *config.OnFailureOnly
	if onFailureOnly && !isFailed {
		return false
	}

	return true
}

// buildContext creates analysis context from pipeline run and event data.
func buildContext(pr *tektonv1.PipelineRun, event *info.Event) map[string]any {
	context := map[string]any{
		"pipeline_name": pr.Name,
		"namespace":     pr.Namespace,
	}

	// Add status information
	if pr.Status.Conditions != nil && len(pr.Status.Conditions) > 0 {
		context["status"] = pr.Status.Conditions[0].Reason
		context["message"] = pr.Status.Conditions[0].Message
	}

	// Add failed task information
	if failedTasks := getFailedTasks(pr); len(failedTasks) > 0 {
		context["failed_tasks"] = failedTasks
	}

	// Add PR information if available
	if event != nil && event.PullRequestNumber > 0 {
		context["pull_request"] = map[string]any{
			"number": event.PullRequestNumber,
			"title":  event.PullRequestTitle,
			"branch": event.HeadBranch,
		}
	}

	return context
}

// getFailedTasks extracts information about failed tasks from the pipeline run.
func getFailedTasks(pr *tektonv1.PipelineRun) []map[string]any {
	var failedTasks []map[string]any

	// For modern Tekton, we get basic info from child references
	// In a full implementation, we'd fetch the actual TaskRun objects for detailed info
	for _, childRef := range pr.Status.ChildReferences {
		if childRef.Kind == "TaskRun" {
			// We can only get basic info without fetching the actual TaskRun
			// This is simplified - in production we'd use the kubeinteraction to get full details
			failedTask := map[string]any{
				"name":          childRef.Name,
				"pipeline_task": childRef.PipelineTaskName,
			}
			failedTasks = append(failedTasks, failedTask)
		}
	}

	return failedTasks
}

// getPrompt returns the analysis prompt, using custom prompt if provided or default.
func getPrompt(config *v1alpha1.AIAnalysisConfig) string {
	if config.Prompt != "" {
		return config.Prompt
	}

	return `You are a DevOps expert analyzing a failed CI/CD pipeline. Based on the provided context, please:

1. Identify the root cause of the failure
2. Suggest specific steps to fix the issue  
3. Recommend preventive measures for the future

Focus on actionable insights and avoid generic advice. Be concise and practical.`
}

// getAPIKeyFromSecret retrieves the API key from a Kubernetes secret.
func getAPIKeyFromSecret(ctx context.Context, kinteract kubeinteraction.Interface, secretName, namespace string) (string, error) {
	opt := types.GetSecretOpt{
		Namespace: namespace,
		Name:      secretName,
		Key:       "token", // Standard key name
	}

	secretValue, err := kinteract.GetSecret(ctx, opt)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretName, err)
	}

	if secretValue == "" {
		return "", fmt.Errorf("secret %s/%s key 'token' is empty", namespace, secretName)
	}

	return secretValue, nil
}
