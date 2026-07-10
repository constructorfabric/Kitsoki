package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/kitrepo"
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
			return runPersonaQAPython(cmd, args)
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

func runPersonaQAPython(cmd *cobra.Command, args []string) error {
	repo := os.Getenv(kitrepo.EnvVar)
	if strings.TrimSpace(repo) == "" {
		repo = kitrepo.Resolve()
	}
	if strings.TrimSpace(repo) == "" {
		return fmt.Errorf("persona-qa: cannot resolve Kitsoki repo; run from a checkout or set %s", kitrepo.EnvVar)
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	python := os.Getenv("PYTHON")
	if strings.TrimSpace(python) == "" {
		python = "python3"
	}
	argv := append([]string{"-m", "tools.persona_qa.kit"}, args...)
	proc := exec.CommandContext(cmd.Context(), python, argv...)
	if wd, err := os.Getwd(); err == nil {
		proc.Dir = wd
	}
	proc.Stdout = cmd.OutOrStdout()
	proc.Stderr = cmd.ErrOrStderr()
	proc.Stdin = cmd.InOrStdin()
	proc.Env = append(os.Environ(),
		kitrepo.EnvVar+"="+repoAbs,
		"PYTHONPATH="+prependEnvPath(os.Getenv("PYTHONPATH"), repoAbs),
	)
	if err := proc.Run(); err != nil {
		return fmt.Errorf("persona-qa: %w", err)
	}
	return nil
}

func prependEnvPath(existing, value string) string {
	if existing == "" {
		return value
	}
	for _, part := range strings.Split(existing, string(os.PathListSeparator)) {
		if part == value {
			return existing
		}
	}
	return value + string(os.PathListSeparator) + existing
}
