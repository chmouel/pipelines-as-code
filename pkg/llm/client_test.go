package llm

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

type mockTransport struct {
	response string
}

func (m *mockTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(m.response)),
	}, nil
}

func TestNewClient(t *testing.T) {
	config := &Config{
		Provider:    "openai",
		APIKey:      "test-key",
		APIEndpoint: "https://api.openai.com/v1/chat/completions",
		Model:       "gpt-4",
		MaxTokens:   1000,
		Temperature: 0.7,
		Enabled:     true,
	}

	logger := zap.NewNop().Sugar()
	client := NewClient(config, logger)

	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Provider, "openai")
	assert.Equal(t, client.config.APIKey, "test-key")
	assert.Equal(t, client.config.Enabled, true)
}

func TestAnalyzeComment_Disabled(t *testing.T) {
	config := &Config{
		Provider: "openai",
		Enabled:  false,
	}

	logger := zap.NewNop().Sugar()
	client := NewClient(config, logger)

	req := &AnalysisRequest{
		UserComment:  "restart the tests",
		Repository:   "test-repo",
		Organization: "test-org",
		EventType:    "pull_request",
	}

	resp, err := client.AnalyzeComment(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, resp.Action, "unknown")
	assert.Equal(t, resp.Explanation, "LLM analysis is disabled")
}

func TestAnalyzeComment_UnsupportedProvider(t *testing.T) {
	config := &Config{
		Provider: "unsupported",
		Enabled:  true,
	}

	logger := zap.NewNop().Sugar()
	client := NewClient(config, logger)

	req := &AnalysisRequest{
		UserComment:  "restart the tests",
		Repository:   "test-repo",
		Organization: "test-org",
		EventType:    "pull_request",
	}

	resp, err := client.AnalyzeComment(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, resp.Action, "unknown")
	assert.Equal(t, resp.Explanation, "unsupported LLM provider: unsupported")
}

func TestBuildPrompt(t *testing.T) {
	config := &Config{
		Provider: "openai",
		Enabled:  true,
	}

	logger := zap.NewNop().Sugar()
	client := NewClient(config, logger)

	req := &AnalysisRequest{
		UserComment:  "restart the go test pipeline",
		Repository:   "test-repo",
		Organization: "test-org",
		EventType:    "pull_request",
		AvailablePipelines: []PipelineInfo{
			{
				Name:        "go-test",
				Description: "Runs Go unit tests",
				Tasks:       []string{"go-test", "go-lint"},
			},
			{
				Name:        "python-test",
				Description: "Runs Python unit tests",
				Tasks:       []string{"python-test", "python-lint"},
			},
		},
	}

	prompt := client.buildPrompt(req)
	assert.Assert(t, prompt != "")
	assert.Assert(t, strings.Contains(prompt, "restart the go test pipeline"))
	assert.Assert(t, strings.Contains(prompt, "go-test: Runs Go unit tests (tasks: go-test, go-lint)"))
	assert.Assert(t, strings.Contains(prompt, "python-test: Runs Python unit tests (tasks: python-test, python-lint)"))
}

func TestAnalyzeComment_Query(t *testing.T) {
	config := &Config{
		Provider:    "openai",
		APIKey:      "test-key",
		APIEndpoint: "https://api.openai.com/v1/chat/completions",
		Model:       "gpt-4",
		MaxTokens:   1000,
		Temperature: 0.7,
		Enabled:     true,
	}

	logger := zap.NewNop().Sugar()
	client := NewClient(config, logger)

	// Mock the HTTP client to return a query response
	client.httpClient = &http.Client{
		Transport: &mockTransport{
			response: `{
				"choices": [{
					"message": {
						"content": "{\"action\": \"query\", \"target_pipelines\": [], \"confidence\": 0.9, \"explanation\": \"User is asking about pipeline information\", \"query_response\": \"Based on the available pipelines, I can see the following production-related pipelines: [list relevant pipelines with descriptions]\"}"
					}
				}]
			}`,
		},
	}

	req := &AnalysisRequest{
		UserComment:        "what is the push to production pipeline",
		Organization:       "test-org",
		Repository:         "test-repo",
		EventType:          "pull_request",
		AvailablePipelines: []PipelineInfo{},
	}

	result, err := client.AnalyzeComment(context.Background(), req)
	assert.NilError(t, err)
	assert.Equal(t, result.Action, "query")
	assert.Equal(t, result.Confidence, 0.9)
	assert.Equal(t, result.Explanation, "User is asking about pipeline information")
	assert.Equal(t, result.QueryResponse, "Based on the available pipelines, I can see the following production-related pipelines: [list relevant pipelines with descriptions]")
}

func TestNewClientFromSettings(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	// Test with LLM disabled
	settings := &testSettings{
		LLMEnabled:     false,
		LLMProvider:    "openai",
		LLMModel:       "gpt-3.5-turbo",
		LLMMaxTokens:   1000,
		LLMTemperature: 0.1,
		LLMTimeout:     30,
	}

	client := NewClientFromSettings(settings, logger)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Enabled, false)

	// Test with LLM enabled but no API key
	settings.LLMEnabled = true
	client = NewClientFromSettings(settings, logger)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Enabled, false) // Should be disabled due to missing API key

	// Test with LLM enabled and API key set
	settings.LLMEnabled = true
	os.Setenv("OPENAI_API_KEY", "test-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	client = NewClientFromSettings(settings, logger)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Enabled, true)
	assert.Equal(t, client.config.Provider, "openai")
	assert.Equal(t, client.config.Model, "gpt-3.5-turbo")
	assert.Equal(t, client.config.MaxTokens, 1000)
	assert.Equal(t, client.config.Temperature, 0.1)
	assert.Equal(t, client.config.Timeout, 30*time.Second)
	assert.Equal(t, client.config.APIKey, "test-key")
	assert.Equal(t, client.config.APIEndpoint, "https://api.openai.com/v1/chat/completions")

	// Test with Gemini provider
	settings.LLMProvider = "gemini"
	settings.LLMModel = "gemini-pro"
	os.Setenv("GEMINI_API_KEY", "test-gemini-key")
	defer os.Unsetenv("GEMINI_API_KEY")

	client = NewClientFromSettings(settings, logger)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Enabled, true)
	assert.Equal(t, client.config.Provider, "gemini")
	assert.Equal(t, client.config.Model, "gemini-pro")
	assert.Equal(t, client.config.APIKey, "test-gemini-key")
	assert.Equal(t, client.config.APIEndpoint, "https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent")

	// Test with unknown provider
	settings.LLMProvider = "unknown"
	client = NewClientFromSettings(settings, logger)
	assert.Assert(t, client != nil)
	assert.Equal(t, client.config.Enabled, false) // Should be disabled for unknown provider
}

// testSettings is a test struct that mimics the settings structure.
type testSettings struct {
	LLMEnabled     bool
	LLMProvider    string
	LLMModel       string
	LLMMaxTokens   int
	LLMTemperature float64
	LLMTimeout     int
}
