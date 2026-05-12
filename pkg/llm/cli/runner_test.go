package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestClientRun(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		input       *Input
		wantContent string
		wantErr     bool
		errContains string
	}{
		{
			name: "successful analysis",
			script: `#!/bin/bash
cat <<'EOF'
{"content": "Pipeline failed due to test error", "metadata": {"tokens_used": 42, "provider": "test"}}
EOF`,
			input:       &Input{Prompt: "analyze this", MaxTokens: 100},
			wantContent: "Pipeline failed due to test error",
		},
		{
			name: "content only response",
			script: `#!/bin/bash
echo '{"content": "simple response"}'`,
			input:       &Input{Prompt: "test"},
			wantContent: "simple response",
		},
		{
			name: "retryable error exit code 1",
			script: `#!/bin/bash
echo "rate limited" >&2
exit 1`,
			input:       &Input{Prompt: "test"},
			wantErr:     true,
			errContains: "rate limited",
		},
		{
			name: "non-retryable error exit code 2",
			script: `#!/bin/bash
echo "invalid api key" >&2
exit 2`,
			input:       &Input{Prompt: "test"},
			wantErr:     true,
			errContains: "invalid api key",
		},
		{
			name: "empty content",
			script: `#!/bin/bash
echo '{"content": ""}'`,
			input:       &Input{Prompt: "test"},
			wantErr:     true,
			errContains: "empty content",
		},
		{
			name: "malformed JSON output",
			script: `#!/bin/bash
echo 'not json'`,
			input:       &Input{Prompt: "test"},
			wantErr:     true,
			errContains: "failed to parse CLI output",
		},
		{
			name: "reads stdin correctly",
			script: `#!/bin/bash
# Read stdin and echo back the prompt from it
INPUT=$(cat)
PROMPT=$(echo "$INPUT" | python3 -c "import sys, json; print(json.load(sys.stdin)['prompt'])")
echo "{\"content\": \"received: $PROMPT\"}"`,
			input:       &Input{Prompt: "hello from PAC"},
			wantContent: "received: hello from PAC",
		},
		{
			name:    "command not found",
			script:  "", // won't be used
			input:   &Input{Prompt: "test"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var scriptPath string

			if tt.name == "command not found" {
				scriptPath = "/nonexistent/path/to/agent"
			} else {
				scriptPath = writeScript(t, tt.script)
			}

			client := NewClient(scriptPath, nil, []string{
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + os.Getenv("HOME"),
			}, 10)

			output, err := client.Run(context.Background(), tt.input)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				if tt.errContains != "" {
					assert.ErrorContains(t, err, tt.errContains)
				}
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, output.Content, tt.wantContent)
		})
	}
}

func TestClientRunTimeout(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
sleep 10
echo '{"content": "too late"}'`)

	client := NewClient(script, nil, []string{
		"PATH=" + os.Getenv("PATH"),
	}, 1) // 1 second timeout

	_, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.Assert(t, err != nil)
	assert.ErrorContains(t, err, "timed out")
}

func TestClientRunWithArgs(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
# Echo args and stdin content
echo "{\"content\": \"args=$*\"}"`)

	client := NewClient(script, []string{"--format", "json"}, []string{
		"PATH=" + os.Getenv("PATH"),
	}, 10)

	output, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.NilError(t, err)
	assert.Equal(t, output.Content, "args=--format json")
}

func TestClientRunWithEnv(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
echo "{\"content\": \"key=$MY_API_KEY\"}"`)

	client := NewClient(script, nil, []string{
		"PATH=" + os.Getenv("PATH"),
		"MY_API_KEY=secret123",
	}, 10)

	output, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.NilError(t, err)
	assert.Equal(t, output.Content, "key=secret123")
}

func TestClientRunMetadata(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
echo '{"content": "result", "metadata": {"tokens_used": 99, "model": "gpt-4", "provider": "openai"}}'`)

	client := NewClient(script, nil, []string{
		"PATH=" + os.Getenv("PATH"),
	}, 10)

	output, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.NilError(t, err)
	assert.Equal(t, output.Content, "result")
	assert.Assert(t, output.Metadata != nil)
	assert.Equal(t, output.Metadata.TokensUsed, 99)
	assert.Equal(t, output.Metadata.Model, "gpt-4")
	assert.Equal(t, output.Metadata.Provider, "openai")
}

func TestClientRunRetryableError(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
exit 1`)

	client := NewClient(script, nil, []string{
		"PATH=" + os.Getenv("PATH"),
	}, 10)

	_, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.Assert(t, err != nil)

	var analysisErr *AnalysisError
	assert.Assert(t, isAnalysisError(err, &analysisErr))
	assert.Assert(t, analysisErr.Retryable)
}

func TestClientRunNonRetryableError(t *testing.T) {
	script := writeScript(t, `#!/bin/bash
exit 2`)

	client := NewClient(script, nil, []string{
		"PATH=" + os.Getenv("PATH"),
	}, 10)

	_, err := client.Run(context.Background(), &Input{Prompt: "test"})
	assert.Assert(t, err != nil)

	var analysisErr *AnalysisError
	assert.Assert(t, isAnalysisError(err, &analysisErr))
	assert.Assert(t, !analysisErr.Retryable)
}

func TestClientInputJSON(t *testing.T) {
	// Verify that the input JSON format is correct
	input := &Input{
		Prompt:    "test prompt",
		Model:     "gpt-4o",
		MaxTokens: 2000,
		Context: map[string]interface{}{
			"pipeline": map[string]interface{}{
				"name":   "my-pipeline",
				"status": "Failed",
			},
		},
	}

	data, err := json.Marshal(input)
	assert.NilError(t, err)

	var parsed map[string]interface{}
	err = json.Unmarshal(data, &parsed)
	assert.NilError(t, err)

	assert.Equal(t, parsed["prompt"], "test prompt")
	assert.Equal(t, parsed["model"], "gpt-4o")
	assert.Equal(t, parsed["max_tokens"], float64(2000))
	assert.Assert(t, parsed["context"] != nil)
}

func TestLimitedWriter(t *testing.T) {
	tests := []struct {
		name      string
		limit     int64
		writes    []string
		wantBytes string
	}{
		{
			name:      "under limit",
			limit:     100,
			writes:    []string{"hello"},
			wantBytes: "hello",
		},
		{
			name:      "at limit",
			limit:     5,
			writes:    []string{"hello"},
			wantBytes: "hello",
		},
		{
			name:      "over limit truncates",
			limit:     3,
			writes:    []string{"follo"},
			wantBytes: "fol",
		},
		{
			name:      "multiple writes over limit",
			limit:     5,
			writes:    []string{"fol", "lo world"},
			wantBytes: "follo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf []byte
			w := &captureWriter{buf: &buf}
			lw := &limitedWriter{w: w, remaining: tt.limit}

			for _, s := range tt.writes {
				_, err := lw.Write([]byte(s))
				assert.NilError(t, err)
			}

			assert.Equal(t, string(buf), tt.wantBytes)
		})
	}
}

// captureWriter captures writes for testing limitedWriter.
type captureWriter struct {
	buf *[]byte
}

func (w *captureWriter) Write(p []byte) (int, error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}

func writeScript(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.sh")
	err := os.WriteFile(path, []byte(content), 0o755)
	assert.NilError(t, err)
	return path
}

func isAnalysisError(err error, target **AnalysisError) bool {
	return errors.As(err, target)
}

func TestMain(m *testing.M) {
	// Ensure bash is available for tests
	if _, err := exec.LookPath("bash"); err != nil {
		os.Exit(0) // skip tests if bash is not available
	}
	os.Exit(m.Run())
}
