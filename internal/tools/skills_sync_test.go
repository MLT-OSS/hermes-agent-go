package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncBuiltinSkills(t *testing.T) {
	bundledDir := t.TempDir()
	installedDir := t.TempDir()

	// Create a bundled skill.
	skillDir := filepath.Join(bundledDir, "test-skill")
	os.MkdirAll(skillDir, 0755)                                                        //nolint:errcheck
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill v1"), 0644) //nolint:errcheck

	// First sync — should install.
	results, err := SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "installed" {
		t.Errorf("expected 'installed', got %q", results[0].Action)
	}

	// Second sync — should be unchanged.
	results, err = SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if results[0].Action != "unchanged" {
		t.Errorf("expected 'unchanged', got %q", results[0].Action)
	}

	// Modify bundled — should update.
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Test Skill v2"), 0644) //nolint:errcheck
	results, err = SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("sync error: %v", err)
	}
	if results[0].Action != "updated" {
		t.Errorf("expected 'updated', got %q", results[0].Action)
	}
}

func TestSyncBuiltinSkills_NoBundledDir(t *testing.T) {
	_, err := SyncBuiltinSkills("/nonexistent/path", t.TempDir())
	if err == nil {
		t.Error("expected error for nonexistent bundled dir")
	}
}
