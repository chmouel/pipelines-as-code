package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Client represents an LLM client for processing natural language commands.
type Client struct {
	httpClient *http.Client
	logger     *zap.SugaredLogger
	config     *Config
}

// Config holds the configuration for the LLM client.
type Config struct {
	Provider    string // "openai", "anthropic", etc.
	APIKey      string
	APIEndpoint string
	Model       string
	MaxTokens   int
	Temperature float64
	Timeout     time.Duration
	Enabled     bool
}

// PipelineInfo represents information about a pipeline that can be used for context.
type PipelineInfo struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Annotations map[string]string `json:"annotations"`
	Labels      map[string]string `json:"labels"`
	Tasks       []string          `json:"tasks"`
}

// AnalysisRequest represents a request to analyze a user comment.
type AnalysisRequest struct {
	UserComment        string         `json:"user_comment"`
	AvailablePipelines []PipelineInfo `json:"available_pipelines"`
	Repository         string         `json:"repository"`
	Organization       string         `json:"organization"`
	EventType          string         `json:"event_type"`
}

// AnalysisResponse represents the LLM's analysis of a user comment.
type AnalysisResponse struct {
	Action          string   `json:"action"`                   // "test", "retest", "cancel", "unknown", "query"
	TargetPipelines []string `json:"target_pipelines"`         // names of pipelines to target
	Confidence      float64  `json:"confidence"`               // confidence score 0-1
	Explanation     string   `json:"explanation"`              // explanation of the decision
	QueryResponse   string   `json:"query_response,omitempty"` // response for informational queries
	Error           string   `json:"error,omitempty"`          // error if any
}

// NewClient creates a new LLM client.
func NewClient(config *Config, logger *zap.SugaredLogger) *Client {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		logger: logger,
		config: config,
	}
}

// NewClientFromSettings creates a new LLM client from settings configuration.
func NewClientFromSettings(settings any, logger *zap.SugaredLogger) *Client {
	settingsValue := reflect.ValueOf(settings).Elem()
	settingsType := settingsValue.Type()

	config := &Config{
		Enabled:     false,
		Provider:    "openai",
		Model:       "gpt-3.5-turbo",
		MaxTokens:   1000,
		Temperature: 0.1,
		Timeout:     30 * time.Second,
	}

	for i := 0; i < settingsType.NumField(); i++ {
		field := settingsType.Field(i)
		fieldValue := settingsValue.Field(i)

		switch field.Name {
		case "LLMEnabled":
			config.Enabled = fieldValue.Bool()
		case "LLMProvider":
			config.Provider = fieldValue.String()
		case "LLMModel":
			config.Model = fieldValue.String()
		case "LLMMaxTokens":
			config.MaxTokens = int(fieldValue.Int())
		case "LLMTemperature":
			config.Temperature = fieldValue.Float()
		case "LLMTimeout":
			config.Timeout = time.Duration(fieldValue.Int()) * time.Second
		}
	}

	switch config.Provider {
	case "openai":
		config.APIEndpoint = "https://api.openai.com/v1/chat/completions"
	case "anthropic":
		config.APIEndpoint = "https://api.anthropic.com/v1/messages"
	case "gemini":
		config.APIEndpoint = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", config.Model)
	default:
		logger.Warnf("Unknown LLM provider: %s, disabling LLM", config.Provider)
		config.Enabled = false
	}

	apiKeyEnv := fmt.Sprintf("%s_API_KEY", strings.ToUpper(config.Provider))
	config.APIKey = os.Getenv(apiKeyEnv)
	if config.APIKey == "" {
		logger.Warnf("No API key found for %s (environment variable %s not set), disabling LLM", config.Provider, apiKeyEnv)
		config.Enabled = false
	}

	return NewClient(config, logger)
}

// AnalyzeComment analyzes a user comment and returns the intended action.
func (c *Client) AnalyzeComment(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	if !c.config.Enabled {
		return &AnalysisResponse{
			Action:      "unknown",
			Explanation: "LLM analysis is disabled",
		}, nil
	}

	switch c.config.Provider {
	case "openai":
		return c.analyzeWithOpenAI(ctx, req)
	case "anthropic":
		return c.analyzeWithAnthropic(ctx, req)
	case "gemini":
		return c.analyzeWithGemini(ctx, req)
	default:
		return &AnalysisResponse{
			Action:      "unknown",
			Explanation: fmt.Sprintf("unsupported LLM provider: %s", c.config.Provider),
		}, nil
	}
}

