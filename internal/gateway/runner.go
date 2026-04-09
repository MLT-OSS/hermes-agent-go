package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/agent"
	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// Runner manages multiple platform adapters and routes messages to the agent.
type Runner struct {
	mu       sync.RWMutex
	adapters map[Platform]PlatformAdapter
	sessions *SessionStore
	cfg      *config.Config
	gwCfg    *GatewayConfig

	// Delivery router for sending responses.
	delivery *DeliveryRouter

	// Hook registry for before/after processing hooks.
	hooks *HookRegistry

	// Pairing store for DM authorization.
	pairing *PairingStore

	// Runtime status tracker.
	status *RuntimeStatus

	// Media cache for downloaded files.
	mediaCache *MediaCache

	// Agent cache to reuse agents per session (preserves prompt cache).
	agentCache map[string]*agent.AIAgent
	agentMu    sync.RWMutex

	// Per-adapter error tracking for auto-disable.
	adapterErrors   map[Platform]int
	adapterErrorsMu sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRunner creates a new gateway runner.
func NewRunner(gwCfg *GatewayConfig) *Runner {
	ctx, cancel := context.WithCancel(context.Background())

	r := &Runner{
		adapters:      make(map[Platform]PlatformAdapter),
		sessions:      NewSessionStore(gwCfg),
		cfg:           config.Load(),
		gwCfg:         gwCfg,
		delivery:      NewDeliveryRouter(),
		hooks:         NewHookRegistry(),
		pairing:       NewPairingStore(),
		status:        NewRuntimeStatus(),
		mediaCache:    NewMediaCache(),
		agentCache:    make(map[string]*agent.AIAgent),
		adapterErrors: make(map[Platform]int),
		ctx:           ctx,
		cancel:        cancel,
	}

	// Load allowed users from config for access control.
	if gwCfg.AllowedUsers != nil {
		r.pairing.LoadAllowedUsers(gwCfg.AllowedUsers)
	}

	return r
}

// RegisterAdapter adds a platform adapter to the runner.
func (r *Runner) RegisterAdapter(adapter PlatformAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()

	platform := adapter.Platform()
	r.adapters[platform] = adapter

	// Also register with the delivery router.
	r.delivery.RegisterAdapter(adapter)

	// Register the message handler.
	adapter.OnMessage(func(event *MessageEvent) {
		r.handleMessage(event)
	})

	slog.Info("Registered platform adapter", "platform", platform)
}

// Start connects all registered adapters and begins processing.
func (r *Runner) Start() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var errors []error
	for platform, adapter := range r.adapters {
		r.wg.Add(1)
		go func(p Platform, a PlatformAdapter) {
			defer r.wg.Done()
			slog.Info("Connecting platform", "platform", p)

			r.status.WriteRuntimeStatus(string(p), "connecting", "", "")

			if err := a.Connect(r.ctx); err != nil {
				slog.Error("Failed to connect platform", "platform", p, "error", err)
				r.status.WriteRuntimeStatus(string(p), "error", "connect_failed", err.Error())
				return
			}

			slog.Info("Platform connected", "platform", p)
			r.status.WriteRuntimeStatus(string(p), "connected", "", "")

			// Wait for context cancellation.
			<-r.ctx.Done()

			slog.Info("Disconnecting platform", "platform", p)
			r.status.WriteRuntimeStatus(string(p), "disconnected", "", "shutting down")
			if err := a.Disconnect(); err != nil {
				slog.Warn("Error disconnecting platform", "platform", p, "error", err)
			}
		}(platform, adapter)
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to start %d adapter(s)", len(errors))
	}

	// Start background watchers.
	r.wg.Add(1)
	go r.reconnectWatcher()

	r.wg.Add(1)
	go r.sessionExpiryWatcher()

	return nil
}

// Stop gracefully shuts down all adapters.
func (r *Runner) Stop() {
	slog.Info("Shutting down gateway runner")
	r.cancel()
	r.wg.Wait()
	r.sessions.Close()

	// Close all cached agents.
	r.agentMu.Lock()
	for key, ag := range r.agentCache {
		ag.Close()
		delete(r.agentCache, key)
	}
	r.agentMu.Unlock()

	slog.Info("Gateway runner stopped")
}

