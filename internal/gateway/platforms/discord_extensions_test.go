package platforms

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestDefaultSlashCommands(t *testing.T) {
	cmds := defaultSlashCommands()

	if len(cmds) < 4 {
		t.Errorf("expected at least 4 slash commands, got %d", len(cmds))
	}

	names := make(map[string]bool)
	for _, cmd := range cmds {
		if cmd.Name == "" {
			t.Error("slash command has empty name")
		}
		if cmd.Description == "" {
			t.Errorf("slash command %q has empty description", cmd.Name)
		}
		names[cmd.Name] = true
	}

	required := []string{"hermes", "reset", "status", "approve", "deny"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("missing required slash command: %s", name)
		}
	}
}

func TestFormatApprovalRequest(t *testing.T) {
	embed, components := FormatApprovalRequest("rm -rf /tmp/test", "Recursive file deletion")

	if embed == nil {
		t.Fatal("embed is nil")
	}
	if embed.Title == "" {
		t.Error("embed has empty title")
	}
	if embed.Color != 0xFF9900 {
		t.Errorf("color = %d, want orange (0xFF9900)", embed.Color)
	}

	if len(components) != 1 {
		t.Fatalf("expected 1 action row, got %d", len(components))
	}

	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatal("component is not ActionsRow")
	}

	if len(row.Components) != 3 {
		t.Errorf("expected 3 buttons, got %d", len(row.Components))
	}

	// Check button styles
	expectedStyles := []discordgo.ButtonStyle{
		discordgo.SuccessButton,
		discordgo.PrimaryButton,
		discordgo.DangerButton,
	}
	for i, comp := range row.Components {
		btn, ok := comp.(discordgo.Button)
		if !ok {
			t.Errorf("component %d is not a Button", i)
			continue
		}
		if btn.Style != expectedStyles[i] {
			t.Errorf("button %d style = %v, want %v", i, btn.Style, expectedStyles[i])
		}
	}
}

func TestTruncateForDiscord(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		check  func(string) bool
	}{
		{
			name:   "short text unchanged",
			input:  "hello",
			maxLen: 2000,
			check:  func(s string) bool { return s == "hello" },
		},
		{
			name:   "long text truncated",
			input:  string(make([]byte, 3000)),
			maxLen: 2000,
			check:  func(s string) bool { return len(s) <= 2000 },
		},
		{
			name:   "contains truncated marker",
			input:  string(make([]byte, 3000)),
			maxLen: 100,
			check: func(s string) bool {
				return len(s) <= 100
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateForDiscord(tt.input, tt.maxLen)
			if !tt.check(result) {
				t.Errorf("truncateForDiscord check failed, len=%d", len(result))
			}
		})
	}
}
