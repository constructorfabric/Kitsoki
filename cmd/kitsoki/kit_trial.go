// `kitsoki kit trial` / `kitsoki kit accept` — the S7 judging half of the
// kit lifecycle (kit_update.go is the staging half). trial runs the
// acceptance gates against a staged candidate and leaves a receipt +
// migration worklist; accept promotes the candidate into kits.lock,
// fail-closed on the receipt's digests.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitdev"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/kittrial"
	"kitsoki/internal/kitverify"
	"kitsoki/internal/testrunner"
)

func kitTrialCmd() *cobra.Command {
	var (
		target   string
		jsonOut  bool
		failFast bool
		verbose  bool
	)
	cmd := &cobra.Command{
		Use:   "trial <name>",
		Short: "Judge a staged kit update: contract, frozen replay, onboarding, and ledger gates",
		Long: `Runs the acceptance gates against the STAGED candidate for <name>
(kitsoki kit update stages one):

  contract       kit verify against the staged tree + staged-resolved load
                 of every consumer instance
  frozen_replay  every consumer flow fixture replayed against BOTH the
                 locked and the staged tree — the fixture's assertions
                 decide, the route diff explains; a fixture that already
                 fails at the locked version is reported as stale, not
                 blamed on the upgrade
  onboarding     the idempotent validation sweep (project checks, profile
                 schema, no-side-effects invariant)
  baseline_live  the validation-ledger consult: already-validated cases
                 skip; no-cost oracles validate by replay; live oracles
                 queue for operator approval

The whole run is replay-strict (KITSOKI_CASSETTE_STRICT): a cassette miss
fails closed, recording is forbidden, and the receipt reports MEASURED
per-gate spend. Failures land in .kitsoki/kit-update/<name>/worklist.yaml
with stable ids; operator edits (status: accepted waivers, notes) survive
re-trials.

Exit codes: 0 trial ready or partial (pending approvals / warnings only),
1 blocked (open error items — kit accept will refuse), 2 fatal.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			targetAbs, err := absTarget(target)
			if err != nil {
				return err
			}

			// Replay-strict for the whole trial process tree: gates must
			// never record or spend silently.
			prevStrict, hadStrict := os.LookupEnv("KITSOKI_CASSETTE_STRICT")
			_ = os.Setenv("KITSOKI_CASSETTE_STRICT", "1")
			defer func() {
				if hadStrict {
					_ = os.Setenv("KITSOKI_CASSETTE_STRICT", prevStrict)
				} else {
					_ = os.Unsetenv("KITSOKI_CASSETTE_STRICT")
				}
			}()

			cmd.SilenceUsage = true
			report, err := kittrial.Run(cmd.Context(), kittrial.Options{
				ProjectRoot:  targetAbs,
				KitName:      name,
				BaseResolver: buildImportResolver(),
				Resolvers:    []app.ImportResolver{buildImportResolver(), buildPlainImportResolver()},
				Flow:         testrunner.FlowOptions{FailFast: failFast, Verbose: verbose},
				Extends:      lockfileExtendsResolver(cmd.Context(), targetAbs),
				ProjectChecks: func(ctx context.Context, resolver app.ImportResolver) ([]kittrial.Check, error) {
					rep, err := checkProjectUpgrade(ctx, projectUpgradeOptions{Target: targetAbs, Resolver: resolver})
					if err != nil {
						return nil, err
					}
					checks := make([]kittrial.Check, 0, len(rep.Checks))
					for _, c := range rep.Checks {
						checks = append(checks, kittrial.Check{ID: c.ID, Status: c.Status, Detail: c.Detail})
					}
					return checks, nil
				},
				Progress: cmd.ErrOrStderr(),
			})
			if err != nil {
				return fmt.Errorf("kit trial: %w", err)
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return err
				}
			} else {
				printTrialReport(out, targetAbs, report)
			}
			if report.Result == kittrial.ResultBlocked {
				return fmt.Errorf("trial blocked: %d open error item(s) in %s", report.Worklist.OpenErrors(), relOrAbs(targetAbs, report.WorklistPath))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root the staged lockfile is read from")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the trial report as JSON")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "stop each fixture replay at its first failure")
	cmd.Flags().BoolVar(&verbose, "v", false, "verbose per-turn flow output")
	return cmd
}

func kitAcceptCmd() *cobra.Command {
	var (
		target       string
		force        bool
		allowPartial bool
	)
	cmd := &cobra.Command{
		Use:   "accept <name>",
		Short: "Promote a staged kit update into kits.lock (fail-closed on the trial receipt)",
		Long: `Promotes the staged candidate for <name> into the accepted lockfile.

Fail-closed unless --force: a trial receipt must exist for the EXACT staged
tree, its result must not be blocked (partial additionally needs
--allow-partial, which is recorded into the acceptance receipt), and every
source digest it pinned (lockfiles, profile, instances, worklist) must
still match — any drift means the trial no longer describes reality and
must be re-run.

Accept writes the durable acceptance receipt to
.kitsoki/receipts/kit-update/<name>@<version>.json (committed — it travels
with the lock it justifies), updates kits.lock, and removes the staging
entry + workdir.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			targetAbs, err := absTarget(target)
			if err != nil {
				return err
			}
			// The fail-closed promotion engine lives in internal/kittrial
			// (shared with the studio MCP kit.accept tool); the CLI
			// contributes only the operator-facing rendering.
			outcome, err := kittrial.Accept(kittrial.AcceptOptions{
				ProjectRoot:  targetAbs,
				KitName:      name,
				Force:        force,
				AllowPartial: allowPartial,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "accepted %s@%s (tree %s)\n", name, displayVersion(outcome.Staged.Version), shortHash(outcome.Staged.TreeHash))
			fmt.Fprintf(out, "  lockfile: %s\n", outcome.LockPath)
			fmt.Fprintf(out, "  receipt:  %s\n", outcome.ReceiptPath)
			if outcome.AcceptedWith != "" {
				fmt.Fprintf(out, "  note:     accepted with %q — recorded in the receipt\n", outcome.AcceptedWith)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root")
	cmd.Flags().BoolVar(&force, "force", false, "skip the trial-receipt gate entirely (recorded in the acceptance receipt)")
	cmd.Flags().BoolVar(&allowPartial, "allow-partial", false, "accept a partial trial (pending approvals / warnings); recorded in the receipt")
	return cmd
}

// lockfileExtendsResolver resolves an extends: identity ("@ns/kit") to a
// kit directory via, in order: the kit-dev override for the identity's
// short name, then each lockfile entry whose resolved manifest identity
// matches. This closes the S4 gap where `kit verify` skipped base-kit
// conformance re-runs for want of a resolver.
func lockfileExtendsResolver(ctx context.Context, projectRoot string) kitverify.ExtendsResolver {
	cache := map[string]string{}
	return func(identity string) (string, error) {
		if dir, ok := cache[identity]; ok {
			return dir, nil
		}
		short := identity
		if i := strings.LastIndex(identity, "/"); i >= 0 {
			short = identity[i+1:]
		}
		if devPath := kitdev.Resolve(short); devPath != "" {
			cache[identity] = devPath
			return devPath, nil
		}
		lf, err := kitlock.Load(kitlock.Path(projectRoot))
		if err != nil {
			return "", err
		}
		for _, name := range lf.SortedNames() {
			entry := lf.Kits[name]
			dir, err := lockedKitDir(ctx, entry, projectRoot)
			if err != nil {
				continue
			}
			def, err := kit.LoadDir(dir)
			if err != nil {
				continue
			}
			if def.Identity() == identity {
				cache[identity] = dir
				return dir, nil
			}
		}
		return "", fmt.Errorf("extends %s: no lockfile entry resolves to that kit identity (run `kitsoki kit add`)", identity)
	}
}

// lockedKitDir materializes a lock entry's kit directory, preferring the
// pinned caches and falling back to live resolution.
func lockedKitDir(ctx context.Context, entry *kitlock.Entry, projectRoot string) (string, error) {
	if dir, ok := kitstage.AcceptedTree(entry); ok {
		return dir, nil
	}
	if url, ref, ok := kitgit.ParseSource(entry.Source); ok {
		res, err := kitgit.Materialize(ctx, kitgit.DefaultRunner, url, ref)
		if err != nil {
			return "", err
		}
		return res.Root, nil
	}
	appPath, err := app.ResolveSource(entry.Source, projectRoot, buildImportResolver())
	if err != nil {
		return "", err
	}
	return filepath.Dir(appPath), nil
}

// printTrialReport renders the trial outcome for operators.
func printTrialReport(out interface{ Write([]byte) (int, error) }, projectRoot string, r *kittrial.Report) {
	status := map[string]string{
		kittrial.ResultReady:   "✓ ready",
		kittrial.ResultPartial: "· partial",
		kittrial.ResultBlocked: "✗ blocked",
	}[r.Result]
	fmt.Fprintf(out, "%s — trial of %s %s -> %s\n", status, r.Kit,
		displayVersion(r.From.Version), displayVersion(r.To.Version))

	for _, g := range r.Gates {
		mark := "✓"
		switch g.Status {
		case kittrial.StatusFail:
			mark = "✗"
		case kittrial.StatusPartial:
			mark = "·"
		case kittrial.StatusSkipped:
			mark = "-"
		}
		fmt.Fprintf(out, "  %s %-14s %s   spend: $%.4f live", mark, g.ID, g.Status, g.Spend.CostUSD)
		if g.Spend.ReplayedCalls > 0 {
			fmt.Fprintf(out, " (%d replayed calls; $%.4f recorded at capture)", g.Spend.ReplayedCalls, g.Spend.ReplayedRecordedCostUSD)
		}
		fmt.Fprintln(out)
		for _, fx := range g.Fixtures {
			fmark := "✓"
			if fx.StagedLeg == "fail" {
				fmark = "✗"
			}
			line := fmt.Sprintf("      %s %s", fmark, fx.Fixture)
			if fx.BaselineLeg == "fail" {
				line += "   [stale at locked version — excluded from verdict]"
			}
			if fx.Drift != nil && !fx.Drift.Identical && fx.StagedLeg == "pass" {
				line += "   [benign drift]"
			}
			fmt.Fprintln(out, line)
			if fx.Drift != nil && !fx.Drift.Identical {
				if len(fx.Drift.AddedStates) > 0 {
					fmt.Fprintf(out, "          drift: new states %v\n", fx.Drift.AddedStates)
				}
				if len(fx.Drift.RemovedStates) > 0 {
					fmt.Fprintf(out, "          drift: dropped states %v\n", fx.Drift.RemovedStates)
				}
				if len(fx.Drift.AddedHostCalls) > 0 {
					fmt.Fprintf(out, "          drift: new host calls %v\n", fx.Drift.AddedHostCalls)
				}
				if len(fx.Drift.DroppedHostCalls) > 0 {
					fmt.Fprintf(out, "          drift: dropped host calls %v\n", fx.Drift.DroppedHostCalls)
				}
			}
		}
		for _, c := range g.Cases {
			cmark := map[string]string{
				"skipped_already_validated": "≡",
				"validated_replay":          "✓",
				"failed":                    "✗",
				"pending_approval":          "?",
			}[c.Status]
			fmt.Fprintf(out, "      %s %s: %s", cmark, c.CaseID, c.Status)
			if c.LedgerRef != "" {
				fmt.Fprintf(out, " (%s)", c.LedgerRef)
			}
			if c.Detail != "" {
				fmt.Fprintf(out, " — %s", c.Detail)
			}
			fmt.Fprintln(out)
		}
	}

	open, resolved, accepted := r.Worklist.Counts()
	fmt.Fprintf(out, "  worklist: %d open / %d resolved / %d waived — %s\n",
		open, resolved, accepted, relOrAbs(projectRoot, r.WorklistPath))
	for _, it := range r.Worklist.Items {
		if it.Status != "open" {
			continue
		}
		fmt.Fprintf(out, "      [%s/%s] %s: %s\n", it.Severity, it.Kind, it.Subject, it.Detail)
		if it.SuggestedAction != "" {
			fmt.Fprintf(out, "          -> %s\n", it.SuggestedAction)
		}
	}
	fmt.Fprintf(out, "  spend:   $%.4f live LLM across all gates\n", r.Spend.CostUSD)
	fmt.Fprintf(out, "  receipt: %s\n", relOrAbs(projectRoot, r.ReceiptPath))
	switch r.Result {
	case kittrial.ResultReady:
		fmt.Fprintf(out, "  next:    kitsoki kit accept %s\n", r.Kit)
	case kittrial.ResultPartial:
		fmt.Fprintf(out, "  next:    resolve pending items, or kitsoki kit accept %s --allow-partial\n", r.Kit)
	default:
		fmt.Fprintf(out, "  next:    address open worklist items, then re-run kitsoki kit trial %s\n", r.Kit)
	}
}

func relOrAbs(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
