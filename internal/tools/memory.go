package tools

import (
	"fmt"
	"sync"
)

// MemoryProvider is the interface for memory storage backends.
// This is defined in the tools package to avoid import cycles with agent.
type MemoryProvider interface {
	ReadMemory() (string, error)
	SaveMemory(key, content string) error
	DeleteMemory(key string) error
	ReadUserProfile() (string, error)
	SaveUserProfile(content string) error
}

// memoryProviderRegistry holds registered memory provider factories.
var (
	memoryProvidersMu sync.RWMutex
	memoryProviders   = map[string]func() MemoryProvider{}
	activeProvider    MemoryProvider
	activeProviderMu  sync.Mutex
	providerInited    bool
)

// RegisterMemoryProvider registers a named memory provider factory.
// Call this in init() of provider packages to make backends available.
func RegisterMemoryProvider(name string, factory func() MemoryProvider) {
	memoryProvidersMu.Lock()
	defer memoryProvidersMu.Unlock()
	memoryProviders[name] = factory
}

// getMemoryProvider returns the active memory provider, initializing it
// from config on first access. Falls back to the "builtin" provider.
func getMemoryProvider() MemoryProvider {
	activeProviderMu.Lock()
	defer activeProviderMu.Unlock()

	if providerInited {
		return activeProvider
	}
	providerInited = true

	// Determine provider name from config.
	providerName := getMemoryProviderName()
	if providerName == "" {
		providerName = "builtin"
	}

	memoryProvidersMu.RLock()
	factory, ok := memoryProviders[providerName]
	memoryProvidersMu.RUnlock()

	if ok {
		activeProvider = factory()
	} else {
		// Try builtin fallback.
		memoryProvidersMu.RLock()
		builtinFactory, hasBuiltin := memoryProviders["builtin"]
		memoryProvidersMu.RUnlock()
		if hasBuiltin {
			activeProvider = builtinFactory()
		}
	}

	return activeProvider
}

// getMemoryProviderName reads the memory provider name from config.
// Overridden by agent package init via SetMemoryProviderNameFunc.
var getMemoryProviderName = func() string {
	return "" // default; overridden by agent package init
}

// SetMemoryProviderNameFunc allows the agent package to inject the config
// reader without creating an import cycle.
func SetMemoryProviderNameFunc(fn func() string) {
	getMemoryProviderName = fn
}

func init() {
	Register(&ToolEntry{
		Name:    "memory",
		Toolset: "memory",
		Schema: map[string]any{
			"name":        "memory",
			"description": "Manage persistent memory across sessions. Read, save, or update memory notes and user profile.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Action to perform",
						"enum":        []string{"read", "save", "delete", "read_user", "save_user"},
					},
					"key": map[string]any{
						"type":        "string",
						"description": "Memory key/title for save or delete",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Content to save",
					},
				},
				"required": []string{"action"},
			},
		},
		Handler: handleMemory,
		Emoji:   "🧠",
	})
}

func handleMemory(args map[string]any, ctx *ToolContext) string {
	action, _ := args["action"].(string)
	key, _ := args["key"].(string)
	content, _ := args["content"].(string)

	p := getMemoryProvider()
	if p == nil {
		return `{"error":"no memory provider configured"}`
	}

	switch action {
	case "read":
		data, err := p.ReadMemory()
		if err != nil {
			return toJSON(map[string]any{"error": fmt.Sprintf("read memory: %v", err)})
		}
		if data == "" {
			return toJSON(map[string]any{
				"content": "",
				"message": "No memory found. Use save to create one.",
			})
		}
		return toJSON(map[string]any{"content": data})

	case "save":
		if key == "" || content == "" {
			return `{"error":"both key and content are required for save"}`
		}
		if err := p.SaveMemory(key, content); err != nil {
			return toJSON(map[string]any{"error": fmt.Sprintf("save memory: %v", err)})
		}
		return toJSON(map[string]any{
			"success": true,
			"key":     key,
			"message": "Memory saved successfully",
		})

	case "delete":
		if key == "" {
			return `{"error":"key is required for delete"}`
		}
		if err := p.DeleteMemory(key); err != nil {
			return toJSON(map[string]any{"error": fmt.Sprintf("delete memory: %v", err)})
		}
		return toJSON(map[string]any{
			"success": true,
			"key":     key,
			"message": "Memory deleted",
		})

	case "read_user":
		data, err := p.ReadUserProfile()
		if err != nil {
			return toJSON(map[string]any{"error": fmt.Sprintf("read user profile: %v", err)})
		}
		if data == "" {
			return toJSON(map[string]any{
				"content": "",
				"message": "No user profile found.",
			})
		}
		return toJSON(map[string]any{"content": data})

	case "save_user":
		if content == "" {
			return `{"error":"content is required"}`
		}
		if err := p.SaveUserProfile(content); err != nil {
			return toJSON(map[string]any{"error": fmt.Sprintf("save user profile: %v", err)})
		}
		return toJSON(map[string]any{
			"success": true,
			"message": "User profile saved",
		})

	default:
		return `{"error":"invalid action. use: read, save, delete, read_user, save_user"}`
	}
}
