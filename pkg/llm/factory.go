package llm

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/gemini"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/openai"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	"go.uber.org/zap"
)

// Secret represents a reference to a Kubernetes secret
type Secret struct {
	// Name is the name of the secret
	Name string `json:"name"`
	// Key is the key within the secret (optional, defaults to "token")
	Key string `json:"key,omitempty"`
}

// ClientConfig holds the configuration needed to create LLM clients
type ClientConfig struct {
	Provider       LLMProvider
	TokenSecretRef *Secret
	TimeoutSeconds int
	MaxTokens      int
}

// Factory creates LLM clients based on provider configuration
type Factory struct {
	run *params.Run
}

// NewFactory creates a new LLM client factory
func NewFactory(run *params.Run) *Factory {
	return &Factory{
		run: run,
	}
}

// CreateClient creates an LLM client based on the provided configuration
func (f *Factory) CreateClient(ctx context.Context, config *ClientConfig, namespace string) (Client, error) {
	if config == nil {
		return nil, fmt.Errorf("client configuration is required")
	}

	// Retrieve the API token from the secret
	token, err := f.getTokenFromSecret(ctx, config.TokenSecretRef, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve LLM token: %w", err)
	}

	// Set defaults if not specified
	timeoutSeconds := config.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = DefaultConfig.TimeoutSeconds
	}

	maxTokens := config.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultConfig.MaxTokens
	}

	// Create provider-specific client with adapter
	var baseClient Client
	switch config.Provider {
	case LLMProviderOpenAI:
		providerClient, providerErr := openai.NewClient(&openai.Config{
			APIKey:         token,
			TimeoutSeconds: timeoutSeconds,
			MaxTokens:      maxTokens,
		})
		if providerErr != nil {
			err = providerErr
		} else {
			baseClient = &openaiAdapter{client: providerClient}
		}
	case LLMProviderGemini:
		providerClient, providerErr := gemini.NewClient(&gemini.Config{
			APIKey:         token,
			TimeoutSeconds: timeoutSeconds,
			MaxTokens:      maxTokens,
		})
		if providerErr != nil {
			err = providerErr
		} else {
			baseClient = &geminiAdapter{client: providerClient}
		}
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", config.Provider)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create base client: %w", err)
	}

	// Wrap with resilient client for retry and circuit breaker functionality
	logger := f.run.Clients.Log
	if logger == nil {
		// Create a basic logger if none is available - use zap development logger
		devLogger, _ := zap.NewDevelopment()
		logger = devLogger.Sugar()
	}
	
	return NewResilientClient(baseClient, logger), nil
}


// ValidateConfig validates the client configuration
func (f *Factory) ValidateConfig(config *ClientConfig) error {
	if config == nil {
		return fmt.Errorf("client configuration is required")
	}

	if config.Provider == "" {
		return fmt.Errorf("LLM provider is required")
	}

	if config.TokenSecretRef == nil {
		return fmt.Errorf("token secret reference is required")
	}

	if config.TokenSecretRef.Name == "" {
		return fmt.Errorf("token secret name is required")
	}

	// Validate provider is supported
	switch config.Provider {
	case LLMProviderOpenAI, LLMProviderGemini:
		// Valid providers
	default:
		return fmt.Errorf("unsupported LLM provider: %s", config.Provider)
	}

	// Validate timeout and token limits
	if config.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout seconds must be non-negative")
	}

	if config.MaxTokens < 0 {
		return fmt.Errorf("max tokens must be non-negative")
	}

	return nil
}

// GetSupportedProviders returns a list of supported LLM providers
func (f *Factory) GetSupportedProviders() []LLMProvider {
	return []LLMProvider{
		LLMProviderOpenAI,
		LLMProviderGemini,
	}
}

// getTokenFromSecret retrieves the API token from a Kubernetes secret
func (f *Factory) getTokenFromSecret(ctx context.Context, secretRef *Secret, namespace string) (string, error) {
	if secretRef == nil {
		return "", fmt.Errorf("secret reference is nil")
	}

	// Use the default key if not specified
	key := secretRef.Key
	if key == "" {
		key = "token"
	}

	// Retrieve the secret value using kubeinteraction
	secretValue, err := f.run.Clients.Kube.GetSecret(ctx, secretRef.Name, key, namespace)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
	}

	if secretValue == "" {
		return "", fmt.Errorf("secret %s/%s key %s is empty", namespace, secretRef.Name, key)
	}

	return secretValue, nil
}

// CreateClientFromProvider creates a client directly from provider string and secret info
func (f *Factory) CreateClientFromProvider(ctx context.Context, provider string, secretName, secretKey, namespace string, timeoutSeconds, maxTokens int) (Client, error) {
	config := &ClientConfig{
		Provider: LLMProvider(provider),
		TokenSecretRef: &Secret{
			Name: secretName,
			Key:  secretKey,
		},
		TimeoutSeconds: timeoutSeconds,
		MaxTokens:      maxTokens,
	}

	if err := f.ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid client configuration: %w", err)
	}

	return f.CreateClient(ctx, config, namespace)
}

// openaiAdapter adapts the OpenAI provider client to the main LLM interface
type openaiAdapter struct {
	client *openai.Client
}

func (a *openaiAdapter) Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error) {
	// Convert main package types to provider types
	providerRequest := &openai.AnalysisRequest{
		Prompt:         request.Prompt,
		Context:        request.Context,
		JSONOutput:     request.JSONOutput,
		MaxTokens:      request.MaxTokens,
		TimeoutSeconds: request.TimeoutSeconds,
	}
	
	providerResponse, err := a.client.Analyze(ctx, providerRequest)
	if err != nil {
		return nil, err
	}
	
	// Convert provider types back to main package types
	return &AnalysisResponse{
		Content:    providerResponse.Content,
		TokensUsed: providerResponse.TokensUsed,
		JSONParsed: providerResponse.JSONParsed,
		Provider:   providerResponse.Provider,
		Timestamp:  providerResponse.Timestamp,
		Duration:   providerResponse.Duration,
	}, nil
}

func (a *openaiAdapter) GetProviderName() string {
	return a.client.GetProviderName()
}

func (a *openaiAdapter) ValidateConfig() error {
	return a.client.ValidateConfig()
}

// geminiAdapter adapts the Gemini provider client to the main LLM interface
type geminiAdapter struct {
	client *gemini.Client
}

func (a *geminiAdapter) Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error) {
	// Convert main package types to provider types
	providerRequest := &gemini.AnalysisRequest{
		Prompt:         request.Prompt,
		Context:        request.Context,
		JSONOutput:     request.JSONOutput,
		MaxTokens:      request.MaxTokens,
		TimeoutSeconds: request.TimeoutSeconds,
	}
	
	providerResponse, err := a.client.Analyze(ctx, providerRequest)
	if err != nil {
		return nil, err
	}
	
	// Convert provider types back to main package types
	return &AnalysisResponse{
		Content:    providerResponse.Content,
		TokensUsed: providerResponse.TokensUsed,
		JSONParsed: providerResponse.JSONParsed,
		Provider:   providerResponse.Provider,
		Timestamp:  providerResponse.Timestamp,
		Duration:   providerResponse.Duration,
	}, nil
}

func (a *geminiAdapter) GetProviderName() string {
	return a.client.GetProviderName()
}

func (a *geminiAdapter) ValidateConfig() error {
	return a.client.ValidateConfig()
}