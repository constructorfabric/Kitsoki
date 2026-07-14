package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/storyauthoring"
)

// Markdown compiles an AppDef into a publishable Markdown document — the
// derived, version-controllable artifact described in the package comment.
// Output is deterministic and stable across runs (every map is walked in
// sorted-key order), so the returned bytes are safe to commit and diff.
//
// def must be non-nil and is expected to be a loaded, validated AppDef
// (the product of [app.Load]); Markdown does not validate it, it only
// reads it. The only error condition is a nil def — Markdown never fails on
// well-formed-but-unusual content, it renders whatever the AppDef holds.
func Markdown(def *app.AppDef) ([]byte, error) {
	if def == nil {
		return nil, fmt.Errorf("render: AppDef is nil")
	}
	var b strings.Builder
	r := &renderer{def: def, w: &b}
	r.run()
	return []byte(b.String()), nil
}

type renderer struct {
	def *app.AppDef
	w   *strings.Builder
}

func (r *renderer) run() {
	r.title()
	r.overview()
	r.stateDiagram()
	r.worldVars()
	r.intents()
	r.rooms()
	r.offPath()
	r.generated()
}

// ---- Sections --------------------------------------------------------------

func (r *renderer) title() {
	title := r.def.App.Title
	if title == "" {
		title = r.def.App.ID
	}
	r.ln("# " + title)
	r.ln("")
	var meta []string
	if r.def.App.Version != "" {
		meta = append(meta, "**Version** "+r.def.App.Version)
	}
	if r.def.App.Author != "" {
		meta = append(meta, "_by "+r.def.App.Author+"_")
	}
	if r.def.App.License != "" {
		meta = append(meta, "License: "+r.def.App.License)
	}
	if len(meta) > 0 {
		r.ln(strings.Join(meta, " · "))
		r.ln("")
	}
}

func (r *renderer) overview() {
	rootName, _ := r.def.Root.(string)
	r.ln("## Overview")
	r.ln("")
	r.ln(fmt.Sprintf("- App ID: `%s`", r.def.App.ID))
	if rootName != "" {
		r.ln(fmt.Sprintf("- Entry room: [`%s`](#room-%s)", rootName, anchorSlug(rootName)))
	}
	r.ln(fmt.Sprintf("- Rooms: %d", countStates(r.def.States)))
	r.ln(fmt.Sprintf("- Intents: %d", len(renderableIntentNames(r.def.Intents))))
	r.ln(fmt.Sprintf("- World variables: %d", len(renderableWorldKeys(r.def.World))))
	if len(r.def.Hosts) > 0 {
		r.ln(fmt.Sprintf("- Host allow-list: %s", backtickList(r.def.Hosts)))
	}
	r.ln("")
}

// stateDiagram emits a Mermaid flowchart showing rooms and transitions.
func (r *renderer) stateDiagram() {
	if len(r.def.States) == 0 {
		return
	}
	r.ln("## State Diagram")
	r.ln("")
	r.ln("```mermaid")
	r.ln("flowchart LR")
	// Collect all state paths (flat + nested).
	var paths []string
	walkStates(r.def.States, "", func(path string, _ *app.State) {
		if storyauthoring.IsFrameworkRoom(path) {
			return
		}
		paths = append(paths, path)
	})
	sort.Strings(paths)
	// Declare nodes.
	for _, p := range paths {
		r.ln(fmt.Sprintf("  %s[\"%s\"]", mermaidID(p), p))
	}
	// Declare edges. Iterate intents in stable order so the rendered Markdown
	// is deterministic across runs (the rendered doc is committed as a work
	// product).
	walkStates(r.def.States, "", func(path string, st *app.State) {
		if storyauthoring.IsFrameworkRoom(path) {
			return
		}
		for _, intent := range stableKeys(st.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range st.On[intent] {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" {
					continue
				}
				label := intent
				if tr.When != "" {
					label = fmt.Sprintf("%s [%s]", intent, tr.When)
				} else if tr.Default {
					label = intent + " (default)"
				}
				r.ln(fmt.Sprintf("  %s -->|%s| %s", mermaidID(path), escapeMermaidLabel(label), mermaidID(target)))
			}
		}
	})
	r.ln("```")
	r.ln("")
}

