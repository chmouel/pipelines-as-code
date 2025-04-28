package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"go.uber.org/zap"
)

const (
	DefaultPort          = "8090"
	DefaultReadTimeout   = 5 * time.Second
	DefaultWriteTimeout  = 40 * time.Second
	DefaultIdleTimeout   = 120 * time.Second
	DefaultListenAddress = ""
)

// Server represents the MCP API server
type Server struct {
	router     *mux.Router
	server     *http.Server
	run        *params.Run
	logger     *zap.SugaredLogger
	port       string
	listenAddr string
	upgrader   websocket.Upgrader
}

// NewServer creates a new MCP API server
func NewServer(run *params.Run, logger *zap.SugaredLogger) *Server {
	router := mux.NewRouter()

	server := &Server{
		router: router,
		run:    run,
		logger: logger,
		port:   DefaultPort,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all connections for now
			},
		},
	}

	// Register routes
	server.registerRoutes()

	return server
}

// Start starts the MCP API server
func (s *Server) Start(ctx context.Context) error {
	// Configure the HTTP server
	s.server = &http.Server{
		Addr:         fmt.Sprintf("%s:%s", s.listenAddr, s.port),
		Handler:      s.router,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}

	// Start the server
	s.logger.Infof("Starting MCP API server on %s", s.server.Addr)
	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for context cancellation to shut down gracefully
	<-ctx.Done()

	// Create a deadline for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.logger.Info("Shutting down MCP API server")
	return s.server.Shutdown(shutdownCtx)
}

// SetPort sets the server port
func (s *Server) SetPort(port string) {
	s.port = port
}

// SetListenAddress sets the server listen address
func (s *Server) SetListenAddress(addr string) {
	s.listenAddr = addr
}

// registerRoutes registers all API routes
func (s *Server) registerRoutes() {
	// Health check endpoint
	s.router.HandleFunc("/health", s.healthHandler).Methods(http.MethodGet)

	// MCP API endpoint
	s.router.HandleFunc("/api/v1/mcp", s.mcpHandler).Methods(http.MethodPost)

	// MCP WebSocket endpoint for streaming
	s.router.HandleFunc("/api/v1/mcp/stream", s.mcpStreamHandler)
}

// mcpHandler processes MCP requests
func (s *Server) mcpHandler(w http.ResponseWriter, r *http.Request) {
	var req MCPRequest

	// Parse the request body
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendErrorResponse(w, "", ErrorCodeInvalidParameters, "Invalid request format", err.Error())
		return
	}

	// Set default ID if not provided
	if req.ID == "" {
		req.ID = uuid.New().String()
	}

	// Validate request type
	if req.Type != TypeRequest {
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, "Invalid request type", nil)
		return
	}

	// Process the action
	switch req.Action {
	case ActionListRepositoryRuns:
		s.handleListRepositoryRuns(w, req)
	case ActionListRunTasks:
		s.handleListRunTasks(w, req)
	case ActionGetTaskLogs:
		s.handleGetTaskLogs(w, req)
	default:
		s.sendErrorResponse(w, req.ID, ErrorCodeInvalidParameters, fmt.Sprintf("Unsupported action: %s", req.Action), nil)
	}
}

// mcpStreamHandler handles WebSocket connections for streaming MCP responses
func (s *Server) mcpStreamHandler(w http.ResponseWriter, r *http.Request) {
	// Upgrade the HTTP connection to a WebSocket connection
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Errorf("Failed to upgrade connection to WebSocket: %v", err)
		return
	}
	defer conn.Close()

	for {
		// Read message from WebSocket
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				s.logger.Errorf("WebSocket error: %v", err)
			}
			break
		}

		// Parse the request
		var streamReq MCPStreamRequest
		if err := json.Unmarshal(message, &streamReq); err != nil {
			s.sendStreamErrorResponse(conn, "", "", ErrorCodeInvalidParameters, "Invalid request format", err.Error())
			continue
		}

		// Set default IDs if not provided
		if streamReq.ID == "" {
			streamReq.ID = uuid.New().String()
		}
		if streamReq.StreamID == "" {
			streamReq.StreamID = uuid.New().String()
		}

		// Validate request type
		if streamReq.Type != TypeRequest {
			s.sendStreamErrorResponse(conn, streamReq.ID, streamReq.StreamID, ErrorCodeInvalidParameters, "Invalid request type", nil)
			continue
		}

		// Process the action
		switch streamReq.Action {
		case ActionGetTaskLogs:
			go s.handleStreamTaskLogs(conn, streamReq)
		default:
			s.sendStreamErrorResponse(conn, streamReq.ID, streamReq.StreamID, ErrorCodeInvalidParameters, fmt.Sprintf("Unsupported action: %s", streamReq.Action), nil)
		}
	}
}

// sendErrorResponse sends an error response for the MCP API
func (s *Server) sendErrorResponse(w http.ResponseWriter, requestID string, code string, message string, details interface{}) {
	errResp := MCPResponse{
		ID:     requestID,
		Type:   TypeResponse,
		Status: StatusError,
		Error: &MCPError{
			Code:    code,
			Message: message,
			Details: details,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK) // Always return 200 for MCP

	if err := json.NewEncoder(w).Encode(errResp); err != nil {
		s.logger.Errorf("Error encoding error response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// sendSuccessResponse sends a success response for the MCP API
func (s *Server) sendSuccessResponse(w http.ResponseWriter, requestID string, data interface{}) {
	resp := MCPResponse{
		ID:     requestID,
		Type:   TypeResponse,
		Status: StatusSuccess,
		Data:   data,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Errorf("Error encoding success response: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// sendStreamErrorResponse sends an error response over a WebSocket
func (s *Server) sendStreamErrorResponse(conn *websocket.Conn, requestID string, streamID string, code string, message string, details interface{}) {
	errResp := MCPStreamResponse{
		MCPResponse: MCPResponse{
			ID:     requestID,
			Type:   TypeResponse,
			Status: StatusError,
			Error: &MCPError{
				Code:    code,
				Message: message,
				Details: details,
			},
		},
		StreamID: streamID,
		IsFinal:  true,
	}

	if err := conn.WriteJSON(errResp); err != nil {
		s.logger.Errorf("Error sending WebSocket error response: %v", err)
	}
}
