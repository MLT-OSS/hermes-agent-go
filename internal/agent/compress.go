package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// chatCompleter is the subset of llm.Client used by context compression.
type chatCompleter interface {
	CreateChatCompletion(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error)
}

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

const (
	// toolResultPruneThreshold is the character count above which tool results
	// are replaced with a short placeholder before LLM summarisation.
	toolResultPruneThreshold = 500

	// toolResultPreviewLen is how many leading characters of a pruned tool
	// result to keep in the placeholder.
	toolResultPreviewLen = 100

	// tailBudgetFraction is the fraction of the context window reserved for
	// recent (tail) messages that are never compressed.
	tailBudgetFraction = 0.25

	// compressionFailureCooldown is the minimum duration between compression
	// attempts after a failure.
	compressionFailureCooldown = 10 * time.Minute

	// summaryHeader is the markdown header that marks a previous conversation
	// summary inside the message stream.
	summaryHeader = "## Conversation Summary"
)

// CompressionConfig controls context compression behavior.
type CompressionConfig struct {
	// Threshold is the fraction of context window that triggers compression (0.0-1.0).
	Threshold float64

	// Strategy selects which compression approach to use.
	Strategy CompressionStrategy

	// KeepCount is the minimum number of recent messages to preserve.
	// Deprecated: token-budget tail protection is used when ContextWindow > 0.
	KeepCount int

	// SummaryMaxWords is the target length for LLM summaries.
	SummaryMaxWords int

	// ContextWindow overrides the model's context length for tail-budget
	// calculation.  When zero the model metadata is used.
	ContextWindow int
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
	if a.isInCompressionCooldown() {
		return false
	}

	meta := llm.GetModelMeta(a.model)
	totalTokens := estimateConversationTokens(messages, a.systemPrompt)

	threshold := int(float64(meta.ContextLength) * a.compressionCfg.Threshold)
	return totalTokens > threshold
}

// CompressContext applies the configured compression strategy to free context space.
func (a *AIAgent) CompressContext(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	cfg := a.compressionCfg
	keepCount := a.tailKeepCount(messages)

	if len(messages) <= keepCount {
		return messages, nil
	}

	slog.Info("Compressing context",
		"strategy", string(cfg.Strategy),
		"message_count", len(messages),
		"keep_count", keepCount,
	)
	a.fireStatus("Compressing context...")

	var result []llm.Message
	var err error

	switch cfg.Strategy {
	case StrategySlidingWindow:
		result = compressSlidingWindow(messages, keepCount)
	case StrategySummarize:
		result, err = a.compressSummarize(ctx, messages, keepCount, cfg)
	case StrategyHybrid:
		result, err = a.compressHybrid(ctx, messages, keepCount, cfg)
	default:
		result, err = a.compressHybrid(ctx, messages, keepCount, cfg)
	}

	if err != nil {
		a.recordCompressionFailure()
		return messages, err
	}
	return result, nil
}

// --- Sliding window strategy ---

