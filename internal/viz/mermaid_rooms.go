// Package viz — mermaid_rooms.go: per-room Mermaid diagrams + overview.
//
// The single-flat diagram is unreadable for apps with many states (devstory
// has ~200). This emits an overview diagram (rooms-only) plus one detail
// diagram per room. A "room" is:
//
//   - The top-level compound state, if a state lives inside one (e.g. cloak's
//     `bar` covers `bar.dark` and `bar.lit`).
//   - Otherwise, the prefix before the first `_` in the state name (so
//     `bugfix_idle` and `bugfix_repro_executing` group into room `bugfix`).
//
// Cross-room transitions are rendered as edges into a stub external node
// labelled "→ <room>" inside the detail diagram. The overview diagram
// collapses every room to one node and aggregates inter-room intents.
package viz

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// Rooms is the result of grouping an AppDef into rooms.
type Rooms struct {
	// Order is the deterministic display order for rooms.
	Order []string
	// Members maps room → ordered absolute state paths in that room.
	Members map[string][]string
	// RoomOf maps absolute state path → room name.
	RoomOf map[string]string
}

// GroupRooms walks the state graph and returns a room grouping.
func GroupRooms(def *app.AppDef) *Rooms {
	r := &Rooms{
		Members: map[string][]string{},
		RoomOf:  map[string]string{},
	}
	walkAllStates(def.States, "", func(path string, _ *app.State) {
		room := roomOf(def, path)
		r.RoomOf[path] = room
		r.Members[room] = append(r.Members[room], path)
	})
	for room, paths := range r.Members {
		sort.Strings(paths)
		r.Members[room] = paths
	}
	for room := range r.Members {
		r.Order = append(r.Order, room)
	}
	sort.Strings(r.Order)
	return r
}

func roomOf(def *app.AppDef, path string) string {
	first := strings.SplitN(path, ".", 2)[0]
	if s, ok := def.States[first]; ok && len(s.States) > 0 {
		return first
	}
	if i := strings.IndexByte(first, '_'); i > 0 {
		return first[:i]
	}
	return first
}

// ExportMermaidRooms writes a directory of per-room Mermaid files plus an
// `_overview.mmd` and `index.md`. outDir is created if needed.
func ExportMermaidRooms(def *app.AppDef, outDir string, mkdir func(string) error, write func(path string, data []byte) error) error {
	if err := mkdir(outDir); err != nil {
		return err
	}
	rooms := GroupRooms(def)
	rootName, _ := def.Root.(string)
	rootRoom := rooms.RoomOf[rootName]
	teleports := detectTeleportRooms(def, rooms)

	overview, err := overviewMermaid(def, rooms, rootRoom, teleports)
	if err != nil {
		return fmt.Errorf("overview: %w", err)
	}
	if err := write(filepath.Join(outDir, "_overview.mmd"), overview); err != nil {
		return err
	}

	for _, room := range rooms.Order {
		body, err := roomDetailMermaid(def, rooms, room, rootName, teleports)
		if err != nil {
			return fmt.Errorf("room %q: %w", room, err)
		}
		if err := write(filepath.Join(outDir, room+".mmd"), body); err != nil {
			return err
		}
	}

	idx := indexMarkdown(def, rooms, teleports)
	return write(filepath.Join(outDir, "index.md"), idx)
}

// detectTeleportRooms returns the set of rooms that are linked to from a
// majority of other rooms — these are "global teleport" exits (e.g.
// `main`, `inbox`, back-buttons). Their edges dominate the visual noise
// and carry near-zero information; we de-emphasize them.
func detectTeleportRooms(def *app.AppDef, rooms *Rooms) map[string]bool {
	// inbound[r] = set of distinct other rooms that link to r.
	inbound := map[string]map[string]struct{}{}
	walkAllStates(def.States, "", func(path string, s *app.State) {
		if s == nil {
			return
		}
		from := rooms.RoomOf[path]
		for _, trs := range s.On {
			for _, tr := range trs {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				to, ok := rooms.RoomOf[target]
				if !ok || to == from {
					continue
				}
				if inbound[to] == nil {
					inbound[to] = map[string]struct{}{}
				}
				inbound[to][from] = struct{}{}
			}
		}
	})
	out := map[string]bool{}
	threshold := (len(rooms.Order) - 1) / 2 // > 50% of *other* rooms link in
	for room, sources := range inbound {
		if len(sources) > threshold {
			out[room] = true
		}
	}
	return out
}

