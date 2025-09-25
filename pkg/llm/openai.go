package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient implements the Client interface for OpenAI.
type OpenAIClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewOpenAIClient creates a new OpenAI client.
func NewOpenAIClient(apiKey string) (*OpenAIClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	return &OpenAIClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Analyze sends a request to OpenAI and returns the analysis result.
func (c *OpenAIClient) Analyze(ctx context.Context, prompt string, context map[string]any) (string, error) {
	// Build full prompt with context
	fullPrompt := c.buildPrompt(prompt, context)

	// Create request
	request := openAIRequest{
		Model:     "gpt-4",
		MaxTokens: 1000,
		Messages: []openAIMessage{
			{
				Role:    "user",
				Content: fullPrompt,
			},
		},
	}

	// Marshal request
	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	// Send request with retry
	var resp *http.Response
	for attempt := 1; attempt <= 3; attempt++ {
		resp, err = c.httpClient.Do(httpReq)
		if err == nil {
			break
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	if err != nil {
		return "", fmt.Errorf("HTTP request failed after retries: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var response openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Handle errors
	if resp.StatusCode != http.StatusOK {
		errorMsg := fmt.Sprintf("OpenAI API error (status %d)", resp.StatusCode)
		if response.Error != nil {
			errorMsg = fmt.Sprintf("OpenAI API error: %s", response.Error.Message)
		}
		return "", fmt.Errorf(errorMsg)
	}

	// Extract content
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no response from OpenAI")
	}

	return response.Choices[0].Message.Content, nil
}

// buildPrompt combines the base prompt with context information.
func (c *OpenAIClient) buildPrompt(prompt string, context map[string]any) string {
	var builder strings.Builder

	builder.WriteString(prompt)
	builder.WriteString("\n\nContext Information:\n")

	for key, value := range context {
		builder.WriteString(fmt.Sprintf("=== %s ===\n", strings.ToUpper(key)))

		switch v := value.(type) {
		case string:
			builder.WriteString(v)
		case map[string]any, []any:
			jsonData, _ := json.MarshalIndent(v, "", "  ")
			builder.Write(jsonData)
		default:
			builder.WriteString(fmt.Sprintf("%v", v))
		}

		builder.WriteString("\n\n")
	}

	return builder.String()
}

// OpenAI API types
type openAIRequest struct {
	Model     string          `json:"model"`
	Messages  []openAIMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

type openAIError struct {
	Message string `json:"message"`
}
