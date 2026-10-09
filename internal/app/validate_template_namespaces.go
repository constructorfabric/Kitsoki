package app

import (
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── unknown template-namespace diagnostic ────────────────────────────────
//
// The bug (bitten twice — see
// .context/scenario-qa-prd-transports-kinks.md fixes 0bb3c77e and 46a8a0e1):
// an agent-prompt template referenced `{{ context.* }}`, a namespace that
// render.ToContext (internal/render/pongo.go) never populates. pongo2
// renders an unknown top-level variable as an empty string with NO error, so
// both bugs surfaced only as a live agent honestly reporting a blank
// handoff — not a load-time or render-time failure. This pass restores an
// authoring-time signal: a NON-FATAL advisory warning (like
// validateViewBindFallbacks) so a genuinely dynamic/future namespace can
// still load, but a typo'd or fictional one is caught before an agent ever
// sees it.
//
// Scope: prompt files referenced via a `with: {prompt: "..."}` string on any
// Invoke effect (host.agent.task/ask/decide's prompt-template mechanism —
// exactly the shape both real bugs hit), plus every state's inline view
// template (the same pongo2 ToContext seam renders both). Filter args,
// quoted string literals, and prose outside `{{ }}` / `{% %}` tags are not
// scanned. For-loop-bound locals (`{% for x in ... %}` / `{% for k, v in
// ... %}`) and the pongo2 `forloop`/`loop` builtins are recognized and
// excluded so a loop variable like `{{ leg.transport }}` is never flagged.
//
// Limitation: this is a best-effort static scan, not a template parse. A
// prompt file that can't be resolved/read at validate time (e.g. an
// imported child's prompt path rebased by a mechanism this pass doesn't
// replicate) is silently skipped rather than treated as a load error — the
// goal is to catch the common case cheaply, not to reimplement prompt-path
// resolution. A dotted reference inside a quoted string literal inside a
// tag (e.g. `{{ "e.g. foo" }}`) can rarely false-positive; that's an
// acceptable false-positive rate for a WARNING-level lint.

// knownTemplateNamespaces are the top-level pongo2 context keys
// render.ToContext ever populates for either view or prompt rendering (see
// internal/render/pongo.go ToContext), plus pongo2's own for-loop builtins.
// Anything else used as `<ident>.<field>` inside a `{{ }}`/`{% %}` tag is
// either a for-loop-bound local (tracked separately per file) or an unknown
// namespace worth flagging.
var knownTemplateNamespaces = map[string]struct{}{
	"world":          {},
	"slots":          {},
	"event":          {},
	"run":            {},
	"args":           {},
	"menu":           {},
	"prerequisites":  {},
	"item":           {},
	"state":          {},
	"available":      {},
	"blocked":        {},
	"blocked_reason": {},
	"intent_status":  {},
	"forloop":        {},
	"loop":           {},
	"true":           {},
	"false":          {},
	"none":           {},
	"nil":            {},
}

// templateTagRE extracts every `{{ ... }}` / `{% ... %}` tag body (including
// the delimiters, so downstream regexes can't accidentally match plain
// prose outside a tag). (?s) lets `.` cross newlines for a tag that wraps.
var templateTagRE = regexp.MustCompile(`(?s)\{[{%].*?[%}]\}`)

// forLoopVarRE captures a for-tag's loop variable(s): `{% for x in ... %}`
// or the two-variable form `{% for k, v in ... %}`.
var forLoopVarRE = regexp.MustCompile(`\{%-?\s*for\s+(\w+)\s*(?:,\s*(\w+)\s*)?\s+in\s`)

// identDotChainRE matches a whole dotted identifier chain (e.g.
// `world.leg_results.items`) and captures only its ROOT identifier. Matching
// the whole chain (not just one `\bident\.` at a time) is what keeps a
// nested field name like `leg_results` in `world.leg_results.items` from
// being mistaken for a second root reference — FindAll resumes scanning
// after the full chain, never re-entering it.
var identDotChainRE = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)(?:\.[A-Za-z_][A-Za-z0-9_]*)+`)

// unknownNamespaceWarning is one advisory finding: Location (a state path or
// prompt file reference) contains a template reference to the unknown
// top-level namespace Ident.
type unknownNamespaceWarning struct {
	Location string
	Ident    string
}

// validateTemplateNamespaces emits advisory (NON-FATAL) warnings for
// `{{ <unknown-namespace>.* }}` references in inline view templates and
// agent-prompt template files. Wired into validateDef alongside
// validateViewBindFallbacks; the errs parameter is accepted only to match
// the validate*-pass call signature and is never appended to.
func validateTemplateNamespaces(file string, def *AppDef, _ *[]error) {
	for _, w := range collectTemplateNamespaceWarnings(def) {
		slog.Warn("template references an unknown top-level namespace; pongo2 "+
			"renders it as an empty string with no error",
			"file", file, "location", w.Location, "namespace", w.Ident,
			"hint", "known namespaces: world/slots/event/run/args/menu/prerequisites/item/state")
	}
}

// collectTemplateNamespaceWarnings walks def's state tree collecting
// findings from both inline view templates and referenced prompt files.
// Findings are returned in a stable (state path, then namespace) order.
func collectTemplateNamespaceWarnings(def *AppDef) []unknownNamespaceWarning {
	if def == nil {
		return nil
	}
	var out []unknownNamespaceWarning
	out = append(out, collectViewTemplateNamespaceWarnings("", def.States)...)
	out = append(out, collectPromptTemplateNamespaceWarnings("", def.States, def.BaseDir)...)
	return out
}

// collectViewTemplateNamespaceWarnings scans every state's inline view text
// (via inlineViewText, shared with validateViewBindFallbacks). States using
// an external standalone template (View.TemplateFile) are skipped — not
// inline-scannable from the AppDef.
func collectViewTemplateNamespaceWarnings(prefix string, states map[string]*State) []unknownNamespaceWarning {
	var out []unknownNamespaceWarning
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		statePath := joinPath(prefix, name)
		if s.View.TemplateFile == "" {
			if text := inlineViewText(s.View); text != "" {
				for _, ident := range scanUnknownTemplateNamespaces(text) {
					out = append(out, unknownNamespaceWarning{
						Location: "state " + strQuote(statePath) + " view",
						Ident:    ident,
					})
				}
			}
		}
		if len(s.States) > 0 {
			out = append(out, collectViewTemplateNamespaceWarnings(statePath, s.States)...)
		}
	}
	return out
}

// collectPromptTemplateNamespaceWarnings walks every effect in the state
// tree (on_enter, every transition's effects, and nested on_complete/effects
// chains — mirroring collectBindTargets's walk) looking for a `with:
// {prompt: "..."}` string on any Invoke effect, reads the referenced file
// relative to baseDir, and scans it. A prompt path that can't be resolved or
// read is silently skipped (see the package-doc Limitation above).
func collectPromptTemplateNamespaceWarnings(prefix string, states map[string]*State, baseDir string) []unknownNamespaceWarning {
	var out []unknownNamespaceWarning
	seenPrompts := map[string]struct{}{}
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		statePath := joinPath(prefix, name)
		refs := collectPromptRefs(s.OnEnter)
		for _, intentName := range sortedKeys(s.On) {
			for _, tr := range s.On[intentName] {
				refs = append(refs, collectPromptRefs(tr.Effects)...)
			}
		}
		for _, ref := range refs {
			path := resolvePromptRefForValidate(ref, baseDir)
			if path == "" {
				continue
			}
			if _, ok := seenPrompts[path]; ok {
				continue
			}
			seenPrompts[path] = struct{}{}
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			for _, ident := range scanUnknownTemplateNamespaces(string(body)) {
				out = append(out, unknownNamespaceWarning{
					Location: "state " + strQuote(statePath) + " prompt " + strQuote(ref),
					Ident:    ident,
				})
			}
		}
		if len(s.States) > 0 {
			out = append(out, collectPromptTemplateNamespaceWarnings(statePath, s.States, baseDir)...)
		}
	}
	return out
}

