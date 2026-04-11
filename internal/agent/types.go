package agent

import (
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// ConversationResult holds the result of a conversation turn.
type ConversationResult struct {
	FinalResponse    string            `json:"final_response"`
	LastReasoning    string            `json:"last_reasoning,omitempty"`
	Messages         []llm.Message     `json:"messages"`
	APICalls         int               `json:"api_calls"`
	Completed        bool              `json:"completed"`
	Partial          bool              `json:"partial"`
	Interrupted      bool              `json:"interrupted"`
	InterruptMessage string            `json:"interrupt_message,omitempty"`
	Model            string            `json:"model"`
	Provider         string            `json:"provider"`
	BaseURL          string            `json:"base_url"`
	InputTokens      int               `json:"input_tokens"`
	OutputTokens     int               `json:"output_tokens"`
	CacheReadTokens  int               `json:"cache_read_tokens"`
	CacheWriteTokens int               `json:"cache_write_tokens"`
	ReasoningTokens  int               `json:"reasoning_tokens"`
	TotalTokens      int               `json:"total_tokens"`
	EstimatedCostUSD float64           `json:"estimated_cost_usd"`
	CostStatus       string            `json:"cost_status"`
	CostSource       string            `json:"cost_source"`
}

// StreamCallbacks holds all callback functions for streaming events.
type StreamCallbacks struct {
	OnStreamDelta    func(text string)
	OnReasoning      func(text string)
	OnThinking       func(msg string)
	OnToolProgress func(toolName, argsPreview string)
	OnToolStart      func(toolName string)
	OnToolComplete   func(toolName string)
	OnStep           func(iteration int, prevTools []string)
	OnStatus         func(msg string)
	OnClarify        func(question string, choices []string) string
}

// AgentOption is a functional option for configuring AIAgent.
type AgentOption func(*AIAgent)

// WithModel sets the model.
func WithModel(model string) AgentOption {
	return func(a *AIAgent) { a.model = model }
}

// WithMaxIterations sets the max iterations.
func WithMaxIterations(n int) AgentOption {
	return func(a *AIAgent) { a.maxIterations = n }
}

// WithPlatform sets the platform identifier.
func WithPlatform(platform string) AgentOption {
	return func(a *AIAgent) { a.platform = platform }
}

// WithSessionID sets the session ID.
func WithSessionID(id string) AgentOption {
	return func(a *AIAgent) { a.sessionID = id }
}

// WithQuietMode suppresses console output.
func WithQuietMode(quiet bool) AgentOption {
	return func(a *AIAgent) { a.quietMode = quiet }
}

// WithCallbacks sets the streaming callbacks.
func WithCallbacks(cb *StreamCallbacks) AgentOption {
	return func(a *AIAgent) { a.callbacks = cb }
}

// WithEnabledToolsets sets the enabled toolsets.
func WithEnabledToolsets(toolsets []string) AgentOption {
	return func(a *AIAgent) { a.enabledToolsets = toolsets }
}

// WithDisabledToolsets sets the disabled toolsets.
func WithDisabledToolsets(toolsets []string) AgentOption {
	return func(a *AIAgent) { a.disabledToolsets = toolsets }
}

// WithBudget sets a shared iteration budget.
func WithBudget(b *IterationBudget) AgentOption {
	return func(a *AIAgent) { a.budget = b }
}

// WithSystemPrompt sets an ephemeral system prompt override.
func WithSystemPrompt(prompt string) AgentOption {
	return func(a *AIAgent) { a.ephemeralSystemPrompt = prompt }
}

// WithSkipContextFiles skips loading context files.
func WithSkipContextFiles(skip bool) AgentOption {
	return func(a *AIAgent) { a.skipContextFiles = skip }
}

// WithSkipMemory skips loading memory.
func WithSkipMemory(skip bool) AgentOption {
	return func(a *AIAgent) { a.skipMemory = skip }
}

// WithPersistSession controls session persistence.
func WithPersistSession(persist bool) AgentOption {
	return func(a *AIAgent) { a.persistSession = persist }
}

// WithBaseURL sets a custom API base URL.
func WithBaseURL(url string) AgentOption {
	return func(a *AIAgent) { a.baseURL = url }
}

// WithAPIKey sets a custom API key.
func WithAPIKey(key string) AgentOption {
	return func(a *AIAgent) { a.apiKey = key }
}

// WithProvider sets the provider name.
func WithProvider(provider string) AgentOption {
	return func(a *AIAgent) { a.provider = provider }
}

// WithAPIMode sets the API mode (openai or anthropic).
func WithAPIMode(mode string) AgentOption {
	return func(a *AIAgent) { a.apiMode = mode }
}

// WithResumeSession resumes a previous session by loading its history.
func WithResumeSession(sessionID string) AgentOption {
	return func(a *AIAgent) { a.resumeSessionID = sessionID }
}

// WithFallbackModels sets the fallback model chain for error recovery.
func WithFallbackModels(models []FallbackModel) AgentOption {
	return func(a *AIAgent) { a.fallbackModels = models }
}

// WithSmartRouter enables smart model routing.
func WithSmartRouter(router *SmartRouter) AgentOption {
	return func(a *AIAgent) { a.smartRouter = router }
}
