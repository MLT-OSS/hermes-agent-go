// Package main implements the hermes CLI entry point using Cobra.
package main

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hermes-agent/hermes-agent-go/internal/acp"
	"github.com/hermes-agent/hermes-agent-go/internal/agent"
	"github.com/hermes-agent/hermes-agent-go/internal/batch"
	"github.com/hermes-agent/hermes-agent-go/internal/cli"
	"github.com/hermes-agent/hermes-agent-go/internal/config"
	"github.com/hermes-agent/hermes-agent-go/internal/cron"
	"github.com/hermes-agent/hermes-agent-go/internal/gateway"
	"github.com/hermes-agent/hermes-agent-go/internal/gateway/platforms"
	"github.com/hermes-agent/hermes-agent-go/internal/skills"
	"github.com/spf13/cobra"
)

// Build-time variables set via ldflags.
var (
	version     = "dev"
	releaseDate = "unknown"
	commit      = "none"
)

func init() {
	cli.Version = version
	cli.ReleaseDate = releaseDate
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// --- Persistent flags ---
var (
	flagProfile  string
	flagModel    string
	flagQuiet    bool
	flagDebug    bool
	flagBaseURL  string
	flagAPIKey   string
	flagAPIMode  string
	flagProvider string
)

// --- Root command ---

var rootCmd = &cobra.Command{
	Use:   "hermes",
	Short: "Hermes Agent - AI assistant framework",
	Long: `Hermes Agent is an AI assistant framework by Nous Research.
It supports interactive CLI, messaging gateways, scheduled tasks, and more.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInteractiveCLI()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProfile, "profile", "p", "", "Configuration profile to use")
	rootCmd.PersistentFlags().StringVarP(&flagModel, "model", "m", "", "Override the model for this session")
	rootCmd.PersistentFlags().BoolVarP(&flagQuiet, "quiet", "q", false, "Quiet mode (suppress UI output)")
	rootCmd.PersistentFlags().BoolVar(&flagDebug, "debug", false, "Enable debug logging")
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", "", "API base URL")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", "", "API key")
	rootCmd.PersistentFlags().StringVar(&flagAPIMode, "api-mode", "", "API mode: openai or anthropic")
	rootCmd.PersistentFlags().StringVar(&flagProvider, "provider", "", "Provider name")

	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(toolsCmd)
	rootCmd.AddCommand(skillsCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(gatewayCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(cronCmd)
	rootCmd.AddCommand(clawCmd)
	rootCmd.AddCommand(batchCmd)
	rootCmd.AddCommand(profileCmd)
	rootCmd.AddCommand(themeCmd)
}

func setupLogging() {
	// Wire --profile flag to config before any config access.
	if flagProfile != "" {
		config.OverrideActiveProfile(flagProfile)
	}

	level := slog.LevelInfo
	if flagDebug || os.Getenv("HERMES_DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func buildAgentOptions() []agent.AgentOption {
	var opts []agent.AgentOption
	if flagModel != "" {
		opts = append(opts, agent.WithModel(flagModel))
	}
	if flagQuiet {
		opts = append(opts, agent.WithQuietMode(true))
	}
	if flagBaseURL != "" {
		opts = append(opts, agent.WithBaseURL(flagBaseURL))
	}
	if flagAPIKey != "" {
		opts = append(opts, agent.WithAPIKey(flagAPIKey))
	}
	if flagAPIMode != "" {
		opts = append(opts, agent.WithAPIMode(flagAPIMode))
	}
	if flagProvider != "" {
		opts = append(opts, agent.WithProvider(flagProvider))
	}
	return opts
}

func runInteractiveCLI() error {
	setupLogging()
	config.EnsureHermesHome()

	app, err := cli.NewApp(buildAgentOptions()...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return err
	}

	return app.Run()
}

// --- Chat command (single query mode) ---

var chatCmd = &cobra.Command{
	Use:   "chat [query]",
	Short: "Send a single query to the agent",
	Long:  "Send a single query and get a response without entering interactive mode.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogging()
		config.EnsureHermesHome()

		query := strings.Join(args, " ")
		opts := buildAgentOptions()
		opts = append(opts, agent.WithQuietMode(true))

		app, err := cli.NewApp(opts...)
		if err != nil {
			return err
		}
		return app.RunSingleQuery(query)
	},
}

// --- Model command ---

var modelCmd = &cobra.Command{
	Use:   "model [name]",
	Short: "Show or switch the active model",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		if len(args) == 0 {
			fmt.Printf("Current model: %s\n", cfg.Model)
			fmt.Println("\nTo switch: hermes model <model-name>")
			return nil
		}

		newModel := args[0]
		cfg.Model = newModel
		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Model set to: %s\n", newModel)
		return nil
	},
}

// --- Tools command ---

var toolsCmd = &cobra.Command{
	Use:   "tools [subcommand]",
	Short: "Manage tools and toolsets",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()
		cli.RunToolsManager(cfg, args)
		return nil
	},
}

// --- Skills command ---

var skillsCmd = &cobra.Command{
	Use:   "skills [subcommand]",
	Short: "Manage skills",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "list" {
			allSkills, err := skills.LoadAllSkills()
			if err != nil {
				return fmt.Errorf("load skills: %w", err)
			}

			if len(allSkills) == 0 {
				fmt.Println("No skills installed.")
				fmt.Printf("Skills directory: %s/skills/\n", config.HermesHome())
				return nil
			}

			fmt.Println("Installed skills:")
			byCategory := skills.GetSkillsByCategory(allSkills)
			for category, catSkills := range byCategory {
				fmt.Printf("\n  %s:\n", category)
				for _, skill := range catSkills {
					desc := skill.Meta.Description
					if desc == "" {
						desc = "(no description)"
					}
					fmt.Printf("    /%s  -  %s\n", skill.Meta.Name, desc)
				}
			}
			return nil
		}

		switch args[0] {
		case "search":
			query := ""
			if len(args) > 1 {
				query = strings.Join(args[1:], " ")
			}
			cli.RunSkillsSearch(query)
			return nil
		case "install":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes skills install <name>")
			}
			cli.RunSkillsInstall(args[1])
			return nil
		case "inspect":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes skills inspect <name>")
			}
			skill, err := skills.FindSkill(args[1])
			if err != nil {
				return err
			}
			fmt.Printf("Name:        %s\n", skill.Meta.Name)
			fmt.Printf("Description: %s\n", skill.Meta.Description)
			fmt.Printf("Version:     %s\n", skill.Meta.Version)
			fmt.Printf("Category:    %s\n", skill.Meta.Category)
			fmt.Printf("Tags:        %s\n", strings.Join(skill.Meta.Tags, ", "))
			fmt.Printf("Platforms:   %s\n", strings.Join(skill.Meta.Platforms, ", "))
			fmt.Printf("Path:        %s\n", skill.Meta.Path)

		default:
			return fmt.Errorf("unknown subcommand: %s (try: list, search, install, inspect)", args[0])
		}

		return nil
	},
}

// --- Config command ---

var configCmd = &cobra.Command{
	Use:   "config [key] [value]",
	Short: "View or modify configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.Load()

		if len(args) == 0 {
			// Show current config.
			fmt.Printf("Model:          %s\n", cfg.Model)
			fmt.Printf("Provider:       %s\n", cfg.Provider)
			fmt.Printf("Max iterations: %d\n", cfg.MaxIterations)
			fmt.Printf("Skin:           %s\n", cfg.Display.Skin)
			fmt.Printf("Streaming:      %v\n", cfg.Display.StreamingEnabled)
			fmt.Printf("Memory:         %v\n", cfg.Memory.Enabled)
			fmt.Printf("Reasoning:      %v (effort: %s)\n", cfg.Reasoning.Enabled, cfg.Reasoning.Effort)
			fmt.Printf("\nConfig file:    %s/config.yaml\n", config.HermesHome())
			return nil
		}

		if len(args) == 1 {
			// Get a specific key.
			switch args[0] {
			case "model":
				fmt.Println(cfg.Model)
			case "provider":
				fmt.Println(cfg.Provider)
			case "max_iterations":
				fmt.Println(cfg.MaxIterations)
			case "skin":
				fmt.Println(cfg.Display.Skin)
			default:
				return fmt.Errorf("unknown config key: %s", args[0])
			}
			return nil
		}

		// Set a key.
		switch args[0] {
		case "model":
			cfg.Model = args[1]
		case "skin":
			cfg.Display.Skin = args[1]
		default:
			return fmt.Errorf("cannot set config key: %s (use config.yaml directly)", args[0])
		}

		if err := config.Save(cfg); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Set %s = %s\n", args[0], args[1])
		return nil
	},
}

// --- Gateway command ---

var gatewayCmd = &cobra.Command{
	Use:   "gateway [subcommand]",
	Short: "Manage the messaging gateway",
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogging()
		config.EnsureHermesHome()

		if len(args) == 0 || args[0] == "start" {
			return runGateway()
		}

		switch args[0] {
		case "status":
			fmt.Println("Gateway status:")
			if config.HasEnv("TELEGRAM_BOT_TOKEN") {
				fmt.Println("  Telegram: configured")
			}
			if config.HasEnv("DISCORD_BOT_TOKEN") {
				fmt.Println("  Discord:  configured")
			}
			if config.HasEnv("SLACK_BOT_TOKEN") {
				fmt.Println("  Slack:    configured")
			}

		case "setup":
			fmt.Println("Interactive gateway setup:")
			fmt.Println("  Configure platforms in ~/.hermes/.env:")
			fmt.Println("  TELEGRAM_BOT_TOKEN=your-token")
			fmt.Println("  DISCORD_BOT_TOKEN=your-token")
			fmt.Println("  SLACK_BOT_TOKEN=your-token")
			fmt.Println("  SLACK_APP_TOKEN=your-token")

		case "stop":
			fmt.Println("Stopping gateway... (send SIGTERM to the running process)")

		default:
			return fmt.Errorf("unknown subcommand: %s (try: start, status, setup, stop)", args[0])
		}

		return nil
	},
}

func runGateway() error {
	gwCfg := gateway.DefaultGatewayConfig()

	// Load allowed_users from config file if available.
	if gcf, err := gateway.LoadGatewayConfig(); err == nil && gcf.AllowedUsers != nil {
		gwCfg.AllowedUsers = gcf.AllowedUsers
	}

	runner := gateway.NewRunner(gwCfg)

	// Register platform adapters from environment.
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		adapter := platforms.NewTelegramAdapter(token)
		runner.RegisterAdapter(adapter)
	}
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		adapter := platforms.NewDiscordAdapter(token)
		runner.RegisterAdapter(adapter)
	}
	if botToken := os.Getenv("SLACK_BOT_TOKEN"); botToken != "" {
		appToken := os.Getenv("SLACK_APP_TOKEN")
		adapter := platforms.NewSlackAdapter(botToken, appToken)
		runner.RegisterAdapter(adapter)
	}

	if len(runner.ConnectedPlatforms()) == 0 {
		fmt.Println("No messaging platforms configured.")
		fmt.Println("Set TELEGRAM_BOT_TOKEN, DISCORD_BOT_TOKEN, or SLACK_BOT_TOKEN in ~/.hermes/.env")
		return fmt.Errorf("no platforms configured")
	}

	// Start the cron scheduler.
	scheduler := cron.NewScheduler()
	if err := scheduler.Start(); err != nil {
		slog.Warn("Failed to start cron scheduler", "error", err)
	}
	defer scheduler.Stop()

	// Start the gateway.
	if err := runner.Start(); err != nil {
		return fmt.Errorf("start gateway: %w", err)
	}

	// Start ACP server if configured.
	if acpPortStr := os.Getenv("HERMES_ACP_PORT"); acpPortStr != "" {
		acpPort, err := strconv.Atoi(acpPortStr)
		if err == nil && acpPort > 0 {
			acpServer := acp.NewACPServer(acpPort)
			go func() {
				if err := acpServer.Start(); err != nil {
					slog.Warn("ACP server failed", "error", err)
				}
			}()
			defer acpServer.Stop()
			slog.Info("ACP server started", "port", acpPort)
		}
	}

	fmt.Println("Gateway running. Press Ctrl+C to stop.")

	// Wait for interrupt.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down...")
	runner.Stop()
	return nil
}

// --- Setup command ---

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		config.EnsureHermesHome()
		cli.RunSetupWizard()
		return nil
	},
}

// --- Doctor command ---

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostics and check system health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cli.RunDoctor()
		return nil
	},
}

// --- Update command ---

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update Hermes Agent to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Update functionality is not yet implemented.")
		fmt.Println("To update manually, pull the latest code and rebuild.")
		return nil
	},
}

// --- Version command ---

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Hermes Agent v%s (%s)\n", version, releaseDate)
		fmt.Printf("Commit: %s\n", commit)
	},
}

// --- Claw (OpenClaw migration) command ---

var clawCmd = &cobra.Command{
	Use:   "claw",
	Short: "OpenClaw migration tools",
}

var clawMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate settings and data from OpenClaw to Hermes",
	RunE: func(cmd *cobra.Command, args []string) error {
		config.EnsureHermesHome()
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		overwrite, _ := cmd.Flags().GetBool("overwrite")
		return cli.RunClawMigrate(dryRun, overwrite)
	},
}

func init() {
	clawMigrateCmd.Flags().Bool("dry-run", false, "Show what would be migrated without making changes")
	clawMigrateCmd.Flags().Bool("overwrite", false, "Overwrite existing files in target")
	clawCmd.AddCommand(clawMigrateCmd)
}

// --- Batch command ---

var batchCmd = &cobra.Command{
	Use:   "batch [file-or-prompts...]",
	Short: "Run multiple prompts in parallel and collect results",
	Long: `Run batch trajectory generation. Prompts can be provided as arguments,
or read from a file (one prompt per line) using --file.

Examples:
  hermes batch "What is 2+2?" "Explain gravity"
  hermes batch --file prompts.txt --model openai/gpt-4o --workers 8`,
	RunE: func(cmd *cobra.Command, args []string) error {
		setupLogging()

		promptFile, _ := cmd.Flags().GetString("file")
		workers, _ := cmd.Flags().GetInt("workers")
		outputDir, _ := cmd.Flags().GetString("output")
		toolSets, _ := cmd.Flags().GetStringSlice("toolsets")

		model := flagModel

		var prompts []string

		// Read prompts from file if provided.
		if promptFile != "" {
			f, err := os.Open(promptFile)
			if err != nil {
				return fmt.Errorf("open prompts file: %w", err)
			}
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" && !strings.HasPrefix(line, "#") {
					prompts = append(prompts, line)
				}
			}
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read prompts file: %w", err)
			}
		}

		// Add positional args as prompts.
		prompts = append(prompts, args...)

		if len(prompts) == 0 {
			return fmt.Errorf("no prompts provided (use arguments or --file)")
		}

		if model == "" {
			cfg := config.Load()
			model = cfg.Model
		}

		fmt.Printf("Running batch: %d prompts, model=%s, workers=%d\n\n", len(prompts), model, workers)

		cfg := batch.BatchConfig{
			Prompts:    prompts,
			Model:      model,
			MaxWorkers: workers,
			OutputDir:  outputDir,
			ToolSets:   toolSets,
		}

		results, err := batch.RunBatch(cfg)
		if err != nil {
			return fmt.Errorf("batch run: %w", err)
		}

		// Print summary.
		succeeded := 0
		totalTokens := 0
		for _, r := range results {
			if r.Error == "" {
				succeeded++
			}
			totalTokens += r.Tokens
			status := "OK"
			if r.Error != "" {
				status = "ERROR: " + r.Error
			}
			prompt := r.Prompt
			if len(prompt) > 60 {
				prompt = prompt[:57] + "..."
			}
			fmt.Printf("  %-60s  %s  (%d tokens, %v)\n", prompt, status, r.Tokens, r.Duration.Round(time.Millisecond))
		}

		fmt.Printf("\nDone: %d/%d succeeded, %d total tokens\n", succeeded, len(results), totalTokens)
		if cfg.OutputDir != "" {
			fmt.Printf("Trajectories saved to: %s\n", cfg.OutputDir)
		}

		return nil
	},
}

func init() {
	batchCmd.Flags().StringP("file", "f", "", "File containing prompts (one per line)")
	// Note: model can be set via the persistent --model/-m flag
	batchCmd.Flags().IntP("workers", "w", 4, "Max parallel workers")
	batchCmd.Flags().StringP("output", "o", "", "Output directory for trajectories")
	batchCmd.Flags().StringSlice("toolsets", nil, "Toolsets to enable (comma-separated)")
}

// --- Cron command ---

var cronCmd = &cobra.Command{
	Use:   "cron [subcommand]",
	Short: "Manage scheduled tasks",
	RunE: func(cmd *cobra.Command, args []string) error {
		store := cron.NewJobStore()
		if err := store.Load(); err != nil {
			return fmt.Errorf("load jobs: %w", err)
		}

		if len(args) == 0 || args[0] == "list" {
			jobs := store.List()
			if len(jobs) == 0 {
				fmt.Println("No cron jobs configured.")
				return nil
			}
			fmt.Println("Cron Jobs:")
			for _, job := range jobs {
				status := "enabled"
				if !job.Enabled {
					status = "paused"
				}
				fmt.Printf("  [%s] %-20s  %s  (%s)\n", job.ID, job.Name, job.Schedule, status)
			}
			return nil
		}

		switch args[0] {
		case "run":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes cron run <job-id>")
			}
			scheduler := cron.NewScheduler()
			return scheduler.TriggerJob(args[1])

		case "pause":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes cron pause <job-id>")
			}
			return store.Pause(args[1])

		case "resume":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes cron resume <job-id>")
			}
			return store.Resume(args[1])

		case "remove":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes cron remove <job-id>")
			}
			return store.Remove(args[1])

		default:
			return fmt.Errorf("unknown subcommand: %s (try: list, run, pause, resume, remove)", args[0])
		}
	},
}

// --- Profile command ---

var profileCmd = &cobra.Command{
	Use:   "profile [subcommand]",
	Short: "Manage configuration profiles",
	Long:  "Create, switch, list, and delete isolated configuration profiles.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "list" {
			cli.RunProfileList()
			return nil
		}

		switch args[0] {
		case "show":
			cli.RunProfileShow()
			return nil

		case "create":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes profile create <name>")
			}
			return cli.RunProfileCreate(args[1])

		case "delete":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes profile delete <name>")
			}
			return cli.RunProfileDelete(args[1])

		case "switch":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes profile switch <name>")
			}
			return cli.RunProfileSwitch(args[1])

		default:
			return fmt.Errorf("unknown subcommand: %s (try: list, show, create, delete, switch)", args[0])
		}
	},
}

// --- Theme command ---

var themeCmd = &cobra.Command{
	Use:   "theme [subcommand]",
	Short: "Manage CLI themes",
	Long:  "List, switch, and show CLI visual themes (skins).",
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "list" {
			cli.RunThemeList()
			return nil
		}

		switch args[0] {
		case "show":
			cli.RunThemeShow()
			return nil

		case "switch":
			if len(args) < 2 {
				return fmt.Errorf("usage: hermes theme switch <name>")
			}
			return cli.RunThemeSwitch(args[1])

		default:
			return fmt.Errorf("unknown subcommand: %s (try: list, show, switch)", args[0])
		}
	},
}
