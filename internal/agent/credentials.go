package agent

import (
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// CredentialPool manages API credentials for multi-provider scenarios.
type CredentialPool struct {
	mu        sync.RWMutex
	providers map[string][]Credential
}

// Credential holds a single API credential entry.
type Credential struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	BaseURL  string `yaml:"base_url"`
	APIKey   string `yaml:"api_key"`
	Priority int    `yaml:"priority"` // lower number = higher priority
}

// NewCredentialPool creates an empty credential pool.
func NewCredentialPool() *CredentialPool {
	return &CredentialPool{
		providers: make(map[string][]Credential),
	}
}

// AddCredential adds a credential to the pool.
func (p *CredentialPool) AddCredential(c Credential) {
	p.mu.Lock()
	defer p.mu.Unlock()

	provider := normalizeProvider(c.Provider)
	p.providers[provider] = append(p.providers[provider], c)

	// Keep sorted by priority (ascending = highest priority first).
	sort.Slice(p.providers[provider], func(i, j int) bool {
		return p.providers[provider][i].Priority < p.providers[provider][j].Priority
	})
}

// GetBestCredential returns the highest-priority credential for a provider.
// Returns nil if no credential is available for that provider.
func (p *CredentialPool) GetBestCredential(provider string) *Credential {
	p.mu.RLock()
	defer p.mu.RUnlock()

	provider = normalizeProvider(provider)
	creds, ok := p.providers[provider]
	if !ok || len(creds) == 0 {
		return nil
	}

	// Return a copy to avoid races.
	best := creds[0]
	return &best
}

// GetCredentialForModel returns the best credential that can serve a specific model.
// It first looks for credentials with an explicit model match, then falls back
// to the provider's best credential.
func (p *CredentialPool) GetCredentialForModel(provider, model string) *Credential {
	p.mu.RLock()
	defer p.mu.RUnlock()

	provider = normalizeProvider(provider)
	creds, ok := p.providers[provider]
	if !ok || len(creds) == 0 {
		return nil
	}

	// Look for an exact model match.
	for _, c := range creds {
		if c.Model != "" && c.Model == model {
			cred := c
			return &cred
		}
	}

	// Fall back to the best credential for this provider.
	best := creds[0]
	return &best
}

// NewRotatorForProvider creates a CredentialRotator for the given provider
// using all credentials in the pool. The strategy is set from the config's
// credential_pool.strategy field (defaults to round_robin).
func (p *CredentialPool) NewRotatorForProvider(provider string, cfg *config.Config) *CredentialRotator {
	p.mu.RLock()
	defer p.mu.RUnlock()

	provider = normalizeProvider(provider)
	creds := p.providers[provider]

	// Copy to avoid sharing slices.
	copied := make([]Credential, len(creds))
	copy(copied, creds)

	rotator := NewCredentialRotator(copied)
	rotator.Strategy = strategyFromConfig(cfg)
	return rotator
}

// AllProviders returns a sorted list of all providers with credentials.
func (p *CredentialPool) AllProviders() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	providers := make([]string, 0, len(p.providers))
	for name := range p.providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

// LoadFromConfig loads credentials from the config.yaml credentials section.
func (p *CredentialPool) LoadFromConfig(cfg *config.Config) {
	// The main config's top-level provider/api_key is always a credential.
	if cfg.APIKey != "" {
		provider := cfg.Provider
		if provider == "" {
			provider = inferProviderFromBaseURL(cfg.BaseURL)
		}
		p.AddCredential(Credential{
			Provider: provider,
			Model:    cfg.Model,
			BaseURL:  cfg.BaseURL,
			APIKey:   cfg.APIKey,
			Priority: 0, // top-level config is highest priority
		})
	}

	// Load from provider_routing map if present.
	if cfg.ProviderRouting == nil {
		return
	}

	credentials, ok := cfg.ProviderRouting["credentials"]
	if !ok {
		return
	}

	credList, ok := credentials.([]any)
	if !ok {
		return
	}

	for i, item := range credList {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		cred := Credential{
			Priority: i + 1, // lower index = higher priority after the main config
		}

		if v, ok := m["provider"].(string); ok {
			cred.Provider = v
		}
		if v, ok := m["model"].(string); ok {
			cred.Model = v
		}
		if v, ok := m["base_url"].(string); ok {
			cred.BaseURL = v
		}
		if v, ok := m["api_key"].(string); ok {
			cred.APIKey = v
		}
		if v, ok := m["priority"].(int); ok {
			cred.Priority = v
		}

		if cred.APIKey != "" {
			p.AddCredential(cred)
		}
	}
}

// LoadFromEnv loads credentials from well-known environment variables.
func (p *CredentialPool) LoadFromEnv() {
	envMappings := []struct {
		envKey   string
		provider string
		baseURL  string
	}{
		{"OPENROUTER_API_KEY", "openrouter", "https://openrouter.ai/api/v1"},
		{"OPENAI_API_KEY", "openai", "https://api.openai.com/v1"},
		{"ANTHROPIC_API_KEY", "anthropic", "https://api.anthropic.com"},
		{"TOGETHER_API_KEY", "together", "https://api.together.xyz/v1"},
		{"GROQ_API_KEY", "groq", "https://api.groq.com/openai/v1"},
		{"DEEPSEEK_API_KEY", "deepseek", "https://api.deepseek.com/v1"},
		{"MISTRAL_API_KEY", "mistral", "https://api.mistral.ai/v1"},
		{"FIREWORKS_API_KEY", "fireworks", "https://api.fireworks.ai/inference/v1"},
		{"GOOGLE_API_KEY", "google", "https://generativelanguage.googleapis.com/v1beta"},
		{"XAI_API_KEY", "xai", "https://api.x.ai/v1"},
	}

	for _, em := range envMappings {
		apiKey := os.Getenv(em.envKey)
		if apiKey == "" {
			continue
		}

		// Check if this provider is already in the pool from config.
		existing := p.GetBestCredential(em.provider)
		priority := 10 // env vars get lower priority than config
		if existing != nil {
			continue // config-defined credential takes precedence
		}

		p.AddCredential(Credential{
			Provider: em.provider,
			BaseURL:  em.baseURL,
			APIKey:   apiKey,
			Priority: priority,
		})

		slog.Debug("Loaded credential from env", "provider", em.provider, "env", em.envKey)
	}
}

// normalizeProvider lowercases and trims the provider name.
func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// inferProviderFromBaseURL guesses the provider from the API base URL.
func inferProviderFromBaseURL(baseURL string) string {
	lower := strings.ToLower(baseURL)

	switch {
	case strings.Contains(lower, "openrouter"):
		return "openrouter"
	case strings.Contains(lower, "anthropic"):
		return "anthropic"
	case strings.Contains(lower, "openai"):
		return "openai"
	case strings.Contains(lower, "together"):
		return "together"
	case strings.Contains(lower, "groq"):
		return "groq"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "mistral"):
		return "mistral"
	case strings.Contains(lower, "fireworks"):
		return "fireworks"
	case strings.Contains(lower, "x.ai"):
		return "xai"
	default:
		return "custom"
	}
}
