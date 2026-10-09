package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/world"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func makeTurnInput(state, text string) harness.TurnInput {
	return harness.TurnInput{
		StatePath:      app.StatePath(state),
		UserText:       text,
		World:          world.New(),
		AllowedIntents: []string{"go", "look"},
	}
}

func extractIntent(t *testing.T, p mcp.CallToolParams) (intent string, slots map[string]any) {
	t.Helper()
	args, ok := p.Arguments.(map[string]any)
	require.True(t, ok, "arguments should be map[string]any")
	intent, _ = args["intent"].(string)
	slots, _ = args["slots"].(map[string]any)
	return intent, slots
}

// ─── ReplayHarness tests ─────────────────────────────────────────────────────

const cloakRecordingPath = "../../testdata/apps/cloak/recording.yaml"

// TestReplayHarness_AllEntries asserts that every (state, input) pair in the
// Cloak recording resolves to the expected intent without a recording miss.
func TestReplayHarness_AllEntries(t *testing.T) {
	h, err := harness.NewReplay(cloakRecordingPath)
	require.NoError(t, err)

	type wantEntry struct {
		state  string
		input  string
		intent string
		slots  map[string]any
	}
	cases := []wantEntry{
		// foyer
		{"foyer", "go south", "go", map[string]any{"direction": "south"}},
		{"foyer", "head south", "go", map[string]any{"direction": "south"}},
		{"foyer", "s", "go", map[string]any{"direction": "south"}},
		{"foyer", "go west", "go", map[string]any{"direction": "west"}},
		{"foyer", "go to the cloakroom", "go", map[string]any{"direction": "west"}},
		{"foyer", "look", "look", map[string]any{}},
		{"foyer", "look around", "look", map[string]any{}},
		// cloakroom
		{"cloakroom", "hang the cloak", "hang_cloak", map[string]any{}},
		{"cloakroom", "hang my cloak on the hook", "hang_cloak", map[string]any{}},
		{"cloakroom", "put the cloak up", "hang_cloak", map[string]any{}},
		{"cloakroom", "head east", "go", map[string]any{"direction": "east"}},
		{"cloakroom", "go east", "go", map[string]any{"direction": "east"}},
		{"cloakroom", "wear the cloak", "wear_cloak", map[string]any{}},
		{"cloakroom", "look", "look", map[string]any{}},
		// bar.dark
		{"bar.dark", "go north", "go", map[string]any{"direction": "north"}},
		{"bar.dark", "n", "go", map[string]any{"direction": "north"}},
		{"bar.dark", "leave", "go", map[string]any{"direction": "north"}},
		{"bar.dark", "look around", "look", map[string]any{}},
		// bar.lit
		{"bar.lit", "read the message", "read_message", map[string]any{}},
		{"bar.lit", "read it", "read_message", map[string]any{}},
		{"bar.lit", "go north", "go", map[string]any{"direction": "north"}},
	}

	ctx := context.Background()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.state+"/"+tc.input, func(t *testing.T) {
			params, err := h.RunTurn(ctx, makeTurnInput(tc.state, tc.input))
			require.NoError(t, err)
			assert.Equal(t, "transition", params.Name)
			intent, slots := extractIntent(t, params)
			assert.Equal(t, tc.intent, intent)
			// Compare slot values (recording stores them as interface{}, not string).
			for k, want := range tc.slots {
				got, ok := slots[k]
				if !ok {
					// Empty expected slots (map[string]any{}) means slots may be nil or empty.
					if len(tc.slots) == 0 {
						continue
					}
					t.Errorf("slot %q missing; slots=%v", k, slots)
					continue
				}
				assert.EqualValues(t, want, got, "slot %q", k)
			}
		})
	}
}

// TestReplayHarness_CaseInsensitive asserts that the case-insensitive fallback
// path works: "GO SOUTH" should resolve the same as "go south".
func TestReplayHarness_CaseInsensitive(t *testing.T) {
	h, err := harness.NewReplay(cloakRecordingPath)
	require.NoError(t, err)
	ctx := context.Background()

	params, err := h.RunTurn(ctx, makeTurnInput("foyer", "GO SOUTH"))
	require.NoError(t, err)
	intent, _ := extractIntent(t, params)
	assert.Equal(t, "go", intent)
}

// TestReplayHarness_Trim asserts that leading/trailing whitespace is handled.
func TestReplayHarness_Trim(t *testing.T) {
	h, err := harness.NewReplay(cloakRecordingPath)
	require.NoError(t, err)
	ctx := context.Background()

	params, err := h.RunTurn(ctx, makeTurnInput("foyer", "  look  "))
	require.NoError(t, err)
	intent, _ := extractIntent(t, params)
	assert.Equal(t, "look", intent)
}

