// Package acp implements the Agent Communication Protocol server for
// editor integration (VS Code, Zed, JetBrains, etc.).
package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/tools"
)

// AgentHandler is the interface that the ACP server uses to interact with
// the underlying AI agent. This avoids a direct dependency on the agent package.
type AgentHandler interface {
	Chat(message string) (string, error)
	Model() string
	SessionID() string
}

// ACPServer implements the Agent Communication Protocol HTTP server.
type ACPServer struct {
	agent    AgentHandler
	port     int
	server   *http.Server
	sessions *SessionStore
	events   *EventBroker
	startAt  time.Time
	mu       sync.Mutex
}

// ChatRequest is the request body for POST /v1/chat.
type ChatRequest struct {
	Message   string         `json:"message"`
	SessionID string         `json:"session_id,omitempty"`
	Model     string         `json:"model,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

// ChatResponse is the response body for POST /v1/chat.
type ChatResponse struct {
	Response  string `json:"response"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
}

// StatusResponse is the response body for GET /v1/status.
type StatusResponse struct {
	Status    string `json:"status"`
	Model     string `json:"model"`
	SessionID string `json:"session_id"`
	Version   string `json:"version"`
	Uptime    string `json:"uptime"`
}

// ToolRequest is the request body for POST /v1/tool.
type ToolRequest struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	SessionID string         `json:"session_id,omitempty"`
}

// ToolResponse is the response body for POST /v1/tool.
type ToolResponse struct {
	Result string `json:"result"`
	Error  string `json:"error,omitempty"`
}

// SessionCreateRequest is the request body for POST /v1/sessions.
type SessionCreateRequest struct {
	Model string `json:"model,omitempty"`
}

// NewACPServer creates a new ACP server on the given port.
func NewACPServer(port int) *ACPServer {
	if port == 0 {
		port = 3000
	}
	return &ACPServer{
		port:     port,
		sessions: NewSessionStore(),
		events:   NewEventBroker(),
		startAt:  time.Now(),
	}
}

// SetAgent attaches an agent handler to the server.
func (s *ACPServer) SetAgent(agent AgentHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agent = agent
}

// Sessions returns the session store.
func (s *ACPServer) Sessions() *SessionStore { return s.sessions }

// Events returns the event broker.
func (s *ACPServer) Events() *EventBroker { return s.events }

