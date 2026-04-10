// Package environments provides the pty_local environment for interactive commands.
//
//go:build !windows
// +build !windows

package environments

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// PTYEnvironment executes commands via a pseudo-terminal,
// supporting interactive programs (vim, top, ssh) and signal forwarding.
type PTYEnvironment struct {
	workDir      string
	gracePeriod  time.Duration
	maxOutputLen int
}

func init() {
	RegisterEnvironment("pty", func(params map[string]string) (Environment, error) {
		env := NewPTYEnvironment()
		if dir, ok := params["working_directory"]; ok && dir != "" {
			env.workDir = dir
		}
		return env, nil
	})
}

// NewPTYEnvironment creates a new PTY-based execution environment.
func NewPTYEnvironment() *PTYEnvironment {
	cwd, _ := os.Getwd()
	return &PTYEnvironment{
		workDir:      cwd,
		gracePeriod:  5 * time.Second,
		maxOutputLen: 1 << 20, // 1 MB
	}
}

// Execute runs a command in a PTY and returns combined output.
func (e *PTYEnvironment) Execute(command string, timeout int) (stdout, stderr string, exitCode int, err error) {
	if timeout <= 0 {
		timeout = 120
	}
	if timeout > 600 {
		timeout = 600
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = e.workDir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	// Prevent the context cancellation from sending SIGKILL immediately.
	// We handle graceful shutdown ourselves.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = e.gracePeriod

	// Start with PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		// Fallback to non-PTY execution if PTY fails (e.g. in CI)
		return e.fallbackExecute(command, timeout)
	}
	defer ptmx.Close()

	// Read output in background
	var outputBuf bytes.Buffer
	var readErr error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, readErr = io.Copy(&limitedWriter{w: &outputBuf, max: e.maxOutputLen}, ptmx)
	}()

	// Wait for command to finish
	waitErr := cmd.Wait()
	// Close PTY to signal EOF to the reader goroutine
	ptmx.Close()
	wg.Wait()

	output := stripANSI(outputBuf.String())

	exitCode = 0
	if waitErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return output, "", -1, fmt.Errorf("command timed out after %d seconds", timeout)
		}
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return output, "", -1, fmt.Errorf("command execution failed: %w", waitErr)
		}
	}

	// PTY merges stdout and stderr into one stream.
	_ = readErr // EOF is expected
	return output, "", exitCode, nil
}

// fallbackExecute runs without PTY when pty.Start fails.
func (e *PTYEnvironment) fallbackExecute(command string, timeout int) (string, string, int, error) {
	local := &LocalEnvironment{workDir: e.workDir}
	return local.Execute(command, timeout)
}

// IsAvailable returns true if PTY support is available.
func (e *PTYEnvironment) IsAvailable() bool {
	// Check if /dev/ptmx exists (Linux/macOS)
	_, err := os.Stat("/dev/ptmx")
	return err == nil
}

// Name returns "pty".
func (e *PTYEnvironment) Name() string {
	return "pty"
}

// SetWorkDir sets the working directory.
func (e *PTYEnvironment) SetWorkDir(dir string) {
	e.workDir = dir
}

// WorkDir returns the current working directory.
func (e *PTYEnvironment) WorkDir() string {
	return e.workDir
}

// limitedWriter wraps a Writer with a maximum byte count.
type limitedWriter struct {
	w       io.Writer
	max     int
	written int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	remaining := lw.max - lw.written
	if remaining <= 0 {
		return len(p), nil // discard but don't error
	}
	if len(p) > remaining {
		p = p[:remaining]
	}
	n, err := lw.w.Write(p)
	lw.written += n
	return n, err
}

// stripANSI removes ANSI escape sequences from output.
func stripANSI(s string) string {
	var result bytes.Buffer
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			// Skip CSI sequence: ESC [ ... final byte
			j := i + 2
			for j < len(s) && s[j] >= 0x30 && s[j] <= 0x3f {
				j++ // parameter bytes
			}
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x2f {
				j++ // intermediate bytes
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7e {
				j++ // final byte
			}
			i = j
			continue
		}
		// Strip other control characters except newline/tab/CR
		if s[i] < 0x20 && s[i] != '\n' && s[i] != '\t' && s[i] != '\r' {
			i++
			continue
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}
