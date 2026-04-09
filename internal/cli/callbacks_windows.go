//go:build windows
// +build windows

package cli

import (
	"bufio"
	"fmt"
	"os"
)

// Windows stubs for terminal ioctl constants (unused on Windows).
const (
	ioctlGetTermios = 0
	ioctlSetTermios = 0
)

// readPassword reads a line from stdin.
// On Windows, echo disabling is not implemented; falls back to plain read.
func readPassword() ([]byte, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return []byte(scanner.Text()), nil
	}
	return nil, fmt.Errorf("no input")
}
