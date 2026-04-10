package agent

import (
	"os"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/tools"
)

// builtinMemoryAdapter wraps agent.BuiltinMemoryProvider to satisfy
// tools.MemoryProvider (avoids import cycle).
type builtinMemoryAdapter struct {
	inner *BuiltinMemoryProvider
}

func (a *builtinMemoryAdapter) ReadMemory() (string, error)      { return a.inner.ReadMemory() }
func (a *builtinMemoryAdapter) SaveMemory(k, c string) error     { return a.inner.SaveMemory(k, c) }
func (a *builtinMemoryAdapter) DeleteMemory(k string) error      { return a.inner.DeleteMemory(k) }
func (a *builtinMemoryAdapter) ReadUserProfile() (string, error) { return a.inner.ReadUserProfile() }
func (a *builtinMemoryAdapter) SaveUserProfile(c string) error   { return a.inner.SaveUserProfile(c) }

// honchoMemoryAdapter wraps agent.HonchoProvider to satisfy tools.MemoryProvider.
type honchoMemoryAdapter struct {
	inner *HonchoProvider
}

func (a *honchoMemoryAdapter) ReadMemory() (string, error)      { return a.inner.ReadMemory() }
func (a *honchoMemoryAdapter) SaveMemory(k, c string) error     { return a.inner.SaveMemory(k, c) }
func (a *honchoMemoryAdapter) DeleteMemory(k string) error      { return a.inner.DeleteMemory(k) }
func (a *honchoMemoryAdapter) ReadUserProfile() (string, error) { return a.inner.ReadUserProfile() }
func (a *honchoMemoryAdapter) SaveUserProfile(c string) error   { return a.inner.SaveUserProfile(c) }

func init() {
	// Register builtin provider.
	tools.RegisterMemoryProvider("builtin", func() tools.MemoryProvider {
		return &builtinMemoryAdapter{
			inner: NewBuiltinMemoryProvider(config.HermesHome()),
		}
	})

	// Register honcho provider.
	tools.RegisterMemoryProvider("honcho", func() tools.MemoryProvider {
		appID := os.Getenv("HONCHO_APP_ID")
		userID := os.Getenv("HONCHO_USER_ID")
		if userID == "" {
			userID = "default"
		}
		if appID == "" || os.Getenv("HONCHO_API_KEY") == "" {
			// Fall back to builtin when Honcho is not configured.
			return &builtinMemoryAdapter{
				inner: NewBuiltinMemoryProvider(config.HermesHome()),
			}
		}
		return &honchoMemoryAdapter{
			inner: NewHonchoProvider(appID, userID),
		}
	})

	// Wire config reader for provider name.
	tools.SetMemoryProviderNameFunc(func() string {
		cfg := config.Load()
		return cfg.Memory.Provider
	})
}
