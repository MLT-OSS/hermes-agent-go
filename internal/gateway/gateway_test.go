package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- DeliveryRouter tests ---

func TestDeliveryRouter_MaxMessageLength(t *testing.T) {
	dr := NewDeliveryRouter()

	// Known platform
	if dr.maxMessageLength(PlatformDMWork) != 4096 {
		t.Errorf("Expected 4096 for dmwork, got %d", dr.maxMessageLength(PlatformDMWork))
	}
	if dr.maxMessageLength(PlatformDMWork) != 4096 {
		t.Errorf("Expected 4096 for dmwork, got %d", dr.maxMessageLength(PlatformDMWork))
	}
	if dr.maxMessageLength(PlatformDMWork) != 4096 {
		t.Errorf("Expected 4096 for dmwork, got %d", dr.maxMessageLength(PlatformDMWork))
	}

	// Unlimited platform defaults to 4096
	if dr.maxMessageLength(PlatformDMWork) != 4096 {
		t.Errorf("Expected 4096 for dmwork (default), got %d", dr.maxMessageLength(PlatformDMWork))
	}

	// Unknown platform defaults to 4096
	if dr.maxMessageLength("unknown") != 4096 {
		t.Errorf("Expected 4096 for unknown platform, got %d", dr.maxMessageLength("unknown"))
	}
}

func TestDeliveryRouter_RegisterAndGetAdapter(t *testing.T) {
	dr := NewDeliveryRouter()
	adapter := &mockAdapter{platform: PlatformDMWork}

	dr.RegisterAdapter(adapter)
	got := dr.GetAdapter(PlatformDMWork)
	if got == nil {
		t.Error("Expected adapter to be registered")
	}

	got = dr.GetAdapter(PlatformLocal)
	if got != nil {
		t.Error("Expected nil for unregistered platform")
	}
}

func TestDeliveryRouter_DeliverResponse_NoAdapter(t *testing.T) {
	dr := NewDeliveryRouter()
	err := dr.DeliverResponse(context.Background(), "chat1", "Hello", SessionSource{Platform: PlatformDMWork})
	if err == nil {
		t.Error("Expected error when no adapter registered")
	}
	if !strings.Contains(err.Error(), "no adapter") {
		t.Errorf("Expected 'no adapter' error, got: %s", err)
	}
}

// --- ExtractMediaFromContent tests ---

func TestExtractMediaFromContent(t *testing.T) {
	text := "Hello world\nMEDIA:/path/to/image.png\nMore text"
	media, cleaned := extractMediaFromContent(text)

	if len(media) != 1 {
		t.Fatalf("Expected 1 media file, got %d", len(media))
	}
	if media[0].Path != "/path/to/image.png" {
		t.Errorf("Expected path '/path/to/image.png', got '%s'", media[0].Path)
	}
	if media[0].IsImage != true {
		t.Error("Expected PNG to be classified as image")
	}
	if strings.Contains(cleaned, "MEDIA:") {
		t.Error("Cleaned text should not contain MEDIA: line")
	}
	if !strings.Contains(cleaned, "Hello world") {
		t.Error("Cleaned text should keep non-media lines")
	}
}

func TestExtractMediaFromContent_NoMedia(t *testing.T) {
	text := "Just regular text\nno media here"
	media, cleaned := extractMediaFromContent(text)
	if len(media) != 0 {
		t.Errorf("Expected 0 media files, got %d", len(media))
	}
	if cleaned != text {
		t.Error("Cleaned text should match original when no media")
	}
}

func TestClassifyMediaFile(t *testing.T) {
	tests := []struct {
		path     string
		isVoice  bool
		isImage  bool
		isDoc    bool
	}{
		{"/path/file.ogg", true, false, false},
		{"/path/file.mp3", true, false, false},
		{"/path/file.wav", true, false, false},
		{"/path/file.opus", true, false, false},
		{"/path/file.jpg", false, true, false},
		{"/path/file.jpeg", false, true, false},
		{"/path/file.png", false, true, false},
		{"/path/file.gif", false, true, false},
		{"/path/file.webp", false, true, false},
		{"/path/file.pdf", false, false, true},
		{"/path/file.txt", false, false, true},
	}

	for _, tt := range tests {
		info := classifyMediaFile(tt.path, "")
		if info.IsVoice != tt.isVoice {
			t.Errorf("classifyMediaFile(%q): IsVoice = %v, want %v", tt.path, info.IsVoice, tt.isVoice)
		}
		if info.IsImage != tt.isImage {
			t.Errorf("classifyMediaFile(%q): IsImage = %v, want %v", tt.path, info.IsImage, tt.isImage)
		}
		if info.IsDoc != tt.isDoc {
			t.Errorf("classifyMediaFile(%q): IsDoc = %v, want %v", tt.path, info.IsDoc, tt.isDoc)
		}
	}
}

