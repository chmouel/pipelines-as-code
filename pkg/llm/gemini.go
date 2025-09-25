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

// GeminiClient implements the Client interface for Google Gemini.
type GeminiClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewGeminiClient creates a new Gemini client.
func NewGeminiClient(apiKey string) (*GeminiClient, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("Gemini API key is required")
	}

	return &GeminiClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// Analyze sends a request to Gemini and returns the analysis result.
func (c *GeminiClient) Analyze(ctx context.Context, prompt string, context map[string]any) (string, error) {
	// Build full prompt with context
	fullPrompt := c.buildPrompt(prompt, context)

	// Create request
	request := geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{
						Text: fullPrompt,
					},
				},
			},
		},
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens: 1000,
		},
	}

	// Marshal request
	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-pro:generateContent?key=%s", c.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")

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
	var response geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	// Handle errors
	if resp.StatusCode != http.StatusOK {
		errorMsg := fmt.Sprintf("Gemini API error (status %d)", resp.StatusCode)
		if response.Error != nil {
			errorMsg = fmt.Sprintf("Gemini API error: %s", response.Error.Message)
		}
		return "", fmt.Errorf(errorMsg)
	}

	// Extract content
	if len(response.Candidates) == 0 || len(response.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no response from Gemini")
	}

	return response.Candidates[0].Content.Parts[0].Text, nil
}

// buildPrompt combines the base prompt with context information.
func (c *GeminiClient) buildPrompt(prompt string, context map[string]any) string {
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

// Gemini API types
type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content geminiContent `json:"content"`
}

type geminiError struct {
	Message string `json:"message"`
}
