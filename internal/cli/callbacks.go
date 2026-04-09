package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// CLICallbacks provides interactive callback functions for the CLI interface.
type CLICallbacks struct {
	scanner *bufio.Scanner
	isTTY   bool
}

// NewCLICallbacks creates a new CLICallbacks instance.
func NewCLICallbacks() *CLICallbacks {
	return &CLICallbacks{
		scanner: bufio.NewScanner(os.Stdin),
		isTTY:   isTerminal(),
	}
}

// SudoPasswordCallback prompts for a sudo password securely (masked input).
func (c *CLICallbacks) SudoPasswordCallback() string {
	if !c.isTTY {
		return ""
	}

	fmt.Fprint(os.Stderr, "[sudo] password: ")

	password, err := readPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr)
		return ""
	}

	fmt.Fprintln(os.Stderr)
	return string(password)
}

// ApprovalCallback shows a dangerous command and asks the user for approval.
func (c *CLICallbacks) ApprovalCallback(command, reason string) (approved bool, scope string) {
	if !c.isTTY {
		return false, ""
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  WARNING: Dangerous command detected\n")
	fmt.Fprintf(os.Stderr, "  Reason:  %s\n", reason)
	fmt.Fprintf(os.Stderr, "  Command: %s\n", command)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  [y] Approve once  [a] Approve all for session  [n] Deny\n")
	fmt.Fprint(os.Stderr, "  Choice: ")

	if !c.scanner.Scan() {
		return false, ""
	}

	input := strings.TrimSpace(strings.ToLower(c.scanner.Text()))

	switch input {
	case "y", "yes":
		return true, "once"
	case "a", "all":
		return true, "session"
	default:
		return false, ""
	}
}

// ClarifyCallback presents a question with choices to the user.
func (c *CLICallbacks) ClarifyCallback(question string, choices []string) string {
	if !c.isTTY {
		return ""
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  %s\n", question)

	if len(choices) > 0 {
		for i, choice := range choices {
			fmt.Fprintf(os.Stderr, "    [%d] %s\n", i+1, choice)
		}
		fmt.Fprint(os.Stderr, "  Enter choice (number or text): ")
	} else {
		fmt.Fprint(os.Stderr, "  > ")
	}

	if !c.scanner.Scan() {
		return ""
	}

	input := strings.TrimSpace(c.scanner.Text())

	if len(choices) > 0 {
		if idx, err := strconv.Atoi(input); err == nil && idx >= 1 && idx <= len(choices) {
			return choices[idx-1]
		}
	}

	return input
}

// SecretCallback prompts for a secret value with masked input.
func (c *CLICallbacks) SecretCallback(prompt string) string {
	if !c.isTTY {
		return ""
	}

	fmt.Fprintf(os.Stderr, "  %s: ", prompt)

	secret, err := readPassword()
	if err != nil {
		fmt.Fprintln(os.Stderr)
		return ""
	}

	fmt.Fprintln(os.Stderr)
	return string(secret)
}

// readPassword reads a line from stdin with echo disabled.
// Uses TCGETS/TCSETS which work on both Linux and macOS.
func readPassword() ([]byte, error) {
	fd := int(os.Stdin.Fd())

	// Try to get current terminal state.
	oldState, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		// Not a terminal; fall back to regular reading.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return []byte(scanner.Text()), nil
		}
		return nil, fmt.Errorf("no input")
	}

	// Disable echo.
	newState := *oldState
	newState.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &newState); err != nil {
		// Fallback to plain read on error.
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			return []byte(scanner.Text()), nil
		}
		return nil, fmt.Errorf("no input")
	}

	// Restore terminal state when done.
	defer unix.IoctlSetTermios(fd, ioctlSetTermios, oldState)

	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return []byte(scanner.Text()), nil
	}
	return nil, fmt.Errorf("no input")
}
