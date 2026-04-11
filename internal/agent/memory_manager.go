package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// MemoryProvider is the interface for memory storage backends.
// Implementations include the built-in file-based provider and
// external providers like Honcho.
type MemoryProvider interface {
	// ReadMemory returns the full contents of the agent's memory store.
	ReadMemory() (string, error)

	// SaveMemory saves a key/content pair to memory. If the key already
	// exists, the entry is updated.
	SaveMemory(key, content string) error

	// DeleteMemory removes a memory entry by key.
	DeleteMemory(key string) error

	// ReadUserProfile returns the user profile contents.
	ReadUserProfile() (string, error)

	// SaveUserProfile writes or replaces the user profile.
	SaveUserProfile(content string) error
}

// --- Optional lifecycle interfaces ---
// Providers may implement any subset of these to hook into the agent lifecycle.
// The MemoryManager checks at runtime via interface assertion.

// SystemPromptProvider allows a memory provider to contribute a block
// of text that is injected into the system prompt.
type SystemPromptProvider interface {
	SystemPromptBlock() string
}

// PrefetchProvider allows a memory provider to prefetch or warm up
// relevant memories before a conversation turn begins.
type PrefetchProvider interface {
	Prefetch(query string) error
}

// TurnSyncProvider allows a memory provider to persist a completed
// conversation turn (user message + assistant reply).
type TurnSyncProvider interface {
	SyncTurn(userMsg, assistantMsg string) error
}

// PreCompressProvider allows a memory provider to extract information
// from messages that are about to be compressed (and therefore lost).
type PreCompressProvider interface {
	OnPreCompress(messages []llm.Message) string
}

// ShutdownProvider allows a memory provider to perform cleanup when
// the agent is shutting down.
type ShutdownProvider interface {
	Shutdown() error
}

// BuiltinMemoryProvider implements MemoryProvider using MEMORY.md and USER.md
// files in the Hermes home directory.
//
// Compile-time interface check.
var _ MemoryProvider = (*BuiltinMemoryProvider)(nil)

// Compile-time lifecycle interface checks.
var _ SystemPromptProvider = (*BuiltinMemoryProvider)(nil)
var _ ShutdownProvider = (*BuiltinMemoryProvider)(nil)

type BuiltinMemoryProvider struct {
	homeDir string
}

// NewBuiltinMemoryProvider creates a provider that stores memory in
// the given home directory's "memories" subdirectory.
func NewBuiltinMemoryProvider(homeDir string) *BuiltinMemoryProvider {
	return &BuiltinMemoryProvider{homeDir: homeDir}
}

func (p *BuiltinMemoryProvider) memoriesDir() string {
	dir := filepath.Join(p.homeDir, "memories")
	os.MkdirAll(dir, 0755)
	return dir
}

// ReadMemory reads the MEMORY.md file.
func (p *BuiltinMemoryProvider) ReadMemory() (string, error) {
	memoryPath := filepath.Join(p.memoriesDir(), "MEMORY.md")
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read memory: %w", err)
	}
	return string(data), nil
}

// SaveMemory saves or updates a memory entry in MEMORY.md under the given key.
func (p *BuiltinMemoryProvider) SaveMemory(key, content string) error {
	if key == "" || content == "" {
		return fmt.Errorf("both key and content are required")
	}

	memoryPath := filepath.Join(p.memoriesDir(), "MEMORY.md")
	existing, _ := os.ReadFile(memoryPath)

	timestamp := time.Now().Format("2006-01-02 15:04")
	entry := fmt.Sprintf("\n## %s\n*Saved: %s*\n\n%s\n", key, timestamp, content)

	existingStr := string(existing)
	marker := fmt.Sprintf("## %s\n", key)

	if idx := strings.Index(existingStr, marker); idx != -1 {
		// Update existing entry: find next section or end.
		nextIdx := strings.Index(existingStr[idx+len(marker):], "\n## ")
		if nextIdx != -1 {
			existingStr = existingStr[:idx] + entry[1:] + existingStr[idx+len(marker)+nextIdx:]
		} else {
			existingStr = existingStr[:idx] + entry[1:]
		}
	} else {
		existingStr += entry
	}

	if err := os.WriteFile(memoryPath, []byte(existingStr), 0644); err != nil {
		return fmt.Errorf("write memory: %w", err)
	}

	return nil
}

// DeleteMemory removes a memory entry by key.
func (p *BuiltinMemoryProvider) DeleteMemory(key string) error {
	if key == "" {
		return fmt.Errorf("key is required")
	}

	memoryPath := filepath.Join(p.memoriesDir(), "MEMORY.md")
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		return fmt.Errorf("read memory: %w", err)
	}

	content := string(data)
	marker := fmt.Sprintf("## %s\n", key)
	idx := strings.Index(content, marker)
	if idx == -1 {
		return fmt.Errorf("memory key '%s' not found", key)
	}

	nextIdx := strings.Index(content[idx+len(marker):], "\n## ")
	if nextIdx != -1 {
		content = content[:idx] + content[idx+len(marker)+nextIdx+1:]
	} else {
		content = content[:idx]
	}

	return os.WriteFile(memoryPath, []byte(content), 0644)
}

// ReadUserProfile reads the USER.md file.
func (p *BuiltinMemoryProvider) ReadUserProfile() (string, error) {
	userPath := filepath.Join(p.memoriesDir(), "USER.md")
	data, err := os.ReadFile(userPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read user profile: %w", err)
	}
	return string(data), nil
}

