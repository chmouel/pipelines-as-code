package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	paramclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fakepipelineclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeapi "knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
)

type commentCaptureProvider struct {
	tprovider.TestProviderImp
	comment      string
	updateMarker string
}

func (p *commentCaptureProvider) CreateComment(_ context.Context, _ *info.Event, comment, updateMarker string) error {
	p.comment = comment
	p.updateMarker = updateMarker
	return nil
}

type statusCaptureProvider struct {
	tprovider.TestProviderImp
	comment      string
	updateMarker string
}

func (p *statusCaptureProvider) CreateStatus(ctx context.Context, event *info.Event, opts providerstatus.StatusOpts) error {
	return p.TestProviderImp.CreateStatus(ctx, event, opts)
}

func (p *statusCaptureProvider) CreateComment(_ context.Context, _ *info.Event, comment, updateMarker string) error {
	p.comment = comment
	p.updateMarker = updateMarker
	return nil
}

func TestAnalysisScriptCapturesPatchFromWorkingTree(t *testing.T) {
	assert.Assert(t, strings.Contains(analysisScriptContent, "edit files in this checkout"))
	assert.Assert(t, strings.Contains(analysisScriptContent, "Your task includes producing a reusable patch"))
	assert.Assert(t, strings.Contains(analysisScriptContent, "git -C \"${repo}\" diff --binary --no-ext-diff"))
	assert.Assert(t, strings.Contains(analysisScriptContent, "git -C \"${repo}\" add -N ."))
	assert.Assert(t, strings.Contains(analysisScriptContent, "--add-dir \"${repo_dir}\""))
	assert.Assert(t, strings.Contains(analysisScriptContent, "git config --global --add safe.directory \"${repo_dir}/\""))
	assert.Assert(t, !strings.Contains(analysisScriptContent, "git -C \"${repo_dir:-.}\" apply --recount"))
	assert.Assert(t, !strings.Contains(analysisScriptContent, "--tools \"\""))
	assert.Assert(t, !strings.Contains(analysisScriptContent, "--max-turns 1"))
}