// TestReplayHarness_RecordingMiss asserts that a miss returns ErrRecordingMiss.
func TestReplayHarness_RecordingMiss(t *testing.T) {
	h, err := harness.NewReplay(cloakRecordingPath)
	require.NoError(t, err)
	ctx := context.Background()

	_, err = h.RunTurn(ctx, makeTurnInput("foyer", "hack the mainframe"))
	require.Error(t, err)

	var miss *harness.ErrRecordingMiss
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "foyer", miss.State)
	assert.Equal(t, "hack the mainframe", miss.Input)
}

// TestReplayHarness_ClarifyEntry asserts that an entry marked `clarify: true`
// yields a *ClarifyResponse (the same error type the live harness returns when
// the LLM can't map an utterance), while an ordinary intent entry in the same
// recording still resolves to its intent. This is the deterministic no-LLM
// hook that lets a replay demo drive the orchestrator's clarify branch (and an
// opt-in agent off-ramp).
func TestReplayHarness_ClarifyEntry(t *testing.T) {
	tmp := t.TempDir()
	rec := filepath.Join(tmp, "recording.yaml")
	require.NoError(t, os.WriteFile(rec, []byte(`kind: recording
entries:
  - state: "lobby"
    input: "tell me a joke"
    clarify: true
    message: "I route rooms, I don't free-associate."
  - state: "lobby"
    input: "look"
    intent:
      name: "look"
`), 0o644))

	h, err := harness.NewReplay(rec)
	require.NoError(t, err)
	ctx := context.Background()

	// Clarify entry → *ClarifyResponse with the recorded message.
	_, err = h.RunTurn(ctx, makeTurnInput("lobby", "tell me a joke"))
	require.Error(t, err)
	var clarify *harness.ClarifyResponse
	require.ErrorAs(t, err, &clarify)
	assert.Equal(t, "I route rooms, I don't free-associate.", clarify.Message)

	// Intent entry in the same recording still resolves to its intent.
	params, err := h.RunTurn(ctx, makeTurnInput("lobby", "look"))
	require.NoError(t, err)
	intent, _ := extractIntent(t, params)
	assert.Equal(t, "look", intent)
}

// TestReplayHarness_ClarifyWithIntentRejected asserts that an entry which sets
// both clarify:true and an intent name fails fast at load — the two kinds are
// mutually exclusive.
func TestReplayHarness_ClarifyWithIntentRejected(t *testing.T) {
	tmp := t.TempDir()
	rec := filepath.Join(tmp, "recording.yaml")
	require.NoError(t, os.WriteFile(rec, []byte(`kind: recording
entries:
  - state: "lobby"
    input: "tell me a joke"
    clarify: true
    intent:
      name: "look"
`), 0o644))

	_, err := harness.NewReplay(rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "clarify")
}

// TestReplayHarness_ParaphrasePool asserts the tier-2.5 paraphrase-pool
// contract: every string in a pre-recorded `paraphrases:` list resolves
// identically to the entry's own `input:` (exact match, plus the existing
// case-insensitive+trimmed fallback), while an utterance never recorded in
// the pool still misses — the pool is exhaustive, not a semantic/embedding
// matcher, so it never silently matches something the recording never pinned.
func TestReplayHarness_ParaphrasePool(t *testing.T) {
	tmp := t.TempDir()
	rec := filepath.Join(tmp, "recording.yaml")
	require.NoError(t, os.WriteFile(rec, []byte(`kind: recording
entries:
  - state: "foyer"
    input: "go south"
    paraphrases:
      - "head south"
      - "walk south please"
      - "I'd like to go toward the south exit"
    intent:
      name: "go"
      slots:
        direction: "south"
    confidence: 0.9
`), 0o644))

	h, err := harness.NewReplay(rec)
	require.NoError(t, err)
	ctx := context.Background()

	// Every pool member (and the original input) resolves to the same intent.
	for _, in := range []string{
		"go south",
		"head south",
		"walk south please",
		"I'd like to go toward the south exit",
		// case-insensitive/trimmed fallback still applies to pool members.
		"  HEAD SOUTH  ",
	} {
		t.Run(in, func(t *testing.T) {
			params, err := h.RunTurn(ctx, makeTurnInput("foyer", in))
			require.NoError(t, err)
			intent, slots := extractIntent(t, params)
			assert.Equal(t, "go", intent)
			assert.EqualValues(t, "south", slots["direction"])
		})
	}

	// An utterance that was never pinned into the pool must NOT silently
	// match — it's a recording miss like any unrecorded free-text input.
	_, err = h.RunTurn(ctx, makeTurnInput("foyer", "please proceed southward"))
	require.Error(t, err)
	var miss *harness.ErrRecordingMiss
	require.ErrorAs(t, err, &miss)
	assert.Equal(t, "please proceed southward", miss.Input)
}

// TestReplayHarness_ParaphraseEmptyRejected asserts that a blank paraphrase
// pool member fails fast at load, same discipline as an empty `input:`.
func TestReplayHarness_ParaphraseEmptyRejected(t *testing.T) {
	tmp := t.TempDir()
	rec := filepath.Join(tmp, "recording.yaml")
	require.NoError(t, os.WriteFile(rec, []byte(`kind: recording
entries:
  - state: "foyer"
    input: "go south"
    paraphrases:
      - "head south"
      - "   "
    intent:
      name: "go"
`), 0o644))

	_, err := harness.NewReplay(rec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "paraphrase")
}

