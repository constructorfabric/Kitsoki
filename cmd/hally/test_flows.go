package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"hally/internal/testrunner"
)

// testFlowsCmd implements `hally test flows`.
//
// Default flow glob: if <app.yaml> lives at testdata/apps/cloak/app.yaml,
// the default glob is testdata/apps/cloak/flows/*.yaml. If the directory
// does not contain a flows/ subdirectory, ./flows/*.yaml is tried next.
// Use --flows to override.
func testFlowsCmd() *cobra.Command {
	var (
		flowsGlob   string
		jsonOut     string
		oraclePath  string
		failFast    bool
		verbose     bool
		allowMissOracle bool
	)

	cmd := &cobra.Command{
		Use:   "flows <app.yaml>",
		Short: "Run Mode 2 deterministic flow tests (no LLM, no cost)",
		Long: `Run all flow fixture files against the app's state machine.

Each flow fixture is a YAML file with test_kind: flow. It declares an
initial state, a sequence of turns (each supplying either a structured
intent or a free-text input resolved via the oracle), and assertions
checked after each turn.

Default flow glob: <app-dir>/flows/*.yaml
Override with --flows.

Exit codes:
  0  all flows pass
  1  one or more flows fail
  2  fatal startup error (bad app YAML, bad glob, etc.)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			// Resolve default glob.
			if flowsGlob == "" {
				flowsGlob = defaultFlowsGlob(appPath)
			}

			opts := testrunner.FlowOptions{
				OracleOverride:     oraclePath,
				AllowMissingOracle: allowMissOracle,
				FailFast:           failFast,
				Verbose:            verbose,
				JSONOut:            jsonOut,
			}

			ctx := context.Background()
			report, err := testrunner.RunFlows(ctx, appPath, flowsGlob, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "hally test flows: %v\n", err)
				os.Exit(2)
			}

			testrunner.PrintFlowReport(report)

			if report.Failed > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&flowsGlob, "flows", "",
		"glob for flow fixture files (default: <app-dir>/flows/*.yaml)")
	cmd.Flags().StringVar(&jsonOut, "json", "",
		"write JSON report to this file")
	cmd.Flags().StringVar(&oraclePath, "oracle", "",
		"override the oracle path declared in fixture files")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false,
		"stop at first flow failure")
	cmd.Flags().BoolVar(&verbose, "v", false,
		"verbose per-turn output")
	cmd.Flags().BoolVar(&allowMissOracle, "allow-missing-oracle", false,
		"treat oracle misses as skips rather than failures")

	return cmd
}

// defaultFlowsGlob returns the glob to use when --flows is not set.
// Tries <app-dir>/flows/*.yaml first, then ./flows/*.yaml.
func defaultFlowsGlob(appPath string) string {
	appDir := filepath.Dir(appPath)
	candidate := filepath.Join(appDir, "flows", "*.yaml")
	// Check if the candidate directory exists.
	dir := filepath.Join(appDir, "flows")
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return candidate
	}
	// Try cwd.
	if info, err := os.Stat("flows"); err == nil && info.IsDir() {
		return filepath.Join("flows", "*.yaml")
	}
	// Fall back to the app dir itself.
	return strings.TrimSuffix(appPath, filepath.Ext(appPath)) + "-flows/*.yaml"
}
