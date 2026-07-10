// Package kitverify implements the S4 kit conformance checks
// (.context/kits-implementation-plan.md "S4 — conformance"): standalone
// kit-boundary checks that work against a kit manifest + its stories without
// needing a full importer, plus the `kitsoki kit verify` orchestration that
// runs those checks alongside the kit's own no-LLM conformance flow suite.
//
// This package sits above internal/app, internal/kit, and
// internal/host/opschema (all of which already export everything it needs),
// so it introduces no new import edges into any of them.
package kitverify

import (
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// CheckExitRequires walks every transition in def's state tree and verifies
// that a transition targeting an exit sets every world key
// def.Exits[X].Requires lists, either via the transition's own
// Set/Bind/Increment effects or via a non-zero world: default (already set
// at world init).
//
// This is the standalone (no-importer) twin of the identical check
// internal/app's import-fold pass runs at fold time
// (imports.go:checkExitRequires, run once per import site inside
// foldChild) — that check only ever runs when the story in question is
// folded under an importer. A kit's `provides.stories` contract says a
// story must be standalone-loadable (kit.Load's story-dir check); CheckExitRequires
// is meant to run against exactly that standalone-loaded AppDef
// (app.Load/app.LoadWithResolver on the story's own app.yaml as the ROOT
// manifest). At that load, materialiseStandaloneExits
// (internal/app/loader.go) has ALREADY rewritten every `@exit:X` transition
// target to a synthesized terminal state `__exit__X` before Load returns —
// unlike foldChild's check, which runs on an unrewritten child mid-fold —
// so this walker matches on the `__exit__` form instead of `@exit:`.
func CheckExitRequires(def *app.AppDef) []string {
	if def == nil {
		return nil
	}
	defaulted := make(map[string]struct{}, len(def.World))
	for k, v := range def.World {
		if _, reserved := app.ReservedWorldKeys[k]; reserved {
			continue
		}
		if v.Default != nil && !isZeroDefault(v.Default) {
			defaulted[k] = struct{}{}
		}
	}
	var out []string
	walkExitRequires("", def.States, def.Exits, defaulted, &out)
	sort.Strings(out)
	return out
}

func walkExitRequires(statePath string, states map[string]*app.State, exits map[string]*app.ExitDef, defaulted map[string]struct{}, out *[]string) {
	for name, s := range states {
		if s == nil {
			continue
		}
		path := name
		if statePath != "" {
			path = statePath + "." + name
		}
		for intentName, list := range s.On {
			for _, tr := range list {
				exitName, ok := exitNameFromTarget(tr.Target)
				if !ok {
					continue
				}
				exit, ok := exits[exitName]
				if !ok || exit == nil || len(exit.Requires) == 0 {
					continue
				}
				setKeys := make(map[string]struct{})
				for _, eff := range tr.Effects {
					for k := range eff.Set {
						setKeys[k] = struct{}{}
					}
					for k := range eff.Bind {
						setKeys[k] = struct{}{}
					}
					for k := range eff.Increment {
						setKeys[k] = struct{}{}
					}
				}
				var missing []string
				for _, req := range exit.Requires {
					if _, ok := setKeys[req]; ok {
						continue
					}
					if _, ok := defaulted[req]; ok {
						continue
					}
					missing = append(missing, req)
				}
				if len(missing) > 0 {
					*out = append(*out, fmt.Sprintf(
						"state %q intent %q transitions to @exit:%s but does not set required key(s) %v (declared in exits.%s.requires)",
						path, intentName, exitName, missing, exitName))
				}
			}
		}
		if len(s.States) > 0 {
			walkExitRequires(path, s.States, exits, defaulted, out)
		}
	}
}

// exitNameFromTarget recognizes both the raw author-written `@exit:X` form
// and the `__exit__X` form materialiseStandaloneExits rewrites it to at
// standalone-load time (see CheckExitRequires's doc comment). Accepting
// both makes this walker usable either against a freshly standalone-loaded
// AppDef (the `kitsoki kit verify` case) or against a def built/decoded
// without going through that rewrite pass.
func exitNameFromTarget(target string) (string, bool) {
	if strings.HasPrefix(target, "@exit:") {
		return strings.TrimPrefix(target, "@exit:"), true
	}
	if strings.HasPrefix(target, "__exit__") {
		return strings.TrimPrefix(target, "__exit__"), true
	}
	return "", false
}

// isZeroDefault mirrors internal/app's unexported helper of the same name
// (imports.go) — a world: default is only "provably set at runtime" when
// it's a non-zero value; a zero-value default (empty string, 0, false, ...)
// is indistinguishable from "never set" for this check's purposes.
func isZeroDefault(v any) bool {
	switch x := v.(type) {
	case string:
		return x == ""
	case int:
		return x == 0
	case int64:
		return x == 0
	case float64:
		return x == 0
	case bool:
		return !x
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}