// TestReplayHarness_MalformedRecording asserts that a missing-kind recording file fails fast.
func TestReplayHarness_MalformedRecording(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("kind: not-recording\nentries: []\n"), 0o644))
	_, err := harness.NewReplay(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected kind")
}

// TestReplayHarness_NonexistentFile checks that a missing recording file gives a clear error.
func TestReplayHarness_NonexistentFile(t *testing.T) {
	_, err := harness.NewReplay("/no/such/recording.yaml")
	require.Error(t, err)
}

// ─── RecordingHarness tests ──────────────────────────────────────────────────

// fakeHarness is a stub that always returns a fixed CallToolParams.
type fakeHarness struct {
	params mcp.CallToolParams
	err    error
}

func (f *fakeHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	return f.params, f.err
}

func (f *fakeHarness) Close() error { return nil }

func TestRecordingHarness_WritesJSONL(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "recording.jsonl")

	inner := &fakeHarness{
		params: mcp.CallToolParams{
			Name: "transition",
			Arguments: map[string]any{
				"intent": "go",
				"slots":  map[string]any{"direction": "south"},
			},
		},
	}

	rh, err := harness.NewRecording(inner, outPath)
	require.NoError(t, err)

	ctx := context.Background()
	input := harness.TurnInput{
		StatePath: "foyer",
		UserText:  "go south",
		World:     world.New(),
	}

	params, err := rh.RunTurn(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, "transition", params.Name)

	require.NoError(t, rh.Close())

	// Read and parse the JSONL file.
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 1, "expected exactly one JSONL record")

	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &rec))

	assert.Equal(t, "foyer", rec["state"])
	assert.Equal(t, "go south", rec["input"])
	assert.Equal(t, "go", rec["intent"])
	assert.NotNil(t, rec["ts"])
	ts, ok := rec["ts"].(float64)
	assert.True(t, ok)
	assert.Greater(t, ts, float64(0))
}

// TestRecordingHarness_InnerError checks that errors from the inner harness are
// propagated and nothing is written to the JSONL file.
func TestRecordingHarness_InnerError(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "recording.jsonl")

	inner := &fakeHarness{
		err: &ErrTest{Msg: "inner harness error"},
	}

	rh, err := harness.NewRecording(inner, outPath)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = rh.RunTurn(ctx, makeTurnInput("foyer", "go south"))
	require.Error(t, err)

	require.NoError(t, rh.Close())

	// File should be empty (no record written on error).
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(data)))
}

// TestRecordingHarness_MultipleWrites checks multiple JSONL records.
func TestRecordingHarness_MultipleWrites(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "recording.jsonl")

	inner := &fakeHarness{
		params: mcp.CallToolParams{
			Name:      "transition",
			Arguments: map[string]any{"intent": "look"},
		},
	}

	rh, err := harness.NewRecording(inner, outPath)
	require.NoError(t, err)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_, err := rh.RunTurn(ctx, makeTurnInput("foyer", "look"))
		require.NoError(t, err)
	}
	require.NoError(t, rh.Close())

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Len(t, lines, 3)
}

// ─── LiveHarness pure-function tests ─────────────────────────────────────────
// We do NOT call the real API. Instead, we test the prompt-building logic
// and the response-parsing logic in isolation.

// TestBuildStablePrefix checks that the stable prefix contains required sections.
func TestBuildStablePrefix(t *testing.T) {
	appDef := &app.AppDef{
		App: app.AppMeta{ID: "test-app", Title: "Test Application"},
		Intents: map[string]app.Intent{
			"go": {
				Title:       "Go",
				Description: "Move in a direction.",
				Examples:    []string{"go north", "n"},
				Slots: map[string]app.Slot{
					"direction": {Type: "enum", Required: true, Values: []string{"north", "south"}},
				},
			},
		},
	}

	prefix := harness.BuildStablePrefixForTest(appDef)
	assert.Contains(t, prefix, "Test Application")
	assert.Contains(t, prefix, "test-app")
	assert.Contains(t, prefix, "go")
	assert.Contains(t, prefix, "direction")
	assert.Contains(t, prefix, "transition")
}

// TestParseTransitionArgs exercises the argument extraction helper.
func TestParseTransitionArgs(t *testing.T) {
	p := mcp.CallToolParams{
		Name: "transition",
		Arguments: map[string]any{
			"intent":     "go",
			"slots":      map[string]any{"direction": "south"},
			"confidence": 0.95,
		},
	}

	intent, slots, conf := harness.ParseTransitionArgsForTest(p)
	assert.Equal(t, "go", intent)
	assert.Equal(t, map[string]any{"direction": "south"}, slots)
	assert.InDelta(t, 0.95, conf, 0.001)
}

// ErrTest is a simple test error type.
type ErrTest struct {
	Msg string
}

func (e *ErrTest) Error() string { return e.Msg }