// --- HookRegistry tests ---

func TestHookRegistry_RegisterAndFire(t *testing.T) {
	hr := NewHookRegistry()

	var called bool
	hr.RegisterHook(HookBeforeMessage, func(event *HookEvent) error {
		called = true
		return nil
	})

	if !hr.HasHooks(HookBeforeMessage) {
		t.Error("Expected HasHooks to return true")
	}
	if hr.HookCount(HookBeforeMessage) != 1 {
		t.Errorf("Expected 1 hook, got %d", hr.HookCount(HookBeforeMessage))
	}

	err := hr.FireHook(HookBeforeMessage, &HookEvent{Message: "test"})
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !called {
		t.Error("Hook should have been called")
	}
}

func TestHookRegistry_FireNoHooks(t *testing.T) {
	hr := NewHookRegistry()
	err := hr.FireHook(HookAfterMessage, &HookEvent{})
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}
}

func TestHookRegistry_FireWithError(t *testing.T) {
	hr := NewHookRegistry()

	hr.RegisterHook(HookOnError, func(event *HookEvent) error {
		return errors.New("hook failed")
	})
	hr.RegisterHook(HookOnError, func(event *HookEvent) error {
		return nil // Second hook still runs
	})

	err := hr.FireHook(HookOnError, &HookEvent{})
	if err == nil {
		t.Error("Expected error from failing hook")
	}
	if !strings.Contains(err.Error(), "hook failed") {
		t.Errorf("Expected 'hook failed', got '%s'", err.Error())
	}
}

func TestHookRegistry_Priority(t *testing.T) {
	hr := NewHookRegistry()

	var order []int
	hr.RegisterNamedHook(HookBeforeMessage, "low", func(event *HookEvent) error {
		order = append(order, 3)
		return nil
	}, 30)
	hr.RegisterNamedHook(HookBeforeMessage, "high", func(event *HookEvent) error {
		order = append(order, 1)
		return nil
	}, 10)
	hr.RegisterNamedHook(HookBeforeMessage, "mid", func(event *HookEvent) error {
		order = append(order, 2)
		return nil
	}, 20)

	hr.FireHook(HookBeforeMessage, &HookEvent{})

	if len(order) != 3 {
		t.Fatalf("Expected 3 hooks called, got %d", len(order))
	}
	if order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("Hooks called in wrong order: %v", order)
	}
}

func TestHookRegistry_HasHooks(t *testing.T) {
	hr := NewHookRegistry()
	if hr.HasHooks(HookBeforeMessage) {
		t.Error("Expected no hooks initially")
	}
	hr.RegisterHook(HookBeforeMessage, func(event *HookEvent) error { return nil })
	if !hr.HasHooks(HookBeforeMessage) {
		t.Error("Expected hooks after registration")
	}
	if hr.HasHooks(HookAfterMessage) {
		t.Error("Expected no hooks for different type")
	}
}

func TestHookRegistry_AllHookTypes(t *testing.T) {
	hr := NewHookRegistry()
	hr.RegisterHook(HookBeforeMessage, func(e *HookEvent) error { return nil })
	hr.RegisterHook(HookOnError, func(e *HookEvent) error { return nil })

	types := hr.AllHookTypes()
	if len(types) != 2 {
		t.Errorf("Expected 2 hook types, got %d", len(types))
	}
}

func TestHookRegistry_EventType(t *testing.T) {
	hr := NewHookRegistry()

	var receivedType string
	hr.RegisterHook(HookBeforeMessage, func(event *HookEvent) error {
		receivedType = event.Type
		return nil
	})

	hr.FireHook(HookBeforeMessage, &HookEvent{})
	if receivedType != HookBeforeMessage {
		t.Errorf("Expected event type '%s', got '%s'", HookBeforeMessage, receivedType)
	}
}

// --- PairingStore tests ---

func TestPairingStore_IsUserAllowed_NoRestrictions(t *testing.T) {
	ps := NewPairingStore()
	// No restrictions = deny by default (secure)
	if ps.IsUserAllowed(PlatformDMWork, "anyuser") {
		t.Error("Expected deny-by-default when no restrictions configured")
	}
}

func TestPairingStore_IsUserAllowed_Wildcard(t *testing.T) {
	ps := NewPairingStore()
	ps.LoadAllowedUsers(map[string]any{
		"dmwork": []any{"*"},
	})
	if !ps.IsUserAllowed(PlatformDMWork, "anyuser") {
		t.Error("Expected wildcard to allow any user")
	}
}