// Start begins serving HTTP requests. Blocks until Stop is called.
func (s *ACPServer) Start() error {
	mux := http.NewServeMux()

	// Public.
	mux.HandleFunc("GET /v1/health", s.handleHealth)

	// Authenticated.
	mux.HandleFunc("POST /v1/chat", withAuth(s.handleChat))
	mux.HandleFunc("GET /v1/status", withAuth(s.handleStatus))
	mux.HandleFunc("POST /v1/tool", withAuth(s.handleTool))
	mux.HandleFunc("GET /v1/tools", withAuth(s.handleToolsList))

	// Sessions.
	mux.HandleFunc("POST /v1/sessions", withAuth(s.handleSessionCreate))
	mux.HandleFunc("GET /v1/sessions", withAuth(s.handleSessionList))
	mux.HandleFunc("GET /v1/sessions/", withAuth(s.handleSessionGet))
	mux.HandleFunc("DELETE /v1/sessions/", withAuth(s.handleSessionDelete))

	// SSE events.
	mux.HandleFunc("GET /v1/events", withAuth(s.handleEvents))

	s.server = &http.Server{
		Addr:        fmt.Sprintf("127.0.0.1:%d", s.port),
		Handler:     withCORS(mux),
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second,
	}

	slog.Info("ACP server starting", "port", s.port, "auth", authToken() != "")

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("acp server listen: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the server.
func (s *ACPServer) Stop() error {
	if s.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	slog.Info("ACP server shutting down")
	return s.server.Shutdown(ctx)
}

// Port returns the configured port.
func (s *ACPServer) Port() int { return s.port }

// --- Chat ---

func (s *ACPServer) handleChat(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	if agent == nil {
		writeError(w, http.StatusServiceUnavailable, "agent not initialized")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	if req.SessionID != "" {
		s.sessions.AppendMessage(req.SessionID, "user", req.Message)
		s.events.Publish(req.SessionID, "status", map[string]string{"message": "processing"})
	}

	response, err := agent.Chat(req.Message)
	if err != nil {
		if req.SessionID != "" {
			s.events.Publish(req.SessionID, "error", map[string]string{"message": err.Error()})
		}
		writeError(w, http.StatusInternalServerError, "chat error: "+err.Error())
		return
	}

	if req.SessionID != "" {
		s.sessions.AppendMessage(req.SessionID, "assistant", response)
		s.events.Publish(req.SessionID, "agent_response", map[string]string{"content": response})
	}

	writeJSON(w, http.StatusOK, ChatResponse{
		Response:  response,
		SessionID: agent.SessionID(),
		Model:     agent.Model(),
	})
}

// --- Status ---

func (s *ACPServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()

	status := StatusResponse{
		Status:  "running",
		Version: "dev",
		Uptime:  time.Since(s.startAt).Round(time.Second).String(),
	}
	if agent != nil {
		status.Model = agent.Model()
		status.SessionID = agent.SessionID()
	}
	writeJSON(w, http.StatusOK, status)
}

// --- Tool (direct dispatch via ToolRegistry) ---

func (s *ACPServer) handleTool(w http.ResponseWriter, r *http.Request) {
	var req ToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Tool == "" {
		writeError(w, http.StatusBadRequest, "tool name is required")
		return
	}

	registry := tools.Registry()
	if !registry.HasTool(req.Tool) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("tool %q not found", req.Tool))
		return
	}

	if req.SessionID != "" {
		s.events.Publish(req.SessionID, "tool_start", map[string]string{"tool": req.Tool})
	}

	ctx := &tools.ToolContext{
		SessionID: req.SessionID,
		Platform:  "acp",
	}
	result := registry.Dispatch(req.Tool, req.Arguments, ctx)

	if req.SessionID != "" {
		s.events.Publish(req.SessionID, "tool_complete", map[string]string{
			"tool": req.Tool, "result": truncate(result, 500),
		})
	}

	writeJSON(w, http.StatusOK, ToolResponse{Result: result})
}

// handleToolsList returns all registered tools and their schemas.
func (s *ACPServer) handleToolsList(w http.ResponseWriter, _ *http.Request) {
	registry := tools.Registry()
	names := registry.GetAllToolNames()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	defs := registry.GetDefinitions(nameSet, true)
	writeJSON(w, http.StatusOK, map[string]any{"tools": defs, "count": len(defs)})
}

// --- Sessions ---

func (s *ACPServer) handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	var req SessionCreateRequest
	json.NewDecoder(r.Body).Decode(&req) // allow empty body

	model := req.Model
	if model == "" {
		s.mu.Lock()
		if s.agent != nil {
			model = s.agent.Model()
		}
		s.mu.Unlock()
	}

	sess := s.sessions.Create(model)
	slog.Info("ACP session created", "session_id", sess.ID, "model", model)
	writeJSON(w, http.StatusCreated, sess)
}

func (s *ACPServer) handleSessionList(w http.ResponseWriter, _ *http.Request) {
	sessions := s.sessions.List()
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "count": len(sessions)})
}

func (s *ACPServer) handleSessionGet(w http.ResponseWriter, r *http.Request) {
	id := extractPathID(r.URL.Path, "/v1/sessions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID required")
		return
	}
	sess := s.sessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *ACPServer) handleSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := extractPathID(r.URL.Path, "/v1/sessions/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "session ID required")
		return
	}
	if !s.sessions.Delete(id) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	slog.Info("ACP session deleted", "session_id", id)
	w.WriteHeader(http.StatusNoContent)
}

// --- Health ---

func (s *ACPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractPathID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	return strings.TrimSuffix(id, "/")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
