// Package sourcecolor distinguishes templated text from LLM-generated
// text in TUI output by switching the terminal background color at the
// source boundary. It sits in the render layer: an LLM operator wraps
// its output with [Wrap] at the result boundary, the zero-width
// sentinels ride through pongo rendering, world serialization, and
// hard-wrapping untouched, and the transcript calls [Colorize] at flush
// time — the final write to the terminal — to turn each sentinel pair
// into an ANSI background switch.
//
// # Algorithm
//
// Two halves, joined by sentinels that survive everything in between:
//
//  1. Wrap brackets a string with the open/close sentinels. Each
//     sentinel is a 4-rune sequence of width-0 invisible math operators
//     (U+2061..U+2063), so no width-aware pass between wrap and colorize
//     can bisect one. See [Wrap], [WrapTree], [Strip], [IsWrapped].
//  2. Colorize walks the wrapped string a line at a time and maintains a
//     background-color stack. Entering an LLM span (open sentinel)
//     pushes the warm bg; leaving one (close sentinel) pops and restores
//     the parent's bg — never a bare reset, so nested spans paint
//     correctly. Each line opens with the foreground + stack-top bg and
//     ends with the theme reset. Multi-line LLM spans are detected as
//     "blocks" and every contained line is padded with bg-carrying
//     spaces so the warm band reads as a solid rectangle.
//
// Colorize passes upstream ANSI SGR sequences (from glamour, lipgloss)
// through verbatim, but re-emits the stack-top bg right after any reset
// that would clear the background — otherwise a per-chunk "\x1b[0m" from
// another renderer would punch a hole in the band mid-line.
//
// # Invariants
//
//   - Sentinels are width-0. Every rune in [LLMOpen] / [LLMClose]
//     consumes zero columns (verified by the package tests), so
//     wrapping never shifts layout and width-based truncation cannot
//     split a marker.
//   - Wrap and Colorize round-trip through Strip. Stripping a wrapped
//     string yields the original plain text; the sentinels carry no
//     payload beyond their position.
//   - Colorize is bg-conservative. It only ever emits background and
//     foreground SGR codes from the [Theme]; it does not invent colors
//     or alter caller text apart from block padding.
//
// # Worked example
//
//	in:   "report: " + Wrap("all clear")
//	      (the "all clear" came from an LLM; "report: " is templated)
//	pass: Colorize(in, DarkTheme, Options{})
//	out:  FG + TplBG + "report: "
//	      + LLMBG + "all clear"       (warm bronze span)
//	      + TplBG                     (pop back to slate)
//	      + Reset
//
// The leading "report: " is painted on the cool slate template bg; the
// "all clear" span switches to the warm bronze LLM bg; the close
// sentinel pops the stack back to slate; the line ends with the theme
// reset. A runnable form of this trace lives in [ExampleColorize].
//
// # Lifecycle
//
// There is no setup or teardown. [Wrap] / [WrapTree] run at the operator
// result boundary, once per LLM output. [Colorize] runs at terminal
// flush, once per rendered frame. The [Theme] values ([DarkTheme],
// [LightTheme], [HighContrastTheme]) are package-level constants chosen
// at startup from the active TUI theme; callers do not construct themes
// at runtime. All functions are pure and concurrency-safe — they hold no
// state and share nothing mutable.
//
// # Non-goals
//
//   - No CJK / wide-rune width. [Colorize] counts every visible rune as
//     one column. Correct east-Asian width is the wrapping layer's job;
//     colorize uses width only for block padding, where mild over- or
//     under-padding is cosmetic, not a correctness defect — so pulling
//     in a full runewidth table here would be overkill.
//   - No error returns. None of the functions return errors; malformed
//     or unbalanced sentinel input is handled silently (an unmatched
//     close simply leaves the stack at the template bg). The package is
//     a cosmetic pass and must never be able to fail a render.
//   - No author-facing surface. Story authors write nothing special —
//     they keep using {{ llm.summary }} or whatever world var holds an
//     LLM result, and the engine wraps it. There is deliberately no
//     markup or directive for authors to learn.
//   - No provenance beyond the binary template/LLM split. The package
//     records "did this text come from an LLM," not which model, prompt,
//     or operator produced it; richer provenance lives in the journal,
//     not in the rendered band.
//
// # Reference
//
// The user-facing source-color contract — what the bands mean and how a
// story's theme picks the hex values — is documented in
// docs/stories/story-style.md. The theme hex values defined here are
// mirrored there; keep the two in sync.
package sourcecolor
