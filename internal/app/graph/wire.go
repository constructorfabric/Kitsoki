package graph

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/storyauthoring"
)

const SchemaV1 = "kitsoki.graph/v1"

// KitsokiGraph is the renderer-neutral graph wire shape used by graph-oriented
// APIs. It deliberately models Kitsoki domain refs, not Mermaid / Vue Flow /
// Cytoscape / ELK internals; clients adapt it to their renderer of choice.
type KitsokiGraph struct {
	Schema      string         `json:"schema"`
	GraphID     string         `json:"graph_id"`
	Kind        string         `json:"kind"`
	Directed    bool           `json:"directed"`
	Cyclic      bool           `json:"cyclic"`
	LayoutHints LayoutHints    `json:"layout_hints,omitempty"`
	Nodes       []GraphNode    `json:"nodes"`
	Edges       []GraphEdge    `json:"edges"`
	Groups      []GraphGroup   `json:"groups,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
}

// LayoutHints are optional renderer hints, kept separate from the topology.
type LayoutHints struct {
	Default string `json:"default,omitempty"`
	RankDir string `json:"rankdir,omitempty"`
}

// GraphNode is one graph node with a stable Kitsoki ref.
type GraphNode struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Label  string         `json:"label"`
	Ref    GraphRef       `json:"ref"`
	Group  string         `json:"group,omitempty"`
	Status string         `json:"status,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

// GraphRef identifies the Kitsoki source entity a graph element represents.
type GraphRef struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// GraphEdge is one directed relationship between nodes.
type GraphEdge struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Source string         `json:"source"`
	Target string         `json:"target"`
	Label  string         `json:"label,omitempty"`
	Status string         `json:"status,omitempty"`
	Attrs  map[string]any `json:"attrs,omitempty"`
}

// GraphGroup is a coarse grouping such as a room. Renderers may map groups to
// compound nodes, swimlanes, filters, or ignore them.
type GraphGroup struct {
	ID    string         `json:"id"`
	Kind  string         `json:"kind"`
	Label string         `json:"label"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// RoomGraph returns the top-level room state machine as nodes and transition
// edges. Cycles and self-loops are kept as explicit edges; layout policy is left
// to clients.
func RoomGraph(a app.App, graphID string) KitsokiGraph {
	rooms := topLevelRooms(a)
	dist := roomDistances(a, rooms)

	g := KitsokiGraph{
		Schema:   SchemaV1,
		GraphID:  graphID,
		Kind:     "room-state-machine",
		Directed: true,
		LayoutHints: LayoutHints{
			Default: "layered",
			RankDir: "LR",
		},
		Nodes:  make([]GraphNode, 0, len(rooms)),
		Groups: make([]GraphGroup, 0, len(rooms)),
	}

	for _, id := range rooms {
		st, _ := a.LookupState(app.StatePath(id))
		attrs := map[string]any{"has_agent": roomHasAgent(st)}
		if d, ok := dist[id]; ok {
			attrs["distance"] = d
		} else {
			attrs["distance"] = unreachableDistance
			attrs["unreachable"] = true
		}
		nodeID := graphNodeID("state", id)
		g.Nodes = append(g.Nodes, GraphNode{
			ID:    nodeID,
			Kind:  "state",
			Label: roomLabel(id, st),
			Ref:   GraphRef{Kind: "state", Ref: id},
			Group: "room:" + id,
			Attrs: attrs,
		})
		g.Groups = append(g.Groups, GraphGroup{
			ID:    "room:" + id,
			Kind:  "room",
			Label: roomLabel(id, st),
		})
	}

	roomSet := map[string]bool{}
	for _, id := range rooms {
		roomSet[id] = true
	}
	for _, fromRoom := range rooms {
		st, ok := a.LookupState(app.StatePath(fromRoom))
		if !ok {
			continue
		}
		for _, edge := range roomTransitionEdges(fromRoom, st, roomSet) {
			if edge.Source == edge.Target {
				g.Cyclic = true
			}
			g.Edges = append(g.Edges, edge)
		}
	}
	sort.SliceStable(g.Edges, func(i, j int) bool {
		if g.Edges[i].Source != g.Edges[j].Source {
			return g.Edges[i].Source < g.Edges[j].Source
		}
		if g.Edges[i].Target != g.Edges[j].Target {
			return g.Edges[i].Target < g.Edges[j].Target
		}
		if g.Edges[i].Label != g.Edges[j].Label {
			return g.Edges[i].Label < g.Edges[j].Label
		}
		return g.Edges[i].ID < g.Edges[j].ID
	})
	if !g.Cyclic {
		g.Cyclic = hasDirectedCycle(g.Nodes, g.Edges)
	}
	return g
}

func roomTransitionEdges(fromRoom string, st *app.State, roomSet map[string]bool) []GraphEdge {
	var out []GraphEdge
	var walk func(path string, s *app.State)
	walk = func(path string, s *app.State) {
		if s == nil {
			return
		}
		for intent, transitions := range s.On {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for i, tr := range transitions {
				target := resolveGraphTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				toRoom := string(app.StatePath(target).TopLevel())
				if toRoom == "" || !roomSet[toRoom] {
					continue
				}
				attrs := map[string]any{
					"intent":           intent,
					"source_ref":       path,
					"target_ref":       target,
					"transition_index": i,
				}
				if tr.When != "" {
					attrs["when"] = tr.When
				}
				out = append(out, GraphEdge{
					ID:     graphEdgeID(fromRoom, intent, toRoom, path, i),
					Kind:   "transition",
					Source: graphNodeID("state", fromRoom),
					Target: graphNodeID("state", toRoom),
					Label:  intent,
					Attrs:  attrs,
				})
			}
		}
		childIDs := make([]string, 0, len(s.States))
		for id := range s.States {
			childIDs = append(childIDs, id)
		}
		sort.Strings(childIDs)
		for _, id := range childIDs {
			childPath := id
			if path != "" {
				childPath = path + "." + id
			}
			walk(childPath, s.States[id])
		}
	}
	walk(fromRoom, st)
	return out
}

func resolveGraphTarget(from, target string) string {
	if target == "" || target == "." || strings.HasPrefix(target, "@") || strings.HasPrefix(target, "__exit__") {
		return ""
	}
	if !strings.HasPrefix(target, "..") {
		return strings.ReplaceAll(target, "/", ".")
	}
	parts := strings.Split(from, ".")
	segs := strings.Split(target, "/")
	for _, seg := range segs {
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, ".")
}

func graphNodeID(kind, ref string) string {
	return kind + ":" + ref
}

func graphEdgeID(fromRoom, intent, toRoom, sourcePath string, index int) string {
	return fmt.Sprintf("transition:%s:%s:%s:%s:%d", fromRoom, intent, toRoom, sourcePath, index)
}

func hasDirectedCycle(nodes []GraphNode, edges []GraphEdge) bool {
	adj := map[string][]string{}
	for _, n := range nodes {
		adj[n.ID] = nil
	}
	for _, e := range edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
	}
	const (
		unseen = 0
		active = 1
		done   = 2
	)
	seen := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		switch seen[id] {
		case active:
			return true
		case done:
			return false
		}
		seen[id] = active
		for _, next := range adj[id] {
			if visit(next) {
				return true
			}
		}
		seen[id] = done
		return false
	}
	for id := range adj {
		if seen[id] == unseen && visit(id) {
			return true
		}
	}
	return false
}
