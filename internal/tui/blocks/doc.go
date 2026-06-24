// Package blocks is the per-block TUI renderer for the single-pane chat
// view: it sits between the orchestrator and the screen, downstream of
// the transcript model that stitches its strings together and upstream
// of nothing (its output is plain styled text, ready to print). The
// transcript model ([kitsoki/internal/tui]) calls these renderers to
// turn engine events into scrollback lines; the preview CLI (`kitsoki
// ui preview`) calls the SAME renderers against static [ChatFixture]
// data so design changes are visible without spinning up the state
// machine.
//
// # Algorithm
//
// There is no pipeline here — each exported method is an independent,
// stateless string builder. A [Renderer] carries three knobs: the wrap
// Width, the [Theme] palette, and a NoColor flag. Every method:
//
//  1. Builds the block's plain text (glyphs, indentation, truncation at
//     Width).
//  2. Wraps that text in a lipgloss style pulled from the active Theme,
//     unless NoColor is set, in which case colour is suppressed and only
//     structural styling (bold/italic) survives.
//
// [Renderer.RenderChatView] is the one composing method: it calls the
// per-block renderers in a fixed order and joins them with one blank
// line, so the whole transcript is one string. The composition order is
// documented on RenderChatView itself.
//
// # Invariants
//
//   - The zero [Renderer] (Width 0, nil Theme) is NOT usable — methods
//     dereference Theme. Always construct via [New], which supplies a
//     non-nil Theme (falling back to the default palette for an unknown
//     name).
//   - Renderers are read-only and hold no mutable state, so a single
//     [Renderer] is safe for concurrent use by multiple goroutines.
//     [Renderer.WithNoColor] returns a copy rather than mutating, so the
//     original stays shareable.
//   - [Renderer.StatusRow] guarantees its styled output is EXACTLY Width
//     visible columns — text is composed and truncated before styling so
//     lipgloss never hard-wraps and leaks a background colour into the
//     next line. This is load-bearing: it was the source of the earlier
//     "footer garbled into transcript" bug.
//   - No method returns an error. Bad input (over-wide text, unknown
//     [Mode], empty action list) degrades to a sensible rendering rather
//     than failing.
//
// # Worked example
//
// A minimal chat view with one turn, rendered at width 60 with colour
// suppressed, traces input to output like this:
//
//	in:  ChatFixture{
//	       Location: "idle", Room: "demo",
//	       Welcome:  "session started",
//	       Turns: []FixtureTurn{{
//	         UserInput: "go north",
//	         Resolved:  Resolved{Kind:"nav", Intent:"north",
//	                             Source: SourceDeterministic},
//	         AgentBody: "You head north.",
//	       }},
//	       PromptMode: ModeNormal,
//	     }
//	out (NoColor, width 60; blocks separated by a blank line, header
//	padded with trailing spaces to the width):
//	     idle · demo            (header, padded to width)
//	     ─────────────…         (rule)
//	     · session started      (system notice)
//	     > go north             (user turn)
//	       → nav: north   (deterministic · 1.00)   (resolved routing)
//	       You head north.      (agent turn, indented)
//	     ─────────────…         (rule)
//	     > _                    (prompt; empty footer collapses out)
//
// A runnable, output-checked form of this trace lives in
// [ExampleRenderer_RenderChatView].
//
// # Lifecycle
//
// Construct one [Renderer] per frame (or reuse one — it is immutable)
// with [New](width, themeName). The width comes from the terminal size;
// the theme name comes from the current room's theme (default,
// meta-blue, meta-amber, off-path). Golden tests and piped output call
// [Renderer.WithNoColor](true) to pin deterministic, ANSI-free strings.
// Themes are looked up by [ThemeByName]; [AllThemes] enumerates them for
// the preview's bake-off mode.
//
// # Non-goals
//
//   - No Markdown rendering. Agent bodies are wrapped as plain text;
//     the live transcript runs them through Glamour separately. Reason:
//     the preview demonstrates layout, not prose styling, and pulling
//     Glamour in would make the package expensive to link from a cobra
//     command.
//   - No state management or state machine. The renderers are pure
//     functions of their arguments. Reason: all routing/turn state lives
//     in the orchestrator and transcript model; duplicating it here would
//     create two sources of truth.
//   - No Bubble Tea, no input handling, no viewport/scroll management.
//     Reason: the package must be linkable from unit tests and the
//     preview CLI without a running event loop.
//   - No theme file loading. Themes are compiled-in [Theme] values.
//     Reason: the palette is part of the binary's design surface, not
//     user configuration, in this PoC phase.
//
// # Reference
//
// The user-facing reference for the chat view — blocks as the unit of
// rendering, the typed-element + pongo2 view pipeline, input/menu/inbox
// behaviour — is docs/tui/README.md.
package blocks
