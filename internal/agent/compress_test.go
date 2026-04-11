package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

func TestIsKeyMessage(t *testing.T) {
	t.Helper()

	tests := []struct {
		name string
		msg  llm.Message
		want bool
	}{
		{"user correction", llm.Message{Role: "user", Content: "No, use the other file"}, true},
		{"user decision", llm.Message{Role: "user", Content: "Decision: we go with plan B"}, true},
		{"user remember", llm.Message{Role: "user", Content: "Remember: always use UTC"}, true},
		{"user short msg", llm.Message{Role: "user", Content: "yes"}, true},
		{"user long boring", llm.Message{Role: "user", Content: string(make([]byte, 200))}, false},
		{"tool message", llm.Message{Role: "tool", Content: "No, this is wrong"}, false},
		{"system message", llm.Message{Role: "system", Content: "important: do X"}, false},
		{"assistant with stop", llm.Message{Role: "assistant", Content: "Stop doing that approach"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKeyMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isKeyMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompressSlidingWindow(t *testing.T) {
	t.Helper()

	messages := make([]llm.Message, 10)
	for i := range messages {
		messages[i] = llm.Message{
			Role:    "user",
			Content: "message " + string(rune('A'+i)),
		}
	}

	result := compressSlidingWindow(messages, 4)

	// Should have 1 system note + 4 kept messages = 5
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(result))
	}

	// First message should be the context note
	if result[0].Role != "system" {
		t.Errorf("first message should be system, got %s", result[0].Role)
	}

	// Last 4 should be the original last 4
	for i := 1; i < 5; i++ {
		if result[i].Content != messages[6+i-1].Content {
			t.Errorf("result[%d] = %q, want %q", i, result[i].Content, messages[6+i-1].Content)
		}
	}
}

func TestCompressSlidingWindow_FewerThanKeep(t *testing.T) {
	t.Helper()

	messages := []llm.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}

	result := compressSlidingWindow(messages, 10)

	// Should keep all + 1 system note
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}
}

func TestEstimateConversationTokens(t *testing.T) {
	t.Helper()

	messages := []llm.Message{
		{Role: "user", Content: "hello world"},
		{Role: "assistant", Content: "hi there"},
	}

	tokens := estimateConversationTokens(messages, "you are helpful")
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestDefaultCompressionConfig(t *testing.T) {
	t.Helper()

	cfg := DefaultCompressionConfig()

	if cfg.Threshold != 0.75 {
		t.Errorf("threshold = %v, want 0.75", cfg.Threshold)
	}
	if cfg.Strategy != StrategyHybrid {
		t.Errorf("strategy = %v, want hybrid", cfg.Strategy)
	}
	if cfg.KeepCount != 6 {
		t.Errorf("keep_count = %d, want 6", cfg.KeepCount)
	}
	if cfg.SummaryMaxWords != 500 {
		t.Errorf("summary_max_words = %d, want 500", cfg.SummaryMaxWords)
	}
}

// ---------------------------------------------------------------------------
// Tool result pruning
// ---------------------------------------------------------------------------

func TestPruneToolResults(t *testing.T) {
	t.Helper()

	tests := []struct {
		name        string
		messages    []llm.Message
		wantPruned  int // how many messages should have been pruned
		checkSubstr string
	}{
		{
			name: "short tool result kept",
			messages: []llm.Message{
				{Role: "tool", Content: "ok", ToolName: "read_file"},
			},
			wantPruned: 0,
		},
		{
			name: "large tool result pruned",
			messages: []llm.Message{
				{Role: "tool", Content: strings.Repeat("x", 600), ToolName: "search_files"},
			},
			wantPruned:  1,
			checkSubstr: "[Tool result: search_files",
		},
		{
			name: "non-tool message untouched",
			messages: []llm.Message{
				{Role: "user", Content: strings.Repeat("x", 600)},
			},
			wantPruned: 0,
		},
		{
			name: "tool with empty name uses unknown",
			messages: []llm.Message{
				{Role: "tool", Content: strings.Repeat("y", 600)},
			},
			wantPruned:  1,
			checkSubstr: "[Tool result: unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := pruneToolResults(tt.messages)
			if len(out) != len(tt.messages) {
				t.Fatalf("output length %d != input length %d", len(out), len(tt.messages))
			}

			pruned := 0
			for i, m := range out {
				if m.Content != tt.messages[i].Content {
					pruned++
				}
			}
			if pruned != tt.wantPruned {
				t.Errorf("pruned %d messages, want %d", pruned, tt.wantPruned)
			}
			if tt.checkSubstr != "" {
				found := false
				for _, m := range out {
					if strings.Contains(m.Content, tt.checkSubstr) {
						found = true
					}
				}
				if !found {
					t.Errorf("expected substring %q in pruned output", tt.checkSubstr)
				}
			}
		})
	}
}

