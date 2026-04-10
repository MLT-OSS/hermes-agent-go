//go:build !windows
// +build !windows

package environments

import (
	"strings"
	"testing"
)

func TestPTYEnvironment_SimpleCommand(t *testing.T) {
	env := NewPTYEnvironment()
	if !env.IsAvailable() {
		t.Skip("PTY not available in this environment")
	}

	stdout, _, exitCode, err := env.Execute("echo hello", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "hello") {
		t.Errorf("stdout = %q, want to contain 'hello'", stdout)
	}
}

func TestPTYEnvironment_ExitCode(t *testing.T) {
	env := NewPTYEnvironment()
	if !env.IsAvailable() {
		t.Skip("PTY not available")
	}

	_, _, exitCode, err := env.Execute("exit 42", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 42 {
		t.Errorf("exit code = %d, want 42", exitCode)
	}
}

func TestPTYEnvironment_Timeout(t *testing.T) {
	env := NewPTYEnvironment()
	if !env.IsAvailable() {
		t.Skip("PTY not available")
	}

	_, _, exitCode, err := env.Execute("sleep 30", 2)
	if err == nil {
		t.Error("expected timeout error")
	}
	if exitCode != -1 {
		t.Errorf("exit code = %d, want -1 on timeout", exitCode)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want 'timed out'", err.Error())
	}
}

func TestPTYEnvironment_WorkDir(t *testing.T) {
	env := NewPTYEnvironment()
	if !env.IsAvailable() {
		t.Skip("PTY not available")
	}

	env.SetWorkDir("/tmp")
	stdout, _, _, err := env.Execute("pwd", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.TrimSpace(stdout), "tmp") {
		t.Errorf("pwd output = %q, want to contain 'tmp'", stdout)
	}
}

func TestPTYEnvironment_InteractiveOutput(t *testing.T) {
	env := NewPTYEnvironment()
	if !env.IsAvailable() {
		t.Skip("PTY not available")
	}

	// ls with color output (PTY enables colors by default)
	stdout, _, exitCode, err := env.Execute("ls /tmp", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0", exitCode)
	}
	// Output should be non-empty and ANSI-stripped
	if stdout == "" {
		t.Error("expected non-empty output from ls")
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no ansi", "hello world", "hello world"},
		{"color code", "\x1b[31mred\x1b[0m", "red"},
		{"cursor move", "\x1b[2Jhello", "hello"},
		{"bold", "\x1b[1mbold\x1b[0m text", "bold text"},
		{"control chars", "a\x07b\x08c", "abc"},
		{"preserves newlines", "line1\nline2\n", "line1\nline2\n"},
		{"preserves tabs", "col1\tcol2", "col1\tcol2"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripANSI(tt.input)
			if got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLimitedWriter(t *testing.T) {
	var buf strings.Builder
	lw := &limitedWriter{w: &buf, max: 10}

	n, err := lw.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Errorf("first write: n=%d, err=%v", n, err)
	}

	n, err = lw.Write([]byte("worldworld"))
	if err != nil {
		t.Errorf("second write err: %v", err)
	}
	// Should only write 5 more bytes (10 - 5 = 5)
	if buf.Len() != 10 {
		t.Errorf("total written = %d, want 10", buf.Len())
	}

	// Further writes should be discarded
	n, err = lw.Write([]byte("overflow"))
	if err != nil {
		t.Errorf("overflow write err: %v", err)
	}
	if buf.Len() != 10 {
		t.Errorf("after overflow: total = %d, want 10", buf.Len())
	}
}

func TestPTYEnvironment_Name(t *testing.T) {
	env := NewPTYEnvironment()
	if env.Name() != "pty" {
		t.Errorf("Name() = %q, want 'pty'", env.Name())
	}
}
