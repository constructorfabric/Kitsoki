package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/expr"
	"kitsoki/internal/render"
)

// promptsCmd groups prompt-extension tooling. Today it exposes `spec`, which
// enumerates a story's specialization surface — the spec_* blocks an author
// marked as provisional/extensible. See docs/stories/prompts.md.
func promptsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prompts",
		Short: "Inspect a story's prompt-extension surface",
	}
	cmd.AddCommand(promptsSpecCmd())
	cmd.AddCommand(promptsRenderCmd())
	return cmd
}

func promptsRenderCmd() *cobra.Command {
	var (
		overlay string
		argKVs  []string
	)
	cmd := &cobra.Command{
		Use:   "render <app.yaml> <prompt-ref>",
		Short: "Render a story prompt to its final text (preview an overlay)",
		Long: `Render a single story prompt to the exact text the LLM would receive —
post-{% extends %}, post-{% include %}, with any overlay applied — without
running the story or making any LLM call. Use it to verify a project overlay
took effect before a real run.

  # the story's own prompt
  kitsoki prompts render stories/bugfix/app.yaml prompts/reproducing_executing.md

  # with a project overlay + template args
  kitsoki prompts render stories/bugfix/app.yaml prompts/reproducing_executing.md \
    --prompt-overlay docs/recipes/prompt-overlay-example \
    --arg ticket_id=ACME-1 --arg ticket_title="login fails"

prompt-ref is the path as the effect references it (e.g. prompts/x.md), or an
@story/@shared/@import reference.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := loadAppWithEnv(args[0])
			if err != nil {
				return err
			}
			ref := args[1]

			pp := render.PromptPath{Story: def.BaseDir}
			if def.Prompts != nil {
				for _, s := range def.Prompts.Shared {
					if !filepath.IsAbs(s) {
						s = filepath.Join(def.BaseDir, s)
					}
					pp.Shared = append(pp.Shared, s)
				}
				if def.Prompts.Overlay != "" {
					pp.Overlay = resolveOverlayArg(def.BaseDir, def.Prompts.Overlay)
				}
			}
			if overlay != "" {
				pp.Overlay = resolveOverlayArg("", overlay)
			}
			if pp.Overlay != "" {
				if info, statErr := os.Stat(pp.Overlay); statErr != nil || !info.IsDir() {
					return fmt.Errorf("prompt overlay %q is not a readable directory", pp.Overlay)
				}
			}
			for alias, w := range def.ImportWrappers {
				if w == nil || w.SourcePath == "" {
					continue
				}
				if pp.Imports == nil {
					pp.Imports = map[string]string{}
				}
				pp.Imports[alias] = filepath.Dir(w.SourcePath)
			}

			r, err := render.NewPromptRenderer(pp, true)
			if err != nil {
				return err
			}
			// Surface a silent-no-op override before rendering.
			if dead := r.OverrideIssues(ref); len(dead) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"warning: overlay overrides block(s) %s not declared by the base — they will not apply\n",
					strings.Join(dead, ", "))
			}
			out, err := r.RenderPrompt(ref, expr.Env{Args: parseArgKVs(argKVs)})
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&overlay, "prompt-overlay", "", "project overlay dir whose prompts shadow the story's")
	cmd.Flags().StringArrayVar(&argKVs, "arg", nil, "template arg as key=value (repeatable); fills {{ args.key }}")
	return cmd
}

// resolveOverlayArg resolves an overlay path: absolute as-is; relative to base
// when base is set, else to the process cwd.
func resolveOverlayArg(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if base != "" {
		return filepath.Join(base, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// parseArgKVs turns ["k=v", ...] into a template args map. A value-less key
// maps to an empty string; "=" splits on the first occurrence.
func parseArgKVs(kvs []string) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func promptsSpecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spec <app.yaml>",
		Short: "List the spec_* specialization surface of a story's prompts",
		Long: `List the spec_* blocks in a story's prompts — the sections whose
defaults the author marked as provisional and intended for project
specialization via an overlay (see docs/stories/prompts.md).

Each block is reported as a HOLE (empty default — a project MUST fill it) or a
DEFAULT (a working default a project MAY refine). Structural (non-spec_) blocks
are not specialization targets and are omitted.

A project specializes a block by dropping an overlay prompt that
{% extends "@story/<path>" %} and overrides the block, then running with
--prompt-overlay <dir>; the story is never forked.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			def, err := loadAppWithEnv(args[0])
			if err != nil {
				return err
			}

			roots := []string{filepath.Join(def.BaseDir, "prompts")}
			if def.Prompts != nil {
				for _, s := range def.Prompts.Shared {
					if !filepath.IsAbs(s) {
						s = filepath.Join(def.BaseDir, s)
					}
					roots = append(roots, s)
				}
			}
			files := collectPromptFiles(roots)
			blocks, err := render.EnumerateSpecBlocks(files)
			if err != nil {
				// A read error on one file is non-fatal; surface it but keep going.
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
			if len(blocks) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "No spec_* specialization blocks found under %s.\n", strings.Join(roots, ", "))
				return nil
			}

			name := def.App.Title
			if name == "" {
				name = def.App.ID
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Specialization surface for %s (%d block(s)):\n\n", name, len(blocks))
			curFile := ""
			for _, b := range blocks {
				rel := relToBase(def.BaseDir, b.File)
				if rel != curFile {
					fmt.Fprintf(out, "%s\n", rel)
					curFile = rel
				}
				kind := "DEFAULT"
				detail := truncateOneLine(b.Default, 60)
				if b.Hole {
					kind = "HOLE   "
					detail = "(empty — project must fill)"
				}
				fmt.Fprintf(out, "  %s  %-28s %s\n", kind, b.Name, detail)
			}
			return nil
		},
	}
	return cmd
}

// collectPromptFiles walks each root and returns the regular files found,
// de-duplicated and sorted. Missing roots are skipped silently (a story need
// not declare shared dirs).
func collectPromptFiles(roots []string) []string {
	seen := map[string]bool{}
	var files []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable subtrees / missing roots
			}
			if d.IsDir() || seen[p] {
				return nil
			}
			seen[p] = true
			files = append(files, p)
			return nil
		})
	}
	sort.Strings(files)
	return files
}

func relToBase(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil {
		return rel
	}
	return p
}

func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