func TestPruneToolResults_PreservesPreview(t *testing.T) {
	t.Helper()

	original := "HEADER:" + strings.Repeat("a", 600)
	msgs := []llm.Message{
		{Role: "tool", Content: original, ToolName: "read_file"},
	}

	out := pruneToolResults(msgs)
	if !strings.Contains(out[0].Content, "HEADER:") {
		t.Error("pruned content should contain the first 100 chars of the original")
	}
	if strings.Contains(out[0].Content, strings.Repeat("a", 200)) {
		t.Error("pruned content should NOT contain the full payload")
	}
}

// ---------------------------------------------------------------------------
// Token-budget tail protection
// ---------------------------------------------------------------------------

func TestTailKeepCount_BasicBudget(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model: "openai/gpt-4o", // 128000 context
		compressionCfg: CompressionConfig{
			KeepCount: 2,
		},
	}

	// Each message ~100 chars = ~25 tokens. Budget = 128000 * 0.25 = 32000 tokens.
	// All 10 messages fit easily (250 tokens total).
	messages := make([]llm.Message, 10)
	for i := range messages {
		messages[i] = llm.Message{Role: "user", Content: strings.Repeat("a", 100)}
	}

	keep := a.tailKeepCount(messages)
	if keep < 10 {
		t.Errorf("all messages should fit budget, got keep=%d", keep)
	}
}

func TestTailKeepCount_BudgetExceeded(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model: "openai/gpt-4o", // 128000 context
		compressionCfg: CompressionConfig{
			KeepCount:     2,
			ContextWindow: 400, // tiny: budget = 100 tokens = 400 chars
		},
	}

	// 10 messages of 100 chars each = 1000 chars total; budget is 400 chars.
	messages := make([]llm.Message, 10)
	for i := range messages {
		messages[i] = llm.Message{Role: "user", Content: strings.Repeat("b", 100)}
	}

	keep := a.tailKeepCount(messages)
	// 400 chars budget / 100 chars per msg => 4 messages fit, but the 5th
	// would exceed.  So keep should be 4 (>= KeepCount=2).
	if keep > 5 {
		t.Errorf("expected keep <= 5 with tiny budget, got %d", keep)
	}
	if keep < 2 {
		t.Errorf("should respect minimum KeepCount=2, got %d", keep)
	}
}

func TestTailKeepCount_FallbackToKeepCount(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model: "openai/gpt-4o",
		compressionCfg: CompressionConfig{
			KeepCount:     8,
			ContextWindow: 100, // very tiny budget
		},
	}

	messages := make([]llm.Message, 10)
	for i := range messages {
		messages[i] = llm.Message{Role: "user", Content: strings.Repeat("c", 200)}
	}

	keep := a.tailKeepCount(messages)
	if keep < 8 {
		t.Errorf("should not go below KeepCount=8, got %d", keep)
	}
}

// ---------------------------------------------------------------------------
// Failure cooldown
// ---------------------------------------------------------------------------

func TestCompressionCooldown_NoFailure(t *testing.T) {
	t.Helper()

	a := &AIAgent{}
	if a.isInCompressionCooldown() {
		t.Error("should not be in cooldown when no failure recorded")
	}
}

func TestCompressionCooldown_RecentFailure(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		lastCompressionFailure: time.Now(),
	}
	if !a.isInCompressionCooldown() {
		t.Error("should be in cooldown right after failure")
	}
}

func TestCompressionCooldown_ExpiredFailure(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		lastCompressionFailure: time.Now().Add(-11 * time.Minute),
	}
	if a.isInCompressionCooldown() {
		t.Error("cooldown should have expired after 11 minutes")
	}
}

func TestRecordCompressionFailure(t *testing.T) {
	t.Helper()

	a := &AIAgent{}
	if a.isInCompressionCooldown() {
		t.Fatal("precondition: no cooldown")
	}

	a.recordCompressionFailure()
	if !a.isInCompressionCooldown() {
		t.Error("should be in cooldown after recordCompressionFailure")
	}
}

// ---------------------------------------------------------------------------
// Structured summary template & iterative updates
// ---------------------------------------------------------------------------

// stubClient implements a minimal CreateChatCompletion that echoes back a
// canned response, avoiding real API calls in unit tests.
type stubLLMClient struct {
	response string
	lastReq  llm.ChatRequest
	err      error
}

func (s *stubLLMClient) CreateChatCompletion(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &llm.ChatResponse{Content: s.response}, nil
}

