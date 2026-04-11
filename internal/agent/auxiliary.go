package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// AuxiliaryClient provides secondary LLM clients for tasks like
// vision analysis, web content summarization, and context compression.
type AuxiliaryClient struct {
	visionClient     *llm.Client
	webExtractClient *llm.Client
	summaryClient    *llm.Client
}

// initAuxClient creates an auxiliary LLM client from environment variables.
// envPrefix is used to look up MODEL, API_KEY, and BASE_URL env vars
// (e.g. "AUXILIARY_VISION" looks for AUXILIARY_VISION_MODEL, etc.).
// Falls back to OPENROUTER_API_KEY and the OpenRouter base URL when
// the service-specific vars are not set.
func initAuxClient(envPrefix, provider string) *llm.Client {
	model := os.Getenv(envPrefix + "_MODEL")
	if model == "" {
		return nil
	}

	key := os.Getenv(envPrefix + "_API_KEY")
	if key == "" {
		key = os.Getenv("OPENROUTER_API_KEY")
	}
	if key == "" {
		return nil
	}

	baseURL := os.Getenv(envPrefix + "_BASE_URL")
	if baseURL == "" {
		baseURL = llm.OpenRouterBaseURL
	}

	c, err := llm.NewClientWithParams(model, baseURL, key, provider)
	if err != nil {
		return nil
	}
	slog.Debug("Auxiliary client initialized", "provider", provider, "model", model)
	return c
}

// NewAuxiliaryClient creates auxiliary LLM clients from config.
func NewAuxiliaryClient(cfg *config.Config) *AuxiliaryClient {
	aux := &AuxiliaryClient{}

	aux.visionClient = initAuxClient("AUXILIARY_VISION", "auxiliary-vision")
	aux.webExtractClient = initAuxClient("AUXILIARY_WEB_EXTRACT", "auxiliary-web")

	// Summary client supports an additional config fallback for the model name.
	aux.summaryClient = initAuxClient("AUXILIARY_SUMMARY", "auxiliary-summary")
	if aux.summaryClient == nil && cfg != nil && cfg.Auxiliary.SummaryModel != "" {
		// Env var not set but config specifies a model -- try with
		// the config value and standard API key env vars.
		key := os.Getenv("OPENROUTER_API_KEY")
		if key != "" {
			c, err := llm.NewClientWithParams(cfg.Auxiliary.SummaryModel, llm.OpenRouterBaseURL, key, "auxiliary-summary")
			if err == nil {
				aux.summaryClient = c
				slog.Debug("Auxiliary client initialized from config", "provider", "auxiliary-summary", "model", cfg.Auxiliary.SummaryModel)
			}
		}
	}

	return aux
}

// VisionClient returns the vision auxiliary client, or nil.
func (a *AuxiliaryClient) VisionClient() *llm.Client {
	return a.visionClient
}

// WebExtractClient returns the web extract auxiliary client, or nil.
func (a *AuxiliaryClient) WebExtractClient() *llm.Client {
	return a.webExtractClient
}

// SummaryClient returns the summary auxiliary client, or nil.
func (a *AuxiliaryClient) SummaryClient() *llm.Client {
	return a.summaryClient
}

// Summarize uses the summary/compression auxiliary client to summarize text.
func (a *AuxiliaryClient) Summarize(ctx context.Context, text string, maxWords int) (string, error) {
	client := a.summaryClient
	if client == nil {
		client = a.webExtractClient
	}
	if client == nil {
		return text, nil // No auxiliary client, return original
	}

	prompt := "Summarize the following text concisely"
	if maxWords > 0 {
		prompt += " in under " + strconv.Itoa(maxWords) + " words"
	}
	prompt += ":\n\n" + text

	resp, err := client.CreateChatCompletion(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 2000,
	})
	if err != nil {
		return text, fmt.Errorf("summarize: %w", err)
	}

	return resp.Content, nil
}
