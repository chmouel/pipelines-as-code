package gemini

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"gotest.tools/v3/assert"
)

type mockTransport struct {
	response *http.Response
	err      error
}

func (m *mockTransport) RoundTrip(_ *http.Request) (*http.Response, error) {
	return m.response, m.err
}

func TestClient_Analyze_JSONOutput(t *testing.T) {
	// Mock server response
	responseBody := `{"candidates": [{"content": {"parts": [{"text": "{\n  \"analysis_summary\": \"The build failed due to a missing dependency.\",\n  \"issues\": [\n    {\n      \"file_path\": \"pkg/cmd/tknpac/generate/command.go\",\n      \"line_number\": 102,\n      \"severity\": \"error\",\n      \"error_message\": \"undefined: afero.NewMemMapFs\",\n      \"suggestion\": \"Ensure the 'afero' package is properly imported and the function NewMemMapFs is available.\"\n    }\n  ]\n}"}]}}]}`
	mockResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(responseBody)),
		Header:     make(http.Header),
	}
	mockResp.Header.Set("Content-Type", "application/json")

	// Create a client with the mock transport
	client := &Client{
		config: &Config{
			APIKey: "test-key",
		},
		httpClient: &http.Client{
			Transport: &mockTransport{response: mockResp},
		},
	}

	// Create an analysis request
	request := &ltypes.AnalysisRequest{
		Prompt:     "Analyze these logs",
		JSONOutput: true,
	}

	// Call the Analyze method
	response, err := client.Analyze(context.Background(), request)
	assert.NilError(t, err)
	assert.Assert(t, response != nil)

	// Verify the JSON output
	assert.Assert(t, response.JSONParsed != nil)
	summary, ok := response.JSONParsed["analysis_summary"].(string)
	assert.Assert(t, ok)
	assert.Equal(t, "The build failed due to a missing dependency.", summary)

	issues, ok := response.JSONParsed["issues"].([]interface{})
	assert.Assert(t, ok)
	assert.Equal(t, 1, len(issues))

	issue, ok := issues[0].(map[string]interface{})
	assert.Assert(t, ok)
	assert.Equal(t, "pkg/cmd/tknpac/generate/command.go", issue["file_path"])
	assert.Equal(t, float64(102), issue["line_number"])
}
