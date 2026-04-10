package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResultStore_StoreAndRetrieve(t *testing.T) {
	dir := t.TempDir()
	rs := &ResultStore{cacheDir: dir}

	// Below threshold — should return nil.
	ref := rs.Store("short", 100)
	if ref != nil {
		t.Error("expected nil for content below threshold")
	}

	// Above threshold — should store.
	content := strings.Repeat("a", 1000)
	ref = rs.Store(content, 100)
	if ref == nil {
		t.Fatal("expected non-nil ref for large content")
	}
	if ref.Size != 1000 {
		t.Errorf("Size = %d, want 1000", ref.Size)
	}
	if !ref.Truncated {
		t.Error("expected Truncated = true for 1000-char content with 500-char preview")
	}

	// Retrieve.
	got, err := rs.Retrieve(ref.ID)
	if err != nil {
		t.Fatalf("retrieve error: %v", err)
	}
	if got != content {
		t.Error("retrieved content doesn't match stored content")
	}
}

func TestResultStore_Retrieve_NotFound(t *testing.T) {
	dir := t.TempDir()
	rs := &ResultStore{cacheDir: dir}

	_, err := rs.Retrieve("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent result")
	}
}

func TestResultStore_Retrieve_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	rs := &ResultStore{cacheDir: dir}

	_, err := rs.Retrieve("../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}

func TestResultStore_Cleanup(t *testing.T) {
	dir := t.TempDir()
	rs := &ResultStore{cacheDir: dir}

	// Create an old file.
	oldFile := filepath.Join(dir, "old_result.txt")
	os.WriteFile(oldFile, []byte("old"), 0644) //nolint:errcheck
	// Backdate modification time.
	oldTime := time.Now().Add(-2 * time.Hour)
	os.Chtimes(oldFile, oldTime, oldTime) //nolint:errcheck

	// Create a new file.
	newFile := filepath.Join(dir, "new_result.txt")
	os.WriteFile(newFile, []byte("new"), 0644) //nolint:errcheck

	removed := rs.Cleanup(1 * time.Hour)
	if removed != 1 {
		t.Errorf("Cleanup removed %d files, want 1", removed)
	}

	// Old file should be gone.
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("old file should have been removed")
	}
	// New file should remain.
	if _, err := os.Stat(newFile); os.IsNotExist(err) {
		t.Error("new file should still exist")
	}
}
