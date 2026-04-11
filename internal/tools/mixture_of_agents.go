package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

func init() {
	Register(&ToolEntry{
		Name:    "mixture_of_agents",
		Toolset: "moa",
		Schema: map[string]any{
			"name":        "mixture_of_agents",
			"description": "Get multiple AI perspectives on a question, then synthesize the best answer. Spawns N agents with different analytical viewpoints and combines their responses.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{
						"type":        "string",
						"description": "The question or problem to analyze from multiple perspectives",
					},
					"num_agents": map[string]any{
						"type":        "integer",
						"description": "Number of agent perspectives to use (default 3, max 5)",
						"default":     3,
					},
				},
				"required": []string{"question"},
			},
		},
		Handler: handleMixtureOfAgents,
		Emoji:   "\U0001f9e0",
	})
}

// agentPerspective defines a system prompt perspective for one MoA agent.
var agentPerspectives = []string{
	"You are a critical analyst. Focus on potential problems, edge cases, risks, and weaknesses. Be skeptical and thorough.",
	"You are a creative problem solver. Think outside the box, consider unconventional approaches, and explore novel angles.",
	"You are a practical engineer. Focus on implementation details, feasibility, efficiency, and real-world constraints.",
	"You are a domain expert. Provide deep technical knowledge, cite best practices, and reference established patterns.",
	"You are a strategic thinker. Consider the big picture, long-term implications, trade-offs, and alignment with goals.",
}

type moaResponse struct {
	Index       int    `json:"index"`
	Perspective string `json:"perspective"`
	Response    string `json:"response"`
	Error       string `json:"error,omitempty"`
	DurationMs  int64  `json:"duration_ms"`
}

func handleMixtureOfAgents(args map[string]any, ctx *ToolContext) string {
	question, _ := args["question"].(string)
	if question == "" {
		return `{"error":"question is required"}`
	}

	numAgents := 3
	if n, ok := args["num_agents"].(float64); ok && n > 0 {
		numAgents = int(n)
	}
	if numAgents > 5 {
		numAgents = 5
	}
	if numAgents < 2 {
		numAgents = 2
	}

	cfg := config.Load()

	// Phase 1: Collect diverse perspectives concurrently
	var wg sync.WaitGroup
	responses := make([]moaResponse, numAgents)
	sem := make(chan struct{}, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			perspective := agentPerspectives[idx%len(agentPerspectives)]
			start := time.Now()

			resp, err := moaCallAgent(cfg, perspective, question)
			result := moaResponse{
				Index:       idx,
				Perspective: perspective[:min(60, len(perspective))] + "...",
				DurationMs:  time.Since(start).Milliseconds(),
			}

			if err != nil {
				result.Error = err.Error()
				slog.Warn("MoA agent failed", "index", idx, "error", err)
			} else {
				result.Response = resp
			}

			responses[idx] = result
		}(i)
	}
	wg.Wait()

	// Phase 2: Synthesize the final answer
	var sb strings.Builder
	sb.WriteString("You are a synthesis expert. Multiple analysts provided different perspectives on a question. ")
	sb.WriteString("Synthesize their responses into a single comprehensive, balanced answer. ")
	sb.WriteString("Highlight areas of agreement and note important disagreements.\n\n")
	sb.WriteString(fmt.Sprintf("Original question: %s\n\n", question))

	successCount := 0
	for i, r := range responses {
		if r.Error != "" {
			continue
		}
		successCount++
		sb.WriteString(fmt.Sprintf("--- Perspective %d ---\n%s\n\n", i+1, r.Response))
	}

	if successCount == 0 {
		return toJSON(map[string]any{
			"error":     "All agent perspectives failed",
			"responses": responses,
		})
	}

	synthesized, err := moaCallAgent(cfg,
		"You are a synthesis expert. Combine multiple perspectives into one clear, comprehensive answer.",
		sb.String(),
	)
	if err != nil {
		// Return raw responses if synthesis fails
		return toJSON(map[string]any{
			"synthesis_error": err.Error(),
			"raw_responses":   responses,
		})
	}

	return toJSON(map[string]any{
		"synthesis":        synthesized,
		"num_perspectives": successCount,
		"perspectives":     responses,
	})
}

func moaCallAgent(cfg *config.Config, systemPrompt, userMessage string) (string, error) {
	client, err := llm.NewClient(cfg)
	if err != nil {
		return "", fmt.Errorf("create client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
		Stream: false,
	}

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("API call: %w", err)
	}

	return resp.Content, nil
}
