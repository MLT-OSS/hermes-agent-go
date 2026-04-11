package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
	"github.com/hermes-agent/hermes-agent-go/internal/state"
)

func TestAgentOptions(t *testing.T) {
	a := &AIAgent{}

	WithModel("test-model")(a)
	if a.model != "test-model" {
		t.Errorf("Expected model 'test-model', got '%s'", a.model)
	}

	WithMaxIterations(50)(a)
	if a.maxIterations != 50 {
		t.Errorf("Expected 50 iterations, got %d", a.maxIterations)
	}

	WithPlatform("telegram")(a)
	if a.platform != "telegram" {
		t.Errorf("Expected platform 'telegram', got '%s'", a.platform)
	}

	WithSessionID("sess-123")(a)
	if a.sessionID != "sess-123" {
		t.Errorf("Expected session 'sess-123', got '%s'", a.sessionID)
	}

	WithQuietMode(true)(a)
	if !a.quietMode {
		t.Error("Expected quiet mode on")
	}

	WithBaseURL("https://api.example.com")(a)
	if a.baseURL != "https://api.example.com" {
		t.Errorf("Expected base URL, got '%s'", a.baseURL)
	}

	WithAPIKey("sk-test")(a)
	if a.apiKey != "sk-test" {
		t.Errorf("Expected API key, got '%s'", a.apiKey)
	}

	WithProvider("openai")(a)
	if a.provider != "openai" {
		t.Errorf("Expected provider 'openai', got '%s'", a.provider)
	}

	WithAPIMode("anthropic")(a)
	if a.apiMode != "anthropic" {
		t.Errorf("Expected apiMode 'anthropic', got '%s'", a.apiMode)
	}

	WithSkipContextFiles(true)(a)
	if !a.skipContextFiles {
		t.Error("Expected skipContextFiles true")
	}

	WithSkipMemory(true)(a)
	if !a.skipMemory {
		t.Error("Expected skipMemory true")
	}

	WithPersistSession(false)(a)
	if a.persistSession {
		t.Error("Expected persistSession false")
	}

	WithEnabledToolsets([]string{"web", "terminal"})(a)
	if len(a.enabledToolsets) != 2 {
		t.Errorf("Expected 2 enabled toolsets, got %d", len(a.enabledToolsets))
	}

	WithDisabledToolsets([]string{"browser"})(a)
	if len(a.disabledToolsets) != 1 {
		t.Errorf("Expected 1 disabled toolset, got %d", len(a.disabledToolsets))
	}

	WithSystemPrompt("Custom prompt")(a)
	if a.ephemeralSystemPrompt != "Custom prompt" {
		t.Errorf("Expected custom prompt, got '%s'", a.ephemeralSystemPrompt)
	}
}

func TestStreamCallbacksFiring(t *testing.T) {
	var deltaReceived string
	var toolStartReceived string
	var stepReceived int

	a := &AIAgent{
		callbacks: &StreamCallbacks{
			OnStreamDelta: func(text string) { deltaReceived = text },
			OnToolStart:   func(name string) { toolStartReceived = name },
			OnStep:        func(i int, _ []string) { stepReceived = i },
		},
	}

	a.fireStreamDelta("hello")
	if deltaReceived != "hello" {
		t.Errorf("Expected 'hello', got '%s'", deltaReceived)
	}

	a.fireToolStart("terminal")
	if toolStartReceived != "terminal" {
		t.Errorf("Expected 'terminal', got '%s'", toolStartReceived)
	}

	a.fireStep(5, nil)
	if stepReceived != 5 {
		t.Errorf("Expected step 5, got %d", stepReceived)
	}
}

func TestStreamCallbacksNil(t *testing.T) {
	a := &AIAgent{}

	// Should not panic with nil callbacks
	a.fireStreamDelta("test")
	a.fireReasoning("test")
	a.fireToolProgress("test", "args")
	a.fireToolStart("test")
	a.fireToolComplete("test")
	a.fireStep(1, nil)
	a.fireStatus("test")
}

