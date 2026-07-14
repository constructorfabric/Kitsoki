package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"kitsoki/internal/graph"
	"kitsoki/internal/graph/featuresadapter"
	"kitsoki/internal/graph/proposalsadapter"
	"kitsoki/internal/host"
)

// graphHostRegistry lazily builds the *host.Registry `kitsoki graph`
// subcommands dispatch through. S5 (.context/kits-implementation-plan.md
// decision 4, "keep kitsoki graph as generic engine verbs — the Makefile
// pipeline uses them"): these subcommands are thin wrappers over
// host.graph.* rather than calling internal/graph directly, so the CLI and
// the kit.<kit>.graph.<op> JSON-RPC/MCP surface share one implementation.
var graphHostRegistry = func() *host.Registry {
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	return reg
}()

func invokeGraphOp(op string, args map[string]any) (host.Result, error) {
	if args == nil {
		args = map[string]any{}
	}
	args["op"] = op
	return graphHostRegistry.Invoke(context.Background(), "host.graph."+op, args)
}

// graphCmd — W1.1: `kitsoki graph lint <dir>` loads a project object graph
// catalog (bundle dir or the single-file seed-objects.yaml shape) and runs
// the cross-node catalog lint (dangling refs, edge type mismatches, cycles
// on acyclic-marked edges, internal nodes reachable from a public edge).
func graphCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Project object graph catalog tools",
	}
	cmd.AddCommand(graphLintCmd())
	cmd.AddCommand(graphProposeCmd())
	cmd.AddCommand(graphApplyCmd())
	cmd.AddCommand(graphCanonicalizeCmd())
	cmd.AddCommand(graphQueryCmd())
	cmd.AddCommand(graphRenderFeaturesCmd())
	cmd.AddCommand(graphMaterializeCmd())
	return cmd
}

