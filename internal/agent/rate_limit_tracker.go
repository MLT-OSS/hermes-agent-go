package agent

import (
	"log/slog"
	"sync"
	"time"
)

// RateLimitEvent records a single rate-limit occurrence from a provider.
type RateLimitEvent struct {
	Timestamp  time.Time
	RetryAfter time.Duration
	StatusCode int
}

// RateLimitTracker tracks per-provider rate-limit events and computes
// adaptive backoff durations. It is safe for concurrent use.
type RateLimitTracker struct {
	mu         sync.RWMutex
	events     map[string][]RateLimitEvent
	windowSize time.Duration
	now        func() time.Time // injectable clock for testing
}

// NewRateLimitTracker creates a tracker with the given sliding window size.
// A zero windowSize defaults to 5 minutes.
func NewRateLimitTracker(windowSize time.Duration) *RateLimitTracker {
	if windowSize <= 0 {
		windowSize = 5 * time.Minute
	}
	return &RateLimitTracker{
		events:     make(map[string][]RateLimitEvent),
		windowSize: windowSize,
		now:        time.Now,
	}
}

// providerKey builds the map key for a provider/model pair.
func providerKey(provider, model string) string {
	return provider + ":" + model
}

// RecordEvent records a rate-limit event for the given provider and model.
func (t *RateLimitTracker) RecordEvent(provider, model string, retryAfter time.Duration, statusCode int) {
	key := providerKey(provider, model)

	t.mu.Lock()
	defer t.mu.Unlock()

	t.pruneKeyLocked(key)

	t.events[key] = append(t.events[key], RateLimitEvent{
		Timestamp:  t.now(),
		RetryAfter: retryAfter,
		StatusCode: statusCode,
	})

	slog.Debug("rate limit event recorded",
		"provider", provider,
		"model", model,
		"events_in_window", len(t.events[key]),
	)
}

// ShouldThrottle reports whether calls to the given provider/model should be
// delayed. When throttling is advised it returns the recommended backoff.
func (t *RateLimitTracker) ShouldThrottle(provider, model string) (bool, time.Duration) {
	key := providerKey(provider, model)

	t.mu.RLock()
	defer t.mu.RUnlock()

	events := t.activeEventsLocked(key)
	if len(events) == 0 {
		return false, 0
	}

	backoff := t.computeBackoff(events)
	return true, backoff
}

// GetBackoffDuration returns the adaptive backoff for the provider/model
// based on recent event density. It returns 0 when no events are recorded.
func (t *RateLimitTracker) GetBackoffDuration(provider, model string) time.Duration {
	key := providerKey(provider, model)

	t.mu.RLock()
	defer t.mu.RUnlock()

	events := t.activeEventsLocked(key)
	return t.computeBackoff(events)
}

// Reset clears all recorded events for a provider/model pair.
func (t *RateLimitTracker) Reset(provider, model string) {
	key := providerKey(provider, model)

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.events, key)

	slog.Debug("rate limit tracker reset", "provider", provider, "model", model)
}

// computeBackoff implements the adaptive backoff schedule:
//
//	1 event  → 5 s
//	2 events → 10 s
//	3 events → 15 s
//	4 events → 20 s
//	5+ events → 30–60 s (scales linearly, capped at 60 s)
//
// If the most recent event carries a RetryAfter hint that exceeds the
// computed value, the hint is used instead.
func (t *RateLimitTracker) computeBackoff(events []RateLimitEvent) time.Duration {
	n := len(events)
	if n == 0 {
		return 0
	}

	var backoff time.Duration
	switch {
	case n < 5:
		backoff = time.Duration(n) * 5 * time.Second
	default:
		// 5+ events: 30 s base, +6 s per additional event, capped at 60 s.
		backoff = 30*time.Second + time.Duration(n-5)*6*time.Second
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}

	// Honour RetryAfter from the most recent event when it is larger.
	latest := events[n-1]
	if latest.RetryAfter > backoff {
		backoff = latest.RetryAfter
	}

	return backoff
}

// activeEventsLocked returns events within the window. Caller holds at least RLock.
func (t *RateLimitTracker) activeEventsLocked(key string) []RateLimitEvent {
	all := t.events[key]
	cutoff := t.now().Add(-t.windowSize)

	var active []RateLimitEvent
	for _, e := range all {
		if !e.Timestamp.Before(cutoff) {
			active = append(active, e)
		}
	}
	return active
}

// pruneKeyLocked removes expired events for a single key. Caller holds Lock.
func (t *RateLimitTracker) pruneKeyLocked(key string) {
	all := t.events[key]
	if len(all) == 0 {
		return
	}

	cutoff := t.now().Add(-t.windowSize)
	kept := all[:0]
	for _, e := range all {
		if !e.Timestamp.Before(cutoff) {
			kept = append(kept, e)
		}
	}

	if len(kept) == 0 {
		delete(t.events, key)
	} else {
		t.events[key] = kept
	}
}
