package llm

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	llmcontext "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/context"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apis "knative.dev/pkg/apis"
)

var (
	ansiControlPattern   = regexp.MustCompile(`(?:\x1b|␛)\[[0-?]*[ -/]*[@-~]`)
	ansiOSCPattern       = regexp.MustCompile(`\x1b\][^\x07]*(?:\x07|\x1b\\)`)
	checklistLinePattern = regexp.MustCompile(`^\[[ xX]\]\s+`)
)

// AnalysisResult represents the collected result of an LLM analysis role.
type AnalysisResult struct {
	Role     string
	Output   string
	Response *AnalysisResponse
	Patch    *MachinePatchMetadata
	Metadata map[string]string
	Error    error
}

// ExecuteAnalysis performs the async PipelineRun-based LLM analysis workflow.
func ExecuteAnalysis(
	ctx context.Context,
	run *params.Run,
	kinteract kubeinteraction.Interface,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
) error {
	if repo.Spec.Settings == nil || repo.Spec.Settings.AIAnalysis == nil || !repo.Spec.Settings.AIAnalysis.Enabled {
		logger.Debug("AI analysis not configured or disabled, skipping")
		return nil
	}

	config := repo.Spec.Settings.AIAnalysis
	if err := validateAnalysisConfig(config); err != nil {
		return fmt.Errorf("invalid AI analysis configuration: %w", err)
	}

	logger = logger.With(
		"pipeline_run", pr.Name,
		"namespace", pr.Namespace,
		"repository", repo.Name,
		"backend", config.Backend,
		"execution_mode", config.GetExecutionMode(),
	)

	// Collect completed analysis PipelineRuns first, before re-evaluating role triggers.
	// This prevents orphaning results when roles no longer match between
	// child PipelineRun creation and completion.
	analysisPRs, err := listAnalysisPipelineRuns(ctx, pr.Name, pr.Namespace, run)
	if err != nil {
		return fmt.Errorf("failed to list llm pipelineruns: %w", err)
	}
	analysisPRsByRole := map[string]*tektonv1.PipelineRun{}
	for i := range analysisPRs {
		role := analysisPRs[i].Annotations[keys.LLMRole]
		if role != "" {
			analysisPRsByRole[role] = &analysisPRs[i]
		}
	}

	for role, analysisPR := range analysisPRsByRole {
		if !analysisPR.IsDone() || isPipelineRunCollected(analysisPR) {
			continue
		}

		if err := markPipelineRunCollected(ctx, run, analysisPR); err != nil {
			return fmt.Errorf("failed to mark llm PipelineRun %s collected: %w", analysisPR.Name, err)
		}

		result, summary := parsePipelineRunEnvelope(ctx, run, analysisPR)
		if err := handleAnalysisResult(ctx, logger, repo, pr, event, prov, result); err != nil {
			return err
		}

		updatedPR, err := persistAnalysisSummary(ctx, run, logger, pr, role, summary)
		if err != nil {
			return fmt.Errorf("failed to persist llm summary for role %s: %w", role, err)
		}
		pr = updatedPR
		logger.Infof("Collected LLM PipelineRun result for role %s", role)
	}

	// Collect completed fix PipelineRuns
	fixPRs, err := listFixPipelineRuns(ctx, pr.Name, pr.Namespace, run)
	if err != nil {
		return fmt.Errorf("failed to list fix pipelineruns: %w", err)
	}
	for i := range fixPRs {
		fixPR := &fixPRs[i]
		if !fixPR.IsDone() || isPipelineRunCollected(fixPR) {
			continue
		}
		if err := markPipelineRunCollected(ctx, run, fixPR); err != nil {
			return fmt.Errorf("failed to mark fix PipelineRun %s collected: %w", fixPR.Name, err)
		}
		roleName := fixPR.Annotations[keys.LLMRole]
		result, _ := parsePipelineRunEnvelope(ctx, run, fixPR)
		if err := postFixCheckRun(ctx, result, pr, event, prov, logger); err != nil {
			logger.Warnf("Failed to post fix check run for role %s: %v", roleName, err)
		}
		logger.Infof("Collected fix PipelineRun result for role %s", roleName)
	}

	executions, err := buildRoleExecutions(ctx, run, kinteract, logger, repo, pr, event, prov)
	if err != nil {
		return fmt.Errorf("failed to prepare llm analysis: %w", err)
	}
	if len(executions) == 0 {
		logger.Debug("No LLM roles matched this PipelineRun")
		return nil
	}

	for _, exec := range executions {
		if _, ok := analysisPRsByRole[exec.Role.Name]; ok {
			continue
		}
		if err := createAnalysisPipelineRun(ctx, run, config, repo, pr, event, exec); err != nil {
			return fmt.Errorf("failed to create llm PipelineRun for role %s: %w", exec.Role.Name, err)
		}
		if exec.Role.GetOutput() == "check-run" {
			if err := postQueuedCheckRun(ctx, exec.Role.Name, pr.Name, event, prov, logger); err != nil {
				logger.With("role", exec.Role.Name, "error", err).Warn("Failed to create queued AI analysis check run")
			}
		}
		logger.Infof("Created LLM PipelineRun for role %s", exec.Role.Name)
	}

	return nil
}

