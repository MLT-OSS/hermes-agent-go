//go:build darwin
// +build darwin

package cli

import "syscall"

// macOS uses TIOCGETA/TIOCSETA.
const (
	ioctlGetTermios = syscall.TIOCGETA
	ioctlSetTermios = syscall.TIOCSETA
)
