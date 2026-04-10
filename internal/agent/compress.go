package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// CompressionStrategy defines which compression approach to use.
type CompressionStrategy string

const (
	// StrategySlidingWindow keeps the most recent N messages, drops the oldest.
	StrategySlidingWindow CompressionStrategy = "sliding_window"

	// StrategySummarize uses LLM to summarize older messages.
	StrategySummarize CompressionStrategy = "summarize"

	// StrategyHybrid keeps key messages + summarizes the rest (default).
	StrategyHybrid CompressionStrategy = "hybrid"
)

// CompressionConfig controls context compression behavior.
type CompressionConfig struct {
	// Threshold is the fraction of context window that triggers compression (0.0-1.0).
	Threshold float64

	// Strategy selects which compression approach to use.
	Strategy CompressionStrategy

	// KeepCount is the minimum number of recent messages to preserve.
	KeepCount int

	// SummaryMaxWords is the target length for LLM summaries.
	SummaryMaxWords int
}

// DefaultCompressionConfig returns sensible defaults.
func DefaultCompressionConfig() CompressionConfig {
	return CompressionConfig{
		Threshold:       0.75,
		Strategy:        StrategyHybrid,
		KeepCount:       6,
		SummaryMaxWords: 500,
	}
}

// ShouldCompress returns true if the conversation should be compressed.
func (a *AIAgent) ShouldCompress(messages []llm.Message) bool {
	meta := llm.GetModelMeta(a.model)
	totalTokens := estimateConversationTokens(messages, a.systemPrompt)

	threshold := int(float64(meta.ContextLength) * a.compressionCfg.Threshold)
	return totalTokens > threshold
}

// CompressContext applies the configured compression strategy to free context space.
func (a *AIAgent) CompressContext(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	cfg := a.compressionCfg

	if len(messages) <= cfg.KeepCount {
		return messages, nil
	}

	slog.Info("Compressing context",
		"strategy", string(cfg.Strategy),
		"message_count", len(messages),
		"keep_count", cfg.KeepCount,
	)
	a.fireStatus("Compressing context...")

	switch cfg.Strategy {
	case StrategySlidingWindow:
		return compressSlidingWindow(messages, cfg), nil
	case StrategySummarize:
		return a.compressSummarize(ctx, messages, cfg)
	case StrategyHybrid:
		return a.compressHybrid(ctx, messages, cfg)
	default:
		return a.compressHybrid(ctx, messages, cfg)
	}
}

// --- Sliding window strategy ---

func compressSlidingWindow(messages []llm.Message, cfg CompressionConfig) []llm.Message {
	keepCount := cfg.KeepCount
	if keepCount > len(messages) {
		keepCount = len(messages)
	}

	kept := messages[len(messages)-keepCount:]
	droppedCount := len(messages) - keepCount

	result := make([]llm.Message, 0, keepCount+1)
	result = append(result, llm.Message{
		Role:    "system",
		Content: fmt.Sprintf("[Context Note: %d earlier messages were dropped to fit context window]", droppedCount),
	})
	result = append(result, kept...)

	slog.Info("Sliding window compression",
		"dropped", droppedCount,
		"kept", keepCount,
	)
	return result
}

// --- Summarize strategy ---

func (a *AIAgent) compressSummarize(ctx context.Context, messages []llm.Message, cfg CompressionConfig) ([]llm.Message, error) {
	keepCount := cfg.KeepCount
	if keepCount > len(messages) {
		return messages, nil
	}

	toSummarize := messages[:len(messages)-keepCount]
	toKeep := messages[len(messages)-keepCount:]

	summary, err := a.generateSummary(ctx, toSummarize, cfg.SummaryMaxWords)
	if err != nil {
		return messages, nil // fail gracefully
	}

	result := make([]llm.Message, 0, keepCount+1)
	result = append(result, llm.Message{
		Role:    "system",
		Content: fmt.Sprintf("[Context Summary — %d messages compressed]\n%s", len(toSummarize), summary),
	})
	result = append(result, toKeep...)

	slog.Info("Summarize compression",
		"summarized", len(toSummarize),
		"kept", keepCount,
		"summary_len", len(summary),
	)
	return result, nil
}