func buildRoleExecutions(
	ctx context.Context,
	run *params.Run,
	kinteract kubeinteraction.Interface,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
) ([]roleExecution, error) {
	config := repo.Spec.Settings.AIAnalysis
	assembler := llmcontext.NewAssembler(run, kinteract, logger)

	celContext, err := assembler.BuildCELContext(pr, event, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to build CEL context: %w", err)
	}

	contextCache := make(map[string]map[string]any)
	executions := []roleExecution{}

	for _, role := range config.Roles {
		shouldTrigger, err := shouldTriggerRole(role, celContext, pr)
		if err != nil {
			logger.With("role", role.Name, "error", err).Warn("Skipping role because CEL evaluation failed")
			continue
		}
		if !shouldTrigger {
			continue
		}

		contextKey := getContextCacheKey(role.ContextItems)
		roleContext, cached := contextCache[contextKey]
		if !cached {
			roleContext, err = assembler.BuildContext(ctx, pr, event, role.ContextItems, prov)
			if err != nil {
				logger.With("role", role.Name, "error", err).Warn("Skipping role because context build failed")
				continue
			}
			contextCache[contextKey] = roleContext
		}

		request := &AnalysisRequest{
			Prompt:         role.Prompt,
			Context:        roleContext,
			MaxTokens:      maxTokensOrDefault(config.MaxTokens),
			TimeoutSeconds: timeoutSecondsOrDefault(config.TimeoutSeconds),
		}

		renderedPrompt, err := BuildPrompt(request)
		if err != nil {
			logger.With("role", role.Name, "error", err).Warn("Skipping role because prompt rendering failed")
			continue
		}

		executions = append(executions, roleExecution{
			Role:     role,
			Request:  request,
			Rendered: renderedPrompt,
		})
	}

	return executions, nil
}

func handleAnalysisResult(
	ctx context.Context,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
	result AnalysisResult,
) error {
	if result.Error != nil {
		logger.Warnf("Analysis failed for role %s: %v", result.Role, result.Error)
		return nil //nolint:nilerr
	}
	if result.Response == nil {
		logger.Warnf("No response for role %s", result.Role)
		return nil
	}

	var roleConfig *v1alpha1.AnalysisRole
	for i := range repo.Spec.Settings.AIAnalysis.Roles {
		if repo.Spec.Settings.AIAnalysis.Roles[i].Name == result.Role {
			roleConfig = &repo.Spec.Settings.AIAnalysis.Roles[i]
			break
		}
	}
	output := result.Output
	if output == "" && roleConfig != nil {
		output = roleConfig.GetOutput()
	}
	if output == "" {
		output = "pr-comment"
	}

	switch output {
	case "pr-comment":
		if err := postPRComment(ctx, result, event, prov, logger); err != nil {
			return fmt.Errorf("failed to publish llm result for role %s on PipelineRun %s/%s: %w", result.Role, pr.Namespace, pr.Name, err)
		}
		return nil
	case "check-run":
		if err := postCheckRun(ctx, result, pr, event, prov, logger); err != nil {
			return fmt.Errorf("failed to publish llm result for role %s on PipelineRun %s/%s: %w", result.Role, pr.Namespace, pr.Name, err)
		}
		return nil
	default:
		logger.Warnf("Unsupported output destination %q for role %s, skipping", output, result.Role)
		return nil
	}
}

