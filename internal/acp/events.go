package acp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Event represents a server-sent event.
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Data      any    `json:"data"`
	Timestamp int64  `json:"timestamp"`
}

// EventBroker manages SSE client connections and event broadcasting.
type EventBroker struct {
	mu      sync.RWMutex
	clients map[string]map[chan Event]struct{}
}

// NewEventBroker creates a new SSE event broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		clients: make(map[string]map[chan Event]struct{}),
	}
}

// Subscribe registers a client channel for a session's events.
func (b *EventBroker) Subscribe(sessionID string) (chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Buffer size 32: SSE clients may lag due to network latency or editor
	// rendering. A buffer prevents blocking the publisher. Overflow events
	// are dropped with a warning log (see Publish).
	ch := make(chan Event, 32)

	if b.clients[sessionID] == nil {
		b.clients[sessionID] = make(map[chan Event]struct{})
	}
	b.clients[sessionID][ch] = struct{}{}

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.clients[sessionID], ch)
		if len(b.clients[sessionID]) == 0 {
			delete(b.clients, sessionID)
		}
		close(ch)
	}

	return ch, unsubscribe
}

// Publish sends an event to all subscribers of a session.
func (b *EventBroker) Publish(sessionID, eventType string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	clients := b.clients[sessionID]
	if len(clients) == 0 {
		return
	}

	evt := Event{
		Type:      eventType,
		SessionID: sessionID,
		Data:      data,
		Timestamp: time.Now().UnixMilli(),
	}

	for ch := range clients {
		select {
		case ch <- evt:
		default:
			slog.Warn("SSE client channel full, dropping event",
				"session_id", sessionID, "type", eventType)
		}
	}
}

// handleEvents serves the SSE endpoint: GET /v1/events?session_id=X
func (s *ACPServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "session_id query parameter required")
		return
	}

	if s.sessions.Get(sessionID) == nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := s.events.Subscribe(sessionID)
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				slog.Warn("SSE marshal error", "error", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
		}
	}
}
