package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// threatPattern pairs a human-readable name with a compiled regex.
type threatPattern struct {
	name string
	re   *regexp.Regexp
}

// contextThreatPatterns is the set of patterns checked by scanContextContent.
var contextThreatPatterns = []threatPattern{
	{"prompt_injection", regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`)},
	{"deception_hide", regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`)},
	{"sys_prompt_override", regexp.MustCompile(`(?i)system\s+prompt\s+override`)},
	{"disregard_rules", regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`)},
	{"bypass_restrictions", regexp.MustCompile(`(?i)act\s+as\s+(if|though)\s+you\s+(have\s+no|don't\s+have)\s+(restrictions|limits|rules)`)},
	{"html_comment_inject", regexp.MustCompile(`(?i)<!--[^>]*(?:ignore|override|system|secret|hidden)[^>]*-->`)},
	{"hidden_div", regexp.MustCompile(`(?i)<\s*div\s+style\s*=\s*["'][\s\S]*?display\s*:\s*none`)},
	{"translate_execute", regexp.MustCompile(`(?i)translate\s+.*\s+into\s+.*\s+and\s+(execute|run|eval)`)},
	{"exfil_curl", regexp.MustCompile(`(?i)curl\s+[^\n]*\$\{?\w*(KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|API)`)},
	{"read_secrets", regexp.MustCompile(`(?i)cat\s+[^\n]*(\.env|credentials|\.netrc|\.pgpass)`)},
}

// invisibleRanges lists Unicode ranges that indicate hidden/bidi text.
var invisibleRanges = [][2]rune{
	{0x200B, 0x200F}, // zero-width spaces and marks
	{0x202A, 0x202E}, // bidi embedding / override characters
	{0x2060, 0x2064}, // invisible formatting characters
}

// scanContextContent checks content for prompt-injection threats.
// It returns the original content and nil when clean.
// When threats are found it returns a blocked message and the list of matched pattern names.
func scanContextContent(filename, content string) (string, []string) {
	var matched []string

	for _, p := range contextThreatPatterns {
		if p.re.MatchString(content) {
			matched = append(matched, p.name)
		}
	}

	// Check for invisible Unicode characters (skip BOM only when at position 0).
	for i, r := range content {
		for _, rng := range invisibleRanges {
			if r >= rng[0] && r <= rng[1] {
				matched = append(matched, "invisible_unicode")
				goto doneUnicode
			}
		}
		// U+FEFF is a BOM; flag it only when it appears after position 0.
		if r == 0xFEFF && i != 0 {
			matched = append(matched, "invisible_unicode")
			goto doneUnicode
		}
	}
doneUnicode:

	if len(matched) == 0 {
		return content, nil
	}

	blocked := fmt.Sprintf("[BLOCKED: %s contained potential prompt injection (%s)]",
		filename, strings.Join(matched, ", "))
	return blocked, matched
}

// ContextFile represents a context reference file discovered on disk.
type ContextFile struct {
	Path    string // absolute or relative path to the file
	Content string // file contents
	Type    string // "soul", "agents", "cursorrules", "copilot", "readme"
}

// contextFileCandidates defines the files to scan for, in priority order.
var contextFileCandidates = []struct {
	// rel is the path relative to the scan root.
	rel string
	// fileType is the ContextFile.Type value.
	fileType string
}{
	{"SOUL.md", "soul"},
	{"AGENTS.md", "agents"},
	{".cursorrules", "cursorrules"},
	{".github/copilot-instructions.md", "copilot"},
}

// LoadContextReferences scans the workspace and hermes home for context files.
// dir is the workspace directory (typically the current working directory).
func LoadContextReferences(dir string) []ContextFile {
	var files []ContextFile

	// 1. Global context from ~/.hermes/
	hermesHome := config.HermesHome()
	globalCandidates := []struct {
		rel      string
		fileType string
	}{
		{"SOUL.md", "soul"},
	}

	for _, c := range globalCandidates {
		path := filepath.Join(hermesHome, c.rel)
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		text, threats := scanContextContent(path, string(raw))
		if threats != nil {
			slog.Warn("Blocked global context file due to threat patterns",
				"path", path, "patterns", threats)
		}
		files = append(files, ContextFile{
			Path:    path,
			Content: text,
			Type:    c.fileType,
		})
		slog.Debug("Loaded global context file", "path", path, "type", c.fileType)
	}

	// 2. Project context from the workspace directory.
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			return files
		}
	}

	for _, c := range contextFileCandidates {
		path := filepath.Join(dir, c.rel)
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}

		// Skip duplicates (e.g., SOUL.md if workspace == hermesHome).
		if isDuplicate(files, path) {
			continue
		}

		text, threats := scanContextContent(path, string(raw))
		if threats != nil {
			slog.Warn("Blocked project context file due to threat patterns",
				"path", path, "patterns", threats)
		}
		files = append(files, ContextFile{
			Path:    path,
			Content: text,
			Type:    c.fileType,
		})
		slog.Debug("Loaded project context file", "path", path, "type", c.fileType)
	}

	// 3. Check for README.md as a fallback if nothing else was found.
	if len(files) == 0 {
		readmePath := filepath.Join(dir, "README.md")
		raw, err := os.ReadFile(readmePath)
		if err == nil && len(raw) > 0 {
			// Only include a truncated version to avoid bloating the prompt.
			text := string(raw)
			if len(text) > 2000 {
				text = text[:2000] + "\n\n... (truncated)"
			}
			text, threats := scanContextContent(readmePath, text)
			if threats != nil {
				slog.Warn("Blocked README context file due to threat patterns",
					"path", readmePath, "patterns", threats)
			}
			files = append(files, ContextFile{
				Path:    readmePath,
				Content: text,
				Type:    "readme",
			})
		}
	}

	return files
}

// isDuplicate checks whether a path is already in the list.
func isDuplicate(files []ContextFile, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for _, f := range files {
		fabs, err := filepath.Abs(f.Path)
		if err != nil {
			fabs = f.Path
		}
		if fabs == abs {
			return true
		}
	}
	return false
}
