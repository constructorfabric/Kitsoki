package harness_test

// vcr_test.go — the VCR record/playback policy teeth: replay on hit, the four
// miss policies (none|once|new|all), the no-live-fallthrough guarantee, and the
// unified recording.yaml round-trip (a recorded run replays under --record none
// with no live calls).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
)

// countingLive is a stub live harness: it records how many times RunTurn was
// called and returns a fixed intent. A test that wires it behind a replay
// cassette asserts CallCount stays 0 to prove no live fall-through.
type countingLive struct {
	calls  int
	intent string
	slots  map[string]any
	conf   float64
	err    error
}

func (c *countingLive) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	c.calls++
	if c.err != nil {
		return mcp.CallToolParams{}, c.err
	}
	args := map[string]any{"intent": c.intent}
	if c.slots != nil {
		args["slots"] = c.slots
	}
	if c.conf != 0 {
		args["confidence"] = c.conf
	}
	return mcp.CallToolParams{Name: "transition", Arguments: args}, nil
}

func (c *countingLive) Close() error { return nil }

// writeCassette writes a minimal recording.yaml with a single (state,input)→intent
// entry and returns its path.
func writeCassette(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "recording.yaml")
	content := `kind: recording
app_id: cloak-of-darkness
entries:
  - state: foyer
    input: "go south"
    intent: { name: go, slots: { direction: south } }
    confidence: 0.91
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestParseVCRMode(t *testing.T) {
	cases := map[string]harness.VCRMode{
		"":     harness.VCRModeNone,
		"none": harness.VCRModeNone,
		"once": harness.VCRModeOnce,
		"new":  harness.VCRModeNew,
		"all":  harness.VCRModeAll,
	}
	for in, want := range cases {
		got, err := harness.ParseVCRMode(in)
		require.NoError(t, err, "mode %q", in)
		require.Equal(t, want, got, "mode %q", in)
	}
	_, err := harness.ParseVCRMode("bogus")
	require.Error(t, err)
}

// TestVCR_ReplayHit replays a known (state,input) pair without ever touching
// the live harness, and reports the recorded confidence.
func TestVCR_ReplayHit(t *testing.T) {
	cassette := writeCassette(t, t.TempDir())
	live := &countingLive{intent: "SHOULD_NOT_BE_CALLED"}

	vcr, err := harness.NewVCR(harness.VCRModeNone, cassette, live)
	require.NoError(t, err)
	defer func() { _ = vcr.Close() }()

	params, err := vcr.RunTurn(context.Background(), makeTurnInput("foyer", "go south"))
	require.NoError(t, err)
	require.Equal(t, "transition", params.Name)
	require.Equal(t, 0, live.calls, "a replay hit must never call the live harness")

	// Confidence surfaced from the cassette entry.
	require.InDelta(t, 0.91, vcr.LastConfidence(), 0.0001)
}

// TestVCR_NoneMode_NoLiveFallthrough is the teeth: under --record none a
// cassette MISS is a hard ErrRecordingMiss and the (failing) live harness is
// NEVER called. This is the CLAUDE.md no-LLM-by-default guarantee.
func TestVCR_NoneMode_NoLiveFallthrough(t *testing.T) {
	cassette := writeCassette(t, t.TempDir())
	// A live harness that fails loudly if invoked — it must stay untouched.
	live := &countingLive{err: errors.New("LIVE HARNESS MUST NOT BE CALLED under --record none")}

	vcr, err := harness.NewVCR(harness.VCRModeNone, cassette, live)
	require.NoError(t, err)
	defer func() { _ = vcr.Close() }()

	_, err = vcr.RunTurn(context.Background(), makeTurnInput("foyer", "this is not in the cassette"))
	require.Error(t, err)
	var miss *harness.ErrRecordingMiss
	require.True(t, errors.As(err, &miss), "a none-mode miss must be ErrRecordingMiss, got %v", err)
	require.Equal(t, 0, live.calls, "none-mode miss must NOT fall through to the live harness")
}

// TestVCR_NewMode_RecordsAndRoundTrips proves the exploratory→regression bridge:
// a --record new run grows the cassette on a miss by calling live, and the
// resulting recording.yaml replays under --record none with no live calls.
func TestVCR_NewMode_RecordsAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	cassette := writeCassette(t, dir)
	live := &countingLive{intent: "look", slots: map[string]any{}, conf: 0.77}

	vcr, err := harness.NewVCR(harness.VCRModeNew, cassette, live)
	require.NoError(t, err)

	// A miss → live + append.
	params, err := vcr.RunTurn(context.Background(), makeTurnInput("foyer", "look around"))
	require.NoError(t, err)
	require.Equal(t, 1, live.calls, "new-mode miss must call live once")
	intentName, _ := extractIntent(t, params)
	require.Equal(t, "look", intentName)
	require.NoError(t, vcr.Close())

	// The cassette now has the novel entry; reopen under --record none with a
	// FAILING live harness and assert the recorded turn replays for free.
	failLive := &countingLive{err: errors.New("must not be called on replay")}
	vcr2, err := harness.NewVCR(harness.VCRModeNone, cassette, failLive)
	require.NoError(t, err)
	defer func() { _ = vcr2.Close() }()

	// The just-recorded entry replays.
	p2, err := vcr2.RunTurn(context.Background(), makeTurnInput("foyer", "look around"))
	require.NoError(t, err)
	intentName2, _ := extractIntent(t, p2)
	require.Equal(t, "look", intentName2)
	// The original hand-authored entry still replays too.
	_, err = vcr2.RunTurn(context.Background(), makeTurnInput("foyer", "go south"))
	require.NoError(t, err)
	require.Equal(t, 0, failLive.calls, "every turn must replay from the grown cassette — no live calls")

	// The grown cassette is a valid recording.yaml that NewReplay loads.
	rp, err := harness.NewReplay(cassette)
	require.NoError(t, err)
	require.NotNil(t, rp)
}

// TestVCR_OnceMode_FreezesAfterFirstRecord checks that once-mode records on a
// miss only while the cassette started empty/new; a non-empty cassette is
// frozen and a miss is an error (never a live call).
func TestVCR_OnceMode_FrozenCassetteErrorsOnMiss(t *testing.T) {
	cassette := writeCassette(t, t.TempDir()) // already non-empty
	live := &countingLive{err: errors.New("frozen once-mode cassette must not call live")}

	vcr, err := harness.NewVCR(harness.VCRModeOnce, cassette, live)
	require.NoError(t, err)
	defer func() { _ = vcr.Close() }()

	_, err = vcr.RunTurn(context.Background(), makeTurnInput("foyer", "novel input"))
	require.Error(t, err)
	var miss *harness.ErrRecordingMiss
	require.True(t, errors.As(err, &miss))
	require.Equal(t, 0, live.calls, "once-mode on a non-empty cassette must not call live")
}

// TestVCR_OnceMode_EmptyCassetteRecords checks once-mode DOES record on a miss
// while the cassette is empty/new, then freezes after the first append.
func TestVCR_OnceMode_EmptyCassetteRecords(t *testing.T) {
	dir := t.TempDir()
	cassette := filepath.Join(dir, "fresh.yaml") // absent → started empty
	live := &countingLive{intent: "go", slots: map[string]any{"direction": "south"}, conf: 1.0}

	vcr, err := harness.NewVCR(harness.VCRModeOnce, cassette, live)
	require.NoError(t, err)
	defer func() { _ = vcr.Close() }()

	// First miss on an empty cassette → records.
	_, err = vcr.RunTurn(context.Background(), makeTurnInput("foyer", "go south"))
	require.NoError(t, err)
	require.Equal(t, 1, live.calls)

	// Cassette now non-empty → frozen. A second NOVEL miss errors, no live call.
	_, err = vcr.RunTurn(context.Background(), makeTurnInput("foyer", "another novel input"))
	require.Error(t, err)
	require.Equal(t, 1, live.calls, "once-mode must freeze after the first record")
}

// TestVCR_AllMode_IgnoresCassetteAndRecords checks all-mode never replays — it
// always calls live and (re)records every turn.
func TestVCR_AllMode_IgnoresCassetteAndRecords(t *testing.T) {
	cassette := writeCassette(t, t.TempDir())
	// "go south" IS in the cassette, but all-mode ignores reads and goes live.
	live := &countingLive{intent: "OVERRIDDEN", slots: map[string]any{}, conf: 0.5}

	vcr, err := harness.NewVCR(harness.VCRModeAll, cassette, live)
	require.NoError(t, err)
	defer func() { _ = vcr.Close() }()

	params, err := vcr.RunTurn(context.Background(), makeTurnInput("foyer", "go south"))
	require.NoError(t, err)
	require.Equal(t, 1, live.calls, "all-mode must call live even for a cassette-present input")
	intentName, _ := extractIntent(t, params)
	require.Equal(t, "OVERRIDDEN", intentName, "all-mode returns the LIVE result, not the cassette's")
}

// TestVCR_NilLiveRejectedForRecordingModes guards the misconfiguration: a
// recording mode with no live harness is a construction error, not a silent
// fall-through.
func TestVCR_NilLiveRejectedForRecordingModes(t *testing.T) {
	cassette := writeCassette(t, t.TempDir())
	for _, mode := range []harness.VCRMode{harness.VCRModeOnce, harness.VCRModeNew, harness.VCRModeAll} {
		_, err := harness.NewVCR(mode, cassette, nil)
		require.Error(t, err, "mode %s with nil live must error", mode)
	}
	// none-mode never records, so nil live is allowed.
	_, err := harness.NewVCR(harness.VCRModeNone, cassette, nil)
	require.NoError(t, err)
}
