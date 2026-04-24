package llm

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	apis "knative.dev/pkg/apis"
)

const (
	analysisResultName  = "analysis"
	collectedValue      = "true"
	sourceWorkspaceName = "source"
	sourceMountPath     = "/workspace/source"
	gcpCredsVolumeName  = "gcp-creds"            //nolint:gosec
	gcpCredsMountPath   = "/workspace/gcp-creds" //nolint:gosec
)

type roleExecution struct {
	Role     v1alpha1.AnalysisRole
	Request  *AnalysisRequest
	Rendered string
}

func listAnalysisPipelineRuns(
	ctx context.Context,
	runName, namespace string,
	run *params.Run,
) ([]tektonv1.PipelineRun, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s",
		keys.LLMAnalysis, formatting.CleanValueKubernetes("true"),
		keys.LLMParentPipelineRun, formatting.CleanValueKubernetes(runName))
	prs, err := run.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, err
	}
	return prs.Items, nil
}

func createAnalysisPipelineRun(
	ctx context.Context,
	run *params.Run,
	config *v1alpha1.AIAnalysisConfig,
	repo *v1alpha1.Repository,
	parent *tektonv1.PipelineRun,
	event *info.Event,
	exec roleExecution,
) error {
	gitImage := run.Info.GetPacOpts().AIAnalysisGitImage
	pr := buildAnalysisPipelineRun(config, repo, parent, event, exec, gitImage)
	_, err := run.Clients.Tekton.TektonV1().PipelineRuns(parent.Namespace).Create(ctx, pr, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func buildAnalysisPipelineRun(
	config *v1alpha1.AIAnalysisConfig,
	repo *v1alpha1.Repository,
	parent *tektonv1.PipelineRun,
	event *info.Event,
	exec roleExecution,
	gitImage string,
) *tektonv1.PipelineRun {
	timeoutSeconds := config.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}

	backend := string(AgentBackend(config.Backend))
	roleName := exec.Role.Name
	output := exec.Role.GetOutput()
	model := exec.Role.GetModel()

	prName := analysisPipelineRunName(parent.Name, roleName)
	timeout := metav1.Duration{Duration: time.Duration(timeoutSeconds) * time.Second}
	promptB64 := base64.StdEncoding.EncodeToString([]byte(exec.Rendered))

	repoURL := event.URL
	if event.CloneURL != "" {
		repoURL = event.CloneURL
	}

	fetchTaskSpec := buildFetchRepoTaskSpec(repoURL, event.SHA, gitImage, parent)

	analysisEnv, analysisVolumes, analysisVolumeMounts := buildAnalysisEnv(config)
	cliTimeout := timeoutSeconds - 60
	if cliTimeout < 60 {
		cliTimeout = 60
	}
	analysisEnv = append(analysisEnv,
		corev1.EnvVar{Name: "LLM_BACKEND", Value: backend},
		corev1.EnvVar{Name: "LLM_MODEL", Value: model},
		corev1.EnvVar{Name: "LLM_MAX_TOKENS", Value: fmt.Sprintf("%d", maxTokensOrDefault(config.MaxTokens))},
		corev1.EnvVar{Name: "LLM_PROMPT_B64", Value: promptB64},
		corev1.EnvVar{Name: "LLM_TIMEOUT_SECONDS", Value: fmt.Sprintf("%d", cliTimeout)},
	)

	analysisTaskSpec := tektonv1.TaskSpec{
		Workspaces: []tektonv1.WorkspaceDeclaration{{Name: sourceWorkspaceName}},
		Results:    []tektonv1.TaskResult{{Name: analysisResultName, Type: tektonv1.ResultsTypeString}},
		Volumes:    analysisVolumes,
		Steps: []tektonv1.Step{
			{
				Name:         "run-analysis",
				Image:        config.Image,
				WorkingDir:   sourceMountPath,
				VolumeMounts: analysisVolumeMounts,
				Env:          analysisEnv,
				Script:       analysisScriptContent,
			},
		},
	}

	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prName,
			Namespace: parent.Namespace,
			Labels: map[string]string{
				keys.LLMAnalysis:          formatting.CleanValueKubernetes("true"),
				keys.LLMParentPipelineRun: formatting.CleanValueKubernetes(parent.Name),
				keys.LLMBackend:           formatting.CleanValueKubernetes(backend),
				keys.LLMRole:              formatting.CleanValueKubernetes(roleName),
				keys.LLMOutput:            formatting.CleanValueKubernetes(output),
				keys.Repository:           formatting.CleanValueKubernetes(repo.Name),
			},
			Annotations: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMBackend:           backend,
				keys.LLMRole:              roleName,
				keys.LLMOutput:            output,
				keys.Repository:           repo.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(parent, schema.GroupVersionKind{
					Group:   "tekton.dev",
					Version: "v1",
					Kind:    "PipelineRun",
				}),
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Timeouts: &tektonv1.TimeoutFields{Pipeline: &timeout},
			Workspaces: []tektonv1.WorkspaceBinding{
				{Name: sourceWorkspaceName, EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
			PipelineSpec: &tektonv1.PipelineSpec{
				Workspaces: []tektonv1.PipelineWorkspaceDeclaration{{Name: sourceWorkspaceName}},
				Results: []tektonv1.PipelineResult{
					{
						Name:  analysisResultName,
						Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "$(tasks.run-analysis.results.analysis)"},
					},
				},
				Tasks: []tektonv1.PipelineTask{
					{
						Name:     "fetch-repo",
						TaskSpec: &tektonv1.EmbeddedTask{TaskSpec: *fetchTaskSpec},
						Workspaces: []tektonv1.WorkspacePipelineTaskBinding{
							{Name: sourceWorkspaceName, Workspace: sourceWorkspaceName},
						},
					},
					{
						Name:     "run-analysis",
						RunAfter: []string{"fetch-repo"},
						TaskSpec: &tektonv1.EmbeddedTask{TaskSpec: analysisTaskSpec},
						Workspaces: []tektonv1.WorkspacePipelineTaskBinding{
							{Name: sourceWorkspaceName, Workspace: sourceWorkspaceName},
						},
					},
				},
			},
		},
	}
}