// postPRComment posts LLM analysis as a PR comment.
func postPRComment(ctx context.Context, result AnalysisResult, event *info.Event, prov provider.Interface, logger *zap.SugaredLogger) error {
	if event.PullRequestNumber == 0 {
		logger.Debug("No pull request associated with this event, skipping PR comment")
		return nil
	}

	comment := formatAnalysisComment(result.Role, result.Response.Content)

	updateMarker := fmt.Sprintf("llm-analysis-%s", result.Role)

	if err := prov.CreateComment(ctx, event, comment, updateMarker); err != nil {
		return fmt.Errorf("failed to create PR comment: %w", err)
	}

	logger.Infof("Posted LLM analysis as PR comment for role %s", result.Role)
	return nil
}

// postCheckRun posts LLM analysis as a GitHub Check Run (GitHub App only).
func postCheckRun(
	ctx context.Context,
	result AnalysisResult,
	parentPR *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
	logger *zap.SugaredLogger,
) error {
	if event.InstallationID == 0 {
		logger.Infof("Skipping check-run output for role %s: not a GitHub App installation (InstallationID is 0)", result.Role)
		return nil
	}

	content := truncateForCheckRun(normalizeAnalysisContent(result.Response.Content))
	if content == "" {
		content = "_No analysis content produced._"
	}

	parentName := ""
	if parentPR != nil {
		parentName = parentPR.Name
	}

	statusOpts := analysisCheckRunStatusOpts(result.Role, parentName, event.SHA)
	statusOpts.Status = "completed"
	statusOpts.Conclusion = status.ConclusionNeutral
	statusOpts.Text = content
	if isMachinePatchValid(result.Patch) {
		statusOpts.Actions = []status.CheckRunAction{
			{
				Label:       "Fix it",
				Description: "Apply the AI-generated patch",
				Identifier:  "llm-fix",
			},
		}
	}

	if err := prov.CreateStatus(ctx, event, statusOpts); err != nil {
		return fmt.Errorf("failed to create check run for AI analysis role %s: %w", result.Role, err)
	}

	logger.Infof("Posted LLM analysis as check run for role %s", result.Role)
	return nil
}

// postFixCheckRun posts the result of a fix PipelineRun as a GitHub Check Run.
func postFixCheckRun(
	ctx context.Context,
	result AnalysisResult,
	parentPR *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
	logger *zap.SugaredLogger,
) error {
	if event.InstallationID == 0 {
		return nil
	}

	parentName := ""
	if parentPR != nil {
		parentName = parentPR.Name
	}

	statusOpts := FixCheckRunStatusOpts(parentName, result.Role, event.SHA)
	statusOpts.Status = "completed"

	switch {
	case result.Error != nil:
		statusOpts.Conclusion = status.ConclusionFailure
		statusOpts.Text = fmt.Sprintf("Fix failed: %v", result.Error)
	case result.Response != nil:
		statusOpts.Conclusion = status.ConclusionSuccess
		content := truncateForCheckRun(normalizeAnalysisContent(result.Response.Content))
		if content == "" {
			content = "_Fix completed with no output._"
		}
		statusOpts.Text = content
	default:
		statusOpts.Conclusion = status.ConclusionNeutral
		statusOpts.Text = "_No fix result available._"
	}

	if err := prov.CreateStatus(ctx, event, statusOpts); err != nil {
		return fmt.Errorf("failed to create fix check run for role %s: %w", result.Role, err)
	}

	logger.Infof("Posted fix check run for role %s", result.Role)

	if statusOpts.Conclusion == status.ConclusionSuccess && event.PullRequestNumber != 0 {
		comment := formatFixComment(result)
		if comment != "" {
			updateMarker := fmt.Sprintf("llm-fix-%s-%s", result.Role, event.SHA)
			if err := prov.CreateComment(ctx, event, comment, updateMarker); err != nil {
				return fmt.Errorf("failed to create fix PR comment for role %s: %w", result.Role, err)
			}
			logger.Infof("Posted fix PR comment for role %s", result.Role)
		}
	}

	return nil
}

