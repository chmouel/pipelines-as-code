package llm

import (
	"context"
	"time"
)

const (
	// DefaultTimeoutSeconds is the default timeout for LLM API calls.
	DefaultTimeoutSeconds = 900

	// DefaultMaxTokens is the default maximum tokens for LLM responses.
	DefaultMaxTokens = 1000

	// MaxCheckRunOutputSize is the safe upper bound for GitHub check-run text fields.
	// GitHub's hard limit is 65,535; we leave 535 bytes of margin.
	MaxCheckRunOutputSize = 65000
)

// AnalysisExecutionMode represents how an LLM analysis is executed.
type AnalysisExecutionMode string

const (
	ExecutionModePipelineRun AnalysisExecutionMode = "pipelinerun"
)

// AgentBackend represents a supported CLI backend.
type AgentBackend string

const (
	BackendCodex        AgentBackend = "codex"
	BackendClaude       AgentBackend = "claude"
	BackendClaudeVertex AgentBackend = "claude-vertex"
	BackendGemini       AgentBackend = "gemini"
	BackendOpencode     AgentBackend = "opencode"
)

// Client defines the interface for LLM providers.
type Client interface {
	Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error)
	GetProviderName() string
	ValidateConfig() error
}

// AnalysisRequest represents a request to analyze CI/CD pipeline data.
type AnalysisRequest struct {
	Prompt         string                 `json:"prompt"`
	Context        map[string]interface{} `json:"context"`
	Model          string                 `json:"model,omitempty"`
	MaxTokens      int                    `json:"max_tokens"`
	TimeoutSeconds int                    `json:"timeout_seconds"`
}

// AnalysisResponse represents the response from an LLM analysis.
type AnalysisResponse struct {
	Content    string        `json:"content"`
	TokensUsed int           `json:"tokens_used"`
	Provider   string        `json:"provider"`
	Model      string        `json:"model,omitempty"`
	Timestamp  time.Time     `json:"timestamp"`
	Duration   time.Duration `json:"duration"`
}

// AnalysisError represents an error from LLM analysis.
type AnalysisError struct {
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *AnalysisError) Error() string {
	return e.Message
}

// AnalysisEnvelope is the JSON payload written by child analysis PipelineRuns.
type AnalysisEnvelope struct {
	Status     string            `json:"status"`
	Backend    string            `json:"backend"`
	Model      string            `json:"model,omitempty"`
	Content    string            `json:"content,omitempty"`
	TokensUsed int               `json:"tokens_used,omitempty"`
	DurationMS int64             `json:"duration_ms,omitempty"`
	Error      *AnalysisError    `json:"error,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AnalysisSummary stores the collection status of an analysis PipelineRun on the parent.
type AnalysisSummary struct {
	PipelineRunName string         `json:"pipeline_run_name"`
	Backend         string         `json:"backend"`
	Model           string         `json:"model,omitempty"`
	Status          string         `json:"status"`
	TokensUsed      int            `json:"tokens_used,omitempty"`
	DurationMS      int64          `json:"duration_ms,omitempty"`
	CollectedAt     time.Time      `json:"collected_at"`
	Error           *AnalysisError `json:"error,omitempty"`
	PatchAvailable  bool           `json:"patch_available,omitempty"`
	PatchBaseSHA    string         `json:"patch_base_sha,omitempty"`
	PatchFormat     string         `json:"patch_format,omitempty"`
	PatchVersion    int            `json:"patch_version,omitempty"`
}

// AIProvider represents a supported LLM provider.
type AIProvider string

const (
	ProviderOpenAI AIProvider = "openai"
	ProviderGemini AIProvider = "gemini"
)
