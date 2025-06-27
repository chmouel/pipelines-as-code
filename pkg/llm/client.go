package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// PRAnalysisRequest represents a request to analyze a pull request for issues and security vulnerabilities.
type PRAnalysisRequest struct {
	UserQuestion      string        `json:"user_question"`
	Repository        string        `json:"repository"`
	Organization      string        `json:"organization"`
	PullRequestNumber int           `json:"pull_request_number"`
	BaseBranch        string        `json:"base_branch"`
	HeadBranch        string        `json:"head_branch"`
	ChangedFiles      []ChangedFile `json:"changed_files"`
	Commits           []CommitInfo  `json:"commits"`
	Labels            []string      `json:"labels"`
	Title             string        `json:"title"`
	Description       string        `json:"description"`
}

// ChangedFile represents information about a changed file in a pull request.
type ChangedFile struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // "added", "modified", "deleted", "renamed"
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// CommitInfo represents information about a commit in a pull request.
type CommitInfo struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

// PRAnalysisResponse represents the LLM's analysis of a pull request.
type PRAnalysisResponse struct {
	Analysis         string   `json:"analysis"`          // Detailed analysis of the PR
	Issues           []string `json:"issues"`            // List of identified issues
	SecurityConcerns []string `json:"security_concerns"` // List of security concerns
	Recommendations  []string `json:"recommendations"`   // List of recommendations
	Confidence       float64  `json:"confidence"`        // confidence score 0-1
	Error            string   `json:"error,omitempty"`   // error if any
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
		Enabled:     true,
		Provider:    "gemini",
		Model:       "gemini-2.5-flash",
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
		// Ensure the model name is properly formatted for Gemini API
		modelName := config.Model
		if !strings.Contains(modelName, "models/") {
			modelName = "models/" + modelName
		}
		config.APIEndpoint = fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:generateContent", modelName)
	default:
		logger.Warnf("Unknown LLM provider: %s, disabling LLM", config.Provider)
		config.Enabled = false
	}

	apiKeyEnv := fmt.Sprintf("%s_API_KEY", strings.ToUpper(config.Provider))
	config.APIKey = os.Getenv(apiKeyEnv)
	if config.Enabled && config.APIKey == "" {
		logger.Warnf("No API key found for %s (environment variable %s not set), disabling LLM", config.Provider, apiKeyEnv)
		config.Enabled = false
	}

	logger.Debugf("LLM configuration: enabled=%v, provider=%s, hasApiKey=%v",
		config.Enabled, config.Provider, config.APIKey != "")

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

