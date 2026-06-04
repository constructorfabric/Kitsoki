package orchestrator

import (
	"strconv"
	"strings"

	"kitsoki/internal/expr"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// rerenderHostArgs re-renders the templates in hc.RawWith against the current
// world snapshot so a host call that runs after an earlier bind in the same
// `on_enter:` block sees the post-bind values.
//
// Falls back to the up-front-resolved hc.Args when:
//   - RawWith is empty (no templates to re-render)
//   - hc.Env is not the expected expr.Env type (older code paths or stubs)
//
// On a *leaf* template-render error the leaf is replaced with the
// corresponding pre-resolved leaf from hc.Args (per-leaf fallback), so a
// single bad nested template no longer poisons the entire `with:` block —
// the rest of the leaves still see the post-bind world.  Returns the
// rerendered args plus a fellBack flag that is true iff any leaf fell back
// (used by HostDispatched to make the diagnostic story honest).
//
// This keeps the behaviour compatible with code that doesn't supply RawWith
// while letting the bugfix room's 2-step `on_enter:` pattern compose
// cleanly.  See `internal/machine/machine.go` HostInvocation for the
// machine-side contract.
// dispatchRerenderWorld builds the world to re-render an invoke's RawWith
// against: the invoke's machine-time WorldSnapshot (the world AS OF the
// invoke's position in the effect chain — earlier set:/increment: reflected,
// later ones NOT) overlaid with the binds accumulated from earlier invokes in
// the same dispatch loop. This is what lets a later `set:` clearing a key not
// clobber an earlier invoke's `with:` arg referencing it (proposal/restart's
// archive-then-clear), while still letting a downstream invoke read an earlier
// invoke's bind. Falls back to the live world `w` when no snapshot was
// captured (HostInvocations built outside the effect-walk / test stubs).
func dispatchRerenderWorld(hc machine.HostInvocation, binds map[string]any, w world.World) world.World {
	if hc.WorldSnapshot == nil {
		return w
	}
	vars := make(map[string]any, len(hc.WorldSnapshot)+len(binds))
	for k, v := range hc.WorldSnapshot {
		vars[k] = v
	}
	for k, v := range binds {
		vars[k] = v
	}
	return world.World{Vars: vars}
}

func rerenderHostArgs(hc machine.HostInvocation, w world.World) (map[string]any, bool) {
	if len(hc.RawWith) == 0 {
		return hc.Args, false
	}
	env, ok := hc.Env.(expr.Env)
	if !ok {
		return hc.Args, false
	}
	// Snapshot the env with the *current* world.
	env.World = w.Vars
	out := make(map[string]any, len(hc.RawWith))
	fellBack := false
	for k, raw := range hc.RawWith {
		// Look up the up-front-resolved leaf-equivalent for this top-level
		// key so per-leaf failures inside a nested map/slice can fall back
		// to the corresponding pre-bind leaf.
		existing, hasExisting := hc.Args[k]
		resolved, leafFell, err := resolveTemplateValueLeafFallback(raw, existing, hasExisting, env)
		if err != nil {
			// Unrecoverable shape mismatch between raw and existing at
			// the top level; preserve the legacy behaviour of falling
			// back to the up-front-resolved value for this key.
			if hasExisting {
				out[k] = existing
			} else {
				out[k] = raw
			}
			fellBack = true
			continue
		}
		if leafFell {
			fellBack = true
		}
		out[k] = resolved
	}
	return out, fellBack
}

// resolveTemplateValueLeafFallback recurses into maps/slices and renders any
// string that looks like an expr-lang template.  On a leaf-template render
// error it falls back to the corresponding leaf from `existing` (the
// up-front-resolved value for this position), if one exists and has a
// matching shape.  The returned bool is true iff any leaf in the subtree
// fell back to its pre-bind value.
//
// The shape-matching rule is:
//   - string leaf → fall back to `existing` (any type)
//   - map leaf    → recurse, matching keys against `existing` if it is a map
//   - slice leaf  → recurse, matching indices against `existing` if it is a
//     slice of the same length
//
// If shapes diverge mid-walk (e.g. raw says map, existing says string), the
// failing subtree falls back wholesale to `existing` and fellBack is set.
func resolveTemplateValueLeafFallback(v any, existing any, hasExisting bool, env expr.Env) (any, bool, error) {
	switch val := v.(type) {
	case string:
		if !containsTemplate(val) {
			return val, false, nil
		}
		r, err := expr.RenderValue(val, env)
		if err != nil {
			if hasExisting {
				return existing, true, nil
			}
			// No pre-bind leaf available; keep raw so the handler sees
			// the un-rendered template rather than nil.
			return val, true, nil
		}
		return r, false, nil
	case map[string]any:
		exMap, _ := existing.(map[string]any)
		out := make(map[string]any, len(val))
		fell := false
		for k, vv := range val {
			var (
				exVal any
				exOK  bool
			)
			if exMap != nil {
				exVal, exOK = exMap[k]
			}
			r, f, err := resolveTemplateValueLeafFallback(vv, exVal, exOK, env)
			if err != nil {
				return nil, fell, err
			}
			if f {
				fell = true
			}
			out[k] = r
		}
		return out, fell, nil
	case []any:
		exSlice, _ := existing.([]any)
		out := make([]any, len(val))
		fell := false
		for i, vv := range val {
			var (
				exVal any
				exOK  bool
			)
			if exSlice != nil && i < len(exSlice) {
				exVal, exOK = exSlice[i], true
			}
			r, f, err := resolveTemplateValueLeafFallback(vv, exVal, exOK, env)
			if err != nil {
				return nil, fell, err
			}
			if f {
				fell = true
			}
			out[i] = r
		}
		return out, fell, nil
	default:
		return v, false, nil
	}
}

func containsTemplate(s string) bool {
	return strings.Contains(s, "{{")
}

// lookupBindPath resolves a dot-separated key path (e.g.
// `submitted.summary_markdown` or `submitted.names[0]`) inside a host
// result's `Data` map. Returns the leaf value and true on success, or
// (nil, false) if any segment is missing or hits a non-traversable
// value. Single-segment keys (the common case) are equivalent to a
// top-level lookup.
//
// Path segments are exact map keys, with an optional trailing `[N]`
// integer index for array fields (e.g. `names[0]` → first element of
// the names slice on the current node, or chained `outer[0].inner` to
// walk into an indexed element). N must be non-negative and in range.
// Whitespace is not stripped, so app authors should keep paths tight.
func lookupBindPath(data map[string]any, path string) (any, bool) {
	if data == nil || path == "" {
		return nil, false
	}
	var cur any = data
	for _, seg := range strings.Split(path, ".") {
		key, indices, ok := parseBindSegment(seg)
		if !ok {
			return nil, false
		}
		if key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			cur, ok = m[key]
			if !ok {
				return nil, false
			}
		}
		for _, idx := range indices {
			arr, ok := cur.([]any)
			if !ok {
				return nil, false
			}
			if idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
		}
	}
	return cur, true
}

// parseBindSegment splits a single dot-segment into its leading key and
// any trailing [N] indices. Returns (key, indices, true) on success or
// (_, _, false) on a malformed segment. An empty key (segment starts
// with `[`) is permitted so chains like `outer.[0]` could in principle
// work — in practice authors write `outer[0]` so the leading key is
// present.
func parseBindSegment(seg string) (string, []int, bool) {
	if seg == "" {
		return "", nil, false
	}
	openIdx := strings.IndexByte(seg, '[')
	if openIdx < 0 {
		return seg, nil, true
	}
	key := seg[:openIdx]
	rest := seg[openIdx:]
	var indices []int
	for len(rest) > 0 {
		if rest[0] != '[' {
			return "", nil, false
		}
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx < 0 {
			return "", nil, false
		}
		numStr := rest[1:closeIdx]
		if numStr == "" {
			return "", nil, false
		}
		n, err := strconv.Atoi(numStr)
		if err != nil {
			return "", nil, false
		}
		indices = append(indices, n)
		rest = rest[closeIdx+1:]
	}
	return key, indices, true
}