// collectPromptRefs walks effs (and their nested OnComplete/Effects chains,
// mirroring collectBindTargets) accumulating every `with.prompt` string
// found on an Invoke effect — the host.agent.task/ask/decide prompt-template
// mechanism both real bugs hit.
func collectPromptRefs(effs []Effect) []string {
	var out []string
	for _, eff := range effs {
		if eff.Invoke != "" && eff.With != nil {
			if p, ok := eff.With["prompt"].(string); ok && p != "" {
				out = append(out, p)
			}
		}
		out = append(out, collectPromptRefs(eff.OnComplete)...)
		out = append(out, collectPromptRefs(eff.Effects)...)
	}
	return out
}

// resolvePromptRefForValidate resolves a prompt reference to an absolute
// path for validate-time reading, mirroring host.resolvePromptPath's
// KITSOKI_APP_DIR-join behavior (baseDir stands in for KITSOKI_APP_DIR,
// which the orchestrator sets to the app's BaseDir at runtime). A templated
// reference (contains `{{`) can't be statically resolved and is skipped.
func resolvePromptRefForValidate(ref, baseDir string) string {
	if ref == "" || strings.Contains(ref, "{{") {
		return ""
	}
	if filepath.IsAbs(ref) {
		return ref
	}
	if baseDir == "" {
		return ""
	}
	return filepath.Join(baseDir, ref)
}

