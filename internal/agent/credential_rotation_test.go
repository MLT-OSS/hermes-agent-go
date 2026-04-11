package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

func TestRotatingCredential_Exhaustion(t *testing.T) {
	rc := &RotatingCredential{
		Credential: Credential{APIKey: "sk-test", Provider: "openai"},
	}

	if rc.IsExhausted() {
		t.Error("new credential should not be exhausted")
	}

	rc.MarkExhausted(10 * time.Second)
	if !rc.IsExhausted() {
		t.Error("marked credential should be exhausted")
	}

	rc.Reset()
	if rc.IsExhausted() {
		t.Error("reset credential should not be exhausted")
	}
}

func TestRotatingCredential_MinimumCooldown(t *testing.T) {
	rc := &RotatingCredential{Credential: Credential{APIKey: "sk-test"}}
	rc.MarkExhausted(1 * time.Second)
	if rc.retryAfter < 5*time.Second {
		t.Errorf("retryAfter = %v, want >= 5s", rc.retryAfter)
	}
}

func TestCredentialRotator_Rotate(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-1", Provider: "openai"},
		{APIKey: "key-2", Provider: "openai"},
		{APIKey: "key-3", Provider: "openai"},
	})

	seen := make(map[string]bool)
	for i := 0; i < 3; i++ {
		cred, err := rotator.Rotate()
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		seen[cred.APIKey] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 unique keys, got %d", len(seen))
	}
}

func TestCredentialRotator_SkipsExhausted(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-1", Provider: "openai"},
		{APIKey: "key-2", Provider: "openai"},
	})

	rotator.MarkExhausted("key-1", 1*time.Minute)

	cred, err := rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if cred.APIKey != "key-2" {
		t.Errorf("expected key-2, got %s", cred.APIKey)
	}
}

func TestCredentialRotator_AllExhausted(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-1", Provider: "openai"},
		{APIKey: "key-2", Provider: "openai"},
	})

	rotator.MarkExhausted("key-1", 1*time.Minute)
	rotator.MarkExhausted("key-2", 2*time.Minute)

	_, err := rotator.Rotate()
	if err == nil {
		t.Error("expected error when all exhausted")
	}
}

func TestCredentialRotator_Empty(t *testing.T) {
	rotator := NewCredentialRotator(nil)
	_, err := rotator.Rotate()
	if err == nil {
		t.Error("expected error for empty rotator")
	}
}

func TestCredentialRotator_SizeAndAvailable(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-1", Provider: "openai"},
		{APIKey: "key-2", Provider: "openai"},
	})

	if rotator.Size() != 2 {
		t.Errorf("Size() = %d, want 2", rotator.Size())
	}
	if rotator.Available() != 2 {
		t.Errorf("Available() = %d, want 2", rotator.Available())
	}

	rotator.MarkExhausted("key-1", 1*time.Minute)
	if rotator.Available() != 1 {
		t.Errorf("Available() = %d, want 1", rotator.Available())
	}
}

func TestCredentialRotator_ResetAll(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-1", Provider: "openai"},
		{APIKey: "key-2", Provider: "openai"},
	})

	rotator.MarkExhausted("key-1", 1*time.Minute)
	rotator.MarkExhausted("key-2", 1*time.Minute)
	rotator.ResetAll()

	if rotator.Available() != 2 {
		t.Errorf("Available() = %d, want 2", rotator.Available())
	}
}

func TestCredentialRotator_Status(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-1234567890", Provider: "openai"},
	})

	status := rotator.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(status))
	}
	if status[0]["label"] != "key-7890" {
		t.Errorf("label = %v, want 'key-7890'", status[0]["label"])
	}
	if status[0]["usage_count"].(int64) != 0 {
		t.Errorf("usage_count = %v, want 0", status[0]["usage_count"])
	}
}

