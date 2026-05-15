// Command kitsoki-migrate-templates is the Phase C codemod (proposal §7).
// It walks YAML files under <path>... and rewrites every templated-string
// leaf from the legacy expr-lang `{{ }}` syntax to pongo2 syntax per the
// §3.1 translation table. Pure-expression fields (`when:`, bare-
// expression `initial:`) are left untouched; the allowlist lives in
// walk.go (isTemplatedField).
//
// Default mode is dry-run: a unified diff per file is printed to stdout
// and the source is not touched. Pass --write to apply the rewrites in
// place. The tool is idempotent — re-running over already-migrated
// source is a no-op (see rewrite_test.go TestRewriteIdempotent).
//
// Usage:
//
//	kitsoki-migrate-templates [--write] [--dry-run] <path> [<path>...]
//
// Each <path> may be a file or a directory; directories are walked
// recursively and all .yaml / .yml files are processed.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	var (
		write  bool
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "kitsoki-migrate-templates [flags] <path> [<path>...]",
		Short: "Rewrite legacy expr-lang {{ }} template syntax to pongo2 syntax in YAML files.",
		Long: `kitsoki-migrate-templates walks every YAML file under the given paths
and rewrites every templated-string leaf from the legacy expr-lang
{{ }} syntax to pongo2 syntax (proposal §3.1). Pure-expression fields
(when: guards, bare-expression initial: selectors) are left untouched.

Default mode is dry-run: prints a unified diff per file to stdout
without touching disk. Pass --write to apply the rewrites in place.

Idempotent: re-running on already-migrated source is a no-op.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.OutOrStdout(), cmd.ErrOrStderr(), args, write, dryRun)
		},
	}
	cmd.Flags().BoolVar(&write, "write", false, "apply rewrites in place")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print a unified diff per file (default behaviour)")
	return cmd
}

// run is the body of the cobra command, extracted for testability. It
// returns an error iff any file errored — files that parsed and
// rewrote successfully still return nil even if their rewrites failed
// to apply (those errors print to stderr).
func run(stdout, stderr interface{ Write([]byte) (int, error) }, paths []string, write, dryRun bool) error {
	// Mutually exclusive with a sensible default.
	if write && dryRun {
		fmt.Fprintln(stderr, "--write and --dry-run are mutually exclusive; pick one.")
		return fmt.Errorf("conflicting flags")
	}

	files, err := expandPaths(paths)
	if err != nil {
		return err
	}
	var anyErr error
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(stderr, "%s: read: %v\n", file, err)
			anyErr = err
			continue
		}
		out, n, err := MigrateFile(file, src)
		if err != nil {
			fmt.Fprintf(stderr, "%s: migrate: %v\n", file, err)
			anyErr = err
			continue
		}
		if n == 0 {
			continue
		}
		if write {
			if err := os.WriteFile(file, out, 0o644); err != nil {
				fmt.Fprintf(stderr, "%s: write: %v\n", file, err)
				anyErr = err
				continue
			}
			fmt.Fprintf(stdout, "%s: %d template(s) rewritten\n", file, n)
		} else {
			diff := difflib.UnifiedDiff{
				A:        difflib.SplitLines(string(src)),
				B:        difflib.SplitLines(string(out)),
				FromFile: file,
				ToFile:   file + " (rewritten)",
				Context:  3,
			}
			text, err := difflib.GetUnifiedDiffString(diff)
			if err != nil {
				fmt.Fprintf(stderr, "%s: diff: %v\n", file, err)
				anyErr = err
				continue
			}
			fmt.Fprint(stdout, text)
		}
	}
	return anyErr
}

// expandPaths turns the user's argv into a flat list of YAML file paths.
// Files are kept as-is; directories are walked recursively and every
// .yaml/.yml file under them is included.
func expandPaths(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if !info.IsDir() {
			out = append(out, p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".yaml" || ext == ".yml" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", p, err)
		}
	}
	return out, nil
}
