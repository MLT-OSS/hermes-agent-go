package llm

import (
	"encoding/json"
	"net/http"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestAnthropicClientCreation(t *testing.T) {
	c := NewAnthropicClient("claude-opus-4-6", "https://api.anthropic.com", "test-key", "anthropic")
	if c.model != "claude-opus-4-6" {
		t.Errorf("Expected model 'claude-opus-4-6', got '%s'", c.model)
	}
	if c.apiKey != "test-key" {
		t.Errorf("Expected api key 'test-key', got '%s'", c.apiKey)
	}
	// baseURL should have /v1 stripped
	if c.baseURL != "https://api.anthropic.com" {
		t.Errorf("Expected stripped base URL, got '%s'", c.baseURL)
	}
}

func TestAnthropicClientBaseURLNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://api.anthropic.com/v1", "https://api.anthropic.com"},
		{"https://api.anthropic.com/v1/", "https://api.anthropic.com"},
		{"https://custom.api.com", "https://custom.api.com"},
		{"https://custom.api.com/", "https://custom.api.com"},
	}

	for _, tt := range tests {
		c := NewAnthropicClient("model", tt.input, "key", "")
		if c.baseURL != tt.expected {
			t.Errorf("For input '%s': expected '%s', got '%s'", tt.input, tt.expected, c.baseURL)
		}
	}
}

func TestAnthropicMessagesURL(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")
	url := c.messagesURL()
	if url != "https://api.anthropic.com/v1/messages" {
		t.Errorf("Expected messages URL, got '%s'", url)
	}
}

func TestBuildAnthropicRequestBasic(t *testing.T) {
	c := NewAnthropicClient("claude-opus-4-6", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 4096,
	}

	apiReq := c.buildAnthropicRequest(req)

	// System is now an array of blocks with cache_control
	sysBlocks, ok := apiReq.System.([]anthropicSystemBlock)
	if !ok {
		t.Fatalf("Expected system to be []anthropicSystemBlock, got %T", apiReq.System)
	}
	if len(sysBlocks) != 1 || sysBlocks[0].Text != "You are helpful." {
		t.Errorf("Expected system text 'You are helpful.', got %+v", sysBlocks)
	}
	if sysBlocks[0].CacheControl == nil || sysBlocks[0].CacheControl.Type != "ephemeral" {
		t.Error("Expected cache_control ephemeral on system block")
	}
	if apiReq.MaxTokens != 4096 {
		t.Errorf("Expected max_tokens 4096, got %d", apiReq.MaxTokens)
	}
	if len(apiReq.Messages) == 0 {
		t.Error("Expected messages")
	}
	// First message should be user
	if apiReq.Messages[0].Role != "user" {
		t.Errorf("Expected first message role 'user', got '%s'", apiReq.Messages[0].Role)
	}
}

func TestBuildAnthropicRequestToolResults(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "System."},
			{Role: "user", Content: "Do something."},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCall{
					{ID: "tc_1", Type: "function", Function: FunctionCall{Name: "terminal", Arguments: `{"command":"ls"}`}},
				},
			},
			{Role: "tool", Content: `{"stdout":"file.txt"}`, ToolCallID: "tc_1"},
		},
	}

	apiReq := c.buildAnthropicRequest(req)

	// Should have system extracted as cached block
	sysBlocks, ok := apiReq.System.([]anthropicSystemBlock)
	if !ok {
		t.Fatalf("Expected system to be []anthropicSystemBlock, got %T", apiReq.System)
	}
	if len(sysBlocks) != 1 || sysBlocks[0].Text != "System." {
		t.Errorf("Expected system text 'System.', got %+v", sysBlocks)
	}

	// Messages should have user, assistant (with tool_use), user (with tool_result)
	if len(apiReq.Messages) < 3 {
		t.Errorf("Expected at least 3 messages, got %d", len(apiReq.Messages))
	}
}

