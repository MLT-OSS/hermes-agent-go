package agent

import (
	"regexp"
	"strings"
)

// thinkPatterns matches reasoning/thinking tag variants emitted by various LLMs.
// Must stay in sync with HasContentAfterThinkBlock.
var thinkPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?si)<think>.*?</think>`),
	regexp.MustCompile(`(?si)<thinking>.*?</thinking>`),
	regexp.MustCompile(`(?si)<reasoning>.*?</reasoning>`),
	regexp.MustCompile(`(?s)<REASONING_SCRATCHPAD>.*?</REASONING_SCRATCHPAD>`),
}

// orphanTagPattern strips leftover opening/closing tags that weren't matched
// by the block patterns above (e.g. unclosed <think> at end of stream).
var orphanTagPattern = regexp.MustCompile(`(?i)</?(?:think|thinking|reasoning|REASONING_SCRATCHPAD)>\s*`)

// extractThinkPattern captures the first <think>...</think> block content for
// returning as structured reasoning.
var extractThinkPattern = regexp.MustCompile(`(?si)<think>(.*?)</think>`)

// StripThinkBlocks removes all reasoning/thinking tag blocks from content,
// returning only the visible text.
func StripThinkBlocks(content string) string {
	if content == "" {
		return ""
	}
	for _, pat := range thinkPatterns {
		content = pat.ReplaceAllString(content, "")
	}
	content = orphanTagPattern.ReplaceAllString(content, "")
	return content
}

// ExtractThinkContent extracts reasoning text from <think> blocks and returns
// (cleaned content, reasoning). If no think block is found, reasoning is empty.
func ExtractThinkContent(content string) (cleaned string, reasoning string) {
	if content == "" {
		return "", ""
	}

	// Extract reasoning from first <think> block
	if m := extractThinkPattern.FindStringSubmatch(content); len(m) > 1 {
		reasoning = strings.TrimSpace(m[1])
	}

	cleaned = StripThinkBlocks(content)
	return strings.TrimSpace(cleaned), reasoning
}