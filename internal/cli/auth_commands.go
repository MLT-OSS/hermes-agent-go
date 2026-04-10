package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// ValidateAPIKey tests an API key against its provider's endpoint.
func ValidateAPIKey(providerID, apiKey string) error {
	provider := GetProvider(providerID)
	if provider == nil {
		return fmt.Errorf("unknown provider: %s", providerID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	testURL := provider.BaseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	if strings.Contains(provider.BaseURL, "anthropic") {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("provider server error (HTTP %d)", resp.StatusCode)
	}
	return nil
}

// SaveAPIKey writes an API key to the .env file.
func SaveAPIKey(providerID, apiKey string) error {
	provider := GetProvider(providerID)
	if provider == nil {
		return fmt.Errorf("unknown provider: %s", providerID)
	}

	envPath := filepath.Join(config.HermesHome(), ".env")
	lines, _ := readEnvLines(envPath)

	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, provider.EnvKey+"=") {
			lines[i] = provider.EnvKey + "=" + apiKey
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, provider.EnvKey+"="+apiKey)
	}

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(envPath, []byte(content), 0600)
}

// RemoveAPIKey removes an API key from the .env file.
func RemoveAPIKey(providerID string) error {
	provider := GetProvider(providerID)
	if provider == nil {
		return fmt.Errorf("unknown provider: %s", providerID)
	}

	envPath := filepath.Join(config.HermesHome(), ".env")
	lines, err := readEnvLines(envPath)
	if err != nil {
		return nil
	}

	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, provider.EnvKey+"=") {
			newLines = append(newLines, line)
		}
	}

	content := strings.Join(newLines, "\n") + "\n"
	return os.WriteFile(envPath, []byte(content), 0600)
}

// ProviderAuthStatus wraps a provider with its auth state.
type ProviderAuthStatus struct {
	Provider   ProviderInfo
	Configured bool
	Valid      bool
}

// AuthStatus returns a summary of all configured providers.
func AuthStatus() []ProviderAuthStatus {
	var result []ProviderAuthStatus
	for _, p := range ListProviders() {
		result = append(result, ProviderAuthStatus{
			Provider:   p,
			Configured: p.Configured,
		})
	}
	return result
}

// TestAllProviders validates all configured API keys.
func TestAllProviders() []ProviderAuthStatus {
	var results []ProviderAuthStatus
	for _, p := range ListProviders() {
		status := ProviderAuthStatus{
			Provider:   p,
			Configured: p.Configured,
		}
		if p.Configured {
			apiKey := os.Getenv(p.EnvKey)
			if err := ValidateAPIKey(p.ID, apiKey); err == nil {
				status.Valid = true
			}
		}
		results = append(results, status)
	}
	return results
}

// FetchModels lists available models from a provider.
func FetchModels(providerID, apiKey string) ([]string, error) {
	provider := GetProvider(providerID)
	if provider == nil {
		return nil, fmt.Errorf("unknown provider: %s", providerID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.BaseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if strings.Contains(provider.BaseURL, "anthropic") {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var modelsResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}

	var models []string
	for _, m := range modelsResp.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

func readEnvLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}
