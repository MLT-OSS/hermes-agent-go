package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

func TestRunProfileCreate(t *testing.T) {
	// Use a temp dir as HERMES_HOME to isolate tests.
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	tests := []struct {
		name    string
		profile string
		wantErr bool
	}{
		{"valid name", "test-dev", false},
		{"empty name", "", true},
		{"reserved default", "default", true},
		{"invalid chars", "bad/name", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunProfileCreate(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Errorf("RunProfileCreate(%q) error = %v, wantErr %v", tt.profile, err, tt.wantErr)
			}
			if !tt.wantErr {
				home := config.GetProfileHome(tt.profile)
				if _, err := os.Stat(home); os.IsNotExist(err) {
					t.Errorf("profile dir %s should exist after create", home)
				}
			}
		})
	}
}

func TestRunProfileCreate_Duplicate(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	if err := RunProfileCreate("dup"); err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	if err := RunProfileCreate("dup"); err == nil {
		t.Error("expected error for duplicate profile")
	}
}

func TestRunProfileDelete(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	tests := []struct {
		name    string
		profile string
		setup   bool // create before delete
		wantErr bool
	}{
		{"existing profile", "to-delete", true, false},
		{"nonexistent", "ghost", false, true},
		{"reserved default", "default", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup {
				if err := config.CreateProfile(tt.profile); err != nil {
					t.Fatalf("setup create: %v", err)
				}
			}
			err := RunProfileDelete(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Errorf("RunProfileDelete(%q) error = %v, wantErr %v", tt.profile, err, tt.wantErr)
			}
			if !tt.wantErr {
				home := config.GetProfileHome(tt.profile)
				if _, err := os.Stat(home); !os.IsNotExist(err) {
					t.Errorf("profile dir %s should not exist after delete", home)
				}
			}
		})
	}
}

func TestRunProfileSwitch(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	// Create a profile to switch to.
	if err := config.CreateProfile("work"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	tests := []struct {
		name    string
		profile string
		wantErr bool
	}{
		{"switch to existing", "work", false},
		{"switch to default", "default", false},
		{"switch to empty", "", false},
		{"switch to nonexistent", "nope", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunProfileSwitch(tt.profile)
			if (err != nil) != tt.wantErr {
				t.Errorf("RunProfileSwitch(%q) error = %v, wantErr %v", tt.profile, err, tt.wantErr)
			}
		})
	}
}

func TestRunProfileList(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	// Create profiles dir structure for default.
	os.MkdirAll(filepath.Join(tmpDir, "sessions"), 0755)

	// Should not panic with no profiles.
	RunProfileList()

	// Create a profile and list again.
	if err := config.CreateProfile("myprofile"); err != nil {
		t.Fatalf("create: %v", err)
	}
	RunProfileList()
}

func TestRunProfileShow(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HERMES_HOME", tmpDir)

	// Should not panic.
	RunProfileShow()
}