func stepEnvValue(step tektonv1.Step, name string) string {
	for _, env := range step.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func TestValidateAnalysisConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *v1alpha1.AIAnalysisConfig
		wantError bool
	}{
		{
			name: "valid config pipelinerun mode",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "codex",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token", Key: "token"},
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this"},
				},
			},
		},
		{
			name: "taskrun mode no longer supported",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "taskrun",
				Backend:       "codex",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token", Key: "token"},
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this"},
				},
			},
			wantError: true,
		},
		{
			name: "missing backend",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token"},
				Roles:         []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
			},
			wantError: true,
		},
		{
			name: "missing image",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "codex",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token"},
				Roles:         []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
			},
			wantError: true,
		},
		{
			name: "invalid execution mode",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "inline",
				Backend:       "codex",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token"},
				Roles:         []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
			},
			wantError: true,
		},
		{
			name: "valid opencode config",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode:   "pipelinerun",
				Backend:         "opencode",
				Image:           "quay.io/example/opencode:latest",
				SecretRef:       &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
				VertexProjectID: "my-gcp-project",
				VertexRegion:    "us-east5",
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this"},
				},
			},
		},
		{
			name: "opencode missing vertex_project_id",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "opencode",
				Image:         "quay.io/example/opencode:latest",
				SecretRef:     &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this"},
				},
			},
			wantError: true,
		},
		{
			name: "valid claude-vertex config",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode:   "pipelinerun",
				Backend:         "claude-vertex",
				Image:           "quay.io/example/claude:latest",
				SecretRef:       &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
				VertexProjectID: "my-gcp-project",
				VertexRegion:    "us-east5",
				Roles:           []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
			},
		},
		{
			name: "claude-vertex missing vertex_project_id",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "claude-vertex",
				Image:         "quay.io/example/claude:latest",
				SecretRef:     &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
				Roles:         []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
			},
			wantError: true,
		},
		{
			name: "invalid output",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "codex",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token"},
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this", Output: "stdout"},
				},
			},
			wantError: true,
		},
		{
			name: "valid check-run output",
			config: &v1alpha1.AIAnalysisConfig{
				ExecutionMode: "pipelinerun",
				Backend:       "codex",
				Image:         "quay.io/example/codex:latest",
				SecretRef:     &v1alpha1.Secret{Name: "llm-token"},
				Roles: []v1alpha1.AnalysisRole{
					{Name: "review", Prompt: "review this", Output: "check-run"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAnalysisConfig(tt.config)
			if tt.wantError {
				assert.Assert(t, err != nil, "expected validation error")
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestExecuteAnalysisCreatesPipelineRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	tekton := fakepipelineclientset.NewSimpleClientset() //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}

	repo := testAIRepository()
	pr := failedPipelineRun()
	event := testEvent()

	err := ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, repo, pr, event, &tprovider.TestProviderImp{})
	assert.NilError(t, err)

	// Filter for analysis PipelineRuns (not the parent)
	analysisPRs, err := tekton.TektonV1().PipelineRuns(pr.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: keys.LLMAnalysis + "=true",
	})
	assert.NilError(t, err)
	assert.Equal(t, len(analysisPRs.Items), 1)
	assert.Equal(t, analysisPRs.Items[0].Annotations[keys.LLMRole], "review")
	assert.Equal(t, analysisPRs.Items[0].Annotations[keys.LLMBackend], "codex")
	assert.Equal(t, analysisPRs.Items[0].Annotations[keys.LLMOutput], "pr-comment")
	assert.Assert(t, analysisPRs.Items[0].Spec.PipelineSpec != nil, "expected inline PipelineSpec")
	assert.Equal(t, len(analysisPRs.Items[0].Spec.PipelineSpec.Tasks), 1, "expected run-analysis task")
	assert.Equal(t, analysisPRs.Items[0].Spec.PipelineSpec.Tasks[0].Name, "run-analysis")
	assert.Assert(t, analysisPRs.Items[0].Spec.PipelineSpec.Tasks[0].TaskSpec != nil, "expected inline TaskSpec for run-analysis")
	assert.Equal(t, len(analysisPRs.Items[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps), 2, "expected clone and run-analysis steps")
	cloneStep := analysisPRs.Items[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0]
	assert.Equal(t, cloneStep.Name, gitCloneStepName)
	assert.Assert(t, cloneStep.Ref == nil, "clone step should be inline shell, not a remote ref")
	assert.Equal(t, cloneStep.Image, settings.AIAnalysisGitImageDefault)
	assert.Equal(t, stepEnvValue(cloneStep, "OUTPUT_PATH"), sourceMountPath)
	assert.Equal(t, stepEnvValue(cloneStep, "REPO_URL"), event.URL)
	assert.Equal(t, stepEnvValue(cloneStep, "REVISION"), event.SHA)
	assert.Assert(t, strings.Contains(cloneStep.Script, "git init"))
	assert.Assert(t, strings.Contains(cloneStep.Script, "git fetch"))
	assert.Equal(t, len(cloneStep.VolumeMounts), 0)
	assert.Equal(t, analysisPRs.Items[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[1].Name, "run-analysis")
}

func TestExecuteAnalysisCollectsPipelineRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	parent := failedPipelineRun()
	parent.Annotations = map[string]string{keys.State: "completed"}

	envelope, err := json.Marshal(AnalysisEnvelope{
		Status:     "success",
		Backend:    "codex",
		Model:      "gpt-5.4-mini",
		Content:    "analysis complete",
		TokensUsed: 42,
		DurationMS: 1500,
	})
	assert.NilError(t, err)

	analysisPR := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      analysisPipelineRunName(parent.Name, "review"),
			Namespace: parent.Namespace,
			Annotations: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMBackend:           "codex",
				keys.LLMRole:              "review",
			},
			Labels: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: knativeapi.ConditionSucceeded, Status: corev1.ConditionTrue},
				},
			},
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				Results: []tektonv1.PipelineRunResult{
					{
						Name: analysisResultName,
						Value: tektonv1.ParamValue{
							Type:      tektonv1.ParamTypeString,
							StringVal: string(envelope),
						},
					},
				},
			},
		},
	}

	tekton := fakepipelineclientset.NewSimpleClientset(parent, analysisPR) //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}

	err = ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, testAIRepository(), parent, testEvent(), &tprovider.TestProviderImp{})
	assert.NilError(t, err)

	updatedAnalysisPR, err := tekton.TektonV1().PipelineRuns(parent.Namespace).Get(context.Background(), analysisPR.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updatedAnalysisPR.Annotations[keys.LLMCollected], collectedValue)

	updatedParent, err := tekton.TektonV1().PipelineRuns(parent.Namespace).Get(context.Background(), parent.Name, metav1.GetOptions{})
	assert.NilError(t, err)

	summaries := map[string]AnalysisSummary{}
	err = json.Unmarshal([]byte(updatedParent.Annotations[keys.LLMResultSummary]), &summaries)
	assert.NilError(t, err)
	assert.Equal(t, summaries["review"].PipelineRunName, analysisPR.Name)
	assert.Equal(t, summaries["review"].Status, "success")
	assert.Equal(t, summaries["review"].TokensUsed, 42)
}

