// storyboard.go — `kitsoki storyboard <validate|render|emit|check>`: the plan
// layer for tour-driven demo videos. A *.storyboard.yaml is validated
// structurally + semantically, rendered to reviewable markdown, emitted as the
// capture-ready tour manifest / kitsoki-ui-qa scenarios, and diffed against a
// recorded video's chapter sidecar. See docs/media/storyboard.md.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/storyboard"
)

func storyboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storyboard",
		Short: "Plan, validate, and cross-check tour-driven demo videos",
		Long: `A storyboard (*.storyboard.yaml) is the single planning source for a demo
video: the goal, the deterministic no-LLM binding, and every scene's purpose,
narration, drive actions, dwell, and observable claims.

  kitsoki storyboard validate demo.storyboard.yaml
  kitsoki storyboard render   demo.storyboard.yaml --out plan.md
  kitsoki storyboard emit tour demo.storyboard.yaml --out tour.yaml
  kitsoki storyboard emit qa   demo.storyboard.yaml --out scenarios.yaml
  kitsoki storyboard check    demo.storyboard.yaml --chapters demo.mp4

See docs/media/storyboard.md for the format and lifecycle.`,
	}
	cmd.AddCommand(storyboardValidateCmd())
	cmd.AddCommand(storyboardRenderCmd())
	cmd.AddCommand(storyboardEmitCmd())
	cmd.AddCommand(storyboardCheckCmd())
	return cmd
}

// loadValidated loads the storyboard and prints every lint finding; it errors
// when any finding is error-severity (or, with strict, when any exists at all).
func loadValidated(cmd *cobra.Command, path, root string, strict bool) (*storyboard.Storyboard, error) {
	sb, err := storyboard.Load(path)
	if err != nil {
		return nil, err
	}
	issues := sb.Validate(storyboard.ValidateOptions{Root: root})
	for _, issue := range issues {
		fmt.Fprintf(cmd.ErrOrStderr(), "  • %s\n", issue)
	}
	if storyboard.HasErrors(issues) || (strict && len(issues) > 0) {
		return nil, fmt.Errorf("storyboard validation failed")
	}
	return sb, nil
}

func storyboardValidateCmd() *cobra.Command {
	var root string
	var strict bool
	cmd := &cobra.Command{
		Use:   "validate <storyboard.yaml>",
		Short: "Lint a storyboard: structure, pacing budgets, bindings, scenario refs",
		Long: `Strictly parse and lint a storyboard. Errors (exit 1): malformed or duplicate
scene ids, missing purpose/narration/expect/dwell, invalid drive actions,
binding paths that do not exist, an unknown product-journey scenario or
disallowed transport. Warnings: dwell below the narration reading budget,
total screen time below the recorder's minimum, raw __ intent names in
viewer-facing text. --strict makes warnings fail too.

Binding paths and the scenario catalog resolve against --root (default: the
current directory — run from the repo root).`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := loadValidated(cmd, args[0], root, strict)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s — invalid\n", args[0])
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s — valid (%d scenes, ~%.1fs planned screen time)\n",
				args[0], len(sb.Scenes), float64(sb.EstimatedTotalMs())/1000)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "directory binding paths and the scenario catalog resolve against")
	cmd.Flags().BoolVar(&strict, "strict", false, "treat warnings as failures")
	return cmd
}

func storyboardRenderCmd() *cobra.Command {
	var root, out string
	cmd := &cobra.Command{
		Use:           "render <storyboard.yaml>",
		Short:         "Render the storyboard as reviewable markdown",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			sb, err := loadValidated(cmd, args[0], root, false)
			if err != nil {
				return err
			}
			return writeOut(cmd, out, sb.RenderMarkdown())
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "directory binding paths and the scenario catalog resolve against")
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	return cmd
}

func storyboardEmitCmd() *cobra.Command {
	var root, out string
	cmd := &cobra.Command{
		Use:   "emit <tour|qa> <storyboard.yaml>",
		Short: "Emit the capture-ready tour manifest or the kitsoki-ui-qa scenarios",
		Long: `emit tour → a standalone tour manifest ({export, steps}) consumable by
"kitsoki tour --manifest" and the Playwright tour specs.
emit qa   → a kitsoki-ui-qa scenarios file (one scenario per scene, steps =
the scene's expect claims) for scripts/qa.sh --scenarios.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, path := args[0], args[1]
			sb, err := loadValidated(cmd, path, root, false)
			if err != nil {
				return err
			}
			var data []byte
			switch kind {
			case "tour":
				data, err = sb.EmitTourYAML()
			case "qa":
				data, err = sb.EmitQAScenariosYAML()
			default:
				return fmt.Errorf("emit kind %q must be tour or qa", kind)
			}
			if err != nil {
				return err
			}
			return writeOut(cmd, out, data)
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "directory binding paths and the scenario catalog resolve against")
	cmd.Flags().StringVar(&out, "out", "", "output file (default: stdout)")
	return cmd
}

func storyboardCheckCmd() *cobra.Command {
	var root, chapters string
	cmd := &cobra.Command{
		Use:   "check <storyboard.yaml> --chapters <video|sidecar>",
		Short: "Diff a captured video's chapter sidecar against the plan",
		Long: `Reads the video's <video>.chapters.json sidecar (pass the video or the
sidecar itself) and checks: every planned scene was captured (chapter id ==
scene id), scenes appear in plan order, and each captured window holds at
least 80% of the planned dwell. Extra chapters warn as unplanned scenes.`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if chapters == "" {
				return fmt.Errorf("--chapters is required")
			}
			sb, err := loadValidated(cmd, args[0], root, false)
			if err != nil {
				return err
			}
			issues, err := storyboard.CheckChapters(sb, chapters)
			if err != nil {
				return err
			}
			for _, issue := range issues {
				fmt.Fprintf(cmd.ErrOrStderr(), "  • %s\n", issue)
			}
			if storyboard.HasErrors(issues) {
				fmt.Fprintf(cmd.ErrOrStderr(), "✗ capture does not match the storyboard\n")
				return fmt.Errorf("storyboard check failed")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ capture matches the storyboard (%d scenes)\n", len(sb.Scenes))
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", ".", "directory binding paths and the scenario catalog resolve against")
	cmd.Flags().StringVar(&chapters, "chapters", "", "video path (sidecar resolved) or the .chapters.json itself")
	return cmd
}

func writeOut(cmd *cobra.Command, out string, data []byte) error {
	if out == "" {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", out)
	return nil
}
