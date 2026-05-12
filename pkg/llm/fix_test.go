package llm

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	paramclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fakepipelineclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildExternalID(t *testing.T) {
	id := BuildExternalID("llm-analysis", "parent-pr", "reviewer", "abc123")
	assert.Equal(t, id, "llm-analysis|parent-pr|reviewer|abc123")
}

func TestParseExternalID(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   ExternalIDParts
		wantOK bool
	}{
		{
			name:  "valid analysis",
			input: "llm-analysis|parent-pr|reviewer|abc123",
			want: ExternalIDParts{
				Kind:   "llm-analysis",
				Parent: "parent-pr",
				Role:   "reviewer",
				SHA:    "abc123",
			},
			wantOK: true,
		},
		{
			name:  "valid fix",
			input: "llm-fix|parent-pr|reviewer|abc123",
			want: ExternalIDParts{
				Kind:   "llm-fix",
				Parent: "parent-pr",
				Role:   "reviewer",
				SHA:    "abc123",
			},
			wantOK: true,
		},
		{
			name:   "too few parts",
			input:  "llm-analysis|parent-pr|reviewer",
			wantOK: false,
		},
		{
			name:   "unknown kind",
			input:  "llm-unknown|parent-pr|reviewer|abc123",
			wantOK: false,
		},
		{
			name:   "empty parent",
			input:  "llm-analysis||reviewer|abc123",
			wantOK: false,
		},
		{
			name:   "empty role",
			input:  "llm-analysis|parent-pr||abc123",
			wantOK: false,
		},
		{
			name:   "empty sha",
			input:  "llm-analysis|parent-pr|reviewer|",
			wantOK: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
		{
			name:   "old format not supported",
			input:  "llm-analysis-reviewer-abc123",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseExternalID(tt.input)
			assert.Equal(t, ok, tt.wantOK)
			if tt.wantOK {
				assert.Equal(t, got.Kind, tt.want.Kind)
				assert.Equal(t, got.Parent, tt.want.Parent)
				assert.Equal(t, got.Role, tt.want.Role)
				assert.Equal(t, got.SHA, tt.want.SHA)
			}
		})
	}
}

func TestExternalIDRoundTrip(t *testing.T) {
	id := BuildExternalID("llm-analysis", "my-pr-run", "code-review", "deadbeef")
	parsed, ok := ParseExternalID(id)
	assert.Assert(t, ok)
	assert.Equal(t, parsed.Kind, "llm-analysis")
	assert.Equal(t, parsed.Parent, "my-pr-run")
	assert.Equal(t, parsed.Role, "code-review")
	assert.Equal(t, parsed.SHA, "deadbeef")
}

func TestIsAnalysisExternalID(t *testing.T) {
	assert.Assert(t, IsAnalysisExternalID("llm-analysis|parent|role|sha"))
	assert.Assert(t, !IsAnalysisExternalID("llm-fix|parent|role|sha"))
	assert.Assert(t, !IsAnalysisExternalID("garbage"))
	assert.Assert(t, !IsAnalysisExternalID(""))
}

func TestFixPipelineRunName(t *testing.T) {
	name := fixPipelineRunName("parent-pr", "reviewer")
	assert.Assert(t, len(name) <= 63)
	assert.Assert(t, name != analysisPipelineRunName("parent-pr", "reviewer"), "fix and analysis names should differ")
}

func TestFixPipelineRunNameLong(t *testing.T) {
	longParent := "a-very-long-pipeline-run-name-that-goes-on-forever"
	longRole := "a-very-long-role-name"
	name := fixPipelineRunName(longParent, longRole)
	assert.Assert(t, len(name) <= 63, "name length %d exceeds 63", len(name))
}

func TestPipelineRunLogSource(t *testing.T) {
	analysisPR := failedPipelineRun()
	taskName, stepContainer := pipelineRunLogSource(analysisPR)
	assert.Equal(t, taskName, "run-analysis")
	assert.Equal(t, stepContainer, "step-run-analysis")

	fixPR := failedPipelineRun()
	fixPR.Annotations[keys.LLMFix] = "true"
	taskName, stepContainer = pipelineRunLogSource(fixPR)
	assert.Equal(t, taskName, "fix")
	assert.Equal(t, stepContainer, "step-run-fix")

	fixPR = failedPipelineRun()
	fixPR.Labels = map[string]string{keys.LLMFix: "true"}
	taskName, stepContainer = pipelineRunLogSource(fixPR)
	assert.Equal(t, taskName, "fix")
	assert.Equal(t, stepContainer, "step-run-fix")
}

