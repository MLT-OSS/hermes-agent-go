package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStore(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")
	os.MkdirAll(filepath.Join(tmpDir, "sessions"), 0755)

	store := NewSessionStore(nil)
	if store == nil {
		t.Fatal("Expected non-nil session store")
	}

	src := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "chat_123",
		UserID:   "user_456",
	}

	// Get or create session
	entry := store.GetOrCreateSession(src, false)
	if entry == nil {
		t.Fatal("Expected non-nil session entry")
	}
	if entry.SessionID == "" {
		t.Error("Expected non-empty session ID")
	}

	// Get same session again
	entry2 := store.GetOrCreateSession(src, false)
	if entry2.SessionID != entry.SessionID {
		t.Error("Expected same session ID for same source")
	}

	// List sessions
	sessions := store.ListSessions(0)
	if len(sessions) < 1 {
		t.Errorf("Expected at least 1 session, got %d", len(sessions))
	}

	// Reset
	key := BuildSessionKey(src, true, false)
	store.ResetSession(key)
	entry3 := store.GetOrCreateSession(src, false)
	if entry3.SessionID == entry.SessionID {
		t.Error("Expected new session ID after reset")
	}
}

func TestBuildSessionKeyVariations(t *testing.T) {
	dm := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "123",
		UserID:   "user1",
		ChatType: "dm",
	}
	key1 := BuildSessionKey(dm, true, false)
	if key1 == "" {
		t.Error("Expected non-empty key for DM")
	}

	group := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "guild_789",
		UserID:   "user1",
		ChatType: "group",
	}
	key2 := BuildSessionKey(group, true, false)
	if key2 == "" {
		t.Error("Expected non-empty key for group")
	}

	thread := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "channel_1",
		UserID:   "user1",
		ThreadID: "thread_ts",
	}
	key3 := BuildSessionKey(thread, true, true)
	if key3 == "" {
		t.Error("Expected non-empty key for thread")
	}

	// Different sources should give different keys
	if key1 == key2 {
		t.Error("Expected different keys for different platforms/chats")
	}
}

func TestHashID(t *testing.T) {
	h1 := HashID("test-input")
	h2 := HashID("test-input")
	if h1 != h2 {
		t.Error("Same input should produce same hash")
	}

	h3 := HashID("different-input")
	if h1 == h3 {
		t.Error("Different inputs should produce different hashes")
	}

	if h1 == "" {
		t.Error("Hash should not be empty")
	}
}
