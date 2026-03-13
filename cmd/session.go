package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"codex/agent"
	"codex/config"
	"codex/llm"
)

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage saved sessions",
	}
	cmd.AddCommand(
		sessionListCmd(),
		sessionShowCmd(),
		sessionDeleteCmd(),
		sessionResumeCmd(),
	)
	return cmd
}

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all saved sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions, err := agent.ListSessions()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("No saved sessions. Use /save inside the REPL or --save-as flag.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tMODEL\tMSGS\tWORK DIR\tDATE")
			fmt.Fprintln(w, "──\t─────\t────\t────────\t────")
			for _, s := range sessions {
				fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
					s.ID, s.Model, len(s.Messages),
					truncate(s.WorkDir, 30),
					s.CreatedAt.Format("01-02 15:04"),
				)
			}
			return w.Flush()
		},
	}
}

func sessionShowCmd() *cobra.Command {
	var showAll bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show messages in a saved session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := agent.LoadSession(args[0])
			if err != nil {
				return err
			}
			fmt.Printf("\033[1mSession:\033[0m %s\n", s.ID)
			fmt.Printf("\033[2mModel: %s  ·  %s  ·  %s\033[0m\n\n",
				s.Model, s.WorkDir, s.CreatedAt.Format("2006-01-02 15:04"))

			for _, m := range s.Messages {
				switch m.Role {
				case "system":
					if showAll {
						fmt.Printf("\033[2m[system]\033[0m\n\033[2m%s\033[0m\n\n",
							truncate(fmt.Sprintf("%v", m.Content), 200))
					}
				case "user":
					fmt.Printf("\033[1;32mYou:\033[0m %v\n\n", m.Content)
				case "assistant":
					if len(m.ToolCalls) > 0 {
						if showAll {
							for _, tc := range m.ToolCalls {
								fmt.Printf("  \033[33m◆\033[0m \033[1m%s\033[0m\n", tc.Function.Name)
							}
						}
					} else {
						content := fmt.Sprintf("%v", m.Content)
						if content != "" && content != "<nil>" {
							fmt.Printf("\033[36m◈\033[0m %s\n\n", content)
						}
					}
				case "tool":
					if showAll {
						fmt.Printf("  \033[2m[tool result: %s]\033[0m\n", m.Name)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&showAll, "all", false, "Show system prompt and tool calls too")
	return cmd
}

func sessionDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <id>",
		Aliases: []string{"rm"},
		Short:   "Delete a saved session",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if !force {
				idx := agent.Prompt(
					fmt.Sprintf("Delete session %q?", id),
					[]agent.Choice{{"Yes, delete"}, {"No, cancel"}},
					1,
				)
				if idx != 0 {
					fmt.Println("Cancelled.")
					return nil
				}
			}
			if err := agent.DeleteSession(id); err != nil {
				return err
			}
			fmt.Printf("\033[32m✓\033[0m Deleted session: %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation")
	return cmd
}

func sessionResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <id>",
		Short: "Resume a saved session in interactive REPL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := agent.LoadSession(args[0])
			if err != nil {
				return err
			}

			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Use session's provider/model, fallback to current config
			providerName := s.Provider
			if providerName == "" {
				providerName = cfg.CurrentProvider
			}
			cfg.CurrentProvider = providerName
			provider, err := cfg.GetCurrentProvider()
			if err != nil {
				return err
			}
			model := s.Model
			if model == "" {
				model = provider.Model
			}
			workDir := s.WorkDir
			if workDir == "" {
				workDir, _ = os.Getwd()
			}

			approver := agent.InteractiveApprover()
			client := llm.NewClient(provider.BaseURL, provider.APIKey, model)
			ag := agent.New(client, workDir, cfg.MaxSteps, os.Stdout, approver, false)
			ag.SetMessages(s.Messages)

			fmt.Printf("\033[1mCodex\033[0m  \033[2m·  %s  ·  %s\033[0m\n", model, workDir)
			fmt.Printf("\033[32m✓\033[0m \033[2mResumed session %s  (%d messages, %s)\033[0m\n",
				s.ID, len(s.Messages), s.CreatedAt.Format("2006-01-02 15:04"))

			return runREPL(ag, nil, provider.Name, model, workDir)
		},
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max+1:]
}
