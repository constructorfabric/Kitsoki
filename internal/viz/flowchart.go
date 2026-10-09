// flowchart.go implements the Mermaid flowchart LR exporter. Where the
// stateDiagram-v2 exporter (mermaid.go) shows state-machine topology, this one
// shows DATA FLOW: rooms as subgraphs, on_enter step chains as hexagon nodes,
// world writes as cylinder nodes — gated by [DetailLevel] and scoped by
// [FlowchartFilter]. The output style matches the hand-drawn bugfix pipeline
// diagrams under stories/bugfix/diagrams/. See doc.go for the overview.
package viz

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/storyauthoring"
)

// DetailLevel controls how much internal structure the flowchart emits. The
// levels are a strict ascending superset chain (rooms ⊂ states ⊂ steps ⊂
// full), so a single graph walk gated on the level produces any of them. The
// zero value is [DetailRooms]; the CLI default is [DetailStates].
type DetailLevel int

const (
	// DetailRooms emits one node per room and cross-room transitions only.
	DetailRooms DetailLevel = iota
	// DetailStates emits states inside room subgraphs plus all transitions (default).
	DetailStates
	// DetailSteps emits on_enter effect chains as hexagon nodes in addition to states.
	DetailSteps
	// DetailFull additionally emits world writes (bind/set) and error targets.
	DetailFull
)

// ParseDetailLevel maps the CLI `--detail` flag value to a [DetailLevel],
// accepting "rooms", "states", "steps", or "full" (case- and
// whitespace-insensitive). On an unknown value it returns [DetailStates] (the
// safe default) together with a descriptive error, so a caller that ignores
// the error still gets a usable level.
func ParseDetailLevel(s string) (DetailLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rooms":
		return DetailRooms, nil
	case "states":
		return DetailStates, nil
	case "steps":
		return DetailSteps, nil
	case "full":
		return DetailFull, nil
	default:
		return DetailStates, fmt.Errorf("unknown detail level %q: must be rooms|states|steps|full", s)
	}
}

// String returns the canonical flag spelling of d, the inverse of
// [ParseDetailLevel]; an out-of-range value renders as "states" so callers
// always get a parseable token.
func (d DetailLevel) String() string {
	switch d {
	case DetailRooms:
		return "rooms"
	case DetailStates:
		return "states"
	case DetailSteps:
		return "steps"
	case DetailFull:
		return "full"
	default:
		return "states"
	}
}

// FlowchartFilter scopes the diagram to a subset of rooms. Its zero value
// selects every room. The two scoping modes are mutually exclusive: set Room
// for a single room, or set both From and To for the rooms on some path
// between them (a reachability slice). Validate before use.
type FlowchartFilter struct {
	Room string // single room name; exclusive with From/To
	From string // start of a range; requires To
	To   string // end of a range; requires From
}

// Validate rejects the two ill-formed filter shapes that [ResolveFilterRooms]
// cannot interpret: combining Room with a From/To range, and setting only one
// end of a range. A zero filter is valid (it selects all rooms).
func (f FlowchartFilter) Validate() error {
	if f.Room != "" && (f.From != "" || f.To != "") {
		return fmt.Errorf("--room cannot be combined with --from/--to")
	}
	if (f.From == "") != (f.To == "") {
		return fmt.Errorf("--from and --to must both be set or both empty")
	}
	return nil
}

