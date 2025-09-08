package llm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"go.uber.org/zap"
)

// HealthChecker performs health checks on LLM providers.
type HealthChecker struct {
	logger *zap.SugaredLogger
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(logger *zap.SugaredLogger) *HealthChecker {
	return &HealthChecker{
		logger: logger,
	}
}

// HealthCheckResult represents the result of a health check.
type HealthCheckResult struct {
	Provider     string        `json:"provider"`
	Healthy      bool          `json:"healthy"`
	ResponseTime time.Duration `json:"response_time"`
	Error        string        `json:"error,omitempty"`
	TokensUsed   int           `json:"tokens_used,omitempty"`
	Timestamp    time.Time     `json:"timestamp"`
}

// CheckHealth performs a health check on an LLM client.
func (h *HealthChecker) CheckHealth(ctx context.Context, client ltypes.Client) *HealthCheckResult {
	startTime := time.Now()
	result := &HealthCheckResult{
		Provider:  client.GetProviderName(),
		Timestamp: startTime,
	}

	// Create a simple health check request
	request := &ltypes.AnalysisRequest{
		Prompt:         "Hello, please respond with 'OK' to confirm you are working.",
		Context:        map[string]interface{}{},
		JSONOutput:     false,
		MaxTokens:      10, // Minimal tokens for health check
		TimeoutSeconds: 10, // Short timeout for health check
	}

	response, err := client.Analyze(ctx, request)
	result.ResponseTime = time.Since(startTime)

	if err != nil {
		result.Healthy = false
		result.Error = err.Error()
		h.logger.Warnf("Health check failed for provider %s: %v", client.GetProviderName(), err)
		return result
	}

	if response == nil {
		result.Healthy = false
		result.Error = "received nil response"
		h.logger.Warnf("Health check failed for provider %s: nil response", client.GetProviderName())
		return result
	}

	result.Healthy = true
	result.TokensUsed = response.TokensUsed
	h.logger.Infof("Health check successful for provider %s (response time: %v, tokens: %d)",
		client.GetProviderName(), result.ResponseTime, result.TokensUsed)

	return result
}

// ValidateProviderConfig validates provider-specific configuration.
func (h *HealthChecker) ValidateProviderConfig(provider ltypes.AIProvider, config map[string]interface{}) error {
	switch provider {
	case ltypes.LLMProviderOpenAI:
		return h.validateOpenAIConfig(config)
	case ltypes.LLMProviderGemini:
		return h.validateGeminiConfig(config)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
}

// validateOpenAIConfig validates OpenAI-specific configuration.
func (h *HealthChecker) validateOpenAIConfig(config map[string]interface{}) error {
	// Check for required OpenAI configuration
	if baseURL, ok := config["base_url"].(string); ok {
		if baseURL != "" && !isValidURL(baseURL) {
			return fmt.Errorf("invalid OpenAI base URL: %s", baseURL)
		}
	}

	if model, ok := config["model"].(string); ok {
		if model != "" && !isValidOpenAIModel(model) {
			h.logger.Warnf("Using non-standard OpenAI model: %s", model)
		}
	}

	return nil
}

// validateGeminiConfig validates Gemini-specific configuration.
func (h *HealthChecker) validateGeminiConfig(config map[string]interface{}) error {
	// Check for required Gemini configuration
	if baseURL, ok := config["base_url"].(string); ok {
		if baseURL != "" && !isValidURL(baseURL) {
			return fmt.Errorf("invalid Gemini base URL: %s", baseURL)
		}
	}

	if model, ok := config["model"].(string); ok {
		if model != "" && !isValidGeminiModel(model) {
			h.logger.Warnf("Using non-standard Gemini model: %s", model)
		}
	}

	return nil
}

// isValidURL performs basic URL validation.
func isValidURL(url string) bool {
	// Basic validation - starts with http:// or https://
	return len(url) > 8 && (url[:7] == "http://" || url[:8] == "https://")
}

// isValidOpenAIModel checks if the model is a known OpenAI model.
func isValidOpenAIModel(model string) bool {
	knownModels := []string{
		"gpt-4",
		"gpt-4-turbo",
		"gpt-4-turbo-preview",
		"gpt-3.5-turbo",
		"gpt-3.5-turbo-16k",
	}

	for _, known := range knownModels {
		if model == known {
			return true
		}
	}
	return false
}

// isValidGeminiModel checks if the model is a known Gemini model.
func isValidGeminiModel(model string) bool {
	knownModels := []string{
		"gemini-1.5-flash",
		"gemini-1.5-pro",
		"gemini-pro",
		"gemini-pro-vision",
	}

	for _, known := range knownModels {
		if model == known {
			return true
		}
	}
	return false
}

// CheckProviderAvailability performs a lightweight check to see if a provider is reachable.
func (h *HealthChecker) CheckProviderAvailability(_ context.Context, provider ltypes.AIProvider, baseURL string) error {
	// For now, this is a simple implementation that validates the provider type
	// In a full implementation, this could make a HEAD request to the provider's endpoint

	switch provider {
	case ltypes.LLMProviderOpenAI:
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		h.logger.Debugf("Checking OpenAI availability at %s", baseURL)

	case ltypes.LLMProviderGemini:
		if baseURL == "" {
			baseURL = "https://generativelanguage.googleapis.com/v1beta"
		}
		h.logger.Debugf("Checking Gemini availability at %s", baseURL)

	default:
		return fmt.Errorf("unsupported provider for availability check: %s", provider)
	}

	// TODO: Implement actual network connectivity check
	// This could involve making a HEAD request to the base URL
	// For now, we just validate that we know about the provider

	return nil
}

// DiagnoseAnalysisFailure provides diagnostic information for analysis failures.
func (h *HealthChecker) DiagnoseAnalysisFailure(err error, provider string) map[string]interface{} {
	diagnosis := map[string]interface{}{
		"provider":  provider,
		"timestamp": time.Now(),
	}

	var analysisErr *ltypes.AnalysisError
	if errors.As(err, &analysisErr) {
		diagnosis["error_type"] = analysisErr.Type
		diagnosis["retryable"] = analysisErr.Retryable
		diagnosis["provider_error"] = analysisErr.Provider

		// Provide specific recommendations based on error type
		switch analysisErr.Type {
		case "invalid_api_key":
			diagnosis["recommendation"] = "Check that the API key is correct and has not expired"
			diagnosis["action_required"] = "Update the secret with a valid API key"

		case "rate_limit_exceeded":
			diagnosis["recommendation"] = "Reduce analysis frequency or upgrade API plan"
			diagnosis["action_required"] = "Wait before retrying or implement backoff"

		case "quota_exceeded":
			diagnosis["recommendation"] = "Check API usage quota and consider upgrading plan"
			diagnosis["action_required"] = "Review usage or increase quota limits"

		case "timeout":
			diagnosis["recommendation"] = "Increase timeout setting or check network connectivity"
			diagnosis["action_required"] = "Adjust timeout_seconds in configuration"

		case "server_error":
			diagnosis["recommendation"] = "Provider experiencing issues, retry later"
			diagnosis["action_required"] = "Monitor provider status and retry"

		default:
			diagnosis["recommendation"] = "Check error details and provider documentation"
			diagnosis["action_required"] = "Review configuration and error message"
		}
	} else {
		diagnosis["error_type"] = "unknown"
		diagnosis["retryable"] = false
		diagnosis["recommendation"] = "Check error message and configuration"
	}

	diagnosis["error_message"] = err.Error()
	return diagnosis
}
