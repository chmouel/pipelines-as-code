package llm

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	llmtypes "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/gemini"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/openai"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	"go.uber.org/zap"
)

// ClientConfig holds the configuration needed to create LLM clients.
type ClientConfig struct {
	Provider       llmtypes.AIProvider
	TokenSecretRef *v1alpha1.Secret
	TimeoutSeconds int
	MaxTokens      int
}

// Factory creates LLM clients based on provider configuration.
type Factory struct {
	run       *params.Run
	kinteract kubeinteraction.Interface
}

// NewFactory creates a new LLM client factory.
func NewFactory(run *params.Run, kinteract kubeinteraction.Interface) *Factory {
	return &Factory{
		run:       run,
		kinteract: kinteract,
	}
}

// CreateClient creates an LLM client based on the provided configuration.
func (f *Factory) CreateClient(ctx context.Context, config *ClientConfig, namespace string) (llmtypes.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("client configuration is required")
	}

	// Retrieve the API token from the secret
	token, err := f.getTokenFromSecret(ctx, config.TokenSecretRef, namespace)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve LLM token: %w", err)
	}

	// Apply defaults
	timeoutSeconds, maxTokens := f.applyDefaults(config.TimeoutSeconds, config.MaxTokens)

	// Create provider-specific client directly
	baseClient, err := f.createProviderClient(config.Provider, token, timeoutSeconds, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s client: %w", config.Provider, err)
	}

	// Wrap with resilient client for retry and circuit breaker functionality
	logger := f.getLogger()
	return NewResilientClient(baseClient, logger), nil
}

// ValidateConfig validates the client configuration.
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
	if !f.isProviderSupported(config.Provider) {
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

// GetSupportedProviders returns a list of supported LLM providers.
func (f *Factory) GetSupportedProviders() []llmtypes.AIProvider {
	return []llmtypes.AIProvider{
		llmtypes.LLMProviderOpenAI,
		llmtypes.LLMProviderGemini,
	}
}

// isProviderSupported checks if the given provider is supported.
func (f *Factory) isProviderSupported(provider llmtypes.AIProvider) bool {
	for _, supported := range f.GetSupportedProviders() {
		if provider == supported {
			return true
		}
	}
	return false
}

// getTokenFromSecret retrieves the API token from a Kubernetes secret.
func (f *Factory) getTokenFromSecret(ctx context.Context, secretRef *v1alpha1.Secret, namespace string) (string, error) {
	if secretRef == nil {
		return "", fmt.Errorf("secret reference is nil")
	}

	// Use the default key if not specified
	key := secretRef.Key
	if key == "" {
		key = "token"
	}

	opt := types.GetSecretOpt{
		Namespace: namespace,
		Name:      secretRef.Name,
		Key:       key,
	}

	// Retrieve the secret value using kubeinteraction
	secretValue, err := f.kinteract.GetSecret(ctx, opt)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
	}

	if secretValue == "" {
		return "", fmt.Errorf("secret %s/%s key %s is empty", namespace, secretRef.Name, key)
	}

	return secretValue, nil
}

// applyDefaults applies default values for timeout and max tokens if not specified.
func (f *Factory) applyDefaults(timeoutSeconds, maxTokens int) (int, int) {
	if timeoutSeconds == 0 {
		timeoutSeconds = llmtypes.DefaultConfig.TimeoutSeconds
	}
	if maxTokens == 0 {
		maxTokens = llmtypes.DefaultConfig.MaxTokens
	}
	return timeoutSeconds, maxTokens
}

// getLogger returns the logger from run.Clients.Log or creates a development logger.
func (f *Factory) getLogger() *zap.SugaredLogger {
	if f.run.Clients.Log != nil {
		return f.run.Clients.Log
	}
	// Create a basic logger if none is available
	devLogger, _ := zap.NewDevelopment()
	return devLogger.Sugar()
}

// createProviderClient creates a provider-specific client.
func (f *Factory) createProviderClient(provider llmtypes.AIProvider, token string, timeoutSeconds, maxTokens int) (llmtypes.Client, error) {
	switch provider {
	case llmtypes.LLMProviderOpenAI:
		return openai.NewClient(&openai.Config{
			APIKey:         token,
			TimeoutSeconds: timeoutSeconds,
			MaxTokens:      maxTokens,
		})
	case llmtypes.LLMProviderGemini:
		return gemini.NewClient(&gemini.Config{
			APIKey:         token,
			TimeoutSeconds: timeoutSeconds,
			MaxTokens:      maxTokens,
		})
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", provider)
	}
}

// CreateClientFromProvider creates a client directly from provider string and secret info.
func (f *Factory) CreateClientFromProvider(ctx context.Context, provider, secretName, secretKey, namespace string, timeoutSeconds, maxTokens int) (llmtypes.Client, error) {
	config := &ClientConfig{
		Provider: llmtypes.AIProvider(provider),
		TokenSecretRef: &v1alpha1.Secret{
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