func postQueuedCheckRun(
	ctx context.Context,
	roleName string,
	parentName string,
	event *info.Event,
	prov provider.Interface,
	logger *zap.SugaredLogger,
) error {
	if event.InstallationID == 0 {
		logger.Infof("Skipping queued check-run output for role %s: not a GitHub App installation (InstallationID is 0)", roleName)
		return nil
	}

	statusOpts := analysisCheckRunStatusOpts(roleName, parentName, event.SHA)
	statusOpts.Status = "queued"
	statusOpts.Conclusion = status.ConclusionPending
	statusOpts.Summary = "AI analysis has been scheduled."
	statusOpts.Text = fmt.Sprintf("Pipelines-as-Code is running AI analysis for role %q. Results will appear here when the analysis PipelineRun completes.", roleName)

	if err := prov.CreateStatus(ctx, event, statusOpts); err != nil {
		return fmt.Errorf("failed to create queued check run for AI analysis role %s: %w", roleName, err)
	}

	logger.Infof("Posted queued AI analysis check run for role %s", roleName)
	return nil
}

func analysisCheckRunStatusOpts(roleName, parentName, sha string) status.StatusOpts {
	return status.StatusOpts{
		PipelineRunName:         BuildExternalID(externalIDKindAnalysis, parentName, roleName, sha),
		OriginalPipelineRunName: analysisCheckRunName(roleName),
		Title:                   fmt.Sprintf("AI Analysis - %s", roleName),
		PipelineRun:             nil, // Don't patch any PipelineRun annotations
	}
}

func analysisCheckRunName(roleName string) string {
	return fmt.Sprintf("AI Analysis / %s", roleName)
}

func formatAnalysisComment(role, content string) string {
	body := normalizeAnalysisContent(content)
	if body == "" {
		body = "_No analysis content produced._"
	}

	return fmt.Sprintf("## 🤖 AI Analysis - %s\n\n%s\n\n---\n*Generated by Pipelines-as-Code LLM Analysis*", role, body)
}

func formatFixComment(result AnalysisResult) string {
	if result.Metadata == nil {
		return ""
	}

	commitShortSHA := result.Metadata["commit_short_sha"]
	if commitShortSHA == "" {
		commitShortSHA = result.Metadata["commit_sha"]
	}
	if commitShortSHA == "" {
		return ""
	}

	branch := result.Metadata["branch"]
	if branch == "" {
		branch = "the pull request branch"
	}

	changedFiles := strings.TrimSpace(result.Metadata["changed_files"])
	if changedFiles == "" {
		changedFiles = "_No changed files were reported._"
	} else {
		lines := strings.Split(changedFiles, "\n")
		formattedLines := make([]string, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			formattedLines = append(formattedLines, fmt.Sprintf("- `%s`", line))
		}
		if len(formattedLines) == 0 {
			changedFiles = "_No changed files were reported._"
		} else {
			changedFiles = strings.Join(formattedLines, "\n")
		}
	}

	return fmt.Sprintf("## AI Fix - %s\n\nPipelines-as-Code pushed fix commit %s to `%s`.\n\nFiles changed:\n%s\n\n---\n*Generated by Pipelines-as-Code AI Fix*", result.Role, commitShortSHA, branch, changedFiles)
}

func normalizeAnalysisContent(content string) string {
	cleaned := sanitizeTerminalOutput(content)
	lines := strings.Split(cleaned, "\n")
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if shouldSkipAnalysisLine(trimmed) {
			continue
		}
		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}

	body := strings.TrimSpace(strings.Join(filtered, "\n"))
	body = strings.TrimSuffix(body, "\n---")
	body = collapseBlankLines(body)

	return strings.TrimSpace(body)
}

