package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- Manifest round-trip ---

func TestLoadManifest_NonExistent(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("expected version 1, got %d", m.Version)
	}
	if m.Skills == nil {
		t.Fatal("expected non-nil Skills map")
	}
	if len(m.Skills) != 0 {
		t.Errorf("expected empty skills map, got %d entries", len(m.Skills))
	}
}

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".manifest.json")

	now := time.Now().Truncate(time.Second)
	original := &SkillManifest{
		Version: 1,
		Skills: map[string]ManifestEntry{
			"my-skill": {
				Hash:         "abc123",
				Source:       "builtin",
				InstalledAt:  now,
				UserModified: false,
				SourceCommit: "deadbeef",
			},
			"custom-skill": {
				Hash:         "def456",
				Source:       "hub",
				InstalledAt:  now,
				UserModified: true,
			},
		},
	}

	if err := SaveManifest(path, original); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if loaded.Version != original.Version {
		t.Errorf("version: got %d, want %d", loaded.Version, original.Version)
	}
	if len(loaded.Skills) != len(original.Skills) {
		t.Fatalf("skills count: got %d, want %d", len(loaded.Skills), len(original.Skills))
	}

	entry := loaded.Skills["my-skill"]
	if entry.Hash != "abc123" {
		t.Errorf("hash: got %q, want %q", entry.Hash, "abc123")
	}
	if entry.Source != "builtin" {
		t.Errorf("source: got %q, want %q", entry.Source, "builtin")
	}
	if entry.SourceCommit != "deadbeef" {
		t.Errorf("source_commit: got %q, want %q", entry.SourceCommit, "deadbeef")
	}

	custom := loaded.Skills["custom-skill"]
	if !custom.UserModified {
		t.Error("expected custom-skill to be user_modified")
	}
}

func TestLoadManifest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".manifest.json")
	os.WriteFile(path, []byte("{invalid json"), 0644) //nolint:errcheck

	_, err := LoadManifest(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadManifest_NilSkillsMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".manifest.json")
	// Write valid JSON with null skills.
	os.WriteFile(path, []byte(`{"version":1,"skills":null}`), 0644) //nolint:errcheck

	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Skills == nil {
		t.Error("expected non-nil Skills map after loading null")
	}
}

// --- Atomic write ---

func TestSaveManifest_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".manifest.json")

	m := &SkillManifest{
		Version: 1,
		Skills:  map[string]ManifestEntry{"s1": {Hash: "h1", Source: "builtin"}},
	}

	if err := SaveManifest(path, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify the file exists and is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var check SkillManifest
	if err := json.Unmarshal(data, &check); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if check.Skills["s1"].Hash != "h1" {
		t.Errorf("expected hash h1, got %q", check.Skills["s1"].Hash)
	}

	// Verify no temp files remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != ".manifest.json" {
			t.Errorf("unexpected leftover file: %s", e.Name())
		}
	}
}

func TestSaveManifest_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", ".manifest.json")

	m := &SkillManifest{Version: 1, Skills: make(map[string]ManifestEntry)}
	if err := SaveManifest(path, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist: %v", err)
	}
}

// --- HashFileContent ---

func TestHashFileContent(t *testing.T) {
	data := []byte("hello world")
	h1 := HashFileContent(data)
	h2 := HashFileContent(data)
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}

	h3 := HashFileContent([]byte("different"))
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}

	if len(h1) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(h1))
	}
}

// --- User-edit detection ---

func TestIsUserModified_NotInManifest(t *testing.T) {
	m := &SkillManifest{Version: 1, Skills: make(map[string]ManifestEntry)}
	if IsUserModified(m, "unknown-skill", "/tmp/fake") {
		t.Error("skill not in manifest should not be detected as modified")
	}
}

func TestIsUserModified_AlreadyMarked(t *testing.T) {
	m := &SkillManifest{
		Version: 1,
		Skills: map[string]ManifestEntry{
			"my-skill": {Hash: "abc", UserModified: true},
		},
	}
	if !IsUserModified(m, "my-skill", "/tmp/fake") {
		t.Error("expected UserModified=true to be detected")
	}
}