// overviewMermaid renders a one-node-per-room diagram with edges for any
// intent that ever crosses a room boundary. Edges into "global teleport"
// rooms (those linked to by a majority of other rooms) are suppressed —
// their target nodes get a "(teleport)" tag instead so the reader knows
// every room can reach them.
func overviewMermaid(def *app.AppDef, rooms *Rooms, rootRoom string, teleports map[string]bool) ([]byte, error) {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	b.WriteString("  direction LR\n")
	if def.App.Title != "" {
		b.WriteString("  %% " + def.App.Title + " — overview\n")
	}
	if len(teleports) > 0 {
		b.WriteString("  %% teleport rooms (linked from a majority of rooms): " +
			strings.Join(sortedSetKeys(toSet(teleports)), ", ") + "\n")
	}
	if rootRoom != "" {
		fmt.Fprintf(&b, "  [*] --> %s\n", mermaidStateID(rootRoom))
	}

	for _, room := range rooms.Order {
		count := len(rooms.Members[room])
		label := fmt.Sprintf("%s (%d)", room, count)
		if teleports[room] {
			label += " ★"
		}
		fmt.Fprintf(&b, "  state %q as %s\n", label, mermaidStateID(room))
	}

	// Aggregate cross-room intents: from -> to -> set(intent).
	type pair struct{ from, to string }
	intents := map[pair]map[string]struct{}{}
	walkAllStates(def.States, "", func(path string, s *app.State) {
		if s == nil {
			return
		}
		fromRoom := rooms.RoomOf[path]
		for intent, trs := range s.On {
			for _, tr := range trs {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				toRoom, ok := rooms.RoomOf[target]
				if !ok || toRoom == fromRoom {
					continue
				}
				if teleports[toRoom] {
					continue // suppress noise
				}
				p := pair{fromRoom, toRoom}
				if intents[p] == nil {
					intents[p] = map[string]struct{}{}
				}
				intents[p][intent] = struct{}{}
			}
		}
	})

	// Emit edges in deterministic order.
	pairs := make([]pair, 0, len(intents))
	for p := range intents {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].from != pairs[j].from {
			return pairs[i].from < pairs[j].from
		}
		return pairs[i].to < pairs[j].to
	})
	for _, p := range pairs {
		names := sortedSetKeys(intents[p])
		label := strings.Join(names, ", ")
		if len(label) > 60 {
			label = fmt.Sprintf("%d intents", len(names))
		}
		fmt.Fprintf(&b, "  %s --> %s : %s\n",
			mermaidStateID(p.from), mermaidStateID(p.to), escapeMermaid(label))
	}
	return []byte(b.String()), nil
}

func toSet(m map[string]bool) map[string]struct{} {
	out := make(map[string]struct{}, len(m))
	for k, v := range m {
		if v {
			out[k] = struct{}{}
		}
	}
	return out
}

