package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/config"
)

// AudioRecorder handles microphone recording via external tools.
// Uses sox (rec) on macOS/Linux, or ffmpeg as fallback.
type AudioRecorder struct {
	outputDir string
}

// NewAudioRecorder creates a recorder that writes to the cache dir.
func NewAudioRecorder() *AudioRecorder {
	dir := filepath.Join(config.HermesHome(), "cache", "audio")
	os.MkdirAll(dir, 0755) //nolint:errcheck
	return &AudioRecorder{outputDir: dir}
}

// RecordingAvailable checks if any recording tool is installed.
func (r *AudioRecorder) RecordingAvailable() bool {
	return findRecordTool() != ""
}

// Record captures audio for the given duration and returns the WAV file path.
func (r *AudioRecorder) Record(duration time.Duration) (string, error) {
	tool := findRecordTool()
	if tool == "" {
		return "", fmt.Errorf("no recording tool found; install sox (rec) or ffmpeg")
	}

	outPath := filepath.Join(r.outputDir, fmt.Sprintf("recording_%d.wav", time.Now().UnixMilli()))

	var cmd *exec.Cmd
	switch tool {
	case "rec":
		// sox's rec command: rec output.wav trim 0 <seconds>
		cmd = exec.Command("rec", outPath, "trim", "0", fmt.Sprintf("%.0f", duration.Seconds()))
	case "arecord":
		// ALSA arecord (Linux)
		cmd = exec.Command("arecord", "-f", "S16_LE", "-r", "16000", "-c", "1",
			"-d", fmt.Sprintf("%.0f", duration.Seconds()), outPath)
	case "ffmpeg":
		// ffmpeg with default input device
		inputDevice := "default"
		inputFormat := "alsa"
		if runtime.GOOS == "darwin" {
			inputFormat = "avfoundation"
			inputDevice = ":0" // default mic on macOS
		}
		cmd = exec.Command("ffmpeg", "-y", "-f", inputFormat, "-i", inputDevice,
			"-t", fmt.Sprintf("%.0f", duration.Seconds()),
			"-ar", "16000", "-ac", "1", outPath)
	default:
		return "", fmt.Errorf("unsupported recording tool: %s", tool)
	}

	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("recording failed: %w", err)
	}

	// Verify file was created and has content.
	info, err := os.Stat(outPath)
	if err != nil || info.Size() == 0 {
		return "", fmt.Errorf("recording produced empty file")
	}

	return outPath, nil
}

// PlayAudio plays an audio file using available system tools.
func PlayAudio(audioPath string) error {
	player := findPlayTool()
	if player == "" {
		return fmt.Errorf("no audio player found; install ffplay, aplay, or afplay")
	}

	var cmd *exec.Cmd
	switch player {
	case "afplay":
		cmd = exec.Command("afplay", audioPath)
	case "aplay":
		cmd = exec.Command("aplay", audioPath)
	case "ffplay":
		cmd = exec.Command("ffplay", "-nodisp", "-autoexit", audioPath)
	default:
		cmd = exec.Command(player, audioPath)
	}

	return cmd.Run()
}

// findRecordTool returns the first available recording tool.
func findRecordTool() string {
	candidates := []string{"rec", "arecord", "ffmpeg"}
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// findPlayTool returns the first available audio playback tool.
func findPlayTool() string {
	var candidates []string
	if runtime.GOOS == "darwin" {
		candidates = []string{"afplay", "ffplay"}
	} else {
		candidates = []string{"aplay", "ffplay", "paplay"}
	}
	for _, name := range candidates {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// VoiceLoopConfig configures the voice interaction loop.
type VoiceLoopConfig struct {
	RecordDuration time.Duration // how long to record per turn
	AutoPlay       bool          // auto-play TTS responses
}

// DefaultVoiceLoopConfig returns sensible defaults.
func DefaultVoiceLoopConfig() VoiceLoopConfig {
	return VoiceLoopConfig{
		RecordDuration: 10 * time.Second,
		AutoPlay:       true,
	}
}

// VoiceLoop runs one cycle of: record → STT → process → TTS → play.
// Returns the transcribed text, response text, and any error.
func VoiceLoop(vm *VoiceMode, recorder *AudioRecorder, cfg VoiceLoopConfig,
	processFunc func(text string) (string, error)) (transcribed string, response string, err error) {

	if !vm.STTAvailable() {
		return "", "", fmt.Errorf("no STT backend available; install whisper or set OPENAI_API_KEY")
	}

	// 1. Record.
	audioPath, err := recorder.Record(cfg.RecordDuration)
	if err != nil {
		return "", "", fmt.Errorf("record: %w", err)
	}
	defer os.Remove(audioPath)

	// 2. Transcribe.
	transcribed, err = vm.TranscribeAudio(audioPath)
	if err != nil {
		return "", "", fmt.Errorf("transcribe: %w", err)
	}
	transcribed = strings.TrimSpace(transcribed)
	if transcribed == "" {
		return "", "", fmt.Errorf("no speech detected")
	}

	// 3. Process (send to agent).
	response, err = processFunc(transcribed)
	if err != nil {
		return transcribed, "", fmt.Errorf("process: %w", err)
	}

	// 4. TTS + playback.
	if cfg.AutoPlay && vm.TTSAvailable() && response != "" {
		ttsPath, ttsErr := vm.SpeakText(response)
		if ttsErr == nil && ttsPath != "" {
			PlayAudio(ttsPath) //nolint:errcheck
			os.Remove(ttsPath) //nolint:errcheck
		}
	}

	return transcribed, response, nil
}
