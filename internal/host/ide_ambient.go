// Package host — ambient editor-context seam for agent prompts.
//
// When the TUI holds a live IDE link it reads the active selection at
// turn-submit and threads it onto the turn ctx with WithIDEAmbient. The agent
// operator-facing handlers (agent_ask.go, agent_ask_with_mcp.go,
// agent_converse.go) expose it two ways:
//
//   - As the reserved `args.ide` template key (mergeIDEAmbient), so a prompt can
//     reference `{{ args.ide.file }}` / `{{ args.ide.selection }}` for precise
//     placement when it wants to.
//   - As a standardized block appended to the rendered prompt automatically
//     (IDEAmbientPreamble / appendIDEAmbient), so the selection feeds the request
//     by default without any story-author opt-in — the "always injected when
//     /ide is connected" behavior. Decision verbs (decide/extract) and the task
//     delegation verb are intentionally excluded so routing/extraction and
//     sub-agent context are not biased by an editor selection.
//
// When no link is connected (CLI one-shots, flow tests, headless runs, or `/ide`
// not connected) no ambient value is set: the template scope and the rendered
// prompt are byte-identical to a run with no editor.
//
// The capture is read-at-submit and gated TUI-side on a deny list; the echo line
// the operator sees (`⧉ Selected N lines from <file>`) is the source of truth for
// the exact range that rode the turn. See docs/tui/README.md ("Editor awareness:
// /ide") and docs/architecture/hosts.md ("host.ide.*").
package host

import (
	"context"
	"fmt"
	"strings"
)

// IDEAmbient is the editor context captured at turn-submit and exposed to
// prompt templates as `args.ide`. Fields are the selection's source file and
// text plus the human-readable range echoed in the transcript; all are empty
// when nothing was selected. It is intentionally small — only what a prompt
// would reference, never the raw diagnostics blob (that rides host.ide.* verbs).
type IDEAmbient struct {
	// File is the absolute path of the file the selection was read from.
	File string `json:"file"`
	// Selection is the selected text exactly as the editor returned it.
	Selection string `json:"selection"`
	// Lines is the line count attributed to the selection (the N in the
	// `⧉ Selected N lines from <file>` echo).
	Lines int `json:"lines"`
	// Range is a compact, human-readable description of the read range
	// (e.g. "12:0-24:8") mirroring the echo's source-of-truth promise.
	Range string `json:"range"`
}

// asMap renders the ambient context as the map a pongo2 template sees under
// `args.ide`. Kept in one place so the template-facing key names stay stable.
func (a IDEAmbient) asMap() map[string]any {
	return map[string]any{
		"file":      a.File,
		"selection": a.Selection,
		"lines":     a.Lines,
		"range":     a.Range,
	}
}

// ideAmbientKey is the unexported context key for the injected ambient context.
type ideAmbientKey struct{}

// WithIDEAmbient injects the turn's ambient editor context into ctx so the
// agent handlers expose it as `args.ide`. An empty IDEAmbient (no File) is a
// no-op — the scope stays byte-identical to a turn with no editor context, so
// the deny-list and disconnected paths never need a separate ctx branch.
func WithIDEAmbient(ctx context.Context, a IDEAmbient) context.Context {
	if a.File == "" {
		return ctx
	}
	return context.WithValue(ctx, ideAmbientKey{}, a)
}

// IDEAmbientFromCtx returns the ambient editor context previously injected with
// WithIDEAmbient, and ok=false when none was injected.
func IDEAmbientFromCtx(ctx context.Context) (IDEAmbient, bool) {
	a, ok := ctx.Value(ideAmbientKey{}).(IDEAmbient)
	return a, ok
}

// mergeIDEAmbient returns templateArgs with the ambient editor context added
// under the reserved `ide` key when one is present in ctx and the author has
// not already bound `ide` themselves (an explicit author binding wins). It never
// mutates the caller's map: when there is nothing to merge it returns the input
// unchanged, and otherwise it shallow-copies before adding the key. This is the
// single seam every agent prompt path calls so `{{ args.ide.* }}` resolves
// consistently.
func mergeIDEAmbient(ctx context.Context, templateArgs map[string]any) map[string]any {
	amb, ok := IDEAmbientFromCtx(ctx)
	if !ok {
		return templateArgs
	}
	if _, taken := templateArgs["ide"]; taken {
		return templateArgs
	}
	merged := make(map[string]any, len(templateArgs)+1)
	for k, v := range templateArgs {
		merged[k] = v
	}
	merged["ide"] = amb.asMap()
	return merged
}

// ideAmbientPreambleHeader marks the auto-injected editor-selection block so it
// is unmistakable in a rendered prompt (and greppable in a recorded trace).
const ideAmbientPreambleHeader = "## Active editor selection (via /ide)"

// ideAmbientPreambleHeaderFile marks the lighter no-selection variant: the
// operator has a file focused but nothing highlighted, so only the path rides.
const ideAmbientPreambleHeaderFile = "## Active editor (via /ide)"

// IDEAmbientPreamble renders the standardized editor-selection block that is
// appended to an operator-facing agent prompt (ask / ask_with_mcp / converse)
// whenever a selection rode the turn — the "always injected when /ide is
// connected" seam. It returns "" when no selection is present (the `/ide` link
// is off, nothing was selected, or the file was deny-ruled TUI-side), so a turn
// with no editor context produces a byte-identical prompt. Unlike the
// `args.ide.*` template scope (which each prompt must reference explicitly),
// this block lands without any story-author opt-in, so a selection feeds
// requests like "do this idea" everywhere by default. The block is appended,
// not prepended: a prompt's role and instructions come first, then the selected
// code as trailing context.
func IDEAmbientPreamble(ctx context.Context) string {
	amb, ok := IDEAmbientFromCtx(ctx)
	if !ok || strings.TrimSpace(amb.File) == "" {
		return ""
	}
	if amb.Selection == "" {
		// No-selection variant: the operator has a file focused but nothing
		// highlighted. Inject the path only (no file read) so a request like
		// "reference the open doc" knows which document and can read it.
		return "\n\n" + ideAmbientPreambleHeaderFile + "\n\n" +
			fmt.Sprintf("The operator currently has `%s` open and focused in their editor, "+
				"with no text selected. Treat it as the document they are referring to; "+
				"read it with your tools if you need its contents.", amb.File)
	}
	var sb strings.Builder
	sb.WriteString("\n\n")
	sb.WriteString(ideAmbientPreambleHeader)
	sb.WriteString("\n\n")
	sb.WriteString(fmt.Sprintf("The operator currently has the following selected in `%s`", amb.File))
	if amb.Range != "" {
		sb.WriteString(fmt.Sprintf(" (%s)", amb.Range))
	}
	sb.WriteString(". Treat it as the concrete subject of the request unless they say otherwise:\n\n")
	sb.WriteString("```\n")
	sb.WriteString(amb.Selection)
	if !strings.HasSuffix(amb.Selection, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```")
	return sb.String()
}

// appendIDEAmbient returns prompt with the editor-selection preamble appended
// when a selection rode the turn, else prompt unchanged. It is the one-liner the
// operator-facing verb handlers call right before dispatch so the block lands on
// both the plugin (Dispatch) and subprocess paths.
func appendIDEAmbient(ctx context.Context, prompt string) string {
	return prompt + IDEAmbientPreamble(ctx)
}
