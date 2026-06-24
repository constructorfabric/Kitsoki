// Throwaway prototype for continue-mode-spike R1.
// Demonstrates that a schema-aware applier — built around evanphx/json-patch
// plus a coerceWorldVar-style post-step that consults app.WorldSchema —
// preserves declared types across a JSON-Patch round-trip.
//
// Run with: go run ./docs/proposals/notes/spike/r1-applier
//
// This file lives under docs/ and is NOT compiled into the production
// binary (kitsoki main packages live in cmd/ and internal/).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/goccy/go-yaml"
)

// vardef mirrors app.VarDef enough for the prototype.
type vardef struct {
	Type    string `yaml:"type"`
	Default any    `yaml:"default,omitempty"`
}

type appdef struct {
	App struct {
		ID string `yaml:"id"`
	} `yaml:"app"`
	World map[string]vardef `yaml:"world"`
}

// coerceWorldVar mirrors internal/store/replay.go:179. The production
// version only handles "int"; we extend the prototype to also handle
// "bool" and "string" so the test exercises every declared type in the
// fixtures we walked through.
func coerceWorldVar(def *appdef, key string, v any) any {
	if def == nil {
		return v
	}
	vd, ok := def.World[key]
	if !ok {
		return v
	}
	switch vd.Type {
	case "int":
		switch x := v.(type) {
		case float64:
			return int64(x)
		case float32:
			return int64(x)
		case int:
			return int64(x)
		case int32:
			return int64(x)
		case int64:
			return x
		}
	case "bool":
		switch x := v.(type) {
		case bool:
			return x
		case float64:
			// Defensive: nobody should be passing a numeric for a bool, but if they do…
			return x != 0
		}
	case "string":
		switch x := v.(type) {
		case string:
			return x
		}
	}
	return v
}

// applier walks a world document, applies an RFC-6902 op list via
// evanphx/json-patch, then post-coerces every top-level vars entry that
// the schema knows about. The applier returns a typed Go map
// (map[string]any with engine-side declared types) rather than bytes,
// because re-encoding to JSON would discard the int64-vs-float64
// distinction that downstream expr-lang guards depend on. Callers that
// need to persist the result re-marshal at the storage edge.
// (Engine-injected keys — $inbox, $proposal, $jobs.* — bypass coercion;
// see proposal §6.1 case 4.)
func applier(def *appdef, current []byte, ops []map[string]any) (map[string]any, error) {
	patchBytes, err := json.Marshal(ops)
	if err != nil {
		return nil, fmt.Errorf("marshal patch: %w", err)
	}
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		return nil, fmt.Errorf("decode patch: %w", err)
	}
	out, err := patch.Apply(current)
	if err != nil {
		return nil, fmt.Errorf("apply patch: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal after apply: %w", err)
	}
	rawVars, ok := doc["vars"].(map[string]any)
	if ok {
		for k, v := range rawVars {
			rawVars[k] = coerceWorldVar(def, k, v)
		}
	}
	return doc, nil
}

// goValueForVar builds the canonical Go type for a schema entry's default,
// so the "expected" comparison after round-trip is against a value typed
// the way the engine writes at run-time (int64 for int, etc.).
func goValueForVar(vd vardef) any {
	switch vd.Type {
	case "int":
		switch v := vd.Default.(type) {
		case int:
			return int64(v)
		case int64:
			return v
		case float64:
			return int64(v)
		case uint64:
			return int64(v)
		default:
			return int64(0)
		}
	case "bool":
		b, _ := vd.Default.(bool)
		return b
	case "string":
		s, _ := vd.Default.(string)
		return s
	}
	return vd.Default
}

func sortedKeys(m map[string]vardef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// roundtrip drives one fixture: build a world doc from schema defaults,
// JSON-encode it, replace every var via an RFC-6902 op with its declared
// value, decode through the applier, and report any mismatches.
func roundtrip(path string) (passed, failed int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	var def appdef
	if err := yaml.Unmarshal(raw, &def); err != nil {
		return 0, 0, fmt.Errorf("yaml: %w", err)
	}
	if len(def.World) == 0 {
		return 0, 0, nil
	}

	// Seed: empty doc with a vars: {} object.
	seed := []byte(`{"vars":{}}`)

	// Build one "replace" op per schema var with the schema's default.
	ops := make([]map[string]any, 0, len(def.World))
	expected := make(map[string]any, len(def.World))
	for _, k := range sortedKeys(def.World) {
		vd := def.World[k]
		ops = append(ops, map[string]any{
			"op":    "add",
			"path":  "/vars/" + k,
			"value": vd.Default,
		})
		expected[k] = goValueForVar(vd)
	}

	doc, err := applier(&def, seed, ops)
	if err != nil {
		return 0, 0, fmt.Errorf("applier: %w", err)
	}
	gotVars, _ := doc["vars"].(map[string]any)

	for _, k := range sortedKeys(def.World) {
		exp := expected[k]
		actual := gotVars[k]
		if !reflect.DeepEqual(exp, actual) {
			failed++
			fmt.Printf("  FAIL key=%q type=%s expected=%v (%T) got=%v (%T)\n",
				k, def.World[k].Type, exp, exp, actual, actual)
		} else {
			passed++
		}
	}
	return passed, failed, nil
}

func main() {
	root := "/home/cloud-user/code/kitsoki/testdata/apps"
	matches, err := filepath.Glob(filepath.Join(root, "*", "app.yaml"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	totalPass, totalFail := 0, 0
	for _, m := range matches {
		fmt.Printf("\n=== %s ===\n", m)
		p, f, err := roundtrip(m)
		if err != nil {
			fmt.Printf("  ERROR: %v\n", err)
			continue
		}
		fmt.Printf("  vars passed: %d  failed: %d\n", p, f)
		totalPass += p
		totalFail += f
	}
	fmt.Printf("\nTOTAL passed=%d failed=%d\n", totalPass, totalFail)
	if totalFail > 0 {
		os.Exit(1)
	}
}
