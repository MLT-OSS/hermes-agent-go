package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	openai "github.com/sashabaranov/go-openai"
)

// APIMode determines which API protocol to use.
type APIMode string

const (
	APIModeOpenAI    APIMode = "openai"
	APIModeAnthropic APIMode = "anthropic"
)

// Client wraps an LLM API client supporting both OpenAI and Anthropic protocols.
type Client struct {
	inner     *openai.Client      // OpenAI-compatible client
	anthropic *AnthropicClient    // Anthropic Messages API client
	apiMode   APIMode
	model     string
	provider  string
	baseURL   string
	apiKey    string
}

// NewClient creates a new LLM client from configuration.
func NewClient(cfg *config.Config) (*Client, error) {
	provider, baseURL, apiKey := ResolveProvider(cfg)
	if apiKey == "" {
		return nil, fmt.Errorf("no API key configured. Run 'hermes setup' or set an API key in %s/.env", config.DisplayHermesHome())
	}

	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4-20250514"
	}

	apiMode := detectAPIMode(cfg.APIMode, provider, baseURL)

	return newClientInternal(model, baseURL, apiKey, provider, apiMode)
}

// NewClientWithParams creates a client with explicit parameters.
func NewClientWithParams(model, baseURL, apiKey, provider string) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	apiMode := detectAPIMode("", provider, baseURL)
	return newClientInternal(model, baseURL, apiKey, provider, apiMode)
}

// NewClientWithMode creates a client with explicit API mode.
func NewClientWithMode(model, baseURL, apiKey, provider string, mode APIMode) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required")
	}
	return newClientInternal(model, baseURL, apiKey, provider, mode)
}

func newClientInternal(model, baseURL, apiKey, provider string, mode APIMode) (*Client, error) {
	c := &Client{
		model:    model,
		provider: provider,
		baseURL:  baseURL,
		apiKey:   apiKey,
		apiMode:  mode,
	}

	switch mode {
	case APIModeAnthropic:
		c.anthropic = NewAnthropicClient(model, baseURL, apiKey, provider)
		slog.Info("Using Anthropic Messages API", "model", model, "baseURL", baseURL)
	default:
		clientCfg := openai.DefaultConfig(apiKey)
		clientCfg.BaseURL = baseURL
		c.inner = openai.NewClientWithConfig(clientCfg)
		slog.Info("Using OpenAI-compatible API", "model", model, "baseURL", baseURL)
	}

	return c, nil
}

func detectAPIMode(explicit, provider, baseURL string) APIMode {
	// Explicit config takes priority
	switch strings.ToLower(explicit) {
	case "anthropic", "anthropic_messages":
		return APIModeAnthropic
	case "openai", "chat_completions", "":
		// fall through to auto-detect
	default:
		// fall through
	}

	// Auto-detect from provider
	if provider == "anthropic" {
		return APIModeAnthropic
	}

	// Auto-detect from base URL
	lower := strings.ToLower(baseURL)
	if strings.Contains(lower, "anthropic.com") {
		return APIModeAnthropic
	}

	return APIModeOpenAI
}

// Model returns the model name.
func (c *Client) Model() string { return c.model }

// Provider returns the provider name.
func (c *Client) Provider() string { return c.provider }

// BaseURL returns the API base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// APIMode returns the API mode.
func (c *Client) APIMode() APIMode { return c.apiMode }

// ChatRequest represents a chat completion request.
type ChatRequest struct {
	Messages       []Message
	Tools          []openai.Tool
	MaxTokens      int
	Temperature    *float32
	Stream         bool
	ReasoningLevel string // "xhigh", "high", "medium", "low", "minimal", ""
}

