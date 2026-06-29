package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
	cmd := &cobra.Command{
		Use:          "score <bench.yaml>",
		Short:        "Score an existing trace without calling a provider",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
	return cmd
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
	status := "FAIL"
	if report.Passed {
		status = "PASS"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", status, report.CaseID)
	fmt.Fprintf(cmd.OutOrStdout(), "trace: %s\n", report.Trace)
	fmt.Fprintf(cmd.OutOrStdout(), "cost=$%.6f input=%d output=%d tools=%d reads=%d wall=%.3fs final=%s submit=%t\n",
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
		fmt.Fprintf(cmd.OutOrStdout(), "agent_calls started=%d finished=%d errored=%d in_flight=%d\n",
			report.Metrics.AgentCallsStarted,
			report.Metrics.AgentCallsFinished,
			report.Metrics.AgentCallsErrored,
			report.Metrics.AgentCallsInFlight,
		)
	}
	for _, failure := range report.Failures {
		fmt.Fprintf(cmd.OutOrStdout(), "ERROR: %s\n", failure)
	}
	return nil
}
