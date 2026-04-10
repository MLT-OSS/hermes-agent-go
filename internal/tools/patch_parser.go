package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PatchHunk represents a single hunk in a unified diff.
type PatchHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []DiffLine
}

// DiffLine represents a single line in a unified diff hunk.
type DiffLine struct {
	Op   byte // ' ' context, '+' add, '-' remove
	Text string
}

// PatchFile represents a parsed unified diff for a single file.
type PatchFile struct {
	OldPath string
	NewPath string
	Hunks   []PatchHunk
}

// hunkHeaderRe matches unified diff hunk headers like @@ -1,3 +1,4 @@.
var hunkHeaderRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// ParseUnifiedDiff parses a unified diff string into PatchFile structs.
func ParseUnifiedDiff(diff string) ([]PatchFile, error) {
	lines := strings.Split(diff, "\n")
	var patches []PatchFile
	var current *PatchFile
	var currentHunk *PatchHunk

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// File header: --- a/path
		if strings.HasPrefix(line, "--- ") {
			if current != nil {
				if currentHunk != nil {
					current.Hunks = append(current.Hunks, *currentHunk)
					currentHunk = nil
				}
				patches = append(patches, *current)
			}
			p := PatchFile{}
			p.OldPath = stripPrefix(line[4:])
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ") {
				p.NewPath = stripPrefix(lines[i+1][4:])
				i++
			}
			current = &p
			continue
		}

		// Hunk header.
		if m := hunkHeaderRe.FindStringSubmatch(line); m != nil {
			if current == nil {
				return nil, fmt.Errorf("hunk header without file header at line %d", i+1)
			}
			if currentHunk != nil {
				current.Hunks = append(current.Hunks, *currentHunk)
			}
			h := PatchHunk{
				OldStart: mustAtoi(m[1]),
				OldCount: atoiOr(m[2], 1),
				NewStart: mustAtoi(m[3]),
				NewCount: atoiOr(m[4], 1),
			}
			currentHunk = &h
			continue
		}

		// Diff lines.
		if currentHunk != nil && len(line) > 0 {
			switch line[0] {
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Op: '+', Text: line[1:]})
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Op: '-', Text: line[1:]})
			case ' ':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{Op: ' ', Text: line[1:]})
			case '\\':
				// "\ No newline at end of file" — skip.
			}
		}
	}

	// Flush remaining.
	if current != nil {
		if currentHunk != nil {
			current.Hunks = append(current.Hunks, *currentHunk)
		}
		patches = append(patches, *current)
	}

	return patches, nil
}

// ApplyHunks applies parsed hunks to file content.
// Returns the new content and any conflicts found.
func ApplyHunks(content string, hunks []PatchHunk) (string, []string) {
	lines := strings.Split(content, "\n")
	var conflicts []string
	offset := 0 // tracks line number shift from previous hunks

	for _, hunk := range hunks {
		startIdx := hunk.OldStart - 1 + offset
		if startIdx < 0 {
			startIdx = 0
		}

		// Verify context lines match.
		contextOK := true
		checkIdx := startIdx
		for _, dl := range hunk.Lines {
			if dl.Op == ' ' || dl.Op == '-' {
				if checkIdx >= len(lines) || lines[checkIdx] != dl.Text {
					contextOK = false
					break
				}
				checkIdx++
			}
		}

		if !contextOK {
			conflicts = append(conflicts, fmt.Sprintf("hunk @@ -%d,%d +%d,%d @@ does not match",
				hunk.OldStart, hunk.OldCount, hunk.NewStart, hunk.NewCount))
			continue
		}

		// Build replacement.
		var newLines []string
		removeCount := 0
		for _, dl := range hunk.Lines {
			switch dl.Op {
			case ' ':
				newLines = append(newLines, dl.Text)
				removeCount++
			case '+':
				newLines = append(newLines, dl.Text)
			case '-':
				removeCount++
			}
		}

		// Splice.
		result := make([]string, 0, len(lines)-removeCount+len(newLines))
		result = append(result, lines[:startIdx]...)
		result = append(result, newLines...)
		if startIdx+removeCount < len(lines) {
			result = append(result, lines[startIdx+removeCount:]...)
		}

		offset += len(newLines) - removeCount
		lines = result
	}

	return strings.Join(lines, "\n"), conflicts
}

func stripPrefix(path string) string {
	// Strip a/ or b/ prefix from diff paths.
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		return path[2:]
	}
	return path
}

func mustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