func TestBuildFixScript(t *testing.T) {
	payload := "H4sIAAAAAAAAA+3BMQEAAADCoPVP7WsIoAAA"
	script := buildFixScript(payload)

	// Shebang is added by the preamble
	assert.Assert(t, strings.HasPrefix(script, "#!/bin/sh"), "script should start with shebang")

	// Patch heredoc is embedded
	assert.Assert(t, contains(script, "PATCH_DATA_B64GZ"), "script should define PATCH_DATA_B64GZ")
	assert.Assert(t, contains(script, payload), "script should embed the encoded payload")
	assert.Assert(t, contains(script, "ENDOFPATCH"), "script should use ENDOFPATCH heredoc delimiter")

	// Template body is present
	assert.Assert(t, contains(script, "base64 -d"), "script should decode the patch")
	assert.Assert(t, contains(script, "git apply"), "script should apply the patch")
	assert.Assert(t, contains(script, "git push"), "script should push changes")
	assert.Assert(t, contains(script, "emit_envelope"), "script should write the real envelope to logs and Tekton results")
	assert.Assert(t, !contains(script, "{\"status\":\"complete\"}"), "fix result must contain the real envelope, not only a completion marker")

	// No backend LLM invocation
	assert.Assert(t, !contains(script, "LLM_BACKEND"), "fix script must not invoke LLM backend")
	assert.Assert(t, !contains(script, "claude --"), "fix script must not invoke claude CLI")
	assert.Assert(t, !contains(script, "codex exec"), "fix script must not invoke codex CLI")
}

func TestParseFixPipelineRunEnvelopeUsesResultWhenLogsUnavailable(t *testing.T) {
	pr := failedPipelineRun()
	pr.Annotations[keys.LLMFix] = "true"
	pr.Annotations[keys.LLMRole] = "reviewer"
	pr.Status.Results = []tektonv1.PipelineRunResult{
		{
			Name: analysisResultName,
			Value: tektonv1.ParamValue{
				Type: tektonv1.ParamTypeString,
				StringVal: `{
					"status": "success",
					"backend": "patch-apply",
					"content": "Stored patch applied and pushed.",
					"metadata": {
						"commit_short_sha": "abc1234",
						"branch": "feature-branch",
						"changed_files": "README.md"
					}
				}`,
			},
		},
	}
	run := &params.Run{}

	result, summary := parsePipelineRunEnvelope(context.Background(), run, pr)
	assert.Assert(t, result.Error == nil)
	assert.Equal(t, summary.Status, "success")
	assert.Assert(t, result.Response != nil)
	assert.Equal(t, result.Response.Content, "Stored patch applied and pushed.")
	assert.Equal(t, result.Metadata["commit_short_sha"], "abc1234")
}

func TestBuildFixPipelineRunLabelsAndAnnotations(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "test:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles:     []v1alpha1.AnalysisRole{{Name: "reviewer", Prompt: "fix it"}},
	}
	parent := failedPipelineRun()
	event := &info.Event{
		HeadBranch: "feature-branch",
		URL:        "https://github.com/test/repo",
		SHA:        "abc123",
	}

	pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", settings.AIAnalysisGitImageDefault, nil)

	// Labels
	assert.Equal(t, pr.Labels[keys.LLMFix], "true")
	assert.Equal(t, pr.Labels[keys.LLMParentPipelineRun], parent.Name)
	assert.Equal(t, pr.Labels[keys.LLMRole], "reviewer")

	// Annotations
	assert.Equal(t, pr.Annotations[keys.LLMFix], "true")
	assert.Equal(t, pr.Annotations[keys.LLMParentPipelineRun], parent.Name)
	assert.Equal(t, pr.Annotations[keys.LLMRole], "reviewer")

	// Should NOT have analysis labels
	_, hasAnalysis := pr.Labels[keys.LLMAnalysis]
	assert.Assert(t, !hasAnalysis, "fix PipelineRun should not have llm-analysis label")

	// Should NOT have backend label (no LLM is invoked)
	_, hasBackend := pr.Labels[keys.LLMBackend]
	assert.Assert(t, !hasBackend, "fix PipelineRun should not have llm-backend label")
}

