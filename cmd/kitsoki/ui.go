// ui.go — implements `kitsoki ui preview`, the renderer-only design
// playground for the single-pane chat TUI. See docs/tui/README.md.
//
// The subcommand exists for three reasons:
//
//  1. Design iteration without spinning up the state machine — tweak a
//     block template, run `kitsoki ui preview --block menu`, see the diff.
//  2. Golden tests — output is byte-stable given the same width + theme +
//     fixture, so the renderer can be pinned against regressions.
//  3. Documentation — paste preview output into docs without
//     hand-drawing ASCII.
//
// It is intentionally renderer-only: no orchestrator, no machine, no
// MCP. The inputs are static fixtures defined in
// internal/tui/blocks/fixtures.go. The output goes through the same
// blocks.Renderer the live TUI will use in later phases, so drift
// between preview and live UI implies a renderer bug, not two parallel
// implementations.
package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"kitsoki/internal/tui/blocks"
)

func uiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "TUI design tools (preview, golden-fixture rendering)",
		Long: `The 'ui' command tree contains design tools for the single-pane chat
TUI. They are renderer-only — no app loading, no state machine, no MCP —
so they can run anywhere a Go binary can.

See docs/tui/README.md for the single-pane TUI architecture.`,
	}
	cmd.AddCommand(uiPreviewCmd())
	return cmd
}

func uiPreviewCmd() *cobra.Command {
	var (
		view      string
		block     string
		theme     string
		width     int
		fixture   string
		noColor   bool
		trueColor bool
	)

	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Render sample TUI blocks to stdout (no app required)",
		Long: `Render a representative sample of the single-pane chat TUI's blocks
to stdout. The fixture lives in internal/tui/blocks/fixtures.go; the
renderer is the same one the live TUI uses in Phase 1+.

Output is byte-stable for a fixed --width, --theme, --no-color, and
fixture. Golden tests in cmd/kitsoki/ui_test.go pin it.

Examples:
  kitsoki ui preview                       # full catalog (every block + every view)
  kitsoki ui preview --view world          # /world dedicated view
  kitsoki ui preview --view trace          # /trace pipeline trace
  kitsoki ui preview --block menu          # just the actions block
  kitsoki ui preview --block routing       # routing-status live frames
  kitsoki ui preview --theme meta-blue     # meta-mode theme
  kitsoki ui preview --width 80            # force terminal width
  kitsoki ui preview --no-color            # plain ASCII output`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreview(cmd.OutOrStdout(), previewOpts{
				View:      view,
				Block:     block,
				Theme:     theme,
				Width:     width,
				Fixture:   fixture,
				NoColor:   noColor,
				TrueColor: trueColor,
			})
		},
	}

	cmd.Flags().StringVar(&view, "view", "",
		"render one view: chat | world | trace. Default (no flag) prints the full catalog.")
	cmd.Flags().StringVar(&block, "block", "",
		"render just one block kind (header, user_turn, routing_status, routing, agent_turn, system_notice, slash_output, menu, inbox, background_complete, footer, prompt). Overrides --view.")
	cmd.Flags().StringVar(&theme, "theme", "default",
		"colour palette: default | mesa | meta-blue | meta-amber | off-path | all (bake-off)")
	cmd.Flags().IntVar(&width, "width", 80,
		"terminal width to render against")
	cmd.Flags().StringVar(&fixture, "fixture", "",
		"path to a fixture file (reserved; ignored in Phase 0)")
	cmd.Flags().BoolVar(&noColor, "no-color", false,
		"suppress ANSI colour escapes (useful for piping to a file)")
	cmd.Flags().BoolVar(&trueColor, "true-color", true,
		"force TrueColor profile so output is deterministic regardless of $TERM")

	return cmd
}

type previewOpts struct {
	View      string
	Block     string
	Theme     string
	Width     int
	Fixture   string
	NoColor   bool
	TrueColor bool
}

func runPreview(out io.Writer, opts previewOpts) error {
	if opts.Width <= 0 {
		opts.Width = 80
	}

	// Pin the color profile so output is deterministic — golden tests
	// rely on this. TrueColor mirrors what routing_chip_test.go does.
	if opts.TrueColor && !opts.NoColor {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}
	if opts.NoColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	if opts.Theme == "all" {
		for i, t := range blocks.AllThemes() {
			if i > 0 {
				fmt.Fprintln(out)
				fmt.Fprintln(out)
			}
			fmt.Fprintf(out, "═══ theme: %s ═══\n\n", t.Name)
			if err := renderOne(out, t.Name, opts); err != nil {
				return err
			}
		}
		return nil
	}

	return renderOne(out, opts.Theme, opts)
}

func renderOne(out io.Writer, themeName string, opts previewOpts) error {
	r := blocks.New(opts.Width, themeName)
	if opts.NoColor {
		r = r.WithNoColor(true)
	}

	// --block: render just one block kind. Used for tight design
	// iteration on a single template.
	if opts.Block != "" {
		body, err := renderSingleBlock(r, opts.Block)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, body)
		return err
	}

	// --view: render one full-pane composition.
	if opts.View != "" {
		var body string
		switch strings.ToLower(opts.View) {
		case "chat":
			body = r.RenderChatView(blocks.DefaultChatFixture())
		case "world":
			body = r.RenderWorldView("cypilot", blocks.WorldFixture())
		case "trace":
			body = r.RenderTraceView(blocks.TraceFixture())
		default:
			return fmt.Errorf("unknown --view %q (use chat|world|trace)", opts.View)
		}
		_, err := fmt.Fprintln(out, body)
		return err
	}

	// Default: print the full catalog — every block kind + every
	// composed view, labelled with section headers so authors can
	// scroll through them in one pass.
	return renderCatalog(out, r)
}