// Message represents a chat message in OpenAI format.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ToolName         string     `json:"tool_name,omitempty"`
	FinishReason     string     `json:"finish_reason,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ImageURLs        []string   `json:"image_urls,omitempty"` // multimodal: image data URLs or HTTP URLs
}

// ToolCall represents a tool call from the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents the function details in a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatResponse represents a chat completion response.
type ChatResponse struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Reasoning    string
	Usage        Usage
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
}

// StreamDelta represents a streaming chunk.
type StreamDelta struct {
	Content   string
	ToolCalls []ToolCall
	Reasoning string
	Done      bool
}

// CreateChatCompletion sends a non-streaming chat completion request.
func (c *Client) CreateChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if c.apiMode == APIModeAnthropic {
		return c.anthropic.CreateChatCompletion(ctx, req)
	}
	return c.openaiChatCompletion(ctx, req)
}

// CreateChatCompletionStream sends a streaming chat completion request.
func (c *Client) CreateChatCompletionStream(ctx context.Context, req ChatRequest) (<-chan StreamDelta, <-chan error) {
	if c.apiMode == APIModeAnthropic {
		return c.anthropic.CreateChatCompletionStream(ctx, req)
	}
	return c.openaiChatCompletionStream(ctx, req)
}

func (c *Client) openaiChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	apiReq := c.buildOpenAIRequest(req)
	apiReq.Stream = false

	resp, err := c.inner.CreateChatCompletion(ctx, apiReq)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return &ChatResponse{FinishReason: "stop"}, nil
	}

	choice := resp.Choices[0]
	result := &ChatResponse{
		Content:      choice.Message.Content,
		FinishReason: string(choice.FinishReason),
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	for _, tc := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:   tc.ID,
			Type: string(tc.Type),
			Function: FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	return result, nil
}

func (c *Client) openaiChatCompletionStream(ctx context.Context, req ChatRequest) (<-chan StreamDelta, <-chan error) {
	deltaCh := make(chan StreamDelta, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(deltaCh)
		defer close(errCh)

		apiReq := c.buildOpenAIRequest(req)
		apiReq.Stream = true

		stream, err := c.inner.CreateChatCompletionStream(ctx, apiReq)
		if err != nil {
			errCh <- fmt.Errorf("stream creation failed: %w", err)
			return
		}
		defer stream.Close()

		toolCalls := make(map[int]*ToolCall)

		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				var finalCalls []ToolCall
				for _, tc := range toolCalls {
					finalCalls = append(finalCalls, *tc)
				}
				deltaCh <- StreamDelta{Done: true, ToolCalls: finalCalls}
				return
			}
			if err != nil {
				errCh <- err
				return
			}

			if len(resp.Choices) == 0 {
				continue
			}

			delta := resp.Choices[0].Delta

			if delta.Content != "" {
				deltaCh <- StreamDelta{Content: delta.Content}
			}

			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				existing, ok := toolCalls[idx]
				if !ok {
					existing = &ToolCall{
						ID:   tc.ID,
						Type: string(tc.Type),
						Function: FunctionCall{
							Name: tc.Function.Name,
						},
					}
					toolCalls[idx] = existing
				}
				if tc.Function.Name != "" {
					existing.Function.Name = tc.Function.Name
				}
				existing.Function.Arguments += tc.Function.Arguments
			}
		}
	}()

	return deltaCh, errCh
}

func (c *Client) buildOpenAIRequest(req ChatRequest) openai.ChatCompletionRequest {
	var msgs []openai.ChatCompletionMessage
	for _, m := range req.Messages {
		msg := openai.ChatCompletionMessage{
			Role: m.Role,
		}

		// Multimodal: if ImageURLs are present, use MultiContent parts
		if len(m.ImageURLs) > 0 {
			var parts []openai.ChatMessagePart
			if m.Content != "" {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeText,
					Text: m.Content,
				})
			}
			for _, imgURL := range m.ImageURLs {
				parts = append(parts, openai.ChatMessagePart{
					Type: openai.ChatMessagePartTypeImageURL,
					ImageURL: &openai.ChatMessageImageURL{
						URL:    imgURL,
						Detail: openai.ImageURLDetailAuto,
					},
				})
			}
			msg.MultiContent = parts
		} else {
			msg.Content = m.Content
		}

		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}

		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolType(tc.Type),
				Function: openai.FunctionCall{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
		}

		msgs = append(msgs, msg)
	}

	apiReq := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: msgs,
		Tools:    req.Tools,
	}

	if req.MaxTokens > 0 {
		apiReq.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		apiReq.Temperature = *req.Temperature
	}

	return apiReq
}

// ParseToolArgs parses a JSON string of tool arguments into a map.
func ParseToolArgs(argsJSON string) (map[string]any, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	return args, nil
}

func init() {
	if os.Getenv("HERMES_DEBUG") != "" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
}