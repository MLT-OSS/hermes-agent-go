package tools

import (
	"strings"
	"testing"
	"time"
)

func TestProcessRegistry_SpawnAndPoll(t *testing.T) {
	r := NewProcessRegistry(4096)

	entry, err := r.Spawn("echo hello", "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.PID <= 0 {
		t.Error("expected positive PID")
	}

	// Wait for process to finish.
	time.Sleep(200 * time.Millisecond)

	status := r.Poll(entry.ID)
	if status["state"] != "exited" {
		t.Errorf("state = %v, want exited", status["state"])
	}

	output := entry.Output()
	if !strings.Contains(output, "hello") {
		t.Errorf("output = %q, want to contain 'hello'", output)
	}
}

func TestProcessRegistry_Kill(t *testing.T) {
	r := NewProcessRegistry(4096)

	entry, err := r.Spawn("sleep 60", "", nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if err := r.Kill(entry.ID); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	status := r.Poll(entry.ID)
	if status["state"] != "killed" {
		t.Errorf("state = %v, want killed", status["state"])
	}
}

func TestProcessRegistry_KillNotFound(t *testing.T) {
	r := NewProcessRegistry(4096)

	if err := r.Kill("nonexistent"); err == nil {
		t.Error("expected error for nonexistent process")
	}
}

func TestProcessRegistry_List(t *testing.T) {
	r := NewProcessRegistry(4096)

	r.Spawn("echo a", "", nil)
	r.Spawn("echo b", "", nil)

	list := r.List()
	if len(list) != 2 {
		t.Errorf("list len = %d, want 2", len(list))
	}
}

func TestProcessRegistry_Cleanup(t *testing.T) {
	r := NewProcessRegistry(4096)

	r.Spawn("sleep 60", "", nil)
	r.Spawn("sleep 60", "", nil)

	time.Sleep(100 * time.Millisecond)

	r.Cleanup()

	for _, entry := range r.List() {
		entry.mu.Lock()
		state := entry.State
		entry.mu.Unlock()
		if state == StateRunning {
			t.Errorf("process %s still running after cleanup", entry.ID)
		}
	}
}

func TestProcessRegistry_PollNotFound(t *testing.T) {
	r := NewProcessRegistry(4096)

	status := r.Poll("nope")
	if _, ok := status["error"]; !ok {
		t.Error("expected error key in poll result")
	}
}

func TestRingBuffer_Basic(t *testing.T) {
	rb := newRingBuffer(8)

	rb.Write([]byte("hello"))
	if got := rb.String(); got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := newRingBuffer(4)

	rb.Write([]byte("abcdef"))
	got := rb.String()
	// Should contain the last 4 bytes.
	if got != "cdef" {
		t.Errorf("got %q, want %q", got, "cdef")
	}
}

func TestRingBuffer_ExactFill(t *testing.T) {
	rb := newRingBuffer(5)

	rb.Write([]byte("12345"))
	got := rb.String()
	if got != "12345" {
		t.Errorf("got %q, want %q", got, "12345")
	}
}
