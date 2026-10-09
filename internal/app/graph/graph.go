// Package graph derives static, read-only views of a loaded story's room
// graph for the story-editor surface (docs/proposals/story-editor-view.md).
//
// Everything here is a PURE function over an app.App: no I/O, no LLM, no
// orchestrator. The editor RPC layer (internal/runstatus/server/editor.go)
// selects an app.App per story from the registry catalogue and calls these
// helpers to answer list-rooms / room-detail / agent-contracts requests.
//
// A "room" is a top-level state (app.StatePath.TopLevel). The graph walks
// State.On[intent][].Target transitions from the initial state, descending
// into compound State.States. Synthetic @exit:* / @import targets are not
// rooms and are skipped as reachable destinations.
package graph

import (
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/storyauthoring"
)

// RoomSummary is one entry in the BFS-ordered room list.
type RoomSummary struct {
	// ID is the top-level state id (room id).
	ID string `json:"id"`
	// Label is a human-facing label (the state Description, falling back to ID).
	Label string `json:"label"`
	// Distance is the mean of all shortest-path distances to this room from the
	// initial state. The initial room is 0. Unreachable rooms report a sentinel
	// (+Inf encoded as a very large number) and sort last by name.
	Distance float64 `json:"distance"`
	// HasAgent is true when the room (or any nested child) carries a
	// host.agent.* invoke in its on_enter or any intent-arc effect.
	HasAgent bool `json:"has_agent"`
}

// unreachableDistance is the JSON-safe sentinel for "unreachable" — +Inf is
// not representable in JSON, so the API uses a large finite marker. Callers
// that need the boolean "is unreachable" should compare >= unreachableDistance.
const unreachableDistance = 1e9

