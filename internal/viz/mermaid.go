// Package viz — mermaid.go implements a Mermaid stateDiagram-v2 exporter
// (§9a.7, §12.1 Visualizer.Mermaid). Templated string generator, no library.
//
// First cut — intentionally minimal so we can iterate. Handles:
//   - top-level atomic + compound states
//   - initial-state arrow [*] --> root
//   - terminal states with --> [*]
//   - intent-labelled transitions, optional [guard] suffix
//   - self-edges, wildcard intent (*), default branch
//
// Not (yet) handled: parallel regions, cross-compound paths drawn as
// dotted edges, view/effect annotations, on_enter, timeouts.
package viz

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"hally/internal/app"
)

// ExportMermaid writes a Mermaid stateDiagram-v2 representation of the app to w.
//
// For large apps (e.g. dev-story, ~200 states) mermaid-cli's default limits
// (maxTextSize=50000, maxEdges=500) reject the diagram. Pass a config file
// to mmdc:
//
//	mmdc -c <(echo '{"maxTextSize":5000000,"maxEdges":50000}') -i x.mmd -o x.svg
//
// (Inline %%{init: ... }%% directives don't lift these particular limits in
// current mermaid-cli — the check runs before the directive is parsed.)
func ExportMermaid(a *app.AppDef, w io.Writer) error {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	b.WriteString("  direction LR\n")
	if a.App.Title != "" {
		b.WriteString("  %% " + a.App.Title + "\n")
	}

	initialState, _ := a.Root.(string)
	if initialState != "" {
		fmt.Fprintf(&b, "  [*] --> %s\n", mermaidStateID(initialState))
	}

	// Emit state declarations and nested compound blocks.
	emitStates(&b, a.States, "", "  ")

	// Emit transition edges flat (mermaid resolves IDs globally).
	walkAllStates(a.States, "", func(path string, s *app.State) {
		emitTransitions(&b, path, s)
	})

	_, err := io.WriteString(w, b.String())
	return err
}

// MermaidBytes returns the Mermaid source as a byte slice.
func MermaidBytes(a *app.AppDef) ([]byte, error) {
	var sb strings.Builder
	if err := ExportMermaid(a, &sb); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// emitStates emits state declarations recursively. Compound states open a
// `state "label" as id { ... }` block and recurse; atomic states emit a
// single declaration line. Children of a compound state get an inner
// initial-arrow when the parent declares `initial:`.
func emitStates(b *strings.Builder, states map[string]*app.State, prefix, indent string) {
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		fullPath := joinDot(prefix, name)
		id := mermaidStateID(fullPath)
		label := fullPath

		if len(s.States) > 0 {
			fmt.Fprintf(b, "%sstate %q as %s {\n", indent, label, id)
			fmt.Fprintf(b, "%s  direction LR\n", indent)
			// Initial-child arrow inside the compound block. `initial:`
			// may be a template expression; only emit when literal.
			if init := s.Initial; init != "" && !strings.Contains(init, "{{") {
				childPath := joinDot(fullPath, init)
				fmt.Fprintf(b, "%s  [*] --> %s\n", indent, mermaidStateID(childPath))
			}
			emitStates(b, s.States, fullPath, indent+"  ")
			fmt.Fprintf(b, "%s}\n", indent)
			continue
		}

		fmt.Fprintf(b, "%sstate %q as %s\n", indent, label, id)
		if s.Terminal {
			fmt.Fprintf(b, "%s%s --> [*]\n", indent, id)
		}
	}
}

// emitTransitions emits one edge per transition entry on a state.
func emitTransitions(b *strings.Builder, fromPath string, s *app.State) {
	fromID := mermaidStateID(fromPath)
	for _, intent := range sortedKeys(s.On) {
		for _, tr := range s.On[intent] {
			target := resolveMermaidTarget(fromPath, tr.Target)
			if target == "" || strings.Contains(target, "{{") {
				continue
			}
			toID := mermaidStateID(target)
			label := mermaidEdgeLabel(intent, tr)
			fmt.Fprintf(b, "  %s --> %s : %s\n", fromID, toID, label)
		}
	}
}

// resolveMermaidTarget resolves a transition target (slash-style relative
// like "../../foyer", "." for self, or a bare/dotted absolute name) against
// the enclosing state path, returning an absolute dotted state path.
// Returns "" for template expressions ("{{ ... }}") and empty targets.
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
		segs := strings.Split(from, ".")
		for _, part := range strings.Split(target, "/") {
			switch part {
			case "", ".":
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

func mermaidEdgeLabel(intent string, tr app.Transition) string {
	name := intent
	if name == "*" {
		name = "*(any)"
	}
	switch {
	case tr.When != "":
		return escapeMermaid(name + " [" + truncate(tr.When, 24) + "]")
	case tr.Default:
		return escapeMermaid(name + " (default)")
	}
	return escapeMermaid(name)
}

// mermaidStateID converts a dotted path to a mermaid-safe identifier.
// Dots and slashes become underscores.
func mermaidStateID(path string) string {
	return strings.NewReplacer(".", "_", "/", "_", "-", "_").Replace(path)
}

func escapeMermaid(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, ":", "—") // colon is the edge-label separator
	return s
}

func joinDot(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func walkAllStates(states map[string]*app.State, prefix string, fn func(path string, s *app.State)) {
	for _, name := range sortedKeys(states) {
		s := states[name]
		if s == nil {
			continue
		}
		full := joinDot(prefix, name)
		fn(full, s)
		if len(s.States) > 0 {
			walkAllStates(s.States, full, fn)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