// SaveUserProfile writes the user profile.
func (p *BuiltinMemoryProvider) SaveUserProfile(content string) error {
	if content == "" {
		return fmt.Errorf("content is required")
	}

	userPath := filepath.Join(p.memoriesDir(), "USER.md")
	if err := os.WriteFile(userPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write user profile: %w", err)
	}
	return nil
}

// SystemPromptBlock returns the combined contents of MEMORY.md and USER.md
// for injection into the system prompt.
func (p *BuiltinMemoryProvider) SystemPromptBlock() string {
	var parts []string

	memory, err := p.ReadMemory()
	if err != nil {
		slog.Warn("Failed to read memory for system prompt", "error", err)
	} else if memory != "" {
		parts = append(parts, "## Agent Memory\n"+memory)
	}

	profile, err := p.ReadUserProfile()
	if err != nil {
		slog.Warn("Failed to read user profile for system prompt", "error", err)
	} else if profile != "" {
		parts = append(parts, "## User Profile\n"+profile)
	}

	return strings.Join(parts, "\n\n")
}

// Shutdown is a no-op for the built-in file-based provider.
func (p *BuiltinMemoryProvider) Shutdown() error {
	return nil
}

// MemoryManager provides a unified interface to memory operations,
// delegating to the configured MemoryProvider.
type MemoryManager struct {
	provider MemoryProvider
}

// NewMemoryManager creates a MemoryManager with the specified provider.
// Supported provider names:
//   - "builtin" or "" -- uses BuiltinMemoryProvider (file-based)
//   - "honcho"        -- placeholder for Honcho integration (falls back to builtin)
func NewMemoryManager(providerName string) *MemoryManager {
	var provider MemoryProvider

	switch strings.ToLower(providerName) {
	case "honcho":
		// Use the Honcho API for cloud-based memory storage.
		appID := os.Getenv("HONCHO_APP_ID")
		userID := os.Getenv("HONCHO_USER_ID")
		if userID == "" {
			userID = "default"
		}
		if appID != "" && os.Getenv("HONCHO_API_KEY") != "" {
			provider = NewHonchoProvider(appID, userID)
		} else {
			// Fall back to builtin if Honcho is not configured.
			provider = NewBuiltinMemoryProvider(config.HermesHome())
		}
	default:
		provider = NewBuiltinMemoryProvider(config.HermesHome())
	}

	return &MemoryManager{provider: provider}
}

// Provider returns the underlying MemoryProvider.
func (m *MemoryManager) Provider() MemoryProvider {
	return m.provider
}

// ReadMemory delegates to the provider.
func (m *MemoryManager) ReadMemory() (string, error) {
	return m.provider.ReadMemory()
}

// SaveMemory delegates to the provider.
func (m *MemoryManager) SaveMemory(key, content string) error {
	return m.provider.SaveMemory(key, content)
}

// DeleteMemory delegates to the provider.
func (m *MemoryManager) DeleteMemory(key string) error {
	return m.provider.DeleteMemory(key)
}

// ReadUserProfile delegates to the provider.
func (m *MemoryManager) ReadUserProfile() (string, error) {
	return m.provider.ReadUserProfile()
}

// SaveUserProfile delegates to the provider.
func (m *MemoryManager) SaveUserProfile(content string) error {
	return m.provider.SaveUserProfile(content)
}

// --- Lifecycle hook dispatchers ---

// GetSystemPromptBlock returns the provider's system prompt contribution,
// or an empty string if the provider does not implement SystemPromptProvider.
func (m *MemoryManager) GetSystemPromptBlock() string {
	if p, ok := m.provider.(SystemPromptProvider); ok {
		return p.SystemPromptBlock()
	}
	return ""
}

// RunPrefetch asks the provider to prefetch memories relevant to the given
// query. It is a no-op if the provider does not implement PrefetchProvider.
func (m *MemoryManager) RunPrefetch(query string) error {
	if p, ok := m.provider.(PrefetchProvider); ok {
		slog.Debug("Running memory prefetch", "query_len", len(query))
		return p.Prefetch(query)
	}
	return nil
}

// RunSyncTurn asks the provider to persist a completed conversation turn.
// It is a no-op if the provider does not implement TurnSyncProvider.
func (m *MemoryManager) RunSyncTurn(userMsg, assistantMsg string) error {
	if p, ok := m.provider.(TurnSyncProvider); ok {
		slog.Debug("Syncing conversation turn")
		return p.SyncTurn(userMsg, assistantMsg)
	}
	return nil
}

// RunOnPreCompress notifies the provider that messages are about to be
// compressed, giving it a chance to extract important information.
// Returns the provider's summary string, or empty if not implemented.
func (m *MemoryManager) RunOnPreCompress(messages []llm.Message) string {
	if p, ok := m.provider.(PreCompressProvider); ok {
		slog.Debug("Running pre-compress hook", "message_count", len(messages))
		return p.OnPreCompress(messages)
	}
	return ""
}

// RunShutdown asks the provider to clean up resources. It is a no-op if
// the provider does not implement ShutdownProvider.
func (m *MemoryManager) RunShutdown() error {
	if p, ok := m.provider.(ShutdownProvider); ok {
		slog.Debug("Running memory provider shutdown")
		return p.Shutdown()
	}
	return nil
}