func (r *renderer) worldVars() {
	if len(r.def.World) == 0 {
		return
	}
	r.ln("## World Variables")
	r.ln("")
	r.ln("| Name | Type | Default | Values |")
	r.ln("|---|---|---|---|")
	for _, k := range renderableWorldKeys(r.def.World) {
		v := r.def.World[k]
		r.ln(fmt.Sprintf("| `%s` | `%s` | %s | %s |",
			k, v.Type, formatDefault(v.Default), formatValues(v.Values)))
	}
	r.ln("")
}

func (r *renderer) intents() {
	if len(r.def.Intents) == 0 {
		return
	}
	r.ln("## Intents")
	r.ln("")
	for _, name := range renderableIntentNames(r.def.Intents) {
		in := r.def.Intents[name]
		title := in.Title
		if title == "" {
			title = name
		}
		r.ln(fmt.Sprintf("### <a id=\"intent-%s\"></a> `%s` — %s", anchorSlug(name), name, title))
		r.ln("")
		if in.Description != "" {
			r.ln(in.Description)
			r.ln("")
		}
		var bullets []string
		if in.Priority != 0 {
			bullets = append(bullets, fmt.Sprintf("Priority **%d**", in.Priority))
		}
		if in.Hidden {
			bullets = append(bullets, "Hidden (not shown in default menu)")
		}
		if len(in.Examples) > 0 {
			bullets = append(bullets, "Examples: "+backtickList(in.Examples))
		}
		for _, b := range bullets {
			r.ln("- " + b)
		}
		if len(bullets) > 0 {
			r.ln("")
		}
		if len(in.Slots) > 0 {
			r.ln("**Slots**:")
			r.ln("")
			r.ln("| Name | Type | Required | Default | Values | Description |")
			r.ln("|---|---|---|---|---|---|")
			for _, sn := range stableKeys(in.Slots) {
				sl := in.Slots[sn]
				req := ""
				if sl.Required {
					req = "yes"
				}
				r.ln(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s | %s |",
					sn, sl.Type, req,
					formatDefault(sl.Default),
					formatValues(sl.Values),
					mdEscape(sl.Description)))
			}
			r.ln("")
		}
	}
}

func (r *renderer) rooms() {
	if len(r.def.States) == 0 {
		return
	}
	r.ln("## Rooms")
	r.ln("")
	rootName, _ := r.def.Root.(string)
	walkStates(r.def.States, "", func(path string, st *app.State) {
		if storyauthoring.IsFrameworkRoom(path) {
			return
		}
		r.room(path, st, path == rootName)
	})
}

func (r *renderer) room(path string, st *app.State, isRoot bool) {
	headline := path
	var flags []string
	if isRoot {
		flags = append(flags, "root")
	}
	if st.Terminal {
		flags = append(flags, "terminal")
	}
	if st.Type == "compound" {
		flags = append(flags, "compound")
	}
	if st.Type == "parallel" {
		flags = append(flags, "parallel")
	}
	if st.Mode != "" {
		flags = append(flags, "mode: "+st.Mode)
	}
	suffix := ""
	if len(flags) > 0 {
		suffix = "  _(" + strings.Join(flags, ", ") + ")_"
	}
	r.ln(fmt.Sprintf("### <a id=\"room-%s\"></a> `%s`%s", anchorSlug(path), headline, suffix))
	r.ln("")
	if st.Description != "" {
		r.ln(st.Description)
		r.ln("")
	}
	if st.Initial != "" {
		r.ln(fmt.Sprintf("**Initial child**: `%s`", st.Initial))
		r.ln("")
	}
	if len(st.RelevantWorld) > 0 {
		r.ln("**Shows world**: " + backtickList(st.RelevantWorld))
		r.ln("")
	}
	if len(st.RelevantSlots) > 0 {
		r.ln("**Shows slots**: " + backtickList(st.RelevantSlots))
		r.ln("")
	}
	if len(st.Menu) > 0 {
		r.ln("**Menu**: " + backtickList(st.Menu))
		r.ln("")
	}
	if len(st.Prerequisites) > 0 {
		r.ln("**Prerequisites**:")
		r.ln("")
		for _, pr := range st.Prerequisites {
			suffix := ""
			if pr.SatisfiedWhen != "" {
				suffix = " — `" + pr.SatisfiedWhen + "`"
			}
			r.ln(fmt.Sprintf("- `%s`: %s%s", pr.ID, mdEscape(pr.Title), suffix))
		}
		r.ln("")
	}
	if src := st.View.SourceString(); src != "" {
		r.ln("**View**:")
		r.ln("")
		r.ln("```")
		r.ln(strings.TrimRight(src, "\n"))
		r.ln("```")
		r.ln("")
	}
	if len(st.OnEnter) > 0 {
		r.ln("**On enter**:")
		r.ln("")
		for i, eff := range st.OnEnter {
			r.ln(fmt.Sprintf("%d. %s", i+1, formatEffect(eff)))
		}
		r.ln("")
	}
	if visibleTransitionCount(path, st) > 0 {
		r.ln("**Transitions**:")
		r.ln("")
		r.ln("| # | Intent | Guard | → | Effects |")
		r.ln("|---|---|---|---|---|")
		idx := 0
		for _, intent := range stableKeys(st.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range st.On[intent] {
				idx++
				r.ln(transitionRow(idx, intent, tr))
			}
		}
		r.ln("")
	}
	if st.Timeout != nil {
		r.ln(fmt.Sprintf("**Timeout**: after `%s` → `%s`", st.Timeout.After, st.Timeout.Target))
		r.ln("")
	}
}