func TestExecuteAnalysisCollectsOrphanedPipelineRun(t *testing.T) {
	// A completed child PipelineRun whose role no longer matches should still be collected.
	testLogger, _ := logger.GetLogger()
	parent := failedPipelineRun()
	parent.Annotations = map[string]string{keys.State: "completed"}

	envelope, err := json.Marshal(AnalysisEnvelope{
		Status:     "success",
		Backend:    "codex",
		Model:      "gpt-5.4-mini",
		Content:    "orphaned analysis",
		TokensUsed: 10,
		DurationMS: 500,
	})
	assert.NilError(t, err)

	// PipelineRun is for role "old-role" which is NOT in the repo config (only "review" is).
	analysisPR := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      analysisPipelineRunName(parent.Name, "old-role"),
			Namespace: parent.Namespace,
			Annotations: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMBackend:           "codex",
				keys.LLMRole:              "old-role",
			},
			Labels: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: knativeapi.ConditionSucceeded, Status: corev1.ConditionTrue},
				},
			},
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				Results: []tektonv1.PipelineRunResult{
					{
						Name: analysisResultName,
						Value: tektonv1.ParamValue{
							Type:      tektonv1.ParamTypeString,
							StringVal: string(envelope),
						},
					},
				},
			},
		},
	}

	tekton := fakepipelineclientset.NewSimpleClientset(parent, analysisPR) //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}

	err = ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, testAIRepository(), parent, testEvent(), &tprovider.TestProviderImp{})
	assert.NilError(t, err)

	updatedAnalysisPR, err := tekton.TektonV1().PipelineRuns(parent.Namespace).Get(context.Background(), analysisPR.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, updatedAnalysisPR.Annotations[keys.LLMCollected], collectedValue)

	updatedParent, err := tekton.TektonV1().PipelineRuns(parent.Namespace).Get(context.Background(), parent.Name, metav1.GetOptions{})
	assert.NilError(t, err)

	summaries := map[string]AnalysisSummary{}
	err = json.Unmarshal([]byte(updatedParent.Annotations[keys.LLMResultSummary]), &summaries)
	assert.NilError(t, err)
	assert.Equal(t, summaries["old-role"].Status, "success")
}

func TestExecuteAnalysisCollectsOrphanedCheckRunOutput(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	parent := failedPipelineRun()
	parent.Annotations = map[string]string{keys.State: "completed"}

	envelope, err := json.Marshal(AnalysisEnvelope{
		Status:     "success",
		Backend:    "codex",
		Model:      "gpt-5.4-mini",
		Content:    "orphaned analysis",
		TokensUsed: 10,
		DurationMS: 500,
	})
	assert.NilError(t, err)

	analysisPR := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      analysisPipelineRunName(parent.Name, "old-role"),
			Namespace: parent.Namespace,
			Annotations: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMBackend:           "codex",
				keys.LLMRole:              "old-role",
				keys.LLMOutput:            "check-run",
			},
			Labels: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMOutput:            "check-run",
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{Type: knativeapi.ConditionSucceeded, Status: corev1.ConditionTrue},
				},
			},
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				Results: []tektonv1.PipelineRunResult{
					{
						Name: analysisResultName,
						Value: tektonv1.ParamValue{
							Type:      tektonv1.ParamTypeString,
							StringVal: string(envelope),
						},
					},
				},
			},
		},
	}

	tekton := fakepipelineclientset.NewSimpleClientset(parent, analysisPR) //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}
	event := testEvent()
	event.InstallationID = 12345
	prov := &statusCaptureProvider{}

	err = ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, testAIRepository(), parent, event, prov)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, prov.LastStatusOpts.Status, "completed")
	assert.Equal(t, prov.LastStatusOpts.OriginalPipelineRunName, analysisCheckRunName("old-role"))
	assert.Assert(t, strings.Contains(prov.LastStatusOpts.Text, "orphaned analysis"))
}

