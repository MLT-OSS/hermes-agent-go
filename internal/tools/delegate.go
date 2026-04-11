package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
	openai "github.com/sashabaranov/go-openai"
)

const (
	// maxDelegationDepth is the maximum nesting depth for delegated tasks.
	maxDelegationDepth = 2

	// depthContextKey is used to track delegation depth across sub-agents.
	depthContextKey = "delegation_depth"
)

// blockedToolsInChildren lists tools that child agents must never invoke.
var blockedToolsInChildren = map[string]bool{
	"delegate_task": true, // no recursive delegation
	"clarify":       true, // children cannot ask the user questions
	"memory":        true, // children should not modify persistent memory
}

func init() {
	Register(&ToolEntry{
		Name:    "delegate_task",
		Toolset: "delegation",
		Schema: map[string]any{
			"name":        "delegate_task",
			"description": "Delegate tasks to sub-agents that run concurrently. Each task gets its own AI agent with independent context. Maximum 3 concurrent tasks. Max nesting depth: 2.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tasks": map[string]any{
						"type":        "array",
						"description": "Array of tasks to delegate",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"goal": map[string]any{
									"type":        "string",
									"description": "The task goal/instruction for the sub-agent",
								},
								"model": map[string]any{
									"type":        "string",
									"description": "Optional model override for this task",
								},
								"provider": map[string]any{
									"type":        "string",
									"description": "Optional provider override (for credential pool routing)",
								},
							},
							"required": []string{"goal"},
						},
					},
				},
				"required": []string{"tasks"},
			},
		},
		Handler: handleDelegateTask,
		Emoji:   "\U0001f91d",
	})
}

// delegateResult holds the result from a single sub-agent task.
type delegateResult struct {
	Index    int    `json:"index"`
	Goal     string `json:"goal"`
	Model    string `json:"model"`
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

func handleDelegateTask(args map[string]any, ctx *ToolContext) string {
	// Check delegation depth
	currentDepth := 0
	if ctx != nil && ctx.Extra != nil {
		if d, ok := ctx.Extra[depthContextKey].(int); ok {
			currentDepth = d
		}
	}
	if currentDepth >= maxDelegationDepth {
		return toJSON(map[string]any{
			"error":         "Maximum delegation depth reached",
			"current_depth": currentDepth,
			"max_depth":     maxDelegationDepth,
			"hint":          "Sub-agents cannot delegate further. Complete the task directly.",
		})
	}

	tasksRaw, ok := args["tasks"].([]any)
	if !ok || len(tasksRaw) == 0 {
		return `{"error":"tasks array is required and must not be empty"}`
	}

	if len(tasksRaw) > 5 {
		return `{"error":"Maximum 5 tasks allowed per delegation"}`
	}

	// Parse tasks
	type taskSpec struct {
		Goal     string
		Model    string
		Provider string
	}
	var tasks []taskSpec
	for _, t := range tasksRaw {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		goal, _ := tm["goal"].(string)
		if goal == "" {
			continue
		}
		model, _ := tm["model"].(string)
		provider, _ := tm["provider"].(string)
		tasks = append(tasks, taskSpec{Goal: goal, Model: model, Provider: provider})
	}

	if len(tasks) == 0 {
		return `{"error":"No valid tasks found. Each task must have a 'goal' field."}`
	}

	cfg := config.Load()
	defaultModel := cfg.Model
	if cfg.Delegation.Model != "" {
		defaultModel = cfg.Delegation.Model
	}

	childDepth := currentDepth + 1

	// Concurrency limiter: max 3 concurrent
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	results := make([]delegateResult, len(tasks))

	// Progress channel for parent notification
	progressCh := make(chan string, len(tasks)*2)
	go func() {
		for msg := range progressCh {
			slog.Info("Delegate progress", "depth", childDepth, "message", msg)
		}
	}()

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, t taskSpec) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			start := time.Now()
			model := t.Model
			if model == "" {
				model = defaultModel
			}

			result := delegateResult{
				Index: idx,
				Goal:  t.Goal,
				Model: model,
			}

			progressCh <- fmt.Sprintf("Task %d started: %s", idx, truncateOutput(t.Goal, 80))

			// Resolve credentials from provider pool
			apiKey := ""
			baseURL := ""
			provider := t.Provider
			if provider != "" {
				apiKey, baseURL = resolveCredentialPool(provider, cfg)
			}

			resp, err := runSubAgentWithOptions(t.Goal, model, provider, apiKey, baseURL, cfg, childDepth)
			if err != nil {
				result.Error = err.Error()
				slog.Warn("Delegate task failed", "index", idx, "error", err)
				progressCh <- fmt.Sprintf("Task %d failed: %s", idx, err.Error())
			} else {
				result.Response = resp
				progressCh <- fmt.Sprintf("Task %d completed (%s)", idx, time.Since(start).Round(time.Millisecond))
			}

			result.Duration = time.Since(start).Round(time.Millisecond).String()
			results[idx] = result
		}(i, task)
	}

	wg.Wait()
	close(progressCh)

	// Build summary
	successCount := 0
	for _, r := range results {
		if r.Error == "" {
			successCount++
		}
	}

	return toJSON(map[string]any{
		"results":       results,
		"total_tasks":   len(tasks),
		"success_count": successCount,
		"failed_count":  len(tasks) - successCount,
		"depth":         childDepth,
	})
}

