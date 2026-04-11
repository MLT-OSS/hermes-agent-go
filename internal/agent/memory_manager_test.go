package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

// --- Mock providers for testing lifecycle interfaces ---

// minimalProvider implements only MemoryProvider (no lifecycle interfaces).
type minimalProvider struct{}

func (m *minimalProvider) ReadMemory() (string, error)      { return "", nil }
func (m *minimalProvider) SaveMemory(k, c string) error     { return nil }
func (m *minimalProvider) DeleteMemory(k string) error      { return nil }
func (m *minimalProvider) ReadUserProfile() (string, error) { return "", nil }
func (m *minimalProvider) SaveUserProfile(c string) error   { return nil }

// fullProvider implements MemoryProvider + all lifecycle interfaces.
type fullProvider struct {
	systemBlock    string
	prefetchCalled bool
	prefetchQuery  string
	syncCalled     bool
	syncUser       string
	syncAssistant  string
	preCompressMsgs []llm.Message
	preCompressOut  string
	shutdownCalled bool
	shutdownErr    error
}

func (f *fullProvider) ReadMemory() (string, error)      { return "", nil }
func (f *fullProvider) SaveMemory(k, c string) error     { return nil }
func (f *fullProvider) DeleteMemory(k string) error      { return nil }
func (f *fullProvider) ReadUserProfile() (string, error) { return "", nil }
func (f *fullProvider) SaveUserProfile(c string) error   { return nil }

func (f *fullProvider) SystemPromptBlock() string {
	return f.systemBlock
}

func (f *fullProvider) Prefetch(query string) error {
	f.prefetchCalled = true
	f.prefetchQuery = query
	return nil
}

func (f *fullProvider) SyncTurn(userMsg, assistantMsg string) error {
	f.syncCalled = true
	f.syncUser = userMsg
	f.syncAssistant = assistantMsg
	return nil
}

func (f *fullProvider) OnPreCompress(messages []llm.Message) string {
	f.preCompressMsgs = messages
	return f.preCompressOut
}

func (f *fullProvider) Shutdown() error {
	f.shutdownCalled = true
	return f.shutdownErr
}

// partialProvider implements only SystemPromptProvider and PrefetchProvider.
type partialProvider struct {
	minimalProvider
	block string
}

func (p *partialProvider) SystemPromptBlock() string { return p.block }
func (p *partialProvider) Prefetch(query string) error {
	return nil
}

// --- Tests ---

func TestMemoryManager_MinimalProvider_LifecycleNoOps(t *testing.T) {
	mm := &MemoryManager{provider: &minimalProvider{}}

	// All lifecycle methods should be no-ops (return zero values, no panic).
	if block := mm.GetSystemPromptBlock(); block != "" {
		t.Errorf("Expected empty system prompt block, got %q", block)
	}
	if err := mm.RunPrefetch("hello"); err != nil {
		t.Errorf("Expected nil error from RunPrefetch, got %v", err)
	}
	if err := mm.RunSyncTurn("u", "a"); err != nil {
		t.Errorf("Expected nil error from RunSyncTurn, got %v", err)
	}
	if out := mm.RunOnPreCompress([]llm.Message{{Role: "user", Content: "x"}}); out != "" {
		t.Errorf("Expected empty pre-compress output, got %q", out)
	}
	if err := mm.RunShutdown(); err != nil {
		t.Errorf("Expected nil error from RunShutdown, got %v", err)
	}
}

func TestMemoryManager_FullProvider_AllHooks(t *testing.T) {
	fp := &fullProvider{
		systemBlock:    "memory context here",
		preCompressOut: "summary of old messages",
	}
	mm := &MemoryManager{provider: fp}

	// SystemPromptBlock
	if block := mm.GetSystemPromptBlock(); block != "memory context here" {
		t.Errorf("Expected 'memory context here', got %q", block)
	}

	// Prefetch
	if err := mm.RunPrefetch("search query"); err != nil {
		t.Fatalf("RunPrefetch error: %v", err)
	}
	if !fp.prefetchCalled {
		t.Error("Expected Prefetch to be called")
	}
	if fp.prefetchQuery != "search query" {
		t.Errorf("Expected query 'search query', got %q", fp.prefetchQuery)
	}

	// SyncTurn
	if err := mm.RunSyncTurn("user msg", "assistant msg"); err != nil {
		t.Fatalf("RunSyncTurn error: %v", err)
	}
	if !fp.syncCalled {
		t.Error("Expected SyncTurn to be called")
	}
	if fp.syncUser != "user msg" || fp.syncAssistant != "assistant msg" {
		t.Errorf("SyncTurn got wrong args: user=%q assistant=%q", fp.syncUser, fp.syncAssistant)
	}

	// PreCompress
	msgs := []llm.Message{
		{Role: "user", Content: "old message"},
		{Role: "assistant", Content: "old reply"},
	}
	out := mm.RunOnPreCompress(msgs)
	if out != "summary of old messages" {
		t.Errorf("Expected 'summary of old messages', got %q", out)
	}
	if len(fp.preCompressMsgs) != 2 {
		t.Errorf("Expected 2 messages passed, got %d", len(fp.preCompressMsgs))
	}

	// Shutdown
	if err := mm.RunShutdown(); err != nil {
		t.Fatalf("RunShutdown error: %v", err)
	}
	if !fp.shutdownCalled {
		t.Error("Expected Shutdown to be called")
	}
}