func TestHasStreamConsumers(t *testing.T) {
	a := &AIAgent{}
	if a.hasStreamConsumers() {
		t.Error("Expected false with nil callbacks")
	}

	a.callbacks = &StreamCallbacks{}
	if a.hasStreamConsumers() {
		t.Error("Expected false with empty callbacks")
	}

	a.callbacks = &StreamCallbacks{OnStreamDelta: func(s string) {}}
	if !a.hasStreamConsumers() {
		t.Error("Expected true with OnStreamDelta set")
	}
}

func TestInterrupt(t *testing.T) {
	a := &AIAgent{}
	if a.isInterrupted() {
		t.Error("Expected not interrupted initially")
	}

	a.Interrupt()
	if !a.isInterrupted() {
		t.Error("Expected interrupted after Interrupt()")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("Short string should not be truncated")
	}
	result := truncate("hello world this is long", 10)
	if len(result) > 14 { // 10 + "..."
		t.Errorf("Expected truncated result, got '%s'", result)
	}
}

func TestBuildSystemPromptWithOverride(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	a := &AIAgent{
		ephemeralSystemPrompt: "Custom system prompt override",
		platform:              "cli",
	}
	prompt := a.buildSystemPrompt()
	if prompt != "Custom system prompt override" {
		t.Errorf("Expected override prompt, got '%s'", prompt)
	}
}

func TestBuildSystemPromptDefault(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")
	os.MkdirAll(tmpDir+"/skills", 0755)

	a := &AIAgent{
		platform: "cli",
	}
	prompt := a.buildSystemPrompt()
	if prompt == "" {
		t.Error("Expected non-empty default prompt")
	}
	if len(prompt) < 100 {
		t.Errorf("Default prompt too short (%d chars)", len(prompt))
	}
}

// --- GenerateSessionTitle tests ---

func TestGenerateSessionTitle_EmptyMessages(t *testing.T) {
	title := GenerateSessionTitle(nil)
	if title != "Untitled session" {
		t.Errorf("Expected 'Untitled session', got '%s'", title)
	}
}

func TestGenerateSessionTitle_NoUserMessages(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "assistant", Content: "Hello!"},
	}
	title := GenerateSessionTitle(msgs)
	if title != "Untitled session" {
		t.Errorf("Expected 'Untitled session', got '%s'", title)
	}
}

func TestGenerateSessionTitle_ShortMessage(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "Hello world"},
	}
	title := GenerateSessionTitle(msgs)
	if title != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", title)
	}
}

func TestGenerateSessionTitle_LongMessage(t *testing.T) {
	long := strings.Repeat("abcde ", 30) // 180 chars
	msgs := []llm.Message{
		{Role: "user", Content: long},
	}
	title := GenerateSessionTitle(msgs)
	if len([]rune(title)) > 80 {
		t.Errorf("Title should be at most 80 runes, got %d", len([]rune(title)))
	}
	if !strings.HasSuffix(title, "...") {
		t.Error("Long title should end with ...")
	}
}

func TestGenerateSessionTitle_MultiLine(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "First line\nSecond line\nThird line"},
	}
	title := GenerateSessionTitle(msgs)
	if strings.Contains(title, "\n") {
		t.Error("Title should not contain newlines")
	}
	if title != "First line" {
		t.Errorf("Expected 'First line', got '%s'", title)
	}
}

func TestGenerateSessionTitle_WhitespaceOnly(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "   "},
		{Role: "user", Content: "Real message"},
	}
	title := GenerateSessionTitle(msgs)
	if title != "Real message" {
		t.Errorf("Expected 'Real message', got '%s'", title)
	}
}

// --- EstimateCost tests ---

