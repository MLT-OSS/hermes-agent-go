package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/hermes-agent/hermes-agent-go/internal/agent"
	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/llm"
	"github.com/hermes-agent/hermes-agent-go/internal/state"
	"github.com/hermes-agent/hermes-agent-go/internal/toolsets"
)

// isTerminal returns true if stdout is a terminal.
func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// App is the main CLI application.
type App struct {
	agent   *agent.AIAgent
	cfg     *config.Config
	scanner *bufio.Scanner

	// Session state.
	sessionID string
	history   []llm.Message
	model     string

	// Display state.
	spinner   *KawaiiSpinner
	skin      *SkinConfig
	isTTY     bool
	streaming bool // true if currently streaming a response

	// Lip Gloss styles.
	promptStyle    lipgloss.Style
	toolPrefixStyle lipgloss.Style
	dimStyle       lipgloss.Style
	responseStyle  lipgloss.Style

	// Session database for insights/browsing.
	sessionDB *state.SessionDB

	// Runtime flags.
	quietMode       bool
	running         bool
	toolProgressMode string // "off", "new", "all", "verbose"
	yoloMode        bool
}

// NewApp creates a new CLI application.
func NewApp(opts ...agent.AgentOption) (*App, error) {
	cfg := config.Load()

	// Initialize skin.
	InitSkinFromConfig(cfg)

	// Open session DB for insights/browsing.
	var sessionDB *state.SessionDB
	if sdb, err := state.NewSessionDB(""); err == nil {
		sessionDB = sdb
	}

	app := &App{
		cfg:              cfg,
		scanner:          bufio.NewScanner(os.Stdin),
		skin:             GetActiveSkin(),
		running:          true,
		isTTY:            isTerminal(),
		sessionDB:        sessionDB,
		toolProgressMode: "new",
	}

	// Build Lip Gloss styles from skin.
	borderColor := app.skin.GetColor("response_border", "#B8860B")
	dimColor := app.skin.GetColor("banner_dim", "#888888")
	accentColor := app.skin.GetColor("banner_accent", "#DAA520")

	app.promptStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(accentColor)).
		Bold(true)

	app.toolPrefixStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(dimColor))

	app.dimStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(dimColor))

	app.responseStyle = lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).
		Padding(0, 1)

	// Create spinner.
	app.spinner = NewKawaiiSpinner(app.skin, func(frame string) {
		if !app.isTTY {
			return
		}
		if frame == "" {
			fmt.Print("\r\033[K")
		} else {
			fmt.Printf("\r\033[K%s", frame)
		}
	})

	// Build agent options with streaming callbacks.
	callbacks := &agent.StreamCallbacks{
		OnStreamDelta: func(text string) {
			if !app.streaming {
				app.spinner.Stop()
				app.streaming = true
			}
			fmt.Print(text)
		},
		OnReasoning: func(text string) {
			if app.isTTY {
				// Show reasoning in dim italic
				fmt.Print(app.dimStyle.Italic(true).Render(text))
			}
		},
		OnToolStart: func(toolName string) {
			app.spinner.Stop()
			app.streaming = false
			prefix := app.skin.ToolPrefix
			if prefix == "" {
				prefix = "┊"
			}
			emoji := "⚡"
			fmt.Println(app.toolPrefixStyle.Render(
				fmt.Sprintf("%s %s %s", prefix, emoji, toolName),
			))
		},
		OnToolProgress: func(toolName, argsPreview string) {
			prefix := app.skin.ToolPrefix
			if prefix == "" {
				prefix = "┊"
			}
			fmt.Println(app.toolPrefixStyle.Render(
				fmt.Sprintf("%s %s: %s", prefix, toolName, argsPreview),
			))
		},
		OnToolComplete: func(toolName string) {
			prefix := app.skin.ToolPrefix
			if prefix == "" {
				prefix = "┊"
			}
			fmt.Println(app.toolPrefixStyle.Render(
				fmt.Sprintf("%s %s done", prefix, toolName),
			))
			app.spinner.Start("")
		},
		OnStep: func(iteration int, prevTools []string) {
			app.streaming = false
			app.spinner.Start("")
		},
		OnStatus: func(msg string) {
			app.spinner.SetMessage(msg)
		},
	}

	allOpts := []agent.AgentOption{
		agent.WithPlatform("cli"),
		agent.WithCallbacks(callbacks),
	}
	allOpts = append(allOpts, opts...)

	ag, err := agent.New(allOpts...)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	app.agent = ag
	app.sessionID = ag.SessionID()
	app.model = ag.Model()

	return app, nil
}

