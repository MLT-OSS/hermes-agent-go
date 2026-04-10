package tools

import (
	"os"
	"strings"
)

// sensitiveEnvPrefixes are environment variable prefixes that should be
// stripped before spawning MCP server subprocesses (security sandboxing).
var sensitiveEnvPrefixes = []string{
	"OPENAI_",
	"ANTHROPIC_",
	"OPENROUTER_",
	"DEEPSEEK_",
	"GOOGLE_API_",
	"NOUS_",
	"AWS_",
	"AZURE_",
	"GCP_",
	"HERMES_ACP_TOKEN",
	"SLACK_BOT_TOKEN",
	"SLACK_APP_TOKEN",
	"DISCORD_BOT_TOKEN",
	"TELEGRAM_BOT_TOKEN",
	"WHATSAPP_",
	"SIGNAL_",
	"MATRIX_",
}

// sensitiveEnvExact are exact env var names to strip.
var sensitiveEnvExact = map[string]bool{
	"API_KEY":     true,
	"SECRET_KEY":  true,
	"PRIVATE_KEY": true,
}

// buildSafeEnv returns a copy of the current environment with sensitive
// variables removed, plus any user-specified overrides from the MCP config.
// This prevents MCP servers from accessing API keys, bot tokens, or cloud
// credentials that belong to the agent.
func buildSafeEnv(userEnv map[string]string) []string {
	base := os.Environ()
	safe := make([]string, 0, len(base))

	for _, entry := range base {
		key := entry
		if idx := strings.IndexByte(entry, '='); idx >= 0 {
			key = entry[:idx]
		}

		if isSensitiveEnvKey(key) {
			continue
		}
		safe = append(safe, entry)
	}

	// Apply user overrides.
	for k, v := range userEnv {
		safe = append(safe, k+"="+v)
	}

	return safe
}

func isSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(key)

	if sensitiveEnvExact[upper] {
		return true
	}

	for _, prefix := range sensitiveEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}

	return false
}
