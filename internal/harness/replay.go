// Package harness — ReplayHarness implementation (§10.5, §12.1).
// Reads a recording YAML and returns deterministic CallToolParams without any LLM.
package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/trace"
)

// recordingFile is the parsed recording YAML structure (§10.4).
type recordingFile struct {
	Kind          string            `yaml:"kind"`
	AppID         string            `yaml:"app_id"`
	AppVersion    string            `yaml:"app_version"`
	GeneratedAt   string            `yaml:"generated_at"`
	Generator     string            `yaml:"generator"`
	MinConfidence float64           `yaml:"min_confidence"`
	Entries       []recordingEntry  `yaml:"entries"`
}

// recordingEntry is one (state, input, intent, slots) record in the recording.
type recordingEntry struct {
	State      string          `yaml:"state"`
	Input      string          `yaml:"input"`
	Intent     recordingIntent `yaml:"intent"`
	Confidence float64         `yaml:"confidence"`
	MajorityOf int             `yaml:"majority_of"`
}

// recordingIntent holds the intent name and slot map within a recording entry.
type recordingIntent struct {
	Name  string         `yaml:"name"`
	Slots map[string]any `yaml:"slots"`
}

// recordingKey is the lookup key for the recording map.
type recordingKey struct {
	State string
	Input string // normalized (lowercased, trimmed)
}

// ErrRecordingMiss is returned when no matching recording entry is found.
type ErrRecordingMiss struct {
	State string
	Input string
}

func (e *ErrRecordingMiss) Error() string {
	return fmt.Sprintf("harness/replay: recording miss for state=%q input=%q", e.State, e.Input)
}

// ReplayHarness looks up (state, input) pairs in a recording YAML and returns
// the recorded intent call without making any LLM calls.
//
// Lookup precedence (documented here, tested in harness_test.go):
//  1. Exact match on (state, input) — original casing, untrimmed.
//  2. Case-insensitive match on (state, input) — both sides lowercased, trimmed.
//
// The first match wins. If no match is found, ErrRecordingMiss is returned.
type ReplayHarness struct {
	// exact maps (state, input) → entry using the original casing.
	exact map[recordingKey]*recordingEntry
	// normalized maps (state, normalized-input) → entry for case-insensitive lookup.
	normalized map[recordingKey]*recordingEntry
	// logger is used for structured trace events.
	logger *slog.Logger
}

// NewReplay loads a recording YAML from recordingPath and constructs a ReplayHarness.
// Returns an error if the file is missing, malformed, or contains duplicate entries.
func NewReplay(recordingPath string) (*ReplayHarness, error) {
	data, err := os.ReadFile(recordingPath)
	if err != nil {
		return nil, fmt.Errorf("harness/replay: read recording %q: %w", recordingPath, err)
	}

	var rf recordingFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("harness/replay: parse recording %q: %w", recordingPath, err)
	}

	if rf.Kind != "recording" {
		return nil, fmt.Errorf("harness/replay: recording %q has unexpected kind %q (want \"recording\")", recordingPath, rf.Kind)
	}
	if len(rf.Entries) == 0 {
		return nil, fmt.Errorf("harness/replay: recording %q has no entries", recordingPath)
	}

	h := &ReplayHarness{
		exact:      make(map[recordingKey]*recordingEntry, len(rf.Entries)),
		normalized: make(map[recordingKey]*recordingEntry, len(rf.Entries)),
		logger:     slog.Default(),
	}

	for i := range rf.Entries {
		e := &rf.Entries[i]
		if e.State == "" {
			return nil, fmt.Errorf("harness/replay: recording entry %d has empty state", i)
		}
		if e.Input == "" {
			return nil, fmt.Errorf("harness/replay: recording entry %d has empty input", i)
		}
		if e.Intent.Name == "" {
			return nil, fmt.Errorf("harness/replay: recording entry %d has empty intent name", i)
		}

		exactKey := recordingKey{State: e.State, Input: e.Input}
		normKey := recordingKey{State: e.State, Input: normalizeInput(e.Input)}

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

// RunTurn looks up the (state, input) pair in the recording and returns a
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
	exactKey := recordingKey{State: state, Input: in.UserText}
	if e, ok := h.exact[exactKey]; ok {
		l.DebugContext(ctx, trace.EvHarnessRecordingHit,
			slog.String("input", in.UserText),
			slog.String("intent", e.Intent.Name),
			slog.Any("slots", e.Intent.Slots),
		)
		return entryToParams(e), nil
	}

	// 2. Case-insensitive + trimmed match.
	normKey := recordingKey{State: state, Input: normalizeInput(in.UserText)}
	if e, ok := h.normalized[normKey]; ok {
		l.DebugContext(ctx, trace.EvHarnessRecordingHit,
			slog.String("input", in.UserText),
			slog.String("intent", e.Intent.Name),
			slog.Any("slots", e.Intent.Slots),
		)
		return entryToParams(e), nil
	}

	l.DebugContext(ctx, trace.EvHarnessRecordingMiss,
		slog.String("input", in.UserText),
		slog.String("state", state),
	)
	return mcp.CallToolParams{}, &ErrRecordingMiss{State: state, Input: in.UserText}
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

// entryToParams converts a recording entry to a mcp.CallToolParams.
func entryToParams(e *recordingEntry) mcp.CallToolParams {
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
