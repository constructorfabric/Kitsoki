// Package tui — warp-basis file loader.
//
// A "warp basis" is a small YAML file that captures everything `/warp`
// needs to jump a session into a primed mid-game state:
//
//	state: leg_c_awaiting_reply
//	world:
//	  money: 400
//	  party_alive: 5
//	  current_landmark: "Chimney Rock"
//
// Authors check these into the repo alongside flow tests so smoke
// scenarios are repeatable and parameter-free. The TUI's `/warp` slash
// command loads them via `/warp file:./scenarios/<name>.yaml` (or just
// `/warp ./scenarios/<name>.yaml`).
//
// Distinct from flow fixtures: a flow fixture drives a sequence of
// turns and asserts; a warp basis only sets up state for an
// interactive operator to take over. The shape overlaps intentionally
// so existing fixtures can be re-used as warp bases (the same
// `initial_state` + `initial_world` keys are also accepted).
package tui

import (
	"fmt"
	"os"
	"path/filepath"

	goyaml "github.com/goccy/go-yaml"
)

// WarpBasis is the parsed shape of a warp-basis YAML file. Both the
// canonical (`state` + `world`) and the flow-fixture-aliased
// (`initial_state` + `initial_world`) field sets are accepted; the
// loader merges them into the canonical fields.
//
// Optional `name` and `description` are echoed back to the transcript
// for operator confirmation when the warp dispatches.
type WarpBasis struct {
	Name        string         `yaml:"name,omitempty"`
	Description string         `yaml:"description,omitempty"`
	State       string         `yaml:"state,omitempty"`
	World       map[string]any `yaml:"world,omitempty"`

	// Flow-fixture-shape fallbacks. Populated by loadWarpBasis only
	// when the canonical fields are absent.
	InitialState string         `yaml:"initial_state,omitempty"`
	InitialWorld map[string]any `yaml:"initial_world,omitempty"`
}

// LoadWarpBasis is the exported wrapper used by `kitsoki run --warp`
// to apply a basis at session boot. The TUI's interactive
// `/warp file:<path>` path uses the unexported loadWarpBasis directly.
// Same semantics, including the app-relative path fallback.
func LoadWarpBasis(path, appPath string) (string, *WarpBasis, error) {
	return loadWarpBasis(path, appPath)
}

// loadWarpBasis reads, parses, and normalises a warp-basis YAML.
//
// Path resolution: tries (a) the path as-is (absolute or relative to
// cwd), then (b) relative to the loaded app's directory. The second
// rule lets `/warp file:scenarios/foo.yaml` work when the operator
// types from the repo root regardless of what cwd kitsoki was
// launched with.
//
// Returns the resolved absolute path (for messaging) and the parsed
// basis. Errors are wrapped with the original input path so the
// transcript message is actionable.
func loadWarpBasis(path, appPath string) (string, *WarpBasis, error) {
	candidates := []string{path}
	if !filepath.IsAbs(path) && appPath != "" {
		candidates = append(candidates, filepath.Join(filepath.Dir(appPath), path))
	}

	var (
		resolved string
		raw      []byte
		lastErr  error
	)
	for _, candidate := range candidates {
		b, err := os.ReadFile(candidate)
		if err == nil {
			resolved = candidate
			raw = b
			break
		}
		lastErr = err
	}
	if raw == nil {
		return path, nil, fmt.Errorf("read %q: %w", path, lastErr)
	}

	var basis WarpBasis
	if err := goyaml.UnmarshalWithOptions(raw, &basis, goyaml.Strict()); err != nil {
		return resolved, nil, fmt.Errorf("parse %s: %v", resolved, err)
	}

	// Normalise: flow-fixture aliases populate the canonical fields when
	// they're not already set.
	if basis.State == "" && basis.InitialState != "" {
		basis.State = basis.InitialState
	}
	if len(basis.World) == 0 && len(basis.InitialWorld) > 0 {
		basis.World = basis.InitialWorld
	}

	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	return resolved, &basis, nil
}
