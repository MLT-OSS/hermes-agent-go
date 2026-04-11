package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GetClipboardImage checks if the clipboard contains an image and saves it
// to a temporary file.
// On macOS: uses pngpaste or osascript.
// On Linux: uses xclip.
// Returns the path to the saved image file, or an error if no image is available.
func GetClipboardImage() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return getClipboardImageMacOS()
	case "linux":
		return getClipboardImageLinux()
	default:
		return "", fmt.Errorf("clipboard image support not available on %s", runtime.GOOS)
	}
}

// HasClipboardImage returns true if the clipboard currently contains an image.
func HasClipboardImage() bool {
	switch runtime.GOOS {
	case "darwin":
		return hasClipboardImageMacOS()
	case "linux":
		return hasClipboardImageLinux()
	default:
		return false
	}
}

// --- macOS implementation ---

func getClipboardImageMacOS() (string, error) {
	outputPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("hermes-clipboard-%d.png", time.Now().UnixNano()))

	// Try pngpaste first (brew install pngpaste).
	if pngpastePath, err := exec.LookPath("pngpaste"); err == nil {
		cmd := exec.Command(pngpastePath, outputPath)
		if err := cmd.Run(); err == nil {
			// Verify the file was created and is non-empty.
			if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
				return outputPath, nil
			}
		}
	}

	// Fall back to osascript.
	script := fmt.Sprintf(`
		set theFile to POSIX file "%s"
		try
			set theImage to the clipboard as «class PNGf»
			set fp to open for access theFile with write permission
			write theImage to fp
			close access fp
			return "ok"
		on error
			return "no image"
		end try
	`, outputPath)

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to read clipboard: %w", err)
	}

	result := string(output)
	if result == "no image\n" || result == "no image" {
		return "", fmt.Errorf("no image found in clipboard")
	}

	// Verify the file was created.
	if info, err := os.Stat(outputPath); err == nil && info.Size() > 0 {
		return outputPath, nil
	}

	return "", fmt.Errorf("failed to save clipboard image")
}

func hasClipboardImageMacOS() bool {
	// Use osascript to check clipboard type.
	script := `
		try
			the clipboard as «class PNGf»
			return "yes"
		on error
			return "no"
		end try
	`
	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	result := string(output)
	return result == "yes\n" || result == "yes"
}

// --- Linux implementation ---

func getClipboardImageLinux() (string, error) {
	xclipPath, err := exec.LookPath("xclip")
	if err != nil {
		return "", fmt.Errorf("xclip not found. Install it with: sudo apt install xclip")
	}

	outputPath := filepath.Join(os.TempDir(),
		fmt.Sprintf("hermes-clipboard-%d.png", time.Now().UnixNano()))

	// Check if clipboard has an image.
	cmd := exec.Command(xclipPath, "-selection", "clipboard", "-t", "image/png", "-o")
	imageData, err := cmd.Output()
	if err != nil || len(imageData) == 0 {
		return "", fmt.Errorf("no image found in clipboard")
	}

	if err := os.WriteFile(outputPath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to save clipboard image: %w", err)
	}

	return outputPath, nil
}

func hasClipboardImageLinux() bool {
	xclipPath, err := exec.LookPath("xclip")
	if err != nil {
		return false
	}

	// Check clipboard targets for image types.
	cmd := exec.Command(xclipPath, "-selection", "clipboard", "-t", "TARGETS", "-o")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	targets := string(output)
	return strings.Contains(targets, "image/png") || strings.Contains(targets, "image/jpeg")
}
