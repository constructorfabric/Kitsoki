// Package viz — nodemap.go adds FlowchartWithMap, which returns the same
// Mermaid source that FlowchartBytes already emits plus a sidecar mapping
// from Mermaid node ID → NodeRef.  FlowchartBytes / Flowchart() stay
// bit-for-bit identical — the recorder is plumbed as a side-effect only.
package viz

import (
	"fmt"
	"strings"

	"kitsoki/internal/app"
)

// NodeRef identifies one semantic element in an AppDef that corresponds to
// a Mermaid node emitted by FlowchartWithMap.
type NodeRef struct {
	Kind string `json:"kind"` // "state" | "effect" | "transition" | "world"
	Ref  string `json:"ref"`
}

// FlowchartResult is the return value of FlowchartWithMap.
type FlowchartResult struct {
	Source  string             `json:"source"`
	NodeMap map[string]NodeRef `json:"node_map"`
}

// FlowchartOptions bundles the parameters accepted by FlowchartWithMap.
// It mirrors the parameters of the existing FlowchartBytes helper so
// callers that already use FlowchartBytes can migrate trivially.
type FlowchartOptions struct {
	Detail DetailLevel
	Filter FlowchartFilter
}

// FlowchartWithMap returns the Mermaid flowchart source (identical to the
// output of FlowchartBytes) plus a sidecar NodeMap that maps every emitted
// Mermaid node ID to the AppDef element it represents.
//
// Ref encoding (stable contract — do not change without flagging):
//
//	Kind="state"      Ref = dot-separated absolute state path, e.g. "bar.dark"
//	                        (the internal representation; dots separate levels)
//	Kind="effect"     Ref = "<state-path>:on_enter:<index>"
//	                        0-based index into State.OnEnter
//	Kind="transition" Ref = "<state-path>:on:<intent>:<index>"
//	                        0-based index into State.On[intent]
//	Kind="world"      Ref = "world:<varname>"
//
// Note on "transition": Mermaid flowchart edges have no clickable node IDs.
// FlowchartWithMap therefore does NOT emit Kind="transition" entries in the
// NodeMap because there is no diagram node ID to key them against.  This
// diverges from the proposal's four-kind vocabulary; see implementation
// notes in the code review request.
//
// Note on DetailRooms: room nodes use Kind="state" with Ref=<room-name>
// (the top-level compound state name).
func FlowchartWithMap(a *app.AppDef, opts FlowchartOptions) (FlowchartResult, error) {
	// 1. Generate the Mermaid source via the existing (unchanged) emitter.
	src, err := FlowchartBytes(a, opts.Detail, opts.Filter)
	if err != nil {
		return FlowchartResult{}, err
	}

	// 2. Walk the same graph and record every node ID the emitter produced.
	nm, err := buildNodeMap(a, opts.Detail, opts.Filter)
	if err != nil {
		return FlowchartResult{}, err
	}

	return FlowchartResult{
		Source:  string(src),
		NodeMap: nm,
	}, nil
}

// buildNodeMap walks the AppDef with the same logic as FlowchartBytes and
// records every node ID that the emitter would have written.  The IDs are
// produced with the same helpers (fcid, fmt.Sprintf) so they match exactly.
func buildNodeMap(a *app.AppDef, detail DetailLevel, filter FlowchartFilter) (map[string]NodeRef, error) {
	nm := make(map[string]NodeRef)

	selected, err := ResolveFilterRooms(a, filter)
	if err != nil {
		return nil, err
	}
	selectedSet := map[string]bool{}
	for _, r := range selected {
		selectedSet[r] = true
	}

	rooms := GroupRooms(a)

	if detail == DetailRooms {
		// One node per room: RI_<room>.
		for _, room := range selected {
			nodeID := "RI_" + fcid(room)
			nm[nodeID] = NodeRef{Kind: "state", Ref: room}
		}
		return nm, nil
	}

	// DetailStates / DetailSteps / DetailFull: one subgraph per room,
	// one leaf node per atomic state (ST_<path>), optionally step nodes
	// (STEP_<path>_<i>) and world-write nodes (WSB_<path>_<i>).
	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil || len(s.States) > 0 {
			return // compound — skip
		}
		fromRoom := rooms.RoomOf[path]
		if !selectedSet[fromRoom] {
			return
		}

		// ── state node ──────────────────────────────────────────────────
		stID := "ST_" + fcid(path)
		nm[stID] = NodeRef{Kind: "state", Ref: path}

		if detail < DetailSteps {
			return
		}

		// ── on_enter effect nodes (DetailSteps+) ───────────────────────
		for i, e := range s.OnEnter {
			if e.Invoke == "" {
				// Set-only effect: DetailFull world-write store node.
				if detail >= DetailFull && (len(e.Set) > 0 || len(e.Bind) > 0) {
					wsID := fmt.Sprintf("WSB_%s_%d", fcid(path), i)
					nm[wsID] = NodeRef{
						Kind: "world",
						Ref:  worldRefFromEffect(e),
					}
				}
				continue
			}

			// STEP_ node — Kind="effect".
			stepID := fmt.Sprintf("STEP_%s_%d", fcid(path), i)
			nm[stepID] = NodeRef{
				Kind: "effect",
				Ref:  fmt.Sprintf("%s:on_enter:%d", path, i),
			}

			if detail < DetailFull {
				continue
			}

			// DetailFull: world-write store node after the invoke step.
			if len(e.Bind) > 0 || len(e.Set) > 0 {
				wsID := fmt.Sprintf("WSB_%s_%d", fcid(path), i)
				nm[wsID] = NodeRef{
					Kind: "world",
					Ref:  worldRefFromEffect(e),
				}
			}

			// DetailFull: on_error node — treated as a second entry for
			// the same effect (same Ref, different node ID).
			if e.OnError != "" {
				errID := fmt.Sprintf("EERR_%s_%d", fcid(path), i)
				nm[errID] = NodeRef{
					Kind: "effect",
					Ref:  fmt.Sprintf("%s:on_enter:%d", path, i),
				}
			}
		}
	})

	return nm, nil
}

// worldRefFromEffect returns the "world:<varname>" Ref for a NodeRef of
// Kind="world".  When the effect writes multiple world keys, the first
// sorted key is used as the canonical identifier; all keys are also
// embedded in the Ref as a comma-separated list for richer clients.
func worldRefFromEffect(e app.Effect) string {
	keys := worldKeysFromEffect(e) // already sorted; prefixed with "world."
	if len(keys) == 0 {
		return "world:?"
	}
	// Strip the "world." prefix added by worldKeysFromEffect.
	varnames := make([]string, len(keys))
	for i, k := range keys {
		varnames[i] = strings.TrimPrefix(k, "world.")
	}
	if len(varnames) == 1 {
		return "world:" + varnames[0]
	}
	return "world:" + strings.Join(varnames, ",")
}