func graphLintCmd() *cobra.Command {
	var checkIndex bool
	var proposalsDir string
	cmd := &cobra.Command{
		Use:   "lint <catalog-path>",
		Short: "Validate a project object graph catalog's cross-node invariants",
		Long: `Loads the catalog at <catalog-path> (a bundle directory, or a single
catalog file such as docs/proposals/project-object-graph/seed-objects.yaml)
and reports every dangling edge reference, edge target type mismatch, cycle
on an acyclic-marked edge, and internal node reachable from a public edge.

With --check-index (W6.0), also regenerates each graph-sourced proposal's
docs/proposals/README.md "Current proposals" index entry and byte-compares
it against --proposals-dir/README.md (default: docs/proposals), failing on
any drift — the machine-checkable-docs principle: a hand-maintained index
next to graph-sourced data rots.

Exit code 0 when the catalog loads and lints clean (and the index doesn't
drift, if checked); non-zero otherwise.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			loadRes, err := invokeGraphOp("load", map[string]any{"catalog_path": path})
			if err != nil {
				return fmt.Errorf("graph lint: %w", err)
			}
			for _, w := range loadRes.Data["warnings"].([]any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			nodeCount := loadRes.Data["node_count"].(int)

			lintRes, err := invokeGraphOp("lint", map[string]any{"catalog_path": path})
			if err != nil {
				return fmt.Errorf("graph lint: %w", err)
			}
			issues := lintRes.Data["issues"].([]any)
			out := cmd.OutOrStdout()
			warningCount := 0
			for _, raw := range issues {
				iss := raw.(map[string]any)
				severity, _ := iss["severity"].(string)
				if severity == string(graph.SeverityWarning) {
					warningCount++
					fmt.Fprintf(out, "%s: %s: warning: %s\n", iss["node"], iss["kind"], iss["message"])
					continue
				}
				fmt.Fprintf(out, "%s: %s: %s\n", iss["node"], iss["kind"], iss["message"])
			}
			errorCount := len(issues) - warningCount

			var indexErrs []string
			if checkIndex {
				// checkProposalsIndex needs the full *graph.Catalog for
				// proposalsadapter (kitsoki-instance tooling that stays as-is
				// per D4, not part of the kit extraction) — load it directly
				// here rather than through host.graph.load's summary shape.
				cat, err := graph.LoadCatalog(path)
				if err != nil {
					return fmt.Errorf("graph lint --check-index: %w", err)
				}
				indexErrs = checkProposalsIndex(cat, proposalsDir)
				for _, e := range indexErrs {
					fmt.Fprintln(out, "index-drift:", e)
				}
			}

			// Warnings are advisory (graph.SeverityWarning) — print them but
			// exit 0, so an advisory nudge (e.g. missing-materialize-check)
			// never breaks a repo's lint gate the way a real error must.
			if errorCount == 0 && len(indexErrs) == 0 {
				if warningCount > 0 {
					fmt.Fprintf(out, "graph lint: %d nodes, %d warning(s), no errors\n", nodeCount, warningCount)
				} else {
					fmt.Fprintf(out, "graph lint: %d nodes, clean\n", nodeCount)
				}
				return nil
			}
			return fmt.Errorf("graph lint: %d issue(s), %d index-drift error(s) found", errorCount, len(indexErrs))
		},
	}
	cmd.Flags().BoolVar(&checkIndex, "check-index", false, "also check docs/proposals/README.md's generated index for drift (W6.0)")
	cmd.Flags().StringVar(&proposalsDir, "proposals-dir", "docs/proposals", "directory containing README.md for --check-index")
	return cmd
}

// checkProposalsIndex regenerates every graph-sourced proposal's README
// index entry and reports any that don't byte-match a line in
// <proposalsDir>/README.md.
func checkProposalsIndex(cat *graph.Catalog, proposalsDir string) []string {
	readmePath := filepath.Join(proposalsDir, "README.md")
	raw, err := os.ReadFile(readmePath)
	if err != nil {
		return []string{fmt.Sprintf("read %s: %v", readmePath, err)}
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		lines[l] = true
	}

	var errs []string
	for _, node := range proposalsadapter.GraphSourcedProposals(cat) {
		entry, err := proposalsadapter.RenderIndexEntry(node)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", node.ID, err))
			continue
		}
		if !lines[entry] {
			errs = append(errs, fmt.Sprintf("%s: generated entry not found in %s (regenerate and update the index): %s", node.ID, readmePath, entry))
		}
	}
	for _, node := range proposalsadapter.GraphSourcedChildProposals(cat) {
		entry, err := proposalsadapter.RenderChildEntry(node)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", node.ID, err))
			continue
		}
		if !lines[entry] {
			errs = append(errs, fmt.Sprintf("%s: generated child entry not found in %s (regenerate and update the index): %s", node.ID, readmePath, entry))
		}
	}
	return errs
}

func graphApplyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply <changeset-id> <catalog-path>",
		Short: "Apply an authorized changeset node's operations to a catalog",
		Long: `Loads the catalog at <catalog-path>, finds the changeset node
<changeset-id>, and — dry-run-first — builds a candidate catalog on a scratch
copy, re-loads and re-lints it, and only if that comes back clean commits the
changed files. A rejected changeset (failing pre-apply validation or
post-apply lint) never touches the real catalog.

The changeset's status must be "authorized" to apply for real; pass --dry-run
to preview a changeset in any status without requiring authorization or
committing anything.

Exit code 0 on a successful apply (or a clean dry-run); non-zero on rejection.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			changesetID, path := args[0], args[1]
			res, err := invokeGraphOp("apply", map[string]any{
				"catalog_path": path,
				"changeset_id": changesetID,
				"dry_run":      dryRun,
			})
			if err != nil {
				return fmt.Errorf("graph apply: %w", err)
			}
			out := cmd.OutOrStdout()
			for _, r := range res.Data["reject_reasons"].([]any) {
				fmt.Fprintln(out, "reject:", r)
			}
			for _, iss := range res.Data["lint_issues"].([]any) {
				fmt.Fprintln(out, "reject (post-apply lint):", iss)
			}
			rejected, _ := res.Data["rejected"].(bool)
			if rejected {
				return fmt.Errorf("graph apply: changeset %q rejected, catalog untouched", changesetID)
			}
			verb := "applied"
			if dryRun {
				verb = "dry-run clean"
			}
			fmt.Fprintf(out, "graph apply: %s, %d file(s) changed: %v\n", verb, len(res.Data["changed_files"].([]any)), res.Data["changed_files"])
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview the changeset without requiring authorization or committing")
	return cmd
}

// graphCanonicalizeCmd — the out-of-band remedy the NEEDS_CANONICALIZATION
// guard message has always demanded but never shipped: re-serialize every
// block-scalar-bearing catalog file into marshalYAMLNode's canonical form
// so the propose/authorize/apply lifecycle stops rejecting on formatting.
// Calls internal/graph.Canonicalize directly (like lint --check-index)
// because host.graph.* has no canonicalize op — this is repo maintenance,
// not a graph write.
func graphCanonicalizeCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "canonicalize <catalog-path>",
		Short: "Re-serialize catalog files into canonical re-marshal form",
		Long: `Rewrites every file backing the catalog at <catalog-path> that the
NEEDS_CANONICALIZATION guard would reject — a file containing a hand-wrapped
block scalar whose bytes differ from yaml.v3's own re-serialization — into
that canonical form, comments preserved. Files without block scalars, or
already canonical, are untouched. Run this once when propose/apply rejects
with NEEDS_CANONICALIZATION; the reflow diff is deliberate and reviewable
instead of smuggled into some later changeset's diff.

Exit code 0 whether or not files changed; non-zero only on load/write errors.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := graph.Canonicalize(args[0], dryRun)
			if err != nil {
				return fmt.Errorf("graph canonicalize: %w", err)
			}
			out := cmd.OutOrStdout()
			for _, s := range res.Skipped {
				fmt.Fprintln(out, "skipped:", s)
			}
			if len(res.ChangedFiles) == 0 {
				fmt.Fprintln(out, "graph canonicalize: already canonical, nothing to do")
			} else {
				verb := "rewrote"
				if dryRun {
					verb = "would rewrite"
				}
				fmt.Fprintf(out, "graph canonicalize: %s %d file(s):\n", verb, len(res.ChangedFiles))
				for _, f := range res.ChangedFiles {
					fmt.Fprintln(out, " ", f)
				}
			}
			if len(res.Skipped) > 0 {
				return fmt.Errorf("graph canonicalize: %d file(s) needed canonicalization but could not be written", len(res.Skipped))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report files that would be rewritten without touching disk")
	return cmd
}

// graphRenderFeaturesCmd — W3.1: regenerate features/*.yaml from the graph's
// public site-page nodes via featuresadapter, so the graph catalog is the
// only producer of the legacy feature-catalog shape. `make features` runs
// this before the existing pnpm features:gen step; with --check it fails
// (without writing) if any regenerated file would differ from what's on
// disk, catching hand-edits to features/*.yaml that bypassed the graph.
func graphRenderFeaturesCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "render-features <catalog-path> <output-dir>",
		Short: "Regenerate features/*.yaml from the graph's public site-page nodes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			catalogPath, outDir := args[0], args[1]
			cat, err := graph.LoadCatalog(catalogPath)
			if err != nil {
				return fmt.Errorf("graph render-features: %w", err)
			}

			var stale []string
			for _, sitePage := range featuresadapter.PublicSitePages(cat) {
				doc, err := featuresadapter.BuildFeatureDoc(cat, sitePage)
				if err != nil {
					return fmt.Errorf("graph render-features: %s: %w", sitePage.ID, err)
				}
				var buf bytes.Buffer
				enc := yaml.NewEncoder(&buf)
				enc.SetIndent(2)
				if err := enc.Encode(doc); err != nil {
					return fmt.Errorf("graph render-features: %s: marshal: %w", sitePage.ID, err)
				}
				enc.Close()
				out := append([]byte("# yaml-language-server: $schema=feature.schema.json\n"), buf.Bytes()...)

				outPath := filepath.Join(outDir, doc.ID+".yaml")
				if check {
					existing, err := os.ReadFile(outPath)
					if err != nil || !bytes.Equal(existing, out) {
						stale = append(stale, outPath)
					}
					continue
				}
				if err := os.WriteFile(outPath, out, 0o644); err != nil {
					return fmt.Errorf("graph render-features: write %s: %w", outPath, err)
				}
			}

			if check {
				if len(stale) > 0 {
					for _, p := range stale {
						fmt.Fprintln(cmd.ErrOrStderr(), "stale:", p)
					}
					return fmt.Errorf("graph render-features --check: %d file(s) out of date with the graph catalog; run `kitsoki graph render-features` to regenerate", len(stale))
				}
				fmt.Fprintln(cmd.OutOrStdout(), "graph render-features --check: up to date")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "graph render-features: wrote %d file(s) to %s\n", len(featuresadapter.PublicSitePages(cat)), outDir)
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail if any regenerated file would differ from what's on disk, without writing")
	return cmd
}

func graphQueryCmd() *cobra.Command {
	var modeRefsTo string
	var modeExplainType string
	var modeImpact string
	var toType string
	cmd := &cobra.Command{
		Use:   "query <catalog-path>",
		Short: "Query relationships, types, or impact of changes in the catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			var mode, target string
			if modeRefsTo != "" {
				mode = "refs-to"
				target = modeRefsTo
			} else if modeExplainType != "" {
				mode = "explain-type"
				target = modeExplainType
			} else if modeImpact != "" {
				mode = "impact"
				target = modeImpact
			} else {
				return fmt.Errorf("must specify one of --refs-to, --explain-type, or --impact")
			}

			res, err := invokeGraphOp("query", map[string]any{
				"catalog_path": path,
				"mode":         mode,
				"target":       target,
				"to_type":      toType,
			})
			if err != nil {
				return fmt.Errorf("graph query: %w", err)
			}
			out := cmd.OutOrStdout()
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(res.Data)
		},
	}
	cmd.Flags().StringVar(&modeRefsTo, "refs-to", "", "find all references to node ID")
	cmd.Flags().StringVar(&modeExplainType, "explain-type", "", "explain effective type structure")
	cmd.Flags().StringVar(&modeImpact, "impact", "", "preview impact of node ID")
	cmd.Flags().StringVar(&toType, "to-type", "", "target type for --impact retype check")
	return cmd
}

