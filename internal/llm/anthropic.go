package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// AnthropicClient implements the Anthropic Messages API.
type AnthropicClient struct {
	httpClient *http.Client
	model      string
	provider   string
	baseURL    string
	apiKey     string
}

// NewAnthropicClient creates a new Anthropic Messages API client.
func NewAnthropicClient(model, baseURL, apiKey, provider string) *AnthropicClient {
	if provider == "" {
		provider = "anthropic"
	}
	// Normalize base URL - remove trailing /v1 if present since we build the full path
	baseURL = strings.TrimSuffix(baseURL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v1")

	return &AnthropicClient{
		httpClient: &http.Client{Timeout: 300 * time.Second},
		model:      model,
		provider:   provider,
		baseURL:    baseURL,
		apiKey:     apiKey,
	}
}

// --- Anthropic API request/response types ---

type anthropicRequest struct {
	Model     string               `json:"model"`
	MaxTokens int                  `json:"max_tokens"`
	System    any                  `json:"system,omitempty"` // string or []anthropicSystemBlock
	Messages  []anthropicMessage   `json:"messages"`
	Tools     []anthropicTool      `json:"tools,omitempty"`
	Stream    bool                 `json:"stream,omitempty"`
}

// anthropicSystemBlock supports cache_control on system prompt blocks.
type anthropicSystemBlock struct {
	Type         string              `json:"type"`
	Text         string              `json:"text"`
	CacheControl *anthropicCacheCtrl `json:"cache_control,omitempty"`
}

type anthropicCacheCtrl struct {
	Type string `json:"type"` // "ephemeral"
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []anthropicContentBlock
}

type anthropicContentBlock struct {
	Type         string              `json:"type"`                    // "text", "tool_use", "tool_result"
	Text         string              `json:"text,omitempty"`          // for "text"
	ID           string              `json:"id,omitempty"`            // for "tool_use"
	Name         string              `json:"name,omitempty"`          // for "tool_use"
	Input        any                 `json:"input,omitempty"`         // for "tool_use" (parsed JSON)
	ToolUseID    string              `json:"tool_use_id,omitempty"`   // for "tool_result"
	Content      any                 `json:"content,omitempty"`       // for "tool_result" - string or blocks
	CacheControl *anthropicCacheCtrl `json:"cache_control,omitempty"` // prompt caching
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema"`
}

type anthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
	Error      *anthropicError         `json:"error,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// --- Streaming event types ---

type anthropicStreamEvent struct {
	Type  string          `json:"type"`
	Index int             `json:"index,omitempty"`
	Delta json.RawMessage `json:"delta,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Message *anthropicResponse `json:"message,omitempty"`
	Usage  *anthropicUsage `json:"usage,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type,omitempty"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

// CreateChatCompletion sends a non-streaming request to the Anthropic Messages API.
func (c *AnthropicClient) CreateChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	apiReq := c.buildAnthropicRequest(req)
	apiReq.Stream = false

	body, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	slog.Debug("Anthropic API request", "url", c.messagesURL(), "model", c.model, "msg_count", len(apiReq.Messages))

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.messagesURL(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %s)", err, truncateStr(string(respBody), 500))
	}

	if apiResp.Error != nil {
		return nil, fmt.Errorf("API error: %s: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	return c.convertResponse(&apiResp), nil
}

// CreateChatCompletionStream sends a streaming request to the Anthropic Messages API.
func (c *AnthropicClient) CreateChatCompletionStream(ctx context.Context, req ChatRequest) (<-chan StreamDelta, <-chan error) {
	deltaCh := make(chan StreamDelta, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(deltaCh)
		defer close(errCh)

		apiReq := c.buildAnthropicRequest(req)
		apiReq.Stream = true

		body, err := json.Marshal(apiReq)
		if err != nil {
			errCh <- fmt.Errorf("marshal request: %w", err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.messagesURL(), bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("create request: %w", err)
			return
		}

		c.setHeaders(httpReq)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errCh <- fmt.Errorf("stream request failed: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			respBody, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(respBody))
			return
		}

		// Parse SSE stream
		c.parseSSEStream(resp.Body, deltaCh, errCh)
	}()

	return deltaCh, errCh
}

func (c *AnthropicClient) parseSSEStream(body io.Reader, deltaCh chan<- StreamDelta, errCh chan<- error) {
	buf := make([]byte, 0, 4096)
	reader := io.Reader(body)
	tmp := make([]byte, 4096)

	// Track tool calls being built
	toolCalls := make(map[int]*ToolCall)
	var currentBlockIndex int

	for {
		n, err := reader.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)

			// Process complete SSE events
			for {
				eventEnd := bytes.Index(buf, []byte("\n\n"))
				if eventEnd == -1 {
					break
				}

				eventData := buf[:eventEnd]
				buf = buf[eventEnd+2:]

				// Parse SSE event
				lines := bytes.Split(eventData, []byte("\n"))
				var eventType string
				var data []byte

				for _, line := range lines {
					if bytes.HasPrefix(line, []byte("event: ")) {
						eventType = string(bytes.TrimPrefix(line, []byte("event: ")))
					} else if bytes.HasPrefix(line, []byte("data: ")) {
						data = bytes.TrimPrefix(line, []byte("data: "))
					}
				}

				if len(data) == 0 {
					continue
				}

				switch eventType {
				case "content_block_start":
					var evt anthropicStreamEvent
					if json.Unmarshal(data, &evt) == nil && evt.ContentBlock != nil {
						currentBlockIndex = evt.Index
						if evt.ContentBlock.Type == "tool_use" {
							toolCalls[currentBlockIndex] = &ToolCall{
								ID:   evt.ContentBlock.ID,
								Type: "function",
								Function: FunctionCall{
									Name: evt.ContentBlock.Name,
								},
							}
						}
					}

				case "content_block_delta":
					var evt anthropicStreamEvent
					if json.Unmarshal(data, &evt) != nil {
						continue
					}

					var delta anthropicDelta
					if json.Unmarshal(evt.Delta, &delta) != nil {
						continue
					}

					switch delta.Type {
					case "text_delta":
						if delta.Text != "" {
							deltaCh <- StreamDelta{Content: delta.Text}
						}
					case "input_json_delta":
						if tc, ok := toolCalls[currentBlockIndex]; ok {
							tc.Function.Arguments += delta.PartialJSON
						}
					case "thinking_delta":
						if delta.Text != "" {
							deltaCh <- StreamDelta{Reasoning: delta.Text}
						}
					}

				case "content_block_stop":

				case "message_delta":
					var evt anthropicStreamEvent
					if json.Unmarshal(data, &evt) == nil {
						// Check for stop reason
						var delta anthropicDelta
						json.Unmarshal(evt.Delta, &delta)
						if delta.StopReason != "" {
							// Emit final tool calls
							var finalCalls []ToolCall
							for _, tc := range toolCalls {
								finalCalls = append(finalCalls, *tc)
							}
							deltaCh <- StreamDelta{Done: true, ToolCalls: finalCalls}
							return
						}
					}

				case "message_stop":
					var finalCalls []ToolCall
					for _, tc := range toolCalls {
						finalCalls = append(finalCalls, *tc)
					}
					deltaCh <- StreamDelta{Done: true, ToolCalls: finalCalls}
					return

				case "error":
					var evt struct {
						Error anthropicError `json:"error"`
					}
					if json.Unmarshal(data, &evt) == nil {
						errCh <- fmt.Errorf("stream error: %s: %s", evt.Error.Type, evt.Error.Message)
					}
					return
				}
			}
		}

		if err == io.EOF {
			// Stream ended - emit final
			var finalCalls []ToolCall
			for _, tc := range toolCalls {
				finalCalls = append(finalCalls, *tc)
			}
			deltaCh <- StreamDelta{Done: true, ToolCalls: finalCalls}
			return
		}
		if err != nil {
			errCh <- fmt.Errorf("stream read error: %w", err)
			return
		}
	}
}

func (c *AnthropicClient) buildAnthropicRequest(req ChatRequest) anthropicRequest {
	apiReq := anthropicRequest{
		Model:     c.model,
		MaxTokens: 8192,
	}

	if req.MaxTokens > 0 {
		apiReq.MaxTokens = req.MaxTokens
	}

	// Convert messages - separate system from conversation
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			// Use cache_control on system prompt for Anthropic prompt caching
			apiReq.System = []anthropicSystemBlock{
				{
					Type:         "text",
					Text:         m.Content,
					CacheControl: &anthropicCacheCtrl{Type: "ephemeral"},
				},
			}

		case "user":
			apiReq.Messages = append(apiReq.Messages, anthropicMessage{
				Role:    "user",
				Content: m.Content,
			})

		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Build content blocks for assistant with tool use
				var blocks []anthropicContentBlock
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{
						Type: "text",
						Text: m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					var input any
					json.Unmarshal([]byte(tc.Function.Arguments), &input)
					if input == nil {
						input = map[string]any{}
					}
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					})
				}
				apiReq.Messages = append(apiReq.Messages, anthropicMessage{
					Role:    "assistant",
					Content: blocks,
				})
			} else {
				apiReq.Messages = append(apiReq.Messages, anthropicMessage{
					Role:    "assistant",
					Content: m.Content,
				})
			}

		case "tool":
			// Anthropic expects tool results as user messages with tool_result blocks
			// Check if the last message is already a user with tool_result blocks - merge
			if len(apiReq.Messages) > 0 {
				lastMsg := &apiReq.Messages[len(apiReq.Messages)-1]
				if lastMsg.Role == "user" {
					// Check if it's already a tool_result array
					if blocks, ok := lastMsg.Content.([]anthropicContentBlock); ok {
						lastMsg.Content = append(blocks, anthropicContentBlock{
							Type:      "tool_result",
							ToolUseID: m.ToolCallID,
							Content:   m.Content,
						})
						continue
					}
				}
			}
			// New user message with tool_result
			apiReq.Messages = append(apiReq.Messages, anthropicMessage{
				Role: "user",
				Content: []anthropicContentBlock{
					{
						Type:      "tool_result",
						ToolUseID: m.ToolCallID,
						Content:   m.Content,
					},
				},
			})
		}
	}

	// Ensure messages alternate user/assistant (Anthropic requirement)
	apiReq.Messages = ensureAlternating(apiReq.Messages)

	// Apply prompt caching: mark the last 3 non-system messages with cache_control.
	// This reduces input costs ~75% by caching the conversation prefix.
	applyMessageCaching(apiReq.Messages)

	// Convert tools to Anthropic format
	for _, t := range req.Tools {
		// Extract from OpenAI tool format
		funcData := t.Function
		tool := anthropicTool{
			Name:        funcData.Name,
			Description: funcData.Description,
			InputSchema: funcData.Parameters,
		}
		apiReq.Tools = append(apiReq.Tools, tool)
	}

	return apiReq
}

// applyMessageCaching applies cache_control breakpoints to the last 3 messages
// in the conversation (Anthropic "system_and_3" strategy). System prompt caching
// is handled separately in buildAnthropicRequest. This caches the rolling
// conversation prefix to reduce input costs ~75%.
func applyMessageCaching(msgs []anthropicMessage) {
	if len(msgs) == 0 {
		return
	}

	cacheCtrl := &anthropicCacheCtrl{Type: "ephemeral"}
	marked := 0

	// Walk backwards, mark the last 3 messages
	for i := len(msgs) - 1; i >= 0 && marked < 3; i-- {
		msg := &msgs[i]

		switch content := msg.Content.(type) {
		case string:
			// Convert string content to block with cache_control
			msg.Content = []anthropicContentBlock{
				{
					Type:         "text",
					Text:         content,
					CacheControl: cacheCtrl,
				},
			}
			marked++

		case []anthropicContentBlock:
			if len(content) > 0 {
				// Apply cache_control to the last block
				content[len(content)-1].CacheControl = cacheCtrl
				msg.Content = content
				marked++
			}
		}
	}
}

// ensureAlternating ensures messages alternate between user and assistant.
// Anthropic requires strict alternation.
func ensureAlternating(msgs []anthropicMessage) []anthropicMessage {
	if len(msgs) == 0 {
		return msgs
	}

	var result []anthropicMessage
	for i, m := range msgs {
		if i > 0 && m.Role == result[len(result)-1].Role {
			// Same role consecutive - merge or insert placeholder
			if m.Role == "user" {
				// Insert empty assistant between consecutive user messages
				result = append(result, anthropicMessage{
					Role:    "assistant",
					Content: "I understand.",
				})
			} else {
				// Insert empty user between consecutive assistant messages
				result = append(result, anthropicMessage{
					Role:    "user",
					Content: "Continue.",
				})
			}
		}
		result = append(result, m)
	}

	// First message must be user
	if len(result) > 0 && result[0].Role != "user" {
		result = append([]anthropicMessage{{Role: "user", Content: "Hello."}}, result...)
	}

	return result
}

func (c *AnthropicClient) convertResponse(resp *anthropicResponse) *ChatResponse {
	result := &ChatResponse{
		Usage: Usage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
			CacheReadTokens:  resp.Usage.CacheReadInputTokens,
			CacheWriteTokens: resp.Usage.CacheCreationInputTokens,
		},
	}

	// Map stop reason
	switch resp.StopReason {
	case "end_turn":
		result.FinishReason = "stop"
	case "tool_use":
		result.FinishReason = "tool_calls"
	case "max_tokens":
		result.FinishReason = "length"
	default:
		result.FinishReason = resp.StopReason
	}

	// Extract content and tool calls from content blocks
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			result.Content += block.Text
		case "tool_use":
			inputJSON, _ := json.Marshal(block.Input)
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: FunctionCall{
					Name:      block.Name,
					Arguments: string(inputJSON),
				},
			})
		case "thinking":
			result.Reasoning += block.Text
		}
	}

	return result
}

func (c *AnthropicClient) messagesURL() string {
	return c.baseURL + "/v1/messages"
}

func (c *AnthropicClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
