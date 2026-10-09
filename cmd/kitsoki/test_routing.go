package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"kitsoki/internal/testrunner"
)

// testRoutingCmd implements `kitsoki test routing` — Mode 0: pure no-LLM
// routing-tier fixtures. Unlike `test intents` (Mode 1, harness/recording
// driven), this NEVER constructs an LLM harness: it calls
// orchestrator.Classify directly (deterministic display/example match ->
// semantic synonym/template -> optional embedding tier) and asserts either a
// resolved intent or an explicit `defers_to_interpreter: true` no-match. See
// internal/testrunner/routing.go and docs/testing/routing-tuning.md.
//
// Default intents glob: <app-dir>/intents/*.yaml (same fixture files `test
// intents` reads — the two commands assert different things about the same
// phrasing corpus).
func testRoutingCmd() *cobra.Command {
	var (
		intentsGlob string
		onlyState   string
	)

	cmd := &cobra.Command{
		Use:   "routing <app.yaml>",
		Short: "Run Mode 0 no-LLM routing-tier fixture tests",
		Long: `Run assertions against the deterministic + semantic (semroute) + embedding
routing tiers ONLY — no LLM harness is ever constructed or invoked. A fixture
either names the expected resolved intent, or sets defers_to_interpreter:
true to assert the phrase correctly falls through to the LLM interpreter
(content-bearing free text the no-LLM tiers should not guess at).

Default intents glob: <app-dir>/intents/*.yaml

Exit codes:
  0  every fixture's routing outcome matched its expectation
  1  at least one fixture mismatched
  2  fatal startup error`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			if intentsGlob == "" {
				intentsGlob = defaultIntentsGlob(appPath)
			}

			opts := testrunner.RoutingOptions{
				Glob:           intentsGlob,
				OnlyState:      onlyState,
				ImportResolver: buildImportResolver(),
			}

			report, err := testrunner.RunRoutingFixtures(context.Background(), appPath, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kitsoki test routing: %v\n", err)
				os.Exit(2)
			}

			testrunner.PrintRoutingReport(report)

			if report.TotalFailed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&intentsGlob, "intents", "",
		"glob for intent fixture files (default: <app-dir>/intents/*.yaml)")
	cmd.Flags().StringVar(&onlyState, "only", "",
		"filter to only this state (exact match)")

	return cmd
}
