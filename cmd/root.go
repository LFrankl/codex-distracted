package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chzyer/readline"
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
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&flagProvider, "provider", "p", "", "Provider to use (overrides config)")
	rootCmd.PersistentFlags().StringVarP(&flagWorkDir, "dir", "d", "", "Working directory (defaults to current dir)")
	rootCmd.PersistentFlags().StringVarP(&flagModel, "model", "m", "", "Model to use (overrides provider default)")
	rootCmd.PersistentFlags().BoolVarP(&flagAutoApprove, "auto-approve", "y", false, "Skip confirmation prompts for shell and patch actions")

	rootCmd.AddCommand(configCmd())
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
	ag := agent.New(client, workDir, cfg.MaxSteps, os.Stdout, approver)

	approveNote := "interactive"
	if flagAutoApprove {
		approveNote = "auto-approve"
	}
	fmt.Fprintf(os.Stdout, "\033[2m[Codex] Provider: %s | Model: %s | Dir: %s | Approve: %s\033[0m\n",
		provider.Name, model, workDir, approveNote)

	// One-shot mode: prompt provided as arg
	if len(args) > 0 {
		prompt := strings.Join(args, " ")
		ctx, cancel := interruptContext()
		defer cancel()
		return ag.Run(ctx, prompt)
	}

	// Interactive REPL mode
	return runREPL(ag)
}

func runREPL(ag *agent.Agent) error {
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "\033[1;32mYou:\033[0m ",
		HistoryFile:     config.ConfigDir() + "/.history",
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return err
	}
	defer rl.Close()

	fmt.Println("\033[1mCodex REPL\033[0m — type your request, 'exit' to quit, '/reset' to clear history")

	for {
		line, err := rl.Readline()
		if err != nil { // EOF or interrupt
			fmt.Println("\nGoodbye!")
			return nil
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch line {
		case "exit", "quit", "/quit":
			fmt.Println("Goodbye!")
			return nil
		case "/reset":
			ag.Reset()
			fmt.Println("\033[2m[context cleared]\033[0m")
			continue
		case "/help":
			printHelp()
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
  /reset   Clear conversation history
  /help    Show this help
  exit     Exit Codex`)
}