// ResolveFilterRooms turns a [FlowchartFilter] into the concrete ordered set
// of rooms to draw, always in [Rooms.Order] for deterministic output. A zero
// filter returns every room; a Room filter returns that one room; a From/To
// range returns the rooms lying on some cross-room path from From to To
// (intersection of forward- and backward-reachable rooms in the room-level
// transition graph). It errors when the filter names a room not present in a.
func ResolveFilterRooms(a *app.AppDef, f FlowchartFilter) ([]string, error) {
	rooms := GroupRooms(a)

	// Zero filter — return all rooms.
	if f.Room == "" && f.From == "" && f.To == "" {
		return rooms.Order, nil
	}

	// Single room filter.
	if f.Room != "" {
		found := false
		for _, r := range rooms.Order {
			if r == f.Room {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("room %q not found in app", f.Room)
		}
		return []string{f.Room}, nil
	}

	// From/To range filter.
	// Validate that both rooms exist.
	roomSet := map[string]bool{}
	for _, r := range rooms.Order {
		roomSet[r] = true
	}
	if !roomSet[f.From] {
		return nil, fmt.Errorf("room %q (--from) not found in app", f.From)
	}
	if !roomSet[f.To] {
		return nil, fmt.Errorf("room %q (--to) not found in app", f.To)
	}

	// Build room-level adjacency graph from cross-room transitions.
	adj := map[string][]string{}  // forward: room → []room
	radj := map[string][]string{} // reverse: room → []room
	adjSeen := map[[2]string]bool{}

	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil {
			return
		}
		fromRoom := rooms.RoomOf[path]
		for _, intent := range sortedKeys(s.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range s.On[intent] {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				toRoom, ok := rooms.RoomOf[target]
				if !ok || toRoom == fromRoom {
					continue
				}
				key := [2]string{fromRoom, toRoom}
				if adjSeen[key] {
					continue
				}
				adjSeen[key] = true
				adj[fromRoom] = append(adj[fromRoom], toRoom)
				radj[toRoom] = append(radj[toRoom], fromRoom)
			}
		}
	})

	// Forward BFS from From.
	forwardReach := map[string]bool{f.From: true}
	queue := []string{f.From}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, next := range adj[cur] {
			if !forwardReach[next] {
				forwardReach[next] = true
				queue = append(queue, next)
			}
		}
	}

	// Backward BFS from To on reversed graph.
	backwardReach := map[string]bool{f.To: true}
	queue = []string{f.To}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, prev := range radj[cur] {
			if !backwardReach[prev] {
				backwardReach[prev] = true
				queue = append(queue, prev)
			}
		}
	}

	// Intersection = rooms on some path from From to To. Use rooms.Order for determinism.
	var selected []string
	for _, r := range rooms.Order {
		if forwardReach[r] && backwardReach[r] {
			selected = append(selected, r)
		}
	}
	return selected, nil
}

// ExportFlowchart is the streaming form of [FlowchartBytes]: it renders the
// flowchart and writes it to w, returning either the render error or w's write
// error. Use [FlowchartBytes] when you need the bytes in memory.
func ExportFlowchart(a *app.AppDef, detail DetailLevel, filter FlowchartFilter, w io.Writer) error {
	b, err := FlowchartBytes(a, detail, filter)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// PipelineOrder returns rooms in BFS discovery order from the initial state,
// which is the human-readable sequence used to label rooms with their position
// (e.g. "phase 2 · reproducing"). It differs from [Rooms.Order]'s alphabetical
// ordering on purpose: a reader follows the flow, not the alphabet. Synthetic
// exit rooms (names starting with "__") are skipped; rooms unreachable from the
// initial state are appended in [Rooms.Order] for determinism.
func PipelineOrder(a *app.AppDef) []string {
	rooms := GroupRooms(a)

	initialState, _ := a.Root.(string)
	initRoom := rooms.RoomOf[initialState]

	// Build cross-room adjacency from transitions.
	adj := map[string][]string{}
	adjSeen := map[[2]string]bool{}

	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil {
			return
		}
		fromRoom := rooms.RoomOf[path]
		for _, intent := range sortedKeys(s.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range s.On[intent] {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				toRoom, ok := rooms.RoomOf[target]
				if !ok || toRoom == fromRoom {
					continue
				}
				key := [2]string{fromRoom, toRoom}
				if adjSeen[key] {
					continue
				}
				adjSeen[key] = true
				adj[fromRoom] = append(adj[fromRoom], toRoom)
			}
		}
	})

	// Sort adjacency lists for determinism.
	for r := range adj {
		sort.Strings(adj[r])
	}

	// BFS from initRoom, skipping synthetic exit rooms.
	var order []string
	visited := map[string]bool{}

	if initRoom != "" && !strings.HasPrefix(initRoom, "__") {
		queue := []string{initRoom}
		visited[initRoom] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			order = append(order, cur)
			for _, next := range adj[cur] {
				if !visited[next] && !strings.HasPrefix(next, "__") {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}
	}

	// Append disconnected rooms not yet visited (in rooms.Order for determinism).
	for _, r := range rooms.Order {
		if !visited[r] && !strings.HasPrefix(r, "__") {
			order = append(order, r)
		}
	}

	return order
}

