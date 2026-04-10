package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateAPIKey_UnknownProvider(t *testing.T) {
	err := ValidateAPIKey("nonexistent", "key")
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestSaveAndRemoveAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	if err := SaveAPIKey("openai", "sk-test-123"); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if got := string(data); got != "OPENAI_API_KEY=sk-test-123\n" {
		t.Errorf("unexpected .env content: %q", got)
	}

	if err := SaveAPIKey("openai", "sk-new-456"); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(tmpDir, ".env"))
	if got := string(data); got != "OPENAI_API_KEY=sk-new-456\n" {
		t.Errorf("unexpected .env after update: %q", got)
	}

	if err := RemoveAPIKey("openai"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(tmpDir, ".env"))
	if got := string(data); got != "\n" {
		t.Errorf("expected empty .env after remove, got: %q", got)
	}
}

func TestSaveAPIKey_UnknownProvider(t *testing.T) {
	if err := SaveAPIKey("nonexistent", "key"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestRemoveAPIKey_NoEnvFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	if err := RemoveAPIKey("openai"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAuthStatus(t *testing.T) {
	statuses := AuthStatus()
	if len(statuses) == 0 {
		t.Error("expected at least one provider in auth status")
	}
	providers := ListProviders()
	if len(statuses) != len(providers) {
		t.Errorf("status count %d != provider count %d", len(statuses), len(providers))
	}
}

func TestReadEnvLines_NonExistent(t *testing.T) {
	if _, err := readEnvLines("/nonexistent/path/.env"); err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestFetchModels_UnknownProvider(t *testing.T) {
	if _, err := FetchModels("nonexistent", "key"); err == nil {
		t.Error("expected error for unknown provider")
	}
}
