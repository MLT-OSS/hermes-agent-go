package cli

import "testing"

func TestRunThemeList(t *testing.T) {
	// Should not panic even with no custom skins.
	RunThemeList()
}

func TestRunThemeShow(t *testing.T) {
	RunThemeShow()
}

func TestRunThemeSwitch(t *testing.T) {
	tests := []struct {
		name    string
		theme   string
		wantErr bool
	}{
		{"default theme", "default", false},
		{"nonexistent theme", "totally-fake-theme-xyz", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RunThemeSwitch(tt.theme)
			if (err != nil) != tt.wantErr {
				t.Errorf("RunThemeSwitch(%q) error = %v, wantErr %v", tt.theme, err, tt.wantErr)
			}
		})
	}
}
