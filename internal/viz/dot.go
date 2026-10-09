// dot.go implements the Graphviz DOT exporter, the one mode that builds its
// graph through github.com/emicklei/dot rather than hand-rolled string
// templating. See doc.go for the package overview.
package viz

import (
	"io"
	"strings"

	dotlib "github.com/emicklei/dot"

	"kitsoki/internal/app"
	"kitsoki/internal/storyauthoring"
)

// Export streams a Graphviz DOT state graph for a to w: compound states become
// cluster subgraphs, leaf states become boxes (terminal states a filled
// double-circle), and transitions become labelled edges from an invisible
// start node into the initial state. Returns w's write error. Use [DOTBytes]
// for the in-memory form.
func Export(a *app.AppDef, w io.Writer) error {
	g := dotlib.NewGraph(dotlib.Directed)
	g.Attr("rankdir", "LR")
	if a.App.Title != "" {
		g.Attr("label", a.App.Title)
	}
	g.Attr("fontname", "Helvetica")

	// Determine initial state.
	initialState := ""
	if s, ok := a.Root.(string); ok {
		initialState = s
	}

	// Invisible start node for the initial-state arrow.
	start := g.Node("__start__")
	start.Attr("shape", "point")
	start.Attr("style", "invis")

	// Walk states into the root graph.
	addStatesToGraph(g, g, a.States, "", initialState, start)

	_, err := io.WriteString(w, g.String())
	return err
}

// DOTBytes is the in-memory form of [Export], returning the DOT source as
// bytes. The error is whatever [Export] reports.
func DOTBytes(a *app.AppDef) ([]byte, error) {
	var sb strings.Builder
	if err := Export(a, &sb); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// addStatesToGraph recursively adds states and their transitions.
// parent is the *dot.Graph that owns the nodes (root or a cluster subgraph).
func addStatesToGraph(
	root *dotlib.Graph,
	parent *dotlib.Graph,
	states map[string]*app.State,
	prefix string,
	initialState string,
	startNode dotlib.Node,
) {
	for name, s := range states {
		if s == nil {
			continue
		}
		fullPath := joinPath(prefix, name)
		if storyauthoring.IsFrameworkRoom(fullPath) {
			continue
		}

		if len(s.States) > 0 {
			// Compound state: cluster subgraph. Parallel parents get a
			// different visual cue so the sibling-region structure reads.
			clusterID := "cluster_" + strings.ReplaceAll(fullPath, ".", "_")
			sg := parent.Subgraph(clusterID, dotlib.ClusterOption{})
			label := fullPath
			if s.Type == "parallel" {
				label = fullPath + " (parallel)"
				sg.Attr("style", "filled,rounded")
				sg.Attr("fillcolor", "lightblue:white")
				sg.Attr("color", "steelblue")
			} else {
				sg.Attr("style", "dashed")
				sg.Attr("color", "grey50")
			}
			sg.Attr("label", label)
			sg.Attr("fontname", "Helvetica")

			// Recurse into children.
			addStatesToGraph(root, sg, s.States, fullPath, initialState, startNode)

			// Add transition edges for this compound level.
			addTransitionEdges(root, s, fullPath)
		} else {
			// Leaf state: node.
			nodeID := strings.ReplaceAll(fullPath, ".", "_")
			n := parent.Node(nodeID)
			n.Attr("label", fullPath)
			n.Attr("fontname", "Helvetica")
			n.Attr("shape", "box")
			n.Attr("style", "rounded")

			if s.Terminal {
				n.Attr("shape", "doublecircle")
				n.Attr("style", "filled")
				n.Attr("fillcolor", "lightyellow")
			}

			// Arrow from start to initial state.
			if fullPath == initialState {
				root.Edge(startNode, n)
			}

			// Outgoing transitions.
			addTransitionEdges(root, s, fullPath)
		}
	}
}

// addTransitionEdges adds directed edges for every transition in a state.
func addTransitionEdges(g *dotlib.Graph, s *app.State, fromPath string) {
	fromID := strings.ReplaceAll(fromPath, ".", "_")
	for intentName, transitions := range s.On {
		if storyauthoring.IsFrameworkTransition(fromPath, intentName) {
			continue
		}
		label := intentName
		if label == "*" {
			label = "*(any)"
		}
		for _, tr := range transitions {
			target := resolveTransitionTarget(fromPath, tr.Target)
			toID := strings.ReplaceAll(target, ".", "_")

			fromNode := g.Node(fromID)
			toNode := g.Node(toID)
			e := g.Edge(fromNode, toNode)

			edgeLabel := label
			if tr.When != "" {
				edgeLabel += "\n[" + truncate(tr.When, 20) + "]"
			}
			e.Attr("label", edgeLabel)
			e.Attr("fontsize", "9")
			e.Attr("fontname", "Helvetica")
		}
	}
}

// joinPath joins a prefix and name with a dot separator.
func joinPath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// truncate truncates s to at most n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// resolveTransitionTarget resolves a transition target string relative to the
// owning state path. Handles: "" / "." (self), "../" relative, absolute.
func resolveTransitionTarget(statePath, target string) string {
	if target == "" || target == "." {
		return statePath
	}
	target = strings.ReplaceAll(target, "/", ".")
	if !strings.HasPrefix(target, "..") {
		return target // absolute
	}
	// Relative.
	parts := strings.Split(statePath, ".")
	segs := strings.Split(target, ".")
	for _, seg := range segs {
		switch seg {
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		case "", ".":
			// skip
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, ".")
}