func (r *renderer) offPath() {
	if r.def.OffPath == nil {
		return
	}
	op := r.def.OffPath
	r.ln("## Off-path Escape Hatch")
	r.ln("")
	if op.Trigger != "" {
		r.ln(fmt.Sprintf("- Trigger: `%s`", op.Trigger))
	}
	if op.Banner != "" {
		r.ln(fmt.Sprintf("- Banner: %q", op.Banner))
	}
	if op.Return != "" {
		r.ln(fmt.Sprintf("- Return: `%s`", op.Return))
	}
	r.ln("")
}

func (r *renderer) generated() {
	r.ln("---")
	r.ln("")
	r.ln("_Generated from `app.yaml` by `kitsoki render`. Do not edit this file directly — edit `app.yaml` and re-run `kitsoki render`. See `kitsoki docs apply-proposal` for the LLM-driven proposal workflow._")
}

// ---- Helpers ---------------------------------------------------------------

func (r *renderer) ln(s string) {
	r.w.WriteString(s)
	r.w.WriteByte('\n')
}

func transitionRow(idx int, intent string, tr app.Transition) string {
	target := tr.Target
	targetCell := "`" + target + "`"
	if !strings.HasPrefix(target, "{{") && !strings.Contains(target, "/") && !strings.HasPrefix(target, ".") && target != "" {
		// Link to a declared room anchor when the target is a bare state name.
		targetCell = fmt.Sprintf("[`%s`](#room-%s)", target, anchorSlug(target))
	}

	intentCell := "`" + intent + "`"
	if intent != "*" {
		intentCell = fmt.Sprintf("[`%s`](#intent-%s)", intent, anchorSlug(intent))
	}

	guard := ""
	if tr.When != "" {
		guard = "`" + mdEscape(tr.When) + "`"
	}
	if tr.Default {
		if guard != "" {
			guard += " · "
		}
		guard += "_default_"
	}

	var effectBits []string
	if tr.GuardHint != "" {
		effectBits = append(effectBits, "_hint: "+mdEscape(tr.GuardHint)+"_")
	}
	for _, eff := range tr.Effects {
		effectBits = append(effectBits, formatEffect(eff))
	}
	if tr.PushHistory != nil && !*tr.PushHistory {
		effectBits = append(effectBits, "_(no-history)_")
	}
	effects := strings.Join(effectBits, " · ")

	return fmt.Sprintf("| %d | %s | %s | %s | %s |", idx, intentCell, guard, targetCell, effects)
}

func visibleTransitionCount(path string, st *app.State) int {
	if st == nil {
		return 0
	}
	count := 0
	for intent, transitions := range st.On {
		if storyauthoring.IsFrameworkTransition(path, intent) {
			continue
		}
		count += len(transitions)
	}
	return count
}