func TestBuildFixPipelineRunSingleTaskWithInlineClone(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "test:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles:     []v1alpha1.AnalysisRole{{Name: "reviewer", Prompt: "fix it"}},
	}
	parent := failedPipelineRun()
	parent.Annotations[keys.GitAuthSecret] = "git-auth"
	event := &info.Event{
		HeadBranch: "feature-branch",
		URL:        "https://github.com/test/repo",
		SHA:        "abc123",
	}

	pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", settings.AIAnalysisGitImageDefault, nil)

	// Should have exactly one PipelineTask
	assert.Equal(t, len(pr.Spec.PipelineSpec.Tasks), 1)
	task := pr.Spec.PipelineSpec.Tasks[0]
	assert.Equal(t, task.Name, "fix")

	// Should clone inline, then run the generated fix script.
	assert.Equal(t, len(task.TaskSpec.Steps), 2)
	cloneStep := task.TaskSpec.Steps[0]
	assert.Equal(t, cloneStep.Name, gitCloneStepName)
	assert.Assert(t, cloneStep.Ref == nil, "clone step should be inline shell, not a remote ref")
	assert.Equal(t, cloneStep.Image, settings.AIAnalysisGitImageDefault, nil)
	assert.Equal(t, stepEnvValue(cloneStep, "OUTPUT_PATH"), sourceMountPath)
	assert.Equal(t, stepEnvValue(cloneStep, "REPO_URL"), "https://github.com/test/repo")
	assert.Equal(t, stepEnvValue(cloneStep, "REVISION"), "feature-branch")
	assert.Assert(t, contains(cloneStep.Script, "git init"), "clone step should initialize the checkout")
	assert.Assert(t, contains(cloneStep.Script, "chmod -R a+rwX"), "clone step should make the checkout writable")
	assert.Equal(t, len(cloneStep.VolumeMounts), 0)

	fixStep := task.TaskSpec.Steps[1]
	assert.Equal(t, fixStep.Image, "test:latest")
	assert.Equal(t, fixStep.Name, "run-fix")
	assert.Equal(t, len(fixStep.VolumeMounts), 0)
	assert.Assert(t, contains(fixStep.Script, "git config --global --add safe.directory"), "should trust the shared workspace")
	assert.Assert(t, contains(fixStep.Script, "git fetch --depth=50 origin"), "should fetch the PR branch in the fix step")
	assert.Assert(t, contains(fixStep.Script, "git checkout -B"), "should check out the branch in the fix step")
	assert.Assert(t, contains(fixStep.Script, "git apply"), "should apply the stored patch")

	// Pipeline result references the single task
	assert.Assert(t, contains(pr.Spec.PipelineSpec.Results[0].Value.StringVal, "tasks.fix.results.analysis"))
}

func TestBuildFixPipelineRunUsesAnalysisChildImageOverride(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "global:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles: []v1alpha1.AnalysisRole{
			{Name: "reviewer", Prompt: "fix it", Image: "role:latest"},
		},
	}
	parent := failedPipelineRun()
	event := &info.Event{
		HeadBranch: "feature-branch",
		URL:        "https://github.com/test/repo",
		SHA:        "abc123",
	}

	analysisPR := buildAnalysisPipelineRun(config, testAIRepository(), parent, event, roleExecution{
		Role:     config.Roles[0],
		Rendered: "review this",
	}, settings.AIAnalysisGitImageDefault)
	fixConfig := *config
	fixConfig.Image = analysisPipelineRunImage(analysisPR)

	pr := buildFixPipelineRun(&fixConfig, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", settings.AIAnalysisGitImageDefault, nil)

	task := pr.Spec.PipelineSpec.Tasks[0]
	fixStep := task.TaskSpec.Steps[1]
	assert.Equal(t, fixStep.Image, "role:latest")
}

