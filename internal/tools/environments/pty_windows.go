//go:build windows
// +build windows

package environments

// PTYEnvironment is not available on Windows.
// It falls back to the local (exec.Command) environment.
type PTYEnvironment struct {
	local *LocalEnvironment
}

func init() {
	RegisterEnvironment("pty", func(params map[string]string) (Environment, error) {
		env := NewPTYEnvironment()
		if dir, ok := params["working_directory"]; ok && dir != "" {
			env.local.workDir = dir
		}
		return env, nil
	})
}

// NewPTYEnvironment creates a fallback environment on Windows.
func NewPTYEnvironment() *PTYEnvironment {
	return &PTYEnvironment{local: NewLocalEnvironment()}
}

// Execute delegates to LocalEnvironment on Windows.
func (e *PTYEnvironment) Execute(command string, timeout int) (string, string, int, error) {
	return e.local.Execute(command, timeout)
}

// IsAvailable returns false on Windows (no PTY support).
func (e *PTYEnvironment) IsAvailable() bool {
	return false
}

// Name returns "pty".
func (e *PTYEnvironment) Name() string {
	return "pty"
}