func formatEffect(eff app.Effect) string {
	switch {
	case len(eff.Set) > 0:
		return "set " + joinKV(eff.Set)
	case len(eff.Increment) > 0:
		parts := make([]string, 0, len(eff.Increment))
		for _, k := range stableKeys(eff.Increment) {
			parts = append(parts, fmt.Sprintf("`%s += %d`", k, eff.Increment[k]))
		}
		return "increment " + strings.Join(parts, ", ")
	case eff.Say != "":
		return fmt.Sprintf("say %q", eff.Say)
	case eff.Emit != "":
		return fmt.Sprintf("emit `%s`", eff.Emit)
	case eff.Invoke != "":
		s := fmt.Sprintf("invoke `%s`", eff.Invoke)
		if len(eff.With) > 0 {
			s += " with " + joinKV(eff.With)
		}
		if len(eff.Bind) > 0 {
			parts := make([]string, 0, len(eff.Bind))
			for _, k := range stableKeys(eff.Bind) {
				parts = append(parts, fmt.Sprintf("`%s ← %s`", k, eff.Bind[k]))
			}
			s += ", bind " + strings.Join(parts, ", ")
		}
		if eff.OnError != "" {
			s += fmt.Sprintf(", on_error → `%s`", eff.OnError)
		}
		return s
	}
	return ""
}

func joinKV(m map[string]any) string {
	parts := make([]string, 0, len(m))
	for _, k := range stableKeys(m) {
		parts = append(parts, fmt.Sprintf("`%s = %s`", k, formatValue(m[k])))
	}
	return strings.Join(parts, ", ")
}

func formatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return `""`
	case string:
		return strconv.Quote(x)
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}

func formatDefault(v any) string {
	if v == nil {
		return ""
	}
	return "`" + fmt.Sprintf("%v", v) + "`"
}

func formatValues(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return backtickList(vs)
}

func backtickList(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = "`" + s + "`"
	}
	return strings.Join(parts, ", ")
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// anchorSlug converts a state/intent name into an anchor-safe slug.
// Dotted paths (bar.dark) become hyphenated (bar-dark).
func anchorSlug(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, "/", "-")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func mermaidID(path string) string {
	return strings.NewReplacer(".", "__", "-", "_", "/", "__").Replace(path)
}

func escapeMermaidLabel(s string) string {
	s = strings.ReplaceAll(s, `"`, `'`)
	s = strings.ReplaceAll(s, "|", "/")
	return s
}

// resolveMermaidTarget resolves a transition target (which may be a
// dot/slash path like "../../foyer" or "." for self) against the enclosing
// state path, returning an absolute state path suitable for the diagram.
// Returns "" for template expressions (e.g. "{{ world.prev_state }}").
func resolveMermaidTarget(from, target string) string {
	if target == "" {
		return ""
	}
	if strings.Contains(target, "{{") {
		return ""
	}
	if target == "." {
		return from
	}
	if strings.Contains(target, "/") {
		// Resolve slash-style relative path segments.
		segs := strings.Split(from, ".")
		for _, part := range strings.Split(target, "/") {
			switch part {
			case "", ".":
				// no-op
			case "..":
				if len(segs) > 0 {
					segs = segs[:len(segs)-1]
				}
			default:
				segs = append(segs, part)
			}
		}
		return strings.Join(segs, ".")
	}
	return target
}

func walkStates(states map[string]*app.State, prefix string, fn func(path string, st *app.State)) {
	for _, k := range stableKeys(states) {
		st := states[k]
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		fn(path, st)
		if len(st.States) > 0 {
			walkStates(st.States, path, fn)
		}
	}
}

func countStates(states map[string]*app.State) int {
	n := 0
	walkStates(states, "", func(path string, _ *app.State) {
		if storyauthoring.IsFrameworkRoom(path) {
			return
		}
		n++
	})
	return n
}

func renderableIntentNames(intents map[string]app.Intent) []string {
	names := stableKeys(intents)
	out := names[:0]
	for _, name := range names {
		if storyauthoring.IsFrameworkIntent(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

func renderableWorldKeys(vars map[string]app.VarDef) []string {
	keys := stableKeys(vars)
	out := keys[:0]
	for _, key := range keys {
		if storyauthoring.IsFrameworkWorldKey(key) {
			continue
		}
		out = append(out, key)
	}
	return out
}

func stableKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
