package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"codex/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Codex configuration",
	}

	cmd.AddCommand(
		configListCmd(),
		configSetProviderCmd(),
		configSetKeyCmd(),
		configSetModelCmd(),
		configShowCmd(),
	)
	return cmd
}

func configListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all configured providers",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tMODEL\tBASE URL\tKEY SET\tACTIVE")
			fmt.Fprintln(w, "--------\t-----\t--------\t-------\t------")

			for name, p := range cfg.Providers {
				keySet := "✗"
				if p.APIKey != "" {
					keySet = "✓"
				}
				active := ""
				if name == cfg.CurrentProvider {
					active = "← current"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					name, p.Model, p.BaseURL, keySet, active)
			}
			return w.Flush()
		},
	}
}

func configSetProviderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-provider <name>",
		Short: "Set the active provider",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Providers[name]; !ok {
				return fmt.Errorf("provider %q not found. Use 'codex config list' to see available providers", name)
			}
			cfg.CurrentProvider = name
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("✓ Active provider set to: %s\n", name)
			return nil
		},
	}
}

func configSetKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-key <provider> <api-key>",
		Short: "Set API key for a provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, key := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, ok := cfg.Providers[name]
			if !ok {
				return fmt.Errorf("provider %q not found", name)
			}
			p.APIKey = key
			cfg.Providers[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("✓ API key set for provider: %s\n", name)
			return nil
		},
	}
}

func configSetModelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set-model <provider> <model>",
		Short: "Set the model for a provider",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, model := args[0], args[1]
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			p, ok := cfg.Providers[name]
			if !ok {
				return fmt.Errorf("provider %q not found", name)
			}
			p.Model = model
			cfg.Providers[name] = p
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Printf("✓ Model for %s set to: %s\n", name, model)
			return nil
		},
	}
}

func configShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show current configuration file path and content summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Config file: %s\n\n", config.ConfigPath())
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Printf("Current provider: %s\n", cfg.CurrentProvider)
			fmt.Printf("Max steps:        %d\n", cfg.MaxSteps)
			fmt.Printf("Work dir:         %s\n", cfg.WorkDir)
			return nil
		},
	}
}