func TestLabelFromKey(t *testing.T) {
	tests := []struct {
		key, want string
	}{
		{"sk-test-1234567890", "key-7890"},
		{"short", "key-***"},
		{"12345678", "key-***"},
		{"123456789", "key-6789"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := labelFromKey(tt.key)
			if got != tt.want {
				t.Errorf("labelFromKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{"empty", "", 30 * time.Second},
		{"seconds", "60", 60 * time.Second},
		{"small", "5", 5 * time.Second},
		{"invalid", "not-a-number", 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.value)
			if got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// --- Strategy selection tests ---

func TestStrategy_RoundRobin(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
		{APIKey: "key-ccc3", Provider: "openai"},
	})
	rotator.Strategy = StrategyRoundRobin

	// Should cycle through all three in order.
	keys := make([]string, 6)
	for i := 0; i < 6; i++ {
		c, err := rotator.Rotate()
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		keys[i] = c.APIKey
	}

	// First three should be aaa1, bbb2, ccc3; next three repeat.
	expected := []string{"key-aaa1", "key-bbb2", "key-ccc3", "key-aaa1", "key-bbb2", "key-ccc3"}
	for i, want := range expected {
		if keys[i] != want {
			t.Errorf("round robin [%d] = %s, want %s", i, keys[i], want)
		}
	}
}

func TestStrategy_FillFirst(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
	})
	rotator.Strategy = StrategyFillFirst

	// Should always return the first credential.
	for i := 0; i < 5; i++ {
		c, err := rotator.Rotate()
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		if c.APIKey != "key-aaa1" {
			t.Errorf("fill_first [%d] = %s, want key-aaa1", i, c.APIKey)
		}
	}

	// Exhaust first, should fall to second.
	rotator.MarkExhausted("key-aaa1", 1*time.Minute)
	c, err := rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate after exhaust: %v", err)
	}
	if c.APIKey != "key-bbb2" {
		t.Errorf("fill_first after exhaust = %s, want key-bbb2", c.APIKey)
	}
}

func TestStrategy_Random(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
		{APIKey: "key-ccc3", Provider: "openai"},
	})
	rotator.Strategy = StrategyRandom

	seen := make(map[string]bool)
	// With 100 iterations and 3 keys, probability of missing one is negligible.
	for i := 0; i < 100; i++ {
		c, err := rotator.Rotate()
		if err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		seen[c.APIKey] = true
	}
	if len(seen) != 3 {
		t.Errorf("random strategy used %d unique keys, want 3", len(seen))
	}
}

func TestStrategy_LeastUsed(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
		{APIKey: "key-ccc3", Provider: "openai"},
	})
	rotator.Strategy = StrategyLeastUsed

	// First call: all have 0 usage, should pick first (tie-break by order).
	c, err := rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if c.APIKey != "key-aaa1" {
		t.Errorf("least_used first = %s, want key-aaa1", c.APIKey)
	}

	// Second call: aaa1 has usage=1, bbb2 and ccc3 have 0 -> pick bbb2.
	c, err = rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if c.APIKey != "key-bbb2" {
		t.Errorf("least_used second = %s, want key-bbb2", c.APIKey)
	}

	// Third call: aaa1=1, bbb2=1, ccc3=0 -> pick ccc3.
	c, err = rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if c.APIKey != "key-ccc3" {
		t.Errorf("least_used third = %s, want key-ccc3", c.APIKey)
	}

	// Fourth call: all at 1 -> tie-break picks aaa1 (first in order).
	c, err = rotator.Rotate()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if c.APIKey != "key-aaa1" {
		t.Errorf("least_used fourth = %s, want key-aaa1", c.APIKey)
	}
}

// --- Quota vs rate limit tests ---

func TestMarkExhaustedByStatus_429(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
	})

	rotator.MarkExhaustedByStatus("key-aaa1", 429)

	rotator.mu.Lock()
	rc := rotator.keys[0]
	if !rc.IsExhausted() {
		t.Error("429 should exhaust credential")
	}
	if rc.retryAfter != CooldownRateLimit {
		t.Errorf("429 cooldown = %v, want %v", rc.retryAfter, CooldownRateLimit)
	}
	rotator.mu.Unlock()
}

func TestMarkExhaustedByStatus_402(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
		{APIKey: "key-bbb2", Provider: "openai"},
	})

	rotator.MarkExhaustedByStatus("key-aaa1", 402)

	rotator.mu.Lock()
	rc := rotator.keys[0]
	if !rc.IsExhausted() {
		t.Error("402 should exhaust credential")
	}
	if rc.retryAfter != CooldownQuota {
		t.Errorf("402 cooldown = %v, want %v", rc.retryAfter, CooldownQuota)
	}
	rotator.mu.Unlock()
}

