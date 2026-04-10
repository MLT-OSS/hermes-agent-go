package acp

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session represents an ACP session with conversation history.
type Session struct {
	ID        string    `json:"id"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []Message `json:"messages"`
}

// Message represents a single message in a session.
type Message struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

// SessionStore manages ACP sessions in memory.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStore creates a new in-memory session store.
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

// Create creates a new session and returns it.
func (s *SessionStore) Create(model string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess := &Session{
		ID:        uuid.New().String(),
		Model:     model,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages:  make([]Message, 0),
	}
	s.sessions[sess.ID] = sess
	return sess
}

// Get returns a session by ID, or nil if not found.
func (s *SessionStore) Get(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

// List returns all sessions (without full message history).
func (s *SessionStore) List() []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		summary := *sess
		summary.Messages = nil
		result = append(result, summary)
	}
	return result
}

// Delete removes a session by ID. Returns true if found.
func (s *SessionStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[id]; !ok {
		return false
	}
	delete(s.sessions, id)
	return true
}

// AppendMessage adds a message to a session's history.
func (s *SessionStore) AppendMessage(sessionID, role, content string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}

	sess.Messages = append(sess.Messages, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
	})
	sess.UpdatedAt = time.Now()
	return true
}