func TestBuildAnalysisPipelineRunWithGitAuthSecret(t *testing.T) {
	config := testAIRepository().Spec.Settings.AIAnalysis
	repo := testAIRepository()
	parent := failedPipelineRun()
	parent.Annotations[keys.GitAuthSecret] = "pac-gitauth-abc123"
	event := testEvent()

	exec := roleExecution{
		Role:     config.Roles[0],
		Rendered: "test prompt",
	}

	pr := buildAnalysisPipelineRun(config, repo, parent, event, exec, settings.AIAnalysisGitImageDefault)

	runAnalysisTask := pr.Spec.PipelineSpec.Tasks[0]
	assert.Equal(t, runAnalysisTask.Name, "run-analysis")
	assert.Assert(t, runAnalysisTask.TaskSpec != nil)

	analysisTaskSpec := runAnalysisTask.TaskSpec.TaskSpec

	assert.Equal(t, len(analysisTaskSpec.Volumes), 0)

	var hasBasicAuthTaskWorkspace bool
	for _, workspace := range analysisTaskSpec.Workspaces {
		if workspace.Name == basicAuthName {
			hasBasicAuthTaskWorkspace = true
		}
	}
	assert.Assert(t, hasBasicAuthTaskWorkspace, "task should declare basic-auth workspace")

	var hasBasicAuthPipelineWorkspace bool
	for _, workspace := range pr.Spec.PipelineSpec.Workspaces {
		if workspace.Name == basicAuthName {
			hasBasicAuthPipelineWorkspace = true
		}
	}
	assert.Assert(t, hasBasicAuthPipelineWorkspace, "pipeline should declare basic-auth workspace")

	var hasBasicAuthPipelineTaskWorkspace bool
	for _, workspace := range runAnalysisTask.Workspaces {
		if workspace.Name == basicAuthName {
			hasBasicAuthPipelineTaskWorkspace = true
			assert.Equal(t, workspace.Workspace, basicAuthName)
		}
	}
	assert.Assert(t, hasBasicAuthPipelineTaskWorkspace, "pipeline task should bind basic-auth workspace")

	var hasBasicAuthRunWorkspace bool
	for _, workspace := range pr.Spec.Workspaces {
		if workspace.Name == basicAuthName {
			hasBasicAuthRunWorkspace = true
			assert.Assert(t, workspace.Secret != nil, "basic-auth workspace should use a secret")
			assert.Equal(t, workspace.Secret.SecretName, "pac-gitauth-abc123")
		}
	}
	assert.Assert(t, hasBasicAuthRunWorkspace, "pipeline run should bind basic-auth secret workspace")

	cloneStep := analysisTaskSpec.Steps[0]
	assert.Equal(t, cloneStep.Name, gitCloneStepName)
	assert.Assert(t, cloneStep.Ref == nil, "clone step should be inline shell, not a remote ref")
	assert.Assert(t, strings.Contains(cloneStep.Script, "/workspace/basic-auth"))
	assert.Equal(t, len(cloneStep.VolumeMounts), 0)
}

