package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/projectprofile"
)

func projectProfileCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project-profile",
		Short: "Inspect and validate Kitsoki project profiles",
	}
	cmd.AddCommand(projectProfileValidateCmd())
	return cmd
}

func projectProfileValidateCmd() *cobra.Command {
	var repoRoot string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:          "validate <project-profile.yaml>",
		Short:        "Validate a project-profile/v1 file with JSON Schema and semantic checks",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := projectprofile.ValidateFile(args[0], repoRoot)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(res); err != nil {
					return err
				}
			} else {
				printProjectProfileValidation(cmd, res)
			}
			if !res.OK {
				return fmt.Errorf("project profile validation failed")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repoRoot, "repo-root", "", "project checkout root for semantic validation (default: profile directory)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit machine-readable JSON")
	return cmd
}

func printProjectProfileValidation(cmd *cobra.Command, res projectprofile.Result) {
	out := cmd.OutOrStdout()
	if res.OK {
		fmt.Fprintln(out, "project profile: valid")
	} else {
		fmt.Fprintln(out, "project profile: invalid")
	}
	printValidationGroup(out, "schema", res.Schema)
	printValidationGroup(out, "semantic", res.Semantic)
	printValidationGroup(out, "warnings", res.Warnings)
}

func printValidationGroup(out io.Writer, name string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s:\n", name)
	for _, item := range items {
		fmt.Fprintf(out, "  - %s\n", strings.TrimSpace(item))
	}
}