// roomDetailMermaid renders the internal states of one room plus stub
// external-room nodes for cross-room transitions. Edges into teleport
// rooms (global navigation) are folded into a single "exits → teleports"
// summary so they don't dominate the layout.
func roomDetailMermaid(def *app.AppDef, rooms *Rooms, room, rootName string, teleports map[string]bool) ([]byte, error) {
	var b strings.Builder
	b.WriteString("stateDiagram-v2\n")
	b.WriteString("  direction LR\n")
	title := def.App.Title
	if title != "" {
		fmt.Fprintf(&b, "  %%%% %s — room: %s\n", title, room)
	} else {
		fmt.Fprintf(&b, "  %%%% room: %s\n", room)
	}

	// Internal state declarations.
	internal := map[string]bool{}
	for _, p := range rooms.Members[room] {
		internal[p] = true
		st := lookupState(def, p)
		id := mermaidStateID(p)
		fmt.Fprintf(&b, "  state %q as %s\n", p, id)
		if st != nil && st.Terminal {
			fmt.Fprintf(&b, "  %s --> [*]\n", id)
		}
		if p == rootName {
			fmt.Fprintf(&b, "  [*] --> %s\n", id)
		}
	}

	// External room stubs (non-teleport) — declared once per other-room target.
	externalRooms := map[string]bool{}
	// Track which teleport rooms are reachable from this room overall, so we
	// can show one summary line in a header comment instead of per-state stubs.
	teleportTargets := map[string]struct{}{}

	type edge struct {
		fromID, toID, label string
	}
	var edges []edge

	for _, p := range rooms.Members[room] {
		st := lookupState(def, p)
		if st == nil {
			continue
		}
		fromID := mermaidStateID(p)
		for _, intent := range sortedKeys(st.On) {
			for _, tr := range st.On[intent] {
				target := resolveMermaidTarget(p, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				label := mermaidEdgeLabel(intent, tr)
				if internal[target] {
					edges = append(edges, edge{fromID, mermaidStateID(target), label})
					continue
				}
				toRoom, ok := rooms.RoomOf[target]
				if !ok {
					continue
				}
				if teleports[toRoom] {
					teleportTargets[toRoom] = struct{}{}
					continue // suppressed: noted in header
				}
				stubID := "ext_" + mermaidStateID(toRoom)
				if !externalRooms[toRoom] {
					externalRooms[toRoom] = true
					fmt.Fprintf(&b, "  state %q as %s\n",
						"→ "+toRoom, stubID)
				}
				edges = append(edges, edge{fromID, stubID, label + " → " + target})
			}
		}
	}

	if len(teleportTargets) > 0 {
		fmt.Fprintf(&b, "  %%%% teleport exits suppressed: ★ %s (available from most states)\n",
			strings.Join(sortedSetKeys(teleportTargets), ", "))
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].fromID != edges[j].fromID {
			return edges[i].fromID < edges[j].fromID
		}
		if edges[i].toID != edges[j].toID {
			return edges[i].toID < edges[j].toID
		}
		return edges[i].label < edges[j].label
	})
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s --> %s : %s\n", e.fromID, e.toID, e.label)
	}
	return []byte(b.String()), nil
}

// indexMarkdown renders a small markdown index linking the room files.
func indexMarkdown(def *app.AppDef, rooms *Rooms, teleports map[string]bool) []byte {
	var b strings.Builder
	title := def.App.Title
	if title == "" {
		title = def.App.ID
	}
	fmt.Fprintf(&b, "# %s — room map\n\n", title)
	fmt.Fprintf(&b, "Generated by `kitsoki viz --mermaid --rooms`. Open `_overview.mmd` for the cross-room map; one file per room for detail.\n\n")
	if len(teleports) > 0 {
		fmt.Fprintf(&b, "Rooms marked ★ are *teleport* rooms — linked from a majority of other rooms (e.g. global back-buttons). Their inbound edges are suppressed in the overview, and outbound edges from each room are folded into a single annotation in the detail diagrams.\n\n")
	}
	fmt.Fprintf(&b, "## Overview\n\n")
	fmt.Fprintf(&b, "- [`_overview.mmd`](./_overview.mmd) — %d rooms, only non-teleport inter-room transitions\n\n", len(rooms.Order))
	fmt.Fprintf(&b, "## Rooms\n\n")
	fmt.Fprintf(&b, "| Room | States | File |\n|---|---|---|\n")
	for _, room := range rooms.Order {
		mark := ""
		if teleports[room] {
			mark = " ★"
		}
		fmt.Fprintf(&b, "| `%s`%s | %d | [`%s.mmd`](./%s.mmd) |\n", room, mark, len(rooms.Members[room]), room, room)
	}
	b.WriteString("\n")
	b.WriteString("## Render\n\n")
	b.WriteString("```sh\n")
	b.WriteString("# one PNG per room\n")
	b.WriteString("for f in *.mmd; do\n")
	b.WriteString("  mmdc -c <(echo '{\"maxTextSize\":5000000,\"maxEdges\":50000}') \\\n")
	b.WriteString("       -w 4000 -H 3000 -i \"$f\" -o \"${f%.mmd}.png\"\n")
	b.WriteString("done\n")
	b.WriteString("```\n")
	return []byte(b.String())
}

// lookupState resolves an absolute dotted state path against def.States.
func lookupState(def *app.AppDef, path string) *app.State {
	parts := strings.Split(path, ".")
	cur := def.States
	var s *app.State
	for _, p := range parts {
		s = cur[p]
		if s == nil {
			return nil
		}
		cur = s.States
	}
	return s
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Compile-time check we use io.
var _ = io.Discard