func TestBuildAnalysisPipelineRunVertex(t *testing.T) {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled:         true,
					ExecutionMode:   "pipelinerun",
					Backend:         "claude-vertex",
					Image:           "quay.io/example/claude:latest",
					SecretRef:       &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
					VertexProjectID: "my-project-123",
					VertexRegion:    "europe-west1",
					Roles:           []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
				},
			},
		},
	}
	config := repo.Spec.Settings.AIAnalysis
	parent := failedPipelineRun()
	event := testEvent()

	exec := roleExecution{
		Role:     config.Roles[0],
		Rendered: "test prompt",
	}

	pr := buildAnalysisPipelineRun(config, repo, parent, event, exec, settings.AIAnalysisGitImageDefault)

	runAnalysisTask := pr.Spec.PipelineSpec.Tasks[0]
	assert.Equal(t, runAnalysisTask.Name, "run-analysis")
	assert.Assert(t, runAnalysisTask.TaskSpec != nil)

	analysisTaskSpec := runAnalysisTask.TaskSpec.TaskSpec

	// Should have gcp-creds volume
	assert.Equal(t, len(analysisTaskSpec.Volumes), 1)
	assert.Equal(t, analysisTaskSpec.Volumes[0].Name, "gcp-creds")
	assert.Equal(t, analysisTaskSpec.Volumes[0].Secret.SecretName, "gcp-sa-key")

	// run-analysis step should have gcp-creds volume mount
	analysisStep := analysisTaskSpec.Steps[1]
	assert.Equal(t, analysisStep.Name, "run-analysis")
	assert.Equal(t, len(analysisStep.VolumeMounts), 1)
	assert.Equal(t, analysisStep.VolumeMounts[0].Name, "gcp-creds")
	assert.Equal(t, analysisStep.VolumeMounts[0].MountPath, "/workspace/gcp-creds")

	// Check env vars
	envMap := map[string]string{}
	for _, env := range analysisStep.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}
	assert.Equal(t, envMap["CLAUDE_CODE_USE_VERTEX"], "1")
	assert.Equal(t, envMap["ANTHROPIC_VERTEX_PROJECT_ID"], "my-project-123")
	assert.Equal(t, envMap["CLOUD_ML_REGION"], "europe-west1")
	assert.Equal(t, envMap["GOOGLE_APPLICATION_CREDENTIALS"], "/workspace/gcp-creds/key.json")
}

func TestBuildAnalysisPipelineRunVertexDefaultRegion(t *testing.T) {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled:         true,
					ExecutionMode:   "pipelinerun",
					Backend:         "claude-vertex",
					Image:           "quay.io/example/claude:latest",
					SecretRef:       &v1alpha1.Secret{Name: "gcp-sa-key", Key: "key.json"},
					VertexProjectID: "my-project-123",
					Roles:           []v1alpha1.AnalysisRole{{Name: "review", Prompt: "review this"}},
				},
			},
		},
	}
	config := repo.Spec.Settings.AIAnalysis
	parent := failedPipelineRun()
	event := testEvent()

	exec := roleExecution{
		Role:     config.Roles[0],
		Rendered: "test prompt",
	}

	pr := buildAnalysisPipelineRun(config, repo, parent, event, exec, settings.AIAnalysisGitImageDefault)

	analysisStep := pr.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[1]
	envMap := map[string]string{}
	for _, env := range analysisStep.Env {
		if env.Value != "" {
			envMap[env.Name] = env.Value
		}
	}
	assert.Equal(t, envMap["CLOUD_ML_REGION"], "global")
}

func TestAnalysisPipelineRunNameLeadingHyphen(t *testing.T) {
	// A long parent name where truncation would land on a hyphen.
	longParent := "a-very-long-pipeline-run-name-that-exceeds-the-limit-by-quite-a-bit"
	name := analysisPipelineRunName(longParent, "review")
	assert.Assert(t, len(name) <= 63, "name exceeds 63 chars: %s", name)
	assert.Assert(t, name[0] != '-', "name starts with hyphen: %s", name)
}

func TestShouldTriggerRole(t *testing.T) {
	failedPR := &tektonv1.PipelineRun{}
	failedPR.Status.Conditions = append(failedPR.Status.Conditions, knativeapi.Condition{Type: knativeapi.ConditionSucceeded, Status: "False"})

	succeededPR := &tektonv1.PipelineRun{}
	succeededPR.Status.Conditions = append(succeededPR.Status.Conditions, knativeapi.Condition{Type: knativeapi.ConditionSucceeded, Status: "True"})

	tests := []struct {
		name        string
		role        v1alpha1.AnalysisRole
		celContext  map[string]any
		pr          *tektonv1.PipelineRun
		wantTrigger bool
		wantError   bool
	}{
		{
			name:        "no cel expression triggers for failed pipeline",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          failedPR,
			wantTrigger: true,
		},
		{
			name:        "no cel expression skips succeeded pipeline",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          succeededPR,
			wantTrigger: false,
		},
		{
			name:        "simple true expression",
			role:        v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "true"},
			celContext:  map[string]any{},
			pr:          succeededPR,
			wantTrigger: true,
		},
		{
			name:        "simple false expression",
			role:        v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "false"},
			celContext:  map[string]any{},
			pr:          failedPR,
			wantTrigger: false,
		},
		{
			name:       "invalid cel expression",
			role:       v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "invalid syntax ("},
			celContext: map[string]any{},
			pr:         failedPR,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger, err := shouldTriggerRole(tt.role, tt.celContext, tt.pr)
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, trigger, tt.wantTrigger)
		})
	}
}

func TestGetContextCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		config   *v1alpha1.ContextConfig
		expected string
	}{
		{
			name:     "nil config returns default key",
			config:   nil,
			expected: "default",
		},
		{
			name:     "config without container logs",
			config:   &v1alpha1.ContextConfig{},
			expected: "commit:false-pr:false-error:false-logs:false-0",
		},
		{
			name: "container logs enabled with explicit max lines",
			config: &v1alpha1.ContextConfig{
				CommitContent: true,
				PRContent:     true,
				ErrorContent:  true,
				ContainerLogs: &v1alpha1.ContainerLogsConfig{
					Enabled:  true,
					MaxLines: 25,
				},
			},
			expected: "commit:true-pr:true-error:true-logs:true-25",
		},
		{
			name: "container logs enabled with default max lines",
			config: &v1alpha1.ContextConfig{
				ContainerLogs: &v1alpha1.ContainerLogsConfig{
					Enabled: true,
				},
			},
			expected: "commit:false-pr:false-error:false-logs:true-50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, getContextCacheKey(tt.config), tt.expected)
		})
	}
}

func TestPostPRCommentNormalizesTranscriptNoise(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &commentCaptureProvider{}
	event := testEvent()
	event.PullRequestNumber = 123

	content := `Performing one time database migration, may take a few minutes...
sqlite-migration:done
Database migration complete.
␛[0m
> build · claude-sonnet-4@20250514
␛[0m
I'll analyze this CI pipeline failure and help you identify the root cause and potential fixes.
␛[0m# ␛[0mTodos
[ ] Analyze the CI pipeline failure details
[ ] Examine the failed task configuration and logs
[ ] Provide specific fix recommendations
␛[0m
Based on the CI pipeline failure information provided, here's my analysis:

## Pipeline Failure Analysis

**Pipeline Details:**
- **Name:** test-0-rhplw
- **Status:** Failed

# ␛[0mTodos
[x] Analyze the CI pipeline failure details
[x] Examine the failed task configuration and logs
[x] Provide specific fix recommendations

Would you like me to help you examine your CI configuration files to identify the specific issue?

---
*Generated by Pipelines-as-Code LLM Analysis*`

	err := postPRComment(context.Background(), AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: content,
		},
	}, event, prov, testLogger)
	assert.NilError(t, err)

	assert.Equal(t, prov.updateMarker, "llm-analysis-failure-analysis")
	assert.Assert(t, strings.Contains(prov.comment, "## 🤖 AI Analysis - failure-analysis"))
	assert.Assert(t, strings.Contains(prov.comment, "## Pipeline Failure Analysis"))
	assert.Assert(t, strings.Contains(prov.comment, "**Pipeline Details:**"))
	assert.Assert(t, strings.Contains(prov.comment, "*Generated by Pipelines-as-Code LLM Analysis*"))

	assert.Assert(t, !strings.Contains(prov.comment, "Performing one time database migration"))
	assert.Assert(t, !strings.Contains(prov.comment, "sqlite-migration:done"))
	assert.Assert(t, !strings.Contains(prov.comment, "> build · claude-sonnet-4@20250514"))
	assert.Assert(t, !strings.Contains(prov.comment, "# Todos"))
	assert.Assert(t, !strings.Contains(prov.comment, "[ ] Analyze the CI pipeline failure details"))
	assert.Assert(t, !strings.Contains(prov.comment, "Would you like me to help"))
	assert.Assert(t, !strings.Contains(prov.comment, "␛[0m"))
}

func TestFormatAnalysisCommentFallsBackWhenContentIsEmpty(t *testing.T) {
	comment := formatAnalysisComment("failure-analysis", "␛[0m\n> build · claude-sonnet-4@20250514\n")

	assert.Assert(t, strings.Contains(comment, "## 🤖 AI Analysis - failure-analysis"))
	assert.Assert(t, strings.Contains(comment, "_No analysis content produced._"))
	assert.Assert(t, !strings.Contains(comment, "> build · claude-sonnet-4@20250514"))
}

func TestPostCheckRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345 // GitHub App

	result := AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: "Root cause: missing dependency\n\nFix: install package",
		},
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)

	opts := prov.LastStatusOpts
	assert.Equal(t, opts.Status, "completed")
	assert.Equal(t, opts.Conclusion, providerstatus.ConclusionNeutral)
	assert.Equal(t, opts.PipelineRunName, BuildExternalID("llm-analysis", "parent-pr", "failure-analysis", event.SHA))
	assert.Equal(t, opts.OriginalPipelineRunName, analysisCheckRunName("failure-analysis"))
	assert.Equal(t, opts.Title, "AI Analysis - failure-analysis")
	assert.Assert(t, strings.Contains(opts.Text, "Root cause: missing dependency"))
	assert.Assert(t, opts.PipelineRun == nil)
}

func TestPostCheckRunHasFixItButtonWhenPatchValid(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	result := AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: "Root cause: missing dependency",
		},
		Patch: &MachinePatchMetadata{
			Version:    1,
			Format:     "git-diff",
			Encoding:   "gzip+base64",
			BaseSHA:    event.SHA,
			Role:       "failure-analysis",
			ChunkCount: 1,
			Available:  true,
		},
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Assert(t, len(prov.LastStatusOpts.Actions) == 1, "should have Fix it action when patch is valid")
	assert.Equal(t, prov.LastStatusOpts.Actions[0].Identifier, "llm-fix")
	assert.Equal(t, prov.LastStatusOpts.Actions[0].Label, "Fix it")
}

func TestPostCheckRunHasNoFixItButtonWhenPatchNil(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	result := AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: "Root cause: missing dependency",
		},
		Patch: nil,
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, len(prov.LastStatusOpts.Actions), 0, "should have no actions when patch is nil")
}

func TestPostCheckRunHasNoFixItButtonWhenPatchInvalid(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	result := AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: "Root cause: missing dependency",
		},
		Patch: &MachinePatchMetadata{
			Available: false, // invalid — not available
		},
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, len(prov.LastStatusOpts.Actions), 0, "should have no actions when patch is invalid")
}

func TestPostQueuedCheckRunHasNoActions(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	err := postQueuedCheckRun(context.Background(), "review", "parent-pr", event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, len(prov.LastStatusOpts.Actions), 0, "queued check run must never have Fix it button")
}

func TestPostCheckRunSkipsWhenNotGitHubApp(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 0 // Not a GitHub App

	result := AnalysisResult{
		Role: "failure-analysis",
		Response: &AnalysisResponse{
			Content: "analysis content",
		},
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts == nil, "should not call CreateStatus when InstallationID is 0")
}

func TestPostCheckRunWithNoPR(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345 // GitHub App
	event.PullRequestNumber = 0  // No PR (e.g., push event)

	result := AnalysisResult{
		Role: "review",
		Response: &AnalysisResponse{
			Content: "analysis content",
		},
	}

	err := postCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil, "should create check run even without PR number")
}

func TestHandleAnalysisResultCheckRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	repo := testAIRepository()
	repo.Spec.Settings.AIAnalysis.Roles[0].Output = "check-run"

	result := AnalysisResult{
		Role: "review",
		Response: &AnalysisResponse{
			Content: "looks good",
		},
	}

	err := handleAnalysisResult(context.Background(), testLogger, repo, failedPipelineRun(), event, prov, result)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil, "should call CreateStatus for check-run output")
	assert.Equal(t, prov.LastStatusOpts.OriginalPipelineRunName, analysisCheckRunName("review"))
}

func TestPostQueuedCheckRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345

	err := postQueuedCheckRun(context.Background(), "review", "parent-pr", event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, prov.LastStatusOpts.Status, "queued")
	assert.Equal(t, prov.LastStatusOpts.Conclusion, providerstatus.ConclusionPending)
	assert.Equal(t, prov.LastStatusOpts.PipelineRunName, BuildExternalID("llm-analysis", "parent-pr", "review", event.SHA))
	assert.Equal(t, prov.LastStatusOpts.OriginalPipelineRunName, analysisCheckRunName("review"))
	assert.Equal(t, prov.LastStatusOpts.Title, "AI Analysis - review")
	assert.Equal(t, prov.LastStatusOpts.Summary, "AI analysis has been scheduled.")
	assert.Assert(t, strings.Contains(prov.LastStatusOpts.Text, "running AI analysis for role \"review\""))
}