func compressSlidingWindow(messages []llm.Message, keepCount int) []llm.Message {
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

func (a *AIAgent) compressSummarize(ctx context.Context, messages []llm.Message, keepCount int, cfg CompressionConfig) ([]llm.Message, error) {
	if keepCount > len(messages) {
		return messages, nil
	}

	toSummarize := messages[:len(messages)-keepCount]
	toKeep := messages[len(messages)-keepCount:]

	summary, err := a.generateSummary(ctx, toSummarize, cfg.SummaryMaxWords)
	if err != nil {
		return messages, fmt.Errorf("compress summarize: %w", err)
	}

	result := make([]llm.Message, 0, keepCount+1)
	result = append(result, llm.Message{
		Role:    "system",
		Content: fmt.Sprintf("[Context Summary -- %d messages compressed]\n%s", len(toSummarize), summary),
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

func (a *AIAgent) compressHybrid(ctx context.Context, messages []llm.Message, keepCount int, cfg CompressionConfig) ([]llm.Message, error) {
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
			return compressSlidingWindow(messages, keepCount), nil
		}
	}

	// Assemble result: summary + key messages + recent messages
	result := make([]llm.Message, 0, len(keyMessages)+keepCount+2)

	if summaryText != "" {
		result = append(result, llm.Message{
			Role: "system",
			Content: fmt.Sprintf("[Context Summary -- %d messages compressed, %d key messages preserved]\n%s",
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

	// Short messages are often corrections or confirmations -- keep them
	// (they're cheap in tokens anyway)
	if len(m.Content) < 100 {
		return true
	}

	return false
}

// generateSummary builds an LLM prompt from messages and returns a structured
// summary.  When the messages already contain a previous summary (identified by
// summaryHeader), the LLM is asked to update it incrementally instead of
// recreating from scratch.
func (a *AIAgent) generateSummary(ctx context.Context, messages []llm.Message, maxWords int) (string, error) {
	// Pre-pass: prune large tool results.
	pruned := pruneToolResults(messages)

	// Detect a previous summary in the message stream.
	var prevSummary string
	for _, m := range pruned {
		if idx := strings.Index(m.Content, summaryHeader); idx >= 0 {
			prevSummary = m.Content[idx:]
			break
		}
	}

	var sb strings.Builder

	if prevSummary != "" {
		// Iterative update mode.
		sb.WriteString(fmt.Sprintf(
			"Below is an existing conversation summary followed by new messages. "+
				"Update the summary to incorporate the new information (max %d words). "+
				"Keep the same structured format with these sections:\n"+
				"## Conversation Summary\n"+
				"### Goal\n### Progress\n### Decisions\n### Files Modified\n### Next Steps\n\n"+
				"--- EXISTING SUMMARY ---\n%s\n--- END EXISTING SUMMARY ---\n\n"+
				"--- NEW MESSAGES ---\n",
			maxWords, prevSummary,
		))
	} else {
		sb.WriteString(fmt.Sprintf(
			"Summarize the following conversation history (max %d words) using this structure:\n\n"+
				"## Conversation Summary\n"+
				"### Goal\n<one-line description of the user's objective>\n"+
				"### Progress\n<bullet list of completed steps>\n"+
				"### Decisions\n<bullet list of decisions and constraints>\n"+
				"### Files Modified\n<bullet list of file paths changed, if any>\n"+
				"### Next Steps\n<bullet list of remaining work>\n\n"+
				"Preserve specific file paths, variable names, and technical details.\n\n",
			maxWords,
		))
	}

	for _, m := range pruned {
		// Skip the message that carries the old summary to avoid duplication.
		if prevSummary != "" && strings.Contains(m.Content, summaryHeader) {
			continue
		}

		content := truncate(m.Content, 800)
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, content))

		// Include tool call info
		for _, tc := range m.ToolCalls {
			args := truncate(tc.Function.Arguments, 200)
			sb.WriteString(fmt.Sprintf("  -> tool: %s(%s)\n", tc.Function.Name, args))
		}
	}

	if prevSummary != "" {
		sb.WriteString("--- END NEW MESSAGES ---\n")
	}

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: sb.String()},
		},
	}

	resp, err := a.compressionCompleter().CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("generate summary: %w", err)
	}

	return resp.Content, nil
}

// compressionCompleter returns the chatCompleter used for summarisation.
// It prefers the dedicated summaryCompleter and falls back to the main
// LLM client.
func (a *AIAgent) compressionCompleter() chatCompleter {
	if a.summaryCompleter != nil {
		return a.summaryCompleter
	}
	return a.client
}

// pruneToolResults replaces large tool-role message content with a short
// placeholder to reduce tokens before sending to the summarisation LLM.
func pruneToolResults(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if m.Role == "tool" && len(m.Content) > toolResultPruneThreshold {
			preview := m.Content
			if len(preview) > toolResultPreviewLen {
				preview = preview[:toolResultPreviewLen]
			}
			name := m.ToolName
			if name == "" {
				name = "unknown"
			}
			out[i].Content = fmt.Sprintf("[Tool result: %s -- %s... (pruned)]", name, preview)
		}
	}
	return out
}

// tailKeepCount determines how many recent messages to protect from
// compression.  It uses a token-budget heuristic: keep enough tail messages to
// fill ~25 % of the context window.  Falls back to KeepCount when the budget
// cannot be computed or KeepCount is larger.
func (a *AIAgent) tailKeepCount(messages []llm.Message) int {
	ctxLen := a.compressionCfg.ContextWindow
	if ctxLen == 0 {
		ctxLen = llm.GetModelMeta(a.model).ContextLength
	}
	if ctxLen == 0 {
		return a.compressionCfg.KeepCount
	}

	budgetTokens := int(float64(ctxLen) * tailBudgetFraction)
	accumulated := 0
	keep := 0

	for i := len(messages) - 1; i >= 0; i-- {
		tokens := llm.EstimateTokens(messages[i].Content)
		for _, tc := range messages[i].ToolCalls {
			tokens += llm.EstimateTokens(tc.Function.Arguments)
		}
		if accumulated+tokens > budgetTokens {
			break
		}
		accumulated += tokens
		keep++
	}

	// Never keep fewer than the configured minimum.
	if keep < a.compressionCfg.KeepCount {
		keep = a.compressionCfg.KeepCount
	}
	return keep
}

// isInCompressionCooldown returns true when a recent compression failure
// should prevent another attempt.
func (a *AIAgent) isInCompressionCooldown() bool {
	if a.lastCompressionFailure.IsZero() {
		return false
	}
	return time.Since(a.lastCompressionFailure) < compressionFailureCooldown
}

// recordCompressionFailure stores the current time so that the cooldown logic
// can skip future attempts.
func (a *AIAgent) recordCompressionFailure() {
	a.lastCompressionFailure = time.Now()
	slog.Warn("Compression failed, entering cooldown", "cooldown", compressionFailureCooldown)
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