func TestGenerateSummary_StructuredPrompt(t *testing.T) {
	t.Helper()

	stub := &stubLLMClient{response: "## Conversation Summary\n### Goal\nTest goal"}
	a := &AIAgent{summaryCompleter: stub}

	messages := []llm.Message{
		{Role: "user", Content: "Please refactor the auth module"},
		{Role: "assistant", Content: "Sure, I will start with the login handler."},
	}

	summary, err := a.generateSummary(context.Background(), messages, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary == "" {
		t.Fatal("summary should not be empty")
	}

	// The prompt sent to the LLM should contain the structured template headers.
	prompt := stub.lastReq.Messages[0].Content
	for _, section := range []string{"### Goal", "### Progress", "### Decisions", "### Files Modified", "### Next Steps"} {
		if !strings.Contains(prompt, section) {
			t.Errorf("prompt missing section %q", section)
		}
	}
}

func TestGenerateSummary_IterativeUpdate(t *testing.T) {
	t.Helper()

	stub := &stubLLMClient{response: "## Conversation Summary\n### Goal\nUpdated goal"}
	a := &AIAgent{summaryCompleter: stub}

	prevSummary := "## Conversation Summary\n### Goal\nOld goal\n### Progress\n- step 1"
	messages := []llm.Message{
		{Role: "system", Content: "[Context Summary]\n" + prevSummary},
		{Role: "user", Content: "Now also update the tests"},
	}

	_, err := a.generateSummary(context.Background(), messages, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := stub.lastReq.Messages[0].Content
	if !strings.Contains(prompt, "EXISTING SUMMARY") {
		t.Error("iterative mode should include EXISTING SUMMARY marker")
	}
	if !strings.Contains(prompt, "Update the summary") {
		t.Error("iterative mode should instruct LLM to update")
	}
}

func TestGenerateSummary_PrunesToolResultsBeforeSummarising(t *testing.T) {
	t.Helper()

	stub := &stubLLMClient{response: "summary"}
	a := &AIAgent{summaryCompleter: stub}

	bigToolContent := strings.Repeat("Z", 700)
	messages := []llm.Message{
		{Role: "tool", Content: bigToolContent, ToolName: "search_files"},
	}

	_, err := a.generateSummary(context.Background(), messages, 300)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := stub.lastReq.Messages[0].Content
	if strings.Contains(prompt, bigToolContent) {
		t.Error("large tool result should have been pruned from the prompt")
	}
	if !strings.Contains(prompt, "[Tool result: search_files") {
		t.Error("pruned placeholder should appear in the prompt")
	}
}

func TestGenerateSummary_LLMError(t *testing.T) {
	t.Helper()

	stub := &stubLLMClient{err: fmt.Errorf("api timeout")}
	a := &AIAgent{summaryCompleter: stub}

	_, err := a.generateSummary(context.Background(), []llm.Message{
		{Role: "user", Content: "hello"},
	}, 300)
	if err == nil {
		t.Fatal("expected error when LLM fails")
	}
	if !strings.Contains(err.Error(), "generate summary") {
		t.Errorf("error should wrap with context, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ShouldCompress respects cooldown
// ---------------------------------------------------------------------------

func TestShouldCompress_RespectsFailureCooldown(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model:                  "openai/gpt-4o",
		compressionCfg:         DefaultCompressionConfig(),
		lastCompressionFailure: time.Now(), // just failed
	}

	// Build messages that would exceed threshold.
	big := make([]llm.Message, 0)
	for i := 0; i < 100; i++ {
		big = append(big, llm.Message{Role: "user", Content: strings.Repeat("x", 10000)})
	}

	if a.ShouldCompress(big) {
		t.Error("ShouldCompress should return false during cooldown")
	}
}

// ---------------------------------------------------------------------------
// CompressContext integration (sliding window, no LLM needed)
// ---------------------------------------------------------------------------

func TestCompressContext_SlidingWindow(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model: "openai/gpt-4o",
		compressionCfg: CompressionConfig{
			Strategy:      StrategySlidingWindow,
			KeepCount:     3,
			ContextWindow: 200, // tiny window so tailKeepCount stays at KeepCount
		},
	}

	// Each message is ~40 chars = ~10 tokens.  With a 200-token window the
	// budget is 50 tokens, so only ~5 fit, but KeepCount=3 is the floor.
	messages := make([]llm.Message, 8)
	for i := range messages {
		messages[i] = llm.Message{Role: "user", Content: fmt.Sprintf("msg-%d with some padding text here", i)}
	}

	result, err := a.CompressContext(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// system note + at least KeepCount messages
	if len(result) < 4 {
		t.Errorf("expected at least 4 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("first message should be system note, got %s", result[0].Role)
	}
}

func TestCompressContext_TooFewMessages(t *testing.T) {
	t.Helper()

	a := &AIAgent{
		model: "openai/gpt-4o",
		compressionCfg: CompressionConfig{
			Strategy:  StrategySummarize,
			KeepCount: 10,
		},
	}

	messages := []llm.Message{
		{Role: "user", Content: "hi"},
	}

	result, err := a.CompressContext(context.Background(), messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("should return messages unchanged when count <= keepCount, got %d", len(result))
	}
}
