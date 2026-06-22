package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/agenteval"
)

func evalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "eval",
		Short: "Inspect and validate story-local agent task evals",
		Long: `Inspect and validate story-local agent task evals.

Default commands are deterministic and do not call live LLM providers. Live
benchmark execution is gated behind --live and intentionally fails until a live
runner is configured.`,
	}
	cmd.AddCommand(evalListCmd())
	cmd.AddCommand(evalShowCmd())
	cmd.AddCommand(evalRunCmd())
	return cmd
}

func evalListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list <story-dir|eval.yaml>",
		Short: "List eval datasets and latest report status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, err := loadEvalRows(args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			for _, row := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", row.Call, row.Status, row.Comparator, row.Pin, row.Path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable JSON output")
	return cmd
}

func evalShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <eval.yaml|report.json>",
		Short: "Show one eval dataset or report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			if strings.HasSuffix(path, ".json") {
				report, err := agenteval.LoadReport(path)
				if err != nil {
					return err
				}
				if jsonOut {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(report)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "eval: %s\ncall: %s\nstatus candidates: %d\n", report.Eval, report.Call, len(report.Candidates))
				for _, c := range report.Candidates {
					marker := "fail"
					if c.Pass {
						marker = "pass"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "- %s/%s effort=%s %s pass_rate=%.2f cost=%.6f p95=%dms\n",
						c.Profile, c.Model, c.Effort, marker, c.ComparatorRate, c.AvgCostUSD, c.P95LatencyMS)
				}
				return nil
			}
			result, err := agenteval.ValidateDataset(path)
			if err != nil {
				return err
			}
			reports, err := agenteval.LoadReportsForDataset(result.Dataset)
			if err != nil {
				return err
			}
			latest := agenteval.LatestReport(reports)
			if jsonOut {
				payload := map[string]any{"dataset": result.Dataset, "call": result.Call, "errors": result.Errors, "warnings": result.Warns, "latest_report": latest}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(payload)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "call: %s\nhandler: %s\nstate: %s\nschema: %s\ncomparator: %s\nstatus: %s\n",
				result.Dataset.Call, result.Call.Handler, result.Call.StatePath, result.Call.SchemaPath,
				result.Dataset.Comparator.Kind, agenteval.ReportStatus(latest, result.Call))
			for _, warn := range result.Warns {
				fmt.Fprintf(cmd.OutOrStdout(), "WARN: %s\n", warn)
			}
			for _, issue := range result.Errors {
				fmt.Fprintf(cmd.OutOrStdout(), "ERROR: %s\n", issue)
			}
			if !result.OK() {
				return fmt.Errorf("eval validation failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable JSON output")
	return cmd
}

func evalRunCmd() *cobra.Command {
	var live bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "run <eval.yaml>",
		Short: "Validate an eval dataset, or run a gated live benchmark",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := agenteval.ValidateDataset(args[0])
			if err != nil {
				return err
			}
			if !result.OK() {
				for _, issue := range result.Errors {
					fmt.Fprintf(cmd.OutOrStdout(), "ERROR: %s\n", issue)
				}
				return fmt.Errorf("eval validation failed")
			}
			if !live {
				if jsonOut {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(map[string]any{"ok": true, "mode": "offline", "call": result.Call})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "OK offline eval contract: %s (%s)\n", result.Dataset.Call, result.Call.Handler)
				fmt.Fprintf(cmd.OutOrStdout(), "live benchmark not run; pass --live to use provider-backed harness profiles\n")
				return nil
			}
			return fmt.Errorf("live eval runner is gated but not implemented in this slice; no provider calls were made")
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "run live provider-backed benchmark matrix")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable JSON output")
	return cmd
}

type evalRow struct {
	Call       string `json:"call"`
	Status     string `json:"status"`
	Comparator string `json:"comparator"`
	Pin        string `json:"pin,omitempty"`
	Path       string `json:"path"`
}

func loadEvalRows(root string) ([]evalRow, error) {
	datasets, err := agenteval.LoadDatasets(root)
	if err != nil {
		return nil, err
	}
	rows := make([]evalRow, 0, len(datasets))
	for _, ds := range datasets {
		result, err := agenteval.ValidateDataset(ds.Path)
		if err != nil {
			return nil, err
		}
		reports, err := agenteval.LoadReportsForDataset(ds)
		if err != nil {
			return nil, err
		}
		status := "invalid"
		if result.OK() {
			status = agenteval.ReportStatus(agenteval.LatestReport(reports), result.Call)
		}
		pin := ""
		if ds.Selection.Pinned.Profile != "" {
			pin = ds.Selection.Pinned.Profile + "/" + ds.Selection.Pinned.Model
		}
		rows = append(rows, evalRow{
			Call:       ds.Call,
			Status:     status,
			Comparator: ds.Comparator.Kind,
			Pin:        pin,
			Path:       filepath.ToSlash(ds.Path),
		})
	}
	return rows, nil
}
