package llm

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	externalIDSeparator    = "|"
	externalIDKindAnalysis = "llm-analysis"
	externalIDKindFix      = "llm-fix"
	fixCommitSubjectMaxLen = 72
	fixCommitBodyMaxLen    = 320

	coAuthorName  = "Pipelines as Code AI"
	coAuthorEmail = "noreply@pipelinesascode.dev"

	// maxInlinePatchBytes is the upper bound for a gzip+base64 patch payload that can
	// be safely embedded in a Tekton step script. Patches beyond this size cannot be
	// applied through the current inline transport; the user must re-run analysis with
	// a smaller scope.
	maxInlinePatchBytes = 512 * 1024
)

// ExternalIDParts holds the parsed components of a PAC AI check-run external ID.
type ExternalIDParts struct {
	Kind   string // "llm-analysis" or "llm-fix"
	Parent string // parent PipelineRun name
	Role   string // analysis role name
	SHA    string // commit SHA
}

// BuildExternalID constructs a pipe-separated external ID for AI check runs.
func BuildExternalID(kind, parent, role, sha string) string {
	return strings.Join([]string{kind, parent, role, sha}, externalIDSeparator)
}

// ParseExternalID parses a pipe-separated external ID into its components.
func ParseExternalID(externalID string) (ExternalIDParts, bool) {
	parts := strings.SplitN(externalID, externalIDSeparator, 4)
	if len(parts) != 4 {
		return ExternalIDParts{}, false
	}
	if parts[0] != externalIDKindAnalysis && parts[0] != externalIDKindFix {
		return ExternalIDParts{}, false
	}
	if parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return ExternalIDParts{}, false
	}
	return ExternalIDParts{
		Kind:   parts[0],
		Parent: parts[1],
		Role:   parts[2],
		SHA:    parts[3],
	}, true
}

// IsAnalysisExternalID returns true if the external ID is a valid analysis external ID.
func IsAnalysisExternalID(externalID string) bool {
	parsed, ok := ParseExternalID(externalID)
	return ok && parsed.Kind == externalIDKindAnalysis
}

// FixCheckRunStatusOpts returns base StatusOpts for a fix check run.
func FixCheckRunStatusOpts(parentName, roleName, sha string) status.StatusOpts {
	return status.StatusOpts{
		PipelineRunName:         BuildExternalID(externalIDKindFix, parentName, roleName, sha),
		OriginalPipelineRunName: fmt.Sprintf("AI Fix / %s", roleName),
		Title:                   fmt.Sprintf("AI Fix - %s", roleName),
		PipelineRun:             nil,
		SkipCheckRunReuse:       true,
	}
}

func queuedFixCheckRunStatusOpts(parentName, roleName, sha string) status.StatusOpts {
	statusOpts := FixCheckRunStatusOpts(parentName, roleName, sha)
	statusOpts.Status = "queued"
	statusOpts.Conclusion = status.ConclusionPending
	statusOpts.Summary = "Applying AI-suggested fix."
	statusOpts.Text = fmt.Sprintf("Applying the AI-suggested changes for the **%s** role. The result will appear here once the fix has been pushed.", roleName)
	return statusOpts
}

func buildFixCommitMessage(roleName string, metadata map[string]string) (string, string) {
	subject := sanitizeCommitMessageField(metadata["commit_subject"])
	body := sanitizeCommitMessageField(metadata["commit_body"])

	if subject == "" {
		subject = fmt.Sprintf("fix: address %s findings", roleName)
	}

	// When the AI puts subject+body on one line, split the overflow into body.
	if len(subject) > fixCommitSubjectMaxLen && body == "" {
		truncated := truncateCommitMessage(subject, fixCommitSubjectMaxLen)
		body = strings.TrimSpace(subject[len(truncated):])
		subject = truncated
	} else {
		subject = truncateCommitMessage(subject, fixCommitSubjectMaxLen)
	}

	if body == "" {
		body = fmt.Sprintf("Apply the AI-generated fix for role %q.", roleName)
	}
	if len(body) > fixCommitBodyMaxLen {
		body = truncateCommitMessage(body, fixCommitBodyMaxLen)
	}

	return subject, body
}