func TestPairingStore_IsUserAllowed_ExactMatch(t *testing.T) {
	ps := NewPairingStore()
	ps.LoadAllowedUsers(map[string]any{
		"dmwork": []any{"user123", "user456"},
	})

	if !ps.IsUserAllowed(PlatformDMWork, "user123") {
		t.Error("Expected allowed user to pass")
	}
	if ps.IsUserAllowed(PlatformDMWork, "user999") {
		t.Error("Expected disallowed user to fail")
	}
}

func TestPairingStore_AddAndRemoveUser(t *testing.T) {
	ps := NewPairingStore()
	ps.AddAllowedUser(PlatformDMWork, "user1")

	if !ps.IsUserAllowed(PlatformDMWork, "user1") {
		t.Error("Expected added user to be allowed")
	}

	ps.RemoveAllowedUser(PlatformDMWork, "user1")
	// After removal, if no users remain for the platform, the platform entry
	// still exists (empty map), so it will block all users
	users := ps.ListAllowedUsers(PlatformDMWork)
	if len(users) != 0 {
		t.Errorf("Expected 0 users after removal, got %d", len(users))
	}
}

func TestPairingStore_PairUser(t *testing.T) {
	ps := NewPairingStore()
	code := ps.GeneratePairCode()

	if code == "" {
		t.Fatal("Expected non-empty pairing code")
	}

	err := ps.PairUser(PlatformDMWork, "new_user", code)
	if err != nil {
		t.Fatalf("PairUser failed: %v", err)
	}

	if !ps.IsUserAllowed(PlatformDMWork, "new_user") {
		t.Error("Expected paired user to be allowed")
	}
}

func TestPairingStore_PairUser_InvalidCode(t *testing.T) {
	ps := NewPairingStore()
	err := ps.PairUser(PlatformDMWork, "user1", "invalid_code")
	if err == nil {
		t.Error("Expected error for invalid pairing code")
	}
}

func TestPairingStore_PairUser_PlatformMismatch(t *testing.T) {
	t.Skip("requires multiple platforms")
	ps := NewPairingStore()
	code := ps.GeneratePairCodeForPlatform(PlatformDMWork)

	err := ps.PairUser(PlatformDMWork, "user1", code)
	if err == nil {
		t.Error("Expected error for platform mismatch")
	}
}

func TestPairingStore_ListAllowedUsers(t *testing.T) {
	t.Skip("requires multiple platforms")
	ps := NewPairingStore()
	ps.AddAllowedUser(PlatformDMWork, "user1")
	ps.AddAllowedUser(PlatformDMWork, "user2")

	users := ps.ListAllowedUsers(PlatformDMWork)
	if len(users) != 2 {
		t.Errorf("Expected 2 users, got %d", len(users))
	}

	users = ps.ListAllowedUsers(PlatformDMWork)
	if users != nil {
		t.Errorf("Expected nil for platform with no users, got %v", users)
	}
}

// --- MediaCache tests ---

func TestMediaCache_CacheFromBytes(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	mc := &MediaCache{
		baseDir: filepath.Join(tmpDir, "cache"),
	}
	os.MkdirAll(filepath.Join(mc.baseDir, "images"), 0755)

	path, err := mc.CacheImageFromBytes([]byte("fake image data"), ".png")
	if err != nil {
		t.Fatalf("CacheImageFromBytes failed: %v", err)
	}
	if path == "" {
		t.Error("Expected non-empty path")
	}
	if !strings.HasSuffix(path, ".png") {
		t.Errorf("Expected .png suffix, got '%s'", path)
	}

	// Caching same data again should return same path (content-addressed)
	path2, err := mc.CacheImageFromBytes([]byte("fake image data"), ".png")
	if err != nil {
		t.Fatalf("Second CacheImageFromBytes failed: %v", err)
	}
	if path != path2 {
		t.Error("Expected same path for same content")
	}
}

func TestMediaCache_CacheDir(t *testing.T) {
	mc := &MediaCache{baseDir: "/test/cache"}
	if mc.CacheDir() != "/test/cache" {
		t.Errorf("Expected '/test/cache', got '%s'", mc.CacheDir())
	}
}

func TestMediaCache_CleanupCache(t *testing.T) {
	tmpDir := t.TempDir()
	mc := &MediaCache{baseDir: tmpDir}

	// Create subdirs
	imgDir := filepath.Join(tmpDir, "images")
	os.MkdirAll(imgDir, 0755)

	// Create a file
	os.WriteFile(filepath.Join(imgDir, "old.jpg"), []byte("data"), 0644)

	// Cleanup with 0 hours (removes everything)
	// The file is just created so it won't be older than any cutoff
	removed := mc.CleanupCache(24)
	// Just created file won't be removed
	if removed != 0 {
		t.Errorf("Expected 0 removed (file too new), got %d", removed)
	}
}

