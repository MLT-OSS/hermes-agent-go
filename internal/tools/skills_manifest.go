package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// SkillManifest tracks the state of all synced skills, including hashes
// for user-edit detection.
type SkillManifest struct {
	Version  int                      `json:"version"`
	Skills   map[string]ManifestEntry `json:"skills"`
	SyncedAt time.Time                `json:"synced_at"`
}

// ManifestEntry records metadata about a single synced skill file.
type ManifestEntry struct {
	Hash         string    `json:"hash"`
	Source       string    `json:"source"`
	InstalledAt  time.Time `json:"installed_at"`
	UserModified bool      `json:"user_modified"`
	SourceCommit string    `json:"source_commit,omitempty"`
}

// DefaultManifestPath returns the default path for the skills manifest.
func DefaultManifestPath() string {
	return filepath.Join(config.HermesHome(), "skills", ".manifest.json")
}

// LoadManifest reads a SkillManifest from the given path. If the file does
// not exist, an empty manifest is returned.
func LoadManifest(path string) (*SkillManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SkillManifest{
				Version: 1,
				Skills:  make(map[string]ManifestEntry),
			}, nil
		}
		return nil, fmt.Errorf("load manifest: %w", err)
	}

	var m SkillManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Skills == nil {
		m.Skills = make(map[string]ManifestEntry)
	}
	return &m, nil
}

// SaveManifest writes the manifest to disk atomically using a temp file and
// os.Rename so readers never see a partial write.
func SaveManifest(path string, m *SkillManifest) error {
	m.SyncedAt = time.Now()

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}

	// Write to a temp file in the same directory so Rename is atomic.
	tmp, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp manifest: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename manifest: %w", err)
	}

	return nil
}

// HashFileContent returns the hex-encoded SHA-256 hash of the given data.
func HashFileContent(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// IsUserModified checks whether the installed file has been modified by the
// user since the last sync. It returns true if the entry is already flagged
// or if the current file hash differs from the hash stored in the manifest.
func IsUserModified(manifest *SkillManifest, skillName, installedPath string) bool {
	entry, ok := manifest.Skills[skillName]
	if !ok {
		return false
	}

	if entry.UserModified {
		return true
	}

	data, err := os.ReadFile(installedPath)
	if err != nil {
		return false
	}

	currentHash := HashFileContent(data)
	if currentHash != entry.Hash {
		slog.Debug("user-modified skill detected",
			"skill", skillName,
			"expected", entry.Hash,
			"actual", currentHash,
		)
		return true
	}
	return false
}

// SyncFromHub is a stub that will eventually fetch a skill index from a
// GitHub-hosted hub and sync new or updated skills into the installed
// directory. For now it returns nil and logs the intent.
func SyncFromHub(hubURL string) error {
	slog.Info("hub sync requested (stub)", "url", hubURL)
	// TODO: fetch skill index JSON from hubURL, iterate entries,
	// download each SKILL.md, run through manifest-aware sync.
	return nil
}