func TestPostFixCheckRunPostsPRCommentWithPushedCommit(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()
	event.InstallationID = 12345
	event.PullRequestNumber = 7

	result := AnalysisResult{
		Role: "review",
		Response: &AnalysisResponse{
			Content: "Stored patch applied and pushed.",
		},
		Metadata: map[string]string{
			"commit_sha":       "abc123def456",
			"commit_short_sha": "abc123d",
			"branch":           "feature-branch",
			"changed_files":    "pkg/llm/analyzer.go\npkg/llm/fix.go",
		},
	}

	err := postFixCheckRun(context.Background(), result, failedPipelineRun(), event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, prov.LastStatusOpts.Status, "completed")
	assert.Equal(t, prov.LastStatusOpts.Conclusion, providerstatus.ConclusionSuccess)
	assert.Assert(t, strings.Contains(prov.comment, "abc123d"))
	assert.Assert(t, strings.Contains(prov.comment, "feature-branch"))
	assert.Assert(t, strings.Contains(prov.comment, "`pkg/llm/analyzer.go`"))
	assert.Equal(t, prov.updateMarker, "llm-fix-review-abc123def456")
}

func TestPostQueuedCheckRunSkipsWhenNotGitHubApp(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &statusCaptureProvider{}
	event := testEvent()

	err := postQueuedCheckRun(context.Background(), "review", "parent-pr", event, prov, testLogger)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts == nil)
}

func TestExecuteAnalysisCreatesQueuedCheckRun(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	tekton := fakepipelineclientset.NewSimpleClientset() //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}
	repo := testAIRepository()
	repo.Spec.Settings.AIAnalysis.Roles[0].Output = "check-run"
	pr := failedPipelineRun()
	event := testEvent()
	event.InstallationID = 12345
	prov := &statusCaptureProvider{}

	err := ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, repo, pr, event, prov)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts != nil)
	assert.Equal(t, prov.LastStatusOpts.Status, "queued")
	assert.Equal(t, prov.LastStatusOpts.PipelineRunName, BuildExternalID("llm-analysis", "parent-pr", "review", event.SHA))
}

func TestExecuteAnalysisDoesNotRecreateQueuedCheckRunWhenAnalysisPipelineRunExists(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	parent := failedPipelineRun()
	existingAnalysisPR := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      analysisPipelineRunName(parent.Name, "review"),
			Namespace: parent.Namespace,
			Annotations: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
				keys.LLMBackend:           "codex",
				keys.LLMRole:              "review",
			},
			Labels: map[string]string{
				keys.LLMAnalysis:          "true",
				keys.LLMParentPipelineRun: parent.Name,
			},
		},
	}
	tekton := fakepipelineclientset.NewSimpleClientset(existingAnalysisPR) //nolint:staticcheck
	run := &params.Run{
		Clients: paramclients.Clients{
			Tekton: tekton,
		},
		Info: info.Info{
			Pac: &info.PacOpts{
				Settings: settings.DefaultSettings(),
			},
		},
	}
	repo := testAIRepository()
	repo.Spec.Settings.AIAnalysis.Roles[0].Output = "check-run"
	event := testEvent()
	event.InstallationID = 12345
	prov := &statusCaptureProvider{}

	err := ExecuteAnalysis(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, repo, parent, event, prov)
	assert.NilError(t, err)
	assert.Assert(t, prov.LastStatusOpts == nil, "existing analysis child PipelineRun should suppress queued check-run creation")
}

func testAIRepository() *v1alpha1.Repository {
	return &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo",
			Namespace: "ns",
		},
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled:       true,
					ExecutionMode: "pipelinerun",
					Backend:       "codex",
					Image:         "quay.io/example/codex:latest",
					SecretRef:     &v1alpha1.Secret{Name: "llm-token", Key: "token"},
					Roles: []v1alpha1.AnalysisRole{
						{Name: "review", Prompt: "review this change"},
					},
				},
			},
		},
	}
}

func testEvent() *info.Event {
	return &info.Event{
		URL: "https://github.com/owner/repo",
		SHA: "abc123def456",
	}
}

func failedPipelineRun() *tektonv1.PipelineRun {
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "parent-pr",
			Namespace: "ns",
			Annotations: map[string]string{
				keys.State: "started",
			},
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{
						Type:    knativeapi.ConditionSucceeded,
						Status:  corev1.ConditionFalse,
						Reason:  "Failed",
						Message: "pipeline failed",
					},
				},
			},
		},
	}
}