// RoomList returns the story's rooms ordered by BFS distance from the initial
// state (ascending), ties and unreachable rooms broken by id. It is a pure
// function: it reads only the app definition.
func RoomList(a app.App) []RoomSummary {
	rooms := topLevelRooms(a)
	dist := roomDistances(a, rooms)

	out := make([]RoomSummary, 0, len(rooms))
	for _, id := range rooms {
		st, _ := a.LookupState(app.StatePath(id))
		d, ok := dist[id]
		if !ok {
			d = unreachableDistance
		}
		out = append(out, RoomSummary{
			ID:       id,
			Label:    roomLabel(id, st),
			Distance: d,
			HasAgent: roomHasAgent(st),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Distance != out[j].Distance {
			return out[i].Distance < out[j].Distance
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// topLevelRooms returns every top-level state id declared on the app, sorted.
// It reaches into the AppDef via the App's per-state lookup: the App interface
// has no "list states" method, so we recover the set from the initial state and
// from transition targets, then confirm each is a real top-level state.
func topLevelRooms(a app.App) []string {
	seen := map[string]bool{}
	var order []string
	add := func(id string) {
		id = roomOf(id)
		if id == "" || seen[id] || storyauthoring.IsFrameworkRoom(id) {
			return
		}
		if _, ok := a.LookupState(app.StatePath(id)); !ok {
			return
		}
		seen[id] = true
		order = append(order, id)
	}

	// Prefer the full declared set when the App exposes it (StateLister) so
	// orphaned rooms — reachable from nothing — are still listed. Falls back to
	// reachability discovery for App implementations that don't expose it.
	if sl, ok := a.(interface{ TopLevelStateIDs() []string }); ok {
		for _, id := range sl.TopLevelStateIDs() {
			add(id)
		}
	}

	// Seed with the initial room.
	init := roomOf(string(a.InitialState()))
	add(init)

	// Walk transitively over transition targets so every reachable room is
	// discovered, plus scan for unreachable rooms via the targets that name
	// them even if no path leads there from init.
	queue := append([]string{}, order...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		st, ok := a.LookupState(app.StatePath(cur))
		if !ok {
			continue
		}
		for _, tgt := range stateTargets(st) {
			r := roomOf(tgt)
			if r != "" && !seen[r] {
				if _, ok := a.LookupState(app.StatePath(r)); ok {
					seen[r] = true
					order = append(order, r)
					queue = append(queue, r)
				}
			}
		}
	}

	sort.Strings(order)
	return order
}

// roomDistances computes, for each reachable room, the MEAN of all
// shortest-path distances from the initial room. Because BFS yields a single
// shortest distance per node, the "mean of all shortest-path distances" reduces
// to that BFS distance; the field is kept as a float64 to honour the contract
// shape and to allow future weighting. Unreachable rooms are absent from the map.
func roomDistances(a app.App, rooms []string) map[string]float64 {
	init := roomOf(string(a.InitialState()))
	dist := map[string]float64{}
	if init == "" {
		return dist
	}
	if _, ok := a.LookupState(app.StatePath(init)); !ok {
		return dist
	}
	dist[init] = 0
	queue := []string{init}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		st, ok := a.LookupState(app.StatePath(cur))
		if !ok {
			continue
		}
		for _, tgt := range stateTargets(st) {
			r := roomOf(tgt)
			if r == "" || r == cur {
				continue
			}
			if _, ok := a.LookupState(app.StatePath(r)); !ok {
				continue
			}
			if _, done := dist[r]; done {
				continue
			}
			dist[r] = dist[cur] + 1
			queue = append(queue, r)
		}
	}
	return dist
}

// stateTargets returns every transition target reachable from a state's intent
// arcs, descending into compound children (a parent is a passthrough; parallel
// children are independent). Effect-level Target / EmitIntent hops inside
// on_complete chains are NOT graph edges for room navigation and are excluded.
func stateTargets(st *app.State) []string {
	if st == nil {
		return nil
	}
	var out []string
	var walk func(path string, s *app.State)
	walk = func(path string, s *app.State) {
		if s == nil {
			return
		}
		for intent, transitions := range s.On {
			if storyauthoring.IsFrameworkTransition(path, intent) {
				continue
			}
			for _, tr := range transitions {
				if tr.Target != "" {
					out = append(out, tr.Target)
				}
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
	walk("", st)
	return out
}

// roomOf maps a transition target / state path to its top-level room id, or ""
// for a synthetic target (@exit:*, @import descents, "." self) that is not a
// room. A "." target means self and carries no room information here.
func roomOf(target string) string {
	if target == "" || target == "." {
		return ""
	}
	// Authored synthetic exit targets (@exit:done) and the loader-materialised
	// pseudo-states they expand to (__exit__done) are graph sinks, not rooms.
	if strings.HasPrefix(target, "@") || strings.HasPrefix(target, "__exit__") {
		return ""
	}
	return string(app.StatePath(target).TopLevel())
}

// roomLabel picks the human-facing room label: the state Description when set,
// falling back to the room id.
func roomLabel(id string, st *app.State) string {
	if st != nil && strings.TrimSpace(st.Description) != "" {
		return st.Description
	}
	return id
}

// roomHasAgent reports whether a room (including nested children) carries any
// host.agent.* invoke in on_enter or any intent-arc effect.
func roomHasAgent(st *app.State) bool {
	if st == nil {
		return false
	}
	found := false
	var walk func(s *app.State)
	walk = func(s *app.State) {
		if s == nil || found {
			return
		}
		if anyAgentEffect(s.OnEnter) {
			found = true
			return
		}
		for _, transitions := range s.On {
			for _, tr := range transitions {
				if anyAgentEffect(tr.Effects) {
					found = true
					return
				}
			}
		}
		for _, child := range s.States {
			walk(child)
		}
	}
	walk(st)
	return found
}

// anyAgentEffect reports whether any effect in the list (or its nested
// on_complete / effects sub-lists) invokes a host.agent.* handler.
func anyAgentEffect(effects []app.Effect) bool {
	for _, e := range effects {
		if isAgentInvoke(e.Invoke) {
			return true
		}
		if anyAgentEffect(e.OnComplete) || anyAgentEffect(e.Effects) {
			return true
		}
	}
	return false
}

// isAgentInvoke reports whether an invoke handler name is a host.agent.* call.
func isAgentInvoke(invoke string) bool {
	return strings.HasPrefix(invoke, "host.agent.")
}