func sanitizeCommitMessageField(text string) string {
	if text == "" {
		return ""
	}

	lines := make([]string, 0, len(strings.Split(text, "\n")))
	inFence := false
	for rawLine := range strings.SplitSeq(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}

func truncateCommitMessage(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	cutoff := strings.LastIndex(text[:maxLen], " ")
	if cutoff <= 0 {
		return text[:maxLen]
	}
	return strings.TrimSpace(text[:cutoff])
}

func loadAnalysisPipelineRun(
	ctx context.Context,
	run *params.Run,
	namespace, parentName, roleName string,
) (*tektonv1.PipelineRun, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s,%s=%s",
		keys.LLMAnalysis, formatting.CleanValueKubernetes("true"),
		keys.LLMParentPipelineRun, formatting.CleanValueKubernetes(parentName),
		keys.LLMRole, formatting.CleanValueKubernetes(roleName),
	)
	prs, err := run.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list analysis PipelineRuns: %w", err)
	}
	if len(prs.Items) == 0 {
		return nil, fmt.Errorf("no analysis PipelineRun found for parent %s role %s", parentName, roleName)
	}

	return &prs.Items[0], nil
}

func loadAnalysisContentFromPipelineRun(
	ctx context.Context,
	run *params.Run,
	analysisPR *tektonv1.PipelineRun,
) (string, map[string]string, error) {
	if analysisPR == nil {
		return "", nil, fmt.Errorf("analysis PipelineRun is required")
	}

	result, _ := parsePipelineRunEnvelope(ctx, run, analysisPR)
	if result.Response == nil {
		if result.Error != nil {
			return "", nil, result.Error
		}
		return "", nil, fmt.Errorf("analysis result is unavailable")
	}
	return result.Response.Content, result.Metadata, nil
}

func analysisPipelineRunImage(pr *tektonv1.PipelineRun) string {
	if pr == nil || pr.Spec.PipelineSpec == nil {
		return ""
	}

	for _, task := range pr.Spec.PipelineSpec.Tasks {
		if task.TaskSpec == nil {
			continue
		}
		for _, step := range task.TaskSpec.Steps {
			if step.Name == "run-analysis" && step.Image != "" {
				return step.Image
			}
		}
	}

	return ""
}

func loadPatchForFixFromPipelineRun(
	ctx context.Context,
	run *params.Run,
	analysisPR *tektonv1.PipelineRun,
	namespace, roleName, sha string,
) (*MachinePatchMetadata, string, error) {
	if analysisPR == nil {
		return nil, "", fmt.Errorf("analysis PipelineRun is required")
	}

	logContent, err := fetchRawPodLogs(ctx, run, analysisPR.Name, namespace, "run-analysis", "step-run-analysis")
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch analysis pod logs: %w", err)
	}

	blocks, err := extractMachinePatchBlocks(logContent)
	if err != nil {
		return nil, "", fmt.Errorf("failed to extract patch blocks: %w", err)
	}
	if len(blocks) == 0 {
		return nil, "", fmt.Errorf("no machine patch found in analysis logs")
	}

	meta, payload, err := parseMachinePatch(blocks, roleName, sha)
	if err != nil {
		return nil, "", fmt.Errorf("invalid machine patch: %w", err)
	}

	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return nil, "", fmt.Errorf("patch payload is not valid base64: %w", err)
	}

	return meta, payload, nil
}

// buildFixScript generates the fix shell script with the encoded patch payload embedded.
func buildFixScript(encodedPayload string) string {
	preamble := fmt.Sprintf("#!/bin/sh\nPATCH_DATA_B64GZ=$(cat << 'ENDOFPATCH'\n%s\nENDOFPATCH\n)\n", encodedPayload)
	return preamble + fixScriptTemplateContent
}