// --- Hybrid strategy ---
// Keeps key messages (user corrections, decisions) + summarizes the rest.

func (a *AIAgent) compressHybrid(ctx context.Context, messages []llm.Message, cfg CompressionConfig) ([]llm.Message, error) {
	keepCount := cfg.KeepCount
	if keepCount > len(messages) {
		return messages, nil
	}

	// Split into old (compressible) and recent (keep as-is)
	oldMessages := messages[:len(messages)-keepCount]
	recentMessages := messages[len(messages)-keepCount:]

	// Identify key messages in the old section
	var keyMessages []llm.Message
	var summarizable []llm.Message

	for _, m := range oldMessages {
		if isKeyMessage(m) {
			keyMessages = append(keyMessages, m)
		} else {
			summarizable = append(summarizable, m)
		}
	}

	// Summarize non-key messages
	var summaryText string
	if len(summarizable) > 0 {
		var err error
		summaryText, err = a.generateSummary(ctx, summarizable, cfg.SummaryMaxWords)
		if err != nil {
			// Fallback to sliding window on summarization failure
			return compressSlidingWindow(messages, cfg), nil
		}
	}

	// Assemble result: summary + key messages + recent messages
	result := make([]llm.Message, 0, len(keyMessages)+keepCount+2)

	if summaryText != "" {
		result = append(result, llm.Message{
			Role: "system",
			Content: fmt.Sprintf("[Context Summary — %d messages compressed, %d key messages preserved]\n%s",
				len(summarizable), len(keyMessages), summaryText),
		})
	}

	result = append(result, keyMessages...)
	result = append(result, recentMessages...)

	slog.Info("Hybrid compression",
		"total_old", len(oldMessages),
		"key_preserved", len(keyMessages),
		"summarized", len(summarizable),
		"recent_kept", keepCount,
	)
	return result, nil
}

// --- Helpers ---

// isKeyMessage returns true if a message should be preserved during compression.
// Key messages include: user corrections, explicit decisions, error resolutions.
func isKeyMessage(m llm.Message) bool {
	if m.Role != "user" && m.Role != "assistant" {
		return false
	}

	lower := strings.ToLower(m.Content)

	// User corrections / decisions
	correctionMarkers := []string{
		"no, ", "wrong", "don't ", "do not ", "stop ", "instead ",
		"actually ", "correction:", "important:", "remember:",
		"decision:", "note:", "always ", "never ",
	}
	for _, marker := range correctionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}

	// Short messages are often corrections or confirmations — keep them
	// (they're cheap in tokens anyway)
	if len(m.Content) < 100 {
		return true
	}

	return false
}

func (a *AIAgent) generateSummary(ctx context.Context, messages []llm.Message, maxWords int) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Summarize the following conversation history concisely (max %d words). "+
			"Focus on: key decisions made, facts learned, current task state, "+
			"and any corrections or constraints the user specified. "+
			"Preserve specific file paths, variable names, and technical details.\n\n",
		maxWords,
	))

	for _, m := range messages {
		content := m.Content
		if len(content) > 800 {
			content = content[:800] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))

		// Include tool call info
		for _, tc := range m.ToolCalls {
			args := tc.Function.Arguments
			if len(args) > 200 {
				args = args[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("  → tool: %s(%s)\n", tc.Function.Name, args))
		}
	}

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: sb.String()},
		},
	}

	resp, err := a.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("generate summary: %w", err)
	}

	return resp.Content, nil
}

func estimateConversationTokens(messages []llm.Message, systemPrompt string) int {
	total := llm.EstimateTokens(systemPrompt)
	for _, m := range messages {
		total += llm.EstimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += llm.EstimateTokens(tc.Function.Arguments)
		}
	}
	return total
}
