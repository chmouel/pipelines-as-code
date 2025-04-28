package mcp

import (
	"time"
)

// MCPRequest represents a request in the Model Context Protocol
type MCPRequest struct {
	// ID is the unique identifier for this request
	ID string `json:"id"`

	// Type describes the type of request
	Type string `json:"type"`

	// Action is the specific action requested
	Action string `json:"action"`

	// Params contains any parameters for the action
	Params map[string]interface{} `json:"params"`

	// Context for the request
	Context map[string]interface{} `json:"context,omitempty"`
}

// MCPResponse represents a response in the Model Context Protocol
type MCPResponse struct {
	// ID is the unique identifier for this response (matching the request ID)
	ID string `json:"id"`

	// Type is the type of response
	Type string `json:"type"`

	// Status indicates success or failure
	Status string `json:"status"`

	// Data contains the response data
	Data interface{} `json:"data,omitempty"`

	// Error provides error information if status is "error"
	Error *MCPError `json:"error,omitempty"`
}

// MCPError represents an error in the MCP response
type MCPError struct {
	// Code is the error code
	Code string `json:"code"`

	// Message is a human-readable error message
	Message string `json:"message"`

	// Details contains additional error details
	Details interface{} `json:"details,omitempty"`
}

// MCPStreamRequest represents a streaming request in the Model Context Protocol
type MCPStreamRequest struct {
	MCPRequest

	// StreamID is a unique identifier for the stream
	StreamID string `json:"stream_id"`
}

// MCPStreamResponse represents a streaming response in the Model Context Protocol
type MCPStreamResponse struct {
	MCPResponse

	// StreamID matches the stream request ID
	StreamID string `json:"stream_id"`

	// IsFinal indicates whether this is the final response in the stream
	IsFinal bool `json:"is_final"`
}

// RunStatus represents the status of a pipeline run or task run
type RunStatus string

const (
	StatusPending   RunStatus = "Pending"
	StatusRunning   RunStatus = "Running"
	StatusSucceeded RunStatus = "Succeeded"
	StatusFailed    RunStatus = "Failed"
	StatusCancelled RunStatus = "Cancelled"
	StatusTimedOut  RunStatus = "TimedOut"
	StatusUnknown   RunStatus = "Unknown"
)

// PipelineRunSummary represents a summary of a PipelineRun for the MCP API
type PipelineRunSummary struct {
	UID       string     `json:"uid"`
	Name      string     `json:"name"`
	Status    RunStatus  `json:"status"`
	StartTime *time.Time `json:"startTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	SHA       string     `json:"sha,omitempty"`
	Branch    string     `json:"branch,omitempty"`
	URL       string     `json:"url,omitempty"`
}

// Step represents a step in a TaskRun
type Step struct {
	Name      string     `json:"name"`
	Status    RunStatus  `json:"status"`
	StartTime *time.Time `json:"startTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	Container string     `json:"container"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	ErrorMsg  string     `json:"errorMessage,omitempty"`
}

// TaskRunSummary represents a summary of a TaskRun for the MCP API
type TaskRunSummary struct {
	UID       string     `json:"uid"`
	Name      string     `json:"name"`
	Status    RunStatus  `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	StartTime *time.Time `json:"startTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	PodName   string     `json:"podName,omitempty"`
	Steps     []Step     `json:"steps"`
}

// ErrorResponse represents an error response for the REST API
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// Constants for MCP request/response types
const (
	TypeRequest  = "request"
	TypeResponse = "response"

	StatusSuccess = "success"
	StatusError   = "error"

	// Action types
	ActionListRepositoryRuns = "listRepositoryRuns"
	ActionListRunTasks       = "listRunTasks"
	ActionGetTaskLogs        = "getTaskLogs"

	// Error codes
	ErrorCodeNotFound          = "not_found"
	ErrorCodeInvalidParameters = "invalid_parameters"
	ErrorCodeServerError       = "server_error"
	ErrorCodeUnauthorized      = "unauthorized"
)