// CreateFixPipelineRun creates a Tekton PipelineRun that applies the stored machine patch.
func CreateFixPipelineRun(
	ctx context.Context,
	run *params.Run,
	_ kubeinteraction.Interface,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	event *info.Event,
	prov provider.Interface,
) error {
	config := repo.Spec.Settings.AIAnalysis
	parentName := event.CheckRunParentPipelineRun
	roleName := event.CheckRunAnalysisRole

	analysisPR, err := loadAnalysisPipelineRun(ctx, run, repo.Namespace, parentName, roleName)
	if err != nil {
		analysisPR = nil
	}

	// Retrieve the stored machine patch from the original analysis child.
	patchMeta, encodedPayload, err := loadPatchForFixFromPipelineRun(ctx, run, analysisPR, repo.Namespace, roleName, event.SHA)
	if err != nil || !isMachinePatchValid(patchMeta) {
		reason := "no reusable machine patch is available"
		if err != nil {
			reason = err.Error()
		}
		logger.Warnf("Fix requested for role %s but patch unavailable: %s", roleName, reason)
		if event.InstallationID > 0 {
			statusOpts := FixCheckRunStatusOpts(parentName, roleName, event.SHA)
			statusOpts.Status = "completed"
			statusOpts.Conclusion = status.ConclusionNeutral
			statusOpts.Text = fmt.Sprintf("No automated fix is available for this analysis. %s\n\nTo apply suggestions manually, check the AI analysis output and re-run analysis on the latest branch state.", reason)
			if postErr := prov.CreateStatus(ctx, event, statusOpts); postErr != nil {
				logger.Warnf("Failed to post no-patch fix check run: %v", postErr)
			}
		}
		return nil
	}

	if len(encodedPayload) > maxInlinePatchBytes {
		logger.Warnf("Fix patch for role %s is too large (%d bytes), cannot embed inline", roleName, len(encodedPayload))
		if event.InstallationID > 0 {
			statusOpts := FixCheckRunStatusOpts(parentName, roleName, event.SHA)
			statusOpts.Status = "completed"
			statusOpts.Conclusion = status.ConclusionNeutral
			statusOpts.Text = "The suggested fix is too large to apply automatically. " +
				"Try re-running analysis with a narrower scope, or apply the suggestions manually."
			if postErr := prov.CreateStatus(ctx, event, statusOpts); postErr != nil {
				logger.Warnf("Failed to post oversized-patch fix check run: %v", postErr)
			}
		}
		return nil
	}

	parentPR, err := run.Clients.Tekton.TektonV1().PipelineRuns(repo.Namespace).Get(ctx, parentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot find parent PipelineRun %s: %w", parentName, err)
	}
	_, analysisMetadata, err := loadAnalysisContentFromPipelineRun(ctx, run, analysisPR)
	if err != nil {
		logger.Warnf("Failed to load analysis content for fix commit message on role %s: %v", roleName, err)
	}
	if analysisMetadata == nil {
		analysisMetadata = map[string]string{}
	}
	commitSubject, commitBody := buildFixCommitMessage(roleName, analysisMetadata)

	fixConfig := *config
	if fixImage := analysisPipelineRunImage(analysisPR); fixImage != "" {
		fixConfig.Image = fixImage
	}

	gitImage := run.Info.GetPacOpts().AIAnalysisGitImage
	pr := buildFixPipelineRun(&fixConfig, repo, parentPR, event, roleName, encodedPayload, commitSubject, commitBody, gitImage)
	_, err = run.Clients.Tekton.TektonV1().PipelineRuns(repo.Namespace).Create(ctx, pr, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		logger.Infof("Fix PipelineRun already exists for role %s, skipping", roleName)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create fix PipelineRun: %w", err)
	}

	if event.InstallationID > 0 {
		statusOpts := queuedFixCheckRunStatusOpts(parentName, roleName, event.SHA)
		if err := prov.CreateStatus(ctx, event, statusOpts); err != nil {
			logger.Warnf("Failed to create queued fix check run: %v", err)
		}
	}

	consoleURL := run.Clients.ConsoleUI().DetailURL(pr)
	logger.Infof("Created fix PipelineRun %s in namespace %s for role %s: %s",
		pr.Name, repo.Namespace, roleName, consoleURL)
	return nil
}