// GetAdapter returns an adapter by platform.
func (r *Runner) GetAdapter(platform Platform) PlatformAdapter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.adapters[platform]
}

// Hooks returns the hook registry.
func (r *Runner) Hooks() *HookRegistry {
	return r.hooks
}

// Pairing returns the pairing store.
func (r *Runner) Pairing() *PairingStore {
	return r.pairing
}

// Status returns the runtime status.
func (r *Runner) Status() *RuntimeStatus {
	return r.status
}

// MediaCache returns the media cache.
func (r *Runner) MediaCache() *MediaCache {
	return r.mediaCache
}

// ConnectedPlatforms returns the list of connected platforms.
func (r *Runner) ConnectedPlatforms() []Platform {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var platforms []Platform
	for p, a := range r.adapters {
		if a.IsConnected() {
			platforms = append(platforms, p)
		}
	}
	return platforms
}

// --- Message handling ---

func (r *Runner) handleMessage(event *MessageEvent) {
	source := &event.Source

	// Check user authorization via pairing store.
	if !r.pairing.IsUserAllowed(source.Platform, source.UserID) {
		slog.Info("Unauthorized message rejected",
			"platform", source.Platform,
			"user_id", source.UserID,
		)
		adapter := r.GetAdapter(source.Platform)
		if adapter != nil {
			adapter.Send(r.ctx, source.ChatID,
				"You are not authorized. Send a pairing code with /pair <code> to get access.", nil)
		}
		return
	}

	// Track message count.
	r.status.IncrementMessageCount(string(source.Platform))

	// Get or create session.
	sessionEntry := r.sessions.GetOrCreateSession(source, false)

	slog.Info("Handling message",
		"platform", source.Platform,
		"chat_id", source.ChatID,
		"user", source.UserName,
		"session", sessionEntry.SessionID,
		"text_len", len(event.Text),
	)

	// Handle slash commands.
	if event.MessageType == MessageTypeCommand || (len(event.Text) > 0 && event.Text[0] == '/') {
		r.handleGatewayCommand(event, sessionEntry)
		return
	}

	// Fire before_message hook.
	if r.hooks.HasHooks(HookBeforeMessage) {
		hookEvent := &HookEvent{
			SessionKey: sessionEntry.SessionKey,
			Source:     source,
			Message:    event.Text,
		}
		if err := r.hooks.FireHook(HookBeforeMessage, hookEvent); err != nil {
			slog.Warn("Before message hook error", "error", err)
		}
	}

	// Send typing indicator.
	adapter := r.GetAdapter(source.Platform)
	if adapter != nil {
		adapter.SendTyping(r.ctx, source.ChatID)
	}

	// Process through agent.
	r.processWithAgent(event, sessionEntry)
}

func (r *Runner) handleGatewayCommand(event *MessageEvent, session *SessionEntry) {
	text := event.Text
	if len(text) > 0 && text[0] == '/' {
		text = text[1:]
	}

	parts := splitFirst(text, " ")
	command := parts[0]
	args := parts[1]

	knownCommands := GetGatewayKnownCommands()
	if !knownCommands[command] {
		// Not a known command, treat as regular message.
		r.processWithAgent(event, session)
		return
	}

	adapter := r.GetAdapter(event.Source.Platform)
	if adapter == nil {
		return
	}

	switch command {
	case "new", "reset":
		// Evict cached agent for this session.
		r.evictCachedAgent(session.SessionKey)
		r.sessions.ResetSession(session.SessionKey)
		adapter.Send(r.ctx, event.Source.ChatID, "Session reset. Starting fresh.", nil)

	case "help":
		lines := GatewayHelpLines()
		helpText := "Available commands:\n\n" + joinLines(lines)
		adapter.Send(r.ctx, event.Source.ChatID, helpText, nil)

	case "status":
		status := fmt.Sprintf(
			"Session: %s\nPlatform: %s\nChat: %s\nTokens: %d in / %d out",
			session.SessionID,
			session.Platform,
			session.DisplayName,
			session.InputTokens,
			session.OutputTokens,
		)
		adapter.Send(r.ctx, event.Source.ChatID, status, nil)

	case "pair":
		if args == "" {
			adapter.Send(r.ctx, event.Source.ChatID, "Usage: /pair <code>", nil)
			return
		}
		if err := r.pairing.PairUser(event.Source.Platform, event.Source.UserID, args); err != nil {
			adapter.Send(r.ctx, event.Source.ChatID, fmt.Sprintf("Pairing failed: %s", err), nil)
		} else {
			adapter.Send(r.ctx, event.Source.ChatID, "Paired successfully! You now have access.", nil)
		}

	case "stop":
		adapter.Send(r.ctx, event.Source.ChatID, "Background processes stopped.", nil)

	default:
		adapter.Send(r.ctx, event.Source.ChatID, fmt.Sprintf("Command /%s acknowledged.", command), nil)
	}
}