// FlowchartBytes renders the full Mermaid flowchart source for a — the leading
// %% comments, the flowchart header, room subgraphs and edges at the requested
// detail, and the trailing classDef style block — and returns it as bytes.
// It is the canonical flowchart entry point; [ExportFlowchart] and
// [FlowchartWithMap] build on it. It errors only when filter names an unknown
// room (via [ResolveFilterRooms]).
func FlowchartBytes(a *app.AppDef, detail DetailLevel, filter FlowchartFilter) ([]byte, error) {
	var b strings.Builder
	if a.App.Title != "" {
		fmt.Fprintf(&b, "%%%% %s\n", a.App.Title)
	}
	fmt.Fprintf(&b, "%%%% kitsoki viz --flowchart --detail %s\n", detail)
	b.WriteString("flowchart LR\n")
	b.WriteString("\n")

	// Start node.
	b.WriteString(`  Start(["<b>Start</b>"]):::input` + "\n")
	b.WriteString("\n")

	initialState, _ := a.Root.(string)

	selected, err := ResolveFilterRooms(a, filter)
	if err != nil {
		return nil, err
	}

	// Build pipeline sequence map for room labels.
	pipelineOrder := PipelineOrder(a)
	seqMap := make(map[string]int, len(pipelineOrder))
	for i, r := range pipelineOrder {
		seqMap[r] = i // 0-indexed: first room = phase 0
	}
	total := len(pipelineOrder)

	if detail == DetailRooms {
		if err := emitFlowchartRooms(&b, a, initialState, selected, seqMap, total); err != nil {
			return nil, err
		}
	} else {
		if err := emitFlowchartStates(&b, a, detail, initialState, selected, seqMap, total); err != nil {
			return nil, err
		}
	}

	emitFlowchartClassDefs(&b)
	return []byte(b.String()), nil
}

// fcid converts an arbitrary string to a Mermaid-safe node identifier.
// Replaces '.', '/', '-', ' ' with '_'. Prefixes leading digits with 'N'.
func fcid(s string) string {
	r := strings.NewReplacer(".", "_", "/", "_", "-", "_", " ", "_")
	out := r.Replace(s)
	if len(out) > 0 && out[0] >= '0' && out[0] <= '9' {
		out = "N" + out
	}
	return out
}

// fcEsc escapes characters that break Mermaid node label content.
func fcEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	// Remove {{ }} which are hexagon shape delimiters in flowchart syntax.
	s = strings.ReplaceAll(s, "{{", "{ {")
	s = strings.ReplaceAll(s, "}}", "} }")
	return s
}

// emitFlowchartRooms emits the DetailRooms view: one node per room, cross-room transitions.
// selected is the ordered list of rooms to include; rooms outside this set get stub nodes.
// seqMap maps room name → 1-based pipeline position; total is the pipeline length.
func emitFlowchartRooms(b *strings.Builder, a *app.AppDef, initialState string, selected []string, seqMap map[string]int, total int) error {
	rooms := GroupRooms(a)

	// Build set of selected rooms for fast lookup.
	selectedSet := map[string]bool{}
	for _, r := range selected {
		selectedSet[r] = true
	}
	filtered := len(selected) < len(rooms.Order)

	// Emit room nodes for selected rooms.
	for _, room := range selected {
		nodeID := "RI_" + fcid(room)
		stateCount := len(rooms.Members[room])
		stateWord := "states"
		if stateCount == 1 {
			stateWord = "state"
		}
		var label string
		if _, hasSeq := seqMap[room]; hasSeq && !strings.HasPrefix(room, "__") {
			label = fmt.Sprintf("phase %d · %s (%d %s)", seqMap[room], fcEsc(room), stateCount, stateWord)
		} else {
			label = fmt.Sprintf("%s (%d %s)", fcEsc(room), stateCount, stateWord)
		}
		fmt.Fprintf(b, "  %s[/%q/]:::room\n", nodeID, label)
	}
	b.WriteString("\n")

	// Initial arrow to the room that contains the initial state.
	if initialState != "" {
		initRoom := rooms.RoomOf[initialState]
		if initRoom != "" && selectedSet[initRoom] {
			fmt.Fprintf(b, "  Start --> RI_%s\n", fcid(initRoom))
		}
	}

	// Cross-room transitions — deduplicate by (fromRoom, toRoom, intent).
	type roomEdge struct{ from, to, intent string }
	seen := map[roomEdge]bool{}

	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil {
			return
		}
		fromRoom := rooms.RoomOf[path]
		if !selectedSet[fromRoom] {
			return
		}
		for _, intent := range sortedKeys(s.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range s.On[intent] {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}
				toRoom, ok := rooms.RoomOf[target]
				if !ok || toRoom == fromRoom {
					continue
				}
				key := roomEdge{fromRoom, toRoom, intent}
				if seen[key] {
					continue
				}
				seen[key] = true
			}
		}
	})

	// Collect and sort edges for deterministic output.
	var edges []roomEdge
	for e := range seen {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		if edges[i].to != edges[j].to {
			return edges[i].to < edges[j].to
		}
		return edges[i].intent < edges[j].intent
	})

	// Track stub nodes needed for exits to rooms outside the selected set.
	stubEmitted := map[string]bool{}

	for _, e := range edges {
		if selectedSet[e.to] {
			fmt.Fprintf(b, "  RI_%s -- %q --> RI_%s\n", fcid(e.from), fcEsc(e.intent), fcid(e.to))
		} else if filtered {
			stubID := "STUB_" + fcid(e.to)
			if !stubEmitted[stubID] {
				stubEmitted[stubID] = true
				fmt.Fprintf(b, "  %s[/\"→ %s (external)\"/]:::next\n", stubID, fcEsc(e.to))
			}
			fmt.Fprintf(b, "  RI_%s -- %q --> %s\n", fcid(e.from), fcEsc(e.intent), stubID)
		}
	}
	b.WriteString("\n")
	return nil
}