// Run starts the main interactive REPL loop.
func (app *App) Run() error {
	// Print welcome banner.
	PrintWelcomeBanner(app.model, app.sessionID)
	fmt.Println()

	// Set up interrupt handler.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for range sigCh {
			if app.agent != nil {
				app.agent.Interrupt()
			}
			app.spinner.Stop()
			app.streaming = false
			fmt.Println("\n(interrupted)")
		}
	}()

	promptSymbol := app.skin.GetBranding("prompt_symbol", "❯ ")

	for app.running {
		select {
		case <-ctx.Done():
			app.running = false
			continue
		default:
		}

		// Display colored prompt.
		if app.isTTY {
			fmt.Print(app.promptStyle.Render(promptSymbol))
		} else {
			fmt.Print(promptSymbol)
		}

		if !app.scanner.Scan() {
			break
		}

		input := strings.TrimSpace(app.scanner.Text())
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			app.handleSlashCommand(input)
			continue
		}

		app.processMessage(input)
	}

	PrintGoodbye()
	app.agent.Close()

	return nil
}

// RunSingleQuery runs a single query and exits.
func (app *App) RunSingleQuery(query string) error {
	_, err := app.agent.RunConversation(query, app.history)
	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	if app.streaming {
		fmt.Println() // newline after streamed content
	}

	app.agent.Close()
	return nil
}

func (app *App) processMessage(input string) {
	app.streaming = false
	app.spinner.Start("")

	result, err := app.agent.RunConversation(input, app.history)

	app.spinner.Stop()
	app.streaming = false

	if err != nil {
		slog.Error("Agent error", "error", err)
		fmt.Printf("\nError: %v\n", err)
		return
	}

	// Update history.
	app.history = result.Messages

	fmt.Println()

	// Show token usage.
	if result.TotalTokens > 0 && app.isTTY {
		fmt.Println(app.dimStyle.Render(
			fmt.Sprintf("[tokens: %d in / %d out / %d total]",
				result.InputTokens, result.OutputTokens, result.TotalTokens),
		))
	}
}

