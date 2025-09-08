package llm

import (
	"context"
	"time"
)

// Client defines the interface for LLM provider implementations
type Client interface {
	// Analyze sends an analysis request to the LLM provider and returns the response
	Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error)
	
	// GetProviderName returns the name of the LLM provider (e.g., "openai", "gemini")
	GetProviderName() string
	
	// ValidateConfig validates the provider-specific configuration
	ValidateConfig() error
}

// AnalysisRequest represents a request to analyze CI/CD pipeline data
type AnalysisRequest struct {
	// Prompt is the base prompt template for the LLM
	Prompt string `json:"prompt"`
	
	// Context contains the assembled context data (logs, diffs, PR info, etc.)
	Context map[string]interface{} `json:"context"`
	
	// JSONOutput indicates whether structured JSON response is expected
	JSONOutput bool `json:"json_output"`
	
	// MaxTokens limits the response length
	MaxTokens int `json:"max_tokens"`
	
	// TimeoutSeconds sets the request timeout
	TimeoutSeconds int `json:"timeout_seconds"`
}

// AnalysisResponse represents the response from an LLM analysis
type AnalysisResponse struct {
	// Content is the raw text response from the LLM
	Content string `json:"content"`
	
	// TokensUsed is the number of tokens consumed by this request
	TokensUsed int `json:"tokens_used"`
	
	// JSONParsed contains parsed JSON if JSONOutput was requested and parsing succeeded
	JSONParsed map[string]interface{} `json:"json_parsed,omitempty"`
	
	// Provider is the name of the LLM provider that generated this response
	Provider string `json:"provider"`
	
	// Timestamp when the analysis was completed
	Timestamp time.Time `json:"timestamp"`
	
	// Duration of the LLM request
	Duration time.Duration `json:"duration"`
}

// AnalysisError represents an error during LLM analysis
type AnalysisError struct {
	// Provider that generated the error
	Provider string
	
	// Type of error (timeout, quota_exceeded, invalid_request, etc.)
	Type string
	
	// Original error message
	Message string
	
	// Whether this error is retryable
	Retryable bool
}

func (e *AnalysisError) Error() string {
	return e.Message
}

// ContextItem represents different types of context that can be included in analysis
type ContextItem string

const (
	ContextItemCommitContent   ContextItem = "commit_content"
	ContextItemPRContent      ContextItem = "pr_content"
	ContextItemErrorContent   ContextItem = "error_content"
	ContextItemContainerLogs  ContextItem = "container_logs"
	ContextItemTaskResults    ContextItem = "task_results"
	ContextItemPipelineStatus ContextItem = "pipeline_status"
)

// OutputDestination represents where LLM analysis results should be sent
type OutputDestination string

const (
	OutputDestinationPRComment  OutputDestination = "pr-comment"
	OutputDestinationCheckRun   OutputDestination = "check-run"
	OutputDestinationAnnotation OutputDestination = "annotation"
)

// LLMProvider represents supported LLM providers
type LLMProvider string

const (
	LLMProviderOpenAI LLMProvider = "openai"
	LLMProviderGemini LLMProvider = "gemini"
)

// DefaultConfig provides default values for LLM analysis
var DefaultConfig = struct {
	TimeoutSeconds int
	MaxTokens      int
	MaxLogLines    int
}{
	TimeoutSeconds: 30,
	MaxTokens:      1000,
	MaxLogLines:    50,
}