// AnalyzePullRequest analyzes a pull request for issues and security vulnerabilities.
func (c *Client) AnalyzePullRequest(ctx context.Context, req *PRAnalysisRequest) (*PRAnalysisResponse, error) {
	if !c.config.Enabled {
		return &PRAnalysisResponse{
			Analysis:   "LLM analysis is disabled",
			Confidence: 0.0,
		}, nil
	}

	switch c.config.Provider {
	case "openai":
		return c.analyzePRWithOpenAI(ctx, req)
	case "anthropic":
		return c.analyzePRWithAnthropic(ctx, req)
	case "gemini":
		return c.analyzePRWithGemini(ctx, req)
	default:
		return &PRAnalysisResponse{
			Analysis:   fmt.Sprintf("unsupported LLM provider: %s", c.config.Provider),
			Confidence: 0.0,
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
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("Gemini API key is not configured")
	}

	prompt := c.buildPrompt(req)

	geminiReq := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{
						"text": "You are an AI assistant that helps users interact with CI/CD pipelines. Analyze the user's comment and determine what pipeline action they want to perform. Respond with valid JSON only.\n\n" + prompt,
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

	// Debug logging
	c.logger.Debugf("Gemini API endpoint: %s", c.config.APIEndpoint)
	c.logger.Debugf("Gemini request payload: %s", string(jsonData))

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
		// Read the response body to get more details about the error
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.logger.Errorf("Gemini API error response: %s", string(bodyBytes))
		return nil, fmt.Errorf("gemini API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Gemini response: %w", err)
	}

	// Debug logging for response structure
	c.logger.Debugf("Gemini response structure: candidates=%d", len(geminiResp.Candidates))
	if len(geminiResp.Candidates) > 0 {
		c.logger.Debugf("First candidate parts: %d", len(geminiResp.Candidates[0].Content.Parts))
	}

	// Check for API errors
	if geminiResp.Error != nil {
		return nil, fmt.Errorf("gemini API error: %d - %s", geminiResp.Error.Code, geminiResp.Error.Message)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		c.logger.Errorf("Gemini API returned empty response. Candidates: %d, Parts: %d",
			len(geminiResp.Candidates),
			func() int {
				if len(geminiResp.Candidates) > 0 {
					return len(geminiResp.Candidates[0].Content.Parts)
				}
				return 0
			}())
		return nil, fmt.Errorf("no content in Gemini response")
	}

	responseText := geminiResp.Candidates[0].Content.Parts[0].Text

	// Extract JSON from markdown code blocks if present
	if strings.Contains(responseText, "```json") {
		start := strings.Index(responseText, "```json")
		if start != -1 {
			start = strings.Index(responseText[start:], "\n")
			if start != -1 {
				start += strings.Index(responseText, "```json") + 1
				end := strings.LastIndex(responseText, "```")
				if end > start {
					responseText = strings.TrimSpace(responseText[start:end])
				}
			}
		}
	} else if strings.Contains(responseText, "```") {
		// Handle generic code blocks
		start := strings.Index(responseText, "```")
		if start != -1 {
			start = strings.Index(responseText[start:], "\n")
			if start != -1 {
				start += strings.Index(responseText, "```") + 1
				end := strings.LastIndex(responseText, "```")
				if end > start {
					responseText = strings.TrimSpace(responseText[start:end])
				}
			}
		}
	}

	c.logger.Debugf("Extracted JSON response: %s", responseText)

	var analysisResp AnalysisResponse
	if err := json.Unmarshal([]byte(responseText), &analysisResp); err != nil {
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

// analyzePRWithOpenAI analyzes the pull request using OpenAI's API.
func (c *Client) analyzePRWithOpenAI(ctx context.Context, req *PRAnalysisRequest) (*PRAnalysisResponse, error) {
	prompt := c.buildPRPrompt(req)

	openAIReq := map[string]any{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an AI assistant that analyzes pull requests for issues, security vulnerabilities, and provides recommendations. Analyze the provided pull request information and respond with valid JSON only.",
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

	var analysisResp PRAnalysisResponse
	if err := json.Unmarshal([]byte(openAIResp.Choices[0].Message.Content), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// analyzePRWithAnthropic analyzes the pull request using Anthropic's API.
func (c *Client) analyzePRWithAnthropic(ctx context.Context, req *PRAnalysisRequest) (*PRAnalysisResponse, error) {
	prompt := c.buildPRPrompt(req)

	anthropicReq := map[string]any{
		"model":       c.config.Model,
		"max_tokens":  c.config.MaxTokens,
		"temperature": c.config.Temperature,
		"system":      "You are an AI assistant that analyzes pull requests for issues, security vulnerabilities, and provides recommendations. Analyze the provided pull request information and respond with valid JSON only.",
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

	var analysisResp PRAnalysisResponse
	if err := json.Unmarshal([]byte(anthropicResp.Content[0].Text), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// analyzePRWithGemini analyzes the pull request using Google's Gemini API.
func (c *Client) analyzePRWithGemini(ctx context.Context, req *PRAnalysisRequest) (*PRAnalysisResponse, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("Gemini API key is not configured")
	}

	prompt := c.buildPRPrompt(req)

	geminiReq := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]string{
					{
						"text": "You are an AI assistant that analyzes pull requests for issues, security vulnerabilities, and provides recommendations. Analyze the provided pull request information and respond with valid JSON only.\n\n" + prompt,
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

	// Debug logging
	c.logger.Infof("Gemini API endpoint: %s", c.config.APIEndpoint)
	c.logger.Infof("Gemini request payload: %s", string(jsonData))

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
		// Read the response body to get more details about the error
		bodyBytes, _ := io.ReadAll(resp.Body)
		c.logger.Errorf("Gemini API error response: %s", string(bodyBytes))
		return nil, fmt.Errorf("gemini API returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, fmt.Errorf("failed to decode Gemini response: %w", err)
	}

	// Debug logging for response structure
	c.logger.Debugf("Gemini response structure: candidates=%d", len(geminiResp.Candidates))
	if len(geminiResp.Candidates) > 0 {
		c.logger.Debugf("First candidate parts: %d", len(geminiResp.Candidates[0].Content.Parts))
	}

	// Check for API errors
	if geminiResp.Error != nil {
		return nil, fmt.Errorf("gemini API error: %d - %s", geminiResp.Error.Code, geminiResp.Error.Message)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		c.logger.Errorf("Gemini API returned empty response. Candidates: %d, Parts: %d",
			len(geminiResp.Candidates),
			func() int {
				if len(geminiResp.Candidates) > 0 {
					return len(geminiResp.Candidates[0].Content.Parts)
				}
				return 0
			}())
		return nil, fmt.Errorf("no content in Gemini response")
	}

	responseText := geminiResp.Candidates[0].Content.Parts[0].Text

	// Extract JSON from markdown code blocks if present
	if strings.Contains(responseText, "```json") {
		start := strings.Index(responseText, "```json")
		if start != -1 {
			start = strings.Index(responseText[start:], "\n")
			if start != -1 {
				start += strings.Index(responseText, "```json") + 1
				end := strings.LastIndex(responseText, "```")
				if end > start {
					responseText = strings.TrimSpace(responseText[start:end])
				}
			}
		}
	} else if strings.Contains(responseText, "```") {
		// Handle generic code blocks
		start := strings.Index(responseText, "```")
		if start != -1 {
			start = strings.Index(responseText[start:], "\n")
			if start != -1 {
				start += strings.Index(responseText, "```") + 1
				end := strings.LastIndex(responseText, "```")
				if end > start {
					responseText = strings.TrimSpace(responseText[start:end])
				}
			}
		}
	}

	c.logger.Debugf("Extracted JSON response: %s", responseText)

	var analysisResp PRAnalysisResponse
	if err := json.Unmarshal([]byte(responseText), &analysisResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal analysis response: %w", err)
	}

	return &analysisResp, nil
}

// buildPRPrompt creates the prompt for PR analysis based on the request.
func (c *Client) buildPRPrompt(req *PRAnalysisRequest) string {
	// Format changed files
	changedFilesInfo := make([]string, 0, len(req.ChangedFiles))
	for _, file := range req.ChangedFiles {
		info := fmt.Sprintf("- %s (%s, +%d -%d)", file.Path, file.Status, file.Additions, file.Deletions)
		changedFilesInfo = append(changedFilesInfo, info)
	}

	// Format commits
	commitsInfo := make([]string, 0, len(req.Commits))
	for _, commit := range req.Commits {
		info := fmt.Sprintf("- %s: %s (by %s on %s)", commit.SHA[:8], commit.Message, commit.Author, commit.Date)
		commitsInfo = append(commitsInfo, info)
	}

	prompt := fmt.Sprintf(`Analyze this pull request for issues, security vulnerabilities, and provide recommendations.

Repository: %s/%s
Pull Request: #%d
Title: %s
Base Branch: %s
Head Branch: %s
Labels: %s

Description:
%s

Changed Files:
%s

Commits:
%s

User Question: %s

Please respond with a JSON object containing:
- "analysis": detailed analysis of the pull request
- "issues": array of identified issues (code quality, logic errors, etc.)
- "security_concerns": array of security vulnerabilities or concerns
- "recommendations": array of recommendations for improvement
- "confidence": confidence score between 0 and 1

Focus on:
1. Code quality issues
2. Security vulnerabilities (SQL injection, XSS, authentication issues, etc.)
3. Performance concerns
4. Best practices violations
5. Potential bugs or edge cases
6. Documentation needs

Response:`, req.Organization, req.Repository, req.PullRequestNumber, req.Title, req.BaseBranch, req.HeadBranch, strings.Join(req.Labels, ", "), req.Description, strings.Join(changedFilesInfo, "\n"), strings.Join(commitsInfo, "\n"), req.UserQuestion)

	return prompt
}
