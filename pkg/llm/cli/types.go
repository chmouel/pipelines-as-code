package cli

// Input is the JSON structure written to the CLI agent's stdin.
type Input struct {
	// Prompt is the fully assembled prompt text with context sections baked in.
	Prompt string `json:"prompt"`
	// Model is the LLM model to use (may be empty to use agent's default).
	Model string `json:"model,omitempty"`
	// MaxTokens is the maximum response length.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Context is the raw context map, provided alongside the prompt so the
	// agent can access structured data if needed.
	Context map[string]interface{} `json:"context,omitempty"`
}

// Output is the JSON structure read from the CLI agent's stdout.
type Output struct {
	// Content is the analysis result text. Required.
	Content string `json:"content"`
	// Metadata contains optional information about the analysis.
	Metadata *Metadata `json:"metadata,omitempty"`
}

// Metadata contains optional metadata from the CLI agent response.
type Metadata struct {
	TokensUsed int    `json:"tokens_used,omitempty"`
	Model      string `json:"model,omitempty"`
	Provider   string `json:"provider,omitempty"`
}

const (
	// ExitRetryable indicates a retryable error (rate limit, server error).
	ExitRetryable = 1
	// ExitNonRetryable indicates a non-retryable error (auth failure, invalid model).
	ExitNonRetryable = 2
)