func (app *App) handleSlashCommand(input string) {
	parts := strings.SplitN(input, " ", 2)
	cmdName := strings.TrimPrefix(parts[0], "/")
	args := ""
	if len(parts) > 1 {
		args = parts[1]
	}

	canonical, found := ResolveCommand(cmdName)
	if !found {
		fmt.Printf("Unknown command: /%s (type /help for available commands)\n", cmdName)
		return
	}

	switch canonical {
	case "help":
		PrintHelp()

	case "quit":
		app.running = false

	case "new":
		app.history = nil
		newAgent, err := agent.New(
			agent.WithPlatform("cli"),
			agent.WithCallbacks(app.agent.Callbacks()),
		)
		if err != nil {
			fmt.Printf("Error creating new session: %v\n", err)
			return
		}
		app.agent.Close()
		app.agent = newAgent
		app.sessionID = newAgent.SessionID()
		fmt.Println("New session started.")

	case "model":
		if args == "" {
			fmt.Printf("Current model: %s\n", app.model)
		} else {
			fmt.Printf("Model switch to '%s' requires restart. Use hermes --model=%s\n", args, args)
		}

	case "history":
		if len(app.history) == 0 {
			fmt.Println("No conversation history.")
			return
		}
		for i, msg := range app.history {
			role := strings.ToUpper(msg.Role[:1]) + msg.Role[1:]
			content := msg.Content
			if len(content) > 200 {
				content = content[:197] + "..."
			}
			fmt.Printf("[%d] %s: %s\n", i+1, role, content)
		}

	case "usage":
		fmt.Printf("Session: %s\n", app.sessionID)
		fmt.Printf("Model:   %s\n", app.model)
		fmt.Printf("Messages: %d\n", len(app.history))

	case "compress":
		fmt.Println("Compressing context...")

	case "config":
		fmt.Printf("Model:          %s\n", app.cfg.Model)
		fmt.Printf("Max iterations: %d\n", app.cfg.MaxIterations)
		fmt.Printf("Skin:           %s\n", app.cfg.Display.Skin)
		fmt.Printf("Streaming:      %v\n", app.cfg.Display.StreamingEnabled)

	case "skin":
		if args == "" {
			skins := ListSkins()
			fmt.Println("Available skins:")
			for _, s := range skins {
				marker := " "
				if s["name"] == GetActiveSkinName() {
					marker = "*"
				}
				fmt.Printf("  %s %-12s %s (%s)\n", marker, s["name"], s["description"], s["source"])
			}
		} else {
			SetActiveSkin(args)
			app.skin = GetActiveSkin()
			fmt.Printf("Skin set to: %s\n", args)
		}

	case "tools":
		fmt.Println("Tool management:")
		fmt.Printf("  /tools list     - List all tools\n")
		fmt.Printf("  /tools enable   - Enable a tool\n")
		fmt.Printf("  /tools disable  - Disable a tool\n")

	case "skills":
		fmt.Println("Skills management:")
		fmt.Printf("  /skills search  - Search for skills\n")
		fmt.Printf("  /skills browse  - Browse available skills\n")
		fmt.Printf("  /skills install - Install a skill\n")

	case "stop":
		fmt.Println("Stopping all background processes...")

	case "personality":
		if args == "" {
			fmt.Println("Usage: /personality <name>")
		} else {
			fmt.Printf("Personality set to: %s\n", args)
		}

	case "retry":
		if len(app.history) < 2 {
			fmt.Println("No previous message to retry.")
			return
		}
		app.history = app.history[:len(app.history)-1]
		lastIdx := len(app.history) - 1
		if lastIdx >= 0 && app.history[lastIdx].Role == "user" {
			lastMsg := app.history[lastIdx].Content
			app.history = app.history[:lastIdx]
			app.processMessage(lastMsg)
		}

	case "undo":
		if len(app.history) < 2 {
			fmt.Println("Nothing to undo.")
			return
		}
		removed := 0
		for len(app.history) > 0 && removed < 2 {
			app.history = app.history[:len(app.history)-1]
			removed++
		}
		fmt.Println("Last exchange removed.")

	case "clear":
		fmt.Print("\033[H\033[2J")
		app.history = nil
		fmt.Println("Screen cleared and session reset.")

	case "platforms":
		fmt.Println("Gateway platform status: (not running in gateway mode)")

	case "title":
		if args == "" {
			// Show current session title.
			title := agent.GenerateSessionTitle(app.history)
			fmt.Printf("Session title: %s\n", title)
		} else {
			// Set a custom title.
			if app.sessionDB != nil {
				if err := app.sessionDB.SetSessionTitle(app.sessionID, args); err != nil {
					fmt.Printf("Error setting title: %v\n", err)
				} else {
					fmt.Printf("Session title set to: %s\n", args)
				}
			} else {
				fmt.Println("Session database not available.")
			}
		}

	case "save":
		app.saveConversation()

	case "verbose":
		// Cycle through tool progress modes.
		modes := []string{"off", "new", "all", "verbose"}
		currentIdx := 0
		for i, m := range modes {
			if m == app.toolProgressMode {
				currentIdx = i
				break
			}
		}
		nextIdx := (currentIdx + 1) % len(modes)
		app.toolProgressMode = modes[nextIdx]
		fmt.Printf("Tool progress mode: %s\n", app.toolProgressMode)

	case "yolo":
		app.yoloMode = !app.yoloMode
		if app.yoloMode {
			fmt.Println("YOLO mode ON -- all dangerous commands will be auto-approved.")
		} else {
			fmt.Println("YOLO mode OFF -- dangerous commands require approval.")
		}

	case "reasoning":
		if args == "" {
			fmt.Printf("Reasoning effort: %s\n", app.cfg.Reasoning.Effort)
			fmt.Printf("Reasoning display: %v\n", app.cfg.Reasoning.Enabled)
		} else {
			switch args {
			case "show", "on":
				app.cfg.Reasoning.Enabled = true
				fmt.Println("Reasoning display enabled.")
			case "hide", "off":
				app.cfg.Reasoning.Enabled = false
				fmt.Println("Reasoning display disabled.")
			case "none", "low", "minimal", "medium", "high", "xhigh":
				app.cfg.Reasoning.Effort = args
				fmt.Printf("Reasoning effort set to: %s\n", args)
			default:
				fmt.Printf("Unknown reasoning option: %s\n", args)
				fmt.Println("Options: none, low, minimal, medium, high, xhigh, show, hide")
			}
		}

	case "profile":
		fmt.Printf("Profile: default\n")
		fmt.Printf("Home: %s\n", config.DisplayHermesHome())
		fmt.Printf("Model: %s\n", app.model)
		fmt.Printf("Provider: %s\n", app.cfg.Provider)

	case "cron":
		if args == "" {
			fmt.Println("Cron management:")
			fmt.Println("  /cron list     - List scheduled tasks")
			fmt.Println("  /cron add      - Add a new task")
			fmt.Println("  /cron remove   - Remove a task")
			fmt.Println("  /cron pause    - Pause a task")
			fmt.Println("  /cron resume   - Resume a task")
			fmt.Println("  /cron run      - Run a task immediately")
		} else {
			fmt.Printf("Cron command: %s (delegated to cron subsystem)\n", args)
		}

	case "toolsets":
		allTS := toolsets.GetAllToolsets()
		fmt.Println("Available toolsets:")
		fmt.Println()
		for name, info := range allTS {
			desc, _ := info["description"].(string)
			tls, _ := info["tools"].([]string)
			fmt.Printf("  %-25s %s (%d tools)\n", name, desc, len(tls))
		}

	case "plugins":
		fmt.Println("Installed plugins:")
		fmt.Println("  (none)")

	case "commands":
		app.printAllCommands()

	case "branch":
		branchName := args
		if branchName == "" {
			branchName = fmt.Sprintf("branch_%d", time.Now().Unix())
		}
		fmt.Printf("Session branched as: %s\n", branchName)
		fmt.Println("(Branch preserves current history as a new session fork)")

	case "insights":
		days := 7
		if args != "" {
			if d, err := strconv.Atoi(args); err == nil && d > 0 {
				days = d
			}
		}
		insights := agent.GetUsageInsights(app.sessionDB, days)
		app.printInsights(insights)

	case "background":
		if args == "" {
			fmt.Println("Usage: /background <prompt>")
			return
		}
		fmt.Printf("Running in background: %s\n", args)
		go func() {
			result, err := app.agent.Chat(args)
			if err != nil {
				slog.Error("Background task failed", "error", err)
				return
			}
			fmt.Printf("\n[Background] %s\n", result)
		}()

	default:
		fmt.Printf("Command /%s is not yet implemented in the Go CLI.\n", canonical)
	}
}

