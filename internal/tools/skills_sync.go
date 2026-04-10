package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// SkillSyncResult describes the outcome of syncing a single skill.
type SkillSyncResult struct {
	Name    string `json:"name"`
	Action  string `json:"action"` // "installed", "updated", "unchanged", "error"
	Message string `json:"message,omitempty"`
}

// SyncBuiltinSkills checks bundled skills against the installed skills directory
// and copies any new or updated skills.
func SyncBuiltinSkills(bundledDir, installedDir string) ([]SkillSyncResult, error) {
	if bundledDir == "" {
		// Default bundled skills location (relative to binary or project root).
		bundledDir = "skills"
	}
	if installedDir == "" {
		installedDir = filepath.Join(config.HermesHome(), "skills")
	}

	if _, err := os.Stat(bundledDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("bundled skills directory not found: %s", bundledDir)
	}

	os.MkdirAll(installedDir, 0755) //nolint:errcheck

	var results []SkillSyncResult

	// Walk bundled skills.
	err := filepath.WalkDir(bundledDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		// Only sync SKILL.md files.
		if d.Name() != "SKILL.md" {
			return nil
		}

		relPath, err := filepath.Rel(bundledDir, path)
		if err != nil {
			return nil
		}

		destPath := filepath.Join(installedDir, relPath)
		skillName := filepath.Dir(relPath)

		bundledData, err := os.ReadFile(path)
		if err != nil {
			results = append(results, SkillSyncResult{
				Name:    skillName,
				Action:  "error",
				Message: fmt.Sprintf("read bundled: %v", err),
			})
			return nil
		}

		// Check if installed version exists and differs.
		installedData, err := os.ReadFile(destPath)
		if err == nil {
			// File exists — compare hashes.
			bundledHash := sha256.Sum256(bundledData)
			installedHash := sha256.Sum256(installedData)
			if bundledHash == installedHash {
				results = append(results, SkillSyncResult{
					Name:   skillName,
					Action: "unchanged",
				})
				return nil
			}
			// Different — update.
			os.MkdirAll(filepath.Dir(destPath), 0755) //nolint:errcheck
			if err := os.WriteFile(destPath, bundledData, 0644); err != nil {
				results = append(results, SkillSyncResult{
					Name:    skillName,
					Action:  "error",
					Message: fmt.Sprintf("write update: %v", err),
				})
				return nil
			}
			results = append(results, SkillSyncResult{
				Name:   skillName,
				Action: "updated",
			})
			return nil
		}

		// New skill — install.
		os.MkdirAll(filepath.Dir(destPath), 0755) //nolint:errcheck
		if err := os.WriteFile(destPath, bundledData, 0644); err != nil {
			results = append(results, SkillSyncResult{
				Name:    skillName,
				Action:  "error",
				Message: fmt.Sprintf("write install: %v", err),
			})
			return nil
		}
		results = append(results, SkillSyncResult{
			Name:   skillName,
			Action: "installed",
		})
		return nil
	})

	return results, err
}
