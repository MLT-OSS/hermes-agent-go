package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PlatformMaxMessageLength defines per-platform message length limits.
var PlatformMaxMessageLength = map[Platform]int{
	PlatformDMWork: 4096,
}

// DeliveryRouter routes responses to the correct platform adapter with
// message splitting, media extraction, and retry logic.
type DeliveryRouter struct {
	mu       sync.RWMutex
	adapters map[Platform]PlatformAdapter

	maxRetries int
	retryDelay time.Duration
}

// NewDeliveryRouter creates a new delivery router.
func NewDeliveryRouter() *DeliveryRouter {
	return &DeliveryRouter{
		adapters:   make(map[Platform]PlatformAdapter),
		maxRetries: 3,
		retryDelay: 1 * time.Second,
	}
}

// RegisterAdapter registers a platform adapter with the router.
func (d *DeliveryRouter) RegisterAdapter(adapter PlatformAdapter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.adapters[adapter.Platform()] = adapter
}

// GetAdapter returns the adapter for a platform.
func (d *DeliveryRouter) GetAdapter(platform Platform) PlatformAdapter {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.adapters[platform]
}

// DeliverResponse delivers a response to the correct platform, handling
// message splitting, media extraction, and retry logic.
func (d *DeliveryRouter) DeliverResponse(ctx context.Context, chatID string, content string, source SessionSource) error {
	adapter := d.GetAdapter(source.Platform)
	if adapter == nil {
		return fmt.Errorf("no adapter registered for platform %s", source.Platform)
	}

	// Extract media from the response.
	mediaFiles, cleanedText := extractMediaFromContent(content)

	// Determine max message length for this platform.
	maxLen := d.maxMessageLength(source.Platform)

	// Build metadata.
	metadata := make(map[string]string)
	if source.ThreadID != "" {
		metadata["thread_id"] = source.ThreadID
	}

	// Split and send text messages.
	if strings.TrimSpace(cleanedText) != "" {
		messages := splitMessage(cleanedText, maxLen)
		for _, msg := range messages {
			if err := d.sendWithRetry(ctx, adapter, chatID, msg, metadata); err != nil {
				return fmt.Errorf("send text message: %w", err)
			}
		}
	}

	// Send media files separately.
	for _, media := range mediaFiles {
		if err := d.sendMedia(ctx, adapter, chatID, media, metadata); err != nil {
			slog.Warn("Failed to send media", "path", media.Path, "error", err)
			// Non-fatal: continue sending remaining media.
		}
	}

	return nil
}

// maxMessageLength returns the maximum message length for a platform.
func (d *DeliveryRouter) maxMessageLength(platform Platform) int {
	if maxLen, ok := PlatformMaxMessageLength[platform]; ok && maxLen > 0 {
		return maxLen
	}
	return 4096 // default
}

// sendWithRetry sends a message with retry logic for transient failures.
func (d *DeliveryRouter) sendWithRetry(ctx context.Context, adapter PlatformAdapter, chatID, text string, metadata map[string]string) error {
	var lastErr error
	delay := d.retryDelay

	for attempt := 0; attempt <= d.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		result, err := adapter.Send(ctx, chatID, text, metadata)
		if err != nil {
			lastErr = err
			slog.Debug("Send attempt failed", "attempt", attempt+1, "error", err)
			continue
		}
		if result != nil && !result.Success {
			if result.Retryable {
				lastErr = fmt.Errorf("send failed: %s", result.Error)
				slog.Debug("Send returned retryable error", "attempt", attempt+1, "error", result.Error)
				continue
			}
			return fmt.Errorf("send failed (non-retryable): %s", result.Error)
		}
		return nil
	}

	return fmt.Errorf("send failed after %d retries: %w", d.maxRetries, lastErr)
}

// mediaFileInfo holds info about a media file extracted from content.
type mediaFileInfo struct {
	Path    string
	IsVoice bool
	IsImage bool
	IsDoc   bool
	Caption string
}

// sendMedia sends a media file via the appropriate adapter method.
func (d *DeliveryRouter) sendMedia(ctx context.Context, adapter PlatformAdapter, chatID string, media mediaFileInfo, metadata map[string]string) error {
	if media.IsVoice {
		_, err := adapter.SendVoice(ctx, chatID, media.Path, metadata)
		return err
	}
	if media.IsImage {
		_, err := adapter.SendImage(ctx, chatID, media.Path, media.Caption, metadata)
		return err
	}
	if media.IsDoc {
		_, err := adapter.SendDocument(ctx, chatID, media.Path, metadata)
		return err
	}

	// Default: try as document.
	_, err := adapter.SendDocument(ctx, chatID, media.Path, metadata)
	return err
}

// imageURLPattern matches markdown image syntax and bare image URLs.
var imageURLPattern = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)

// extractMediaFromContent extracts MEDIA: tags and image references from text.
func extractMediaFromContent(text string) ([]mediaFileInfo, string) {
	var mediaFiles []mediaFileInfo
	lines := strings.Split(text, "\n")
	var cleanLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Handle MEDIA: prefix.
		if strings.HasPrefix(trimmed, "MEDIA:") {
			path := strings.TrimSpace(strings.TrimPrefix(trimmed, "MEDIA:"))
			if path != "" {
				info := classifyMediaFile(path, "")
				mediaFiles = append(mediaFiles, info)
				continue
			}
		}

		cleanLines = append(cleanLines, line)
	}

	cleanedText := strings.Join(cleanLines, "\n")
	return mediaFiles, cleanedText
}

// classifyMediaFile classifies a file path into media categories.
func classifyMediaFile(path, caption string) mediaFileInfo {
	lower := strings.ToLower(path)
	info := mediaFileInfo{
		Path:    path,
		Caption: caption,
	}

	switch {
	case strings.HasSuffix(lower, ".ogg"),
		strings.HasSuffix(lower, ".opus"),
		strings.HasSuffix(lower, ".mp3"),
		strings.HasSuffix(lower, ".wav"):
		info.IsVoice = true
	case strings.HasSuffix(lower, ".jpg"),
		strings.HasSuffix(lower, ".jpeg"),
		strings.HasSuffix(lower, ".png"),
		strings.HasSuffix(lower, ".gif"),
		strings.HasSuffix(lower, ".webp"):
		info.IsImage = true
	default:
		info.IsDoc = true
	}

	return info
}