// analyzeWithOpenAI analyzes the comment using OpenAI's API.
func (c *Client) analyzeWithOpenAI(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	prompt := c.buildPrompt(req)

	openAIReq := map[string]any{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an AI assistant that helps users interact with CI/CD pipelines. Analyze the user's comment and determine what pipeline action they want to perform.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	jsonData, err := json.Marshal(openAIReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenAI request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.APIEndpoint, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d", resp.StatusCode)
	}

	var openAIResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("failed to decode OpenAI response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	var analysisResp AnalysisResponse
	if err := json.Unmarshal([]byte(openAIResp.Choices[0].Message.Content), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// analyzeWithAnthropic analyzes the comment using Anthropic's API.
func (c *Client) analyzeWithAnthropic(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	prompt := c.buildPrompt(req)

	anthropicReq := map[string]any{
		"model":       c.config.Model,
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
		"system":      "You are an AI assistant that helps users interact with CI/CD pipelines. Analyze the user's comment and determine what pipeline action they want to perform. Respond with valid JSON only.",
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
	}

	jsonData, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Anthropic request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.APIEndpoint, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.config.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic API returned status %d", resp.StatusCode)
	}

	var anthropicResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to decode Anthropic response: %w", err)
	}

	if len(anthropicResp.Content) == 0 {
		return nil, fmt.Errorf("no content in Anthropic response")
	}

	var analysisResp AnalysisResponse
	if err := json.Unmarshal([]byte(anthropicResp.Content[0].Text), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// analyzeWithGemini analyzes the comment using Google's Gemini API.
func (c *Client) analyzeWithGemini(ctx context.Context, req *AnalysisRequest) (*AnalysisResponse, error) {
	prompt := c.buildPrompt(req)

	geminiReq := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{
						"text": "You are an AI assistant that helps users interact with CI/CD pipelines. Analyze the user's comment and determine what pipeline action they want to perform. Respond with valid JSON only.",
					},
				},
			},
			{
				"parts": []map[string]string{
					{
						"text": prompt,
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":     c.config.Temperature,
			"maxOutputTokens": c.config.MaxTokens,
		},
	}

	jsonData, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Gemini request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.APIEndpoint, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini API returned status %d", resp.StatusCode)
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Gemini response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("no content in Gemini response")
	}

	var analysisResp AnalysisResponse
	if err := json.Unmarshal([]byte(geminiResp.Candidates[0].Content.Parts[0].Text), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// buildPrompt creates the prompt for the LLM based on the analysis request.
func (c *Client) buildPrompt(req *AnalysisRequest) string {
	pipelineDescriptions := make([]string, 0, len(req.AvailablePipelines))
	for _, pipeline := range req.AvailablePipelines {
		desc := fmt.Sprintf("- %s", pipeline.Name)
		if pipeline.Description != "" {
			desc += fmt.Sprintf(": %s", pipeline.Description)
		}
		if len(pipeline.Tasks) > 0 {
			desc += fmt.Sprintf(" (tasks: %s)", strings.Join(pipeline.Tasks, ", "))
		}
		pipelineDescriptions = append(pipelineDescriptions, desc)
	}

	prompt := fmt.Sprintf(`Analyze this user comment and determine what they want to do with CI/CD pipelines.

Repository: %s/%s
Event Type: %s
User Comment: "%s"

Available Pipelines:
%s

Please respond with a JSON object containing:
- "action": one of "test", "retest", "cancel", "query", or "unknown"
- "target_pipelines": array of pipeline names to target (empty array for "all" or "query")
- "confidence": confidence score between 0 and 1
- "explanation": brief explanation of your decision
- "query_response": detailed response for informational queries (only for "query" action)

Examples:
- "restart the go test pipeline" -> {"action": "retest", "target_pipelines": ["go-test"], "confidence": 0.9, "explanation": "User wants to restart the go test pipeline"}
- "run all tests" -> {"action": "test", "target_pipelines": [], "confidence": 0.8, "explanation": "User wants to run all available test pipelines"}
- "cancel everything" -> {"action": "cancel", "target_pipelines": [], "confidence": 0.9, "explanation": "User wants to cancel all running pipelines"}
- "what is the push to production pipeline" -> {"action": "query", "target_pipelines": [], "confidence": 0.9, "explanation": "User is asking about pipeline information", "query_response": "Based on the available pipelines, I can see the following production-related pipelines: [list relevant pipelines with descriptions]"}
- "which pipeline handles deployment" -> {"action": "query", "target_pipelines": [], "confidence": 0.8, "explanation": "User is asking about deployment pipelines", "query_response": "Looking at the available pipelines, here are the deployment-related ones: [list with descriptions]"}

Response:`, req.Organization, req.Repository, req.EventType, req.UserComment, strings.Join(pipelineDescriptions, "\n"))

	return prompt
}