func buildFetchRepoTaskSpec(repoURL, sha, cloneImage string, parent *tektonv1.PipelineRun) *tektonv1.TaskSpec {
	script := `#!/bin/sh
set -eu
if [ -d /workspace/basic-auth ]; then
    cp /workspace/basic-auth/.gitconfig "${HOME}/.gitconfig" 2>/dev/null || true
    cp /workspace/basic-auth/.git-credentials "${HOME}/.git-credentials" 2>/dev/null || true
    chmod 600 "${HOME}/.git-credentials" 2>/dev/null || true
fi
git init "${OUTPUT_PATH}"
cd "${OUTPUT_PATH}"
git remote add origin "${REPO_URL}"
git fetch --depth=1 origin "${REVISION}" 2>/dev/null || git fetch origin "${REVISION}"
git checkout FETCH_HEAD
`

	env := []corev1.EnvVar{
		{Name: "REPO_URL", Value: repoURL},
		{Name: "REVISION", Value: sha},
		{Name: "OUTPUT_PATH", Value: sourceMountPath},
	}

	volumes := []corev1.Volume{}
	volumeMounts := []corev1.VolumeMount{}

	if gitAuthSecret := parent.Annotations[keys.GitAuthSecret]; gitAuthSecret != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "basic-auth",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: gitAuthSecret},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "basic-auth",
			MountPath: "/workspace/basic-auth",
		})
	}

	return &tektonv1.TaskSpec{
		Workspaces: []tektonv1.WorkspaceDeclaration{{Name: sourceWorkspaceName}},
		Volumes:    volumes,
		Steps: []tektonv1.Step{
			{
				Name:         "clone",
				Image:        cloneImage,
				Script:       script,
				Env:          env,
				VolumeMounts: volumeMounts,
			},
		},
	}
}

