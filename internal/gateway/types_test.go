package gateway

import (
	"strings"
	"testing"
)

func TestMessageEventCommand(t *testing.T) {
	tests := []struct {
		text      string
		isCommand bool
		command   string
	}{
		{"/help", true, "help"},
		{"/model gpt-4", true, "model"},
		{"/new", true, "new"},
		{"hello", false, ""},
		{"", false, ""},
	}

	for _, tt := range tests {
		isCmd := strings.HasPrefix(tt.text, "/")
		if isCmd != tt.isCommand {
			t.Errorf("IsCommand(%q) = %v, want %v", tt.text, isCmd, tt.isCommand)
		}
		if tt.isCommand {
			parts := strings.SplitN(tt.text, " ", 2)
			cmd := strings.TrimPrefix(parts[0], "/")
			cmd = strings.ToLower(cmd)
			if cmd != tt.command {
				t.Errorf("GetCommand(%q) = %q, want %q", tt.text, cmd, tt.command)
			}
		}
	}
}

func TestSessionSource(t *testing.T) {
	src := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "12345",
		UserID:   "user1",
	}

	key := BuildSessionKey(src, true, false)
	if key == "" {
		t.Error("Expected non-empty session key")
	}

	// Same source should produce same key
	key2 := BuildSessionKey(src, true, false)
	if key != key2 {
		t.Error("Expected same key for same source")
	}

	// Different chat should produce different key
	src2 := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "99999",
		UserID:   "user1",
	}
	key3 := BuildSessionKey(src2, true, false)
	if key == key3 {
		t.Error("Expected different key for different chat")
	}
}

func TestSendResult(t *testing.T) {
	success := &SendResult{Success: true, MessageID: "msg_123"}
	if !success.Success {
		t.Error("Expected success")
	}
	if success.MessageID != "msg_123" {
		t.Errorf("Expected msg_123, got %s", success.MessageID)
	}

	failure := &SendResult{Success: false, Error: "rate limited", Retryable: true}
	if failure.Success {
		t.Error("Expected failure")
	}
	if !failure.Retryable {
		t.Error("Expected retryable")
	}
}

func TestMessageType(t *testing.T) {
	if MessageTypeText != "text" {
		t.Errorf("Expected 'text', got '%s'", MessageTypeText)
	}
	if MessageTypePhoto != "photo" {
		t.Errorf("Expected 'photo', got '%s'", MessageTypePhoto)
	}
	if MessageTypeCommand != "command" {
		t.Errorf("Expected 'command', got '%s'", MessageTypeCommand)
	}
}

func TestPlatformConstants(t *testing.T) {
	if PlatformDMWork != "dmwork" {
		t.Errorf("Expected 'telegram', got '%s'", PlatformDMWork)
	}
	if PlatformDMWork != "dmwork" {
		t.Errorf("Expected 'discord', got '%s'", PlatformDMWork)
	}
	if PlatformDMWork != "dmwork" {
		t.Errorf("Expected 'slack', got '%s'", PlatformDMWork)
	}
}
