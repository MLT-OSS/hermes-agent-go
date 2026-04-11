package agent

import (
	"testing"
)

func TestStripThinkBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no think blocks",
			input: "Hello, world!",
			want:  "Hello, world!",
		},
		{
			name:  "simple think block",
			input: "<think>reasoning here</think>The answer is 42.",
			want:  "The answer is 42.",
		},
		{
			name:  "multiline think block",
			input: "<think>\nstep 1\nstep 2\n</think>\nResult: done.",
			want:  "\nResult: done.",
		},
		{
			name:  "thinking tag variant",
			input: "<thinking>some thoughts</thinking>Response here",
			want:  "Response here",
		},
		{
			name:  "THINKING uppercase",
			input: "<THINKING>loud thinking</THINKING>quiet response",
			want:  "quiet response",
		},
		{
			name:  "reasoning tag",
			input: "<reasoning>analysis</reasoning>Final answer.",
			want:  "Final answer.",
		},
		{
			name:  "REASONING_SCRATCHPAD",
			input: "<REASONING_SCRATCHPAD>work</REASONING_SCRATCHPAD>Output",
			want:  "Output",
		},
		{
			name:  "orphan tags stripped",
			input: "<think>unclosed",
			want:  "unclosed",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only think block",
			input: "<think>all reasoning no answer</think>",
			want:  "",
		},
		{
			name:  "multiple think blocks",
			input: "<think>first</think>middle<think>second</think>end",
			want:  "middleend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripThinkBlocks(tt.input)
			if got != tt.want {
				t.Errorf("StripThinkBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractThinkContent(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantCleaned   string
		wantReasoning string
	}{
		{
			name:          "with reasoning",
			input:         "<think>I need to calculate</think>The answer is 42.",
			wantCleaned:   "The answer is 42.",
			wantReasoning: "I need to calculate",
		},
		{
			name:          "no reasoning",
			input:         "Just a plain response.",
			wantCleaned:   "Just a plain response.",
			wantReasoning: "",
		},
		{
			name:          "empty",
			input:         "",
			wantCleaned:   "",
			wantReasoning: "",
		},
		{
			name:          "reasoning with whitespace",
			input:         "<think>\n  step 1\n  step 2\n</think>\nDone.",
			wantCleaned:   "Done.",
			wantReasoning: "step 1\n  step 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, reasoning := ExtractThinkContent(tt.input)
			if cleaned != tt.wantCleaned {
				t.Errorf("cleaned = %q, want %q", cleaned, tt.wantCleaned)
			}
			if reasoning != tt.wantReasoning {
				t.Errorf("reasoning = %q, want %q", reasoning, tt.wantReasoning)
			}
		})
	}
}