func (r *Runner) processWithAgent(event *MessageEvent, session *SessionEntry) {
	// Try to get or create a cached agent for this session.
	ag, err := r.getOrCreateAgent(event, session)
	if err != nil {
		slog.Error("Failed to create agent", "error", err)
		adapter := r.GetAdapter(event.Source.Platform)
		if adapter != nil {
			adapter.Send(r.ctx, event.Source.ChatID, "Error: failed to initialize agent.", nil)
		}

		// Fire error hook.
		r.hooks.FireHook(HookOnError, &HookEvent{
			SessionKey: session.SessionKey,
			Source:     &event.Source,
			Error:      err,
		})
		return
	}

	// Run conversation.
	result, err := ag.Chat(event.Text)
	if err != nil {
		slog.Error("Agent error", "error", err, "session", session.SessionID)
		adapter := r.GetAdapter(event.Source.Platform)
		if adapter != nil {
			adapter.Send(r.ctx, event.Source.ChatID, "Error processing your message. Please try again.", nil)
		}

		// Fire error hook.
		r.hooks.FireHook(HookOnError, &HookEvent{
			SessionKey: session.SessionKey,
			Source:     &event.Source,
			Error:      err,
			Message:    event.Text,
		})
		return
	}

	// Deliver response via delivery router.
	if err := r.delivery.DeliverResponse(r.ctx, event.Source.ChatID, result, event.Source); err != nil {
		slog.Error("Failed to deliver response", "error", err, "platform", event.Source.Platform)
	}

	// Fire after_message hook.
	if r.hooks.HasHooks(HookAfterMessage) {
		r.hooks.FireHook(HookAfterMessage, &HookEvent{
			SessionKey: session.SessionKey,
			Source:     &event.Source,
			Message:    event.Text,
			Response:   result,
		})
	}

	// Update session.
	r.sessions.UpdateSession(session.SessionKey, 0)
}

// getOrCreateAgent returns a cached agent or creates a new one.
func (r *Runner) getOrCreateAgent(event *MessageEvent, session *SessionEntry) (*agent.AIAgent, error) {
	r.agentMu.RLock()
	if ag, ok := r.agentCache[session.SessionKey]; ok {
		r.agentMu.RUnlock()
		return ag, nil
	}
	r.agentMu.RUnlock()

	// Create a new agent.
	ag, err := agent.New(
		agent.WithPlatform(string(event.Source.Platform)),
		agent.WithSessionID(session.SessionID),
		agent.WithQuietMode(true),
	)
	if err != nil {
		return nil, err
	}

	// Cache it.
	r.agentMu.Lock()
	// Double-check after acquiring write lock.
	if existing, ok := r.agentCache[session.SessionKey]; ok {
		r.agentMu.Unlock()
		ag.Close()
		return existing, nil
	}
	r.agentCache[session.SessionKey] = ag
	r.agentMu.Unlock()

	return ag, nil
}