func TestBuildFixPipelineRunEnvVars(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "test:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles:     []v1alpha1.AnalysisRole{{Name: "reviewer", Prompt: "fix it"}},
	}
	parent := failedPipelineRun()
	event := &info.Event{
		HeadBranch: "feature-branch",
		URL:        "https://github.com/test/repo",
		SHA:        "abc123",
	}

	pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", settings.AIAnalysisGitImageDefault, nil)

	task := pr.Spec.PipelineSpec.Tasks[0]
	fixStep := task.TaskSpec.Steps[1]
	envMap := map[string]string{}
	for _, env := range fixStep.Env {
		envMap[env.Name] = env.Value
	}
	assert.Equal(t, envMap["REPO_URL"], "https://github.com/test/repo")
	assert.Equal(t, envMap["REPO_DIR"], sourceMountPath)
	assert.Equal(t, envMap["PR_BRANCH"], "feature-branch")
	assert.Equal(t, envMap["EXPECTED_SHA"], "abc123")
	assert.Equal(t, envMap["ROLE_NAME"], "reviewer")
	subjectBytes, err := base64.StdEncoding.DecodeString(envMap["FIX_COMMIT_SUBJECT_B64"])
	assert.NilError(t, err)
	bodyBytes, err := base64.StdEncoding.DecodeString(envMap["FIX_COMMIT_BODY_B64"])
	assert.NilError(t, err)
	assert.Equal(t, string(subjectBytes), "fix: update docs")
	assert.Equal(t, string(bodyBytes), "Apply the documentation fix.")

	// These must NOT be present; no LLM is invoked in the fix pod
	_, hasBackend := envMap["LLM_BACKEND"]
	assert.Assert(t, !hasBackend, "fix env must not contain LLM_BACKEND")
	_, hasAnalysisText := envMap["LLM_ANALYSIS_TEXT_B64"]
	assert.Assert(t, !hasAnalysisText, "fix env must not contain LLM_ANALYSIS_TEXT_B64")
	_, hasTimeout := envMap["LLM_TIMEOUT_SECONDS"]
	assert.Assert(t, !hasTimeout, "fix env must not contain LLM_TIMEOUT_SECONDS")
}

func TestBuildFixPipelineRunGitAuthWorkspace(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "test:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles:     []v1alpha1.AnalysisRole{{Name: "reviewer", Prompt: "fix it"}},
	}
	parent := failedPipelineRun()
	parent.Annotations[keys.GitAuthSecret] = "pac-gitauth-secret"
	event := &info.Event{
		HeadBranch: "feature-branch",
		URL:        "https://github.com/test/repo",
		SHA:        "abc123",
	}

	pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", settings.AIAnalysisGitImageDefault, nil)

	task := pr.Spec.PipelineSpec.Tasks[0]

	assert.Equal(t, len(task.TaskSpec.Volumes), 0)

	var hasBasicAuthTaskWorkspace bool
	for _, workspace := range task.TaskSpec.Workspaces {
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
	for _, workspace := range task.Workspaces {
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
			assert.Equal(t, workspace.Secret.SecretName, "pac-gitauth-secret")
		}
	}
	assert.Assert(t, hasBasicAuthRunWorkspace, "pipeline run should bind basic-auth secret workspace")

	assert.Equal(t, len(task.TaskSpec.Steps), 2)
	cloneStep := task.TaskSpec.Steps[0]
	assert.Assert(t, cloneStep.Ref == nil, "clone step should be inline shell, not a remote ref")
	assert.Assert(t, contains(cloneStep.Script, "/workspace/basic-auth"))
	assert.Equal(t, len(cloneStep.VolumeMounts), 0)

	fixStep := task.TaskSpec.Steps[1]
	assert.Equal(t, len(fixStep.VolumeMounts), 0)
}

func TestFixCheckRunStatusOpts(t *testing.T) {
	opts := FixCheckRunStatusOpts("parent-pr", "reviewer", "abc123")
	assert.Equal(t, opts.OriginalPipelineRunName, "AI Fix / reviewer")
	assert.Equal(t, opts.Title, "AI Fix - reviewer")
	assert.Equal(t, opts.PipelineRunName, "llm-fix|parent-pr|reviewer|abc123")
	assert.Assert(t, opts.PipelineRun == nil)
}

func TestQueuedFixCheckRunStatusOptsHasFriendlyText(t *testing.T) {
	opts := queuedFixCheckRunStatusOpts("parent-pr", "reviewer", "abc123")
	assert.Equal(t, opts.Status, "queued")
	assert.Equal(t, opts.Conclusion, providerstatus.ConclusionPending)
	assert.Equal(t, opts.Summary, "Applying AI-suggested fix.")
	assert.Assert(t, contains(opts.Text, "Applying the AI-suggested changes for the **reviewer** role"))
}

func TestBuildFixCommitMessageUsesMetadata(t *testing.T) {
	metadata := map[string]string{
		"commit_subject": "fix: avoid creating a second check run when applying suggestions",
		"commit_body":    "The check-run reuse logic was bypassed for fix runs.",
	}
	subject, body := buildFixCommitMessage("reviewer", metadata)
	assert.Equal(t, subject, "fix: avoid creating a second check run when applying suggestions")
	assert.Equal(t, body, "The check-run reuse logic was bypassed for fix runs.")
}

