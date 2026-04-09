//go:build !darwin && !windows
// +build !darwin,!windows

package cli

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Linux and other Unix systems use TCGETS/TCSETS.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)

// readPassword reads a line from stdin with echo disabled.
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())

	oldState, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return []byte(scanner.Text()), nil
		}
		return nil, fmt.Errorf("no input")
	}

	newState := *oldState
	newState.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &newState); err != nil {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return []byte(scanner.Text()), nil
		}
		return nil, fmt.Errorf("no input")
	}

	defer unix.IoctlSetTermios(fd, ioctlSetTermios, oldState)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return []byte(scanner.Text()), nil
	}
	return nil, fmt.Errorf("no input")
}