func TestMemoryManager_FullProvider_ShutdownError(t *testing.T) {
	fp := &fullProvider{shutdownErr: fmt.Errorf("disk full")}
	mm := &MemoryManager{provider: fp}

	err := mm.RunShutdown()
	if err == nil || err.Error() != "disk full" {
		t.Errorf("Expected 'disk full' error, got %v", err)
	}
}

func TestMemoryManager_PartialProvider(t *testing.T) {
	pp := &partialProvider{block: "partial block"}
	mm := &MemoryManager{provider: pp}

	// Implemented hooks should work.
	if block := mm.GetSystemPromptBlock(); block != "partial block" {
		t.Errorf("Expected 'partial block', got %q", block)
	}
	if err := mm.RunPrefetch("q"); err != nil {
		t.Errorf("RunPrefetch error: %v", err)
	}

	// Unimplemented hooks should be no-ops.
	if err := mm.RunSyncTurn("u", "a"); err != nil {
		t.Errorf("Expected nil from RunSyncTurn, got %v", err)
	}
	if out := mm.RunOnPreCompress(nil); out != "" {
		t.Errorf("Expected empty from RunOnPreCompress, got %q", out)
	}
	if err := mm.RunShutdown(); err != nil {
		t.Errorf("Expected nil from RunShutdown, got %v", err)
	}
}

// --- BuiltinMemoryProvider lifecycle tests ---

func TestBuiltinProvider_SystemPromptBlock_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewBuiltinMemoryProvider(tmpDir)

	block := p.SystemPromptBlock()
	if block != "" {
		t.Errorf("Expected empty block for empty provider, got %q", block)
	}
}

func TestBuiltinProvider_SystemPromptBlock_WithMemoryAndProfile(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewBuiltinMemoryProvider(tmpDir)

	if err := p.SaveMemory("pref", "I like Go"); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if err := p.SaveUserProfile("Name: Alice"); err != nil {
		t.Fatalf("SaveUserProfile: %v", err)
	}

	block := p.SystemPromptBlock()
	if !strings.Contains(block, "Agent Memory") {
		t.Error("Expected '## Agent Memory' in block")
	}
	if !strings.Contains(block, "I like Go") {
		t.Error("Expected memory content in block")
	}
	if !strings.Contains(block, "User Profile") {
		t.Error("Expected '## User Profile' in block")
	}
	if !strings.Contains(block, "Name: Alice") {
		t.Error("Expected profile content in block")
	}
}

func TestBuiltinProvider_SystemPromptBlock_MemoryOnly(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewBuiltinMemoryProvider(tmpDir)

	if err := p.SaveMemory("note", "remember this"); err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}

	block := p.SystemPromptBlock()
	if !strings.Contains(block, "Agent Memory") {
		t.Error("Expected memory section")
	}
	if strings.Contains(block, "User Profile") {
		t.Error("Expected no profile section when USER.md does not exist")
	}
}

func TestBuiltinProvider_Shutdown(t *testing.T) {
	p := NewBuiltinMemoryProvider(t.TempDir())
	if err := p.Shutdown(); err != nil {
		t.Errorf("Expected nil from Shutdown, got %v", err)
	}
}

// --- Compile-time interface assertion tests ---

func TestBuiltinProvider_ImplementsSystemPromptProvider(t *testing.T) {
	var p MemoryProvider = NewBuiltinMemoryProvider(t.TempDir())
	if _, ok := p.(SystemPromptProvider); !ok {
		t.Error("BuiltinMemoryProvider should implement SystemPromptProvider")
	}
}

func TestBuiltinProvider_ImplementsShutdownProvider(t *testing.T) {
	var p MemoryProvider = NewBuiltinMemoryProvider(t.TempDir())
	if _, ok := p.(ShutdownProvider); !ok {
		t.Error("BuiltinMemoryProvider should implement ShutdownProvider")
	}
}

func TestBuiltinProvider_DoesNotImplementPrefetchProvider(t *testing.T) {
	var p MemoryProvider = NewBuiltinMemoryProvider(t.TempDir())
	if _, ok := p.(PrefetchProvider); ok {
		t.Error("BuiltinMemoryProvider should not implement PrefetchProvider")
	}
}

func TestBuiltinProvider_DoesNotImplementTurnSyncProvider(t *testing.T) {
	var p MemoryProvider = NewBuiltinMemoryProvider(t.TempDir())
	if _, ok := p.(TurnSyncProvider); ok {
		t.Error("BuiltinMemoryProvider should not implement TurnSyncProvider")
	}
}

// --- MemoryManager via NewMemoryManager ---

func TestNewMemoryManager_BuiltinDefault(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	mm := NewMemoryManager("")
	if mm.Provider() == nil {
		t.Fatal("Expected non-nil provider")
	}

	// Builtin provider should implement SystemPromptProvider.
	if block := mm.GetSystemPromptBlock(); block != "" {
		t.Errorf("Expected empty block for fresh builtin provider, got %q", block)
	}

	// Write some memory and check the block.
	memDir := filepath.Join(tmpDir, "memories")
	os.MkdirAll(memDir, 0755)
	os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("## Key\nValue\n"), 0644)

	block := mm.GetSystemPromptBlock()
	if !strings.Contains(block, "Value") {
		t.Errorf("Expected memory content in block, got %q", block)
	}
}