func TestBuildFixCommitMessageFallsBackWhenMetadataIsEmpty(t *testing.T) {
	subject, body := buildFixCommitMessage("reviewer", map[string]string{})
	assert.Equal(t, subject, "fix: address reviewer findings")
	assert.Equal(t, body, "Apply the AI-generated fix for role \"reviewer\".")
}

func TestBuildFixCommitMessageFallsBackWhenMetadataIsNil(t *testing.T) {
	subject, body := buildFixCommitMessage("reviewer", nil)
	assert.Equal(t, subject, "fix: address reviewer findings")
	assert.Equal(t, body, "Apply the AI-generated fix for role \"reviewer\".")
}

func TestBuildFixCommitMessageSubjectOnly(t *testing.T) {
	metadata := map[string]string{
		"commit_subject": "fix: handle nil pointer in auth handler",
	}
	subject, body := buildFixCommitMessage("reviewer", metadata)
	assert.Equal(t, subject, "fix: handle nil pointer in auth handler")
	assert.Equal(t, body, "Apply the AI-generated fix for role \"reviewer\".")
}

func TestBuildFixCommitMessageStripsMarkdownFences(t *testing.T) {
	metadata := map[string]string{
		"commit_subject": "```text\nfix: restrict tekton-pr-notify skill execution to CI context\n```",
		"commit_body":    "```\nThe skill should only run when PAC_LLM_EXECUTION_CONTEXT=ci is present.\n```",
	}
	subject, body := buildFixCommitMessage("reviewer", metadata)
	assert.Equal(t, subject, "fix: restrict tekton-pr-notify skill execution to CI context")
	assert.Equal(t, body, "The skill should only run when PAC_LLM_EXECUTION_CONTEXT=ci is present.")
}

func TestBuildFixCommitMessageTruncatesLongSubject(t *testing.T) {
	metadata := map[string]string{
		"commit_subject": "fix: " + strings.Repeat("a very long description ", 5),
	}
	subject, _ := buildFixCommitMessage("reviewer", metadata)
	assert.Assert(t, len(subject) <= fixCommitSubjectMaxLen)
}

func TestBuildFixCommitMessageSplitsOverflowIntoBody(t *testing.T) {
	long := "fix: remove scheduling constraints blocking nginx deployment The deployment specified a nodeSelector for ssd/gpu nodes that do not exist"
	metadata := map[string]string{
		"commit_subject": long,
	}
	subject, body := buildFixCommitMessage("reviewer", metadata)
	assert.Assert(t, len(subject) <= fixCommitSubjectMaxLen, "subject must fit in %d chars, got %d", fixCommitSubjectMaxLen, len(subject))
	assert.Assert(t, contains(body, "nodeSelector"), "overflow should become the body")
}

func TestBuildFixCommitMessageTruncatesLongBody(t *testing.T) {
	metadata := map[string]string{
		"commit_subject": "fix: something",
		"commit_body":    strings.Repeat("Long body text. ", 30),
	}
	_, body := buildFixCommitMessage("reviewer", metadata)
	assert.Assert(t, len(body) <= fixCommitBodyMaxLen)
}