func TestGuessExtension(t *testing.T) {
	tests := []struct {
		url        string
		defaultExt string
		expected   string
	}{
		{"https://example.com/image.jpg", ".png", ".jpg"},
		{"https://example.com/file.png?token=abc", ".jpg", ".png"},
		{"https://example.com/file.png#section", ".jpg", ".png"},
		{"https://example.com/noext", ".jpg", ".jpg"},
		{"https://example.com/file.verylongext", ".default", ".default"},
	}

	for _, tt := range tests {
		result := guessExtension(tt.url, tt.defaultExt)
		if result != tt.expected {
			t.Errorf("guessExtension(%q, %q) = %q, want %q", tt.url, tt.defaultExt, result, tt.expected)
		}
	}
}

// --- ChannelDirectory tests ---

func TestChannelDirectory_SetAndGetBinding(t *testing.T) {
	cd := NewChannelDirectory()

	cd.SetBinding("dmwork", "C12345", "customer_support")
	binding := cd.GetBinding("dmwork", "C12345")

	if binding == nil {
		t.Fatal("Expected binding")
	}
	if binding.SkillName != "customer_support" {
		t.Errorf("Expected 'customer_support', got '%s'", binding.SkillName)
	}
}

func TestChannelDirectory_GetBinding_NotFound(t *testing.T) {
	cd := NewChannelDirectory()
	binding := cd.GetBinding("dmwork", "unknown")
	if binding != nil {
		t.Error("Expected nil for unknown binding")
	}
}

func TestChannelDirectory_UpdateBinding(t *testing.T) {
	cd := NewChannelDirectory()

	cd.SetBinding("dmwork", "C12345", "old_skill")
	cd.SetBinding("dmwork", "C12345", "new_skill")

	binding := cd.GetBinding("dmwork", "C12345")
	if binding.SkillName != "new_skill" {
		t.Errorf("Expected 'new_skill', got '%s'", binding.SkillName)
	}
}

func TestChannelDirectory_RemoveBinding(t *testing.T) {
	cd := NewChannelDirectory()

	cd.SetBinding("dmwork", "C12345", "skill")
	removed := cd.RemoveBinding("dmwork", "C12345")
	if !removed {
		t.Error("Expected remove to return true")
	}

	binding := cd.GetBinding("dmwork", "C12345")
	if binding != nil {
		t.Error("Expected nil after removal")
	}

	removed = cd.RemoveBinding("dmwork", "nonexistent")
	if removed {
		t.Error("Expected false for nonexistent binding")
	}
}

func TestChannelDirectory_PlatformFilter(t *testing.T) {
	t.Skip("requires multiple platforms")
	cd := NewChannelDirectory()
	cd.SetBinding("dmwork", "C12345", "slack_skill")

	// Matching platform
	binding := cd.GetBinding("dmwork", "C12345")
	if binding == nil {
		t.Error("Expected binding for matching platform")
	}

	// Wrong platform
	binding = cd.GetBinding("dmwork", "C12345")
	if binding != nil {
		t.Error("Expected nil for wrong platform")
	}
}

func TestChannelDirectory_ListBindings(t *testing.T) {
	cd := NewChannelDirectory()
	cd.SetBinding("dmwork", "C1", "skill1")
	cd.SetBinding("dmwork", "D1", "skill2")

	bindings := cd.ListBindings()
	if len(bindings) != 2 {
		t.Errorf("Expected 2 bindings, got %d", len(bindings))
	}
}

func TestChannelDirectory_LoadFromConfig(t *testing.T) {
	cd := NewChannelDirectory()
	cfg := map[string]any{
		"channel_bindings": []any{
			map[string]any{
				"channel_id": "C100",
				"skill_name": "test_skill",
				"platform":   "dmwork",
			},
			map[string]any{
				"channel_id": "D200",
				"skill_name": "discord_skill",
				"platform":   "dmwork",
			},
		},
	}

	cd.LoadFromConfig(cfg)

	bindings := cd.ListBindings()
	if len(bindings) != 2 {
		t.Errorf("Expected 2 bindings from config, got %d", len(bindings))
	}

	b := cd.GetBinding("dmwork", "C100")
	if b == nil || b.SkillName != "test_skill" {
		t.Error("Expected test_skill binding for C100")
	}
}

// --- StickerCache tests ---

