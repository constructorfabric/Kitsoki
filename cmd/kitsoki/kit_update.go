// `kitsoki kit update` / `kitsoki kit reject` — the S7 staging half of the
// kit lifecycle. update re-resolves a locked kit, gates the candidate on the
// recorded semver constraint, and STAGES it (kits.staged.lock +
// .kitsoki/kit-update/<name>/plan.yaml) without touching the accepted
// lockfile; reject removes the staged candidate residue-free. `kit trial`
// judges a staged candidate and `kit accept` promotes it (later slices).
package main

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"kitsoki/internal/kitstage"
)

func kitUpdateCmd() *cobra.Command {
	var (
		target    string
		to        string
		source    string
		checkOnly bool
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Re-resolve a locked kit and stage the result as an update candidate",
		Long: `Re-resolves a locked kit's source, checks the result against the version
constraint recorded at 'kit add --version' time (overridable with --to),
and stages the resolution as an update CANDIDATE:

  .kitsoki/kits.staged.lock            the pinned candidate (accepted
                                       kits.lock is not touched)
  .kitsoki/kit-update/<name>/plan.yaml the upgrade plan: from/to versions,
                                       changed files, and the candidate's
                                       compat.renamed hints annotated with
                                       the local references they affect

Nothing resolves against the candidate unless asked: pass --staged (any
command) to resolve staged kits, run 'kit trial <name>' to judge the
candidate, then 'kit accept <name>' to promote it into kits.lock or
'kit reject <name>' to drop it.

For a git-sourced kit, --to rewrites the source's @ref (tag/branch/commit);
for every other tier --to is a version constraint the candidate must
satisfy (bare versions mean exact match). --source replaces the source
string entirely, e.g. to migrate a kit from @kitsoki/<name> to a git
remote.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			targetAbs, err := absTarget(target)
			if err != nil {
				return err
			}
			// The staging engine lives in internal/kitstage (shared with the
			// studio MCP kit.update tool); the CLI contributes its resolver
			// seam and the operator-facing rendering.
			res, err := kitstage.Update(cmd.Context(), kitstage.UpdateOptions{
				ProjectRoot: targetAbs,
				Name:        name,
				To:          to,
				Source:      source,
				CheckOnly:   checkOnly,
				Resolve:     resolveKitEntry,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if res.UpToDate {
				fmt.Fprintf(out, "%s is already up to date (%s, tree %s) — nothing staged\n",
					name, displayVersion(res.Locked.Version), shortHash(res.Locked.TreeHash))
				return nil
			}
			printUpdatePlan(out, res.Plan, jsonOut)
			if checkOnly {
				fmt.Fprintln(out, "\n--check-only: nothing staged")
				return nil
			}
			if !jsonOut {
				fmt.Fprintf(out, "\nstaged: %s\nplan:   %s\nnext:   kitsoki kit trial %s   (judge)   ·   kitsoki kit reject %s   (drop)\n",
					kitstage.Path(targetAbs), res.PlanPath, name, name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root the lockfile is read from")
	cmd.Flags().StringVar(&to, "to", "", "git tier: new @ref for the source; other tiers: version constraint the candidate must satisfy (bare version = exact)")
	cmd.Flags().StringVar(&source, "source", "", "replace the kit's source string entirely (e.g. migrate to a git+ remote)")
	cmd.Flags().BoolVar(&checkOnly, "check-only", false, "resolve and print the plan without staging anything")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the plan as JSON instead of plain text")
	return cmd
}

func kitRejectCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "reject <name>",
		Short: "Drop a staged update candidate (kits.staged.lock entry + kit-update workdir)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			targetAbs, err := absTarget(target)
			if err != nil {
				return err
			}
			f, err := kitstage.Load(kitstage.Path(targetAbs))
			if err != nil {
				return err
			}
			entry := f.Kits[name]
			if entry == nil {
				return fmt.Errorf("kit %q has no staged candidate under %s", name, targetAbs)
			}
			if err := kitstage.Remove(targetAbs, name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "rejected staged %s@%s (accepted resolution unchanged)\n", name, displayVersion(entry.Version))
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root the staged lockfile is read from")
	return cmd
}

// printUpdatePlan renders a kitstage.Plan for operators (or as JSON).
func printUpdatePlan(out interface{ Write([]byte) (int, error) }, plan *kitstage.Plan, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(plan)
		return
	}
	fmt.Fprintf(out, "update plan for %s\n", plan.Kit)
	fmt.Fprintf(out, "  from: %s (tree %s)\n", displayVersion(plan.From.Version), shortHash(plan.From.TreeHash))
	fmt.Fprintf(out, "  to:   %s (tree %s)\n", displayVersion(plan.To.Version), shortHash(plan.To.TreeHash))
	switch {
	case plan.ChangedFilesNote != "":
		fmt.Fprintf(out, "  changed files: %s\n", plan.ChangedFilesNote)
	case len(plan.ChangedFiles) == 0:
		fmt.Fprintln(out, "  changed files: none")
	default:
		fmt.Fprintf(out, "  changed files (%d):\n", len(plan.ChangedFiles))
		for _, c := range plan.ChangedFiles {
			fmt.Fprintf(out, "    %-8s %s\n", c.Kind, c.Path)
		}
	}
	if len(plan.RenameHints) > 0 {
		fmt.Fprintln(out, "  rename hints (compat.renamed):")
		for _, h := range plan.RenameHints {
			if h.Detected != "" {
				fmt.Fprintf(out, "    ! %s: %s -> %s — %s\n", h.Category, h.Old, h.New, h.SuggestedAction)
			} else {
				fmt.Fprintf(out, "    · %s: %s -> %s (declared upstream, not detected locally)\n", h.Category, h.Old, h.New)
			}
		}
	}
}

// shortHash abbreviates a tree/commit hash for display.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	if h == "" {
		return "(none)"
	}
	return h
}
