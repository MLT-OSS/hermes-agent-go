package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ProcessInfo tracks a running background process.
type ProcessInfo struct {
	ID        string
	Command   string
	StartTime time.Time
	Cmd       *exec.Cmd
	Output    bytes.Buffer
	Done      bool
	ExitCode  int
}

var (
	processRegistry = make(map[string]*ProcessInfo)
	processMu       sync.RWMutex
	processCounter  int
)

func init() {
	Register(&ToolEntry{
		Name:    "terminal",
		Toolset: "terminal",
		Schema: map[string]any{
			"name":        "terminal",
			"description": "Execute a shell command in the terminal. Returns stdout, stderr, and exit code. Use background=true for long-running commands.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The shell command to execute",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout in seconds (default: 120, max: 600)",
						"default":     120,
					},
					"background": map[string]any{
						"type":        "boolean",
						"description": "Run in background and return process ID",
						"default":     false,
					},
					"working_directory": map[string]any{
						"type":        "string",
						"description": "Working directory for the command",
					},
				},
				"required": []string{"command"},
			},
		},
		Handler: handleTerminal,
		Emoji:   "💻",
	})

	Register(&ToolEntry{
		Name:    "process",
		Toolset: "terminal",
		Schema: map[string]any{
			"name":        "process",
			"description": "Manage background processes: check status, get output, or stop a process.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "Action to perform",
						"enum":        []string{"status", "output", "stop", "list"},
					},
					"process_id": map[string]any{
						"type":        "string",
						"description": "Process ID (required for status, output, stop)",
					},
				},
				"required": []string{"action"},
			},
		},
		Handler: handleProcess,
		Emoji:   "⚙️",
	})
}

func handleTerminal(args map[string]any, ctx *ToolContext) string {
	command, _ := args["command"].(string)
	if command == "" {
		return `{"error":"command is required"}`
	}

	timeout := 120
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = int(t)
	}
	if timeout > 600 {
		timeout = 600
	}

	background, _ := args["background"].(bool)
	workDir, _ := args["working_directory"].(string)

	// TERMINAL_CWD support: use env var as default working directory
	if workDir == "" {
		if envCWD := os.Getenv("TERMINAL_CWD"); envCWD != "" {
			workDir = envCWD
		}
	}

	// Sudo handling: detect sudo commands and warn
	if isSudoCommand(command) {
		if ctx != nil && ctx.Platform == "cli" {
			slog.Warn("Sudo command detected", "command", command)
		}
		return toJSON(map[string]any{
			"error":   "sudo_required",
			"command": command,
			"message": "This command requires sudo privileges. Please confirm you want to run this command with elevated permissions.",
			"hint":    "The agent cannot enter interactive sudo passwords. Consider running the command manually, or use NOPASSWD in sudoers for non-interactive execution.",
		})
	}

	// Dangerous command check: detect destructive or dangerous commands.
	if isDangerous, reason := IsDangerousCommand(command); isDangerous {
		slog.Warn("Dangerous command blocked", "command", command, "reason", reason)
		return toJSON(map[string]any{
			"error":   "dangerous_command",
			"command": command,
			"reason":  reason,
			"message": "This command has been flagged as potentially dangerous and was blocked. Please review and confirm if you really want to execute this.",
		})
	}

	// Disk usage warning: check before large write operations
	if isLargeWriteOperation(command) {
		warning := checkDiskUsage(workDir)
		if warning != "" {
			slog.Warn("Disk usage warning", "warning", warning)
		}
	}

	if background {
		return startBackground(command, workDir)
	}

	result := executeCommand(command, workDir, timeout)

	// Track working directory changes: if the command is a cd, update TERMINAL_CWD
	updateTerminalCWD(command, workDir)

	return result
}

// isSudoCommand detects if a command starts with sudo or uses privilege escalation.
func isSudoCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if strings.HasPrefix(trimmed, "sudo ") {
		return true
	}
	// Detect sudo in pipes: ... | sudo ...
	parts := strings.Split(trimmed, "|")
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "sudo ") {
			return true
		}
	}
	// Detect sudo in && chains
	parts = strings.Split(trimmed, "&&")
	for _, part := range parts {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "sudo ") {
			return true
		}
	}
	return false
}

// isLargeWriteOperation detects commands that might write large amounts of data.
func isLargeWriteOperation(command string) bool {
	largeOps := []string{
		"dd ", "tar ", "cp -r", "rsync ", "docker pull", "docker build",
		"npm install", "yarn install", "pip install", "cargo build",
		"go build", "make ", "git clone",
	}
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, op := range largeOps {
		if strings.HasPrefix(lower, op) || strings.Contains(lower, " "+op) {
			return true
		}
	}
	return false
}

// checkDiskUsage checks available disk space and returns a warning if low.
func checkDiskUsage(workDir string) string {
	dir := workDir
	if dir == "" {
		dir = "/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "df", "-h", dir)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return ""
	}

	// Parse the capacity percentage from df output
	fields := strings.Fields(lines[1])
	if len(fields) < 5 {
		return ""
	}

	capacityStr := strings.TrimSuffix(fields[4], "%")
	var capacity int
	fmt.Sscanf(capacityStr, "%d", &capacity)

	if capacity > 90 {
		return fmt.Sprintf("WARNING: Disk usage at %d%% for %s. Only %s available. Consider freeing space before large operations.",
			capacity, dir, fields[3])
	}
	return ""
}