func buildFixPipelineRun(
	config *v1alpha1.AIAnalysisConfig,
	repo *v1alpha1.Repository,
	parent *tektonv1.PipelineRun,
	event *info.Event,
	roleName string,
	encodedPayload string,
	commitSubject string,
	commitBody string,
	gitImage string,
) *tektonv1.PipelineRun {
	timeoutSeconds := config.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}

	prName := fixPipelineRunName(parent.Name, roleName)
	timeout := metav1.Duration{Duration: time.Duration(timeoutSeconds) * time.Second}

	repoURL := event.URL
	if event.CloneURL != "" {
		repoURL = event.CloneURL
	}

	fixEnv := []corev1.EnvVar{
		{Name: "REPO_URL", Value: repoURL},
		{Name: "PR_NUMBER", Value: fmt.Sprintf("%d", event.PullRequestNumber)},
		{Name: "REPO_DIR", Value: sourceMountPath},
		{Name: "PR_BRANCH", Value: event.HeadBranch},
		{Name: "EXPECTED_SHA", Value: event.SHA},
		{Name: "ROLE_NAME", Value: roleName},
		{Name: "FIX_COMMIT_SUBJECT_B64", Value: base64.StdEncoding.EncodeToString([]byte(commitSubject))},
		{Name: "FIX_COMMIT_BODY_B64", Value: base64.StdEncoding.EncodeToString([]byte(commitBody))},
		// AI co-author (always)
		{Name: "GIT_COAUTHOR_NAME", Value: coAuthorName},
		{Name: "GIT_COAUTHOR_EMAIL", Value: coAuthorEmail},
		// Human author (only when trustworthy)
		{Name: "GIT_AUTHOR_NAME", Value: event.SHAAuthorName},
		{Name: "GIT_AUTHOR_EMAIL", Value: event.SHAAuthorEmail},
	}

	workspaces := buildChildPipelineRunWorkspaces(parent)
	cloneStep := buildCloneStep(repoURL, event.HeadBranch, gitImage)

	// Single task so the shell checkout and fix script share the EmptyDir workspace.
	fixTaskSpec := tektonv1.TaskSpec{
		Workspaces: workspaces.task,
		Results:    []tektonv1.TaskResult{{Name: analysisResultName, Type: tektonv1.ResultsTypeString}},
		Steps: []tektonv1.Step{
			cloneStep,
			{
				Name:       "run-fix",
				Image:      config.Image,
				WorkingDir: sourceMountPath,
				Env:        fixEnv,
				Script:     buildFixScript(encodedPayload),
			},
		},
	}

	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prName,
			Namespace: parent.Namespace,
			Labels: map[string]string{
				keys.LLMFix:               formatting.CleanValueKubernetes("true"),
				keys.LLMParentPipelineRun: formatting.CleanValueKubernetes(parent.Name),
				keys.LLMRole:              formatting.CleanValueKubernetes(roleName),
				keys.Repository:           formatting.CleanValueKubernetes(repo.Name),
			},
			Annotations: map[string]string{
				keys.LLMFix:               "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMRole:              roleName,
				keys.Repository:           repo.Name,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Timeouts:   &tektonv1.TimeoutFields{Pipeline: &timeout},
			Workspaces: workspaces.pipelineRun,
			PipelineSpec: &tektonv1.PipelineSpec{
				Workspaces: workspaces.pipeline,
				Results: []tektonv1.PipelineResult{
					{
						Name:  analysisResultName,
						Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "$(tasks.fix.results.analysis)"},
					},
				},
				Tasks: []tektonv1.PipelineTask{
					{
						Name:       "fix",
						TaskSpec:   &tektonv1.EmbeddedTask{TaskSpec: fixTaskSpec},
						Workspaces: workspaces.pipelineTask,
					},
				},
			},
		},
	}
}

func fixPipelineRunName(parentName, roleName string) string {
	base := fmt.Sprintf("%s-%s-fix", parentName, roleName)
	hash := sha256.Sum256([]byte(base))
	suffix := hex.EncodeToString(hash[:])[:8]
	prefix := formatting.CleanValueKubernetes(strings.ToLower(parentName))
	role := formatting.CleanValueKubernetes(strings.ToLower(roleName))
	name := fmt.Sprintf("%s-fix-%s-%s", prefix, role, suffix)
	if len(name) <= 63 {
		return name
	}
	name = name[len(name)-63:]
	name = strings.TrimLeft(name, "-")
	return name
}

// listFixPipelineRuns lists fix PipelineRuns for a given parent.
func listFixPipelineRuns(
	ctx context.Context,
	runName, namespace string,
	run *params.Run,
) ([]tektonv1.PipelineRun, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s",
		keys.LLMFix, formatting.CleanValueKubernetes("true"),
		keys.LLMParentPipelineRun, formatting.CleanValueKubernetes(runName))
	prs, err := run.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	return prs.Items, nil
}

//go:embed templates/fix.sh
var fixScriptTemplateContent string