func TestStickerCache_GetSet(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")
	os.MkdirAll(filepath.Join(tmpDir, "cache"), 0755)

	sc := &StickerCache{
		entries: make(map[string]StickerEntry),
		path:    filepath.Join(tmpDir, "cache", "stickers.json"),
	}

	_, ok := sc.Get("sticker-1")
	if ok {
		t.Error("Expected not found for new cache")
	}

	sc.Set(StickerEntry{ID: "sticker-1", Emoji: "😀", SetName: "test-set"})

	entry, ok := sc.Get("sticker-1")
	if !ok {
		t.Error("Expected found after set")
	}
	if entry.Emoji != "😀" {
		t.Errorf("Expected emoji, got '%s'", entry.Emoji)
	}
}

func TestStickerCache_DescribeSticker(t *testing.T) {
	sc := &StickerCache{
		entries: map[string]StickerEntry{
			"s1": {ID: "s1", Emoji: "🎉"},
			"s2": {ID: "s2", SetName: "party"},
		},
		path: "/dev/null",
	}

	desc := sc.DescribeSticker("s1")
	if desc != "[Sticker: 🎉]" {
		t.Errorf("Expected '[Sticker: 🎉]', got '%s'", desc)
	}

	desc = sc.DescribeSticker("s2")
	if desc != "[Sticker from set: party]" {
		t.Errorf("Expected '[Sticker from set: party]', got '%s'", desc)
	}

	desc = sc.DescribeSticker("unknown")
	if desc != "[Sticker]" {
		t.Errorf("Expected '[Sticker]', got '%s'", desc)
	}
}

// --- MessageMirror tests ---

func TestMessageMirror_LoadRules(t *testing.T) {
	mm := NewMessageMirror()
	cfg := map[string]any{
		"mirrors": []any{
			map[string]any{
				"source_platform": "dmwork",
				"source_chat":     "-100123",
				"dest_platform":   "dmwork",
				"dest_chat":       "987654",
			},
			map[string]any{
				"source_platform": "dmwork",
				"source_chat":     "C100",
				"dest_platform":   "dmwork",
				"dest_chat":       "-100456",
				"direction":       "bidirectional",
			},
		},
	}

	mm.LoadRules(cfg)

	rules := mm.Rules()
	if len(rules) != 2 {
		t.Fatalf("Expected 2 rules, got %d", len(rules))
	}
	if rules[0].Direction != MirrorOneWay {
		t.Errorf("Expected one-way, got '%s'", rules[0].Direction)
	}
	if rules[1].Direction != MirrorBidirectional {
		t.Errorf("Expected bidirectional, got '%s'", rules[1].Direction)
	}
}

func TestMessageMirror_ShouldMirror_Forward(t *testing.T) {
	mm := NewMessageMirror()
	mm.LoadRules(map[string]any{
		"mirrors": []any{
			map[string]any{
				"source_platform": "dmwork",
				"source_chat":     "-100123",
				"dest_platform":   "dmwork",
				"dest_chat":       "987654",
			},
		},
	})

	matches := mm.ShouldMirror(SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "-100123",
	})
	if len(matches) != 1 {
		t.Fatalf("Expected 1 match, got %d", len(matches))
	}
	if matches[0].DestPlatform != "dmwork" {
		t.Error("Expected dest platform discord")
	}
}

func TestMessageMirror_ShouldMirror_NoMatch(t *testing.T) {
	mm := NewMessageMirror()
	mm.LoadRules(map[string]any{
		"mirrors": []any{
			map[string]any{
				"source_platform": "dmwork",
				"source_chat":     "-100123",
				"dest_platform":   "dmwork",
				"dest_chat":       "987654",
			},
		},
	})

	matches := mm.ShouldMirror(SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "C100",
	})
	if len(matches) != 0 {
		t.Errorf("Expected 0 matches, got %d", len(matches))
	}
}

func TestMessageMirror_ShouldMirror_Bidirectional(t *testing.T) {
	mm := NewMessageMirror()
	mm.LoadRules(map[string]any{
		"mirrors": []any{
			map[string]any{
				"source_platform": "dmwork",
				"source_chat":     "-100123",
				"dest_platform":   "dmwork",
				"dest_chat":       "987654",
				"direction":       "bidirectional",
			},
		},
	})

	// Forward match
	matches := mm.ShouldMirror(SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "-100123",
	})
	if len(matches) != 1 {
		t.Fatalf("Expected 1 forward match, got %d", len(matches))
	}

	// Reverse match
	matches = mm.ShouldMirror(SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "987654",
	})
	if len(matches) != 1 {
		t.Fatalf("Expected 1 reverse match, got %d", len(matches))
	}
	if matches[0].DestPlatform != "dmwork" {
		t.Errorf("Expected reverse dest platform 'telegram', got '%s'", matches[0].DestPlatform)
	}
}