// emitFlowchartStates emits DetailStates / DetailSteps / DetailFull views.
// selected is the ordered list of rooms to include; rooms outside this set get stub nodes.
// seqMap maps room name → 1-based pipeline position; total is the pipeline length.
func emitFlowchartStates(b *strings.Builder, a *app.AppDef, detail DetailLevel, initialState string, selected []string, seqMap map[string]int, total int) error {
	rooms := GroupRooms(a)

	// Build set of selected rooms for fast lookup.
	selectedSet := map[string]bool{}
	for _, r := range selected {
		selectedSet[r] = true
	}
	filtered := len(selected) < len(rooms.Order)

	// Emit subgraphs, one per room.
	for _, room := range selected {
		paths := rooms.Members[room]

		// Room label: use room name (with pipeline sequence if available).
		// If there's a description on the first top-level state, append it.
		var roomHeading string
		if _, hasSeq := seqMap[room]; hasSeq && !strings.HasPrefix(room, "__") {
			roomHeading = fmt.Sprintf("<b>phase %d · %s</b>", seqMap[room], fcEsc(room))
		} else {
			roomHeading = "<b>" + fcEsc(room) + "</b>"
		}
		roomLabel := roomHeading
		if topState, ok := a.States[room]; ok && topState.Description != "" {
			roomLabel += " — " + fcEsc(topState.Description)
		}

		fmt.Fprintf(b, "  subgraph SG_%s[%q]\n", fcid(room), roomLabel)
		b.WriteString("    direction LR\n")

		// Emit state nodes — only leaf/atomic states that belong to this room.
		for _, path := range paths {
			s := lookupState(a, path)
			if s == nil {
				continue
			}
			// Skip compound states (they have children) — only emit leaves.
			if len(s.States) > 0 {
				continue
			}

			stID := "ST_" + fcid(path)
			stLabel := fcEsc(path)
			if detail >= DetailSteps && len(s.OnEnter) > 0 {
				// Count invoke steps for the label annotation.
				invokeCount := 0
				for _, e := range s.OnEnter {
					if e.Invoke != "" {
						invokeCount++
					}
				}
				if invokeCount > 0 {
					stLabel += fmt.Sprintf(" (on_enter: %d)", invokeCount)
				}
			}
			fmt.Fprintf(b, "    %s[/%q/]:::room\n", stID, stLabel)

			// Emit on_enter chain for DetailSteps / DetailFull.
			if detail >= DetailSteps && len(s.OnEnter) > 0 {
				prevID := stID
				stepIdx := 0
				for i, e := range s.OnEnter {
					if e.Invoke == "" {
						// DetailFull: emit store node for set-only effects if they have Set/Bind.
						if detail >= DetailFull && (len(e.Set) > 0 || len(e.Bind) > 0) {
							wsID := fmt.Sprintf("WSB_%s_%d", fcid(path), i)
							keys := worldKeysFromEffect(e)
							fmt.Fprintf(b, "    %s[(%q)]:::store\n", wsID, fcEsc(strings.Join(keys, "<br/>")))
							fmt.Fprintf(b, "    %s --> %s\n", prevID, wsID)
							prevID = wsID
						}
						continue
					}

					stepID := fmt.Sprintf("STEP_%s_%d", fcid(path), i)
					label := buildStepLabel(stepIdx, e)
					cls := stepClass(e)

					fmt.Fprintf(b, "    %s{{%q}}:::%s\n", stepID, label, cls)
					fmt.Fprintf(b, "    %s --> %s\n", prevID, stepID)
					prevID = stepID
					stepIdx++

					// DetailFull: world-write store node after each step.
					if detail >= DetailFull && (len(e.Bind) > 0 || len(e.Set) > 0) {
						wsID := fmt.Sprintf("WSB_%s_%d", fcid(path), i)
						keys := worldKeysFromEffect(e)
						fmt.Fprintf(b, "    %s[(%q)]:::store\n", wsID, fcEsc(strings.Join(keys, "<br/>")))
						fmt.Fprintf(b, "    %s --> %s\n", stepID, wsID)
						prevID = wsID
					}

					// DetailFull: on_error node.
					if detail >= DetailFull && e.OnError != "" {
						errID := fmt.Sprintf("EERR_%s_%d", fcid(path), i)
						errLabel := fcEsc("→ " + e.OnError)
						fmt.Fprintf(b, "    %s{{%q}}:::err\n", errID, errLabel)
						fmt.Fprintf(b, "    %s -.- %s\n", stepID, errID)
					}
				}
			}
		}

		b.WriteString("  end\n")
		b.WriteString("\n")
	}

	// Initial arrow — only if initial state belongs to a selected room.
	if initialState != "" {
		initRoom := rooms.RoomOf[initialState]
		if selectedSet[initRoom] {
			initID := "ST_" + fcid(initialState)
			fmt.Fprintf(b, "  Start --> %s\n", initID)
		}
	}
	b.WriteString("\n")

	// Transitions — deduplicate by (fromID, toID, label).
	// For filtered views: transitions to states outside selected rooms become stub nodes.
	type transEdge struct {
		from, to, label string
		toIsStub        bool
		stubRoom        string // room name for the stub node
	}
	seen := map[[3]string]bool{} // key: from, to, label (stub uses STUB_ prefix in to)
	var edges []transEdge

	// Track stub nodes needed.
	stubNodes := map[string]bool{} // STUB_<room> → emitted?

	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil || len(s.States) > 0 {
			return // skip compound states
		}
		fromRoom := rooms.RoomOf[path]
		if !selectedSet[fromRoom] {
			return // source state not in selected rooms
		}
		fromID := "ST_" + fcid(path)
		for _, intent := range sortedKeys(s.On) {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range s.On[intent] {
				target := resolveMermaidTarget(path, tr.Target)
				if target == "" || strings.Contains(target, "{{") {
					continue
				}

				label := intent
				if label == "*" {
					label = "*(any)"
				}
				if tr.When != "" {
					label += " [" + truncate(tr.When, 20) + "]"
				} else if tr.Default {
					label += " (default)"
				}

				toRoom, ok := rooms.RoomOf[target]
				if !ok {
					continue
				}
				if filtered && !selectedSet[toRoom] {
					// Emit a stub edge to the external room.
					stubID := "STUB_" + fcid(toRoom)
					key := [3]string{fromID, stubID, label}
					if seen[key] {
						continue
					}
					seen[key] = true
					stubNodes[stubID] = true
					edges = append(edges, transEdge{from: fromID, to: stubID, label: label, toIsStub: true, stubRoom: toRoom})
				} else {
					toID := "ST_" + fcid(target)
					key := [3]string{fromID, toID, label}
					if seen[key] {
						continue
					}
					seen[key] = true
					edges = append(edges, transEdge{from: fromID, to: toID, label: label})
				}
			}
		}
	})

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].from != edges[j].from {
			return edges[i].from < edges[j].from
		}
		if edges[i].to != edges[j].to {
			return edges[i].to < edges[j].to
		}
		return edges[i].label < edges[j].label
	})

	// Emit stub nodes (sorted for determinism).
	var stubIDs []string
	for id := range stubNodes {
		stubIDs = append(stubIDs, id)
	}
	sort.Strings(stubIDs)
	for _, stubID := range stubIDs {
		// Derive room name from stub ID by stripping STUB_ prefix and unescaping.
		// We can recover it from edges.
		roomName := ""
		for _, e := range edges {
			if e.to == stubID && e.toIsStub {
				roomName = e.stubRoom
				break
			}
		}
		if roomName != "" {
			fmt.Fprintf(b, "  %s([\"→ %s\"]):::next\n", stubID, fcEsc(roomName))
		}
	}

	for _, e := range edges {
		fmt.Fprintf(b, "  %s -- %q --> %s\n", e.from, fcEsc(e.label), e.to)
	}
	b.WriteString("\n")

	return nil
}

