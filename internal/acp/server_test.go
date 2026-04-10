package acp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockAgent implements AgentHandler for testing.
type mockAgent struct {
	response string
	err      error
}

func (m *mockAgent) Chat(message string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func (m *mockAgent) Model() string     { return "test-model" }
func (m *mockAgent) SessionID() string { return "test-session" }

func newTestServer() *ACPServer {
	s := NewACPServer(0)
	s.SetAgent(&mockAgent{response: "hello from agent"})
	return s
}

// --- Health ---

func TestHandleHealth(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/v1/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %q", body["status"])
	}
}

// --- Status ---

func TestHandleStatus(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("GET", "/v1/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body StatusResponse
	json.NewDecoder(w.Body).Decode(&body)
	if body.Status != "running" {
		t.Errorf("expected running, got %q", body.Status)
	}
	if body.Model != "test-model" {
		t.Errorf("expected test-model, got %q", body.Model)
	}
}

// --- Chat ---

func TestHandleChat(t *testing.T) {
	s := newTestServer()

	body := `{"message": "hi"}`
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChat(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp ChatResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Response != "hello from agent" {
		t.Errorf("expected 'hello from agent', got %q", resp.Response)
	}
}

func TestHandleChatEmptyMessage(t *testing.T) {
	s := newTestServer()

	body := `{"message": ""}`
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChat(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleChatNoAgent(t *testing.T) {
	s := NewACPServer(0) // no agent set

	body := `{"message": "hi"}`
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleChat(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// --- Tool (direct dispatch) ---

func TestHandleTool_EmptyName(t *testing.T) {
	s := newTestServer()

	body := `{"tool": ""}`
	req := httptest.NewRequest("POST", "/v1/tool", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleTool(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandleTool_NotFound(t *testing.T) {
	s := newTestServer()

	body := `{"tool": "nonexistent_tool_xyz"}`
	req := httptest.NewRequest("POST", "/v1/tool", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleTool(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Sessions ---

func TestSessionCRUD(t *testing.T) {
	s := newTestServer()

	// Create
	req := httptest.NewRequest("POST", "/v1/sessions", strings.NewReader(`{"model":"gpt-4o"}`))
	w := httptest.NewRecorder()
	s.handleSessionCreate(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d", w.Code)
	}

	var sess Session
	json.NewDecoder(w.Body).Decode(&sess)
	if sess.ID == "" {
		t.Fatal("create: expected non-empty session ID")
	}
	if sess.Model != "gpt-4o" {
		t.Errorf("create: expected model gpt-4o, got %q", sess.Model)
	}

	// Get
	req = httptest.NewRequest("GET", "/v1/sessions/"+sess.ID, nil)
	w = httptest.NewRecorder()
	s.handleSessionGet(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get: expected 200, got %d", w.Code)
	}

	// List
	req = httptest.NewRequest("GET", "/v1/sessions", nil)
	w = httptest.NewRecorder()
	s.handleSessionList(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}

	var listResp map[string]any
	json.NewDecoder(w.Body).Decode(&listResp)
	count := int(listResp["count"].(float64))
	if count != 1 {
		t.Errorf("list: expected 1 session, got %d", count)
	}

	// Delete
	req = httptest.NewRequest("DELETE", "/v1/sessions/"+sess.ID, nil)
	w = httptest.NewRecorder()
	s.handleSessionDelete(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d", w.Code)
	}

	// Verify deleted
	req = httptest.NewRequest("GET", "/v1/sessions/"+sess.ID, nil)
	w = httptest.NewRecorder()
	s.handleSessionGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("get after delete: expected 404, got %d", w.Code)
	}
}

func TestSessionDeleteNotFound(t *testing.T) {
	s := newTestServer()

	req := httptest.NewRequest("DELETE", "/v1/sessions/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleSessionDelete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Auth ---

func TestAuthMiddleware_NoToken(t *testing.T) {
	// When HERMES_ACP_TOKEN is not set, all requests pass.
	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 in dev mode, got %d", w.Code)
	}
}

func TestAuthMiddleware_WithToken(t *testing.T) {
	t.Setenv("HERMES_ACP_TOKEN", "secret123")

	handler := withAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No auth header.
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without header, got %d", w.Code)
	}

	// Wrong token.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with wrong token, got %d", w.Code)
	}

	// Correct token.
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w = httptest.NewRecorder()
	handler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with correct token, got %d", w.Code)
	}
}

// --- Events ---

func TestEventBrokerPubSub(t *testing.T) {
	broker := NewEventBroker()

	ch, unsub := broker.Subscribe("sess-1")
	defer unsub()

	broker.Publish("sess-1", "test_event", map[string]string{"key": "value"})

	select {
	case evt := <-ch:
		if evt.Type != "test_event" {
			t.Errorf("expected test_event, got %q", evt.Type)
		}
		if evt.SessionID != "sess-1" {
			t.Errorf("expected sess-1, got %q", evt.SessionID)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBrokerUnsubscribe(t *testing.T) {
	broker := NewEventBroker()

	_, unsub := broker.Subscribe("sess-1")
	unsub()

	// Publishing after unsubscribe should not panic.
	broker.Publish("sess-1", "test", nil)
}

// --- Session Store ---

func TestSessionStoreAppendMessage(t *testing.T) {
	store := NewSessionStore()
	sess := store.Create("model")

	ok := store.AppendMessage(sess.ID, "user", "hello")
	if !ok {
		t.Fatal("expected AppendMessage to return true")
	}

	got := store.Get(sess.ID)
	if len(got.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got.Messages))
	}
	if got.Messages[0].Content != "hello" {
		t.Errorf("expected 'hello', got %q", got.Messages[0].Content)
	}
}

func TestSessionStoreAppendMessage_NotFound(t *testing.T) {
	store := NewSessionStore()

	ok := store.AppendMessage("nonexistent", "user", "hello")
	if ok {
		t.Fatal("expected AppendMessage to return false for nonexistent session")
	}
}

// --- Helpers ---

func TestExtractPathID(t *testing.T) {
	tests := []struct {
		path, prefix, expected string
	}{
		{"/v1/sessions/abc-123", "/v1/sessions/", "abc-123"},
		{"/v1/sessions/abc-123/", "/v1/sessions/", "abc-123"},
		{"/v1/sessions/", "/v1/sessions/", ""},
		{"/other/path", "/v1/sessions/", ""},
	}
	for _, tt := range tests {
		got := extractPathID(tt.path, tt.prefix)
		if got != tt.expected {
			t.Errorf("extractPathID(%q, %q) = %q, want %q", tt.path, tt.prefix, got, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("short", 10) != "short" {
		t.Error("should not truncate short strings")
	}
	if truncate("long string here", 4) != "long..." {
		t.Error("should truncate long strings")
	}
}