func TestMatchChat(t *testing.T) {
	if !matchChat("", "anything") {
		t.Error("Empty pattern should match anything")
	}
	if !matchChat("*", "anything") {
		t.Error("Wildcard should match anything")
	}
	if !matchChat("123", "123") {
		t.Error("Exact match should work")
	}
	if matchChat("123", "456") {
		t.Error("Non-matching should fail")
	}
}

// --- SessionSource.Description tests ---

func TestSessionSource_Description(t *testing.T) {
	tests := []struct {
		source   SessionSource
		expected string
	}{
		{SessionSource{Platform: PlatformLocal}, "CLI terminal"},
		{SessionSource{ChatType: "dm", UserName: "john"}, "DM with john"},
		{SessionSource{ChatType: "dm", UserID: "u123"}, "DM with u123"},
		{SessionSource{ChatType: "dm"}, "DM with user"},
		{SessionSource{ChatType: "group", ChatName: "Dev Team"}, "group: Dev Team"},
		{SessionSource{ChatType: "group", ChatID: "G123"}, "group: G123"},
		{SessionSource{ChatType: "channel", ChatName: "#general"}, "channel: #general"},
		{SessionSource{ChatType: "", ChatName: "test"}, "test"},
		{SessionSource{ChatType: "", ChatID: "C123"}, "C123"},
	}

	for _, tt := range tests {
		desc := tt.source.Description()
		if desc != tt.expected {
			t.Errorf("Description() for %+v = %q, want %q", tt.source, desc, tt.expected)
		}
	}
}

func TestSessionSource_ToMap(t *testing.T) {
	src := &SessionSource{
		Platform: PlatformDMWork,
		ChatID:   "123",
		UserID:   "user1",
		ChatType: "group",
	}

	m := src.ToMap()
	if m["platform"] != "dmwork" {
		t.Error("Expected platform in map")
	}
	if m["chat_id"] != "123" {
		t.Error("Expected chat_id in map")
	}
}

// --- RuntimeStatus tests ---

func TestRuntimeStatus_Basic(t *testing.T) {
	rs := NewRuntimeStatus()
	if rs.StartedAt == "" {
		t.Error("Expected non-empty StartedAt")
	}
	if rs.TotalMessages != 0 {
		t.Error("Expected 0 total messages initially")
	}
}

func TestRuntimeStatus_IncrementMessages(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	rs := NewRuntimeStatus()
	rs.IncrementMessageCount("dmwork")
	rs.IncrementMessageCount("dmwork")
	rs.IncrementMessageCount("dmwork")

	if rs.TotalMessages != 3 {
		t.Errorf("Expected 3 total messages, got %d", rs.TotalMessages)
	}

	snap := rs.Snapshot()
	if snap.TotalMessages != 3 {
		t.Errorf("Expected 3 in snapshot, got %d", snap.TotalMessages)
	}
}

func TestRuntimeStatus_WriteStatus(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	rs := NewRuntimeStatus()
	rs.WriteRuntimeStatus("dmwork", "connected", "", "")

	ps := rs.Platforms["dmwork"]
	if ps.State != "connected" {
		t.Errorf("Expected 'connected', got '%s'", ps.State)
	}
	if ps.ConnectedAt == "" {
		t.Error("Expected ConnectedAt to be set")
	}
}

func TestRuntimeStatus_SetActiveSessions(t *testing.T) {
	rs := NewRuntimeStatus()
	rs.SetActiveSessions(5)
	if rs.ActiveSessions != 5 {
		t.Errorf("Expected 5, got %d", rs.ActiveSessions)
	}
}

// --- DefaultGatewayConfig tests ---

func TestDefaultGatewayConfig(t *testing.T) {
	cfg := DefaultGatewayConfig()
	if cfg == nil {
		t.Fatal("Expected non-nil config")
	}
	if !cfg.Settings.GroupSessionsPerUser {
		t.Error("Expected GroupSessionsPerUser to be true by default")
	}
	if cfg.Settings.MaxMessageLength != 4096 {
		t.Errorf("Expected 4096, got %d", cfg.Settings.MaxMessageLength)
	}
}

// --- StringFromMap tests ---

func TestStringFromMap(t *testing.T) {
	m := map[string]any{
		"key": "value",
		"num": 42,
	}

	if stringFromMap(m, "key") != "value" {
		t.Error("Expected 'value'")
	}
	if stringFromMap(m, "num") != "" {
		t.Error("Expected empty string for non-string value")
	}
	if stringFromMap(m, "missing") != "" {
		t.Error("Expected empty string for missing key")
	}
}