func TestConvertResponse(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "end_turn",
		Content: []anthropicContentBlock{
			{Type: "text", Text: "Hello!"},
		},
		Usage: anthropicUsage{
			InputTokens:  100,
			OutputTokens: 50,
		},
	}

	result := c.convertResponse(resp)
	if result.Content != "Hello!" {
		t.Errorf("Expected 'Hello!', got '%s'", result.Content)
	}
	if result.FinishReason != "stop" {
		t.Errorf("Expected 'stop', got '%s'", result.FinishReason)
	}
	if result.Usage.PromptTokens != 100 {
		t.Errorf("Expected 100 prompt tokens, got %d", result.Usage.PromptTokens)
	}
}

func TestConvertResponseToolUse(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "tool_use",
		Content: []anthropicContentBlock{
			{Type: "text", Text: "Let me check."},
			{Type: "tool_use", ID: "tc_1", Name: "terminal", Input: map[string]any{"command": "ls"}},
		},
		Usage: anthropicUsage{InputTokens: 200, OutputTokens: 100},
	}

	result := c.convertResponse(resp)
	if result.FinishReason != "tool_calls" {
		t.Errorf("Expected 'tool_calls', got '%s'", result.FinishReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "terminal" {
		t.Errorf("Expected tool name 'terminal', got '%s'", result.ToolCalls[0].Function.Name)
	}

	// Arguments should be valid JSON
	var args map[string]any
	if err := json.Unmarshal([]byte(result.ToolCalls[0].Function.Arguments), &args); err != nil {
		t.Errorf("Tool arguments not valid JSON: %v", err)
	}
}

func TestEnsureAlternating(t *testing.T) {
	// Two consecutive user messages
	msgs := []anthropicMessage{
		{Role: "user", Content: "First"},
		{Role: "user", Content: "Second"},
	}
	result := ensureAlternating(msgs)
	if len(result) != 3 {
		t.Errorf("Expected 3 messages (inserted assistant), got %d", len(result))
	}
	if result[1].Role != "assistant" {
		t.Errorf("Expected inserted assistant, got '%s'", result[1].Role)
	}

	// First message is assistant
	msgs = []anthropicMessage{
		{Role: "assistant", Content: "Hi"},
	}
	result = ensureAlternating(msgs)
	if result[0].Role != "user" {
		t.Errorf("Expected user prepended, got '%s'", result[0].Role)
	}
}

func TestEnsureAlternating_Empty(t *testing.T) {
	result := ensureAlternating(nil)
	if len(result) != 0 {
		t.Errorf("Expected empty result for nil input, got %d", len(result))
	}
}

func TestEnsureAlternating_SingleUser(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "Hello"},
	}
	result := ensureAlternating(msgs)
	if len(result) != 1 {
		t.Errorf("Expected 1 message, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Error("Expected user role")
	}
}

func TestEnsureAlternating_ConsecutiveAssistant(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "Start"},
		{Role: "assistant", Content: "First response"},
		{Role: "assistant", Content: "Second response"},
	}
	result := ensureAlternating(msgs)
	// Should insert a user message between consecutive assistants
	if len(result) != 4 {
		t.Errorf("Expected 4 messages, got %d", len(result))
	}
	if result[2].Role != "user" {
		t.Errorf("Expected inserted user between assistants, got '%s'", result[2].Role)
	}
}

func TestEnsureAlternating_ProperlyAlternating(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi"},
		{Role: "user", Content: "How are you?"},
		{Role: "assistant", Content: "Good!"},
	}
	result := ensureAlternating(msgs)
	if len(result) != 4 {
		t.Errorf("Expected 4 messages (no changes needed), got %d", len(result))
	}
}

func TestEnsureAlternating_MultipleConsecutiveUser(t *testing.T) {
	msgs := []anthropicMessage{
		{Role: "user", Content: "1"},
		{Role: "user", Content: "2"},
		{Role: "user", Content: "3"},
	}
	result := ensureAlternating(msgs)
	// Each pair of consecutive users should get an assistant inserted
	// 1 user, assistant, 2 user, assistant, 3 user = 5
	if len(result) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(result))
	}
	for i, m := range result {
		expectedRole := "user"
		if i%2 == 1 {
			expectedRole = "assistant"
		}
		if m.Role != expectedRole {
			t.Errorf("Message %d: expected role '%s', got '%s'", i, expectedRole, m.Role)
		}
	}
}