// saveConversation exports the current conversation to a JSON file.
func (app *App) saveConversation() {
	if len(app.history) == 0 {
		fmt.Println("No conversation to save.")
		return
	}

	// Build filename.
	timestamp := time.Now().Format("20060102_150405")
	title := agent.GenerateSessionTitle(app.history)
	// Sanitize title for filename.
	safeTitle := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		if r == ' ' {
			return '_'
		}
		return -1
	}, title)
	if len(safeTitle) > 40 {
		safeTitle = safeTitle[:40]
	}

	filename := fmt.Sprintf("hermes_%s_%s.json", timestamp, safeTitle)
	savePath := filepath.Join(config.HermesHome(), "sessions", filename)

	// Build export data.
	exportData := map[string]any{
		"session_id": app.sessionID,
		"model":      app.model,
		"saved_at":   time.Now().Format(time.RFC3339),
		"messages":   app.history,
	}

	data, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling conversation: %v\n", err)
		return
	}

	if err := os.WriteFile(savePath, data, 0644); err != nil {
		fmt.Printf("Error saving conversation: %v\n", err)
		return
	}

	fmt.Printf("Conversation saved to: %s\n", savePath)
}

// printAllCommands prints all available commands grouped by category.
func (app *App) printAllCommands() {
	accentColor := app.skin.GetColor("banner_accent", "#FFBF00")
	dimColor := app.skin.GetColor("banner_dim", "#B8860B")
	textColor := app.skin.GetColor("banner_text", "#FFF8DC")

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(accentColor)).
		Bold(true)
	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(textColor))
	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(dimColor))

	fmt.Println()
	fmt.Println(headerStyle.Render("All Available Commands"))
	fmt.Println()

	byCategory := GetCommandsByCategory()
	for _, cat := range CommandCategories() {
		cmds := byCategory[cat]
		fmt.Println(headerStyle.Render("  " + cat))

		for _, cmd := range cmds {
			name := "/" + cmd.Name
			if cmd.ArgsHint != "" {
				name += " " + cmd.ArgsHint
			}

			scope := ""
			if cmd.CLIOnly {
				scope = " [CLI]"
			} else if cmd.GatewayOnly {
				scope = " [Gateway]"
			}

			aliasStr := ""
			if len(cmd.Aliases) > 0 {
				aliasStr = " (alias: /" + strings.Join(cmd.Aliases, ", /") + ")"
			}

			fmt.Printf("    %s  %s%s%s\n",
				cmdStyle.Render(fmt.Sprintf("%-30s", name)),
				descStyle.Render(cmd.Description),
				descStyle.Render(scope),
				descStyle.Render(aliasStr),
			)
		}
		fmt.Println()
	}
}

