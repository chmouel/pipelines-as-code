package v1alpha1

const (
	// defaultContainerLogsMaxLines is the default maximum number of log lines to fetch per container.
	defaultContainerLogsMaxLines = 50

	// DefaultAIExecutionMode is the only supported execution mode for CLI-based analysis.
	DefaultAIExecutionMode = "pipelinerun"

	// DefaultVertexRegion is the default GCP region for Vertex AI.
	DefaultVertexRegion = "global"
)

// AIAnalysisConfig defines configuration for AI/LLM-powered analysis of CI/CD pipeline events.
// Use Backend + Image to select which CLI tool runs inside a Kubernetes PipelineRun.
type AIAnalysisConfig struct {
	// Enabled controls whether AI analysis is active for this repository
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// ExecutionMode controls how analysis is executed.
	// +optional
	// +kubebuilder:default=pipelinerun
	// +kubebuilder:validation:Enum=pipelinerun
	ExecutionMode string `json:"execution_mode,omitempty"`

	// Backend selects the CLI backend used for analysis.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=codex;claude;gemini;claude-vertex;opencode
	Backend string `json:"backend"`

	// Image is the container image used to execute the selected CLI backend.
	// +kubebuilder:validation:Required
	Image string `json:"image"`

	// SecretRef references the Kubernetes secret containing the backend token.
	// +kubebuilder:validation:Required
	SecretRef *Secret `json:"secret_ref"`

	// TimeoutSeconds sets the maximum time to wait for CLI analysis (default: 30)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=900
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// MaxTokens limits the response length from the CLI backend (default: 1000)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4000
	MaxTokens int `json:"max_tokens,omitempty"`

	// VertexProjectID is the GCP project ID for Vertex AI (required when backend is claude-vertex)
	// +optional
	VertexProjectID string `json:"vertex_project_id,omitempty"`

	// VertexRegion is the GCP region for Vertex AI (e.g. us-east5, europe-west1; default: us-east5)
	// +optional
	VertexRegion string `json:"vertex_region,omitempty"`

	// Roles defines different analysis scenarios and their configurations
	// +listType=map
	// +listMapKey=name
	Roles []AnalysisRole `json:"roles,omitempty"`
}

// EnvVar defines an environment variable for the CLI agent.
// Either Value or SecretRef must be set, not both.
type EnvVar struct {
	// Name is the environment variable name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Value is a literal value for the environment variable.
	// +optional
	Value string `json:"value,omitempty"`

	// SecretRef references a Kubernetes secret to source the value from.
	// +optional
	SecretRef *Secret `json:"secret_ref,omitempty"`
}

// AnalysisRole defines a specific analysis scenario with its prompt, conditions, and output configuration.
type AnalysisRole struct {
	// Name is a unique identifier for this analysis role
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Prompt is the base prompt template sent to the backend for analysis
	// +kubebuilder:validation:Required
	Prompt string `json:"prompt"`

	// Image overrides the top-level AIAnalysisConfig.Image for this role only.
	// When set, the child PipelineRun for this role uses this container image
	// instead of the shared default image, allowing different roles to use
	// different agent images (e.g. a heavier model image for code review vs.
	// a lighter one for failure summarisation).
	// +optional
	Image string `json:"image,omitempty"`

	// Model specifies which model to use for this role (optional).
	// +optional
	Model string `json:"model,omitempty"`

	// MaxTokens limits the response length for this specific role.
	// Overrides the top-level MaxTokens setting when set.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4000
	MaxTokens int `json:"max_tokens,omitempty"`

	// OnCEL is a CEL expression that determines when this role should be triggered
	// +optional
	OnCEL string `json:"on_cel,omitempty"`

	// Output specifies where the analysis results should be sent (default: pr-comment)
	// +optional
	// +kubebuilder:default=pr-comment
	// +kubebuilder:validation:Enum=pr-comment;check-run
	Output string `json:"output,omitempty"`

	// ContextItems defines what context data to include in the analysis
	// +optional
	ContextItems *ContextConfig `json:"context_items,omitempty"`
}

// ContextConfig defines what contextual information to include in LLM analysis.
type ContextConfig struct {
	// CommitContent includes commit message and diff information
	// +optional
	CommitContent bool `json:"commit_content,omitempty" yaml:"commit_content,omitempty"`

	// PRContent includes pull request title, description, and metadata
	// +optional
	PRContent bool `json:"pr_content,omitempty" yaml:"pr_content,omitempty"`

	// ErrorContent includes error messages and failure summaries
	// +optional
	ErrorContent bool `json:"error_content,omitempty" yaml:"error_content,omitempty"`

	// ContainerLogs configures inclusion of container/task logs
	// +optional
	ContainerLogs *ContainerLogsConfig `json:"container_logs,omitempty" yaml:"container_logs,omitempty"`

	// DiffContent includes the pull request code diff
	// +optional
	DiffContent bool `json:"diff_content,omitempty" yaml:"diff_content,omitempty"`

	// Files lists repository file paths to include verbatim in the context
	// +optional
	Files []string `json:"files,omitempty" yaml:"files,omitempty"`
}

// ContainerLogsConfig defines how container logs should be included in analysis.
type ContainerLogsConfig struct {
	// Enabled controls whether container logs are included
	Enabled bool `json:"enabled" yaml:"enabled"`

	// MaxLines limits the number of log lines to include (default: 50)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MaxLines int `json:"max_lines,omitempty" yaml:"max_lines,omitempty"`
}

func (c *ContainerLogsConfig) GetMaxLines() int {
	if c == nil || c.MaxLines == 0 {
		return defaultContainerLogsMaxLines
	}
	return c.MaxLines
}

// GetExecutionMode returns the configured execution mode with a default value.
func (c *AIAnalysisConfig) GetExecutionMode() string {
	if c == nil || c.ExecutionMode == "" {
		return DefaultAIExecutionMode
	}
	return c.ExecutionMode
}

// GetOutput returns the output destination with a default value if not specified.
func (r *AnalysisRole) GetOutput() string {
	if r.Output == "" {
		return "pr-comment"
	}
	return r.Output
}

// GetVertexRegion returns the configured Vertex AI region with a default value.
func (c *AIAnalysisConfig) GetVertexRegion() string {
	if c == nil || c.VertexRegion == "" {
		return DefaultVertexRegion
	}
	return c.VertexRegion
}

// GetImage returns the role-level image override, or an empty string if not set
// (callers should fall back to AIAnalysisConfig.Image in that case).
func (r *AnalysisRole) GetImage() string {
	return r.Image
}

// GetModel returns the configured model or an empty string to use backend defaults.
func (r *AnalysisRole) GetModel() string {
	return r.Model
}

// GetMaxTokens returns the role-level MaxTokens if set, otherwise 0 (caller should use global default).
func (r *AnalysisRole) GetMaxTokens() int {
	return r.MaxTokens
}
