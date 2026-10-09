package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/taskcase"
)

func historyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Inspect repo-history training material",
	}
	cmd.AddCommand(historyTaskCasesCmd())
	return cmd
}

func historyTaskCasesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task-cases",
		Short: "Validate and adapt history_task.v1 manifests",
	}
	cmd.AddCommand(historyTaskCasesValidateCmd())
	cmd.AddCommand(historyTaskCasesAdaptBugfixCmd())
	return cmd
}

func historyTaskCasesValidateCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "validate <task-case.yaml|dir>",
		Short: "Validate history_task.v1 manifests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cases, err := taskcase.LoadAll(args[0])
			if err != nil {
				return err
			}
			results := make([]taskcase.ValidationResult, 0, len(cases))
			ok := true
			for _, c := range cases {
				result := taskcase.Validate(c)
				results = append(results, result)
				if !result.OK() {
					ok = false
				}
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(map[string]any{"ok": ok, "results": results}); err != nil {
					return err
				}
			} else {
				for _, result := range results {
					id := "<unknown>"
					if result.Case != nil {
						id = result.Case.ID
					}
					status := "OK"
					if !result.OK() {
						status = "ERROR"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", status, id)
					for _, warning := range result.Warnings {
						fmt.Fprintf(cmd.OutOrStdout(), "WARN: %s\n", warning)
					}
					for _, issue := range result.Errors {
						fmt.Fprintf(cmd.OutOrStdout(), "ERROR: %s\n", issue)
					}
				}
			}
			if !ok {
				return fmt.Errorf("history task-case validation failed")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable JSON output")
	return cmd
}

func historyTaskCasesAdaptBugfixCmd() *cobra.Command {
	var bugs string
	var validate bool
	cmd := &cobra.Command{
		Use:   "adapt-bugfix <bugfix-manifest.yaml>",
		Short: "Render bugfix-bakeoff rows as history_task.v1 cases",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cases, err := taskcase.LoadBugfixManifest(args[0], splitNonEmpty(bugs))
			if err != nil {
				return err
			}
			if validate {
				var errors []string
				for i := range cases {
					result := taskcase.Validate(&cases[i])
					for _, issue := range result.Errors {
						errors = append(errors, cases[i].ID+": "+issue)
					}
				}
				if len(errors) > 0 {
					return fmt.Errorf("adapted case validation failed: %s", strings.Join(errors, "; "))
				}
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(map[string]any{"kind": "history_task_set.v1", "cases": cases})
		},
	}
	cmd.Flags().StringVar(&bugs, "bug", "", "comma-separated bug ids to adapt")
	cmd.Flags().BoolVar(&validate, "validate", false, "validate adapted cases before printing")
	return cmd
}

func splitNonEmpty(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