// buildStepLabel constructs the label for an on_enter step hex node.
func buildStepLabel(idx int, e app.Effect) string {
	invokeName := e.Invoke
	// Strip package prefix for brevity — use last segment after dot.
	if dot := strings.LastIndex(invokeName, "."); dot >= 0 {
		// Keep the last two segments for readability (e.g. "host.agent" → "host.agent").
		invokeName = e.Invoke
	}

	label := fmt.Sprintf("<b>step %d — %s</b>", idx, fcEsc(invokeName))

	// Prompt file: look in With["prompt"] or With["prompt_path"].
	for _, key := range []string{"prompt", "prompt_path"} {
		if v, ok := e.With[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				// Remove template expressions from the basename.
				base := filepath.Base(s)
				if !strings.Contains(base, "{{") {
					label += "<br/>prompt: " + fcEsc(base)
				}
			}
			break
		}
	}

	// Command: look in With["cmd"] or With["command"].
	for _, key := range []string{"cmd", "command"} {
		if v, ok := e.With[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				cmd := truncate(s, 40)
				if !strings.Contains(cmd, "{{") {
					label += "<br/>cmd: " + fcEsc(cmd)
				} else {
					// Show just the non-template prefix for templated commands.
					plain := strings.SplitN(cmd, "{{", 2)[0]
					plain = strings.TrimSpace(plain)
					if plain != "" {
						label += "<br/>cmd: " + fcEsc(plain) + "…"
					}
				}
			}
			break
		}
	}

	// Background prefix.
	if e.Background {
		label = "⚙ " + label
	}

	// Conditional prefix.
	if e.When != "" {
		label = "[cond] " + label
	}

	return label
}

