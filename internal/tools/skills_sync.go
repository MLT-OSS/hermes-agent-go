package tools

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// SkillSyncResult describes the outcome of syncing a single skill.
type SkillSyncResult struct {
	Name    string `json:"name"`
	Action  string `json:"action"` // "installed", "updated", "unchanged", "skipped", "error"
	Message string `json:"message,omitempty"`
}

// SyncBuiltinSkills checks bundled skills against the installed skills directory
// and copies any new or updated skills. It loads the manifest to detect
// user-modified files and never overwrites them.
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

	// Load the manifest for user-edit detection.
	manifestPath := filepath.Join(installedDir, ".manifest.json")
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		slog.Warn("could not load manifest, starting fresh", "error", err)
		manifest = &SkillManifest{Version: 1, Skills: make(map[string]ManifestEntry)}
	}

	var results []SkillSyncResult

	// Walk bundled skills.
	err = filepath.WalkDir(bundledDir, func(path string, d os.DirEntry, err error) error {
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

		bundledHash := HashFileContent(bundledData)

		// Check if installed version exists.
		installedData, readErr := os.ReadFile(destPath)
		if readErr == nil {
			// File exists — check for user modifications.
			if IsUserModified(manifest, skillName, destPath) {
				slog.Info("skipping user-modified skill", "skill", skillName)
				results = append(results, SkillSyncResult{
					Name:    skillName,
					Action:  "skipped",
					Message: "user-modified",
				})
				return nil
			}

			// Compare hashes to see if an update is needed.
			installedHash := HashFileContent(installedData)
			if installedHash == bundledHash {
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

			// Update manifest entry.
			manifest.Skills[skillName] = ManifestEntry{
				Hash:        bundledHash,
				Source:      "builtin",
				InstalledAt: time.Now(),
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

		// Record in manifest.
		manifest.Skills[skillName] = ManifestEntry{
			Hash:        bundledHash,
			Source:      "builtin",
			InstalledAt: time.Now(),
		}

		results = append(results, SkillSyncResult{
			Name:   skillName,
			Action: "installed",
		})
		return nil
	})

	// Persist the updated manifest.
	if saveErr := SaveManifest(manifestPath, manifest); saveErr != nil {
		slog.Error("failed to save manifest", "error", saveErr)
	}

	return results, err
}