func TestIsUserModified_FileChanged(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")

	originalContent := []byte("# Original")
	os.WriteFile(skillPath, originalContent, 0644) //nolint:errcheck

	m := &SkillManifest{
		Version: 1,
		Skills: map[string]ManifestEntry{
			"test-skill": {Hash: HashFileContent(originalContent)},
		},
	}

	// Not modified yet.
	if IsUserModified(m, "test-skill", skillPath) {
		t.Error("file unchanged, should not be user-modified")
	}

	// Modify the file.
	os.WriteFile(skillPath, []byte("# User Edited"), 0644) //nolint:errcheck

	if !IsUserModified(m, "test-skill", skillPath) {
		t.Error("file changed, should be user-modified")
	}
}

func TestIsUserModified_FileUnreadable(t *testing.T) {
	m := &SkillManifest{
		Version: 1,
		Skills: map[string]ManifestEntry{
			"gone-skill": {Hash: "abc"},
		},
	}
	// File does not exist — not modified.
	if IsUserModified(m, "gone-skill", "/nonexistent/path") {
		t.Error("missing file should not be reported as modified")
	}
}

// --- Sync integration with manifest ---

func TestSyncBuiltinSkills_ManifestCreated(t *testing.T) {
	bundledDir := t.TempDir()
	installedDir := t.TempDir()

	// Create a bundled skill.
	skillDir := filepath.Join(bundledDir, "alpha")
	os.MkdirAll(skillDir, 0755)                                                     //nolint:errcheck
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Alpha v1"), 0644) //nolint:errcheck

	results, err := SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(results) != 1 || results[0].Action != "installed" {
		t.Fatalf("expected installed, got %+v", results)
	}

	// Manifest should exist now.
	manifestPath := filepath.Join(installedDir, ".manifest.json")
	m, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	entry, ok := m.Skills["alpha"]
	if !ok {
		t.Fatal("expected alpha in manifest")
	}
	if entry.Hash == "" {
		t.Error("expected non-empty hash in manifest")
	}
	if entry.Source != "builtin" {
		t.Errorf("expected source 'builtin', got %q", entry.Source)
	}
}

func TestSyncBuiltinSkills_SkipsUserModified(t *testing.T) {
	bundledDir := t.TempDir()
	installedDir := t.TempDir()

	// Create a bundled skill.
	skillDir := filepath.Join(bundledDir, "beta")
	os.MkdirAll(skillDir, 0755)                                                    //nolint:errcheck
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Beta v1"), 0644) //nolint:errcheck

	// Sync it once.
	_, err := SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("initial sync: %v", err)
	}

	// User modifies the installed file.
	installedPath := filepath.Join(installedDir, "beta", "SKILL.md")
	os.WriteFile(installedPath, []byte("# Beta (user-customized)"), 0644) //nolint:errcheck

	// Update bundled version.
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Beta v2"), 0644) //nolint:errcheck

	// Re-sync — should skip because user modified the file.
	results, err := SyncBuiltinSkills(bundledDir, installedDir)
	if err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Action != "skipped" {
		t.Errorf("expected 'skipped', got %q", results[0].Action)
	}

	// Verify user's content was preserved.
	data, _ := os.ReadFile(installedPath)
	if string(data) != "# Beta (user-customized)" {
		t.Errorf("user content was overwritten: %q", string(data))
	}
}

func TestSyncBuiltinSkills_UpdatesManifestHash(t *testing.T) {
	bundledDir := t.TempDir()
	installedDir := t.TempDir()

	skillDir := filepath.Join(bundledDir, "gamma")
	os.MkdirAll(skillDir, 0755) //nolint:errcheck

	v1 := []byte("# Gamma v1")
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), v1, 0644) //nolint:errcheck

	SyncBuiltinSkills(bundledDir, installedDir) //nolint:errcheck

	manifestPath := filepath.Join(installedDir, ".manifest.json")
	m1, _ := LoadManifest(manifestPath)
	hash1 := m1.Skills["gamma"].Hash

	// Update bundled skill.
	v2 := []byte("# Gamma v2")
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), v2, 0644) //nolint:errcheck

	SyncBuiltinSkills(bundledDir, installedDir) //nolint:errcheck

	m2, _ := LoadManifest(manifestPath)
	hash2 := m2.Skills["gamma"].Hash

	if hash1 == hash2 {
		t.Error("manifest hash should change after update")
	}
	if hash2 != HashFileContent(v2) {
		t.Error("manifest hash should match the new bundled content")
	}
}

// --- SyncFromHub stub ---

func TestSyncFromHub_Stub(t *testing.T) {
	err := SyncFromHub("https://example.com/skills-index.json")
	if err != nil {
		t.Errorf("expected no error from stub, got: %v", err)
	}
}
