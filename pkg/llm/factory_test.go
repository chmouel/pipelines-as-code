package llm

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFactory_ValidateConfig(t *testing.T) {
	run := &params.Run{}
	factory := NewFactory(run)

	tests := []struct {
		name      string
		config    *ClientConfig
		wantError bool
	}{
		{
			name: "valid openai config",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: false,
		},
		{
			name: "valid gemini config",
			config: &ClientConfig{
				Provider: LLMProviderGemini,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "api_key",
				},
				TimeoutSeconds: 45,
				MaxTokens:      2000,
			},
			wantError: false,
		},
		{
			name:      "nil config",
			config:    nil,
			wantError: true,
		},
		{
			name: "missing provider",
			config: &ClientConfig{
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
			},
			wantError: true,
		},
		{
			name: "missing token secret ref",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
			},
			wantError: true,
		},
		{
			name: "invalid provider",
			config: &ClientConfig{
				Provider: "invalid-provider",
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
			},
			wantError: true,
		},
		{
			name: "negative timeout",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				TimeoutSeconds: -1,
			},
			wantError: true,
		},
		{
			name: "negative max tokens",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				MaxTokens: -1,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := factory.ValidateConfig(tt.config)
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestFactory_CreateClient(t *testing.T) {
	// Create fake Kubernetes client with secret
	fakeClient := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("test-api-key"),
			},
		},
	)

	run := &params.Run{
		Clients: params.Clients{
			Kube: fakeClient,
		},
	}
	factory := NewFactory(run)

	tests := []struct {
		name      string
		config    *ClientConfig
		namespace string
		wantError bool
	}{
		{
			name: "create openai client",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			namespace: "default",
			wantError: false,
		},
		{
			name: "create gemini client",
			config: &ClientConfig{
				Provider: LLMProviderGemini,
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			namespace: "default",
			wantError: false,
		},
		{
			name: "missing secret",
			config: &ClientConfig{
				Provider: LLMProviderOpenAI,
				TokenSecretRef: &secrets.Secret{
					Name: "missing-secret",
					Key:  "token",
				},
			},
			namespace: "default",
			wantError: true,
		},
		{
			name: "unsupported provider",
			config: &ClientConfig{
				Provider: "unsupported",
				TokenSecretRef: &secrets.Secret{
					Name: "test-secret",
					Key:  "token",
				},
			},
			namespace: "default",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client, err := factory.CreateClient(ctx, tt.config, tt.namespace)
			
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.Assert(t, client == nil, "expected nil client on error")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, client != nil, "expected non-nil client")
				
				// Verify client type matches provider
				switch tt.config.Provider {
				case LLMProviderOpenAI:
					assert.Equal(t, client.GetProviderName(), string(LLMProviderOpenAI))
				case LLMProviderGemini:
					assert.Equal(t, client.GetProviderName(), string(LLMProviderGemini))
				}
			}
		})
	}
}

func TestFactory_GetSupportedProviders(t *testing.T) {
	run := &params.Run{}
	factory := NewFactory(run)

	providers := factory.GetSupportedProviders()
	
	assert.Assert(t, len(providers) >= 2, "expected at least 2 supported providers")
	
	// Check that OpenAI and Gemini are supported
	var hasOpenAI, hasGemini bool
	for _, provider := range providers {
		switch provider {
		case LLMProviderOpenAI:
			hasOpenAI = true
		case LLMProviderGemini:
			hasGemini = true
		}
	}
	
	assert.Assert(t, hasOpenAI, "expected OpenAI to be supported")
	assert.Assert(t, hasGemini, "expected Gemini to be supported")
}

func TestFactory_CreateClientFromProvider(t *testing.T) {
	// Create fake Kubernetes client with secret
	fakeClient := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Data: map[string][]byte{
				"token": []byte("test-api-key"),
			},
		},
	)

	run := &params.Run{
		Clients: params.Clients{
			Kube: fakeClient,
		},
	}
	factory := NewFactory(run)

	ctx := context.Background()
	client, err := factory.CreateClientFromProvider(
		ctx,
		"openai",
		"test-secret",
		"token",
		"default",
		30,
		1000,
	)

	assert.NilError(t, err)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.GetProviderName(), "openai")
}