// runSubAgentWithOptions creates an LLM client with full control over provider/credentials
// and runs a multi-turn agent loop with tool calling. It enforces the blocked tools list for children.
func runSubAgentWithOptions(goal, model, provider, apiKey, baseURL string, cfg *config.Config, depth int) (string, error) {
	var client *llm.Client
	var err error

	if apiKey != "" && baseURL != "" {
		client, err = llm.NewClientWithParams(model, baseURL, apiKey, provider)
	} else {
		client, err = llm.NewClient(cfg)
		if err == nil && model != "" && model != cfg.Model {
			client, err = llm.NewClientWithParams(model, "", "", "")
		}
	}
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build allowed tool names (all tools minus blocked ones)
	allNames := Registry().GetAllToolNames()
	allowedNames := make(map[string]bool)
	for _, name := range allNames {
		if !blockedToolsInChildren[name] {
			allowedNames[name] = true
		}
	}

	// Get tool definitions in OpenAI format
	toolDefs := Registry().GetDefinitions(allowedNames, true)
	var tools []openai.Tool
	for _, td := range toolDefs {
		fnDef, ok := td["function"].(map[string]any)
		if !ok {
			continue
		}
		name, _ := fnDef["name"].(string)
		desc, _ := fnDef["description"].(string)
		params, _ := fnDef["parameters"]

		paramsJSON, _ := json.Marshal(params)

		tools = append(tools, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: desc,
				Parameters:  json.RawMessage(paramsJSON),
			},
		})
	}

	// Build system prompt
	var blockedList []string
	for t := range blockedToolsInChildren {
		blockedList = append(blockedList, t)
	}

	systemPrompt := fmt.Sprintf(
		"You are a focused sub-agent (depth %d/%d). Complete the given task concisely and accurately. "+
			"Do not ask for clarification - do your best with the information provided. "+
			"You MUST NOT use these tools: %v",
		depth, maxDelegationDepth, blockedList,
	)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: goal},
	}

	// ReAct loop: iterate until text response or max iterations
	const maxIterations = 5
	for i := 0; i < maxIterations; i++ {
		req := llm.ChatRequest{
			Messages: messages,
			Tools:    tools,
			Stream:   false,
		}

		resp, err := client.CreateChatCompletion(ctx, req)
		if err != nil {
			return "", fmt.Errorf("API call (iteration %d): %w", i, err)
		}

		// If no tool calls, return the text response
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Append assistant message with tool calls
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call and append results
		for _, tc := range resp.ToolCalls {
			// Block forbidden tools at runtime
			if blockedToolsInChildren[tc.Function.Name] {
				messages = append(messages, llm.Message{
					Role:       "tool",
					Content:    `{"error":"tool not available for sub-agents"}`,
					ToolCallID: tc.ID,
				})
				continue
			}

			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{"raw": tc.Function.Arguments}
			}

			toolCtx := &ToolContext{
				SessionID: fmt.Sprintf("delegate-%d", depth),
				Platform:  "delegate",
				Extra:     map[string]any{depthContextKey: depth},
			}

			result := Registry().Dispatch(tc.Function.Name, args, toolCtx)

			// Truncate very large results
			if len(result) > 8000 {
				result = result[:8000] + "\n... (truncated)"
			}

			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	// If we hit max iterations, return whatever we have
	return "Sub-agent reached maximum iterations without a final response.", nil
}

// resolveCredentialPool looks up API credentials for a named provider
// from the config's provider_routing section.
func resolveCredentialPool(providerName string, cfg *config.Config) (apiKey, baseURL string) {
	if cfg.ProviderRouting == nil {
		return "", ""
	}

	providerCfg, ok := cfg.ProviderRouting[providerName]
	if !ok {
		return "", ""
	}

	provMap, ok := providerCfg.(map[string]any)
	if !ok {
		return "", ""
	}

	if key, ok := provMap["api_key"].(string); ok {
		apiKey = key
	}
	if url, ok := provMap["base_url"].(string); ok {
		baseURL = url
	}

	return apiKey, baseURL
}
