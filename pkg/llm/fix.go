package llm

import (
	"context"
	"crypto/sha256"
	_ "embed"
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
	}
}

func queuedFixCheckRunStatusOpts(parentName, roleName, sha string) status.StatusOpts {
	statusOpts := FixCheckRunStatusOpts(parentName, roleName, sha)
	statusOpts.Status = "queued"
	statusOpts.Conclusion = status.ConclusionPending
	statusOpts.Summary = "AI fix has been scheduled."
	statusOpts.Text = fmt.Sprintf("Pipelines-as-Code is applying the AI-generated patch for role %q. The final check run will report the pushed commit or the failure reason.", roleName)
	return statusOpts
}

// loadPatchForFix retrieves the machine patch from the original analysis child PipelineRun logs.
// It returns the patch metadata and the raw encoded payload (gzip+base64 string).
func loadPatchForFix(ctx context.Context, run *params.Run, namespace, parentName, roleName, sha string) (*MachinePatchMetadata, string, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s,%s=%s",
		keys.LLMAnalysis, formatting.CleanValueKubernetes("true"),
		keys.LLMParentPipelineRun, formatting.CleanValueKubernetes(parentName),
		keys.LLMRole, formatting.CleanValueKubernetes(roleName),
	)
	prs, err := run.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, "", fmt.Errorf("failed to list analysis PipelineRuns: %w", err)
	}
	if len(prs.Items) == 0 {
		return nil, "", fmt.Errorf("no analysis PipelineRun found for parent %s role %s", parentName, roleName)
	}

	// Use the first (should be only one per role).
	analysisPR := prs.Items[0]

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

	// Retrieve the stored machine patch from the original analysis child.
	patchMeta, encodedPayload, err := loadPatchForFix(ctx, run, repo.Namespace, parentName, roleName, event.SHA)
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
			statusOpts.Text = fmt.Sprintf("No machine patch is available for this analysis. %s\n\nPlease re-run analysis on the latest branch state.", reason)
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
			statusOpts.Text = fmt.Sprintf(
				"The stored patch is too large (%d bytes encoded) to embed inline. "+
					"Re-run analysis with a narrower scope or wait for a larger-transport implementation.",
				len(encodedPayload),
			)
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

	gitImage := run.Info.GetPacOpts().AIAnalysisGitImage
	pr := buildFixPipelineRun(config, repo, parentPR, event, roleName, encodedPayload, gitImage)
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

	logger.Infof("Created fix PipelineRun %s for role %s", pr.Name, roleName)
	return nil
}

func buildFixPipelineRun(
	config *v1alpha1.AIAnalysisConfig,
	repo *v1alpha1.Repository,
	parent *tektonv1.PipelineRun,
	event *info.Event,
	roleName string,
	encodedPayload string,
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
		{Name: "REPO_DIR", Value: sourceMountPath},
		{Name: "PR_BRANCH", Value: event.HeadBranch},
		{Name: "EXPECTED_SHA", Value: event.SHA},
		{Name: "ROLE_NAME", Value: roleName},
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
