package harness

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/goccy/go-yaml"
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/trace"
)

// recordingFile is the parsed recording YAML structure (see
// docs/tracing/cassettes.md for the on-disk format).
type recordingFile struct {
	Kind          string           `yaml:"kind"`
	AppID         string           `yaml:"app_id,omitempty"`
	AppVersion    string           `yaml:"app_version,omitempty"`
	GeneratedAt   string           `yaml:"generated_at,omitempty"`
	Generator     string           `yaml:"generator,omitempty"`
	MinConfidence float64          `yaml:"min_confidence,omitempty"`
	Entries       []recordingEntry `yaml:"entries"`
}

// recordingEntry is one (state, input, intent, slots) record in the recording.
//
// An entry is one of two kinds:
//
//   - Intent entry (the default): `intent.name` is set and the entry maps the
//     (state, input) pair to that intent call.
//   - Clarify entry: `clarify: true` is set and `intent` is omitted. The entry
//     deterministically reproduces the "router couldn't map this utterance"
//     outcome — RunTurn returns a [*ClarifyResponse] (the same error type the
//     live harness returns when the LLM answers without calling the tool),
//     carrying the optional `message:` as free-form text. This lets a no-LLM
//     replay demo trigger the orchestrator's clarify branch (and, for an
//     off-ramp room, the agent off-ramp) without an LLM.
//
// Example clarify entry:
//
//   - state: "ROOT.lobby"
//     input: "tell me a joke about quantum physics"
//     clarify: true
//     message: "I can route you to a room, but I can't free-associate."
//
// # Paraphrase tier (2.5)
//
// `paraphrases:` is an optional list of additional free-text strings that
// resolve identically to `input:` — a pre-recorded pool of rephrasings of
// the same utterance, pinned at record time (hand-authored or generated
// once, offline, by a human/agent — never at replay time and never by a
// live model in CI; see docs/tracing/testing.md#paraphrase-tier-25). Each
// pool member is indexed exactly like `input:` (exact, then
// case-insensitive+trimmed) — this is NOT semantic/embedding matching at
// runtime; it reuses the same exact-match discipline over a wider,
// pre-recorded set of strings, so an utterance outside the pool still
// misses (ErrRecordingMiss), same as today. This is what makes free-text
// realism in flow fixtures/swarm tier 2 not bottlenecked on a single exact
// string without adding a live-model dependency anywhere in the replay
// path.
type recordingEntry struct {
	State      string          `yaml:"state"`
	Input      string          `yaml:"input"`
	Intent     recordingIntent `yaml:"intent,omitempty"`
	Confidence float64         `yaml:"confidence,omitempty"`
	MajorityOf int             `yaml:"majority_of,omitempty"`
	// Clarify marks this entry as a deterministic no-match: RunTurn returns a
	// *ClarifyResponse instead of an intent. When true, `intent` is ignored
	// (and must be omitted; see the load-time invariant in NewReplay).
	Clarify bool `yaml:"clarify,omitempty"`
	// Message is the free-form clarification text surfaced when Clarify is true.
	// Optional; the orchestrator falls back to a generic hint when empty.
	Message string `yaml:"message,omitempty"`
	// Paraphrases is the pre-recorded paraphrase pool (tier 2.5): additional
	// utterances that resolve to this same entry's outcome. See the type
	// doc comment above for the matching contract.
	Paraphrases []string `yaml:"paraphrases,omitempty"`
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
//
// A matched entry yields one of two outcomes:
//   - Intent entry: RunTurn returns the recorded intent call.
//   - Clarify entry (`clarify: true`): RunTurn returns a [*ClarifyResponse]
//     with the entry's `message:`, identical to the live harness's "the LLM
//     couldn't map this" path. This lets a deterministic, no-LLM replay demo
//     drive the orchestrator's clarify branch (and an opt-in agent off-ramp).
//     See recordingEntry for the on-disk shape.
type ReplayHarness struct {
	// exact maps (state, input) → entry using the original casing.
	exact map[recordingKey]*recordingEntry
	// normalized maps (state, normalized-input) → entry for case-insensitive lookup.
	normalized map[recordingKey]*recordingEntry
	// logger is used for structured trace events.
	logger *slog.Logger

	// lastConfidence is the confidence of the most recently resolved intent
	// entry, exposed via LastConfidence for headless drivers (kitsoki drive)
	// that surface routing confidence per turn. Guarded by mu.
	mu             sync.Mutex
	lastConfidence float64
}

// ConfidenceReporter is implemented by harnesses that can report the routing
// confidence of their most recent resolved turn. kitsoki drive type-asserts on
// it to fill the per-turn JSONL `confidence` field without reaching into the
// orchestrator's internal trace events.
type ConfidenceReporter interface {
	LastConfidence() float64
}

// LastConfidence returns the confidence of the most recently resolved intent
// entry (0 if none resolved or the entry carried no confidence).
func (h *ReplayHarness) LastConfidence() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastConfidence
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

	h, err := newReplayFromFile(rf)
	if err != nil {
		return nil, fmt.Errorf("harness/replay: recording %q: %w", recordingPath, err)
	}
	return h, nil
}

