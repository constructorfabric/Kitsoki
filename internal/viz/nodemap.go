// nodemap.go adds FlowchartWithMap, which returns the same Mermaid source that
// FlowchartBytes already emits plus a sidecar mapping from Mermaid node ID to
// [NodeRef]. FlowchartBytes stays bit-for-bit identical — the node recorder is
// a separate walk over the same graph, so a UI can map a clicked node back to
// the AppDef element without changing the diagram. See doc.go for the overview.
package viz

import (
	"fmt"
	"strings"

	"kitsoki/internal/app"
)

// NodeRef identifies the AppDef element behind one Mermaid node, so a UI that
// renders a [FlowchartResult] can resolve a clicked node back to its source.
// Kind is one of "state", "effect", or "world" ("transition" is reserved but
// never emitted — see [FlowchartWithMap]). Ref is the kind-specific locator
// whose grammar is documented on [FlowchartWithMap]; treat it as opaque except
// to match it against the AppDef.
type NodeRef struct {
	Kind string `json:"kind"` // "state" | "effect" | "world" (never "transition")
	Ref  string `json:"ref"`  // kind-specific locator; see FlowchartWithMap
}

// FlowchartResult pairs the rendered Mermaid source with its node-ID sidecar.
// Source is byte-identical to what [FlowchartBytes] would emit for the same
// options; NodeMap keys are exactly the node IDs present in Source.
type FlowchartResult struct {
	Source  string             `json:"source"`
	NodeMap map[string]NodeRef `json:"node_map"`
}

// FlowchartOptions selects the detail level and room filter for
// [FlowchartWithMap]. The zero value is the lowest-detail, no-filter view
// (Detail [DetailRooms], every room); callers wanting the common
// states-inside-rooms view set Detail to [DetailStates] explicitly. The fields
// mirror the positional arguments of [FlowchartBytes] so callers migrate
// trivially.
type FlowchartOptions struct {
	Detail DetailLevel
	Filter FlowchartFilter

	// Banners, when true, appends one `%% banner <state-path> <text>` comment
	// line per leaf state that declares a static (non-templated) banner view
	// element. Mermaid renderers ignore `%%` comments, and FlowchartBytes is
	// not consulted, so the rendered diagram and the CLI `viz` output stay
	// byte-identical; only consumers that parse the source (the runstatus web
	// viewer) recover the banner. Used by the web path so the path/horizon
	// metro stations can show each room's declared phase banner
	// (INTAKE / SEARCHING / …) without a separate metadata channel.
	Banners bool
}

// FlowchartWithMap is [FlowchartBytes] plus a node-ID sidecar: it runs the
// emitter unchanged and, in a second walk over the same graph, records what
// each emitted node ID points to. The two walks use the same ID helpers, so
// the [FlowchartResult.NodeMap] keys and the [FlowchartResult.Source] IDs
// always agree. It returns the error from [ResolveFilterRooms] when opts.Filter
// names an unknown room.
//
// Ref encoding (stable contract — do not change without flagging):
//
//	Kind="state"      Ref = dot-separated absolute state path, e.g. "bar.dark"
//	                        (the internal representation; dots separate levels)
//	Kind="effect"     Ref = "<state-path>:on_enter:<index>"
//	                        0-based index into State.OnEnter
//	Kind="world"      Ref = "world:<varname>"
//
// Kind="transition" is deliberately never emitted: Mermaid flowchart edges
// carry no clickable node ID, so there is nothing to key a transition mapping
// against. The sidecar therefore covers diagram nodes only.
//
// At [DetailRooms], room nodes use Kind="state" with Ref set to the room name
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

	out := string(src)
	if opts.Banners {
		bc, err := bannerComments(a, opts)
		if err != nil {
			return FlowchartResult{}, err
		}
		if bc != "" {
			out += "\n" + bc
		}
	}

	return FlowchartResult{
		Source:  out,
		NodeMap: nm,
	}, nil
}

// bannerComments returns `%% banner <state-path> <text>` lines (one per leaf
// state with a static banner view element) for the rooms selected by opts.Filter.
// The state path — not the Mermaid node id — is the key, so the consumer matches
// banners to rooms by the same label it already prefix-matches against. Returns
// the empty string when no selected state declares a banner.
func bannerComments(a *app.AppDef, opts FlowchartOptions) (string, error) {
	selected, err := ResolveFilterRooms(a, opts.Filter)
	if err != nil {
		return "", err
	}
	selectedSet := map[string]bool{}
	for _, r := range selected {
		selectedSet[r] = true
	}
	rooms := GroupRooms(a)

	var b strings.Builder
	walkAllStates(a.States, "", func(path string, s *app.State) {
		if s == nil || len(s.States) > 0 {
			return // compound — banners live on leaf states
		}
		if !selectedSet[rooms.RoomOf[path]] {
			return
		}
		if txt := staticBannerText(s); txt != "" {
			fmt.Fprintf(&b, "%%%% banner %s %s\n", path, txt)
		}
	})
	return b.String(), nil
}

// staticBannerText returns the first banner view element's text for a state,
// or "" when the state declares none or the text is templated (contains `{{`).
// Templated banners are skipped deliberately: their value is a runtime render,
// not declared graph metadata, so surfacing them statically would be a guess.
func staticBannerText(s *app.State) string {
	for _, el := range s.View.Elements {
		if el.Kind != "banner" {
			continue
		}
		t := strings.TrimSpace(el.Source)
		if t == "" || strings.Contains(t, "{{") {
			return ""
		}
		if i := strings.IndexAny(t, "\r\n"); i >= 0 {
			t = t[:i]
		}
		return t
	}
	return ""
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