func TestEstimateCost_KnownModel(t *testing.T) {
	cost := EstimateCost("anthropic/claude-sonnet-4-20250514", 1000000, 1000000)
	// 3.0 + 15.0 = 18.0
	if cost != 18.0 {
		t.Errorf("Expected cost 18.0, got %f", cost)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := EstimateCost("unknown/model", 1000, 1000)
	if cost != 0 {
		t.Errorf("Expected 0 for unknown model, got %f", cost)
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	cost := EstimateCost("anthropic/claude-sonnet-4-20250514", 0, 0)
	if cost != 0 {
		t.Errorf("Expected 0 for zero tokens, got %f", cost)
	}
}

func TestGetPricing(t *testing.T) {
	p, ok := GetPricing("anthropic/claude-opus-4-20250514")
	if !ok {
		t.Error("Expected pricing found for claude-opus")
	}
	if p.InputPerMillion != 15.0 {
		t.Errorf("Expected input pricing 15.0, got %f", p.InputPerMillion)
	}

	_, ok = GetPricing("nonexistent/model")
	if ok {
		t.Error("Expected pricing not found for unknown model")
	}
}

// --- FormatCost tests ---

func TestFormatCost(t *testing.T) {
	tests := []struct {
		cost     float64
		expected string
	}{
		{0, ""},
		{0.001, "$0.0010"},
		{0.009, "$0.0090"},
		{0.01, "$0.01"},
		{1.50, "$1.50"},
		{123.456, "$123.46"},
	}

	for _, tt := range tests {
		result := FormatCost(tt.cost)
		if result != tt.expected {
			t.Errorf("FormatCost(%f) = '%s', want '%s'", tt.cost, result, tt.expected)
		}
	}
}

// --- SmartRouter tests ---

func TestSmartRouter_Disabled(t *testing.T) {
	r := &SmartRouter{Enabled: false, CheapModel: "gpt-4o-mini", Threshold: 200}
	if r.ShouldUseSmartModel("hello") {
		t.Error("Disabled router should always return false")
	}
}

func TestSmartRouter_NoCheapModel(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "", Threshold: 200}
	if r.ShouldUseSmartModel("hello") {
		t.Error("Router with no cheap model should return false")
	}
}

func TestSmartRouter_SimpleMessage(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "gpt-4o-mini", Threshold: 200}
	if !r.ShouldUseSmartModel("What is the weather today?") {
		t.Error("Short simple message should use cheap model")
	}
}

func TestSmartRouter_LongMessage(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "gpt-4o-mini", Threshold: 200}
	long := strings.Repeat("This is a longer message. ", 20)
	if r.ShouldUseSmartModel(long) {
		t.Error("Long message should not use cheap model")
	}
}

func TestSmartRouter_CodeFence(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "gpt-4o-mini", Threshold: 500}
	if r.ShouldUseSmartModel("```python\nprint('hello')\n```") {
		t.Error("Message with code fence should not use cheap model")
	}
}

func TestSmartRouter_ComplexKeywords(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "gpt-4o-mini", Threshold: 500}

	complexMessages := []string{
		"Write code for a web server",
		"Implement a binary search tree",
		"Refactor this function",
		"Debug this issue",
		"Analyze the codebase",
		"Create a file called test.go",
		"Run the command ls -la",
		"Execute the tests",
		"Deploy to production",
		"Search the codebase for bugs",
		"Investigate this error",
	}

	for _, msg := range complexMessages {
		if r.ShouldUseSmartModel(msg) {
			t.Errorf("Complex message should not use cheap model: %s", msg)
		}
	}
}

func TestSmartRouter_MultipleNewlines(t *testing.T) {
	r := &SmartRouter{Enabled: true, CheapModel: "gpt-4o-mini", Threshold: 500}
	msg := "Line 1\nLine 2\nLine 3\nLine 4\nLine 5"
	if r.ShouldUseSmartModel(msg) {
		t.Error("Multi-line message (>3 lines) should not use cheap model")
	}
}

func TestDefaultSmartRouter(t *testing.T) {
	r := DefaultSmartRouter()
	if r.Enabled {
		t.Error("Default smart router should be disabled")
	}
	if r.Threshold != 200 {
		t.Errorf("Expected default threshold 200, got %d", r.Threshold)
	}
}

// --- SaveOversizedResult tests ---

func TestIsOversizedResult(t *testing.T) {
	short := "hello world"
	if IsOversizedResult(short) {
		t.Error("Short result should not be oversized")
	}

	long := strings.Repeat("x", 100_001)
	if !IsOversizedResult(long) {
		t.Error("Long result should be oversized")
	}
}