func TestFixScriptChecksRemoteBranchTip(t *testing.T) {
	assert.Assert(t, contains(fixScriptTemplateContent, "git config --global --add safe.directory"), "should trust the working tree before git operations")
	assert.Assert(t, contains(fixScriptTemplateContent, "git config --global --add safe.directory \"${repo_dir}/\""), "should trust the slash-normalized workspace path")
	assert.Assert(t, contains(fixScriptTemplateContent, "git checkout -B"), "should check out the branch before applying patch")
	assert.Assert(t, contains(fixScriptTemplateContent, "git fetch --depth=1 origin"), "should refresh the remote branch before push")
	assert.Assert(t, contains(fixScriptTemplateContent, "git rev-parse \"origin/${pr_branch}\""), "should compare against the remote branch tip")
	assert.Assert(t, contains(fixScriptTemplateContent, "branch_moved"), "should emit a branch_moved error envelope when the branch advanced")
	assert.Assert(t, contains(fixScriptTemplateContent, "missing_checkout"), "should emit a missing_checkout error envelope when the shell clone failed")
	assert.Assert(t, contains(fixScriptTemplateContent, "FIX_COMMIT_SUBJECT_B64"), "should read generated commit subject from the environment")
	assert.Assert(t, contains(fixScriptTemplateContent, "FIX_COMMIT_BODY_B64"), "should read generated commit body from the environment")
	assert.Assert(t, contains(fixScriptTemplateContent, "git apply"), "should apply the stored patch")
	assert.Assert(t, contains(fixScriptTemplateContent, "base64 -d"), "should decode the patch payload")
	assert.Assert(t, contains(fixScriptTemplateContent, "Analyzed commit:"), "should include the analyzed commit in the commit body")
	assert.Assert(t, contains(fixScriptTemplateContent, "commit_short_sha"), "should emit pushed commit metadata")
	assert.Assert(t, contains(fixScriptTemplateContent, "cat \"${envelope_file}\" > \"${result_file}\""), "should persist the real fix envelope to the Tekton result")
	assert.Assert(t, !contains(fixScriptTemplateContent, "AI-suggested patch applied by Pipelines-as-Code"), "fix template must not use the old placeholder commit message")
	assert.Assert(t, !contains(fixScriptTemplateContent, "{\"status\":\"complete\"}"), "fix template must not overwrite the real envelope with a completion marker")
	assert.Assert(t, !contains(fixScriptTemplateContent, "git init"), "fix template must not initialize the repo; clone runs in a dedicated step")
	assert.Assert(t, !contains(fixScriptTemplateContent, "LLM_BACKEND"), "fix template must not reference LLM_BACKEND")
}

func TestCreateFixPipelineRunPostsNoPatchStatusWhenAnalysisChildMissing(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	parent := failedPipelineRun()
	// No analysis PipelineRun in the fake client — loadPatchForFix should fail
	tekton := fakepipelineclientset.NewSimpleClientset(parent) //nolint:staticcheck
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
	run.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	event := testEvent()
	event.InstallationID = 12345
	event.HeadBranch = "feature-branch"
	event.CheckRunParentPipelineRun = parent.Name
	event.CheckRunAnalysisRole = "review"
	prov := &statusCaptureProvider{}

	err := CreateFixPipelineRun(context.Background(), run, &kubeinteraction.Interaction{}, testLogger, testAIRepository(), event, prov)
	assert.NilError(t, err)

	// Should post a neutral completed check run explaining that no patch is available
	assert.Assert(t, prov.LastStatusOpts != nil, "should post a status when no patch is found")
	assert.Equal(t, prov.LastStatusOpts.Status, "completed")
	assert.Equal(t, prov.LastStatusOpts.Conclusion, providerstatus.ConclusionNeutral)
	assert.Assert(t, contains(prov.LastStatusOpts.Text, "No automated fix is available"), "should explain that no patch is available")

	// Should NOT have created a fix PipelineRun
	_, err = tekton.TektonV1().PipelineRuns(parent.Namespace).Get(context.Background(), fixPipelineRunName(parent.Name, "review"), metav1.GetOptions{})
	assert.Assert(t, err != nil, "fix PipelineRun should not have been created")
}

