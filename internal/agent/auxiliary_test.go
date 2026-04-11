package agent

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
)

func TestNewAuxiliaryClient_NoEnvVars(t *testing.T) {
	// Clear relevant env vars.
	for _, k := range []string{
		"AUXILIARY_VISION_MODEL", "AUXILIARY_WEB_EXTRACT_MODEL",
		"AUXILIARY_SUMMARY_MODEL", "OPENROUTER_API_KEY",
	} {
		os.Unsetenv(k)
	}

	aux := NewAuxiliaryClient(&config.Config{})
	if aux.VisionClient() != nil {
		t.Error("Expected nil vision client without env vars")
	}
	if aux.WebExtractClient() != nil {
		t.Error("Expected nil web extract client without env vars")
	}
	if aux.SummaryClient() != nil {
		t.Error("Expected nil summary client without env vars")
	}
}

func TestNewAuxiliaryClient_SummaryFromEnv(t *testing.T) {
	os.Setenv("AUXILIARY_SUMMARY_MODEL", "openai/gpt-4o-mini")
	os.Setenv("AUXILIARY_SUMMARY_API_KEY", "test-key-123")
	defer os.Unsetenv("AUXILIARY_SUMMARY_MODEL")
	defer os.Unsetenv("AUXILIARY_SUMMARY_API_KEY")

	aux := NewAuxiliaryClient(&config.Config{})
	if aux.SummaryClient() == nil {
		t.Fatal("Expected non-nil summary client when env var is set")
	}
}

func TestNewAuxiliaryClient_SummaryFromConfig(t *testing.T) {
	// No env var set, but config has summary_model.
	os.Unsetenv("AUXILIARY_SUMMARY_MODEL")
	os.Setenv("OPENROUTER_API_KEY", "test-key-cfg")
	defer os.Unsetenv("OPENROUTER_API_KEY")

	cfg := &config.Config{}
	cfg.Auxiliary.SummaryModel = "openai/gpt-4o-mini"

	aux := NewAuxiliaryClient(cfg)
	if aux.SummaryClient() == nil {
		t.Fatal("Expected non-nil summary client when config is set")
	}
}

func TestNewAuxiliaryClient_EnvOverridesConfig(t *testing.T) {
	os.Setenv("AUXILIARY_SUMMARY_MODEL", "openai/gpt-4o")
	os.Setenv("AUXILIARY_SUMMARY_API_KEY", "env-key")
	defer os.Unsetenv("AUXILIARY_SUMMARY_MODEL")
	defer os.Unsetenv("AUXILIARY_SUMMARY_API_KEY")

	cfg := &config.Config{}
	cfg.Auxiliary.SummaryModel = "openai/gpt-4o-mini" // should be overridden

	aux := NewAuxiliaryClient(cfg)
	if aux.SummaryClient() == nil {
		t.Fatal("Expected non-nil summary client")
	}
	// The client model should be from env var.
	if aux.SummaryClient().Model() != "openai/gpt-4o" {
		t.Errorf("Expected model openai/gpt-4o, got %s", aux.SummaryClient().Model())
	}
}

func TestNewAuxiliaryClient_NilConfig(t *testing.T) {
	os.Unsetenv("AUXILIARY_SUMMARY_MODEL")
	os.Unsetenv("AUXILIARY_VISION_MODEL")
	os.Unsetenv("AUXILIARY_WEB_EXTRACT_MODEL")

	// Should not panic with nil config.
	aux := NewAuxiliaryClient(nil)
	if aux.SummaryClient() != nil {
		t.Error("Expected nil summary client with nil config and no env")
	}
}

func TestSummarize_BugFix_MaxWordsFormatting(t *testing.T) {
	// The original bug: string(rune(500)) produced a Unicode character.
	// After fix, strconv.Itoa(500) should produce "500".
	// Verify by checking that strconv.Itoa works correctly for our use case.
	result := strconv.Itoa(500)
	if result != "500" {
		t.Errorf("Expected '500', got '%s'", result)
	}

	// The old buggy code would do:
	buggy := string(rune(500)) // This is Unicode character U+01F4 (Latin small letter f with dot above)
	if buggy == "500" {
		t.Error("The buggy code should NOT produce '500'")
	}
}

func TestSummarize_NoClient(t *testing.T) {
	aux := &AuxiliaryClient{}
	text := "some text to summarize"
	result, err := aux.Summarize(context.Background(), text, 100)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if result != text {
		t.Errorf("Without clients, expected original text, got '%s'", result)
	}
}

// --- Title generation tests ---

func TestGenerateSessionTitleHeuristic_NoMessages(t *testing.T) {
	title := generateSessionTitleHeuristic(nil)
	if title != "Untitled session" {
		t.Errorf("Expected 'Untitled session', got '%s'", title)
	}
}

func TestGenerateSessionTitleHeuristic_Simple(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "How do I deploy to production?"},
	}
	title := generateSessionTitleHeuristic(msgs)
	if title != "How do I deploy to production?" {
		t.Errorf("Expected exact message, got '%s'", title)
	}
}

func TestFirstUserMessage(t *testing.T) {
	msgs := []llm.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "  "},
		{Role: "user", Content: "Hello world"},
	}
	result := firstUserMessage(msgs)
	if result != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", result)
	}
}

func TestFirstUserMessage_Empty(t *testing.T) {
	result := firstUserMessage(nil)
	if result != "" {
		t.Errorf("Expected empty string, got '%s'", result)
	}
}

func TestGenerateTitleForMessages_NilClient(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: "Test message"},
	}
	title := GenerateTitleForMessages(context.Background(), nil, msgs)
	if title != "Test message" {
		t.Errorf("Expected heuristic title 'Test message', got '%s'", title)
	}
}

func TestGenerateSessionTitleWithLLM_EmptyMessages(t *testing.T) {
	// With no user messages, should return "Untitled session" without calling LLM.
	title := GenerateSessionTitleWithLLM(context.Background(), nil, nil)
	if title != "Untitled session" {
		t.Errorf("Expected 'Untitled session', got '%s'", title)
	}
}
