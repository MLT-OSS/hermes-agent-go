package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// Strategy constants for credential pool selection.
const (
	StrategyRoundRobin = "round_robin"
	StrategyFillFirst  = "fill_first"
	StrategyRandom     = "random"
	StrategyLeastUsed  = "least_used"
)

// Default cooldown durations for different HTTP error codes.
const (
	CooldownRateLimit = 1 * time.Hour  // 429 Too Many Requests
	CooldownQuota     = 24 * time.Hour // 402 Payment Required
)

// RotatingCredential wraps a Credential with exhaustion tracking for
// rate-limit-aware rotation across multiple API keys.
type RotatingCredential struct {
	Credential
	exhaustedAt time.Time
	retryAfter  time.Duration
	usageCount  int64
}

// IsExhausted returns true if the credential is currently rate-limited.
func (rc *RotatingCredential) IsExhausted() bool {
	if rc.exhaustedAt.IsZero() {
		return false
	}
	return time.Since(rc.exhaustedAt) < rc.retryAfter
}

// ExhaustedUntil returns when the credential becomes available again.
func (rc *RotatingCredential) ExhaustedUntil() time.Time {
	return rc.exhaustedAt.Add(rc.retryAfter)
}

// MarkExhausted marks the credential as rate-limited for the given duration.
func (rc *RotatingCredential) MarkExhausted(d time.Duration) {
	rc.exhaustedAt = time.Now()
	rc.retryAfter = d
	if rc.retryAfter < 5*time.Second {
		rc.retryAfter = 5 * time.Second
	}
}

// Reset clears the exhaustion state.
func (rc *RotatingCredential) Reset() {
	rc.exhaustedAt = time.Time{}
	rc.retryAfter = 0
}

// CredentialRotator manages rotation across multiple API keys within a
// single provider, with exhaustion tracking and automatic recovery.
// It sits on top of CredentialPool which handles provider-level lookup.
type CredentialRotator struct {
	mu         sync.Mutex
	keys       []*RotatingCredential
	currentIdx int
	Strategy   string
}

// NewCredentialRotator creates a rotator from a list of credentials.
// The default strategy is round_robin.
func NewCredentialRotator(creds []Credential) *CredentialRotator {
	keys := make([]*RotatingCredential, len(creds))
	for i, c := range creds {
		keys[i] = &RotatingCredential{Credential: c}
	}
	return &CredentialRotator{keys: keys, Strategy: StrategyRoundRobin}
}

// Rotate returns the next available (non-exhausted) credential using the
// configured selection strategy. Returns an error if all credentials are
// exhausted or the rotator is empty.
func (r *CredentialRotator) Rotate() (*Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.keys) == 0 {
		return nil, fmt.Errorf("credential rotator is empty")
	}

	var rc *RotatingCredential
	switch r.Strategy {
	case StrategyFillFirst:
		rc = r.selectFillFirst()
	case StrategyRandom:
		rc = r.selectRandom()
	case StrategyLeastUsed:
		rc = r.selectLeastUsed()
	default:
		rc = r.selectRoundRobin()
	}

	if rc != nil {
		rc.usageCount++
		slog.Debug("credential rotated",
			"label", labelFromKey(rc.APIKey),
			"provider", rc.Provider,
			"strategy", r.Strategy,
		)
		return &rc.Credential, nil
	}

	// All exhausted -- find soonest recovery.
	var soonest *RotatingCredential
	for _, k := range r.keys {
		if soonest == nil || k.ExhaustedUntil().Before(soonest.ExhaustedUntil()) {
			soonest = k
		}
	}
	wait := time.Until(soonest.ExhaustedUntil())
	return nil, fmt.Errorf("all %d credentials exhausted, soonest recovery in %v", len(r.keys), wait.Round(time.Second))
}

// selectRoundRobin iterates keys starting from currentIdx, returning the
// first non-exhausted credential and advancing the index.
func (r *CredentialRotator) selectRoundRobin() *RotatingCredential {
	n := len(r.keys)
	for i := 0; i < n; i++ {
		idx := (r.currentIdx + i) % n
		rc := r.keys[idx]
		if !rc.IsExhausted() {
			r.currentIdx = (idx + 1) % n
			return rc
		}
	}
	return nil
}

// selectFillFirst returns the first non-exhausted credential in order,
// always preferring lower-index (higher-priority) keys.
func (r *CredentialRotator) selectFillFirst() *RotatingCredential {
	for _, rc := range r.keys {
		if !rc.IsExhausted() {
			return rc
		}
	}
	return nil
}

// selectRandom returns a random non-exhausted credential.
func (r *CredentialRotator) selectRandom() *RotatingCredential {
	available := make([]*RotatingCredential, 0, len(r.keys))
	for _, rc := range r.keys {
		if !rc.IsExhausted() {
			available = append(available, rc)
		}
	}
	if len(available) == 0 {
		return nil
	}
	return available[rand.Intn(len(available))]
}

// selectLeastUsed returns the non-exhausted credential with the lowest
// usageCount. Ties are broken by key order (lower index wins).
func (r *CredentialRotator) selectLeastUsed() *RotatingCredential {
	var best *RotatingCredential
	for _, rc := range r.keys {
		if rc.IsExhausted() {
			continue
		}
		if best == nil || rc.usageCount < best.usageCount {
			best = rc
		}
	}
	return best
}

// MarkExhausted marks the credential with the given API key as rate-limited.
func (r *CredentialRotator) MarkExhausted(apiKey string, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rc := range r.keys {
		if rc.APIKey == apiKey {
			rc.MarkExhausted(retryAfter)
			slog.Info("credential marked exhausted",
				"label", labelFromKey(apiKey),
				"retry_after", retryAfter.Round(time.Second),
			)
			return
		}
	}
}

