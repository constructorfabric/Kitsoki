package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"kitsoki/internal/testrunner"
)

func testFlowCoverageCmd() *cobra.Command {
	var (
		flowsGlobs                 []string
		jsonOut                    string
		minBranchCoverage          float64
		requireAllBranches         bool
		minEffectCoverage          float64
		requireAllEffects          bool
		requireHostAsserts         bool
		starlarkCoverage           bool
		minStarlarkStatements      float64
		minStarlarkBranches        float64
		requireAllStarlarkBranches bool
		requireRealStarlark        bool
		maxCombinations            int
	)

	cmd := &cobra.Command{
		Use:   "flow-coverage <app.yaml>",
		Short: "Report static coverage of story flow fixtures",
		Long: `Report which authored story transition branches are exercised by flow fixtures.

By default this is a static coverage ledger for fixture completeness. With
--starlark it also executes the deterministic flow fixtures with instrumented
host.starlark.run scripts, using the same cassettes and no LLM calls. Run
'kitsoki test flows' for behavioral correctness. A transition branch is credited
when a fixture turn submits the same state+intent and either the branch is
unambiguous or the turn pins an expect_state / expect_state_in that matches the
branch target.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]
			flowsGlob := strings.Join(flowsGlobs, ",")
			if flowsGlob == "" {
				flowsGlob = defaultFlowsGlob(appPath)
			}
			report, err := testrunner.RunFlowCoverage(context.Background(), appPath, testrunner.FlowCoverageOptions{
				FlowsGlob:                    flowsGlob,
				JSONOut:                      jsonOut,
				MinBranchCoverage:            minBranchCoverage,
				RequireAllBranches:           requireAllBranches,
				MinEffectCoverage:            minEffectCoverage,
				RequireAllEffects:            requireAllEffects,
				RequireHostAssertions:        requireHostAsserts,
				StarlarkCoverage:             starlarkCoverage,
				MinStarlarkStatementCoverage: minStarlarkStatements,
				MinStarlarkBranchCoverage:    minStarlarkBranches,
				RequireAllStarlarkBranches:   requireAllStarlarkBranches,
				RequireRealStarlark:          requireRealStarlark,
				MaxCombinations:              maxCombinations,
				ImportResolver:               buildImportResolver(),
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "kitsoki test flow-coverage: %v\n", err)
				os.Exit(2)
			}
			printFlowCoverageReport(report)
			if !report.Passed {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&flowsGlobs, "flows", nil, "glob for flow fixture files; may be repeated or comma-separated (default: <app-dir>/flows/*.yaml)")
	cmd.Flags().StringVar(&jsonOut, "json", "", "write JSON report to this file")
	cmd.Flags().Float64Var(&minBranchCoverage, "min-branch", 0, "minimum authored branch coverage percentage required to pass")
	cmd.Flags().BoolVar(&requireAllBranches, "require-all-branches", false, "fail when any authored transition branch is uncovered")
	cmd.Flags().Float64Var(&minEffectCoverage, "min-effect", 0, "minimum authored effect coverage percentage required to pass")
	cmd.Flags().BoolVar(&requireAllEffects, "require-all-effects", false, "fail when any authored effect is uncovered")
	cmd.Flags().BoolVar(&requireHostAsserts, "require-host-assertions", false, "fail when a covered invoke lacks expect_host_calls or a host cassette")
	cmd.Flags().BoolVar(&starlarkCoverage, "starlark", false, "run deterministic flow fixtures and report host.starlark.run statement/branch coverage")
	cmd.Flags().Float64Var(&minStarlarkStatements, "min-starlark", 0, "minimum Starlark statement coverage percentage required to pass; implies --starlark")
	cmd.Flags().Float64Var(&minStarlarkBranches, "min-starlark-branches", 0, "minimum Starlark branch-outcome coverage percentage required to pass; implies --starlark")
	cmd.Flags().BoolVar(&requireAllStarlarkBranches, "require-all-starlark-branches", false, "fail when any Starlark if/elif outcome is uncovered; implies --starlark")
	cmd.Flags().BoolVar(&requireRealStarlark, "require-real-starlark", false, "fail when covered host.starlark.run effects are handler-stubbed instead of running the real script; implies --starlark")
	cmd.Flags().IntVar(&maxCombinations, "max-combinations", 64, "maximum enum-slot combination product to expand per state+intent")
	return cmd
}

func printFlowCoverageReport(report *testrunner.FlowCoverageReport) {
	fmt.Printf("Story flow coverage for %s\n", report.App)
	fmt.Printf("Flows: %s\n\n", report.FlowsGlob)

	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintf(w, "STATUS\tSTATE\tINTENT\tBRANCH\tTARGET\n")
	for _, branch := range report.Branches {
		status := "MISS"
		if branch.Covered {
			status = "HIT"
		}
		target := branch.Target
		if branch.When != "" {
			target += " when " + branch.When
		} else if branch.Default {
			target += " default"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", status, branch.State, branch.Intent, branch.Index, target)
	}
	_ = w.Flush()

	if len(report.Effects) > 0 {
		fmt.Println("\nEffect coverage gaps:")
		w = tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintf(w, "STATUS\tASSERT\tSITE\tKIND\tINVOKE\n")
		for _, effect := range report.Effects {
			if effect.Covered && (effect.Invoke == "" || effect.HostAsserted) {
				continue
			}
			status := "MISS"
			if effect.Covered {
				status = "HIT"
			}
			assertStatus := ""
			if effect.Invoke != "" {
				assertStatus = "unasserted"
				if effect.HostAsserted {
					assertStatus = "asserted"
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", status, assertStatus, effect.ID, effect.Kind, effect.Invoke)
		}
		_ = w.Flush()
	}

	if len(report.ParameterChecks) > 0 {
		fmt.Println("\nParameter coverage gaps:")
		w = tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		for _, check := range report.ParameterChecks {
			if check.Passed {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t", check.State, check.Intent)
			if len(check.MissingValues) > 0 {
				fmt.Fprintf(w, "missing enum values: %v", check.MissingValues)
			}
			if len(check.MissingCombinations) > 0 {
				fmt.Fprintf(w, " missing combinations: %d", len(check.MissingCombinations))
			}
			if check.SkippedReason != "" {
				fmt.Fprintf(w, " combinations skipped: %s", check.SkippedReason)
			}
			fmt.Fprintln(w)
		}
		_ = w.Flush()
	}
	if len(report.StarlarkScripts) > 0 {
		fmt.Println("\nStarlark coverage gaps:")
		w = tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintf(w, "STATUS\tSCRIPT\tLINE\tKIND\tDETAIL\n")
		for _, script := range report.StarlarkScripts {
			for _, stmt := range script.Statements {
				if stmt.Covered {
					continue
				}
				fmt.Fprintf(w, "MISS\t%s\t%d\t%s\t%s\n", filepath.Base(script.Script), stmt.Line, stmt.Kind, stmt.Function)
			}
			for _, branch := range script.Branches {
				if branch.TrueHits == 0 {
					fmt.Fprintf(w, "MISS\t%s\t%d\tbranch\ttrue outcome%s\n", filepath.Base(script.Script), branch.Line, functionSuffix(branch.Function))
				}
				if branch.FalseHits == 0 {
					fmt.Fprintf(w, "MISS\t%s\t%d\tbranch\tfalse outcome%s\n", filepath.Base(script.Script), branch.Line, functionSuffix(branch.Function))
				}
			}
		}
		_ = w.Flush()
	}

	if len(report.Problems) > 0 {
		fmt.Println("\nProblems:")
		for _, problem := range report.Problems {
			fmt.Printf("  - %s\n", problem)
		}
	}

	total := report.BranchCoverage.Total
	fmt.Printf("\nSummary: %d/%d authored branches covered (%.1f%%)\n",
		report.BranchCoverage.Covered, total, report.BranchCoverage.Percent)
	fmt.Printf("Effects: %d/%d authored effects covered (%.1f%%)\n",
		report.EffectCoverage.Covered, report.EffectCoverage.Total, report.EffectCoverage.Percent)
	if report.StarlarkStatementCoverage.Total > 0 || report.StarlarkBranchCoverage.Total > 0 {
		fmt.Printf("Starlark statements: %d/%d covered (%.1f%%)\n",
			report.StarlarkStatementCoverage.Covered, report.StarlarkStatementCoverage.Total, report.StarlarkStatementCoverage.Percent)
		fmt.Printf("Starlark branches: %d/%d outcomes covered (%.1f%%)\n",
			report.StarlarkBranchCoverage.Covered, report.StarlarkBranchCoverage.Total, report.StarlarkBranchCoverage.Percent)
	}
	if report.Passed {
		fmt.Println("PASS")
	} else {
		fmt.Println("FAIL")
	}
	if report.BranchCoverage.Covered != total {
		fmt.Printf("Tip: add expect_state to ambiguous guarded turns so coverage can prove the branch, then inspect %s with --json for CI artifacts.\n", filepath.Base(report.AppPath))
	}
}

func functionSuffix(fn string) string {
	if fn == "" {
		return ""
	}
	return " in " + fn
}