// --- Mock adapter for testing ---

type mockAdapter struct {
	platform Platform
}

func (m *mockAdapter) Platform() Platform { return m.platform }
func (m *mockAdapter) Connect(ctx context.Context) error { return nil }
func (m *mockAdapter) Disconnect() error { return nil }
func (m *mockAdapter) Send(ctx context.Context, chatID string, text string, metadata map[string]string) (*SendResult, error) {
	return &SendResult{Success: true}, nil
}
func (m *mockAdapter) SendTyping(ctx context.Context, chatID string) error { return nil }
func (m *mockAdapter) SendImage(ctx context.Context, chatID string, imagePath string, caption string, metadata map[string]string) (*SendResult, error) {
	return &SendResult{Success: true}, nil
}
func (m *mockAdapter) SendVoice(ctx context.Context, chatID string, audioPath string, metadata map[string]string) (*SendResult, error) {
	return &SendResult{Success: true}, nil
}
func (m *mockAdapter) SendDocument(ctx context.Context, chatID string, filePath string, metadata map[string]string) (*SendResult, error) {
	return &SendResult{Success: true}, nil
}
func (m *mockAdapter) OnMessage(handler func(event *MessageEvent)) {}
func (m *mockAdapter) IsConnected() bool { return true }

// --- splitMessage tests ---

func TestSplitMessage_Short(t *testing.T) {
	parts := splitMessage("Hello", 100)
	if len(parts) != 1 {
		t.Errorf("Expected 1 part, got %d", len(parts))
	}
	if parts[0] != "Hello" {
		t.Errorf("Expected 'Hello', got '%s'", parts[0])
	}
}

func TestSplitMessage_ExactMaxLen(t *testing.T) {
	msg := strings.Repeat("a", 100)
	parts := splitMessage(msg, 100)
	if len(parts) != 1 {
		t.Errorf("Expected 1 part for exact length, got %d", len(parts))
	}
}

func TestSplitMessage_NeedsSplt(t *testing.T) {
	msg := strings.Repeat("a", 300)
	parts := splitMessage(msg, 100)
	if len(parts) < 3 {
		t.Errorf("Expected at least 3 parts, got %d", len(parts))
	}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	if total != 300 {
		t.Errorf("Expected total 300 chars, got %d", total)
	}
}

func TestSplitMessage_SplitsAtNewline(t *testing.T) {
	msg := strings.Repeat("a", 50) + "\n" + strings.Repeat("b", 50) + "\n" + strings.Repeat("c", 50)
	parts := splitMessage(msg, 60)
	// Should split at newlines
	if len(parts) < 2 {
		t.Errorf("Expected at least 2 parts, got %d", len(parts))
	}
}

func TestSplitMessage_ZeroMaxLen(t *testing.T) {
	parts := splitMessage("Hello", 0)
	// Should default to 4096
	if len(parts) != 1 {
		t.Errorf("Expected 1 part with default max, got %d", len(parts))
	}
}

// --- GetGatewayKnownCommands tests ---

func TestGetGatewayKnownCommands(t *testing.T) {
	cmds := GetGatewayKnownCommands()
	if len(cmds) == 0 {
		t.Error("Expected non-empty command map")
	}
	expectedCmds := []string{"new", "reset", "help", "status", "model", "retry"}
	for _, c := range expectedCmds {
		if !cmds[c] {
			t.Errorf("Expected command '%s' in known commands", c)
		}
	}
}

// --- GatewayHelpLines tests ---

func TestGatewayHelpLines(t *testing.T) {
	lines := GatewayHelpLines()
	if len(lines) == 0 {
		t.Error("Expected non-empty help lines")
	}
	foundNew := false
	for _, line := range lines {
		if strings.Contains(line, "/new") {
			foundNew = true
		}
	}
	if !foundNew {
		t.Error("Expected '/new' in help lines")
	}
}

// --- HashSenderID tests ---

func TestHashSenderID(t *testing.T) {
	result := HashSenderID("test-user-123")
	if !strings.HasPrefix(result, "user_") {
		t.Errorf("Expected 'user_' prefix, got '%s'", result)
	}
	if len(result) < 10 {
		t.Errorf("Expected longer hash, got '%s'", result)
	}

	// Same input should give same output
	result2 := HashSenderID("test-user-123")
	if result != result2 {
		t.Error("Same input should produce same hash")
	}

	// Different input should give different output
	result3 := HashSenderID("other-user")
	if result == result3 {
		t.Error("Different input should produce different hash")
	}
}

