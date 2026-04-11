package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStripProviderPrefix(t *testing.T) {
	tests := []struct {
		model        string
		wantProvider string
		wantBase     string
	}{
		// Known provider prefixes.
		{"openai/gpt-4o", "openai", "gpt-4o"},
		{"anthropic/claude-sonnet-4-20250514", "anthropic", "claude-sonnet-4-20250514"},
		{"google/gemini-2.5-pro", "google", "gemini-2.5-pro"},
		{"together/meta-llama/llama-3-70b", "together", "meta-llama/llama-3-70b"},
		{"fireworks/mixtral-8x7b", "fireworks", "mixtral-8x7b"},
		{"deepinfra/meta-llama/llama-3", "deepinfra", "meta-llama/llama-3"},
		{"groq/llama-3-70b", "groq", "llama-3-70b"},
		{"mistral/mistral-large", "mistral", "mistral-large"},
		{"perplexity/pplx-70b", "perplexity", "pplx-70b"},
		{"cohere/command-r", "cohere", "command-r"},
		{"deepseek/deepseek-chat", "deepseek", "deepseek-chat"},
		{"nvidia/nemotron-4-340b", "nvidia", "nemotron-4-340b"},
		{"cerebras/llama3.1-70b", "cerebras", "llama3.1-70b"},
		{"sambanova/llama-3-70b", "sambanova", "llama-3-70b"},
		{"qwen/qwen-2.5-72b", "qwen", "qwen-2.5-72b"},
		{"ai21/jamba-1.5-large", "ai21", "jamba-1.5-large"},

		// Case-insensitive matching.
		{"OpenAI/gpt-4o", "openai", "gpt-4o"},
		{"GOOGLE/gemini-2.5-pro", "google", "gemini-2.5-pro"},

		// meta-llama is a known prefix.
		{"meta-llama/llama-4-maverick", "meta-llama", "llama-4-maverick"},

		// No prefix.
		{"gpt-4o", "", "gpt-4o"},

		// Unknown prefix — treated as no known provider.
		{"unknownprovider/some-model", "", "unknownprovider/some-model"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			provider, base := StripProviderPrefix(tt.model)
			if provider != tt.wantProvider {
				t.Errorf("provider: got %q, want %q", provider, tt.wantProvider)
			}
			if base != tt.wantBase {
				t.Errorf("base: got %q, want %q", base, tt.wantBase)
			}
		})
	}
}

func TestFetchModelMetadata_Success(t *testing.T) {
	// Set up a mock HTTP server.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/gpt-4o-test" {
			http.NotFound(w, r)
			return
		}
		resp := modelsDevResponse{
			ContextLength:  256000,
			MaxOutput:      32000,
			SupportsTools:  true,
			SupportsVision: true,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Replace the base URL and clean up.
	origURL := modelsDevBaseURL
	modelsDevBaseURL = server.URL
	defer func() { modelsDevBaseURL = origURL }()

	// Clear cache.
	modelCache.Range(func(key, _ any) bool {
		modelCache.Delete(key)
		return true
	})

	meta, err := FetchModelMetadata("openai/gpt-4o-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.ContextLength != 256000 {
		t.Errorf("ContextLength: got %d, want 256000", meta.ContextLength)
	}
	if meta.MaxOutput != 32000 {
		t.Errorf("MaxOutput: got %d, want 32000", meta.MaxOutput)
	}
	if !meta.SupportsTools {
		t.Error("expected SupportsTools=true")
	}
	if !meta.SupportsVision {
		t.Error("expected SupportsVision=true")
	}
}

func TestFetchModelMetadata_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	origURL := modelsDevBaseURL
	modelsDevBaseURL = server.URL
	defer func() { modelsDevBaseURL = origURL }()

	_, err := FetchModelMetadata("openai/nonexistent-model")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestFetchModelMetadata_NoProvider(t *testing.T) {
	_, err := FetchModelMetadata("model-without-prefix")
	if err == nil {
		t.Fatal("expected error for model without provider prefix")
	}
}