func sanitizeTerminalOutput(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = ansiOSCPattern.ReplaceAllString(content, "")
	content = ansiControlPattern.ReplaceAllString(content, "")

	var builder strings.Builder
	builder.Grow(len(content))

	for _, r := range content {
		switch {
		case r == '\n' || r == '\t':
			builder.WriteRune(r)
		case r >= 0x20:
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

func shouldSkipAnalysisLine(line string) bool {
	if line == "" {
		return false
	}

	lower := strings.ToLower(line)

	switch {
	case strings.HasPrefix(line, "## 🤖 AI Analysis -"):
		return true
	case lower == "# todos", lower == "## todos":
		return true
	case checklistLinePattern.MatchString(line):
		return true
	case strings.HasPrefix(lower, "performing one time database migration"):
		return true
	case lower == "sqlite-migration:done":
		return true
	case lower == "database migration complete.":
		return true
	case strings.HasPrefix(line, "> ") && strings.Contains(line, "·"):
		return true
	case strings.HasPrefix(lower, "i'll analyze this"):
		return true
	case strings.HasPrefix(lower, "would you like me"):
		return true
	case strings.Contains(lower, "generated by pipelines-as-code llm analysis"):
		return true
	default:
		return false
	}
}

func truncateForCheckRun(content string) string {
	if len(content) <= MaxCheckRunOutputSize {
		return content
	}
	truncated := content[:MaxCheckRunOutputSize]
	// Walk back to the last valid UTF-8 boundary.
	for len(truncated) > 0 {
		r := truncated[len(truncated)-1]
		if r&0x80 == 0 || r&0xC0 == 0xC0 {
			break
		}
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + "\n\n_(response truncated to fit GitHub check-run limit)_"
}

func collapseBlankLines(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	lastBlank := false

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if lastBlank {
				continue
			}
			lastBlank = true
			result = append(result, "")
			continue
		}

		lastBlank = false
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// getContextCacheKey generates a unique key for a context configuration.
func getContextCacheKey(config *v1alpha1.ContextConfig) string {
	if config == nil {
		return "default"
	}
	maxLines := 0
	if config.ContainerLogs != nil {
		maxLines = config.ContainerLogs.GetMaxLines()
	}

	return fmt.Sprintf("commit:%t-pr:%t-error:%t-logs:%t-%d",
		config.CommitContent,
		config.PRContent,
		config.ErrorContent,
		config.ContainerLogs != nil && config.ContainerLogs.Enabled,
		maxLines,
	)
}

// shouldTriggerRole evaluates the CEL expression to determine if a role should be triggered.
// If no on_cel is provided, defaults to triggering only for failed PipelineRuns.
func shouldTriggerRole(role v1alpha1.AnalysisRole, celContext map[string]any, pr *tektonv1.PipelineRun) (bool, error) {
	if role.OnCEL == "" {
		succeededCondition := pr.Status.GetCondition(apis.ConditionSucceeded)
		return succeededCondition != nil && succeededCondition.Status == corev1.ConditionFalse, nil
	}

	result, err := cel.Value(role.OnCEL, celContext["body"],
		make(map[string]string),
		make(map[string]string),
		make(map[string]any))
	if err != nil {
		return false, fmt.Errorf("failed to evaluate CEL expression '%s': %w", role.OnCEL, err)
	}

	if boolVal, ok := result.Value().(bool); ok {
		return boolVal, nil
	}

	return false, fmt.Errorf("CEL expression '%s' did not return boolean value", role.OnCEL)
}

// validateAnalysisConfig validates the AI analysis configuration.
func validateAnalysisConfig(config *v1alpha1.AIAnalysisConfig) error {
	if config.GetExecutionMode() != string(ExecutionModePipelineRun) {
		return fmt.Errorf("execution mode %q is not supported", config.GetExecutionMode())
	}

	if config.Backend == "" {
		return fmt.Errorf("backend is required")
	}
	switch AgentBackend(config.Backend) {
	case BackendCodex, BackendClaude, BackendClaudeVertex, BackendGemini, BackendOpencode:
	default:
		return fmt.Errorf("backend %q is not supported", config.Backend)
	}

	if (AgentBackend(config.Backend) == BackendClaudeVertex || AgentBackend(config.Backend) == BackendOpencode) && config.VertexProjectID == "" {
		return fmt.Errorf("vertex_project_id is required when backend is %q", config.Backend)
	}

	if config.Image == "" {
		return fmt.Errorf("image is required")
	}

	if config.SecretRef == nil {
		return fmt.Errorf("secret reference is required")
	}
	if config.SecretRef.Name == "" {
		return fmt.Errorf("secret reference name is required")
	}

	if len(config.Roles) == 0 {
		return fmt.Errorf("at least one analysis role is required")
	}

	for i, role := range config.Roles {
		if role.Name == "" {
			return fmt.Errorf("role[%d]: name is required", i)
		}
		if role.Prompt == "" {
			return fmt.Errorf("role[%d]: prompt is required", i)
		}
		if role.GetOutput() != "pr-comment" && role.GetOutput() != "check-run" {
			return fmt.Errorf("role[%d]: invalid output destination '%s' (only 'pr-comment' and 'check-run' are currently supported)", i, role.GetOutput())
		}
	}

	return nil
}

func timeoutSecondsOrDefault(timeoutSeconds int) int {
	if timeoutSeconds == 0 {
		return DefaultTimeoutSeconds
	}
	return timeoutSeconds
}