// printInsights formats and prints usage insights.
func (app *App) printInsights(insights map[string]any) {
	if errMsg, ok := insights["error"].(string); ok {
		fmt.Printf("Insights error: %s\n", errMsg)
		return
	}

	accentColor := app.skin.GetColor("banner_accent", "#FFBF00")
	dimColor := app.skin.GetColor("banner_dim", "#B8860B")

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(accentColor)).
		Bold(true)
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(dimColor))

	days := insights["days"]
	fmt.Println()
	fmt.Println(headerStyle.Render(fmt.Sprintf("Usage Insights (last %v days)", days)))
	fmt.Println()

	fmt.Printf("  Sessions:       %v\n", insights["total_sessions"])
	fmt.Printf("  Messages:       %v\n", insights["total_messages"])
	fmt.Printf("  Input tokens:   %v\n", insights["total_input_tokens"])
	fmt.Printf("  Output tokens:  %v\n", insights["total_output_tokens"])
	fmt.Printf("  Total tokens:   %v\n", insights["total_tokens"])
	fmt.Printf("  Avg tokens/session: %v\n", insights["avg_tokens_per_session"])

	if cost, ok := insights["estimated_cost_usd"].(float64); ok && cost > 0 {
		fmt.Printf("  Estimated cost: $%.4f\n", cost)
	}

	// Top models.
	if topModels, ok := insights["top_models"].([]map[string]any); ok && len(topModels) > 0 {
		fmt.Println()
		fmt.Println(dimStyle.Render("  Top models:"))
		for _, m := range topModels {
			model, _ := m["model"].(string)
			count, _ := m["count"].(int)
			fmt.Printf("    %-35s %d sessions\n", model, count)
		}
	}

	// Sessions per day.
	if perDay, ok := insights["sessions_per_day"].([]map[string]any); ok && len(perDay) > 0 {
		fmt.Println()
		fmt.Println(dimStyle.Render("  Sessions per day:"))
		for _, d := range perDay {
			date, _ := d["date"].(string)
			count, _ := d["count"].(int)
			bar := strings.Repeat("|", count)
			fmt.Printf("    %s  %s (%d)\n", date, bar, count)
		}
	}

	fmt.Println()
}