// renderCatalog dumps every block kind and every view in a fixed
// order. Section labels are bold + bracketed so they stand out
// against the surrounding rendered content. The catalog is the
// default `kitsoki ui preview` output — no flags needed to see
// everything.
func renderCatalog(out io.Writer, r *blocks.Renderer) error {
	sections := []struct {
		title string
		body  func() (string, error)
	}{
		{"welcome", func() (string, error) { return renderSingleBlock(r, "welcome") }},
		{"header", func() (string, error) { return renderSingleBlock(r, "header") }},
		{"divider", func() (string, error) { return renderSingleBlock(r, "divider") }},
		{"user turn", func() (string, error) { return renderSingleBlock(r, "user_turn") }},
		{"routing status (live frames)", func() (string, error) { return renderSingleBlock(r, "routing_status") }},
		{"routing resolved", func() (string, error) { return renderSingleBlock(r, "routing") }},
		{"agent turn", func() (string, error) { return renderSingleBlock(r, "agent_turn") }},
		{"system notice", func() (string, error) { return renderSingleBlock(r, "system_notice") }},
		{"slash output", func() (string, error) { return renderSingleBlock(r, "slash_output") }},
		{"queued echo", func() (string, error) { return renderSingleBlock(r, "queued_echo") }},
		{"menu / actions", func() (string, error) { return renderSingleBlock(r, "menu") }},
		{"inbox notification", func() (string, error) { return renderSingleBlock(r, "inbox") }},
		{"background complete", func() (string, error) { return renderSingleBlock(r, "background_complete") }},
		{"trace block", func() (string, error) { return renderSingleBlock(r, "trace") }},
		{"footer (two-line)", func() (string, error) { return renderSingleBlock(r, "footer") }},
		{"status row", func() (string, error) { return renderSingleBlock(r, "status_row") }},
		{"prompt (per mode)", func() (string, error) { return renderSingleBlock(r, "prompt") }},
		{"world (dedicated view body)", func() (string, error) { return renderSingleBlock(r, "world") }},
		{"VIEW: chat (full composition)", func() (string, error) { return r.RenderChatView(blocks.DefaultChatFixture()), nil }},
		{"VIEW: world (full composition)", func() (string, error) { return r.RenderWorldView("cypilot", blocks.WorldFixture()), nil }},
		{"VIEW: trace (full composition)", func() (string, error) { return r.RenderTraceView(blocks.TraceFixture()), nil }},
	}
	for _, s := range sections {
		body, err := s.body()
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "─── %s ───\n\n%s\n\n", s.title, body)
	}
	return nil
}

func renderSingleBlock(r *blocks.Renderer, name string) (string, error) {
	f := blocks.DefaultChatFixture()
	switch strings.ToLower(name) {
	case "header":
		return r.Header(f.Location, f.Room), nil
	case "user_turn":
		return r.UserTurn(f.Turns[0].UserInput), nil
	case "routing_status":
		// Print one frame per phase so authors see each intermediate
		// state side-by-side. Settled line at the bottom.
		return r.RenderRoutingFrames(f.Turns[1].UserInput, blocks.RoutingPhases(), f.Turns[1].Resolved), nil
	case "routing":
		return r.RoutingResolved(f.Turns[1].Resolved), nil
	case "agent_turn":
		return r.AgentTurn(f.Turns[1].AgentBody), nil
	case "system_notice":
		return r.SystemNotice(f.Welcome), nil
	case "slash_output":
		return r.SlashOutput("(meta reload signal queued — next turn will refresh)"), nil
	case "menu", "actions":
		return r.Menu(f.Actions), nil
	case "inbox":
		return r.Inbox(f.Inbox[0]), nil
	case "background_complete":
		bc := f.BackgroundCompletes[0]
		return r.BackgroundComplete(bc.Room, bc.Summary), nil
	case "footer":
		return r.Footer(f.FooterLine1, f.FooterLine2), nil
	case "prompt":
		// Show one prompt per mode so the mode-prefix table is visible.
		var b strings.Builder
		modes := []struct {
			label string
			mode  blocks.Mode
		}{
			{"normal", blocks.ModeNormal},
			{"meta", blocks.ModeMeta},
			{"off-path", blocks.ModeOffPath},
			{"slot-fill", blocks.ModeSlotFilling},
			{"awaiting", blocks.ModeAwaitingLLM},
		}
		for i, m := range modes {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(fmt.Sprintf("%-10s  %s", m.label+":", r.Prompt(m.mode)))
		}
		return b.String(), nil
	case "trace":
		return r.RoutingTrace(blocks.TraceFixture()), nil
	case "world":
		return r.World(blocks.WorldFixture()), nil
	case "welcome":
		return r.WelcomeBlock(blocks.Welcome{
			Logo:     true,
			Title:    "kitsoki · cypilot",
			Subtitle: "v1.2.0 · by brad",
			Hints: []string{
				"/help        list commands",
				"/world       inspect current state",
				"/quit        exit",
			},
			Status: "session sess_42… · state idle",
		}), nil
	case "divider":
		return r.Divider(), nil
	case "status_row":
		return r.StatusRow(f.FooterLine1, "normal"), nil
	case "queued_echo":
		// Three depths so authors see the singular / plural variants.
		var b strings.Builder
		b.WriteString(r.QueuedEcho("use the backup branch instead", 1))
		b.WriteByte('\n')
		b.WriteString(r.QueuedEcho("re-run CI when ready", 2))
		b.WriteByte('\n')
		b.WriteString(r.QueuedEcho("then merge", 3))
		return b.String(), nil
	}
	return "", errors.New("unknown --block " + name)
}