func TestFetchModelMetadata_ZeroValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := modelsDevResponse{
			ContextLength:  0,
			MaxOutput:      0,
			SupportsTools:  false,
			SupportsVision: false,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := modelsDevBaseURL
	modelsDevBaseURL = server.URL
	defer func() { modelsDevBaseURL = origURL }()

	modelCache.Range(func(key, _ any) bool {
		modelCache.Delete(key)
		return true
	})

	meta, err := FetchModelMetadata("openai/zero-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should apply defaults for zero values.
	if meta.ContextLength != 128000 {
		t.Errorf("ContextLength: got %d, want 128000 (default)", meta.ContextLength)
	}
	if meta.MaxOutput != 8192 {
		t.Errorf("MaxOutput: got %d, want 8192 (default)", meta.MaxOutput)
	}
}

func TestCacheTTL(t *testing.T) {
	// Clear cache.
	modelCache.Range(func(key, _ any) bool {
		modelCache.Delete(key)
		return true
	})

	now := time.Now()

	// Override timeNow.
	origTimeNow := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = origTimeNow }()

	// Manually cache an entry.
	modelCache.Store("test/cached-model", cacheEntry{
		meta: ModelMeta{
			ContextLength:  99999,
			MaxOutput:      5000,
			SupportsTools:  true,
			SupportsVision: true,
		},
		expiresAt: now.Add(cacheTTL),
	})

	// Should be found while not expired.
	meta, ok := getCached("test/cached-model")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if meta.ContextLength != 99999 {
		t.Errorf("ContextLength: got %d, want 99999", meta.ContextLength)
	}

	// Advance time past TTL.
	timeNow = func() time.Time { return now.Add(cacheTTL + time.Second) }

	_, ok = getCached("test/cached-model")
	if ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestGetModelMeta_ResolutionChain(t *testing.T) {
	// Set up a mock server that returns metadata for a specific model.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/groq/special-model" {
			resp := modelsDevResponse{
				ContextLength:  77777,
				MaxOutput:      4096,
				SupportsTools:  true,
				SupportsVision: false,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	origURL := modelsDevBaseURL
	modelsDevBaseURL = server.URL
	defer func() { modelsDevBaseURL = origURL }()

	// Clear cache.
	modelCache.Range(func(key, _ any) bool {
		modelCache.Delete(key)
		return true
	})

	// Reset timeNow.
	origTimeNow := timeNow
	timeNow = time.Now
	defer func() { timeNow = origTimeNow }()

	tests := []struct {
		name          string
		model         string
		wantContext   int
		wantMaxOutput int
		desc          string
	}{
		{
			name:          "exact hardcoded match",
			model:         "openai/gpt-4o",
			wantContext:   128000,
			wantMaxOutput: 16384,
			desc:          "should match directly from KnownModels",
		},
		{
			name:          "prefix strip to hardcoded",
			model:         "groq/gpt-4o",
			wantContext:   128000,
			wantMaxOutput: 16384,
			desc:          "should strip groq/ and match openai/gpt-4o base",
		},
		{
			name:          "API lookup",
			model:         "groq/special-model",
			wantContext:   77777,
			wantMaxOutput: 4096,
			desc:          "should fetch from models.dev API",
		},
		{
			name:          "fallback default",
			model:         "unknownprovider/nonexistent-model",
			wantContext:   128000,
			wantMaxOutput: 8192,
			desc:          "should return defaults when all lookups fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear cache between subtests.
			modelCache.Range(func(key, _ any) bool {
				modelCache.Delete(key)
				return true
			})

			meta := GetModelMeta(tt.model)
			if meta.ContextLength != tt.wantContext {
				t.Errorf("ContextLength: got %d, want %d (%s)", meta.ContextLength, tt.wantContext, tt.desc)
			}
			if meta.MaxOutput != tt.wantMaxOutput {
				t.Errorf("MaxOutput: got %d, want %d (%s)", meta.MaxOutput, tt.wantMaxOutput, tt.desc)
			}
		})
	}
}

func TestGetModelMeta_CachedResult(t *testing.T) {
	// Clear cache.
	modelCache.Range(func(key, _ any) bool {
		modelCache.Delete(key)
		return true
	})

	origTimeNow := timeNow
	timeNow = time.Now
	defer func() { timeNow = origTimeNow }()

	// Pre-populate cache.
	modelCache.Store("custom/cached-only", cacheEntry{
		meta: ModelMeta{
			ContextLength:  55555,
			MaxOutput:      2222,
			SupportsTools:  false,
			SupportsVision: true,
		},
		expiresAt: time.Now().Add(cacheTTL),
	})

	// Set up a server that always fails, to verify cache is checked first.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	origURL := modelsDevBaseURL
	modelsDevBaseURL = server.URL
	defer func() { modelsDevBaseURL = origURL }()

	meta := GetModelMeta("custom/cached-only")
	if meta.ContextLength != 55555 {
		t.Errorf("ContextLength: got %d, want 55555", meta.ContextLength)
	}
	if meta.MaxOutput != 2222 {
		t.Errorf("MaxOutput: got %d, want 2222", meta.MaxOutput)
	}
}

func TestKnownProviderPrefixes_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range knownProviderPrefixes {
		if seen[p] {
			t.Errorf("duplicate provider prefix: %s", p)
		}
		seen[p] = true
	}
}
