package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"
)

const (
	// maxStdoutSize limits stdout to 1MB to prevent OOM from runaway agents.
	maxStdoutSize = 1 << 20
	// DefaultTimeoutSeconds is the default timeout for CLI agent calls.
	DefaultTimeoutSeconds = 30
)

// AnalysisError represents an error from CLI agent execution.
type AnalysisError struct {
	Provider  string
	Type      string
	Message   string
	Retryable bool
}

func (e *AnalysisError) Error() string {
	return e.Message
}

// Client invokes an external CLI binary for LLM analysis.
type Client struct {
	command        string
	args           []string
	env            []string
	timeoutSeconds int
}

// NewClient creates a new CLI-based LLM client.
func NewClient(command string, args, env []string, timeoutSeconds int) *Client {
	if timeoutSeconds == 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}
	return &Client{
		command:        command,
		args:           args,
		env:            env,
		timeoutSeconds: timeoutSeconds,
	}
}

// Run sends the input to the CLI agent via stdin and reads the output from stdout.
func (c *Client) Run(ctx context.Context, input *Input) (*Output, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, &AnalysisError{
			Provider:  "cli",
			Type:      "marshal_error",
			Message:   fmt.Sprintf("failed to marshal CLI input: %v", err),
			Retryable: false,
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(c.timeoutSeconds)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, c.command, c.args...) //nolint:gosec // command comes from trusted CRD configuration
	cmd.Env = c.env
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &stdout, remaining: maxStdoutSize}
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, c.handleError(timeoutCtx, err, stderr.String())
	}

	var output Output
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, &AnalysisError{
			Provider:  "cli",
			Type:      "parse_error",
			Message:   fmt.Sprintf("failed to parse CLI output: %v (stdout: %s)", err, truncate(stdout.String(), 200)),
			Retryable: false,
		}
	}

	if output.Content == "" {
		return nil, &AnalysisError{
			Provider:  "cli",
			Type:      "empty_response",
			Message:   "CLI agent returned empty content",
			Retryable: false,
		}
	}

	return &output, nil
}

func (c *Client) handleError(ctx context.Context, err error, stderrOutput string) *AnalysisError {
	if ctx.Err() != nil {
		return &AnalysisError{
			Provider:  "cli",
			Type:      "timeout",
			Message:   fmt.Sprintf("CLI agent timed out after %ds", c.timeoutSeconds),
			Retryable: true,
		}
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitCode()
		retryable := exitCode != ExitNonRetryable

		msg := stderrOutput
		if msg == "" {
			msg = fmt.Sprintf("CLI agent exited with code %d", exitCode)
		}

		return &AnalysisError{
			Provider:  "cli",
			Type:      fmt.Sprintf("exit_code_%d", exitCode),
			Message:   msg,
			Retryable: retryable,
		}
	}

	return &AnalysisError{
		Provider:  "cli",
		Type:      "exec_error",
		Message:   fmt.Sprintf("failed to execute CLI agent: %v", err),
		Retryable: false,
	}
}

// limitedWriter wraps a writer and stops writing after a limit.
type limitedWriter struct {
	w         io.Writer
	remaining int64
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return len(p), nil // silently discard
	}
	if int64(len(p)) > lw.remaining {
		p = p[:lw.remaining]
	}
	n, err := lw.w.Write(p)
	lw.remaining -= int64(n)
	return n, err
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
