package gateway

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// StreamConsumer accumulates streaming text deltas and periodically flushes
// them to a platform adapter. This prevents flooding the platform with
// per-token messages and respects rate limits.
type StreamConsumer struct {
	adapter  PlatformAdapter
	chatID   string
	metadata map[string]string

	mu       sync.Mutex
	buffer   strings.Builder
	lastSent time.Time
	minDelay time.Duration // minimum delay between sends

	// flushTimer triggers a flush after a period of inactivity.
	flushTimer *time.Timer
	closed     bool
}

// NewStreamConsumer creates a StreamConsumer that sends accumulated text
// to the given adapter/chat. minDelay controls the minimum time between
// sends (e.g., 1s for Telegram rate limit protection).
func NewStreamConsumer(adapter PlatformAdapter, chatID string) *StreamConsumer {
	sc := &StreamConsumer{
		adapter:  adapter,
		chatID:   chatID,
		metadata: make(map[string]string),
		minDelay: defaultMinDelay(adapter.Platform()),
		lastSent: time.Now().Add(-10 * time.Second), // allow immediate first send
	}
	return sc
}

// SetMetadata sets metadata (e.g., thread_id) for outgoing messages.
func (sc *StreamConsumer) SetMetadata(metadata map[string]string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.metadata = metadata
}

// OnDelta receives a text chunk from the LLM stream.
// Text is buffered and flushed periodically based on the minimum delay.
func (sc *StreamConsumer) OnDelta(text string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.closed {
		return
	}

	sc.buffer.WriteString(text)

	// Check if enough time has passed since the last send.
	if time.Since(sc.lastSent) >= sc.minDelay && sc.buffer.Len() > 0 {
		sc.flushLocked()
		return
	}

	// Set up a flush timer if not already active.
	if sc.flushTimer == nil {
		remaining := sc.minDelay - time.Since(sc.lastSent)
		if remaining < 0 {
			remaining = sc.minDelay
		}
		sc.flushTimer = time.AfterFunc(remaining, func() {
			sc.mu.Lock()
			defer sc.mu.Unlock()
			sc.flushTimer = nil
			if sc.buffer.Len() > 0 && !sc.closed {
				sc.flushLocked()
			}
		})
	}
}

// Flush sends any remaining buffered text immediately.
func (sc *StreamConsumer) Flush() error {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	if sc.flushTimer != nil {
		sc.flushTimer.Stop()
		sc.flushTimer = nil
	}

	if sc.buffer.Len() == 0 {
		return nil
	}

	return sc.flushLocked()
}

// Close flushes remaining text and marks the consumer as closed.
func (sc *StreamConsumer) Close() error {
	err := sc.Flush()
	sc.mu.Lock()
	sc.closed = true
	sc.mu.Unlock()
	return err
}

// flushLocked sends the buffered text. Must be called with sc.mu held.
func (sc *StreamConsumer) flushLocked() error {
	text := sc.buffer.String()
	sc.buffer.Reset()
	sc.lastSent = time.Now()

	if strings.TrimSpace(text) == "" {
		return nil
	}

	// Send outside the lock would be better for concurrency, but
	// we keep it simple here. The adapter.Send should be non-blocking
	// or have its own timeout.
	_, err := sc.adapter.Send(context.Background(), sc.chatID, text, sc.metadata)
	if err != nil {
		slog.Warn("StreamConsumer flush failed",
			"chat_id", sc.chatID,
			"platform", sc.adapter.Platform(),
			"error", err,
		)
	}
	return err
}

// defaultMinDelay returns a sensible minimum delay for each platform
// to avoid rate limiting.
func defaultMinDelay(platform Platform) time.Duration {
	switch platform {
	case PlatformDMWork:
		return 500 * time.Millisecond
	default:
		return 500 * time.Millisecond
	}
}
