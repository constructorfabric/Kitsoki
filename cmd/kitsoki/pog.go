package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/pogbootstrap"
)

func pogCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pog",
		Short: "POG kit commands",
	}
	cmd.AddCommand(pogInitCmd())
	return cmd
}

func pogInitCmd() *cobra.Command {
	var opts pogbootstrap.Options
	cmd := &cobra.Command{
		Use:   "init <repo>",
		Short: "Initialize a POG-tracked product repo from a brief",
		Long: `Initialize a protected, lint-clean product repo from a short brief.

The command is a thin CLI entrypoint over the POG bootstrap story contract:
brief.intake -> repo.plan -> operator.consent -> repo.apply -> repo.verify
-> handoff. The current implementation records that story-shaped trace as
JSONL while the full kit story runtime lands upstream.

No remote is created and nothing is pushed by default.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.RepoPath = args[0]
			if opts.KitsokiCommand == "" {
				if exe, err := os.Executable(); err == nil {
					opts.KitsokiCommand = exe
				} else {
					opts.KitsokiCommand = "kitsoki"
				}
			}
			_, err := pogbootstrap.NewStory(opts, cmd.OutOrStdout()).Run()
			if err != nil {
				return fmt.Errorf("pog init: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.BriefPath, "brief", "", "source brief markdown path")
	cmd.Flags().StringVar(&opts.Custody, "custody", "personal-private", "initial custody assumption")
	cmd.Flags().StringVar(&opts.Remote, "remote", "none", "remote mode: none, github:owner/name, git:<url>, or adapter:<name>")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show every planned file, git, check, and remote action without mutating")
	cmd.Flags().BoolVar(&opts.Yes, "yes", false, "apply non-sensitive local file/git actions after planning")
	cmd.Flags().BoolVar(&opts.Commit, "commit", false, "create the initial commit after writing generated files")
	cmd.Flags().StringVar(&opts.TracePath, "trace", "", "write story trace JSONL to this path (default: <repo>/.artifacts/pog-init-trace.jsonl)")
	cmd.Flags().StringVar(&opts.KitsokiCommand, "kitsoki-command", "", "kitsoki command used by generated checks.sh (default: current executable)")
	_ = cmd.MarkFlagRequired("brief")
	return cmd
}
