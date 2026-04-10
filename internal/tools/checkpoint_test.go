package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointManager_NewAndEnsure(t *testing.T) {
	dir := t.TempDir()

	cm := NewCheckpointManager(dir, 50)
	if cm.workDir != dir {
		t.Errorf("workDir = %q, want %q", cm.workDir, dir)
	}

	if err := cm.Ensure(); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}

	// Shadow repo should exist.
	shadowHead := filepath.Join(dir, ".hermes-checkpoints", "HEAD")
	if _, err := os.Stat(shadowHead); err != nil {
		t.Errorf("shadow repo HEAD not found: %v", err)
	}

	// Second call should be no-op.
	if err := cm.Ensure(); err != nil {
		t.Fatalf("second Ensure() error: %v", err)
	}
}

func TestCheckpointManager_SnapshotAndList(t *testing.T) {
	dir := t.TempDir()
	cm := NewCheckpointManager(dir, 50)

	if err := cm.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Create a file.
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	entry, err := cm.Snapshot("test checkpoint")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.Reason != "test checkpoint" {
		t.Errorf("reason = %q, want %q", entry.Reason, "test checkpoint")
	}

	// List should return it.
	entries, err := cm.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 entry")
	}
}

func TestCheckpointManager_SnapshotNoChanges(t *testing.T) {
	dir := t.TempDir()
	cm := NewCheckpointManager(dir, 50)
	if err := cm.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Create a file and snapshot.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("data"), 0644)
	_, _ = cm.Snapshot("first")

	// Second snapshot with no changes should return nil.
	entry, err := cm.Snapshot("no changes")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if entry != nil {
		t.Error("expected nil entry when no changes")
	}
}

func TestCheckpointManager_Disabled(t *testing.T) {
	dir := t.TempDir()
	cm := NewCheckpointManager(dir, 50)
	cm.enabled = false

	entry, err := cm.Snapshot("should not run")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if entry != nil {
		t.Error("expected nil when disabled")
	}
}

func TestCheckpointManager_Restore(t *testing.T) {
	dir := t.TempDir()
	cm := NewCheckpointManager(dir, 50)
	if err := cm.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// Create file and checkpoint.
	filePath := filepath.Join(dir, "test.txt")
	os.WriteFile(filePath, []byte("original"), 0644)
	entry, err := cm.Snapshot("before change")
	if err != nil || entry == nil {
		t.Fatalf("Snapshot: entry=%v, err=%v", entry, err)
	}

	// Modify file.
	os.WriteFile(filePath, []byte("modified"), 0644)

	// Restore.
	if err := cm.Restore(entry.ID); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// File should be original.
	data, _ := os.ReadFile(filePath)
	if string(data) != "original" {
		t.Errorf("restored content = %q, want %q", string(data), "original")
	}
}
