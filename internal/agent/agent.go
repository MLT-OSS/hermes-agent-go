package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
	"github.com/hermes-agent/hermes-agent-go/internal/state"
	"github.com/hermes-agent/hermes-agent-go/internal/tools"
	"github.com/hermes-agent/hermes-agent-go/internal/toolsets"
	openai "github.com/sashabaranov/go-openai"
)

// FallbackModel describes an alternative model to try on API failures.
type FallbackModel struct {
	Model    string `yaml:"model"`
	Provider string `yaml:"provider"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
}

// AIAgent is the core agent that manages conversations with LLM and tools.
type AIAgent struct {
	// Configuration
	model                string
	baseURL              string
	apiKey               string
	provider             string
	apiMode              string // "openai" or "anthropic"
	maxIterations        int
	platform             string
	sessionID            string
	quietMode            bool
	enabledToolsets      []string
	disabledToolsets     []string
	ephemeralSystemPrompt string
	skipContextFiles     bool
	skipMemory           bool
	persistSession       bool
	compressionCfg       CompressionConfig

	// Session resume
	resumeSessionID string

	// Fallback model chain
	fallbackModels []FallbackModel

	// Smart routing
	smartRouter *SmartRouter

	// Runtime state
	client          *llm.Client
	auxiliaryClient *AuxiliaryClient
	sessionDB       *state.SessionDB
	budget        *IterationBudget
	callbacks     *StreamCallbacks
	toolDefs      []openai.Tool
	validTools    map[string]bool
	systemPrompt  string

	// Interrupt support
	mu                sync.Mutex
	interruptRequested bool
	apiCallCount      int
	lastActivity      time.Time

	// Compression cooldown
	lastCompressionFailure time.Time

	// summaryCompleter overrides the LLM client used for context
	// compression summaries.  Nil means use the main client.
	summaryCompleter chatCompleter

	// Token tracking
	totalInputTokens  int
	totalOutputTokens int
	totalCacheRead    int
	totalCacheWrite   int
	totalReasoning    int

	// Cost tracking
	cumulativeCostUSD float64
}

// New creates a new AIAgent with the given options.
func New(opts ...AgentOption) (*AIAgent, error) {
	cfg := config.Load()

	a := &AIAgent{
		model:          cfg.Model,
		baseURL:        cfg.BaseURL,
		apiKey:         cfg.APIKey,
		provider:       cfg.Provider,
		apiMode:        cfg.APIMode,
		maxIterations:  cfg.MaxIterations,
		platform:       "cli",
		persistSession: true,
		compressionCfg: DefaultCompressionConfig(),
		lastActivity:   time.Now(),
	}

	// Options override config defaults
	for _, opt := range opts {
		opt(a)
	}

	// Create iteration budget if not shared
	if a.budget == nil {
		a.budget = NewIterationBudget(a.maxIterations)
	}

	// Generate session ID if not set
	if a.sessionID == "" {
		a.sessionID = uuid.New().String()
	}

	// Create LLM client
	var err error
	if a.baseURL != "" && a.apiKey != "" {
		if a.apiMode != "" {
			a.client, err = llm.NewClientWithMode(a.model, a.baseURL, a.apiKey, a.provider, llm.APIMode(a.apiMode))
		} else {
			a.client, err = llm.NewClientWithParams(a.model, a.baseURL, a.apiKey, a.provider)
		}
	} else {
		a.client, err = llm.NewClient(cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	a.model = a.client.Model()
	a.provider = a.client.Provider()
	a.baseURL = a.client.BaseURL()

	// Initialize auxiliary clients
	a.auxiliaryClient = NewAuxiliaryClient(cfg)
	if a.auxiliaryClient != nil && a.auxiliaryClient.SummaryClient() != nil {
		a.summaryCompleter = a.auxiliaryClient.SummaryClient()
	}

	// Open session DB
	if a.persistSession {
		a.sessionDB, err = state.NewSessionDB("")
		if err != nil {
			slog.Warn("Could not open session DB", "error", err)
		}
	}

	// Handle session resume
	if a.resumeSessionID != "" {
		if err := a.loadResumedSession(); err != nil {
			slog.Warn("Failed to resume session", "session_id", a.resumeSessionID, "error", err)
		}
	}

	// Build tool definitions
	a.buildToolDefs(cfg)

	// Build system prompt
	a.systemPrompt = a.buildSystemPrompt()

	return a, nil
}

// Chat is the simple chat interface. Returns final response text.
func (a *AIAgent) Chat(message string) (string, error) {
	result, err := a.RunConversation(message, nil)
	if err != nil {
		return "", err
	}
	return result.FinalResponse, nil
}

// RunConversation runs a full conversation turn with tool calling.
func (a *AIAgent) RunConversation(userMessage string, history []llm.Message) (*ConversationResult, error) {
	ctx := context.Background()

	// Create session in DB
	if a.sessionDB != nil {
		a.sessionDB.CreateSession(a.sessionID, a.platform, a.model, "")
	}

	// Build messages
	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, history...)
	messages = append(messages, llm.Message{
		Role:    "user",
		Content: userMessage,
	})

	// Persist user message
	if a.sessionDB != nil {
		a.sessionDB.AppendMessage(a.sessionID, "user", userMessage, "", "", nil, "")
	}

	// Auto-generate title on first turn
	a.autoGenerateTitle(messages)

	result := &ConversationResult{
		Model:    a.model,
		Provider: a.provider,
		BaseURL:  a.baseURL,
	}

	a.apiCallCount = 0
	a.interruptRequested = false

	// Main agent loop
	for a.apiCallCount < a.maxIterations {
		if !a.budget.Consume() {
			a.fireStatus("Iteration budget exhausted")
			break
		}

		if a.isInterrupted() {
			result.Interrupted = true
			break
		}

		a.apiCallCount++
		a.lastActivity = time.Now()

		// Fire step callback
		a.fireStep(a.apiCallCount, nil)

		// Build API request
		apiMessages := a.buildAPIMessages(messages)

		req := llm.ChatRequest{
			Messages: apiMessages,
			Tools:    a.toolDefs,
			Stream:   a.hasStreamConsumers(),
		}

		// Call LLM (with fallback chain on failure)
		var resp *llm.ChatResponse
		var err error

		if req.Stream {
			resp, err = a.streamingAPICall(ctx, req)
		} else {
			resp, err = a.client.CreateChatCompletion(ctx, req)
		}

		if err != nil {
			// Try fallback models
			resp, err = a.tryFallbackModels(ctx, req, err)
		}

		if err != nil {
			slog.Error("API call failed", "error", err, "attempt", a.apiCallCount)
			result.FinalResponse = fmt.Sprintf("API error: %v", err)
			result.Completed = false
			break
		}

		// Track tokens
		a.totalInputTokens += resp.Usage.PromptTokens
		a.totalOutputTokens += resp.Usage.CompletionTokens
		if a.sessionDB != nil {
			a.sessionDB.UpdateTokenCounts(a.sessionID, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, 0, 0, 0)
		}

		// Extract reasoning from think blocks if not already present
		if resp.Reasoning == "" && resp.Content != "" {
			cleaned, reasoning := ExtractThinkContent(resp.Content)
			if reasoning != "" {
				resp.Reasoning = reasoning
				resp.Content = cleaned
			}
		}

		// Sanitize surrogates to prevent JSON encoding failures
		resp.Content = SanitizeSurrogates(resp.Content)

		// Repair misspelled tool names
		if len(resp.ToolCalls) > 0 && a.validTools != nil {
			repaired, count := RepairToolCalls(resp.ToolCalls, a.validTools)
			if count > 0 {
				slog.Info("Auto-repaired tool names", "count", count)
				resp.ToolCalls = repaired
			}
		}

		// Deduplicate tool calls
		if len(resp.ToolCalls) > 1 {
			resp.ToolCalls = DeduplicateToolCalls(resp.ToolCalls)
		}

		// Build assistant message
		assistantMsg := llm.Message{
			Role:         "assistant",
			Content:      resp.Content,
			FinishReason: resp.FinishReason,
			Reasoning:    resp.Reasoning,
		}

		if len(resp.ToolCalls) > 0 {
			assistantMsg.ToolCalls = resp.ToolCalls
		}

		messages = append(messages, assistantMsg)

		// Persist assistant message
		if a.sessionDB != nil {
			var tcData []map[string]any
			for _, tc := range resp.ToolCalls {
				tcData = append(tcData, map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			a.sessionDB.AppendMessage(a.sessionID, "assistant", resp.Content, "", "", tcData, resp.Reasoning)
		}

		// Handle tool calls
		if len(resp.ToolCalls) > 0 {
			toolResults := a.executeToolCalls(resp.ToolCalls)

			for _, tr := range toolResults {
				messages = append(messages, tr)
				if a.sessionDB != nil {
					a.sessionDB.AppendMessage(a.sessionID, "tool", tr.Content, tr.ToolCallID, tr.ToolName, nil, "")
				}
			}

			if a.isInterrupted() {
				result.Interrupted = true
				break
			}

			// Continue loop for next LLM call
			continue
		}

		// No tool calls — final response
		result.FinalResponse = resp.Content
		result.Completed = true
		result.LastReasoning = resp.Reasoning
		break
	}

	// Finalize result
	result.Messages = messages
	result.APICalls = a.apiCallCount
	result.InputTokens = a.totalInputTokens
	result.OutputTokens = a.totalOutputTokens
	result.TotalTokens = a.totalInputTokens + a.totalOutputTokens
	result.CacheReadTokens = a.totalCacheRead
	result.CacheWriteTokens = a.totalCacheWrite
	result.ReasoningTokens = a.totalReasoning

	// Cost tracking
	cost := EstimateCost(a.model, a.totalInputTokens, a.totalOutputTokens)
	a.cumulativeCostUSD += cost
	result.EstimatedCostUSD = a.cumulativeCostUSD
	if _, found := GetPricing(a.model); found {
		result.CostSource = "known_pricing"
		result.CostStatus = "estimated"
	} else {
		result.CostSource = "unknown_model"
		result.CostStatus = "unavailable"
	}

	// End session
	if a.sessionDB != nil && result.Completed {
		a.sessionDB.EndSession(a.sessionID, "completed")
	}

	return result, nil
}

// Interrupt requests the agent to stop after the current tool call.
func (a *AIAgent) Interrupt() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.interruptRequested = true
}

func (a *AIAgent) isInterrupted() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interruptRequested
}

// SessionID returns the current session ID.
func (a *AIAgent) SessionID() string {
	return a.sessionID
}

// Model returns the current model.
func (a *AIAgent) Model() string {
	return a.model
}

// Callbacks returns the current stream callbacks.
func (a *AIAgent) Callbacks() *StreamCallbacks {
	return a.callbacks
}

// Close cleans up resources.
func (a *AIAgent) Close() {
	if a.sessionDB != nil {
		a.sessionDB.Close()
	}
}


// executeToolCalls runs tool calls, parallelizing when safe.
// Uses smart path-based overlap detection for file-scoped tools.
func (a *AIAgent) executeToolCalls(toolCalls []llm.ToolCall) []llm.Message {
	if len(toolCalls) == 1 || !ShouldParallelizeToolBatch(toolCalls) {
		// Sequential execution
		var results []llm.Message
		for _, tc := range toolCalls {
			if a.isInterrupted() {
				break
			}
			results = append(results, a.executeSingleTool(tc))
		}
		return results
	}

	// Parallel execution
	type indexedResult struct {
		index int
		msg   llm.Message
	}

	resultCh := make(chan indexedResult, len(toolCalls))
	sem := make(chan struct{}, MaxParallelWorkers)

	for i, tc := range toolCalls {
		sem <- struct{}{} // acquire
		go func(idx int, call llm.ToolCall) {
			defer func() { <-sem }() // release
			msg := a.executeSingleTool(call)
			resultCh <- indexedResult{index: idx, msg: msg}
		}(i, tc)
	}

	// Collect results in order
	collected := make([]llm.Message, len(toolCalls))
	for range toolCalls {
		ir := <-resultCh
		collected[ir.index] = ir.msg
	}

	return collected
}


func (a *AIAgent) executeSingleTool(tc llm.ToolCall) llm.Message {
	toolName := tc.Function.Name
	a.fireToolStart(toolName)
	a.fireToolProgress(toolName, truncate(tc.Function.Arguments, 100))

	args, err := llm.ParseToolArgs(tc.Function.Arguments)
	if err != nil {
		args = map[string]any{}
		slog.Warn("Failed to parse tool args", "tool", toolName, "error", err)
	}

	toolCtx := &tools.ToolContext{
		SessionID:  a.sessionID,
		ToolCallID: tc.ID,
		Platform:   a.platform,
	}

	toolResult := tools.Registry().Dispatch(toolName, args, toolCtx)

	// Redact secrets before the result enters conversation history
	toolResult = RedactSecrets(toolResult)

	// Save oversized results to disk
	if IsOversizedResult(toolResult) {
		slog.Info("Tool result oversized, saving to file", "tool", toolName, "chars", len(toolResult))
		toolResult = SaveOversizedResult(toolName, toolResult)
	}

	a.fireToolComplete(toolName)

	return llm.Message{
		Role:       "tool",
		Content:    toolResult,
		ToolCallID: tc.ID,
		ToolName:   toolName,
	}
}

func (a *AIAgent) buildAPIMessages(messages []llm.Message) []llm.Message {
	apiMessages := make([]llm.Message, 0, len(messages)+1)

	// System prompt
	apiMessages = append(apiMessages, llm.Message{
		Role:    "system",
		Content: a.systemPrompt,
	})

	// Conversation messages
	apiMessages = append(apiMessages, messages...)

	return apiMessages
}

func (a *AIAgent) buildToolDefs(cfg *config.Config) {
	// Resolve which tools to enable
	toolNames := resolveTools(a.enabledToolsets, a.disabledToolsets)
	a.validTools = toolNames

	// Get OpenAI-format definitions
	defs := tools.Registry().GetDefinitions(toolNames, a.quietMode)

	a.toolDefs = make([]openai.Tool, 0, len(defs))
	for _, d := range defs {
		b, _ := json.Marshal(d)
		var tool openai.Tool
		json.Unmarshal(b, &tool)
		a.toolDefs = append(a.toolDefs, tool)
	}
}

func (a *AIAgent) streamingAPICall(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	deltaCh, errCh := a.client.CreateChatCompletionStream(ctx, req)

	resp := &llm.ChatResponse{}
	var contentBuilder []byte

	for delta := range deltaCh {
		if delta.Done {
			resp.ToolCalls = delta.ToolCalls
			break
		}

		if delta.Content != "" {
			contentBuilder = append(contentBuilder, delta.Content...)
			a.fireStreamDelta(delta.Content)
		}

		if delta.Reasoning != "" {
			a.fireReasoning(delta.Reasoning)
			resp.Reasoning += delta.Reasoning
		}
	}

	// Check for streaming error
	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	default:
	}

	resp.Content = string(contentBuilder)

	if len(resp.ToolCalls) > 0 {
		resp.FinishReason = "tool_calls"
	} else {
		resp.FinishReason = "stop"
	}

	return resp, nil
}

func resolveTools(enabled, disabled []string) map[string]bool {
	var toolList []string

	if len(enabled) > 0 {
		toolList = toolsets.ResolveMultipleToolsets(enabled)
	} else {
		// Default: use hermes-cli toolset (which equals CoreTools)
		toolList = toolsets.ResolveToolset("hermes-cli")
	}

	result := make(map[string]bool, len(toolList))
	for _, t := range toolList {
		result[t] = true
	}

	// Remove disabled toolset tools
	if len(disabled) > 0 {
		disabledTools := toolsets.ResolveMultipleToolsets(disabled)
		for _, t := range disabledTools {
			delete(result, t)
		}
	}

	return result
}

// ResumeSession loads history from a previous session and resumes it.
func (a *AIAgent) ResumeSession(sessionID string) error {
	a.resumeSessionID = sessionID
	return a.loadResumedSession()
}

// loadResumedSession loads messages from the session DB for a resumed session.
func (a *AIAgent) loadResumedSession() error {
	if a.sessionDB == nil {
		return fmt.Errorf("session DB not available")
	}
	if a.resumeSessionID == "" {
		return nil
	}

	// Verify the session exists
	sess, err := a.sessionDB.GetSession(a.resumeSessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session %s not found", a.resumeSessionID)
	}

	// Use the resumed session's ID going forward
	a.sessionID = a.resumeSessionID

	slog.Info("Resumed session", "session_id", a.sessionID)
	return nil
}

// tryFallbackModels attempts each fallback model in order after the primary fails.
func (a *AIAgent) tryFallbackModels(ctx context.Context, req llm.ChatRequest, primaryErr error) (*llm.ChatResponse, error) {
	if len(a.fallbackModels) == 0 {
		return nil, primaryErr
	}

	for _, fb := range a.fallbackModels {
		slog.Warn("Primary model failed, trying fallback",
			"primary_error", primaryErr,
			"fallback_model", fb.Model)

		apiKey := fb.APIKey
		if apiKey == "" {
			apiKey = a.apiKey
		}
		baseURL := fb.BaseURL
		if baseURL == "" {
			baseURL = a.baseURL
		}

		fbClient, err := llm.NewClientWithParams(fb.Model, baseURL, apiKey, fb.Provider)
		if err != nil {
			slog.Warn("Failed to create fallback client", "model", fb.Model, "error", err)
			continue
		}

		resp, err := fbClient.CreateChatCompletion(ctx, req)
		if err != nil {
			slog.Warn("Fallback model also failed", "model", fb.Model, "error", err)
			primaryErr = err
			continue
		}

		slog.Info("Fallback model succeeded", "model", fb.Model)
		return resp, nil
	}

	return nil, primaryErr
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
