// Package testrunner — StaticHarness is a test-only Harness implementation
// that returns canned intents from a pre-loaded map. Not for production use.
//
// It also supports a configurable noise function so the Mode 1 runner's
// statistical reporting is exercisable without a real LLM.
package testrunner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/harness"
)

// staticKey is the lookup key for StaticHarness entries.
type staticKey struct {
	State string
	Input string // lowercased + trimmed
}

// StaticHarness is a test-only Harness that returns canned intents from a map.
// Keyed by (state, normalised-input). When a NoiseFunc is set, it is called
// with the call count (1-indexed) before returning and may alter the result.
type StaticHarness struct {
	// entries maps (state, normalised-input) → CallToolParams.
	entries map[staticKey]mcp.CallToolParams
	// callCount tracks total calls for noise injection.
	callCount int
	// NoiseFunc, if non-nil, is called before returning to inject noise.
	// count is the 1-indexed call number; params is the canonical result.
	// The function may return a different params to simulate mis-routing.
	NoiseFunc func(count int, canonical mcp.CallToolParams) mcp.CallToolParams
}

// staticOracleIntent is the intent block inside a static oracle entry.
type staticOracleIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots"`
}

// staticOracleEntry is one row in the static oracle YAML.
type staticOracleEntry struct {
	State      string             `yaml:"state"`
	Input      string             `yaml:"input"`
	Intent     staticOracleIntent `yaml:"intent"`
	Confidence float64            `yaml:"confidence"`
	MajorityOf int                `yaml:"majority_of"`
}

// staticOracleFile is the top-level structure of the oracle YAML.
type staticOracleFile struct {
	Kind    string              `yaml:"kind"`
	Entries []staticOracleEntry `yaml:"entries"`
}

// NewStaticHarnessFromOracle parses an oracle YAML and builds a StaticHarness.
// The oracle format is identical to the replay-harness oracle (§10.4).
func NewStaticHarnessFromOracle(oraclePath string) (*StaticHarness, error) {
	data, err := os.ReadFile(oraclePath)
	if err != nil {
		return nil, fmt.Errorf("static harness: read oracle %q: %w", oraclePath, err)
	}

	var of staticOracleFile
	if err := yaml.Unmarshal(data, &of); err != nil {
		return nil, fmt.Errorf("static harness: parse oracle %q: %w", oraclePath, err)
	}

	if of.Kind != "oracle" {
		return nil, fmt.Errorf("static harness: oracle %q has kind %q (want \"oracle\")", oraclePath, of.Kind)
	}

	entries := make(map[staticKey]mcp.CallToolParams, len(of.Entries))
	for i, e := range of.Entries {
		if e.State == "" || e.Input == "" || e.Intent.Name == "" {
			return nil, fmt.Errorf("static harness: oracle entry %d is incomplete (state=%q input=%q intent=%q)", i, e.State, e.Input, e.Intent.Name)
		}
		key := staticKey{
			State: e.State,
			Input: strings.ToLower(strings.TrimSpace(e.Input)),
		}
		args := map[string]any{"intent": e.Intent.Name}
		if e.Intent.Slots != nil {
			args["slots"] = e.Intent.Slots
		}
		// First entry wins (matches ReplayHarness behaviour).
		if _, dup := entries[key]; !dup {
			entries[key] = mcp.CallToolParams{
				Name:      "transition",
				Arguments: args,
			}
		}
	}

	return &StaticHarness{entries: entries}, nil
}

// NewStaticHarnessFromMap creates a StaticHarness from an explicit entries map.
// Pass nil to get an empty harness (all lookups will fail).
// This is the low-level constructor used in tests within this package.
func NewStaticHarnessFromMap(entries map[staticKey]mcp.CallToolParams) *StaticHarness {
	if entries == nil {
		entries = make(map[staticKey]mcp.CallToolParams)
	}
	return &StaticHarness{entries: entries}
}

// NewEmptyStaticHarness creates a StaticHarness with no entries.
// All RunTurn calls will return an error. Useful for dry-run tests.
func NewEmptyStaticHarness() *StaticHarness {
	return &StaticHarness{entries: make(map[staticKey]mcp.CallToolParams)}
}

// WithNoiseEveryN returns a pointer to a NEW StaticHarness (shallow copy) with
// a NoiseFunc that substitutes wrongIntent on every nth call (1-indexed, n>0).
func (h *StaticHarness) WithNoiseEveryN(n int, wrongIntent string) *StaticHarness {
	clone := &StaticHarness{
		entries:   h.entries,
		callCount: 0,
	}
	clone.NoiseFunc = func(count int, canonical mcp.CallToolParams) mcp.CallToolParams {
		if n > 0 && count%n == 0 {
			return mcp.CallToolParams{
				Name:      "transition",
				Arguments: map[string]any{"intent": wrongIntent, "slots": map[string]any{}},
			}
		}
		return canonical
	}
	return clone
}

// RunTurn implements harness.Harness. It looks up the canned intent for
// (state, normalised-input) and optionally applies the NoiseFunc.
func (h *StaticHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	h.callCount++
	state := string(in.StatePath)
	normInput := strings.ToLower(strings.TrimSpace(in.UserText))

	key := staticKey{State: state, Input: normInput}
	params, ok := h.entries[key]
	if !ok {
		return mcp.CallToolParams{}, fmt.Errorf("static harness: no entry for state=%q input=%q (normalised: %q)", state, in.UserText, normInput)
	}

	if h.NoiseFunc != nil {
		params = h.NoiseFunc(h.callCount, params)
	}

	return params, nil
}

// Close is a no-op for StaticHarness.
func (h *StaticHarness) Close() error { return nil }

// Ensure StaticHarness satisfies the Harness interface at compile time.
var _ harness.Harness = (*StaticHarness)(nil)
