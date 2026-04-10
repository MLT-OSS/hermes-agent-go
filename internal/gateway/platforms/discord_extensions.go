package platforms

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/hermes-agent/hermes-agent-go/internal/gateway"
)

// --- Slash commands ---

// DiscordSlashCommand defines a slash command to register.
type DiscordSlashCommand struct {
	Name        string
	Description string
	Options     []*discordgo.ApplicationCommandOption
}

// defaultSlashCommands returns the slash commands the bot registers.
func defaultSlashCommands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "hermes",
			Description: "Send a message to Hermes Agent",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "message",
					Description: "Your message to the agent",
					Required:    true,
				},
			},
		},
		{
			Name:        "reset",
			Description: "Reset the current session",
		},
		{
			Name:        "status",
			Description: "Show current session status",
		},
		{
			Name:        "approve",
			Description: "Approve a pending dangerous command",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "scope",
					Description: "Approval scope: once, session, or always",
					Required:    false,
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "This command only", Value: "once"},
						{Name: "This session", Value: "session"},
						{Name: "Always (permanent)", Value: "always"},
					},
				},
			},
		},
		{
			Name:        "deny",
			Description: "Deny a pending dangerous command",
		},
	}
}

// RegisterSlashCommands registers slash commands with Discord.
// Returns the registered command IDs for cleanup.
func (d *DiscordAdapter) RegisterSlashCommands() ([]*discordgo.ApplicationCommand, error) {
	if d.session == nil {
		return nil, fmt.Errorf("not connected")
	}

	appID := d.session.State.User.ID
	cmds := defaultSlashCommands()

	registered := make([]*discordgo.ApplicationCommand, 0, len(cmds))
	for _, cmd := range cmds {
		created, err := d.session.ApplicationCommandCreate(appID, "", cmd)
		if err != nil {
			slog.Warn("failed to register slash command",
				"command", cmd.Name, "error", err)
			continue
		}
		registered = append(registered, created)
		slog.Info("registered slash command", "command", cmd.Name)
	}

	return registered, nil
}

// CleanupSlashCommands removes registered slash commands.
func (d *DiscordAdapter) CleanupSlashCommands(cmds []*discordgo.ApplicationCommand) {
	if d.session == nil {
		return
	}
	appID := d.session.State.User.ID
	for _, cmd := range cmds {
		if err := d.session.ApplicationCommandDelete(appID, "", cmd.ID); err != nil {
			slog.Warn("failed to delete slash command",
				"command", cmd.Name, "error", err)
		}
	}
}

// handleInteraction processes slash command interactions.
func (d *DiscordAdapter) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()

	// Acknowledge the interaction immediately (3-second Discord limit).
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	switch data.Name {
	case "hermes":
		msg := ""
		for _, opt := range data.Options {
			if opt.Name == "message" {
				msg = opt.StringValue()
			}
		}
		if msg == "" {
			d.editInteractionResponse(s, i, "Please provide a message.")
			return
		}
		// Convert to message event for the gateway runner.
		d.emitInteractionAsMessage(i, msg)

	case "reset":
		d.emitInteractionAsMessage(i, "/reset")

	case "status":
		d.emitInteractionAsMessage(i, "/status")

	case "approve":
		scope := "once"
		for _, opt := range data.Options {
			if opt.Name == "scope" {
				scope = opt.StringValue()
			}
		}
		d.emitInteractionAsMessage(i, "/approve "+scope)

	case "deny":
		d.emitInteractionAsMessage(i, "/deny")

	default:
		d.editInteractionResponse(s, i, fmt.Sprintf("Unknown command: /%s", data.Name))
	}
}

func (d *DiscordAdapter) editInteractionResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
	})
}

func (d *DiscordAdapter) emitInteractionAsMessage(i *discordgo.InteractionCreate, text string) {
	userID := ""
	userName := ""
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
		userName = i.Member.User.Username
	} else if i.User != nil {
		userID = i.User.ID
		userName = i.User.Username
	}

	chatType := "channel"
	if i.GuildID == "" {
		chatType = "dm"
	}

	source := gateway.SessionSource{
		Platform: gateway.PlatformDiscord,
		ChatID:   i.ChannelID,
		ChatType: chatType,
		UserID:   userID,
		UserName: userName,
	}

	event := &gateway.MessageEvent{
		Text:        text,
		MessageType: gateway.MessageTypeCommand,
		Source:      source,
		RawMessage:  i,
	}

	d.EmitMessage(event)
}

