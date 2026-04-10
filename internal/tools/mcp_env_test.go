package tools

import (
	"testing"
)

func TestIsSensitiveEnvKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"OPENAI_API_KEY", true},
		{"ANTHROPIC_API_KEY", true},
		{"OPENROUTER_API_KEY", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"HERMES_ACP_TOKEN", true},
		{"DISCORD_BOT_TOKEN", true},
		{"TELEGRAM_BOT_TOKEN", true},
		{"SLACK_BOT_TOKEN", true},
		{"API_KEY", true},
		{"SECRET_KEY", true},
		{"PATH", false},
		{"HOME", false},
		{"TERM", false},
		{"LANG", false},
		{"USER", false},
		{"EDITOR", false},
		{"MCP_SERVER_PORT", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := isSensitiveEnvKey(tt.key)
			if got != tt.want {
				t.Errorf("isSensitiveEnvKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestBuildSafeEnv(t *testing.T) {
	// buildSafeEnv filters the real environment, so we just check that
	// user overrides are appended.
	env := buildSafeEnv(map[string]string{"MCP_PORT": "8080"})

	found := false
	for _, entry := range env {
		if entry == "MCP_PORT=8080" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected MCP_PORT=8080 in safe env")
	}

	// Verify sensitive keys are stripped (at least one should be absent).
	for _, entry := range env {
		if len(entry) > 7 && entry[:7] == "OPENAI_" {
			t.Error("OPENAI_ prefix should be stripped from safe env")
		}
	}
}
