package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"codex/agent"
	"codex/config"
	"codex/llm"
)

var rootCmd = &cobra.Command{
	Use:   "codex [prompt]",
	Short: "AI code agent powered by OpenAI-compatible APIs",
	Long: `Codex is a CLI code agent that supports any OpenAI-compatible API provider.
Supports DeepSeek, Qwen, Zhipu, Moonshot, and other compatible providers.

Examples:
  codex "write a fibonacci function in Go"
  codex --provider deepseek "refactor this codebase"
  codex  (interactive REPL mode)`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgent,
}

var (
	flagProvider    string
	flagWorkDir     string
	flagModel       string
	flagAutoApprove bool
	flagSession     string
	flagSaveAs      string
	flagThorough    bool
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProvider, "provider", "p", "", "Provider to use (overrides config)")
	rootCmd.PersistentFlags().StringVarP(&flagWorkDir, "dir", "d", "", "Working directory (defaults to current dir)")
	rootCmd.PersistentFlags().StringVarP(&flagModel, "model", "m", "", "Model to use (overrides provider default)")
	rootCmd.PersistentFlags().BoolVarP(&flagAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts for shell and patch actions")
	rootCmd.PersistentFlags().StringVarP(&flagSession, "session", "s", "", "Resume a saved session by ID")
	rootCmd.PersistentFlags().StringVar(&flagSaveAs, "save-as", "", "Auto-save session on exit with this name")
	rootCmd.PersistentFlags().BoolVar(&flagThorough, "thorough", false, "Thorough mode: explore codebase, run tests, verify changes")

	rootCmd.AddCommand(configCmd(), sessionCmd())
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runAgent(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Determine working directory
	workDir := cfg.WorkDir
	if flagWorkDir != "" {
		workDir = flagWorkDir
	}
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	// Get provider
	provider, err := resolveProvider(cfg, flagProvider)
	if err != nil {
		return err
	}

	model := provider.Model
	if flagModel != "" {
		model = flagModel
	}

	approver := agent.InteractiveApprover()
	if flagAutoApprove {
		approver = agent.AutoApprover()
	}

	client := llm.NewClient(provider.BaseURL, provider.APIKey, model)
	ag := agent.New(client, workDir, cfg.MaxSteps, os.Stdout, approver, flagThorough)

	// Resume a saved session if --session is set
	if flagSession != "" {
		s, err := agent.LoadSession(flagSession)
		if err != nil {
			return err
		}
		ag.SetMessages(s.Messages)
		fmt.Printf("\033[32m✓\033[0m \033[2mResumed session %s  (%d messages)\033[0m\n",
			s.ID, len(s.Messages))
	}

	marks := ""
	if flagAutoApprove {
		marks += "  \033[33m·  auto-approve\033[0m"
	}
	if flagThorough {
		marks += "  \033[35m·  thorough\033[0m"
	}
	fmt.Fprintf(os.Stdout, "\033[1mCodex\033[0m  \033[2m·  %s  ·  %s%s\033[0m\n",
		model, workDir, marks)

	// One-shot mode
	if len(args) > 0 {
		prompt := strings.Join(args, " ")
		ctx, cancel := interruptContext()
		defer cancel()
		if err := ag.Run(ctx, prompt); err != nil {
			return err
		}
		if flagSaveAs != "" {
			saveSession(ag, provider.Name, model, workDir, flagSaveAs)
		}
		return nil
	}

	// Interactive REPL mode
	err = runREPL(ag, provider.Name, model, workDir)
	if flagSaveAs != "" {
		saveSession(ag, provider.Name, model, workDir, flagSaveAs)
	}
	return err
}

func runREPL(ag *agent.Agent, provider, model, workDir string) error {
	promptFn := func() string {
		if ag.IsThorough() {
			return "\033[1;32mYou\033[0m \033[35m[thorough]\033[0m\033[1;32m:\033[0m "
		}
		return "\033[1;32mYou:\033[0m "
	}

	rl := newLiner(defaultHistoryFile())
	rl.SetPrompt(promptFn())
	defer rl.Close()

	fmt.Println("\033[2mType your request · /help for commands\033[0m")

	for {
		line, err := rl.Readline()
		if err != nil { // EOF or interrupt
			fmt.Println()
			promptSaveOnExit(ag, provider, model, workDir)
			fmt.Println("Goodbye!")
			return nil
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case line == "exit" || line == "quit" || line == "/quit":
			promptSaveOnExit(ag, provider, model, workDir)
			fmt.Println("Goodbye!")
			return nil

		case line == "/reset":
			ag.Reset()
			fmt.Println("\033[2m[context cleared]\033[0m")
			continue

		case line == "/help":
			printHelp()
			continue

		case line == "/thorough":
			ag.SetThorough(true)
			rl.SetPrompt(promptFn())
			fmt.Println("\033[35m◈ thorough mode — explore, verify, report\033[0m")
			continue

		case line == "/default":
			ag.SetThorough(false)
			rl.SetPrompt(promptFn())
			fmt.Println("\033[2m◈ default mode — minimal, do exactly what's asked\033[0m")
			continue

		case line == "/mode":
			if ag.IsThorough() {
				fmt.Println("\033[35m● thorough\033[0m")
			} else {
				fmt.Println("\033[2m● default\033[0m")
			}
			continue

		case line == "/undo":
			msg, err := ag.Undo()
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[31m✗ %v\033[0m\n", err)
			} else {
				fmt.Printf("\033[32m✓ %s\033[0m  (%d undo steps left)\n", msg, ag.UndoLen())
			}
			continue

		case line == "/sessions":
			printSessions()
			continue

		case strings.HasPrefix(line, "/save"):
			name := strings.TrimSpace(strings.TrimPrefix(line, "/save"))
			saveSession(ag, provider, model, workDir, name)
			continue

		case strings.HasPrefix(line, "/load"):
			id := strings.TrimSpace(strings.TrimPrefix(line, "/load"))
			if id == "" {
				fmt.Println("Usage: /load <session-id>")
				continue
			}
			loadSession(ag, id)
			continue
		}

		ctx, cancel := interruptContext()
		if err := ag.Run(ctx, line); err != nil {
			if err == context.Canceled {
				fmt.Println("\n\033[33m[interrupted]\033[0m")
			} else {
				fmt.Fprintf(os.Stderr, "\033[31mError: %v\033[0m\n", err)
			}
		}
		cancel()
		fmt.Println()
	}
}

func resolveProvider(cfg *config.Config, override string) (*config.Provider, error) {
	if override != "" {
		// Temporarily set the provider for this run
		orig := cfg.CurrentProvider
		cfg.CurrentProvider = override
		p, err := cfg.GetCurrentProvider()
		cfg.CurrentProvider = orig
		return p, err
	}
	return cfg.GetCurrentProvider()
}

func interruptContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()
	return ctx, cancel
}

func printHelp() {
	fmt.Println(`Commands:
  /thorough         Switch to thorough mode (explore, verify, report)
  /default          Switch to default mode (minimal, do exactly what's asked)
  /mode             Show current mode
  /reset            Clear conversation history
  /undo             Revert last file write or patch
  /save [name]      Save current session
  /load <id>        Load a saved session
  /sessions         List saved sessions
  /help             Show this help
  exit              Exit Codex`)
}

func saveSession(ag *agent.Agent, provider, model, workDir, name string) {
	msgs := ag.Messages()
	if len(msgs) == 0 {
		fmt.Println("Nothing to save (no messages yet)")
		return
	}
	id, err := agent.SaveSession(msgs, name, provider, model, workDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m✗ save failed: %v\033[0m\n", err)
		return
	}
	stats := ag.Stats()
	extra := ""
	if stats.Total() > 0 {
		extra = fmt.Sprintf(" | %s", stats.String())
	}
	fmt.Printf("\033[32m✓ Session saved: %s\033[0m  (%d messages%s)\n", id, len(msgs), extra)
}

func loadSession(ag *agent.Agent, id string) {
	s, err := agent.LoadSession(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m✗ %v\033[0m\n", err)
		return
	}
	ag.SetMessages(s.Messages)
	fmt.Printf("\033[32m✓ Loaded session %s\033[0m  (%d messages, saved %s)\n",
		s.ID, len(s.Messages), s.CreatedAt.Format("2006-01-02 15:04"))
}

func promptSaveOnExit(ag *agent.Agent, provider, model, workDir string) {
	if flagSaveAs != "" {
		return
	}
	msgs := ag.Messages()
	userCount := 0
	for _, m := range msgs {
		if m.Role == "user" {
			userCount++
		}
	}
	if userCount == 0 {
		return
	}

	idx := agent.Prompt("Save this session?", []agent.Choice{
		{"Yes — enter a name"},
		{"No"},
	}, 1)

	if idx != 0 {
		return
	}

	fmt.Printf("\033[2mSession name (blank = timestamp): \033[0m")
	var name string
	fmt.Scanln(&name)
	saveSession(ag, provider, model, workDir, strings.TrimSpace(name))
}

func printSessions() {
	sessions, err := agent.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\033[31m✗ %v\033[0m\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Println("No saved sessions. Use /save to save one.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tMODEL\tMSGS\tDATE")
	fmt.Fprintln(w, "--\t--------\t-----\t----\t----")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			s.ID, s.Provider, s.Model, len(s.Messages),
			s.CreatedAt.Format("2006-01-02 15:04"))
	}
	w.Flush()
}
