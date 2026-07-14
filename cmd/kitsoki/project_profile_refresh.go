package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
)

func projectProfileRefreshCmd() *cobra.Command {
	var target string
	var apply bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:          "refresh",
		Short:        "Re-discover and safely merge managed project-profile fields",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		Long: `Re-run deterministic onboarding discovery for an existing project.

The command is a dry run unless --apply is passed. It refreshes fields whose
onboarding.resolutions entry is discovered/default, preserves source: operator
values (and legacy values that differ from their last managed value), validates
the merged profile, and never overwrites the project-owned story wrapper. Base
story updates arrive through the wrapper's @kitsoki/dev-story import.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report, err := host.RefreshDevOnboardingProfile(target, apply)
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(report)
			}
			if report["ok"] != true {
				fmt.Fprintln(cmd.OutOrStdout(), "project profile refresh: invalid candidate")
				return fmt.Errorf("refreshed project profile did not validate")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "project profile: %s\n", report["profile_path"])
			fmt.Fprintf(cmd.OutOrStdout(), "changed: %v\n", report["changed"])
			fmt.Fprintf(cmd.OutOrStdout(), "applied: %v\n", report["applied"])
			if values, ok := report["preserved_overrides"].([]any); ok && len(values) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "preserved operator fields:")
				for _, value := range values {
					fmt.Fprintf(cmd.OutOrStdout(), "  - %v\n", value)
				}
			}
			if values, ok := report["defaults_used"].([]any); ok && len(values) > 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "defaults requiring review:")
				for _, value := range values {
					entry, _ := value.(map[string]any)
					fmt.Fprintf(cmd.OutOrStdout(), "  - %v: %v (%v)\n", entry["field"], entry["value"], entry["update"])
				}
			}
			if !apply && report["changed"] == true {
				fmt.Fprintln(cmd.OutOrStdout(), "run again with --apply after reviewing the candidate")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project checkout containing .kitsoki/project-profile.yaml")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the validated merged profile")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the complete candidate and merge report as JSON")
	return cmd
}