func TestConvertResponse_ThinkingBlock(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "end_turn",
		Content: []anthropicContentBlock{
			{Type: "thinking", Text: "Let me think about this..."},
			{Type: "text", Text: "The answer is 42."},
		},
		Usage: anthropicUsage{InputTokens: 50, OutputTokens: 30},
	}

	result := c.convertResponse(resp)
	if result.Reasoning != "Let me think about this..." {
		t.Errorf("Expected reasoning 'Let me think about this...', got '%s'", result.Reasoning)
	}
	if result.Content != "The answer is 42." {
		t.Errorf("Expected content 'The answer is 42.', got '%s'", result.Content)
	}
}

func TestConvertResponse_MaxTokens(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "max_tokens",
		Content: []anthropicContentBlock{
			{Type: "text", Text: "Partial response..."},
		},
		Usage: anthropicUsage{InputTokens: 100, OutputTokens: 1000},
	}

	result := c.convertResponse(resp)
	if result.FinishReason != "length" {
		t.Errorf("Expected 'length', got '%s'", result.FinishReason)
	}
}

func TestConvertResponse_CacheUsage(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "end_turn",
		Content: []anthropicContentBlock{
			{Type: "text", Text: "Cached response"},
		},
		Usage: anthropicUsage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 80,
			CacheReadInputTokens:     200,
		},
	}

	result := c.convertResponse(resp)
	if result.Usage.CacheReadTokens != 200 {
		t.Errorf("Expected 200 cache read tokens, got %d", result.Usage.CacheReadTokens)
	}
	if result.Usage.CacheWriteTokens != 80 {
		t.Errorf("Expected 80 cache write tokens, got %d", result.Usage.CacheWriteTokens)
	}
}

func TestAnthropicClient_MessagesURL(t *testing.T) {
	tests := []struct {
		baseURL  string
		expected string
	}{
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		{"https://custom.proxy.com", "https://custom.proxy.com/v1/messages"},
	}

	for _, tt := range tests {
		c := NewAnthropicClient("model", tt.baseURL, "key", "")
		url := c.messagesURL()
		if url != tt.expected {
			t.Errorf("For baseURL '%s': expected '%s', got '%s'", tt.baseURL, tt.expected, url)
		}
	}
}

func TestAnthropicClient_SetHeaders(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "test-api-key", "")

	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	c.setHeaders(req)

	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("Expected Content-Type 'application/json', got '%s'", req.Header.Get("Content-Type"))
	}
	if req.Header.Get("x-api-key") != "test-api-key" {
		t.Errorf("Expected x-api-key 'test-api-key', got '%s'", req.Header.Get("x-api-key"))
	}
	if req.Header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("Expected anthropic-version '2023-06-01', got '%s'", req.Header.Get("anthropic-version"))
	}
}

func TestTruncateStr(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}

	for _, tt := range tests {
		result := truncateStr(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateStr(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestBuildAnthropicRequest_DefaultMaxTokens(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
	}

	apiReq := c.buildAnthropicRequest(req)
	if apiReq.MaxTokens != 8192 {
		t.Errorf("Expected default max_tokens 8192, got %d", apiReq.MaxTokens)
	}
}

func TestBuildAnthropicRequest_CustomMaxTokens(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 16000,
	}

	apiReq := c.buildAnthropicRequest(req)
	if apiReq.MaxTokens != 16000 {
		t.Errorf("Expected max_tokens 16000, got %d", apiReq.MaxTokens)
	}
}

func TestBuildAnthropicRequest_ToolConversion(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	tool := openai.Tool{
		Type: "function",
		Function: &openai.FunctionDefinition{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters:  map[string]any{"type": "object"},
		},
	}

	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Use the tool"},
		},
		Tools: []openai.Tool{tool},
	}

	apiReq := c.buildAnthropicRequest(req)
	if len(apiReq.Tools) != 1 {
		t.Fatalf("Expected 1 tool, got %d", len(apiReq.Tools))
	}
	if apiReq.Tools[0].Name != "test_tool" {
		t.Errorf("Expected tool name 'test_tool', got '%s'", apiReq.Tools[0].Name)
	}
	if apiReq.Tools[0].Description != "A test tool" {
		t.Errorf("Expected description 'A test tool', got '%s'", apiReq.Tools[0].Description)
	}
}

