package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/hygiene"
)

func capsuleCleanupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "cleanup", Short: "Plan and apply local Capsule disk hygiene"}
	cmd.AddCommand(capsuleCleanupPlanCmd(), capsuleCleanupApplyCmd(), capsuleCleanupClearCmd())
	return cmd
}

const capsuleClearInactiveCooloff = 5 * time.Minute

// capsuleCleanupClearCmd is deliberately parameterless: it is the safe
// high-water-mark cleanup for merged, clean, inactive development capsules.
func capsuleCleanupClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "clear",
		Short:        "Remove all clean merged capsules inactive for at least five minutes",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := hygiene.Apply(cmd.Context(), capsuleClearInactiveOptions("."))
			if err != nil {
				return err
			}
			return capsuleCleanupWrite(cmd, result, false)
		},
	}
}

func capsuleClearInactiveOptions(project string) hygiene.Options {
	return hygiene.Options{
		ProjectRoot:           project,
		KeepRuns:              -1,
		KeepWorkspaces:        -1,
		MinWorkspaceAge:       capsuleClearInactiveCooloff,
		ClearInactiveMerged:   true,
		MeasureWorkspaceBytes: false,
	}
}

type capsuleCleanupFlags struct {
	project               string
	keepRuns              int
	keepWorkspaces        int
	workspaceMinAge       time.Duration
	minFreeBytes          int64
	measureWorkspaceBytes bool
	pinnedWorkspaces      []string
	includeCapsuleCache   bool
	includeGoBuildCache   bool
	jsonOut               bool
}

func (f *capsuleCleanupFlags) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.project, "project", ".", "project root")
	cmd.Flags().IntVar(&f.keepRuns, "keep-runs", 20, "number of newest Capsule CI run records to retain")
	cmd.Flags().IntVar(&f.keepWorkspaces, "keep-workspaces", 5, "number of newest clean terminal managed workspaces to retain")
	cmd.Flags().DurationVar(&f.workspaceMinAge, "workspace-min-age", 24*time.Hour, "minimum age before a clean terminal managed workspace is reclaimable")
	cmd.Flags().Int64Var(&f.minFreeBytes, "min-free-bytes", 10<<30, "report disk pressure when project filesystem free space is below this floor")
	cmd.Flags().BoolVar(&f.measureWorkspaceBytes, "measure-workspace-bytes", false, "walk workspace contents to estimate reclaimable bytes (slower)")
	cmd.Flags().StringSliceVar(&f.pinnedWorkspaces, "pin-workspace", nil, "workspace id to retain for investigation or reuse (repeatable)")
	cmd.Flags().BoolVar(&f.includeCapsuleCache, "include-capsule-cache", false, "include project .capsules/cache entries")
	cmd.Flags().BoolVar(&f.includeGoBuildCache, "include-go-build-cache", false, "include the Go build/test cache via go clean")
	cmd.Flags().BoolVar(&f.jsonOut, "json", true, "print JSON")
}

func (f capsuleCleanupFlags) options() hygiene.Options {
	return hygiene.Options{
		ProjectRoot:           f.project,
		KeepRuns:              f.keepRuns,
		KeepWorkspaces:        f.keepWorkspaces,
		MinWorkspaceAge:       f.workspaceMinAge,
		MinFreeBytes:          f.minFreeBytes,
		MeasureWorkspaceBytes: f.measureWorkspaceBytes,
		PinnedWorkspaceIDs:    append([]string(nil), f.pinnedWorkspaces...),
		IncludeCapsuleCache:   f.includeCapsuleCache,
		IncludeGoBuildCache:   f.includeGoBuildCache,
	}
}

func capsuleCleanupPlanCmd() *cobra.Command {
	var flags capsuleCleanupFlags
	cmd := &cobra.Command{Use: "plan", Short: "Show reclaimable Capsule-managed disk state without deleting it", RunE: func(cmd *cobra.Command, args []string) error {
		plan, err := hygiene.BuildPlan(cmd.Context(), flags.options())
		if err != nil {
			return err
		}
		return capsuleCleanupWrite(cmd, plan, flags.jsonOut)
	}}
	flags.bind(cmd)
	return cmd
}

func capsuleCleanupApplyCmd() *cobra.Command {
	var flags capsuleCleanupFlags
	cmd := &cobra.Command{Use: "apply", Short: "Apply a local Capsule cleanup plan", RunE: func(cmd *cobra.Command, args []string) error {
		result, err := hygiene.Apply(cmd.Context(), flags.options())
		if err != nil {
			return err
		}
		return capsuleCleanupWrite(cmd, result, flags.jsonOut)
	}}
	flags.bind(cmd)
	return cmd
}

func capsuleCleanupWrite(cmd *cobra.Command, value any, jsonOut bool) error {
	if jsonOut {
		return capsuleWorkspaceWrite(cmd, value, true)
	}
	switch v := value.(type) {
	case hygiene.Plan:
		fmt.Fprintf(cmd.OutOrStdout(), "cleanup inventory: %d entries (%d measured bytes, %d unmeasured), reclaimable measured bytes: %d\n", len(v.Candidates), v.InventoryBytes, v.Unmeasured, v.TotalBytes)
		if v.Disk.Known {
			fmt.Fprintf(cmd.OutOrStdout(), "disk free: %d bytes, projected after cleanup: %d bytes, floor: %d bytes, pressure: %t\n", v.Disk.FreeBytes, v.Disk.ProjectedFreeBytes, v.Disk.MinFreeBytes, v.Disk.BelowMinimum)
		} else if v.DiskError != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "disk usage unavailable: %s\n", v.DiskError)
		}
	case hygiene.ApplyResult:
		fmt.Fprintf(cmd.OutOrStdout(), "cleanup removed: %d (%d bytes), skipped after recheck: %d, concurrent cleanup tolerated: %d\n", len(v.Removed), v.TotalBytes, len(v.Skipped), len(v.Tolerated))
	default:
		fmt.Fprintln(cmd.OutOrStdout(), value)
	}
	return nil
}