// MarkExhaustedByStatus marks a credential based on the HTTP status code.
// 429 applies a 1-hour cooldown; 402 applies a 24-hour cooldown.
func (r *CredentialRotator) MarkExhaustedByStatus(apiKey string, statusCode int) {
	var cooldown time.Duration
	switch statusCode {
	case 402:
		cooldown = CooldownQuota
		slog.Warn("credential quota exceeded (402), 24h cooldown",
			"label", labelFromKey(apiKey),
		)
	case 429:
		cooldown = CooldownRateLimit
		slog.Warn("credential rate limited (429), 1h cooldown",
			"label", labelFromKey(apiKey),
		)
	default:
		return
	}
	r.MarkExhausted(apiKey, cooldown)
}

// Available returns the number of non-exhausted credentials.
func (r *CredentialRotator) Available() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, rc := range r.keys {
		if !rc.IsExhausted() {
			count++
		}
	}
	return count
}

// Size returns the total number of credentials.
func (r *CredentialRotator) Size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.keys)
}

// ResetAll clears exhaustion state on all credentials.
func (r *CredentialRotator) ResetAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rc := range r.keys {
		rc.Reset()
	}
}

// Status returns a summary of all credentials' states.
func (r *CredentialRotator) Status() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]map[string]any, len(r.keys))
	for i, rc := range r.keys {
		entry := map[string]any{
			"label":       labelFromKey(rc.APIKey),
			"provider":    rc.Provider,
			"exhausted":   rc.IsExhausted(),
			"usage_count": rc.usageCount,
		}
		if rc.IsExhausted() {
			entry["retry_after"] = time.Until(rc.ExhaustedUntil()).Round(time.Second).String()
		}
		result[i] = entry
	}
	return result
}

// credentialState is the JSON-serializable snapshot for persistence.
type credentialState struct {
	Keys []credentialKeyState `json:"keys"`
}

type credentialKeyState struct {
	Label      string `json:"label"`
	APIKeySuf  string `json:"api_key_suffix"`
	UsageCount int64  `json:"usage_count"`
}

// SaveState persists usage counters to disk so they survive restarts.
// The file is written to the provided path (typically ~/.hermes/credential_state.json).
func (r *CredentialRotator) SaveState(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	state := credentialState{
		Keys: make([]credentialKeyState, len(r.keys)),
	}
	for i, rc := range r.keys {
		state.Keys[i] = credentialKeyState{
			Label:      labelFromKey(rc.APIKey),
			APIKeySuf:  keySuffix(rc.APIKey),
			UsageCount: rc.usageCount,
		}
	}

	if err := config.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("save credential state: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("save credential state: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("save credential state: %w", err)
	}

	slog.Debug("credential state saved", "path", path, "keys", len(state.Keys))
	return nil
}

// LoadState restores usage counters from a previously saved file.
// Keys are matched by their API key suffix. Unknown entries are ignored.
func (r *CredentialRotator) LoadState(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no saved state is fine
		}
		return fmt.Errorf("load credential state: %w", err)
	}

	var state credentialState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("load credential state: %w", err)
	}

	// Build a lookup by API key suffix.
	lookup := make(map[string]int64, len(state.Keys))
	for _, ks := range state.Keys {
		lookup[ks.APIKeySuf] = ks.UsageCount
	}

	for _, rc := range r.keys {
		suf := keySuffix(rc.APIKey)
		if count, ok := lookup[suf]; ok {
			rc.usageCount = count
		}
	}

	slog.Debug("credential state loaded", "path", path, "keys", len(state.Keys))
	return nil
}

// defaultStatePath returns the default credential state file path.
func defaultStatePath() string {
	return filepath.Join(config.HermesHome(), "credential_state.json")
}

// keySuffix returns the last 4 characters of a key for matching.
func keySuffix(key string) string {
	if len(key) <= 4 {
		return key
	}
	return key[len(key)-4:]
}

// labelFromKey generates a short display label from an API key.
func labelFromKey(key string) string {
	if len(key) <= 8 {
		return "key-***"
	}
	return "key-" + key[len(key)-4:]
}

// parseRetryAfter extracts a retry duration from a "Retry-After" header value.
// Supports delta-seconds ("60") and HTTP-date formats.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 30 * time.Second
	}

	var seconds int
	if _, err := fmt.Sscanf(value, "%d", &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	} {
		if t, err := time.Parse(layout, value); err == nil {
			d := time.Until(t)
			if d > 0 {
				return d
			}
		}
	}

	return 30 * time.Second
}

// strategyFromConfig reads the credential pool strategy from the config's
// provider_routing map, defaulting to round_robin.
func strategyFromConfig(cfg *config.Config) string {
	if cfg.ProviderRouting == nil {
		return StrategyRoundRobin
	}

	pool, ok := cfg.ProviderRouting["credential_pool"]
	if !ok {
		return StrategyRoundRobin
	}

	poolMap, ok := pool.(map[string]any)
	if !ok {
		return StrategyRoundRobin
	}

	strategy, ok := poolMap["strategy"].(string)
	if !ok || strategy == "" {
		return StrategyRoundRobin
	}

	switch strategy {
	case StrategyRoundRobin, StrategyFillFirst, StrategyRandom, StrategyLeastUsed:
		return strategy
	default:
		slog.Warn("unknown credential pool strategy, using round_robin", "strategy", strategy)
		return StrategyRoundRobin
	}
}
