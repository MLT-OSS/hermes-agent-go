package cli

import (
	"testing"
	"time"
)

func TestNewAudioRecorder(t *testing.T) {
	t.Setenv("HERMES_HOME", t.TempDir())
	r := NewAudioRecorder()
	if r.outputDir == "" {
		t.Error("outputDir should not be empty")
	}
}

func TestRecordingAvailable(t *testing.T) {
	r := &AudioRecorder{outputDir: t.TempDir()}
	// Just verify it doesn't panic — actual availability depends on system.
	_ = r.RecordingAvailable()
}

func TestFindRecordTool(t *testing.T) {
	// Doesn't panic, returns empty or a tool name.
	_ = findRecordTool()
}

func TestFindPlayTool(t *testing.T) {
	_ = findPlayTool()
}

func TestDefaultVoiceLoopConfig(t *testing.T) {
	cfg := DefaultVoiceLoopConfig()
	if cfg.RecordDuration != 10*time.Second {
		t.Errorf("RecordDuration = %v, want 10s", cfg.RecordDuration)
	}
	if !cfg.AutoPlay {
		t.Error("AutoPlay should default to true")
	}
}

func TestVoiceLoop_NoSTT(t *testing.T) {
	t.Setenv("HERMES_HOME", t.TempDir())

	vm := &VoiceMode{} // empty, no backends
	recorder := &AudioRecorder{outputDir: t.TempDir()}
	cfg := DefaultVoiceLoopConfig()

	_, _, err := VoiceLoop(vm, recorder, cfg, func(text string) (string, error) {
		return "ok", nil
	})

	if err == nil {
		t.Error("expected error when no STT available")
	}
}

func TestPlayAudio_NoPlayer(t *testing.T) {
	// Set PATH to empty to ensure no player found.
	t.Setenv("PATH", "")
	err := PlayAudio("/nonexistent/file.wav")
	if err == nil {
		t.Error("expected error when no player available")
	}
}
