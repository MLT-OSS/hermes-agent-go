package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// testMemoryProvider is a file-based provider for testing.
type testMemoryProvider struct {
	dir string
}

func (p *testMemoryProvider) ReadMemory() (string, error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "MEMORY.md"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

func (p *testMemoryProvider) SaveMemory(key, content string) error {
	path := filepath.Join(p.dir, "MEMORY.md")
	existing, _ := os.ReadFile(path)
	entry := "\n## " + key + "\n" + content + "\n"
	return os.WriteFile(path, append(existing, []byte(entry)...), 0644)
}

func (p *testMemoryProvider) DeleteMemory(key string) error {
	return os.WriteFile(filepath.Join(p.dir, "MEMORY.md"), []byte(""), 0644)
}

func (p *testMemoryProvider) ReadUserProfile() (string, error) {
	data, err := os.ReadFile(filepath.Join(p.dir, "USER.md"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

func (p *testMemoryProvider) SaveUserProfile(content string) error {
	return os.WriteFile(filepath.Join(p.dir, "USER.md"), []byte(content), 0644)
}

func setupTestProvider(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	os.MkdirAll(tmpDir, 0755)

	// Reset provider state for each test.
	activeProviderMu.Lock()
	activeProvider = &testMemoryProvider{dir: tmpDir}
	providerInited = true
	activeProviderMu.Unlock()

	t.Cleanup(func() {
		activeProviderMu.Lock()
		activeProvider = nil
		providerInited = false
		activeProviderMu.Unlock()
	})
}

func TestMemorySaveAndRead(t *testing.T) {
	setupTestProvider(t)

	result := handleMemory(map[string]any{
		"action":  "save",
		"key":     "test-key",
		"content": "test content for memory",
	}, nil)

	var m map[string]any
	json.Unmarshal([]byte(result), &m)
	if m["success"] != true {
		t.Errorf("Expected save success, got: %s", result)
	}

	result = handleMemory(map[string]any{"action": "read"}, nil)
	json.Unmarshal([]byte(result), &m)
	content, _ := m["content"].(string)
	if content == "" {
		t.Error("Expected non-empty memory content")
	}
}

func TestMemoryDelete(t *testing.T) {
	setupTestProvider(t)

	handleMemory(map[string]any{"action": "save", "key": "to-delete", "content": "will be deleted"}, nil)

	result := handleMemory(map[string]any{"action": "delete", "key": "to-delete"}, nil)
	var m map[string]any
	json.Unmarshal([]byte(result), &m)
	if m["success"] != true {
		t.Errorf("Expected delete success, got: %s", result)
	}
}

func TestMemoryUserProfile(t *testing.T) {
	setupTestProvider(t)

	result := handleMemory(map[string]any{
		"action":  "save_user",
		"content": "Name: Test User\nRole: Developer",
	}, nil)
	var m map[string]any
	json.Unmarshal([]byte(result), &m)
	if m["success"] != true {
		t.Errorf("Expected save_user success, got: %s", result)
	}

	result = handleMemory(map[string]any{"action": "read_user"}, nil)
	json.Unmarshal([]byte(result), &m)
	if m["content"] == nil || m["content"] == "" {
		t.Error("Expected non-empty user profile")
	}
}

func TestMemoryInvalidAction(t *testing.T) {
	setupTestProvider(t)

	result := handleMemory(map[string]any{"action": "invalid"}, nil)
	var m map[string]any
	json.Unmarshal([]byte(result), &m)
	if m["error"] == nil {
		t.Error("Expected error for invalid action")
	}
}

func TestMemoryNoProvider(t *testing.T) {
	activeProviderMu.Lock()
	activeProvider = nil
	providerInited = true
	activeProviderMu.Unlock()

	t.Cleanup(func() {
		activeProviderMu.Lock()
		activeProvider = nil
		providerInited = false
		activeProviderMu.Unlock()
	})

	result := handleMemory(map[string]any{"action": "read"}, nil)
	var m map[string]any
	json.Unmarshal([]byte(result), &m)
	if m["error"] == nil {
		t.Error("Expected error when no provider configured")
	}
}

func TestRegisterMemoryProvider(t *testing.T) {
	called := false
	RegisterMemoryProvider("test-backend", func() MemoryProvider {
		called = true
		return &testMemoryProvider{dir: t.TempDir()}
	})
	_ = called // factory is not invoked, just registered

	memoryProvidersMu.RLock()
	_, ok := memoryProviders["test-backend"]
	memoryProvidersMu.RUnlock()

	if !ok {
		t.Error("Expected test-backend to be registered")
	}

	// Clean up.
	memoryProvidersMu.Lock()
	delete(memoryProviders, "test-backend")
	memoryProvidersMu.Unlock()
}