// stepClass returns the CSS class for a step node based on the invoke name.
func stepClass(e app.Effect) string {
	invoke := strings.ToLower(e.Invoke)
	switch {
	case strings.Contains(invoke, "agent") || strings.Contains(invoke, ".llm"):
		return "llm"
	case strings.Contains(invoke, ".run") || strings.Contains(invoke, "shell") || strings.Contains(invoke, "exec"):
		return "shell"
	default:
		return "work"
	}
}

// worldKeysFromEffect returns world key labels for a world-write store node.
func worldKeysFromEffect(e app.Effect) []string {
	var keys []string
	for k := range e.Bind {
		keys = append(keys, "world."+k)
	}
	for k := range e.Set {
		keys = append(keys, "world."+k)
	}
	sort.Strings(keys)
	return keys
}

// emitFlowchartClassDefs writes the classDef block matching the bugfix diagram style.
func emitFlowchartClassDefs(b *strings.Builder) {
	b.WriteString("  classDef input  fill:#eef,stroke:#446,stroke-width:1.5px\n")
	b.WriteString("  classDef room   fill:#fff,stroke:#357,stroke-width:1.5px\n")
	b.WriteString("  classDef work   fill:#f7f7fa,stroke:#888\n")
	b.WriteString("  classDef llm    fill:#f0e6ff,stroke:#6a3eb5,stroke-width:2px,color:#3a2270\n")
	b.WriteString("  classDef shell  fill:#fef6e4,stroke:#a85,stroke-width:1.5px\n")
	b.WriteString("  classDef store  fill:#eef5fb,stroke:#369,stroke-width:1px\n")
	b.WriteString("  classDef cache  fill:#fff6e0,stroke:#a86,stroke-width:1.5px\n")
	b.WriteString("  classDef err    fill:#fff5f5,stroke:#c66,stroke-width:1px,stroke-dasharray:4 3\n")
	b.WriteString("  classDef next   fill:#e8f5e9,stroke:#272,stroke-width:1.5px\n")
	b.WriteString("  classDef note   fill:#fffbe6,stroke:#a90,stroke-width:1px,stroke-dasharray:2 2,color:#553\n")
}
