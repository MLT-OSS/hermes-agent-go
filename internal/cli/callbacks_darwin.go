//go:build darwin
// +build darwin

package cli

import (
	"bufio"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// macOS uses TIOCGETA/TIOCSETA.
const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
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
