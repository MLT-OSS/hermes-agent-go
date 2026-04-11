// Package skills handles SKILL.md parsing, loading, and management.
package skills

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillMeta holds parsed metadata from a SKILL.md frontmatter.
type SkillMeta struct {
	Name          string   `yaml:"name" json:"name"`
	Description   string   `yaml:"description" json:"description"`
	Version       string   `yaml:"version" json:"version"`
	Author        string   `yaml:"author" json:"author"`
	Tags          []string `yaml:"tags" json:"tags"`
	Category      string   `yaml:"category" json:"category"`
	Platforms     []string `yaml:"platforms" json:"platforms,omitempty"`
	Prerequisites []string `yaml:"prerequisites" json:"prerequisites,omitempty"`
	MinVersion    string   `yaml:"min_version" json:"min_version,omitempty"`

	// Path to the SKILL.md file (set by loader, not from frontmatter).
	Path string `json:"path"`
}

// frontmatterRe matches YAML frontmatter delimited by --- ... ---.
var frontmatterRe = regexp.MustCompile(`(?s)\A---\s*\n(.*?)\n---\s*\n`)

// ParseSkillMD parses a SKILL.md file and returns the metadata and body content.
func ParseSkillMD(path string) (*SkillMeta, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read skill file: %w", err)
	}

	content := string(data)
	meta, body := ParseFrontmatter(content)
	meta.Path = path

	return meta, body, nil
}

// ParseFrontmatter extracts YAML frontmatter from a markdown string.
// Returns the parsed metadata and the remaining body text.
func ParseFrontmatter(content string) (*SkillMeta, string) {
	meta := &SkillMeta{}

	if !strings.HasPrefix(content, "---") {
		return meta, content
	}

	match := frontmatterRe.FindStringSubmatchIndex(content)
	if match == nil {
		return meta, content
	}

	yamlContent := content[match[2]:match[3]]
	body := content[match[1]:]

	// Try YAML parsing first.
	if err := yaml.Unmarshal([]byte(yamlContent), meta); err != nil {
		// Fallback: simple key:value parsing.
		parseSimpleFrontmatter(yamlContent, meta)
	}

	return meta, body
}

// parseSimpleFrontmatter does simple key:value parsing as a fallback.
func parseSimpleFrontmatter(yamlContent string, meta *SkillMeta) {
	for _, line := range strings.Split(yamlContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "name":
			meta.Name = value
		case "description":
			meta.Description = value
		case "version":
			meta.Version = value
		case "author":
			meta.Author = value
		case "category":
			meta.Category = value
		case "tags":
			meta.Tags = parseCommaSeparated(value)
		case "platforms":
			meta.Platforms = parseCommaSeparated(value)
		case "prerequisites":
			meta.Prerequisites = parseCommaSeparated(value)
		}
	}
}

// parseCommaSeparated parses a comma-separated or YAML list value.
func parseCommaSeparated(value string) []string {
	// Handle YAML list format: [a, b, c]
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")

	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		item = strings.Trim(item, "\"'")
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

// SkillMatchesPlatform checks if a skill is compatible with the current OS.
func SkillMatchesPlatform(meta *SkillMeta) bool {
	if len(meta.Platforms) == 0 {
		return true // No platform restriction.
	}

	currentPlatform := compilePlatform()
	for _, p := range meta.Platforms {
		mapped := mapPlatformName(p)
		if mapped == currentPlatform || p == "all" {
			return true
		}
	}
	return false
}

// mapPlatformName maps user-facing platform names to Go runtime values.
func mapPlatformName(name string) string {
	switch strings.ToLower(name) {
	case "macos", "darwin":
		return "darwin"
	case "linux":
		return "linux"
	case "windows", "win32":
		return "windows"
	default:
		return strings.ToLower(name)
	}
}