func TestMarkExhaustedByStatus_Other(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
	})

	rotator.MarkExhaustedByStatus("key-aaa1", 500)

	rotator.mu.Lock()
	rc := rotator.keys[0]
	if rc.IsExhausted() {
		t.Error("500 should not exhaust credential")
	}
	rotator.mu.Unlock()
}

// --- State persistence round-trip tests ---

func TestSaveLoadState_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credential_state.json")

	rotator := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-aaa1", Provider: "openai"},
		{APIKey: "sk-test-bbb2", Provider: "openai"},
	})
	rotator.Strategy = StrategyLeastUsed

	// Simulate usage.
	for i := 0; i < 5; i++ {
		_, _ = rotator.Rotate()
	}

	if err := rotator.SaveState(path); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify file exists and is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	var state credentialState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if len(state.Keys) != 2 {
		t.Fatalf("expected 2 keys in state, got %d", len(state.Keys))
	}

	// Load into a new rotator with the same keys.
	rotator2 := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-aaa1", Provider: "openai"},
		{APIKey: "sk-test-bbb2", Provider: "openai"},
	})
	if err := rotator2.LoadState(path); err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	// Check that usage counts were restored.
	rotator2.mu.Lock()
	total := rotator2.keys[0].usageCount + rotator2.keys[1].usageCount
	rotator2.mu.Unlock()
	if total != 5 {
		t.Errorf("total usage count after load = %d, want 5", total)
	}
}

func TestLoadState_NonExistent(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-aaa1", Provider: "openai"},
	})

	err := rotator.LoadState("/nonexistent/path/state.json")
	if err != nil {
		t.Errorf("LoadState on missing file should return nil, got %v", err)
	}
}

func TestSaveState_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "credential_state.json")

	rotator := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-aaa1", Provider: "openai"},
	})

	if err := rotator.SaveState(path); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("state file should exist after SaveState")
	}
}

// --- Configuration tests ---

func TestStrategyFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *config.Config
		want   string
	}{
		{
			name: "nil routing",
			cfg:  &config.Config{},
			want: StrategyRoundRobin,
		},
		{
			name: "no credential_pool key",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"other": "value",
				},
			},
			want: StrategyRoundRobin,
		},
		{
			name: "round_robin explicit",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "round_robin",
					},
				},
			},
			want: StrategyRoundRobin,
		},
		{
			name: "fill_first",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "fill_first",
					},
				},
			},
			want: StrategyFillFirst,
		},
		{
			name: "random",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "random",
					},
				},
			},
			want: StrategyRandom,
		},
		{
			name: "least_used",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "least_used",
					},
				},
			},
			want: StrategyLeastUsed,
		},
		{
			name: "unknown falls back to round_robin",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "invalid_strategy",
					},
				},
			},
			want: StrategyRoundRobin,
		},
		{
			name: "empty strategy falls back to round_robin",
			cfg: &config.Config{
				ProviderRouting: map[string]any{
					"credential_pool": map[string]any{
						"strategy": "",
					},
				},
			},
			want: StrategyRoundRobin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strategyFromConfig(tt.cfg)
			if got != tt.want {
				t.Errorf("strategyFromConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Usage counter tests ---

func TestUsageCountIncrementsOnRotate(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "key-aaa1", Provider: "openai"},
	})

	for i := 0; i < 3; i++ {
		_, _ = rotator.Rotate()
	}

	rotator.mu.Lock()
	count := rotator.keys[0].usageCount
	rotator.mu.Unlock()

	if count != 3 {
		t.Errorf("usageCount = %d, want 3", count)
	}
}

func TestStatusIncludesUsageCount(t *testing.T) {
	rotator := NewCredentialRotator([]Credential{
		{APIKey: "sk-test-1234567890", Provider: "openai"},
	})

	_, _ = rotator.Rotate()

	status := rotator.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(status))
	}
	if status[0]["usage_count"].(int64) != 1 {
		t.Errorf("usage_count = %v, want 1", status[0]["usage_count"])
	}
}