func TestMaxInlinePatchBytesConstant(t *testing.T) {
	assert.Equal(t, maxInlinePatchBytes, 512*1024)
	oversized := strings.Repeat("A", maxInlinePatchBytes+1)
	assert.Assert(t, len(oversized) > maxInlinePatchBytes)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestAnalysisScriptCommitMessageExtraction(t *testing.T) {
	assert.Assert(t, contains(analysisScriptContent, "commit_subject"), "analysis script should extract commit subject")
	assert.Assert(t, contains(analysisScriptContent, "commit_body"), "analysis script should extract commit body")
	assert.Assert(t, contains(analysisScriptContent, "Commit"), "analysis script should look for Commit message heading")
	assert.Assert(t, contains(analysisScriptContent, "/^[[:space:]]*```/d"), "analysis script should drop markdown fence lines from commit metadata")
}

func TestFixScriptAuthorFallback(t *testing.T) {
	assert.Assert(t, contains(fixScriptTemplateContent, "GIT_AUTHOR_NAME"), "should read author name from env")
	assert.Assert(t, contains(fixScriptTemplateContent, "GIT_AUTHOR_EMAIL"), "should read author email from env")
	assert.Assert(t, contains(fixScriptTemplateContent, "if [ -n \"${GIT_AUTHOR_NAME}\" ] && [ -n \"${GIT_AUTHOR_EMAIL}\" ]"), "should check both name and email")
	assert.Assert(t, contains(fixScriptTemplateContent, "Pipelines as Code AI"), "should have fallback author")
	assert.Assert(t, contains(fixScriptTemplateContent, "Co-authored-by:"), "should include co-author trailer")
	assert.Assert(t, contains(fixScriptTemplateContent, "GIT_COAUTHOR_NAME"), "should read co-author name from env")
	assert.Assert(t, contains(fixScriptTemplateContent, "GIT_COAUTHOR_EMAIL"), "should read co-author email from env")
}

func TestBuildFixPipelineRunAuthorEnvVars(t *testing.T) {
	config := &v1alpha1.AIAnalysisConfig{
		Backend:   "claude",
		Image:     "test:latest",
		SecretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
		Roles:     []v1alpha1.AnalysisRole{{Name: "reviewer", Prompt: "fix it"}},
	}
	parent := failedPipelineRun()

	t.Run("with trustworthy author", func(t *testing.T) {
		event := &info.Event{
			HeadBranch:     "feature-branch",
			URL:            "https://github.com/test/repo",
			SHA:            "abc123",
			SHAAuthorName:  "Jane Developer",
			SHAAuthorEmail: "jane@example.com",
		}

		pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", "ghcr.io/git:latest", nil)

		// Extract env vars from the fix step
		task := pr.Spec.PipelineSpec.Tasks[0]
		fixStep := task.TaskSpec.Steps[1]
		envMap := make(map[string]string)
		for _, env := range fixStep.Env {
			envMap[env.Name] = env.Value
		}

		assert.Equal(t, envMap["GIT_AUTHOR_NAME"], "Jane Developer")
		assert.Equal(t, envMap["GIT_AUTHOR_EMAIL"], "jane@example.com")
		assert.Equal(t, envMap["GIT_COAUTHOR_NAME"], "Pipelines as Code AI")
		assert.Equal(t, envMap["GIT_COAUTHOR_EMAIL"], "noreply@pipelinesascode.dev")
	})

	t.Run("without author", func(t *testing.T) {
		event := &info.Event{
			HeadBranch: "feature-branch",
			URL:        "https://github.com/test/repo",
			SHA:        "abc123",
			// No SHAAuthorName or SHAAuthorEmail
		}

		pr := buildFixPipelineRun(config, testAIRepository(), parent, event, "reviewer", "encodedpayload", "fix: update docs", "Apply the documentation fix.", "ghcr.io/git:latest", nil)

		// Extract env vars from the fix step
		task := pr.Spec.PipelineSpec.Tasks[0]
		fixStep := task.TaskSpec.Steps[1]
		envMap := make(map[string]string)
		for _, env := range fixStep.Env {
			envMap[env.Name] = env.Value
		}

		// Should have empty strings, shell script will use fallback
		assert.Equal(t, envMap["GIT_AUTHOR_NAME"], "")
		assert.Equal(t, envMap["GIT_AUTHOR_EMAIL"], "")
		assert.Equal(t, envMap["GIT_COAUTHOR_NAME"], "Pipelines as Code AI")
		assert.Equal(t, envMap["GIT_COAUTHOR_EMAIL"], "noreply@pipelinesascode.dev")
	})
}

func TestPatchPayloadBase64Validation(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantError bool
	}{
		{
			name:      "valid base64",
			payload:   base64.StdEncoding.EncodeToString([]byte("valid patch data")),
			wantError: false,
		},
		{
			name:      "invalid base64 with bad chars",
			payload:   "not!valid@base64#data",
			wantError: true,
		},
		{
			name:      "truncated base64",
			payload:   "SGVsbG8gV29ybGQ",
			wantError: true,
		},
		{
			name:      "empty payload",
			payload:   "",
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := base64.StdEncoding.DecodeString(tt.payload)
			if tt.wantError {
				assert.Assert(t, err != nil, "expected base64 decode error for payload %q", tt.payload)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestFixScriptEmptyPatchGuard(t *testing.T) {
	assert.Assert(t, contains(fixScriptTemplateContent, "if [ -z \"${PATCH_DATA_B64GZ}\" ]"), "should check for empty patch data")
	assert.Assert(t, contains(fixScriptTemplateContent, "empty_patch"), "should report empty_patch error type")
}