// scanUnknownTemplateNamespaces extracts every `{{ }}`/`{% %}` tag from text,
// collects for-loop-bound local variable names, then reports (in sorted,
// de-duplicated order) every root identifier of a dotted reference inside a
// tag that is neither a known namespace (knownTemplateNamespaces) nor a
// local.
func scanUnknownTemplateNamespaces(text string) []string {
	if !strings.Contains(text, "{{") && !strings.Contains(text, "{%") {
		return nil
	}
	locals := map[string]struct{}{}
	for _, m := range forLoopVarRE.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			locals[strings.ToLower(m[1])] = struct{}{}
		}
		if m[2] != "" {
			locals[strings.ToLower(m[2])] = struct{}{}
		}
	}
	found := map[string]struct{}{}
	for _, tag := range templateTagRE.FindAllString(text, -1) {
		// Blank out quoted string literals first — a `| default:"open
		// rollup.md for last_result.driver_scenarios"` fallback string is
		// prose, not a template variable reference, and its dotted-looking
		// words (filenames, dotted doc references) would otherwise false-
		// positive as unknown root identifiers.
		stripped := stripQuotedStringLiterals(tag)
		for _, m := range identDotChainRE.FindAllStringSubmatch(stripped, -1) {
			ident := m[1]
			lower := strings.ToLower(ident)
			if _, ok := knownTemplateNamespaces[lower]; ok {
				continue
			}
			if _, ok := locals[lower]; ok {
				continue
			}
			found[ident] = struct{}{}
		}
	}
	if len(found) == 0 {
		return nil
	}
	out := make([]string, 0, len(found))
	for ident := range found {
		out = append(out, ident)
	}
	sort.Strings(out)
	return out
}

// strQuote wraps s in double quotes for a diagnostic message (mirrors the
// %q-shaped location strings used elsewhere in this package's warnings).
func strQuote(s string) string { return "\"" + s + "\"" }

// stripQuotedStringLiterals blanks out (replaces with spaces, preserving
// offsets) every '...'/"..."/`...`-quoted span in s, honoring a backslash
// escape immediately before the closing quote. Used to keep
// identDotChainRE from matching dotted-looking prose inside a filter's
// string argument (e.g. `| default:"open rollup.md for details"`) as if it
// were a real template variable reference.
func stripQuotedStringLiterals(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var inQuote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote && s[i-1] != '\\' {
				inQuote = 0
			}
			b.WriteByte(' ')
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			inQuote = c
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
