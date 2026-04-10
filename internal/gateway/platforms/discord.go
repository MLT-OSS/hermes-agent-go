package platforms

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/bwmarrin/discordgo"
	"github.com/hermes-agent/hermes-agent-go/internal/gateway"
)

// DiscordAdapter implements the gateway.PlatformAdapter interface for Discord.
type DiscordAdapter struct {
	BasePlatformAdapter
	session *discordgo.Session
	token   string
}

// NewDiscordAdapter creates a new Discord adapter.
func NewDiscordAdapter(token string) *DiscordAdapter {
	if token == "" {
		token = os.Getenv("DISCORD_BOT_TOKEN")
	}
	return &DiscordAdapter{
		BasePlatformAdapter: NewBasePlatformAdapter(gateway.PlatformDiscord),
		token:               token,
	}
}

// Connect establishes a connection to Discord.
func (d *DiscordAdapter) Connect(ctx context.Context) error {
	if d.token == "" {
		return fmt.Errorf("DISCORD_BOT_TOKEN not set")
	}

	dg, err := discordgo.New("Bot " + d.token)
	if err != nil {
		return fmt.Errorf("create Discord session: %w", err)
	}

	dg.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentsDirectMessages |
		discordgo.IntentsMessageContent |
		discordgo.IntentsGuilds

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		// Ignore messages from the bot itself.
		if m.Author.ID == s.State.User.ID {
			return
		}
		d.handleMessage(s, m)
	})

	// Handle slash command interactions.
	dg.AddHandler(d.handleInteraction)

	if err := dg.Open(); err != nil {
		return fmt.Errorf("open Discord connection: %w", err)
	}

	d.session = dg
	d.connected = true
	slog.Info("Discord bot connected", "username", dg.State.User.Username)

	// Wait for context cancellation.
	go func() {
		<-ctx.Done()
		d.Disconnect()
	}()

	return nil
}

// Disconnect cleanly disconnects from Discord.
func (d *DiscordAdapter) Disconnect() error {
	if d.session != nil {
		d.session.Close()
	}
	d.connected = false
	return nil
}

// Send sends a text message to a Discord channel.
func (d *DiscordAdapter) Send(ctx context.Context, chatID string, text string, metadata map[string]string) (*gateway.SendResult, error) {
	if d.session == nil {
		return nil, fmt.Errorf("not connected")
	}

	// Split long messages (Discord limit: 2000 chars).
	parts := SplitMessage(text, 2000)
	var lastMsgID string

	for _, part := range parts {
		msg, err := d.session.ChannelMessageSend(chatID, part)
		if err != nil {
			return &gateway.SendResult{
				Success:   false,
				Error:     err.Error(),
				Retryable: true,
			}, nil
		}
		lastMsgID = msg.ID
	}

	return &gateway.SendResult{
		Success:   true,
		MessageID: lastMsgID,
	}, nil
}

// SendTyping sends a typing indicator to a Discord channel.
func (d *DiscordAdapter) SendTyping(ctx context.Context, chatID string) error {
	if d.session == nil {
		return fmt.Errorf("not connected")
	}
	return d.session.ChannelTyping(chatID)
}

// SendImage sends an image to a Discord channel.
func (d *DiscordAdapter) SendImage(ctx context.Context, chatID string, imagePath string, caption string, metadata map[string]string) (*gateway.SendResult, error) {
	if d.session == nil {
		return nil, fmt.Errorf("not connected")
	}

	f, err := os.Open(imagePath)
	if err != nil {
		return &gateway.SendResult{Success: false, Error: err.Error()}, nil
	}
	defer f.Close()

	ms := &discordgo.MessageSend{
		Content: caption,
		Files: []*discordgo.File{
			{
				Name:   imagePath,
				Reader: f,
			},
		},
	}

	msg, err := d.session.ChannelMessageSendComplex(chatID, ms)
	if err != nil {
		return &gateway.SendResult{
			Success:   false,
			Error:     err.Error(),
			Retryable: true,
		}, nil
	}

	return &gateway.SendResult{
		Success:   true,
		MessageID: msg.ID,
	}, nil
}

// SendVoice sends a voice file to a Discord channel.
func (d *DiscordAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, metadata map[string]string) (*gateway.SendResult, error) {
	// Discord doesn't have a native voice message API, send as a file.
	return d.SendDocument(ctx, chatID, audioPath, metadata)
}

// SendDocument sends a document to a Discord channel.
func (d *DiscordAdapter) SendDocument(ctx context.Context, chatID string, filePath string, metadata map[string]string) (*gateway.SendResult, error) {
	if d.session == nil {
		return nil, fmt.Errorf("not connected")
	}

	f, err := os.Open(filePath)
	if err != nil {
		return &gateway.SendResult{Success: false, Error: err.Error()}, nil
	}
	defer f.Close()

	ms := &discordgo.MessageSend{
		Files: []*discordgo.File{
			{
				Name:   filePath,
				Reader: f,
			},
		},
	}

	msg, err := d.session.ChannelMessageSendComplex(chatID, ms)
	if err != nil {
		return &gateway.SendResult{
			Success:   false,
			Error:     err.Error(),
			Retryable: true,
		}, nil
	}

	return &gateway.SendResult{
		Success:   true,
		MessageID: msg.ID,
	}, nil
}

// --- Internal ---

func (d *DiscordAdapter) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	chatType := "group"
	channel, err := s.Channel(m.ChannelID)
	if err == nil {
		switch channel.Type {
		case discordgo.ChannelTypeDM, discordgo.ChannelTypeGroupDM:
			chatType = "dm"
		case discordgo.ChannelTypeGuildText:
			chatType = "channel"
		}
	}

	source := gateway.SessionSource{
		Platform: gateway.PlatformDiscord,
		ChatID:   m.ChannelID,
		ChatType: chatType,
		UserID:   m.Author.ID,
		UserName: m.Author.Username,
	}

	if channel != nil {
		source.ChatName = channel.Name
		source.ChatTopic = channel.Topic
	}

	// Check for thread.
	if m.GuildID != "" && channel != nil && channel.IsThread() {
		source.ThreadID = m.ChannelID
		source.ChatID = channel.ParentID
	}

	msgType := gateway.MessageTypeText
	text := m.Content

	// Detect commands (messages starting with /).
	if len(text) > 0 && text[0] == '/' {
		msgType = gateway.MessageTypeCommand
	}

	event := &gateway.MessageEvent{
		Text:        text,
		MessageType: msgType,
		Source:      source,
		RawMessage:  m,
	}

	// Handle attachments.
	for _, att := range m.Attachments {
		event.MediaURLs = append(event.MediaURLs, att.URL)
	}

	d.EmitMessage(event)
}