// newReplayFromFile indexes an already-parsed recordingFile into a
// ReplayHarness. Factored out of NewReplay so an in-memory cassette (the VCR
// harness's growing recording.yaml) can be re-indexed after each append
// without a disk round-trip. The same per-entry load-time invariants apply.
func newReplayFromFile(rf recordingFile) (*ReplayHarness, error) {
	h := &ReplayHarness{
		exact:      make(map[recordingKey]*recordingEntry, len(rf.Entries)),
		normalized: make(map[recordingKey]*recordingEntry, len(rf.Entries)),
		logger:     slog.Default(),
	}

	for i := range rf.Entries {
		e := &rf.Entries[i]
		if e.State == "" {
			return nil, fmt.Errorf("entry %d has empty state", i)
		}
		if e.Input == "" {
			return nil, fmt.Errorf("entry %d has empty input", i)
		}
		if e.Clarify {
			// A clarify entry yields a *ClarifyResponse, not an intent — the
			// two kinds are mutually exclusive to keep authoring unambiguous.
			if e.Intent.Name != "" {
				return nil, fmt.Errorf("entry %d sets clarify:true and intent.name=%q (a clarify entry must omit intent)", i, e.Intent.Name)
			}
		} else if e.Intent.Name == "" {
			return nil, fmt.Errorf("entry %d has empty intent name (set intent.name, or clarify:true for a deterministic no-match)", i)
		}

		for j, p := range e.Paraphrases {
			if strings.TrimSpace(p) == "" {
				return nil, fmt.Errorf("entry %d has empty paraphrase at index %d", i, j)
			}
		}

		// Index the primary input plus every paraphrase-pool member under the
		// same entry (tier 2.5): all of them resolve identically. First
		// registration wins (preserves YAML source order) — same precedent as
		// the plain-input dedup rule below.
		inputs := make([]string, 0, 1+len(e.Paraphrases))
		inputs = append(inputs, e.Input)
		inputs = append(inputs, e.Paraphrases...)
		for _, in := range inputs {
			exactKey := recordingKey{State: e.State, Input: in}
			normKey := recordingKey{State: e.State, Input: normalizeInput(in)}

			// First entry wins (preserves YAML source order).
			if _, dup := h.exact[exactKey]; !dup {
				h.exact[exactKey] = e
			}
			if _, dup := h.normalized[normKey]; !dup {
				h.normalized[normKey] = e
			}
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
		return h.resolve(ctx, l, in.UserText, e)
	}

	// 2. Case-insensitive + trimmed match.
	normKey := recordingKey{State: state, Input: normalizeInput(in.UserText)}
	if e, ok := h.normalized[normKey]; ok {
		return h.resolve(ctx, l, in.UserText, e)
	}

	l.DebugContext(ctx, trace.EvHarnessRecordingMiss,
		slog.String("input", in.UserText),
		slog.String("state", state),
	)
	return mcp.CallToolParams{}, &ErrRecordingMiss{State: state, Input: in.UserText}
}

// resolve turns a matched recording entry into a RunTurn result: an intent
// call for an ordinary entry, or a *ClarifyResponse for a clarify entry (the
// same error type the live harness returns when the LLM can't map an
// utterance, so the orchestrator's clarify branch — and any opt-in off-ramp —
// fires identically under replay).
func (h *ReplayHarness) resolve(ctx context.Context, l *slog.Logger, input string, e *recordingEntry) (mcp.CallToolParams, error) {
	if e.Clarify {
		l.DebugContext(ctx, trace.EvHarnessRecordingHit,
			slog.String("input", input),
			slog.String("outcome", "clarify"),
			slog.String("message", e.Message),
		)
		return mcp.CallToolParams{}, &ClarifyResponse{Message: e.Message}
	}
	h.mu.Lock()
	h.lastConfidence = e.Confidence
	h.mu.Unlock()
	l.DebugContext(ctx, trace.EvHarnessRecordingHit,
		slog.String("input", input),
		slog.String("intent", e.Intent.Name),
		slog.Any("slots", e.Intent.Slots),
	)
	return entryToParams(e), nil
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

// entryToParams converts a recording entry to a mcp.CallToolParams. The
// recorded confidence is carried through on the arguments so the orchestrator's
// routing breadcrumb (EvTurnLLMRouted) and any confidence-reading consumer
// (kitsoki drive's per-turn JSONL) observe the cassette's confidence under
// replay — identical to the field the live harness populates.
func entryToParams(e *recordingEntry) mcp.CallToolParams {
	args := map[string]any{
		"intent": e.Intent.Name,
	}
	if e.Intent.Slots != nil {
		args["slots"] = e.Intent.Slots
	}
	if e.Confidence != 0 {
		args["confidence"] = e.Confidence
	}
	return mcp.CallToolParams{
		Name:      "transition",
		Arguments: args,
	}
}
