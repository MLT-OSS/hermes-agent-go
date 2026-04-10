// Package tools provides process_registry — lifecycle management for background
// processes spawned by the terminal tool. Tracks running processes with unique
// IDs, provides output ring buffers, and cleans up on session end.
package tools

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ProcessState represents the lifecycle state of a managed process.
type ProcessState string

const (
	StateRunning  ProcessState = "running"
	StateExited   ProcessState = "exited"
	StateKilled   ProcessState = "killed"
	StateTimedOut ProcessState = "timed_out"
)

// ProcessEntry holds metadata and output for a managed process.
type ProcessEntry struct {
	ID        string       `json:"id"`
	Command   string       `json:"command"`
	PID       int          `json:"pid"`
	State     ProcessState `json:"state"`
	ExitCode  int          `json:"exit_code"`
	StartedAt time.Time    `json:"started_at"`
	EndedAt   time.Time    `json:"ended_at,omitempty"`

	mu     sync.Mutex
	output *ringBuffer
	cmd    *exec.Cmd
}

// Output returns the captured output (up to the ring buffer capacity).
func (p *ProcessEntry) Output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.output == nil {
		return ""
	}
	return p.output.String()
}

// ProcessRegistry manages background processes with unique IDs.
type ProcessRegistry struct {
	mu        sync.Mutex
	processes map[string]*ProcessEntry
	counter   int
	bufSize   int // ring buffer size per process
}

// NewProcessRegistry creates a registry with the given output buffer size per process.
func NewProcessRegistry(bufSize int) *ProcessRegistry {
	if bufSize <= 0 {
		bufSize = 64 * 1024 // 64KB default
	}
	return &ProcessRegistry{
		processes: make(map[string]*ProcessEntry),
		bufSize:   bufSize,
	}
}

// Spawn starts a command in the background and returns its registry entry.
func (r *ProcessRegistry) Spawn(command, workDir string, env []string) (*ProcessEntry, error) {
	cmd := exec.Command("sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(env) > 0 {
		cmd.Env = env
	}

	buf := newRingBuffer(r.bufSize)

	// Combine stdout and stderr into the ring buffer.
	cmd.Stdout = buf
	cmd.Stderr = buf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	r.mu.Lock()
	r.counter++
	id := fmt.Sprintf("proc-%d", r.counter)
	entry := &ProcessEntry{
		ID:        id,
		Command:   command,
		PID:       cmd.Process.Pid,
		State:     StateRunning,
		StartedAt: time.Now(),
		output:    buf,
		cmd:       cmd,
	}
	r.processes[id] = entry
	r.mu.Unlock()

	// Wait for process exit in background — tracked by WaitGroup-style state update.
	go func() {
		err := cmd.Wait()
		entry.mu.Lock()
		defer entry.mu.Unlock()

		entry.EndedAt = time.Now()
		if entry.State == StateKilled {
			// Already marked as killed by Kill().
			return
		}
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				entry.ExitCode = exitErr.ExitCode()
			} else {
				entry.ExitCode = -1
			}
		}
		entry.State = StateExited
	}()

	return entry, nil
}

// Get returns a process entry by ID.
func (r *ProcessRegistry) Get(id string) *ProcessEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.processes[id]
}

// Poll returns a status snapshot of a process.
func (r *ProcessRegistry) Poll(id string) map[string]any {
	entry := r.Get(id)
	if entry == nil {
		return map[string]any{"error": fmt.Sprintf("process %q not found", id)}
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	result := map[string]any{
		"id":         entry.ID,
		"command":    entry.Command,
		"pid":        entry.PID,
		"state":      string(entry.State),
		"started_at": entry.StartedAt.Format(time.RFC3339),
	}
	if entry.State != StateRunning {
		result["exit_code"] = entry.ExitCode
		result["ended_at"] = entry.EndedAt.Format(time.RFC3339)
		result["duration"] = entry.EndedAt.Sub(entry.StartedAt).Round(time.Millisecond).String()
	}
	return result
}

// Kill terminates a running process.
func (r *ProcessRegistry) Kill(id string) error {
	entry := r.Get(id)
	if entry == nil {
		return fmt.Errorf("process %q not found", id)
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.State != StateRunning {
		return fmt.Errorf("process %q is not running (state: %s)", id, entry.State)
	}

	if entry.cmd != nil && entry.cmd.Process != nil {
		_ = entry.cmd.Process.Kill()
	}
	entry.State = StateKilled
	entry.EndedAt = time.Now()
	return nil
}

// List returns all process entries.
func (r *ProcessRegistry) List() []*ProcessEntry {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]*ProcessEntry, 0, len(r.processes))
	for _, entry := range r.processes {
		result = append(result, entry)
	}
	return result
}

// Cleanup terminates all running processes and clears the registry.
func (r *ProcessRegistry) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, entry := range r.processes {
		entry.mu.Lock()
		if entry.State == StateRunning && entry.cmd != nil && entry.cmd.Process != nil {
			_ = entry.cmd.Process.Kill()
			entry.State = StateKilled
			entry.EndedAt = time.Now()
		}
		entry.mu.Unlock()
	}
}

// --- Ring buffer ---

// ringBuffer is a fixed-size circular buffer that implements io.Writer.
// When full, oldest bytes are overwritten.
type ringBuffer struct {
	mu   sync.Mutex
	data []byte
	size int
	pos  int
	full bool
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (rb *ringBuffer) Write(p []byte) (int, error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n := len(p)
	for _, b := range p {
		rb.data[rb.pos] = b
		rb.pos = (rb.pos + 1) % rb.size
		if rb.pos == 0 {
			rb.full = true
		}
	}
	return n, nil
}

func (rb *ringBuffer) String() string {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if !rb.full {
		return string(rb.data[:rb.pos])
	}
	// Ring has wrapped: read from pos to end, then start to pos.
	var sb strings.Builder
	sb.Write(rb.data[rb.pos:])
	sb.Write(rb.data[:rb.pos])
	return sb.String()
}

// Ensure ringBuffer implements io.Writer.
var _ io.Writer = (*ringBuffer)(nil)