// updateTerminalCWD tracks directory changes from cd commands.
func updateTerminalCWD(command, workDir string) {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "cd ") {
		return
	}

	// Extract target directory
	target := strings.TrimSpace(trimmed[3:])
	if target == "" {
		return
	}

	// Resolve relative paths
	if !filepath.IsAbs(target) {
		base := workDir
		if base == "" {
			base, _ = os.Getwd()
		}
		target = filepath.Join(base, target)
	}

	// Expand ~
	if strings.HasPrefix(target, "~/") {
		home, _ := os.UserHomeDir()
		target = filepath.Join(home, target[2:])
	}

	if info, err := os.Stat(target); err == nil && info.IsDir() {
		os.Setenv("TERMINAL_CWD", target)
		slog.Debug("Updated TERMINAL_CWD", "path", target)
	}
}

func executeCommand(command, workDir string, timeout int) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	} else {
		cwd, _ := os.Getwd()
		cmd.Dir = cwd
	}

	// Set environment
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	duration := time.Since(startTime)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			return toJSON(map[string]any{
				"error":     "Command timed out",
				"timeout":   timeout,
				"stdout":    truncateOutput(stdout.String(), 50000),
				"stderr":    truncateOutput(stderr.String(), 10000),
				"exit_code": -1,
			})
		}
	}

	result := map[string]any{
		"stdout":      truncateOutput(stdout.String(), 50000),
		"stderr":      truncateOutput(stderr.String(), 10000),
		"exit_code":   exitCode,
		"duration_ms": duration.Milliseconds(),
	}

	return toJSON(result)
}

func startBackground(command, workDir string) string {
	processMu.Lock()
	processCounter++
	id := fmt.Sprintf("bg_%d", processCounter)
	processMu.Unlock()

	cmd := exec.Command("sh", "-c", command)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	proc := &ProcessInfo{
		ID:        id,
		Command:   command,
		StartTime: time.Now(),
		Cmd:       cmd,
	}

	cmd.Stdout = &proc.Output
	cmd.Stderr = &proc.Output

	if err := cmd.Start(); err != nil {
		return toJSON(map[string]any{
			"error": fmt.Sprintf("Failed to start: %v", err),
		})
	}

	processMu.Lock()
	processRegistry[id] = proc
	processMu.Unlock()

	// Monitor in background
	go func() {
		err := cmd.Wait()
		proc.Done = true
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				proc.ExitCode = exitErr.ExitCode()
			}
		}
		slog.Debug("Background process completed", "id", id, "exit_code", proc.ExitCode)
	}()

	return toJSON(map[string]any{
		"process_id": id,
		"command":    command,
		"status":     "running",
		"message":    fmt.Sprintf("Process started with ID %s", id),
	})
}

func handleProcess(args map[string]any, ctx *ToolContext) string {
	action, _ := args["action"].(string)
	processID, _ := args["process_id"].(string)

	switch action {
	case "list":
		return listProcesses()
	case "status":
		return processStatus(processID)
	case "output":
		return processOutput(processID)
	case "stop":
		return stopProcess(processID)
	default:
		return `{"error":"Invalid action. Use: status, output, stop, list"}`
	}
}

func listProcesses() string {
	processMu.RLock()
	defer processMu.RUnlock()

	var procs []map[string]any
	for _, p := range processRegistry {
		status := "running"
		if p.Done {
			status = "completed"
		}
		procs = append(procs, map[string]any{
			"id":       p.ID,
			"command":  truncateOutput(p.Command, 100),
			"status":   status,
			"duration": time.Since(p.StartTime).String(),
		})
	}

	return toJSON(map[string]any{"processes": procs})
}

func processStatus(id string) string {
	processMu.RLock()
	p, ok := processRegistry[id]
	processMu.RUnlock()

	if !ok {
		return fmt.Sprintf(`{"error":"Process %s not found"}`, id)
	}

	status := "running"
	if p.Done {
		status = "completed"
	}

	return toJSON(map[string]any{
		"id":        p.ID,
		"status":    status,
		"exit_code": p.ExitCode,
		"duration":  time.Since(p.StartTime).String(),
	})
}

func processOutput(id string) string {
	processMu.RLock()
	p, ok := processRegistry[id]
	processMu.RUnlock()

	if !ok {
		return fmt.Sprintf(`{"error":"Process %s not found"}`, id)
	}

	return toJSON(map[string]any{
		"id":     p.ID,
		"output": truncateOutput(p.Output.String(), 50000),
		"done":   p.Done,
	})
}

func stopProcess(id string) string {
	processMu.RLock()
	p, ok := processRegistry[id]
	processMu.RUnlock()

	if !ok {
		return fmt.Sprintf(`{"error":"Process %s not found"}`, id)
	}

	if p.Done {
		return toJSON(map[string]any{
			"id":      p.ID,
			"status":  "already_completed",
			"message": "Process already finished",
		})
	}

	if p.Cmd.Process != nil {
		p.Cmd.Process.Kill()
	}

	return toJSON(map[string]any{
		"id":      p.ID,
		"status":  "stopped",
		"message": "Process terminated",
	})
}

func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("\n... (truncated, %d chars total)", len(s))
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// fileExists checks if a file or directory exists
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// absPath returns absolute path, expanding ~ if needed
func absPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}