// buildAnalysisEnv returns the env vars, extra volumes, and extra volume mounts
// for the run-analysis step based on the configured backend.
func buildAnalysisEnv(config *v1alpha1.AIAnalysisConfig) ([]corev1.EnvVar, []corev1.Volume, []corev1.VolumeMount) {
	backend := AgentBackend(config.Backend)

	if backend == BackendClaudeVertex || backend == BackendOpencode {
		secretKey := secretKeyOrDefault(config.SecretRef)
		credsPath := fmt.Sprintf("%s/%s", gcpCredsMountPath, secretKey)
		envVars := []corev1.EnvVar{
			{Name: "CLOUD_ML_REGION", Value: config.GetVertexRegion()},
			{Name: "VERTEX_LOCATION", Value: "global"},
			{Name: "GOOGLE_CLOUD_PROJECT", Value: config.VertexProjectID},
			{Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: credsPath},
		}
		// claude-vertex needs additional Claude-specific env vars
		if backend == BackendClaudeVertex {
			envVars = append(envVars,
				corev1.EnvVar{Name: "CLAUDE_CODE_USE_VERTEX", Value: "1"},
				corev1.EnvVar{Name: "ANTHROPIC_VERTEX_PROJECT_ID", Value: config.VertexProjectID},
			)
		}
		vols := []corev1.Volume{
			{
				Name: gcpCredsVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: config.SecretRef.Name},
				},
			},
		}
		mounts := []corev1.VolumeMount{
			{Name: gcpCredsVolumeName, MountPath: gcpCredsMountPath, ReadOnly: true},
		}
		return envVars, vols, mounts
	}

	envVars := []corev1.EnvVar{
		{
			Name: backendTokenEnv(backend),
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: config.SecretRef.Name},
					Key:                  secretKeyOrDefault(config.SecretRef),
				},
			},
		},
	}
	return envVars, nil, nil
}

//go:embed templates/analysis.sh
var analysisScriptContent string

func analysisPipelineRunName(parentName, roleName string) string {
	base := fmt.Sprintf("%s-%s", parentName, roleName)
	hash := sha256.Sum256([]byte(base))
	suffix := hex.EncodeToString(hash[:])[:8]
	prefix := formatting.CleanValueKubernetes(strings.ToLower(parentName))
	role := formatting.CleanValueKubernetes(strings.ToLower(roleName))
	name := fmt.Sprintf("%s-llm-%s-%s", prefix, role, suffix)
	if len(name) <= 63 {
		return name
	}
	name = name[len(name)-63:]
	name = strings.TrimLeft(name, "-")
	return name
}

func backendTokenEnv(backend AgentBackend) string {
	switch backend {
	case BackendCodex:
		return "OPENAI_API_KEY"
	case BackendClaude:
		return "ANTHROPIC_API_KEY"
	case BackendGemini:
		return "GEMINI_API_KEY"
	default:
		return "LLM_API_KEY"
	}
}

func secretKeyOrDefault(secretRef *v1alpha1.Secret) string {
	if secretRef == nil || secretRef.Key == "" {
		return "token"
	}
	return secretRef.Key
}

func maxTokensOrDefault(maxTokens int) int {
	if maxTokens == 0 {
		return DefaultMaxTokens
	}
	return maxTokens
}

func fetchAnalysisFromLogs(ctx context.Context, run *params.Run, pr *tektonv1.PipelineRun) (string, error) {
	if run.Clients.Kube == nil {
		return "", fmt.Errorf("kubernetes client not available")
	}

	labelSelector := fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=run-analysis", pr.Name)
	pods, err := run.Clients.Kube.CoreV1().Pods(pr.Namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return "", fmt.Errorf("failed to list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for analysis task")
	}

	pod := pods.Items[0]
	req := run.Clients.Kube.CoreV1().Pods(pr.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
		Container: "step-run-analysis",
	})
	logs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}
	defer logs.Close()

	logBytes, err := io.ReadAll(logs)
	if err != nil {
		return "", fmt.Errorf("failed to read logs: %w", err)
	}

	logContent := string(logBytes)
	startMarker := "===ANALYSIS_BEGIN==="
	endMarker := "===ANALYSIS_END==="

	startIdx := strings.Index(logContent, startMarker)
	if startIdx == -1 {
		return "", fmt.Errorf("analysis begin marker not found in logs")
	}
	startIdx += len(startMarker)

	endIdx := strings.Index(logContent[startIdx:], endMarker)
	if endIdx == -1 {
		return "", fmt.Errorf("analysis end marker not found in logs")
	}

	envelope := strings.TrimSpace(logContent[startIdx : startIdx+endIdx])
	return envelope, nil
}

