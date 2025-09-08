package llm

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"knative.dev/pkg/apis"
)

// MockLLMClient implements the Client interface for testing
type MockLLMClient struct {
	AnalyzeResponse *AnalysisResponse
	AnalyzeError    error
	ValidateError   error
	ProviderName    string
}

func (m *MockLLMClient) Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error) {
	if m.AnalyzeError != nil {
		return nil, m.AnalyzeError
	}
	return m.AnalyzeResponse, nil
}

func (m *MockLLMClient) GetProviderName() string {
	if m.ProviderName != "" {
		return m.ProviderName
	}
	return "mock"
}

func (m *MockLLMClient) ValidateConfig() error {
	return m.ValidateError
}

// MockProvider implements basic provider interface for testing
type MockProvider struct {
	CreateCommentError error
	CreateStatusError  error
}

func (m *MockProvider) SetLogger(logger interface{}) {}
func (m *MockProvider) Validate(ctx context.Context, params *params.Run, event *info.Event) error {
	return nil
}
func (m *MockProvider) Detect(req interface{}, payload string, logger interface{}) (bool, bool, interface{}, string, error) {
	return false, false, nil, "", nil
}
func (m *MockProvider) ParsePayload(ctx context.Context, run *params.Run, request interface{}, payload string) (*info.Event, error) {
	return nil, nil
}
func (m *MockProvider) IsAllowed(ctx context.Context, event *info.Event) (bool, error) {
	return true, nil
}
func (m *MockProvider) IsAllowedOwnersFile(ctx context.Context, event *info.Event) (bool, error) {
	return true, nil
}
func (m *MockProvider) CreateStatus(ctx context.Context, event *info.Event, opts provider.StatusOpts) error {
	return m.CreateStatusError
}
func (m *MockProvider) GetTektonDir(ctx context.Context, event *info.Event, path, provenance string) (string, error) {
	return "", nil
}
func (m *MockProvider) GetFileInsideRepo(ctx context.Context, event *info.Event, path, branch string) (string, error) {
	return "", nil
}
func (m *MockProvider) SetClient(ctx context.Context, run *params.Run, event *info.Event, repo *v1alpha1.Repository, eventEmitter interface{}) error {
	return nil
}
func (m *MockProvider) SetPacInfo(pacInfo *info.PacOpts) {}
func (m *MockProvider) GetCommitInfo(ctx context.Context, event *info.Event) error {
	return nil
}
func (m *MockProvider) GetConfig() *info.ProviderConfig {
	return &info.ProviderConfig{}
}
func (m *MockProvider) GetFiles(ctx context.Context, event *info.Event) (interface{}, error) {
	return nil, nil
}
func (m *MockProvider) GetTaskURI(ctx context.Context, event *info.Event, uri string) (bool, string, error) {
	return false, "", nil
}
func (m *MockProvider) CreateToken(ctx context.Context, scopes []string, event *info.Event) (string, error) {
	return "", nil
}
func (m *MockProvider) CheckPolicyAllowing(ctx context.Context, event *info.Event, scopes []string) (bool, string) {
	return true, ""
}
func (m *MockProvider) GetTemplate(templateType provider.CommentType) string {
	return ""
}
func (m *MockProvider) CreateComment(ctx context.Context, event *info.Event, comment, updateMarker string) error {
	return m.CreateCommentError
}

func TestAnalyzer_Analyze(t *testing.T) {
	logger := logger.GetLogger()
	
	// Create fake Kubernetes client
	fakeClient := fake.NewSimpleClientset()
	
	run := &params.Run{
		Clients: params.Clients{
			Kube: fakeClient,
		},
	}
	
	// Create mock kubeinteraction
	kinteract := &kubeinteraction.Interaction{}
	
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name         string
		request      *AnalyzeRequest
		wantResults  int
		wantError    bool
		setupRepo    func() *v1alpha1.Repository
	}{
		{
			name: "no ai analysis config",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository:  &v1alpha1.Repository{},
				Provider:    &MockProvider{},
			},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "ai analysis disabled",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository: &v1alpha1.Repository{
					Spec: v1alpha1.RepositorySpec{
						Settings: &v1alpha1.Settings{
							AIAnalysis: &v1alpha1.AIAnalysisConfig{
								Enabled: false,
							},
						},
					},
				},
				Provider: &MockProvider{},
			},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "invalid config",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository: &v1alpha1.Repository{
					Spec: v1alpha1.RepositorySpec{
						Settings: &v1alpha1.Settings{
							AIAnalysis: &v1alpha1.AIAnalysisConfig{
								Enabled:  true,
								Provider: "openai",
								// Missing required fields
							},
						},
					},
				},
				Provider: &MockProvider{},
			},
			wantResults: 0,
			wantError:   true,
		},
		{
			name:    "nil request",
			request: nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results, err := analyzer.Analyze(ctx, tt.request)
			
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, len(results), tt.wantResults)
			}
		})
	}
}

func TestAnalyzer_ValidateConfig(t *testing.T) {
	logger := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name      string
		config    *v1alpha1.AIAnalysisConfig
		wantError bool
	}{
		{
			name: "valid config",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: false,
		},
		{
			name: "missing provider",
			config: &v1alpha1.AIAnalysisConfig{
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "missing token secret ref",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "no roles",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{},
			},
			wantError: true,
		},
		{
			name: "invalid role - missing name",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid role - missing prompt",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid role - invalid output",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "invalid-output",
					},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := analyzer.validateConfig(tt.config)
			
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func createTestPipelineRun(status string, reason string) *tektonv1.PipelineRun {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "default",
		},
		Spec: tektonv1.PipelineRunSpec{},
		Status: tektonv1.PipelineRunStatus{},
	}

	// Add status condition
	if status != "" && reason != "" {
		condition := apis.Condition{
			Type:   apis.ConditionSucceeded,
			Status: corev1.ConditionStatus(status),
			Reason: reason,
		}
		pr.Status.SetCondition(&condition)
	}

	return pr
}

func TestAnalyzer_ShouldTriggerRole(t *testing.T) {
	logger := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name        string
		role        v1alpha1.AnalysisRole
		celContext  map[string]interface{}
		wantTrigger bool
		wantError   bool
	}{
		{
			name: "no cel expression - always trigger",
			role: v1alpha1.AnalysisRole{
				Name: "test-role",
			},
			celContext:  map[string]interface{}{},
			wantTrigger: true,
			wantError:   false,
		},
		{
			name: "simple true expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "true",
			},
			celContext:  map[string]interface{}{},
			wantTrigger: true,
			wantError:   false,
		},
		{
			name: "simple false expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "false",
			},
			celContext:  map[string]interface{}{},
			wantTrigger: false,
			wantError:   false,
		},
		{
			name: "invalid cel expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "invalid syntax (",
			},
			celContext:  map[string]interface{}{},
			wantTrigger: false,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldTrigger, err := analyzer.shouldTriggerRole(tt.role, tt.celContext)
			
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, shouldTrigger, tt.wantTrigger)
			}
		})
	}
}