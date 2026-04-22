// Package harness — ReplayHarness implementation (§10.5, §12.1).
// Reads an oracle YAML and returns deterministic CallToolParams without any LLM.
package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"hally/internal/trace"
)

// oracleFile is the parsed oracle YAML structure (§10.4).
type oracleFile struct {
	Kind          string         `yaml:"kind"`
	AppID         string         `yaml:"app_id"`
	AppVersion    string         `yaml:"app_version"`
	GeneratedAt   string         `yaml:"generated_at"`
	Generator     string         `yaml:"generator"`
	MinConfidence float64        `yaml:"min_confidence"`
	Entries       []oracleEntry  `yaml:"entries"`
}

// oracleEntry is one (state, input, intent, slots) record in the oracle.
type oracleEntry struct {
	State   string      `yaml:"state"`
	Input   string      `yaml:"input"`
	Intent  oracleIntent `yaml:"intent"`
	Confidence float64 `yaml:"confidence"`
	MajorityOf int    `yaml:"majority_of"`
}

// oracleIntent holds the intent name and slot map within an oracle entry.
type oracleIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots"`
}

// oracleKey is the lookup key for the oracle map.
type oracleKey struct {
	State string
	Input string // normalized (lowercased, trimmed)
}

// ErrOracleMiss is returned when no matching oracle entry is found.
type ErrOracleMiss struct {
	State string
	Input string
}

func (e *ErrOracleMiss) Error() string {
	return fmt.Sprintf("harness/replay: oracle miss for state=%q input=%q", e.State, e.Input)
}

// ReplayHarness looks up (state, input) pairs in an oracle YAML and returns
// the recorded intent call without making any LLM calls.
//
// Lookup precedence (documented here, tested in harness_test.go):
//  1. Exact match on (state, input) — original casing, untrimmed.
//  2. Case-insensitive match on (state, input) — both sides lowercased, trimmed.
//
// The first match wins. If no match is found, ErrOracleMiss is returned.
type ReplayHarness struct {
	// exact maps (state, input) → entry using the original casing.
	exact map[oracleKey]*oracleEntry
	// normalized maps (state, normalized-input) → entry for case-insensitive lookup.
	normalized map[oracleKey]*oracleEntry
	// logger is used for structured trace events.
	logger *slog.Logger
}

// NewReplay loads an oracle YAML from oraclePath and constructs a ReplayHarness.
// Returns an error if the file is missing, malformed, or contains duplicate entries.
func NewReplay(oraclePath string) (*ReplayHarness, error) {
	data, err := os.ReadFile(oraclePath)
	if err != nil {
		return nil, fmt.Errorf("harness/replay: read oracle %q: %w", oraclePath, err)
	}

	var of oracleFile
	if err := yaml.Unmarshal(data, &of); err != nil {
		return nil, fmt.Errorf("harness/replay: parse oracle %q: %w", oraclePath, err)
	}

	if of.Kind != "oracle" {
		return nil, fmt.Errorf("harness/replay: oracle %q has unexpected kind %q (want \"oracle\")", oraclePath, of.Kind)
	}
	if len(of.Entries) == 0 {
		return nil, fmt.Errorf("harness/replay: oracle %q has no entries", oraclePath)
	}

	h := &ReplayHarness{
		exact:      make(map[oracleKey]*oracleEntry, len(of.Entries)),
		normalized: make(map[oracleKey]*oracleEntry, len(of.Entries)),
		logger:     slog.Default(),
	}

	for i := range of.Entries {
		e := &of.Entries[i]
		if e.State == "" {
			return nil, fmt.Errorf("harness/replay: oracle entry %d has empty state", i)
		}
		if e.Input == "" {
			return nil, fmt.Errorf("harness/replay: oracle entry %d has empty input", i)
		}
		if e.Intent.Name == "" {
			return nil, fmt.Errorf("harness/replay: oracle entry %d has empty intent name", i)
		}

		exactKey := oracleKey{State: e.State, Input: e.Input}
		normKey := oracleKey{State: e.State, Input: normalizeInput(e.Input)}

		// First entry wins (preserves YAML source order).
		if _, dup := h.exact[exactKey]; !dup {
			h.exact[exactKey] = e
		}
		if _, dup := h.normalized[normKey]; !dup {
			h.normalized[normKey] = e
		}
	}

	return h, nil
}

// RunTurn looks up the (state, input) pair in the oracle and returns a
// mcp.CallToolParams for the matched intent.
func (h *ReplayHarness) RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error) {
	state := string(in.StatePath)

	l := h.logger.With(
		slog.String("session_id", string(in.SessionID)),
		slog.Int64("turn", int64(in.TurnNumber)),
		slog.String("state_path", state),
	)

	l.DebugContext(ctx, trace.EvHarnessRequest,
		slog.String("input", in.UserText),
		slog.Int("allowed_intents", len(in.AllowedIntents)),
	)

	// 1. Exact match.
	exactKey := oracleKey{State: state, Input: in.UserText}
	if e, ok := h.exact[exactKey]; ok {
		l.DebugContext(ctx, trace.EvHarnessOracleHit,
			slog.String("input", in.UserText),
			slog.String("intent", e.Intent.Name),
			slog.Any("slots", e.Intent.Slots),
		)
		return entryToParams(e), nil
	}

	// 2. Case-insensitive + trimmed match.
	normKey := oracleKey{State: state, Input: normalizeInput(in.UserText)}
	if e, ok := h.normalized[normKey]; ok {
		l.DebugContext(ctx, trace.EvHarnessOracleHit,
			slog.String("input", in.UserText),
			slog.String("intent", e.Intent.Name),
			slog.Any("slots", e.Intent.Slots),
		)
		return entryToParams(e), nil
	}

	l.DebugContext(ctx, trace.EvHarnessOracleMiss,
		slog.String("input", in.UserText),
		slog.String("state", state),
	)
	return mcp.CallToolParams{}, &ErrOracleMiss{State: state, Input: in.UserText}
}

// WithLogger sets the logger for trace emission.
func (h *ReplayHarness) WithLogger(l *slog.Logger) {
	if l != nil {
		h.logger = l
	}
}

// Close is a no-op for ReplayHarness.
func (h *ReplayHarness) Close() error { return nil }

// normalizeInput lowercases and trims whitespace from an input string.
func normalizeInput(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// entryToParams converts an oracle entry to a mcp.CallToolParams.
func entryToParams(e *oracleEntry) mcp.CallToolParams {
	args := map[string]any{
		"intent": e.Intent.Name,
	}
	if e.Intent.Slots != nil {
		args["slots"] = e.Intent.Slots
	}
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: args,
	}
}