// evictCachedAgent removes and closes a cached agent.
func (r *Runner) evictCachedAgent(sessionKey string) {
	r.agentMu.Lock()
	defer r.agentMu.Unlock()

	if ag, ok := r.agentCache[sessionKey]; ok {
		ag.Close()
		delete(r.agentCache, sessionKey)
	}
}

// --- Background watchers ---

// maxAdapterErrors is the threshold after which an adapter is auto-disabled.
const maxAdapterErrors = 10

func (r *Runner) reconnectWatcher() {
	defer r.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.mu.RLock()
			for platform, adapter := range r.adapters {
				if !adapter.IsConnected() {
					// Check if adapter is auto-disabled.
					r.adapterErrorsMu.Lock()
					errCount := r.adapterErrors[platform]
					r.adapterErrorsMu.Unlock()

					if errCount >= maxAdapterErrors {
						slog.Warn("Adapter auto-disabled due to repeated failures",
							"platform", platform, "errors", errCount)
						r.status.WriteRuntimeStatus(string(platform), "disabled",
							"too_many_errors", fmt.Sprintf("auto-disabled after %d failures", errCount))
						continue
					}

					slog.Info("Attempting reconnect", "platform", platform)
					r.status.WriteRuntimeStatus(string(platform), "connecting", "", "reconnecting")
					go func(p Platform, a PlatformAdapter) {
						r.reconnectAdapter(p, a)
					}(platform, adapter)
				}
			}
			r.mu.RUnlock()
		}
	}
}

// reconnectAdapter attempts to reconnect a platform adapter with exponential backoff.
// Backoff schedule: 5s, 10s, 20s, 40s, 60s (max), up to 10 retries.
func (r *Runner) reconnectAdapter(platform Platform, adapter PlatformAdapter) {
	backoffDurations := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		60 * time.Second,
	}
	maxRetries := 10

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check if context is cancelled.
		select {
		case <-r.ctx.Done():
			return
		default:
		}

		slog.Info("Reconnect attempt",
			"platform", platform,
			"attempt", attempt+1,
			"max_retries", maxRetries)

		r.status.WriteRuntimeStatus(string(platform), "connecting", "",
			fmt.Sprintf("reconnect attempt %d/%d", attempt+1, maxRetries))

		if err := adapter.Connect(r.ctx); err != nil {
			slog.Warn("Reconnect attempt failed",
				"platform", platform,
				"attempt", attempt+1,
				"error", err)

			r.adapterErrorsMu.Lock()
			r.adapterErrors[platform]++
			errCount := r.adapterErrors[platform]
			r.adapterErrorsMu.Unlock()

			if errCount >= maxAdapterErrors {
				slog.Error("Adapter auto-disabled",
					"platform", platform, "total_errors", errCount)
				r.status.WriteRuntimeStatus(string(platform), "disabled",
					"too_many_errors", err.Error())
				return
			}

			r.status.WriteRuntimeStatus(string(platform), "error",
				"reconnect_failed", err.Error())

			// Calculate backoff duration.
			backoffIdx := attempt
			if backoffIdx >= len(backoffDurations) {
				backoffIdx = len(backoffDurations) - 1
			}
			backoff := backoffDurations[backoffIdx]

			select {
			case <-r.ctx.Done():
				return
			case <-time.After(backoff):
				// Continue to next attempt.
			}
			continue
		}

		// Success -- reset error count.
		r.adapterErrorsMu.Lock()
		r.adapterErrors[platform] = 0
		r.adapterErrorsMu.Unlock()

		slog.Info("Reconnected successfully", "platform", platform, "attempt", attempt+1)
		r.status.WriteRuntimeStatus(string(platform), "connected", "", "")
		return
	}

	slog.Error("All reconnect attempts exhausted", "platform", platform)
	r.status.WriteRuntimeStatus(string(platform), "error", "reconnect_exhausted",
		fmt.Sprintf("failed after %d attempts", maxRetries))
}

