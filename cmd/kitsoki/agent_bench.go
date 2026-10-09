package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/agentbench"
)

func agentBenchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "agent-bench",
		Short:        "Score provider-backed agent tasks against deterministic budgets",
		SilenceUsage: true,
		Long: `Score provider-backed agent tasks against deterministic budgets.

The default score command is offline and CI-safe: it reads a trace JSONL file,
extracts cost, token, tool, submit, and state metrics, then compares them with
the manifest's budgets and expectations. The run command executes a manifest
command only when --live is passed.`,
	}
	cmd.AddCommand(agentBenchScoreCmd())
	cmd.AddCommand(agentBenchRunCmd())
	return cmd
}

func agentBenchScoreCmd() *cobra.Command {
	var caseID string
	var trace string
	var jsonOut string
	var markdownOut string
	var slideyOut string
	var envelope bool
	cmd := &cobra.Command{
		Use:          "score <bench.yaml>",
		Short:        "Score an existing trace without calling a provider",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if envelope {
				return runAgentBenchScoreEnvelope(cmd, args[0], agentBenchScoreOptions{
					CaseID:      caseID,
					Trace:       trace,
					JSONOut:     jsonOut,
					MarkdownOut: markdownOut,
					SlideyOut:   slideyOut,
				})
			}
			report, err := agentbench.ScoreManifestCase(args[0], caseID, trace)
			if err != nil {
				return err
			}
			if err := writeAgentBenchReportArtifacts(agentBenchArtifactOptions{
				JSONOut:     jsonOut,
				MarkdownOut: markdownOut,
				SlideyOut:   slideyOut,
				JSONPayload: report,
				Report:      report,
			}); err != nil {
				return err
			}
			if err := printAgentBenchReport(cmd, report); err != nil {
				return err
			}
			if !report.Passed {
				return fmt.Errorf("agent bench failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&caseID, "case", "", "case id to score; optional for single-case manifests")
	cmd.Flags().StringVar(&trace, "trace", "", "override trace path")
	cmd.Flags().StringVar(&jsonOut, "json-out", "", "write machine-readable report JSON")
	cmd.Flags().StringVar(&markdownOut, "markdown-out", "", "write reviewable report Markdown")
	cmd.Flags().StringVar(&slideyOut, "slidey-out", "", "write a Slidey JSON report deck")
	cmd.Flags().BoolVar(&envelope, "envelope", false, "emit a final JSON status envelope for host.run bindings and return zero")
	return cmd
}

type agentBenchScoreOptions struct {
	CaseID      string
	Trace       string
	JSONOut     string
	MarkdownOut string
	SlideyOut   string
}

func runAgentBenchScoreEnvelope(cmd *cobra.Command, manifest string, opts agentBenchScoreOptions) error {
	report, err := agentbench.ScoreManifestCase(manifest, opts.CaseID, opts.Trace)
	if err != nil {
		return printAgentBenchScoreEnvelope(cmd, map[string]any{
			"status":          "failed",
			"summary":         "failed",
			"error":           err.Error(),
			"stdout":          "",
			"report_json":     opts.JSONOut,
			"report_markdown": opts.MarkdownOut,
			"report_deck":     opts.SlideyOut,
			"exit_code":       1,
		})
	}
	if err := writeAgentBenchReportArtifacts(agentBenchArtifactOptions{
		JSONOut:     opts.JSONOut,
		MarkdownOut: opts.MarkdownOut,
		SlideyOut:   opts.SlideyOut,
		JSONPayload: report,
		Report:      report,
	}); err != nil {
		return printAgentBenchScoreEnvelope(cmd, map[string]any{
			"status":          "failed",
			"summary":         "failed",
			"error":           err.Error(),
			"stdout":          "",
			"report_json":     opts.JSONOut,
			"report_markdown": opts.MarkdownOut,
			"report_deck":     opts.SlideyOut,
			"exit_code":       1,
		})
	}

	stdout := agentBenchReportText(report)
	fmt.Fprint(cmd.OutOrStdout(), stdout)
	status := "failed"
	exitCode := 1
	if report.Passed {
		status = "passed"
		exitCode = 0
	}
	summary := firstNonEmptyAgentBenchLine(stdout)
	if summary == "" {
		summary = status
	}
	return printAgentBenchScoreEnvelope(cmd, map[string]any{
		"status":          status,
		"summary":         summary,
		"error":           strings.Join(report.Failures, "\n"),
		"stdout":          stdout,
		"report_json":     opts.JSONOut,
		"report_markdown": opts.MarkdownOut,
		"report_deck":     opts.SlideyOut,
		"exit_code":       exitCode,
	})
}

func printAgentBenchScoreEnvelope(cmd *cobra.Command, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}

func agentBenchRunCmd() *cobra.Command {
	var caseID string
	var trace string
	var jsonOut string
	var markdownOut string
	var slideyOut string
	var live bool
	cmd := &cobra.Command{
		Use:          "run <bench.yaml>",
		Short:        "Run a manifest command, then score its trace",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := agentbench.RunManifestCase(agentbench.RunOptions{
				ManifestPath: args[0],
				CaseID:       caseID,
				Trace:        trace,
				Live:         live,
			})
			if err != nil {
				return err
			}
			if err := writeAgentBenchReportArtifacts(agentBenchArtifactOptions{
				JSONOut:     jsonOut,
				MarkdownOut: markdownOut,
				SlideyOut:   slideyOut,
				JSONPayload: report,
				Report:      report.Report,
			}); err != nil {
				return err
			}
			if report.Stdout != "" {
				fmt.Fprint(cmd.OutOrStdout(), report.Stdout)
			}
			if report.Stderr != "" {
				fmt.Fprint(cmd.ErrOrStderr(), report.Stderr)
			}
			if err := printAgentBenchReport(cmd, report.Report); err != nil {
				return err
			}
			if !report.Passed {
				return fmt.Errorf("agent bench failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&caseID, "case", "", "case id to run; optional for single-case manifests")
	cmd.Flags().StringVar(&trace, "trace", "", "override trace path")
	cmd.Flags().StringVar(&jsonOut, "json-out", "", "write machine-readable report JSON")
	cmd.Flags().StringVar(&markdownOut, "markdown-out", "", "write reviewable report Markdown")
	cmd.Flags().StringVar(&slideyOut, "slidey-out", "", "write a Slidey JSON report deck")
	cmd.Flags().BoolVar(&live, "live", false, "execute the manifest command; may call live providers")
	return cmd
}

type agentBenchArtifactOptions struct {
	JSONOut     string
	MarkdownOut string
	SlideyOut   string
	JSONPayload any
	Report      agentbench.Report
}

func writeAgentBenchReportArtifacts(opts agentBenchArtifactOptions) error {
	if opts.JSONOut != "" {
		b, err := json.MarshalIndent(opts.JSONPayload, "", "  ")
		if err != nil {
			return err
		}
		if err := writeAgentBenchArtifact(opts.JSONOut, append(b, '\n')); err != nil {
			return err
		}
	}
	if opts.MarkdownOut != "" {
		if err := writeAgentBenchArtifact(opts.MarkdownOut, []byte(agentbench.MarkdownReport(opts.Report))); err != nil {
			return err
		}
	}
	if opts.SlideyOut != "" {
		b, err := agentbench.SlideyDeckJSON(opts.Report)
		if err != nil {
			return err
		}
		if err := writeAgentBenchArtifact(opts.SlideyOut, append(b, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func writeAgentBenchArtifact(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func printAgentBenchReport(cmd *cobra.Command, report agentbench.Report) error {
	fmt.Fprint(cmd.OutOrStdout(), agentBenchReportText(report))
	return nil
}

func agentBenchReportText(report agentbench.Report) string {
	var b strings.Builder
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	fmt.Fprintf(&b, "%s %s\n", status, report.CaseID)
	fmt.Fprintf(&b, "trace: %s\n", report.Trace)
	fmt.Fprintf(&b, "cost=$%.6f input=%d output=%d tools=%d reads=%d wall=%.3fs final=%s submit=%t\n",
		report.Metrics.CostUSD,
		report.Metrics.InputTokens,
		report.Metrics.OutputTokens,
		report.Metrics.ToolCallsTotal,
		report.Metrics.ReadCalls,
		report.Metrics.WallSeconds,
		report.Metrics.FinalState,
		report.Metrics.Submitted,
	)
	if report.Metrics.AgentCallsStarted > 0 || report.Metrics.AgentCallsInFlight > 0 {
		fmt.Fprintf(&b, "agent_calls started=%d finished=%d errored=%d in_flight=%d\n",
			report.Metrics.AgentCallsStarted,
			report.Metrics.AgentCallsFinished,
			report.Metrics.AgentCallsErrored,
			report.Metrics.AgentCallsInFlight,
		)
	}
	for _, failure := range report.Failures {
		fmt.Fprintf(&b, "ERROR: %s\n", failure)
	}
	return b.String()
}

func firstNonEmptyAgentBenchLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}