func TestAnthropicClient_DefaultProvider(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")
	if c.provider != "anthropic" {
		t.Errorf("Expected default provider 'anthropic', got '%s'", c.provider)
	}
}

func TestBuildAnthropicRequest_MultipleToolResults(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "system", Content: "System."},
			{Role: "user", Content: "Do multiple things."},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{ID: "tc_1", Type: "function", Function: FunctionCall{Name: "tool1", Arguments: `{"key":"val1"}`}},
					{ID: "tc_2", Type: "function", Function: FunctionCall{Name: "tool2", Arguments: `{"key":"val2"}`}},
				},
			},
			{Role: "tool", Content: "result1", ToolCallID: "tc_1"},
			{Role: "tool", Content: "result2", ToolCallID: "tc_2"},
		},
	}

	apiReq := c.buildAnthropicRequest(req)

	// System should be extracted
	if apiReq.System == nil {
		t.Error("Expected system to be set")
	}

	// Should have: user, assistant(with blocks), user(with tool_results merged)
	if len(apiReq.Messages) < 3 {
		t.Errorf("Expected at least 3 messages, got %d", len(apiReq.Messages))
	}
}

func TestBuildAnthropicRequest_AssistantNoToolCalls(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi there!"},
			{Role: "user", Content: "How are you?"},
		},
	}

	apiReq := c.buildAnthropicRequest(req)

	// Simple assistant message should be a string content
	if len(apiReq.Messages) != 3 {
		t.Errorf("Expected 3 messages, got %d", len(apiReq.Messages))
	}
	if apiReq.Messages[1].Role != "assistant" {
		t.Errorf("Expected assistant role, got '%s'", apiReq.Messages[1].Role)
	}
}

func TestBuildAnthropicRequest_AssistantWithTextAndToolUse(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	req := ChatRequest{
		Messages: []Message{
			{Role: "user", Content: "Help me"},
			{
				Role:    "assistant",
				Content: "Let me check.",
				ToolCalls: []ToolCall{
					{ID: "tc_1", Type: "function", Function: FunctionCall{Name: "search", Arguments: `{"query":"test"}`}},
				},
			},
		},
	}

	apiReq := c.buildAnthropicRequest(req)

	// Assistant message should have both text and tool_use blocks
	if len(apiReq.Messages) < 2 {
		t.Errorf("Expected at least 2 messages, got %d", len(apiReq.Messages))
	}
	assistantMsg := apiReq.Messages[1]
	blocks, ok := assistantMsg.Content.([]anthropicContentBlock)
	if !ok {
		t.Fatal("Expected assistant content to be []anthropicContentBlock")
	}
	if len(blocks) != 2 {
		t.Errorf("Expected 2 content blocks (text + tool_use), got %d", len(blocks))
	}
	if blocks[0].Type != "text" {
		t.Errorf("Expected first block type 'text', got '%s'", blocks[0].Type)
	}
	if blocks[1].Type != "tool_use" {
		t.Errorf("Expected second block type 'tool_use', got '%s'", blocks[1].Type)
	}
}

func TestConvertResponse_MultipleToolCalls(t *testing.T) {
	c := NewAnthropicClient("model", "https://api.anthropic.com", "key", "")

	resp := &anthropicResponse{
		StopReason: "tool_use",
		Content: []anthropicContentBlock{
			{Type: "tool_use", ID: "tc_1", Name: "read_file", Input: map[string]any{"path": "/test.txt"}},
			{Type: "tool_use", ID: "tc_2", Name: "terminal", Input: map[string]any{"command": "ls"}},
		},
		Usage: anthropicUsage{InputTokens: 100, OutputTokens: 50},
	}

	result := c.convertResponse(resp)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("Expected first tool 'read_file', got '%s'", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "terminal" {
		t.Errorf("Expected second tool 'terminal', got '%s'", result.ToolCalls[1].Function.Name)
	}
}
