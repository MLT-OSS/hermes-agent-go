package llm

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// knownProviderPrefixes lists all recognized provider prefixes for model names.
var knownProviderPrefixes = []string{
	"openrouter",
	"anthropic",
	"openai",
	"google",
	"together",
	"fireworks",
	"deepinfra",
	"groq",
	"mistral",
	"perplexity",
	"cohere",
	"aws",
	"azure",
	"deepseek",
	"meta-llama",
	"meta",
	"huggingface",
	"anyscale",
	"replicate",
	"databricks",
	"cloudflare",
	"lepton",
	"octo",
	"baseten",
	"modal",
	"nvidia",
	"sambanova",
	"cerebras",
	"ai21",
	"aleph-alpha",
	"qwen",
	"zhipu",
	"moonshot",
	"minimax",
	"baichuan",
	"01-ai",
}

// cacheEntry stores a cached ModelMeta along with its expiration time.
type cacheEntry struct {
	meta      ModelMeta
	expiresAt time.Time
}

// modelCache stores dynamically discovered model metadata with TTL.
var modelCache sync.Map

// cacheTTL defines how long cached entries remain valid.
var cacheTTL = 1 * time.Hour

// httpClient is the HTTP client used for API requests. It can be replaced in tests.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// modelsDevBaseURL is the base URL for the models.dev API. It can be replaced in tests.
var modelsDevBaseURL = "https://models.dev/api/models"

// modelsDevResponse represents the JSON response from the models.dev API.
type modelsDevResponse struct {
	ContextLength  int  `json:"context_length"`
	MaxOutput      int  `json:"max_output"`
	SupportsTools  bool `json:"supports_tools"`
	SupportsVision bool `json:"supports_vision"`
}

// StripProviderPrefix splits a model string into its provider prefix and base
// model name. If no known prefix is found, provider is empty and baseModel is
// the original string.
func StripProviderPrefix(model string) (provider, baseModel string) {
	slashIdx := strings.Index(model, "/")
	if slashIdx < 0 {
		return "", model
	}

	prefix := model[:slashIdx]
	rest := model[slashIdx+1:]

	for _, p := range knownProviderPrefixes {
		if strings.EqualFold(prefix, p) {
			return p, rest
		}
	}

	// Unrecognized prefix; return the full string so the caller falls
	// through to other lookup strategies.
	return "", model
}

// FetchModelMetadata queries the models.dev API for metadata about a model.
// On success the result is cached for cacheTTL.
func FetchModelMetadata(modelName string) (*ModelMeta, error) {
	provider, baseModel := StripProviderPrefix(modelName)
	if provider == "" || baseModel == "" {
		return nil, fmt.Errorf("fetchModelMetadata: cannot determine provider/model from %q", modelName)
	}

	url := fmt.Sprintf("%s/%s/%s", modelsDevBaseURL, provider, baseModel)
	slog.Debug("Fetching model metadata", "url", url)

	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetchModelMetadata: HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchModelMetadata: API returned status %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("fetchModelMetadata: reading response body: %w", err)
	}

	var apiResp modelsDevResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("fetchModelMetadata: parsing JSON: %w", err)
	}

	meta := ModelMeta{
		ContextLength:  apiResp.ContextLength,
		MaxOutput:      apiResp.MaxOutput,
		SupportsTools:  apiResp.SupportsTools,
		SupportsVision: apiResp.SupportsVision,
	}

	if meta.ContextLength == 0 {
		meta.ContextLength = 128000
	}
	if meta.MaxOutput == 0 {
		meta.MaxOutput = 8192
	}

	modelCache.Store(modelName, cacheEntry{
		meta:      meta,
		expiresAt: timeNow().Add(cacheTTL),
	})

	return &meta, nil
}

// timeNow is a function variable so tests can override the clock.
var timeNow = time.Now

// getCached looks up a model in the cache. Returns the meta and true if found
// and not expired.
func getCached(model string) (ModelMeta, bool) {
	val, ok := modelCache.Load(model)
	if !ok {
		return ModelMeta{}, false
	}
	entry := val.(cacheEntry)
	if timeNow().After(entry.expiresAt) {
		modelCache.Delete(model)
		return ModelMeta{}, false
	}
	return entry.meta, true
}

// GetModelMeta returns metadata for a model using a resolution chain:
//  1. Exact match in hardcoded KnownModels
//  2. Strip provider prefix and try hardcoded KnownModels
//  3. Check the in-memory cache
//  4. Query the models.dev API
//  5. Fall back to defaults (128K context, 8192 max output)
func GetModelMeta(model string) ModelMeta {
	// 1. Exact match in hardcoded table.
	if meta, ok := KnownModels[model]; ok {
		return meta
	}

	// Try partial match against known models.
	lower := strings.ToLower(model)
	for key, meta := range KnownModels {
		if strings.Contains(lower, strings.ToLower(key)) {
			return meta
		}
	}

	// 2. Strip provider prefix and retry hardcoded lookup.
	_, baseModel := StripProviderPrefix(model)
	if baseModel != model && baseModel != "" {
		for key, meta := range KnownModels {
			_, knownBase := StripProviderPrefix(key)
			if strings.EqualFold(baseModel, knownBase) {
				return meta
			}
		}
	}

	// 3. Check cache.
	if meta, ok := getCached(model); ok {
		return meta
	}

	// 4. Try models.dev API (non-blocking best-effort).
	if meta, err := FetchModelMetadata(model); err == nil {
		return *meta
	}

	// 5. Default for unknown models.
	return ModelMeta{
		ContextLength:  128000,
		MaxOutput:      8192,
		SupportsTools:  true,
		SupportsVision: false,
	}
}
