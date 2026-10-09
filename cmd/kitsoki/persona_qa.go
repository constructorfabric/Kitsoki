package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func personaQACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "persona-qa <init|validate|transports|emit-run|drive|review|deck|complete> [flags]",
		Short:              "Internal Persona QA compatibility adapter",
		Hidden:             true,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || hasHelpArg(args) {
				printPersonaQAHelp(cmd)
				return nil
			}
			return fmt.Errorf("persona-qa: the compatibility adapter no longer runs Python; use `kitsoki run @kitsoki/scenario-qa` or `kitsoki starlark run <script.star>`")
		},
	}
	return cmd
}

func printPersonaQAHelp(cmd *cobra.Command) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Persona QA compatibility adapter")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "The supported operator surface is the story:")
	fmt.Fprintln(out, "  kitsoki run @kitsoki/scenario-qa")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Story prompts:")
	fmt.Fprintln(out, "  preview project-onboarding across all transports")
	fmt.Fprintln(out, "  check project-onboarding across all transports for core-maintainer on gears-rust")
	fmt.Fprintln(out, "  next leg")
	fmt.Fprintln(out, "  report")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Maintainer/debug adapter usage:")
	fmt.Fprintln(out, "  kitsoki persona-qa init --root .")
	fmt.Fprintln(out, "  kitsoki persona-qa validate --config persona-qa.yaml")
	fmt.Fprintln(out, "  kitsoki persona-qa transports --config persona-qa.yaml --scenario project-onboarding --transport all")
	fmt.Fprintln(out, "  kitsoki persona-qa emit-run --config persona-qa.yaml --project local-app --persona core-maintainer --scenario project-onboarding --transport all")
	fmt.Fprintln(out, "  kitsoki persona-qa emit-run --config persona-qa.yaml --scenario project-onboarding --transport all --preview")
	fmt.Fprintln(out, "  kitsoki persona-qa drive --config persona-qa.yaml --run-dir <run-dir> --mode replay")
	fmt.Fprintln(out, "  kitsoki persona-qa review --config persona-qa.yaml --run-dir <run-dir>")
	fmt.Fprintln(out, "  kitsoki persona-qa deck --config persona-qa.yaml --run-dir <run-dir> --out docs/decks/persona-qa-latest.slidey.json")
	fmt.Fprintln(out, "  kitsoki persona-qa complete --config persona-qa.yaml --run-dir <run-dir>")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Adapter commands are no-LLM by default. Live capture stays behind explicit story/operator surfaces.")
}

func hasHelpArg(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			return true
		}
	}
	return false
}
