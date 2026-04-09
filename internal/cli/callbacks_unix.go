//go:build !darwin
// +build !darwin

package cli

import "golang.org/x/sys/unix"

// Linux and other Unix systems use TCGETS/TCSETS.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
