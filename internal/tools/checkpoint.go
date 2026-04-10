// Package tools provides checkpoint — workspace snapshot and rollback using a
// shadow git repository. Before destructive tool calls the checkpoint manager
// snapshots the working directory so the user can diff or restore.
package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CheckpointEntry records a single snapshot.
type CheckpointEntry struct {
	ID        string    `json:"id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

// CheckpointManager manages shadow-git snapshots of a working directory.
type CheckpointManager struct {
	mu           sync.Mutex
	workDir      string
	shadowRepo   string
	maxSnapshots int
	enabled      bool
}

// NewCheckpointManager creates a manager for the given working directory.
// maxSnapshots limits how many checkpoints are retained (0 = unlimited).
func NewCheckpointManager(workDir string, maxSnapshots int) *CheckpointManager {
	shadow := filepath.Join(workDir, ".hermes-checkpoints")
	return &CheckpointManager{
		workDir:      workDir,
		shadowRepo:   shadow,
		maxSnapshots: maxSnapshots,
		enabled:      true,
	}
}

// Ensure initialises the shadow repo if it does not exist.
func (cm *CheckpointManager) Ensure() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, err := os.Stat(filepath.Join(cm.shadowRepo, "HEAD")); err == nil {
		return nil // already initialised
	}

	if err := os.MkdirAll(cm.shadowRepo, 0755); err != nil {
		return fmt.Errorf("create shadow repo dir: %w", err)
	}

	if err := cm.git("init", "--bare"); err != nil {
		return fmt.Errorf("git init shadow repo: %w", err)
	}

	// Write a .gitignore in work dir to exclude the shadow repo from tracking.
	ignorePath := filepath.Join(cm.workDir, ".gitignore")
	if _, err := os.Stat(ignorePath); os.IsNotExist(err) {
		_ = os.WriteFile(ignorePath, []byte(".hermes-checkpoints/\n.hermes-checkpoints-ignore\n"), 0644)
	}

	return nil
}
func (cm *CheckpointManager) Snapshot(reason string) (*CheckpointEntry, error) {
	if !cm.enabled {
		return nil, nil
	}

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.ensureLocked(); err != nil {
		return nil, err
	}

	// Stage all files, excluding the shadow repo itself.
	if err := cm.gitWork("add", "-A", "--", "."); err != nil {
		return nil, fmt.Errorf("git add: %w", err)
	}

	// Check if there are staged changes.
	if err := cm.gitWork("diff", "--cached", "--quiet"); err == nil {
		return nil, nil // nothing to snapshot
	}

	ts := time.Now()
	msg := fmt.Sprintf("[checkpoint] %s (%s)", reason, ts.Format(time.RFC3339))

	if err := cm.gitWork("commit", "-m", msg, "--allow-empty-message"); err != nil {
		return nil, fmt.Errorf("git commit: %w", err)
	}

	// Get the commit hash.
	hash, err := cm.gitWorkOutput("rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("rev-parse: %w", err)
	}

	entry := &CheckpointEntry{
		ID:        strings.TrimSpace(hash),
		Reason:    reason,
		CreatedAt: ts,
	}

	// Prune old checkpoints.
	if cm.maxSnapshots > 0 {
		cm.pruneOld()
	}

	return entry, nil
}

// List returns all checkpoint entries (most recent first).
func (cm *CheckpointManager) List() ([]CheckpointEntry, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	out, err := cm.gitWorkOutput("log", "--format=%H|%s|%aI", "--reverse")
	if err != nil {
		return nil, nil // no commits yet
	}

	var entries []CheckpointEntry
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}

		ts, _ := time.Parse(time.RFC3339, parts[2])
		entries = append(entries, CheckpointEntry{
			ID:        parts[0],
			Reason:    parts[1],
			CreatedAt: ts,
		})
	}

	// Reverse for most-recent-first.
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return entries, nil
}

// Diff returns the diff between a checkpoint and the current working directory.
func (cm *CheckpointManager) Diff(checkpointID string) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	out, err := cm.gitWorkOutput("diff", checkpointID, "--", ".")
	if err != nil {
		return "", fmt.Errorf("diff %s: %w", checkpointID, err)
	}
	return out, nil
}

// Restore reverts the working directory to a checkpoint.
func (cm *CheckpointManager) Restore(checkpointID string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.gitWork("checkout", checkpointID, "--", "."); err != nil {
		return fmt.Errorf("restore %s: %w", checkpointID, err)
	}
	return nil
}

// --- internal helpers ---

func (cm *CheckpointManager) ensureLocked() error {
	if _, err := os.Stat(filepath.Join(cm.shadowRepo, "HEAD")); err != nil {
		if err := os.MkdirAll(cm.shadowRepo, 0755); err != nil {
			return fmt.Errorf("create shadow repo dir: %w", err)
		}
		if err := cm.git("init", "--bare"); err != nil {
			return fmt.Errorf("git init shadow repo: %w", err)
		}
	}
	return nil
}

func (cm *CheckpointManager) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cm.shadowRepo
	// For bare repo operations (init), only set GIT_DIR, not GIT_WORK_TREE.
	cmd.Env = cm.gitEnvBare()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (cm *CheckpointManager) gitWork(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = cm.workDir
	cmd.Env = cm.gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return nil
}

func (cm *CheckpointManager) gitWorkOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cm.workDir
	cmd.Env = cm.gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (cm *CheckpointManager) gitEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_DIR="+cm.shadowRepo)
	env = append(env, "GIT_WORK_TREE="+cm.workDir)
	env = append(env, "GIT_AUTHOR_NAME=hermes-checkpoint")
	env = append(env, "GIT_AUTHOR_EMAIL=checkpoint@hermes.local")
	env = append(env, "GIT_COMMITTER_NAME=hermes-checkpoint")
	env = append(env, "GIT_COMMITTER_EMAIL=checkpoint@hermes.local")
	return env
}

func (cm *CheckpointManager) gitEnvBare() []string {
	env := os.Environ()
	env = append(env, "GIT_DIR="+cm.shadowRepo)
	env = append(env, "GIT_AUTHOR_NAME=hermes-checkpoint")
	env = append(env, "GIT_AUTHOR_EMAIL=checkpoint@hermes.local")
	env = append(env, "GIT_COMMITTER_NAME=hermes-checkpoint")
	env = append(env, "GIT_COMMITTER_EMAIL=checkpoint@hermes.local")
	return env
}

func (cm *CheckpointManager) pruneOld() {
	out, err := cm.gitWorkOutput("rev-list", "--count", "HEAD")
	if err != nil {
		return
	}

	count := 0
	fmt.Sscanf(strings.TrimSpace(out), "%d", &count)

	if count <= cm.maxSnapshots {
		return
	}

	// Soft-prune: squash oldest commits.
	target := fmt.Sprintf("HEAD~%d", cm.maxSnapshots)
	_ = cm.gitWork("reset", "--soft", target)
}
