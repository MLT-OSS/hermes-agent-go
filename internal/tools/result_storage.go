package tools

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// ResultStore caches large tool outputs to files and returns reference IDs.
// This saves context tokens — instead of putting 50KB of output in the
// conversation, we store it and give the LLM a short reference ID.
type ResultStore struct {
	mu       sync.Mutex
	cacheDir string
}

// ResultRef is a reference to a stored result.
type ResultRef struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Size      int    `json:"size"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
}

var (
	globalResultStore *ResultStore
	resultStoreOnce   sync.Once
)

// GetResultStore returns the singleton result store.
func GetResultStore() *ResultStore {
	resultStoreOnce.Do(func() {
		cacheDir := filepath.Join(config.HermesHome(), "cache", "results")
		os.MkdirAll(cacheDir, 0755) //nolint:errcheck
		globalResultStore = &ResultStore{cacheDir: cacheDir}
	})
	return globalResultStore
}

// Store saves content to a file and returns a reference.
// If content is smaller than threshold, returns nil (caller should use inline).
func (rs *ResultStore) Store(content string, threshold int) *ResultRef {
	if len(content) < threshold {
		return nil
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()

	// Generate ID from content hash + timestamp.
	hash := sha256.Sum256([]byte(content))
	id := fmt.Sprintf("result_%x_%d", hash[:4], time.Now().UnixMilli()%100000)

	path := filepath.Join(rs.cacheDir, id+".txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil
	}

	preview := content
	truncated := false
	if len(preview) > 500 {
		preview = preview[:500]
		truncated = true
	}

	return &ResultRef{
		ID:        id,
		Path:      path,
		Size:      len(content),
		Preview:   preview,
		Truncated: truncated,
	}
}

// Retrieve reads back a stored result by ID.
func (rs *ResultStore) Retrieve(id string) (string, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	path := filepath.Join(rs.cacheDir, id+".txt")

	// Validate the path doesn't escape cache dir.
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("invalid result id: %w", err)
	}
	absCacheDir, _ := filepath.Abs(rs.cacheDir)
	if !filepath.HasPrefix(absPath, absCacheDir) {
		return "", fmt.Errorf("result id contains path traversal")
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("result %q not found: %w", id, err)
	}
	return string(data), nil
}

// Cleanup removes result files older than maxAge.
func (rs *ResultStore) Cleanup(maxAge time.Duration) int {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	removed := 0

	entries, err := os.ReadDir(rs.cacheDir)
	if err != nil {
		return 0
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(rs.cacheDir, e.Name())) //nolint:errcheck
			removed++
		}
	}
	return removed
}
