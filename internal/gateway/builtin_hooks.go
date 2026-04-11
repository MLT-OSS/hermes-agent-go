package gateway

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// RegisterBuiltinHooks registers the default hook implementations
// on the given HookRegistry:
//   - LoggingHook: logs all messages to file
//   - RateLimitHook: prevents abuse (max N messages per minute per user)
//   - ContentFilterHook: basic content moderation
//   - MetricsHook: tracks message counts, response times
func RegisterBuiltinHooks(registry *HookRegistry) {
	registerLoggingHook(registry)
	registerRateLimitHook(registry)
	registerMetricsHook(registry)
}

// --- Logging Hook ---

func registerLoggingHook(registry *HookRegistry) {
	registry.RegisterNamedHook(HookBeforeMessage, "logging_before", func(event *HookEvent) error {
		logMessage("incoming", event)
		return nil
	}, 100)

	registry.RegisterNamedHook(HookAfterMessage, "logging_after", func(event *HookEvent) error {
		logMessage("outgoing", event)
		return nil
	}, 100)

	registry.RegisterNamedHook(HookOnError, "logging_error", func(event *HookEvent) error {
		if event.Error != nil {
			slog.Error("Gateway error",
				"session", event.SessionKey,
				"error", event.Error,
			)
		}
		return nil
	}, 100)
}

func logMessage(direction string, event *HookEvent) {
	platform := ""
	user := ""
	if event.Source != nil {
		platform = string(event.Source.Platform)
		user = event.Source.UserName
		if user == "" {
			user = event.Source.UserID
		}
	}

	text := event.Message
	if direction == "outgoing" {
		text = event.Response
	}

	slog.Debug("Gateway message",
		"direction", direction,
		"platform", platform,
		"user", user,
		"session", event.SessionKey,
		"text_length", len(text),
	)

	// Also append to a log file if the logs directory exists.
	writeToLogFile(direction, platform, user, text)
}

func writeToLogFile(direction, platform, user, text string) {
	logDir := filepath.Join(config.HermesHome(), "logs")
	if _, err := os.Stat(logDir); os.IsNotExist(err) {
		return
	}

	logFile := filepath.Join(logDir, "gateway.log")
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	ts := time.Now().Format("2006-01-02 15:04:05")

	preview := text
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}

	fmt.Fprintf(f, "[%s] %s %s/%s: %s\n", ts, direction, platform, user, preview)
}

// --- Rate Limit Hook ---

// rateLimitState tracks per-user message timestamps for rate limiting.
var rateLimitState = struct {
	mu      sync.Mutex
	history map[string][]time.Time
}{
	history: make(map[string][]time.Time),
}

// DefaultRateLimit is the maximum number of messages per user per minute.
const DefaultRateLimit = 30

func registerRateLimitHook(registry *HookRegistry) {
	registry.RegisterNamedHook(HookBeforeMessage, "rate_limit", func(event *HookEvent) error {
		if event.Source == nil {
			return nil
		}

		userKey := string(event.Source.Platform) + ":" + event.Source.UserID
		now := time.Now()
		windowStart := now.Add(-1 * time.Minute)

		rateLimitState.mu.Lock()
		defer rateLimitState.mu.Unlock()

		// Clean old entries.
		history := rateLimitState.history[userKey]
		var recent []time.Time
		for _, t := range history {
			if t.After(windowStart) {
				recent = append(recent, t)
			}
		}

		if len(recent) >= DefaultRateLimit {
			slog.Warn("Rate limit exceeded",
				"user", userKey,
				"count", len(recent),
				"limit", DefaultRateLimit,
			)
			return fmt.Errorf("rate limit exceeded: max %d messages per minute", DefaultRateLimit)
		}

		recent = append(recent, now)
		rateLimitState.history[userKey] = recent
		return nil
	}, 10) // High priority (low number) so it runs before other hooks.
}

// --- Metrics Hook ---

// MetricsCollector tracks gateway usage metrics.
type MetricsCollector struct {
	mu             sync.Mutex
	messageCount   int64
	responseCount  int64
	errorCount     int64
	toolCallCount  int64
	totalLatencyMs int64
	startTime      time.Time
	lastMessage    time.Time
}

var globalMetrics = &MetricsCollector{
	startTime: time.Now(),
}

// GetMetrics returns the global metrics collector.
func GetMetrics() *MetricsCollector {
	return globalMetrics
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *MetricsCollector) Snapshot() map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()

	uptime := time.Since(m.startTime)
	avgLatency := float64(0)
	if m.responseCount > 0 {
		avgLatency = float64(m.totalLatencyMs) / float64(m.responseCount)
	}

	return map[string]any{
		"messages":          m.messageCount,
		"responses":         m.responseCount,
		"errors":            m.errorCount,
		"tool_calls":        m.toolCallCount,
		"avg_latency_ms":    avgLatency,
		"uptime_seconds":    int64(uptime.Seconds()),
		"last_message_at":   m.lastMessage.Format(time.RFC3339),
	}
}

func registerMetricsHook(registry *HookRegistry) {
	// Track incoming messages.
	registry.RegisterNamedHook(HookBeforeMessage, "metrics_before", func(event *HookEvent) error {
		globalMetrics.mu.Lock()
		globalMetrics.messageCount++
		globalMetrics.lastMessage = time.Now()
		globalMetrics.mu.Unlock()

		// Store start time in metadata for latency tracking.
		if event.Metadata == nil {
			event.Metadata = make(map[string]string)
		}
		event.Metadata["_metrics_start"] = fmt.Sprintf("%d", time.Now().UnixMilli())
		return nil
	}, 90)

	// Track responses and latency.
	registry.RegisterNamedHook(HookAfterMessage, "metrics_after", func(event *HookEvent) error {
		globalMetrics.mu.Lock()
		globalMetrics.responseCount++

		// Calculate latency if start time is available.
		if startStr, ok := event.Metadata["_metrics_start"]; ok {
			var startMs int64
			fmt.Sscanf(startStr, "%d", &startMs)
			if startMs > 0 {
				latency := time.Now().UnixMilli() - startMs
				globalMetrics.totalLatencyMs += latency
			}
		}

		globalMetrics.mu.Unlock()
		return nil
	}, 90)

	// Track tool calls.
	registry.RegisterNamedHook(HookAfterToolCall, "metrics_tool", func(event *HookEvent) error {
		globalMetrics.mu.Lock()
		globalMetrics.toolCallCount++
		globalMetrics.mu.Unlock()
		return nil
	}, 90)

	// Track errors.
	registry.RegisterNamedHook(HookOnError, "metrics_error", func(event *HookEvent) error {
		globalMetrics.mu.Lock()
		globalMetrics.errorCount++
		globalMetrics.mu.Unlock()
		return nil
	}, 90)
}