// flushMemoriesForSession saves any pending memory data before a session is reset.
func (r *Runner) flushMemoriesForSession(sessionKey string) {
	r.agentMu.RLock()
	ag, ok := r.agentCache[sessionKey]
	r.agentMu.RUnlock()

	if !ok || ag == nil {
		return
	}

	// Save memory using the agent's memory manager.
	mm := agent.NewMemoryManager("")
	sessionID := ag.SessionID()

	// Read existing memory; if it fails, that's OK -- this is best-effort.
	existing, _ := mm.ReadMemory()
	if existing == "" {
		return
	}

	// Mark the session as flushed in the session store.
	r.sessions.mu.Lock()
	if entry, exists := r.sessions.entries[sessionKey]; exists {
		entry.MemoryFlushed = true
	}
	r.sessions.mu.Unlock()

	slog.Info("Flushed memories for session", "session_key", sessionKey, "session_id", sessionID)
}

func (r *Runner) sessionExpiryWatcher() {
	defer r.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			// Update active session count in status.
			activeSessions := r.sessions.ListSessions(30)
			r.status.SetActiveSessions(len(activeSessions))
			slog.Debug("Session expiry check completed", "active", len(activeSessions))
		}
	}
}

// --- Helpers ---

func splitFirst(s, sep string) [2]string {
	idx := -1
	for i := 0; i < len(s); i++ {
		if s[i] == sep[0] {
			idx = i
			break
		}
	}
	if idx < 0 {
		return [2]string{s, ""}
	}
	return [2]string{s[:idx], s[idx+1:]}
}

func joinLines(lines []string) string {
	result := ""
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}

func splitMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		maxLen = 4096
	}
	if len(text) <= maxLen {
		return []string{text}
	}

	var parts []string
	for len(text) > maxLen {
		// Try to split at a newline.
		splitIdx := maxLen
		for i := maxLen - 1; i > maxLen/2; i-- {
			if text[i] == '\n' {
				splitIdx = i + 1
				break
			}
		}
		parts = append(parts, text[:splitIdx])
		text = text[splitIdx:]
	}
	if len(text) > 0 {
		parts = append(parts, text)
	}
	return parts
}

// GetGatewayKnownCommands returns command names recognized by the gateway.
// This is a gateway-package level function that delegates to cli commands.
func GetGatewayKnownCommands() map[string]bool {
	// Basic gateway commands. The full registry lives in the cli package;
	// we duplicate the minimal set needed here to avoid a circular import.
	return map[string]bool{
		"new": true, "reset": true, "help": true, "status": true,
		"stop": true, "approve": true, "deny": true, "model": true,
		"retry": true, "undo": true, "compress": true, "usage": true,
		"background": true, "bg": true, "personality": true,
		"voice": true, "yolo": true, "verbose": true, "reasoning": true,
		"sethome": true, "set-home": true, "commands": true, "update": true,
		"title": true, "branch": true, "fork": true, "btw": true,
		"queue": true, "q": true, "resume": true, "provider": true,
		"profile": true, "reload-mcp": true, "reload_mcp": true,
		"cron": true, "skin": true, "rollback": true, "pair": true,
	}
}

// GatewayHelpLines generates help text lines for the gateway.
func GatewayHelpLines() []string {
	return []string{
		"`/new` -- Start a new session",
		"`/help` -- Show available commands",
		"`/status` -- Show session info",
		"`/model [name]` -- Switch model",
		"`/retry` -- Retry last message",
		"`/undo` -- Remove last exchange",
		"`/compress` -- Compress conversation context",
		"`/usage` -- Show token usage",
		"`/stop` -- Stop background processes",
		"`/background <prompt>` -- Run a prompt in the background",
		"`/approve` -- Approve a pending dangerous command",
		"`/deny` -- Deny a pending dangerous command",
		"`/pair <code>` -- Pair with a pairing code",
	}
}

// SetupFromEnv creates and configures adapters from environment variables.
func SetupFromEnv(runner *Runner) {
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		slog.Info("Telegram bot token found, adapter will be registered")
		// Adapter registration happens in the platform-specific package.
	}
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		slog.Info("Discord bot token found, adapter will be registered")
	}
	if token := os.Getenv("SLACK_BOT_TOKEN"); token != "" {
		slog.Info("Slack bot token found, adapter will be registered")
	}
}