// --- Thread management ---

// CreateThread creates a new thread from a message.
func (d *DiscordAdapter) CreateThread(channelID, messageID, name string) (*discordgo.Channel, error) {
	if d.session == nil {
		return nil, fmt.Errorf("not connected")
	}

	thread, err := d.session.MessageThreadStartComplex(channelID, messageID, &discordgo.ThreadStart{
		Name:                name,
		AutoArchiveDuration: 1440, // 24 hours
		Type:                discordgo.ChannelTypeGuildPublicThread,
	})
	if err != nil {
		return nil, fmt.Errorf("create thread: %w", err)
	}
	return thread, nil
}

// --- Rich message support ---

// SendEmbed sends an embedded rich message.
func (d *DiscordAdapter) SendEmbed(channelID string, embed *discordgo.MessageEmbed) (string, error) {
	if d.session == nil {
		return "", fmt.Errorf("not connected")
	}

	msg, err := d.session.ChannelMessageSendEmbed(channelID, embed)
	if err != nil {
		return "", fmt.Errorf("send embed: %w", err)
	}
	return msg.ID, nil
}

// SendComponents sends a message with interactive components (buttons, selects).
func (d *DiscordAdapter) SendComponents(channelID, content string, components []discordgo.MessageComponent) (string, error) {
	if d.session == nil {
		return "", fmt.Errorf("not connected")
	}

	msg, err := d.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:    content,
		Components: components,
	})
	if err != nil {
		return "", fmt.Errorf("send components: %w", err)
	}
	return msg.ID, nil
}

// EditMessage edits an existing message.
func (d *DiscordAdapter) EditMessage(channelID, messageID, content string) error {
	if d.session == nil {
		return fmt.Errorf("not connected")
	}

	_, err := d.session.ChannelMessageEdit(channelID, messageID, content)
	if err != nil {
		return fmt.Errorf("edit message: %w", err)
	}
	return nil
}

// DeleteMessage deletes a message.
func (d *DiscordAdapter) DeleteMessage(channelID, messageID string) error {
	if d.session == nil {
		return fmt.Errorf("not connected")
	}

	if err := d.session.ChannelMessageDelete(channelID, messageID); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}
	return nil
}

// AddReaction adds a reaction to a message.
func (d *DiscordAdapter) AddReaction(channelID, messageID, emoji string) error {
	if d.session == nil {
		return fmt.Errorf("not connected")
	}

	return d.session.MessageReactionAdd(channelID, messageID, emoji)
}

// --- Approval request formatting ---

// FormatApprovalRequest creates a rich embed + buttons for a dangerous command approval.
func FormatApprovalRequest(command, reason string) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	embed := &discordgo.MessageEmbed{
		Title:       "⚠️ Command Approval Required",
		Description: fmt.Sprintf("A potentially dangerous command needs your approval.\n\n**Command:**\n```\n%s\n```\n**Reason:** %s", command, reason),
		Color:       0xFF9900, // orange
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Approve (once)",
					Style:    discordgo.SuccessButton,
					CustomID: "approve_once",
				},
				discordgo.Button{
					Label:    "Approve (session)",
					Style:    discordgo.PrimaryButton,
					CustomID: "approve_session",
				},
				discordgo.Button{
					Label:    "Deny",
					Style:    discordgo.DangerButton,
					CustomID: "approve_deny",
				},
			},
		},
	}

	return embed, components
}

// truncateForDiscord ensures text fits within Discord's 2000 char limit.
func truncateForDiscord(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}

	// Try to cut at a code block boundary.
	cutPoint := maxLen - 20
	if idx := strings.LastIndex(text[:cutPoint], "\n```"); idx > cutPoint/2 {
		return text[:idx] + "\n```\n\n...(truncated)"
	}

	// Cut at word boundary.
	if idx := strings.LastIndex(text[:cutPoint], " "); idx > cutPoint/2 {
		return text[:idx] + "\n\n...(truncated)"
	}

	return text[:cutPoint] + "\n\n...(truncated)"
}
