// Command group `kitsoki kit` — S2 (.context/kits-implementation-plan.md):
// resolve, lock, list, and dev-override kit dependencies. `kit verify` is
// S4's full contract-check + no-LLM conformance-flow runner (it supersedes
// the narrower lockfile-hash stub S2 originally shipped here — see PR #126
// and #127's flagged decisions). `kit update` remains a documented stub;
// S7 (lifecycle) builds it out for real.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	stdpath "path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/kitdev"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitver"
	"kitsoki/internal/kitverify"
	"kitsoki/internal/testrunner"
)

// kitCmd groups the kit-dependency lifecycle commands.
func kitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kit",
		Short: "Manage kit dependencies (add/list/verify/update/dev)",
		Long: `Resolve and lock kit dependencies against a project's .kitsoki/kits.lock.

A kit source is one of:
  @kitsoki/<name>              a story in the kitsoki repo or embedded library
  git+https://host/org/repo@ref  a git remote, fetched into a content-addressed
                                cache (ref may be a tag, branch, or commit SHA)
  ./relative/path or /absolute/path   a local checkout

'kit add' resolves a source, checks any --version constraint against the
resolved kit's own app.version:, and records (source, version, commit,
tree-hash) in the lockfile. 'kit list' reads it back. 'kit verify' runs the
full S4 contract-check + no-LLM conformance-flow suite against a kit
directory. 'kit update' is a stub — semver-gated re-resolution is S7.
'kit dev' overrides one kit's resolution with a local checkout for
development, generalizing the --kitsoki-repo / $KITSOKI_REPO override to a
single named kit (internal/kitdev).`,
	}
	cmd.AddCommand(kitAddCmd())
	cmd.AddCommand(kitListCmd())
	cmd.AddCommand(kitVerifyCmd())
	cmd.AddCommand(kitUpdateCmd())
	cmd.AddCommand(kitDevCmd())
	return cmd
}

func kitAddCmd() *cobra.Command {
	var (
		target     string
		name       string
		constraint string
	)
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Resolve a kit source and add/update its lockfile entry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			targetAbs, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve --target: %w", err)
			}
			kitName := name
			if kitName == "" {
				kitName = deriveKitName(source)
				if kitName == "" {
					return fmt.Errorf("cannot derive a kit name from %q; pass --name", source)
				}
			}

			entry, err := resolveKitEntry(cmd.Context(), source, targetAbs, constraint)
			if err != nil {
				return err
			}

			lockPath := kitlock.Path(targetAbs)
			lf, err := kitlock.Load(lockPath)
			if err != nil {
				return err
			}
			lf.Kits[kitName] = entry
			if err := kitlock.Save(lockPath, lf); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "locked %s@%s\n", kitName, displayVersion(entry.Version))
			fmt.Fprintf(out, "  source:    %s\n", entry.Source)
			if entry.Commit != "" {
				fmt.Fprintf(out, "  commit:    %s\n", entry.Commit)
			}
			fmt.Fprintf(out, "  tree_hash: %s\n", entry.TreeHash)
			fmt.Fprintf(out, "  lockfile:  %s\n", lockPath)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root the lockfile is written under (.kitsoki/kits.lock)")
	cmd.Flags().StringVar(&name, "name", "", "kit name to lock under (default: derived from the source)")
	cmd.Flags().StringVar(&constraint, "version", "", "version constraint the resolved kit's app.version: must satisfy (e.g. ^1.2.0)")
	return cmd
}

func kitListCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List locked kit dependencies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			targetAbs, err := filepath.Abs(target)
			if err != nil {
				return fmt.Errorf("resolve --target: %w", err)
			}
			lockPath := kitlock.Path(targetAbs)
			out := cmd.OutOrStdout()
			if !kitlock.Exists(lockPath) {
				fmt.Fprintf(out, "no lockfile at %s — run `kitsoki kit add` first\n", lockPath)
				return nil
			}
			lf, err := kitlock.Load(lockPath)
			if err != nil {
				return err
			}
			if len(lf.Kits) == 0 {
				fmt.Fprintln(out, "no kits locked yet")
				return nil
			}
			for _, kn := range lf.SortedNames() {
				e := lf.Kits[kn]
				fmt.Fprintf(out, "%s@%s\n", kn, displayVersion(e.Version))
				fmt.Fprintf(out, "  source:    %s\n", e.Source)
				if e.Commit != "" {
					fmt.Fprintf(out, "  commit:    %s\n", e.Commit)
				}
				fmt.Fprintf(out, "  tree_hash: %s\n", e.TreeHash)
				if dev := kitdev.Resolve(kn); dev != "" {
					fmt.Fprintf(out, "  dev override: %s\n", dev)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", ".", "project root the lockfile is read from")
	return cmd
}

// kitVerifyCmd implements `kitsoki kit verify <kit-dir>` (S4).
func kitVerifyCmd() *cobra.Command {
	var (
		target        string
		jsonOut       bool
		recordingPath string
		failFast      bool
		verbose       bool
	)

	cmd := &cobra.Command{
		Use:   "verify [kit-dir]",
		Short: "Run a kit's standalone contract checks and no-LLM conformance flow suite",
		Long: `Loads the kit.yaml manifest at <kit-dir> and, for each story it
provides:

  - checks that every exit-firing transition sets the world keys its
    exits.<name>.requires: declares (standalone — no importer needed)
  - checks that every name in exports.intents: is actually defined in
    intents:
  - checks that every host_interfaces.<name>.default's declared operation
    input/output shapes match either its starlark sidecar (when
    host_bindings bound it to a script) or a registered Go handler schema
    (internal/host/opschema) — an unregistered handler is skipped, not
    flagged

It then runs every glob in conformance.flows: (kit-root-relative) as a
no-LLM flow/cassette suite via the same runner as ` + "`kitsoki test flows`" + `,
and — when the manifest declares extends: dependencies — notes that a base
kit's own conformance suite would be re-run too, given a resolver (S2's
kit-resolution/lockfile machinery is not wired into this command yet; see
the PR description's flagged decisions).

Exit codes:
  0  every check and every flow passed
  1  a contract check failed or a flow failed
  2  fatal error (bad kit.yaml, bad glob, ...)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return verifyKitLockfile(cmd, target)
			}

			kitDir := args[0]
			opts := kitverify.Options{
				ImportResolver: buildImportResolver(),
				Flow: testrunner.FlowOptions{
					RecordingOverride: recordingPath,
					FailFast:          failFast,
					Verbose:           verbose,
				},
			}
			report, err := kitverify.VerifyKit(kitDir, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "kitsoki kit verify: %v\n", err)
				os.Exit(2)
			}

			out := cmd.OutOrStdout()
			if jsonOut {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(report); err != nil {
					return fmt.Errorf("encode report: %w", err)
				}
			} else {
				printVerifyReport(out, report)
			}

			if !report.OK() {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&target, "target", ".", "project root the lockfile is read from")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the report as JSON instead of plain text")
	cmd.Flags().StringVar(&recordingPath, "recording", "", "override the recording path declared in conformance flow fixtures")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "stop each flow suite at its first failure")
	cmd.Flags().BoolVar(&verbose, "v", false, "verbose per-turn flow output")

	return cmd
}

func verifyKitLockfile(cmd *cobra.Command, target string) error {
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve --target: %w", err)
	}
	lockPath := kitlock.Path(targetAbs)
	if !kitlock.Exists(lockPath) {
		return fmt.Errorf("no lockfile at %s", lockPath)
	}
	lf, err := kitlock.Load(lockPath)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(lf.Kits) == 0 {
		fmt.Fprintln(out, "no kits locked yet")
		return nil
	}

	ok := true
	for _, name := range lf.SortedNames() {
		locked := lf.Kits[name]
		resolved, err := resolveKitEntry(cmd.Context(), locked.Source, targetAbs, "")
		if err != nil {
			ok = false
			fmt.Fprintf(out, "%s: ERROR %v\n", name, err)
			continue
		}
		if locked.TreeHash != resolved.TreeHash || locked.Commit != resolved.Commit {
			ok = false
			fmt.Fprintf(out, "%s: MISMATCH\n", name)
			fmt.Fprintf(out, "  locked:   commit=%s tree_hash=%s\n", locked.Commit, locked.TreeHash)
			fmt.Fprintf(out, "  resolved: commit=%s tree_hash=%s\n", resolved.Commit, resolved.TreeHash)
			continue
		}
		fmt.Fprintf(out, "%s: OK\n", name)
	}
	if !ok {
		return fmt.Errorf("one or more locked kits changed")
	}
	return nil
}

// printVerifyReport renders a kitverify.Report as the plain-text default
// output, mirroring validateCmd's ✓/✗ convention.
func printVerifyReport(out interface{ Write([]byte) (int, error) }, report *kitverify.Report) {
	status := "✓"
	if !report.OK() {
		status = "✗"
	}
	fmt.Fprintf(out, "%s %s (%s)\n", status, report.Kit, report.Dir)

	if len(report.ParamIssues) > 0 {
		fmt.Fprintln(out, "  parameters:")
		for _, issue := range report.ParamIssues {
			fmt.Fprintf(out, "    - %s\n", issue)
		}
	}

	for _, s := range report.Stories {
		st := "✓"
		if len(s.Issues) > 0 {
			st = "✗"
		}
		fmt.Fprintf(out, "  %s story %s\n", st, s.Story)
		for _, issue := range s.Issues {
			fmt.Fprintf(out, "      - %s\n", issue)
		}
	}

	for _, f := range report.Flows {
		switch {
		case f.Err != nil:
			fmt.Fprintf(out, "  ✗ flows %s: %v\n", f.Pattern, f.Err)
		case f.Report == nil:
			fmt.Fprintf(out, "  · flows %s: no fixtures matched\n", f.Pattern)
		default:
			st := "✓"
			if f.Report.Failed > 0 {
				st = "✗"
			}
			fmt.Fprintf(out, "  %s flows %s: %d passed, %d failed (app %s)\n", st, f.Pattern, f.Report.Passed, f.Report.Failed, f.AppPath)
		}
	}

	for _, e := range report.Extends {
		if e.Err != nil {
			fmt.Fprintf(out, "  · extends %s: skipped (%v)\n", e.Kit, e.Err)
			continue
		}
		st := "✓"
		if e.Report != nil && !e.Report.OK() {
			st = "✗"
		}
		fmt.Fprintf(out, "  %s extends %s: re-ran base kit's own conformance suite\n", st, e.Kit)
	}
}

func kitUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [name]",
		Short: "Re-resolve a locked kit against its version constraint (not yet implemented)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), "kit update: not yet implemented — semver-gated re-resolution + migration worklist land in S7 (lifecycle). For now, re-run `kitsoki kit add` with the new source/version to update a lock entry.")
			return nil
		},
	}
}

func kitDevCmd() *cobra.Command {
	var (
		path  string
		clear bool
	)
	cmd := &cobra.Command{
		Use:   "dev <name>",
		Short: "Override a kit's resolution with a local checkout for development",
		Long: `Generalizes the --kitsoki-repo / $KITSOKI_REPO override (which points EVERY
@kitsoki/<name> import at one repo-wide checkout) into a per-kit local-path
override: every resolution of <name> — whether its declared source is
@kitsoki/<name> or a git+ source recorded under that name in the lockfile —
resolves against <path>/app.yaml instead, until 'kitsoki kit dev <name>
--clear'. Persisted under ~/.kitsoki/kit-dev/ (internal/kitdev), mirroring
how ~/.kitsoki/repo persists the repo-wide override (internal/kitrepo).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			out := cmd.OutOrStdout()
			if clear {
				if err := kitdev.Clear(name); err != nil {
					return err
				}
				fmt.Fprintf(out, "cleared dev override for %s\n", name)
				return nil
			}
			if path == "" {
				return fmt.Errorf("--path is required (or pass --clear to remove an existing override)")
			}
			abs, err := filepath.Abs(path)
			if err != nil {
				return fmt.Errorf("resolve --path: %w", err)
			}
			if _, statErr := os.Stat(filepath.Join(abs, "app.yaml")); statErr != nil {
				return fmt.Errorf("--path %s: no app.yaml found there: %w", abs, statErr)
			}
			if err := kitdev.Set(name, abs); err != nil {
				return err
			}
			fmt.Fprintf(out, "dev override: %s -> %s\n", name, abs)
			fmt.Fprintf(out, "every @kitsoki/%s import (or a git+ source locked under %q) now resolves here until `kitsoki kit dev %s --clear`.\n", name, name, name)
			return nil
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "local checkout path (must contain app.yaml)")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the dev override for <name>")
	return cmd
}

// resolveKitEntry resolves source (git+ or any other tier) and builds the
// lockfile Entry it should produce, validating an optional version
// constraint along the way.
func resolveKitEntry(ctx context.Context, source, importerDir, constraint string) (*kitlock.Entry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if url, ref, ok := kitgit.ParseSource(source); ok {
		res, err := kitgit.Materialize(ctx, kitgit.DefaultRunner, url, ref)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", source, err)
		}
		appPath := filepath.Join(res.Root, "app.yaml")
		def, err := app.LoadWithResolver(appPath, nil, buildImportResolver())
		if err != nil {
			return nil, fmt.Errorf("load resolved kit manifest %s: %w", appPath, err)
		}
		if err := checkConstraint(def.App.Version, constraint); err != nil {
			return nil, err
		}
		return &kitlock.Entry{Source: source, Version: def.App.Version, Commit: res.Commit, TreeHash: res.TreeHash}, nil
	}

	appPath, err := app.ResolveSource(source, importerDir, buildImportResolver())
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", source, err)
	}
	def, err := app.LoadWithResolver(appPath, nil, buildImportResolver())
	if err != nil {
		return nil, fmt.Errorf("load resolved kit manifest %s: %w", appPath, err)
	}
	if err := checkConstraint(def.App.Version, constraint); err != nil {
		return nil, err
	}
	treeHash, err := kitgit.DirTreeHash(filepath.Dir(appPath))
	if err != nil {
		return nil, fmt.Errorf("hash resolved kit directory: %w", err)
	}
	return &kitlock.Entry{Source: source, Version: def.App.Version, TreeHash: treeHash}, nil
}

func checkConstraint(version, constraint string) error {
	if constraint == "" {
		return nil
	}
	ok, err := kitver.Satisfies(version, constraint)
	if err != nil {
		return fmt.Errorf("version constraint %q: %w", constraint, err)
	}
	if !ok {
		return fmt.Errorf("resolved version %q does not satisfy constraint %q", version, constraint)
	}
	return nil
}

// deriveKitName picks a reasonable default lock-entry name from a source
// string when --name isn't given.
func deriveKitName(source string) string {
	if url, _, ok := kitgit.ParseSource(source); ok {
		base := stdpath.Base(url)
		return strings.TrimSuffix(base, ".git")
	}
	if strings.HasPrefix(source, "@kitsoki/") {
		return strings.TrimPrefix(source, "@kitsoki/")
	}
	trimmed := strings.TrimRight(source, "/")
	base := filepath.Base(trimmed)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func displayVersion(v string) string {
	if v == "" {
		return "(no app.version declared)"
	}
	return v
}
