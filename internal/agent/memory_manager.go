package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
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

// BuiltinMemoryProvider implements MemoryProvider using MEMORY.md and USER.md
// files in the Hermes home directory.
//
// Compile-time interface check.
var _ MemoryProvider = (*BuiltinMemoryProvider)(nil)

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