func parsePipelineRunEnvelope(ctx context.Context, run *params.Run, pr *tektonv1.PipelineRun) (AnalysisResult, AnalysisSummary) {
	role := pr.Annotations[keys.LLMRole]
	output := pr.Annotations[keys.LLMOutput]
	summary := AnalysisSummary{
		PipelineRunName: pr.Name,
		Backend:         pr.Annotations[keys.LLMBackend],
		Status:          "error",
		CollectedAt:     time.Now().UTC(),
	}

	var envelopeJSON string
	var parseErr error

	// Try log-based parsing first
	envelopeJSON, parseErr = fetchAnalysisFromLogs(ctx, run, pr)
	if parseErr != nil {
		// Fall back to result-based parsing
		var ok bool
		for _, result := range pr.Status.Results {
			if result.Name == analysisResultName {
				envelopeJSON = result.Value.StringVal
				ok = true
				break
			}
		}
		if !ok {
			err := &AnalysisError{
				Provider: summary.Backend,
				Type:     "missing_result",
				Message:  pipelineRunErrorMessage(pr),
			}
			summary.Error = err
			return AnalysisResult{Role: role, Output: output, Error: err}, summary
		}
	}

	envelope := AnalysisEnvelope{}
	if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err != nil {
		unmarshalErr := &AnalysisError{
			Provider: summary.Backend,
			Type:     "invalid_result",
			Message:  fmt.Sprintf("failed to parse PipelineRun result: %v", err),
		}
		summary.Error = unmarshalErr
		return AnalysisResult{Role: role, Output: output, Error: unmarshalErr}, summary
	}

	summary.Backend = envelope.Backend
	summary.Model = envelope.Model
	summary.TokensUsed = envelope.TokensUsed
	summary.DurationMS = envelope.DurationMS

	if envelope.Status != "success" {
		summary.Error = envelope.Error
		if summary.Error == nil {
			summary.Error = &AnalysisError{
				Provider: envelope.Backend,
				Type:     "pipelinerun_failed",
				Message:  pipelineRunErrorMessage(pr),
			}
		}
		return AnalysisResult{Role: role, Output: output, Error: summary.Error}, summary
	}

	summary.Status = "success"
	response := &AnalysisResponse{
		Content:    envelope.Content,
		TokensUsed: envelope.TokensUsed,
		Provider:   envelope.Backend,
		Model:      envelope.Model,
		Timestamp:  summary.CollectedAt,
		Duration:   time.Duration(envelope.DurationMS) * time.Millisecond,
	}
	return AnalysisResult{Role: role, Output: output, Response: response}, summary
}

func pipelineRunErrorMessage(pr *tektonv1.PipelineRun) string {
	if condition := pr.Status.GetCondition(apis.ConditionSucceeded); condition != nil {
		if condition.Message != "" {
			return condition.Message
		}
		if condition.Reason != "" {
			return condition.Reason
		}
	}
	return fmt.Sprintf("PipelineRun %s did not produce a valid analysis result", pr.Name)
}

func isPipelineRunCollected(pr *tektonv1.PipelineRun) bool {
	return pr.Annotations[keys.LLMCollected] == collectedValue
}

func markPipelineRunCollected(ctx context.Context, run *params.Run, pr *tektonv1.PipelineRun) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.LLMCollected: collectedValue,
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = run.Clients.Tekton.TektonV1().PipelineRuns(pr.Namespace).Patch(
		ctx,
		pr.Name,
		types.MergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	return err
}

func loadAnalysisSummaries(parent *tektonv1.PipelineRun) (map[string]AnalysisSummary, error) {
	summaries := map[string]AnalysisSummary{}
	if parent.Annotations == nil {
		return summaries, nil
	}
	raw := parent.Annotations[keys.LLMResultSummary]
	if raw == "" {
		return summaries, nil
	}
	if err := json.Unmarshal([]byte(raw), &summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

func persistAnalysisSummary(
	ctx context.Context,
	run *params.Run,
	logger *zap.SugaredLogger,
	parent *tektonv1.PipelineRun,
	role string,
	summary AnalysisSummary,
) (*tektonv1.PipelineRun, error) {
	summaries, err := loadAnalysisSummaries(parent)
	if err != nil {
		return parent, err
	}
	summaries[role] = summary
	encoded, err := json.Marshal(summaries)
	if err != nil {
		return parent, err
	}

	return action.PatchPipelineRun(ctx, logger, "persisting llm analysis summary", run.Clients.Tekton, parent, map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.LLMResultSummary: string(encoded),
			},
		},
	})
}
