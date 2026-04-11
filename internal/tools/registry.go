package tools

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// ToolHandler is the function signature for tool handlers.
// It receives parsed arguments and returns a JSON string result.
type ToolHandler func(args map[string]any, ctx *ToolContext) string

// ToolContext provides context to tool handlers.
type ToolContext struct {
	TaskID          string
	SessionID       string
	ToolCallID      string
	Platform        string
	Extra           map[string]any  // Additional context (e.g., delegation depth)
	ApprovalHandler ApprovalHandler // Optional handler for interactive command approval
}

// ToolEntry holds metadata for a registered tool.
type ToolEntry struct {
	Name        string
	Toolset     string
	Schema      map[string]any
	Handler     ToolHandler
	CheckFn     func() bool
	RequiresEnv []string
	IsAsync     bool
	Description string
	Emoji       string
}

// ToolRegistry is the central registry for all tools.
type ToolRegistry struct {
	mu            sync.RWMutex
	tools         map[string]*ToolEntry
	toolsetChecks map[string]func() bool
}

// Global registry singleton
var registry = &ToolRegistry{
	tools:         make(map[string]*ToolEntry),
	toolsetChecks: make(map[string]func() bool),
}

// Registry returns the global tool registry.
func Registry() *ToolRegistry {
	return registry
}

// Register adds a tool to the registry.
func Register(entry *ToolEntry) {
	registry.Register(entry)
}

// Register adds a tool to the registry.
func (r *ToolRegistry) Register(entry *ToolEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.tools[entry.Name]; ok {
		if existing.Toolset != entry.Toolset {
			slog.Warn("Tool name collision", "name", entry.Name,
				"old_toolset", existing.Toolset, "new_toolset", entry.Toolset)
		}
	}

	r.tools[entry.Name] = entry

	if entry.CheckFn != nil {
		if _, ok := r.toolsetChecks[entry.Toolset]; !ok {
			r.toolsetChecks[entry.Toolset] = entry.CheckFn
		}
	}
}

// Deregister removes a tool from the registry.
func (r *ToolRegistry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.tools[name]
	if !ok {
		return
	}

	delete(r.tools, name)

	// Drop toolset check if no other tools remain in the toolset
	hasOthers := false
	for _, e := range r.tools {
		if e.Toolset == entry.Toolset {
			hasOthers = true
			break
		}
	}
	if !hasOthers {
		delete(r.toolsetChecks, entry.Toolset)
	}
}

// GetDefinitions returns OpenAI-format tool schemas for the requested tool names.
func (r *ToolRegistry) GetDefinitions(toolNames map[string]bool, quiet bool) []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []map[string]any
	checkResults := make(map[string]bool)

	// Sort for deterministic output
	names := make([]string, 0, len(toolNames))
	for name := range toolNames {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entry, ok := r.tools[name]
		if !ok {
			continue
		}

		if entry.CheckFn != nil {
			key := entry.Toolset
			if _, checked := checkResults[key]; !checked {
				func() {
					defer func() {
						if r := recover(); r != nil {
							checkResults[key] = false
						}
					}()
					checkResults[key] = entry.CheckFn()
				}()
			}
			if !checkResults[key] {
				if !quiet {
					slog.Debug("Tool unavailable (check failed)", "tool", name)
				}
				continue
			}
		}

		schema := make(map[string]any)
		for k, v := range entry.Schema {
			schema[k] = v
		}
		schema["name"] = entry.Name

		result = append(result, map[string]any{
			"type":     "function",
			"function": schema,
		})
	}

	return result
}

// Dispatch executes a tool handler by name.
func (r *ToolRegistry) Dispatch(name string, args map[string]any, ctx *ToolContext) string {
	r.mu.RLock()
	entry, ok := r.tools[name]
	r.mu.RUnlock()

	if !ok {
		return fmt.Sprintf(`{"error":"Unknown tool: %s"}`, name)
	}

	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("Tool dispatch panic", "tool", name, "error", rec)
		}
	}()

	return entry.Handler(args, ctx)
}

// GetAllToolNames returns sorted list of all registered tool names.
func (r *ToolRegistry) GetAllToolNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// GetToolsetForTool returns the toolset a tool belongs to.
func (r *ToolRegistry) GetToolsetForTool(name string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.tools[name]; ok {
		return entry.Toolset
	}
	return ""
}

// GetEmoji returns the emoji for a tool.
func (r *ToolRegistry) GetEmoji(name, defaultEmoji string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.tools[name]; ok && entry.Emoji != "" {
		return entry.Emoji
	}
	return defaultEmoji
}

// GetToolToToolsetMap returns a map of tool name to toolset name.
func (r *ToolRegistry) GetToolToToolsetMap() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	m := make(map[string]string, len(r.tools))
	for name, entry := range r.tools {
		m[name] = entry.Toolset
	}
	return m
}

// IsToolsetAvailable checks if a toolset's requirements are met.
func (r *ToolRegistry) IsToolsetAvailable(toolset string) bool {
	r.mu.RLock()
	check, ok := r.toolsetChecks[toolset]
	r.mu.RUnlock()

	if !ok {
		return true
	}

	defer func() {
		if rec := recover(); rec != nil {
			slog.Debug("Toolset check raised", "toolset", toolset)
		}
	}()

	return check()
}

// CheckToolsetRequirements returns availability for every toolset.
func (r *ToolRegistry) CheckToolsetRequirements() map[string]bool {
	r.mu.RLock()
	toolsets := make(map[string]bool)
	for _, entry := range r.tools {
		toolsets[entry.Toolset] = false
	}
	r.mu.RUnlock()

	for ts := range toolsets {
		toolsets[ts] = r.IsToolsetAvailable(ts)
	}
	return toolsets
}

// GetAvailableToolsets returns toolset metadata for UI display.
func (r *ToolRegistry) GetAvailableToolsets() map[string]map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]map[string]any)
	for _, entry := range r.tools {
		ts := entry.Toolset
		if _, ok := result[ts]; !ok {
			result[ts] = map[string]any{
				"available":    r.IsToolsetAvailable(ts),
				"tools":        []string{},
				"requirements": []string{},
			}
		}
		tools := result[ts]["tools"].([]string)
		result[ts]["tools"] = append(tools, entry.Name)
		if len(entry.RequiresEnv) > 0 {
			reqs := result[ts]["requirements"].([]string)
			for _, env := range entry.RequiresEnv {
				found := false
				for _, r := range reqs {
					if r == env {
						found = true
						break
					}
				}
				if !found {
					reqs = append(reqs, env)
				}
			}
			result[ts]["requirements"] = reqs
		}
	}
	return result
}

// GetSchema returns a tool's raw schema dict.
func (r *ToolRegistry) GetSchema(name string) map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if entry, ok := r.tools[name]; ok {
		return entry.Schema
	}
	return nil
}

// HasTool returns true if a tool is registered.
func (r *ToolRegistry) HasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

// ToolCount returns the number of registered tools.
func (r *ToolRegistry) ToolCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}