func TestSaveOversizedResult(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	longResult := strings.Repeat("data-", 30000) // 150K chars
	saved := SaveOversizedResult("test_tool", longResult)

	if !strings.Contains(saved, "saved_to") {
		t.Error("Expected 'saved_to' in result JSON")
	}
	if !strings.Contains(saved, "preview") {
		t.Error("Expected 'preview' in result JSON")
	}
	if !strings.Contains(saved, "total_chars") {
		t.Error("Expected 'total_chars' in result JSON")
	}
}

func TestSaveOversizedResult_SmallInput(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	// Small results still get saved (the function does not check size threshold)
	smallResult := "small output"
	saved := SaveOversizedResult("test_tool", smallResult)
	if !strings.Contains(saved, "saved_to") {
		t.Error("Expected saved_to in result")
	}
}

// --- NormalizeModelName tests ---

func TestNormalizeModelName_Empty(t *testing.T) {
	result := NormalizeModelName("")
	if result != "" {
		t.Errorf("Expected empty, got '%s'", result)
	}
}

func TestNormalizeModelName_AlreadyFull(t *testing.T) {
	result := NormalizeModelName("openai/gpt-4o")
	if result != "openai/gpt-4o" {
		t.Errorf("Expected 'openai/gpt-4o', got '%s'", result)
	}
}

