// Package gateway implements the multi-platform messaging gateway for Hermes Agent.
package gateway

import (
	"context"
)

// MessageType represents the type of an incoming message.
type MessageType string

const (
	MessageTypeText     MessageType = "text"
	MessageTypePhoto    MessageType = "photo"
	MessageTypeVideo    MessageType = "video"
	MessageTypeAudio    MessageType = "audio"
	MessageTypeVoice    MessageType = "voice"
	MessageTypeDocument MessageType = "document"
	MessageTypeSticker  MessageType = "sticker"
	MessageTypeCommand  MessageType = "command"
)

// Platform identifies a messaging platform.
type Platform string

const (
	PlatformLocal        Platform = "local"
	PlatformDMWork       Platform = "dmwork"
)

// MessageEvent represents an incoming message from any platform.
type MessageEvent struct {
	Text        string            `json:"text"`
	MessageType MessageType       `json:"message_type"`
	Source      SessionSource     `json:"source"`
	MediaURLs   []string          `json:"media_urls,omitempty"`
	MediaPaths  []string          `json:"media_paths,omitempty"`
	ReplyToID   string            `json:"reply_to_id,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	RawMessage  any               `json:"-"` // Platform-specific raw message
}

// SendResult represents the result of sending a message.
type SendResult struct {
	Success   bool   `json:"success"`
	MessageID string `json:"message_id,omitempty"`
	Error     string `json:"error,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
}

// SessionSource describes where a message originated from.
type SessionSource struct {
	Platform  Platform `json:"platform"`
	ChatID    string   `json:"chat_id"`
	ChatName  string   `json:"chat_name,omitempty"`
	ChatType  string   `json:"chat_type"` // "dm", "group", "channel", "thread"
	UserID    string   `json:"user_id,omitempty"`
	UserName  string   `json:"user_name,omitempty"`
	ThreadID  string   `json:"thread_id,omitempty"`
	ChatTopic string   `json:"chat_topic,omitempty"`
	UserIDAlt string   `json:"user_id_alt,omitempty"` // Signal UUID etc.
	ChatIDAlt string   `json:"chat_id_alt,omitempty"` // Signal group internal ID
}

// Description returns a human-readable description of the source.
func (s *SessionSource) Description() string {
	if s.Platform == PlatformLocal {
		return "CLI terminal"
	}
	switch s.ChatType {
	case "dm":
		name := s.UserName
		if name == "" {
			name = s.UserID
		}
		if name == "" {
			name = "user"
		}
		return "DM with " + name
	case "group":
		name := s.ChatName
		if name == "" {
			name = s.ChatID
		}
		return "group: " + name
	case "channel":
		name := s.ChatName
		if name == "" {
			name = s.ChatID
		}
		return "channel: " + name
	default:
		name := s.ChatName
		if name == "" {
			name = s.ChatID
		}
		return name
	}
}

// ToMap serializes SessionSource to a map.
func (s *SessionSource) ToMap() map[string]any {
	m := map[string]any{
		"platform":  string(s.Platform),
		"chat_id":   s.ChatID,
		"chat_name": s.ChatName,
		"chat_type": s.ChatType,
		"user_id":   s.UserID,
		"user_name": s.UserName,
		"thread_id": s.ThreadID,
	}
	if s.ChatTopic != "" {
		m["chat_topic"] = s.ChatTopic
	}
	if s.UserIDAlt != "" {
		m["user_id_alt"] = s.UserIDAlt
	}
	if s.ChatIDAlt != "" {
		m["chat_id_alt"] = s.ChatIDAlt
	}
	return m
}

// PlatformAdapter is the interface that all platform adapters must implement.
type PlatformAdapter interface {
	// Platform returns the platform identifier.
	Platform() Platform

	// Connect establishes a connection to the platform.
	Connect(ctx context.Context) error

	// Disconnect cleanly disconnects from the platform.
	Disconnect() error

	// Send sends a text message to a chat.
	Send(ctx context.Context, chatID string, text string, metadata map[string]string) (*SendResult, error)

	// SendTyping sends a typing indicator.
	SendTyping(ctx context.Context, chatID string) error

	// SendImage sends an image file.
	SendImage(ctx context.Context, chatID string, imagePath string, caption string, metadata map[string]string) (*SendResult, error)

	// SendVoice sends a voice/audio message.
	SendVoice(ctx context.Context, chatID string, audioPath string, metadata map[string]string) (*SendResult, error)

	// SendDocument sends a document/file.
	SendDocument(ctx context.Context, chatID string, filePath string, metadata map[string]string) (*SendResult, error)

	// OnMessage registers a handler for incoming messages.
	OnMessage(handler func(event *MessageEvent))

	// IsConnected returns true if the adapter is currently connected.
	IsConnected() bool
}

// PlatformConfig holds the configuration for a platform adapter.
type PlatformConfig struct {
	Enabled  bool              `yaml:"enabled"`
	Token    string            `yaml:"token"`
	Settings map[string]string `yaml:"settings,omitempty"`
}

// GatewayConfig holds the full gateway configuration.
type GatewayConfig struct {
	Platforms    map[Platform]*PlatformConfig `yaml:"platforms"`
	Settings     GatewaySettings             `yaml:"settings"`
	AllowedUsers map[string]any              `yaml:"allowed_users"`
}

// GatewaySettings holds gateway-level settings.
type GatewaySettings struct {
	GroupSessionsPerUser  bool `yaml:"group_sessions_per_user"`
	ThreadSessionsPerUser bool `yaml:"thread_sessions_per_user"`
	MaxMessageLength      int  `yaml:"max_message_length"`
}

// DefaultGatewayConfig returns a gateway config with sensible defaults.
func DefaultGatewayConfig() *GatewayConfig {
	return &GatewayConfig{
		Platforms: make(map[Platform]*PlatformConfig),
		Settings: GatewaySettings{
			GroupSessionsPerUser:  true,
			ThreadSessionsPerUser: false,
			MaxMessageLength:      4096,
		},
	}
}
