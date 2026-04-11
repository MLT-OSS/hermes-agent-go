package agent

import (
	"sync"
	"testing"
	"time"
)

// stubClock returns a controllable clock for tests.
func stubClock(start time.Time) (func() time.Time, func(d time.Duration)) {
	mu := sync.Mutex{}
	now := start
	return func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		}, func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			now = now.Add(d)
		}
}

func TestRecordEvent(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	tracker.RecordEvent("openai", "gpt-4", 0, 429)
	tracker.RecordEvent("openai", "gpt-4", 10*time.Second, 429)

	throttle, backoff := tracker.ShouldThrottle("openai", "gpt-4")
	if !throttle {
		t.Fatal("expected throttle after recording events")
	}
	if backoff != 10*time.Second {
		t.Errorf("backoff = %v, want 10s (2 events × 5s = 10s)", backoff)
	}

	// Different provider/model should be independent.
	throttle, _ = tracker.ShouldThrottle("anthropic", "claude-3")
	if throttle {
		t.Error("expected no throttle for unrelated provider")
	}
}

func TestWindowPruning(t *testing.T) {
	nowFn, advance := stubClock(time.Now())
	tracker := NewRateLimitTracker(5 * time.Minute)
	tracker.now = nowFn

	// Record two events "now".
	tracker.RecordEvent("openai", "gpt-4", 0, 429)
	tracker.RecordEvent("openai", "gpt-4", 0, 429)

	throttle, _ := tracker.ShouldThrottle("openai", "gpt-4")
	if !throttle {
		t.Fatal("expected throttle within window")
	}

	// Advance past the window.
	advance(6 * time.Minute)

	throttle, _ = tracker.ShouldThrottle("openai", "gpt-4")
	if throttle {
		t.Error("expected no throttle after window expires")
	}
}

func TestShouldThrottle_NoEvents(t *testing.T) {
	tracker := NewRateLimitTracker(0) // uses default 5m

	throttle, backoff := tracker.ShouldThrottle("openai", "gpt-4")
	if throttle {
		t.Error("expected no throttle with no events")
	}
	if backoff != 0 {
		t.Errorf("backoff = %v, want 0", backoff)
	}
}

func TestAdaptiveBackoff(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	tests := []struct {
		eventCount int
		wantBackoff time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 15 * time.Second},
		{4, 20 * time.Second},
		{5, 30 * time.Second},
		{6, 36 * time.Second},
		{10, 60 * time.Second}, // capped
		{15, 60 * time.Second}, // still capped
	}

	for _, tt := range tests {
		tracker.Reset("p", "m")
		for i := 0; i < tt.eventCount; i++ {
			tracker.RecordEvent("p", "m", 0, 429)
		}
		got := tracker.GetBackoffDuration("p", "m")
		if got != tt.wantBackoff {
			t.Errorf("events=%d: backoff = %v, want %v", tt.eventCount, got, tt.wantBackoff)
		}
	}
}

func TestBackoff_RetryAfterHonoured(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	// Single event with a large RetryAfter should override the 5s default.
	tracker.RecordEvent("openai", "gpt-4", 30*time.Second, 429)

	got := tracker.GetBackoffDuration("openai", "gpt-4")
	if got != 30*time.Second {
		t.Errorf("backoff = %v, want 30s (RetryAfter override)", got)
	}
}

func TestReset(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	tracker.RecordEvent("openai", "gpt-4", 0, 429)
	tracker.Reset("openai", "gpt-4")

	throttle, _ := tracker.ShouldThrottle("openai", "gpt-4")
	if throttle {
		t.Error("expected no throttle after reset")
	}
}

func TestGetBackoffDuration_NoEvents(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	got := tracker.GetBackoffDuration("openai", "gpt-4")
	if got != 0 {
		t.Errorf("backoff = %v, want 0 for no events", got)
	}
}

func TestConcurrency(t *testing.T) {
	tracker := NewRateLimitTracker(5 * time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.RecordEvent("openai", "gpt-4", 0, 429)
			tracker.ShouldThrottle("openai", "gpt-4")
			tracker.GetBackoffDuration("openai", "gpt-4")
		}()
	}
	wg.Wait()

	// Verify it didn't panic and has recorded events.
	throttle, _ := tracker.ShouldThrottle("openai", "gpt-4")
	if !throttle {
		t.Error("expected throttle after concurrent writes")
	}
}

func TestPruningOnRecord(t *testing.T) {
	nowFn, advance := stubClock(time.Now())
	tracker := NewRateLimitTracker(5 * time.Minute)
	tracker.now = nowFn

	// Record old events.
	tracker.RecordEvent("openai", "gpt-4", 0, 429)
	tracker.RecordEvent("openai", "gpt-4", 0, 429)

	// Advance past window.
	advance(6 * time.Minute)

	// Record a new event — old ones should be pruned.
	tracker.RecordEvent("openai", "gpt-4", 0, 429)

	// Only 1 event should remain (the new one).
	got := tracker.GetBackoffDuration("openai", "gpt-4")
	if got != 5*time.Second {
		t.Errorf("backoff = %v, want 5s (1 event after pruning)", got)
	}
}