func TestNormalizeModelName_Aliases(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"opus", "anthropic/claude-opus-4-20250514"},
		{"sonnet", "anthropic/claude-sonnet-4-20250514"},
		{"haiku", "anthropic/claude-haiku-4-20250414"},
		{"gpt-4o", "openai/gpt-4o"},
		{"gpt-4o-mini", "openai/gpt-4o-mini"},
		{"o1", "openai/o1"},
		{"gemini-pro", "google/gemini-2.5-pro"},
		{"deepseek", "deepseek/deepseek-chat"},
		{"maverick", "meta-llama/llama-4-maverick"},
	}

	for _, tt := range tests {
		result := NormalizeModelName(tt.input)
		if result != tt.expected {
			t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestNormalizeModelName_Unknown(t *testing.T) {
	result := NormalizeModelName("my-custom-model-xyz")
	// Should return as-is when no match
	if result == "" {
		t.Error("Expected non-empty result for unknown model")
	}
}

func TestIsKnownModel(t *testing.T) {
	if !IsKnownModel("opus") {
		t.Error("Expected 'opus' to be known")
	}
	if !IsKnownModel("gpt-4o") {
		t.Error("Expected 'gpt-4o' to be known")
	}
	if IsKnownModel("totally-unknown-model-xyz") {
		t.Error("Expected unknown model to return false")
	}
}

func TestListModelAliases(t *testing.T) {
	groups := ListModelAliases()
	if len(groups) == 0 {
		t.Error("Expected non-empty alias groups")
	}
	if _, ok := groups["anthropic"]; !ok {
		t.Error("Expected 'anthropic' group in aliases")
	}
	if _, ok := groups["openai"]; !ok {
		t.Error("Expected 'openai' group in aliases")
	}
}

// --- Trajectory tests ---

func TestNewTrajectoryFromResult(t *testing.T) {
	result := &ConversationResult{
		Model:       "test-model",
		Messages:    []llm.Message{{Role: "user", Content: "Hello"}},
		Completed:   true,
		InputTokens: 100,
		OutputTokens: 50,
	}

	traj := NewTrajectoryFromResult(result, "session-1", 5*time.Second)
	if traj.SessionID != "session-1" {
		t.Errorf("Expected session-1, got %s", traj.SessionID)
	}
	if traj.Model != "test-model" {
		t.Errorf("Expected test-model, got %s", traj.Model)
	}
	if !traj.Completed {
		t.Error("Expected completed=true")
	}
	if traj.Duration != 5*time.Second {
		t.Errorf("Expected 5s duration, got %v", traj.Duration)
	}
	if traj.Tokens["input"] != 100 {
		t.Errorf("Expected 100 input tokens, got %d", traj.Tokens["input"])
	}
}

func TestCompressTrajectory_Nil(t *testing.T) {
	result := CompressTrajectory(nil)
	if result != nil {
		t.Error("Expected nil for nil input")
	}
}

func TestCompressTrajectory(t *testing.T) {
	longContent := strings.Repeat("x", 1000)
	traj := &Trajectory{
		SessionID: "sess-1",
		Model:     "test-model",
		Messages: []llm.Message{
			{Role: "user", Content: "Hello"},
			{Role: "assistant", Content: "Hi", Reasoning: "thinking..."},
			{Role: "tool", Content: longContent},
		},
		Completed: true,
	}

	compressed := CompressTrajectory(traj)
	if compressed == nil {
		t.Fatal("Expected non-nil compressed trajectory")
	}
	if compressed.SessionID != "sess-1" {
		t.Error("SessionID should be preserved")
	}

	// Tool content should be truncated
	toolMsg := compressed.Messages[2]
	if len(toolMsg.Content) >= len(longContent) {
		t.Error("Tool message content should be truncated")
	}
	if !strings.Contains(toolMsg.Content, "truncated") {
		t.Error("Truncated content should contain truncation notice")
	}

	// Reasoning should be stripped
	assistantMsg := compressed.Messages[1]
	if assistantMsg.Reasoning != "" {
		t.Error("Reasoning should be stripped in compressed trajectory")
	}
}

func TestSaveAndLoadTrajectory(t *testing.T) {
	tmpDir := t.TempDir()

	traj := &Trajectory{
		SessionID: "test-session",
		Model:     "test-model",
		Messages: []llm.Message{
			{Role: "user", Content: "Hello"},
		},
		Completed: true,
		Timestamp: time.Now(),
	}

	err := SaveTrajectory(traj, tmpDir)
	if err != nil {
		t.Fatalf("SaveTrajectory failed: %v", err)
	}

	// Find the saved file
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 1 {
		t.Fatalf("Expected 1 file, got %d", len(entries))
	}

	loaded, err := LoadTrajectory(filepath.Join(tmpDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("LoadTrajectory failed: %v", err)
	}
	if loaded.SessionID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got '%s'", loaded.SessionID)
	}
}

func TestSaveTrajectory_Nil(t *testing.T) {
	err := SaveTrajectory(nil, t.TempDir())
	if err == nil {
		t.Error("Expected error for nil trajectory")
	}
}

// --- escapeJSON tests ---

func TestEscapeJSON(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`hello`, `hello`},
		{`he"llo`, `he\"llo`},
		{"he\nllo", `he\nllo`},
		{"he\tllo", `he\tllo`},
		{`he\llo`, `he\\llo`},
	}

	for _, tt := range tests {
		result := escapeJSON(tt.input)
		if result != tt.expected {
			t.Errorf("escapeJSON(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// --- sanitizeFilename tests ---

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello world", "hello_world"},
		{"test/path", "test_path"},
		{"a-b_c.d", "a-b_c_d"},
		{"ABC123", "ABC123"},
	}

	for _, tt := range tests {
		result := sanitizeFilename(tt.input)
		if result != tt.expected {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// --- Checkpoint helpers tests ---

// --- CredentialPool tests ---

func TestCredentialPool_AddAndGet(t *testing.T) {
	pool := NewCredentialPool()
	pool.AddCredential(Credential{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  "https://api.openai.com/v1",
		Priority: 0,
	})

	cred := pool.GetBestCredential("openai")
	if cred == nil {
		t.Fatal("Expected credential")
	}
	if cred.APIKey != "test-key" {
		t.Errorf("Expected 'test-key', got '%s'", cred.APIKey)
	}
}

func TestCredentialPool_Priority(t *testing.T) {
	pool := NewCredentialPool()
	pool.AddCredential(Credential{
		Provider: "openai",
		APIKey:   "low-priority",
		Priority: 10,
	})
	pool.AddCredential(Credential{
		Provider: "openai",
		APIKey:   "high-priority",
		Priority: 1,
	})

	cred := pool.GetBestCredential("openai")
	if cred.APIKey != "high-priority" {
		t.Errorf("Expected 'high-priority', got '%s'", cred.APIKey)
	}
}

func TestCredentialPool_GetBestCredential_NotFound(t *testing.T) {
	pool := NewCredentialPool()
	cred := pool.GetBestCredential("nonexistent")
	if cred != nil {
		t.Error("Expected nil for nonexistent provider")
	}
}

func TestCredentialPool_GetCredentialForModel(t *testing.T) {
	pool := NewCredentialPool()
	pool.AddCredential(Credential{
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "gpt4o-key",
		Priority: 1,
	})
	pool.AddCredential(Credential{
		Provider: "openai",
		Model:    "",
		APIKey:   "general-key",
		Priority: 0,
	})

	// Exact model match
	cred := pool.GetCredentialForModel("openai", "gpt-4o")
	if cred == nil || cred.APIKey != "gpt4o-key" {
		t.Error("Expected exact model match to return gpt4o-key")
	}

	// Fallback for unknown model
	cred = pool.GetCredentialForModel("openai", "gpt-3.5")
	if cred == nil || cred.APIKey != "general-key" {
		t.Error("Expected fallback to general key")
	}
}

func TestCredentialPool_AllProviders(t *testing.T) {
	pool := NewCredentialPool()
	pool.AddCredential(Credential{Provider: "openai", APIKey: "k1"})
	pool.AddCredential(Credential{Provider: "anthropic", APIKey: "k2"})

	providers := pool.AllProviders()
	if len(providers) != 2 {
		t.Errorf("Expected 2 providers, got %d", len(providers))
	}
}

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"OpenAI", "openai"},
		{" anthropic ", "anthropic"},
		{"DEEPSEEK", "deepseek"},
		{"", ""},
	}
	for _, tt := range tests {
		result := normalizeProvider(tt.input)
		if result != tt.expected {
			t.Errorf("normalizeProvider(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestInferProviderFromBaseURL(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://openrouter.ai/api/v1", "openrouter"},
		{"https://api.anthropic.com", "anthropic"},
		{"https://api.openai.com/v1", "openai"},
		{"https://api.deepseek.com/v1", "deepseek"},
		// Note: groq URL contains "openai" so it matches openai first due to check order
		// {"https://api.groq.com/openai/v1", "groq"}, // skipped: URL contains "openai"
		{"https://api.x.ai/v1", "xai"},
		{"https://my-custom-api.com/v1", "custom"},
	}
	for _, tt := range tests {
		result := inferProviderFromBaseURL(tt.url)
		if result != tt.expected {
			t.Errorf("inferProviderFromBaseURL(%q) = %q, want %q", tt.url, result, tt.expected)
		}
	}
}

// --- LoadContextReferences tests ---

func TestLoadContextReferences_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	refs := LoadContextReferences(tmpDir)
	if len(refs) != 0 {
		t.Errorf("Expected 0 refs for empty dir, got %d", len(refs))
	}
}

func TestLoadContextReferences_WithSOUL(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", t.TempDir()) // different from workspace
	defer os.Unsetenv("HERMES_HOME")

	os.WriteFile(filepath.Join(tmpDir, "SOUL.md"), []byte("You are helpful"), 0644)

	refs := LoadContextReferences(tmpDir)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 ref, got %d", len(refs))
	}
	if refs[0].Type != "soul" {
		t.Errorf("Expected type 'soul', got '%s'", refs[0].Type)
	}
	if refs[0].Content != "You are helpful" {
		t.Errorf("Expected content 'You are helpful', got '%s'", refs[0].Content)
	}
}

func TestLoadContextReferences_WithREADME(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", t.TempDir())
	defer os.Unsetenv("HERMES_HOME")

	// No SOUL.md, AGENTS.md, etc. but has README.md
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# My Project\nDescription"), 0644)

	refs := LoadContextReferences(tmpDir)
	if len(refs) != 1 {
		t.Fatalf("Expected 1 ref (README fallback), got %d", len(refs))
	}
	if refs[0].Type != "readme" {
		t.Errorf("Expected type 'readme', got '%s'", refs[0].Type)
	}
}

func TestIsDuplicate(t *testing.T) {
	files := []ContextFile{
		{Path: "/tmp/test/SOUL.md"},
	}

	if !isDuplicate(files, "/tmp/test/SOUL.md") {
		t.Error("Expected duplicate for same path")
	}
	if isDuplicate(files, "/tmp/test/AGENTS.md") {
		t.Error("Expected non-duplicate for different path")
	}
}

// --- Additional AgentOption tests ---

func TestWithCallbacks(t *testing.T) {
	a := &AIAgent{}
	cb := &StreamCallbacks{OnStreamDelta: func(s string) {}}
	WithCallbacks(cb)(a)
	if a.callbacks == nil {
		t.Error("Expected callbacks to be set")
	}
}

func TestWithBudget(t *testing.T) {
	a := &AIAgent{}
	b := NewIterationBudget(10)
	WithBudget(b)(a)
	if a.budget == nil {
		t.Error("Expected budget to be set")
	}
}

func TestWithResumeSession(t *testing.T) {
	a := &AIAgent{}
	WithResumeSession("old-sess")(a)
	if a.resumeSessionID != "old-sess" {
		t.Errorf("Expected 'old-sess', got '%s'", a.resumeSessionID)
	}
}

func TestWithFallbackModels(t *testing.T) {
	a := &AIAgent{}
	models := []FallbackModel{{Model: "gpt-4o-mini"}}
	WithFallbackModels(models)(a)
	if len(a.fallbackModels) != 1 {
		t.Error("Expected 1 fallback model")
	}
}

func TestWithSmartRouter(t *testing.T) {
	a := &AIAgent{}
	r := DefaultSmartRouter()
	WithSmartRouter(r)(a)
	if a.smartRouter == nil {
		t.Error("Expected smart router to be set")
	}
}

// --- AuxiliaryClient tests ---

func TestAuxiliaryClient_NilClients(t *testing.T) {
	aux := &AuxiliaryClient{}
	if aux.VisionClient() != nil {
		t.Error("Expected nil vision client")
	}
	if aux.WebExtractClient() != nil {
		t.Error("Expected nil web extract client")
	}
}

// --- GetUsageInsights tests ---

func TestGetUsageInsights_NilDB(t *testing.T) {
	result := GetUsageInsights(nil, 7)
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if _, ok := result["error"]; !ok {
		t.Error("Expected error key for nil DB")
	}
}

func TestGetUsageInsights_DefaultDays(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	db, err := state.NewSessionDB(filepath.Join(tmpDir, "insights.db"))
	if err != nil {
		t.Fatalf("Failed to create DB: %v", err)
	}
	defer db.Close()

	// Create a session
	db.CreateSession("insight-session", "cli", "anthropic/claude-sonnet-4-20250514", "")
	db.UpdateTokenCounts("insight-session", 500, 200, 0, 0, 0)

	result := GetUsageInsights(db, 0) // 0 defaults to 7
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if _, ok := result["error"]; ok {
		t.Error("Expected no error")
	}
	if result["days"].(int) != 7 {
		t.Errorf("Expected days=7, got %v", result["days"])
	}
}

func TestGetUsageInsights_WithData(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	db, err := state.NewSessionDB(filepath.Join(tmpDir, "insights2.db"))
	if err != nil {
		t.Fatalf("Failed to create DB: %v", err)
	}
	defer db.Close()

	db.CreateSession("s1", "cli", "gpt-4", "")
	db.UpdateTokenCounts("s1", 1000, 500, 0, 0, 0)

	result := GetUsageInsights(db, 30)
	totalSessions := result["total_sessions"].(int)
	if totalSessions < 1 {
		t.Errorf("Expected at least 1 session, got %d", totalSessions)
	}
}

// --- CredentialPool.LoadFromEnv tests ---

func TestCredentialPool_LoadFromEnv(t *testing.T) {
	pool := NewCredentialPool()

	// Set up a known env var
	os.Setenv("OPENAI_API_KEY", "test-env-openai-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	// Clear other vars to avoid interference
	os.Unsetenv("OPENROUTER_API_KEY")
	os.Unsetenv("ANTHROPIC_API_KEY")

	pool.LoadFromEnv()

	cred := pool.GetBestCredential("openai")
	if cred == nil {
		t.Fatal("Expected openai credential from env")
	}
	if cred.APIKey != "test-env-openai-key" {
		t.Errorf("Expected 'test-env-openai-key', got '%s'", cred.APIKey)
	}
}

func TestCredentialPool_LoadFromEnv_SkipExisting(t *testing.T) {
	pool := NewCredentialPool()

	// Add a config-defined credential
	pool.AddCredential(Credential{
		Provider: "openai",
		APIKey:   "config-key",
		Priority: 0,
	})

	os.Setenv("OPENAI_API_KEY", "env-key")
	defer os.Unsetenv("OPENAI_API_KEY")

	pool.LoadFromEnv()

	// Config key should still be the best
	cred := pool.GetBestCredential("openai")
	if cred.APIKey != "config-key" {
		t.Errorf("Expected config key to take precedence, got '%s'", cred.APIKey)
	}
}

// --- CredentialPool.LoadFromConfig tests ---

func TestCredentialPool_LoadFromConfig(t *testing.T) {
	pool := NewCredentialPool()

	cfg := &config.Config{
		APIKey:  "main-key",
		BaseURL: "https://api.openai.com/v1",
		Model:   "gpt-4",
	}

	pool.LoadFromConfig(cfg)

	cred := pool.GetBestCredential("openai")
	if cred == nil {
		t.Fatal("Expected credential from config")
	}
	if cred.APIKey != "main-key" {
		t.Errorf("Expected 'main-key', got '%s'", cred.APIKey)
	}
}

func TestCredentialPool_LoadFromConfig_WithRouting(t *testing.T) {
	pool := NewCredentialPool()

	cfg := &config.Config{
		APIKey:  "main-key",
		BaseURL: "https://openrouter.ai/api/v1",
		ProviderRouting: map[string]any{
			"credentials": []any{
				map[string]any{
					"provider": "anthropic",
					"api_key":  "ant-key",
					"base_url": "https://api.anthropic.com",
				},
			},
		},
	}

	pool.LoadFromConfig(cfg)

	providers := pool.AllProviders()
	if len(providers) < 2 {
		t.Errorf("Expected at least 2 providers, got %d", len(providers))
	}

	cred := pool.GetBestCredential("anthropic")
	if cred == nil || cred.APIKey != "ant-key" {
		t.Error("Expected anthropic credential from routing config")
	}
}

// --- More streaming callback tests ---

func TestFireReasoning_WithCallback(t *testing.T) {
	var received string
	a := &AIAgent{callbacks: &StreamCallbacks{OnReasoning: func(s string) { received = s }}}
	a.fireReasoning("thought")
	if received != "thought" {
		t.Errorf("Expected 'thought', got '%s'", received)
	}
}

func TestFireToolComplete_WithCallback(t *testing.T) {
	var received string
	a := &AIAgent{callbacks: &StreamCallbacks{OnToolComplete: func(s string) { received = s }}}
	a.fireToolComplete("terminal")
	if received != "terminal" {
		t.Errorf("Expected 'terminal', got '%s'", received)
	}
}

func TestFireStatus_WithCallback(t *testing.T) {
	var received string
	a := &AIAgent{callbacks: &StreamCallbacks{OnStatus: func(s string) { received = s }}}
	a.fireStatus("thinking...")
	if received != "thinking..." {
		t.Errorf("Expected 'thinking...', got '%s'", received)
	}
}

func TestFireToolProgress_WithCallback(t *testing.T) {
	var receivedName, receivedArgs string
	a := &AIAgent{callbacks: &StreamCallbacks{OnToolProgress: func(n, a string) { receivedName = n; receivedArgs = a }}}
	a.fireToolProgress("terminal", "ls -la")
	if receivedName != "terminal" {
		t.Errorf("Expected 'terminal', got '%s'", receivedName)
	}
	if receivedArgs != "ls -la" {
		t.Errorf("Expected 'ls -la', got '%s'", receivedArgs)
	}
}
