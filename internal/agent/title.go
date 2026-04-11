package agent

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// GenerateSessionTitle derives a short title from the first user message.
// It uses a fast heuristic so that title generation never adds latency.
func GenerateSessionTitle(messages []llm.Message) string {
	return generateSessionTitleHeuristic(messages)
}

// GenerateSessionTitleWithLLM uses an LLM client to generate a concise
// session title. Falls back to the heuristic approach on error.
func GenerateSessionTitleWithLLM(ctx context.Context, client *llm.Client, messages []llm.Message) string {
	firstUser := firstUserMessage(messages)
	if firstUser == "" {
		return "Untitled session"
	}

	// Truncate long input to avoid wasting tokens on title generation.
	input := firstUser
	if utf8.RuneCountInString(input) > 500 {
		runes := []rune(input)
		input = string(runes[:500])
	}

	resp, err := client.CreateChatCompletion(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{
				Role:    "user",
				Content: "Generate a concise 3-8 word title for this conversation. Output only the title, no quotes or punctuation.\n\n" + input,
			},
		},
		MaxTokens: 30,
	})
	if err != nil {
		slog.Debug("LLM title generation failed, falling back to heuristic", "error", err)
		return generateSessionTitleHeuristic(messages)
	}

	title := strings.TrimSpace(resp.Content)
	if title == "" {
		return generateSessionTitleHeuristic(messages)
	}

	// Sanitize: strip quotes that the model might add despite the prompt.
	title = strings.Trim(title, "\"'")

	return title
}

// generateSessionTitleHeuristic extracts a title from the first user message
// using simple string manipulation. No LLM call is needed.
func generateSessionTitleHeuristic(messages []llm.Message) string {
	firstUser := firstUserMessage(messages)
	if firstUser == "" {
		return "Untitled session"
	}

	// Use the first line, capped at 80 runes.
	title := firstUser
	if idx := strings.IndexAny(title, "\n\r"); idx > 0 {
		title = title[:idx]
	}
	title = strings.TrimSpace(title)

	if utf8.RuneCountInString(title) > 80 {
		runes := []rune(title)
		title = string(runes[:77]) + "..."
	}

	return title
}

// firstUserMessage returns the content of the first non-empty user message.
func firstUserMessage(messages []llm.Message) string {
	for _, m := range messages {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			return strings.TrimSpace(m.Content)
		}
	}
	return ""
}

// autoGenerateTitle generates and persists a session title on the first
// conversation turn. Call this after the first user message has been
// appended.
func (a *AIAgent) autoGenerateTitle(messages []llm.Message) {
	if a.sessionDB == nil {
		return
	}

	// Only set title if it is currently empty.
	existing := a.sessionDB.GetSessionTitle(a.sessionID)
	if existing != "" {
		return
	}

	var title string
	if a.auxiliaryClient != nil && a.auxiliaryClient.SummaryClient() != nil {
		title = GenerateSessionTitleWithLLM(context.Background(), a.auxiliaryClient.SummaryClient(), messages)
	} else {
		title = GenerateSessionTitle(messages)
	}

	if err := a.sessionDB.SetSessionTitle(a.sessionID, title); err != nil {
		slog.Warn("Failed to set session title", "error", err)
	}
}

// GenerateTitleForMessages produces a title, optionally using an LLM client.
func GenerateTitleForMessages(ctx context.Context, client *llm.Client, messages []llm.Message) string {
	if client != nil {
		return GenerateSessionTitleWithLLM(ctx, client, messages)
	}
	return GenerateSessionTitle(messages)
}