// --- DeliveryRouter with mock adapter tests ---

func TestDeliveryRouter_DeliverResponse_WithAdapter(t *testing.T) {
	dr := NewDeliveryRouter()
	adapter := &mockAdapter{platform: PlatformDMWork}
	dr.RegisterAdapter(adapter)

	err := dr.DeliverResponse(context.Background(), "chat1", "Hello world", SessionSource{Platform: PlatformDMWork})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestDeliveryRouter_DeliverResponse_WithMedia(t *testing.T) {
	dr := NewDeliveryRouter()
	adapter := &mockAdapter{platform: PlatformDMWork}
	dr.RegisterAdapter(adapter)

	content := "Hello\nMEDIA:/path/to/image.png\nWorld"
	err := dr.DeliverResponse(context.Background(), "chat1", content, SessionSource{Platform: PlatformDMWork})
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

// --- SessionSource.ToMap with optional fields ---

func TestSessionSource_ToMap_WithOptionalFields(t *testing.T) {
	src := &SessionSource{
		Platform:  PlatformDMWork,
		ChatID:    "123",
		UserID:    "user1",
		ChatType:  "group",
		ChatTopic: "test-topic",
		UserIDAlt: "alt-user",
		ChatIDAlt: "alt-chat",
	}

	m := src.ToMap()
	if m["chat_topic"] != "test-topic" {
		t.Error("Expected chat_topic in map")
	}
	if m["user_id_alt"] != "alt-user" {
		t.Error("Expected user_id_alt in map")
	}
	if m["chat_id_alt"] != "alt-chat" {
		t.Error("Expected chat_id_alt in map")
	}
}

// --- PlatformMaxMessageLength tests ---

func TestPlatformMaxMessageLength(t *testing.T) {
	t.Skip("requires multiple platforms")
	tests := []struct {
		platform Platform
		expected int
	}{
		{PlatformDMWork, 4096},
		{PlatformDMWork, 2000},
		{PlatformDMWork, 40000},
		{PlatformDMWork, 1600},
		{PlatformDMWork, 2048},
	}

	for _, tt := range tests {
		if PlatformMaxMessageLength[tt.platform] != tt.expected {
			t.Errorf("Platform %s: expected %d, got %d",
				tt.platform, tt.expected, PlatformMaxMessageLength[tt.platform])
		}
	}
}

// --- PairingStore CaseInsensitive tests ---

func TestPairingStore_CaseInsensitiveMatch(t *testing.T) {
	ps := NewPairingStore()
	ps.AddAllowedUser(PlatformDMWork, "UserName123")

	// Should match case-insensitively
	if !ps.IsUserAllowed(PlatformDMWork, "username123") {
		t.Error("Expected case-insensitive match")
	}
	if !ps.IsUserAllowed(PlatformDMWork, "USERNAME123") {
		t.Error("Expected case-insensitive match for uppercase")
	}
}

// --- LoadAllowedUsers string format ---

func TestPairingStore_LoadAllowedUsers_StringFormat(t *testing.T) {
	ps := NewPairingStore()
	ps.LoadAllowedUsers(map[string]any{
		"dmwork": "single_user",
	})

	if !ps.IsUserAllowed(PlatformDMWork, "single_user") {
		t.Error("Expected single string user to be allowed")
	}
}

func TestPairingStore_LoadAllowedUsers_NilConfig(t *testing.T) {
	ps := NewPairingStore()
	ps.LoadAllowedUsers(nil)
	// Should not panic; nil config = no allowed users = deny by default
	if ps.IsUserAllowed(PlatformDMWork, "anyone") {
		t.Error("Expected deny-by-default with nil config")
	}
}

// --- RuntimeStatus Disconnect ---

func TestRuntimeStatus_WriteStatus_Disconnect(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("HERMES_HOME", tmpDir)
	defer os.Unsetenv("HERMES_HOME")

	rs := NewRuntimeStatus()
	rs.WriteRuntimeStatus("dmwork", "connected", "", "")
	rs.WriteRuntimeStatus("dmwork", "disconnected", "TIMEOUT", "Connection timed out")

	ps := rs.Platforms["dmwork"]
	if ps.State != "disconnected" {
		t.Errorf("Expected 'disconnected', got '%s'", ps.State)
	}
	if ps.ConnectedAt != "" {
		t.Error("Expected ConnectedAt to be cleared on disconnect")
	}
	if ps.ErrorCode != "TIMEOUT" {
		t.Errorf("Expected error code 'TIMEOUT', got '%s'", ps.ErrorCode)
	}
}
