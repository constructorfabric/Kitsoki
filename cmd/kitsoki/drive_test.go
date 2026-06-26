package main

// drive_test.go — `kitsoki drive` headless driver: deterministic replay of a
// known cassette reproduces a known transcript, free text routes through the
// real orchestrator turn loop (no LLM), and a recorded run round-trips back
// through --record none. The no-live-fallthrough teeth assert that replay+none
// never touches an injected failing live harness.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/harness"
)

// failingLive is a live harness that fails loudly if RunTurn is ever called.
// Wired behind a replay cassette in the no-live-fallthrough test: a miss under
// --record none must error WITHOUT calling it.
type failingLive struct{ calls int }

func (f *failingLive) RunTurn(_ context.Context, _ harness.TurnInput) (mcp.CallToolParams, error) {
	f.calls++
	return mcp.CallToolParams{}, errors.New("drive: live harness MUST NOT be called under --record none")
}
func (f *failingLive) Close() error { return nil }

// scriptLive is a deterministic stub live harness for the round-trip test: it
// maps (state, input) to a fixed intent so a --record new run can grow a
// cassette with NO real LLM, which a later --record none run replays.
type scriptLive struct {
	calls int
	reply map[string]string // input -> intent name
}

func (s *scriptLive) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	s.calls++
	intent, ok := s.reply[in.UserText]
	if !ok {
		return mcp.CallToolParams{}, &harness.ErrRecordingMiss{State: string(in.StatePath), Input: in.UserText}
	}
	args := map[string]any{"intent": intent, "confidence": 0.5}
	if intent == "go" {
		args["slots"] = map[string]any{"direction": "west"}
	}
	return mcp.CallToolParams{Name: "transition", Arguments: args}, nil
}
func (s *scriptLive) Close() error { return nil }

func newDriveCmd(t *testing.T, stdin string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(stdin))
	return cmd, out
}

// decodeDriveFrames parses the per-turn JSONL stdout into driveFrame values.
func decodeDriveFrames(t *testing.T, raw string) []driveFrame {
	t.Helper()
	var frames []driveFrame
	dec := json.NewDecoder(strings.NewReader(raw))
	for dec.More() {
		var df driveFrame
		require.NoError(t, dec.Decode(&df))
		frames = append(frames, df)
	}
	return frames
}

const cloakAppPath = "../../testdata/apps/cloak/app.yaml"
const cloakCassettePath = "../../testdata/apps/cloak/recording.yaml"

// TestDrive_ReplayNoneReproducesKnownTranscript (task 2.1 + 2.4): driving the
// cloak app under --harness replay --record none with recorded free-text inputs
// reproduces a known transcript — routed intents, exits, and resting states —
// with NO live harness.
func TestDrive_ReplayNoneReproducesKnownTranscript(t *testing.T) {
	script := "go west\nhang the cloak\ngo east\ngo south\n"
	cmd, out := newDriveCmd(t, script)
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	err := runDrive(cmd, driveCmdConfig{
		appPath:     cloakAppPath,
		tracePath:   tracePath,
		harnessType: "replay",
		cassette:    cloakCassettePath,
		recordMode:  "none",
		cols:        100,
		rows:        30,
	})
	require.NoError(t, err)

	frames := decodeDriveFrames(t, out.String())
	require.Len(t, frames, 4, "one JSON frame per input line")

	// The known transcript: free text routes through the real orchestrator.
	require.Equal(t, "go", frames[0].RoutedIntent)
	require.Equal(t, driveExitAccepted, frames[0].Exit)
	require.Equal(t, "cloakroom", frames[0].Frame.Metadata.State, "go west → cloakroom")

	require.Equal(t, "hang_cloak", frames[1].RoutedIntent)
	require.Equal(t, "cloakroom", frames[1].Frame.Metadata.State)

	require.Equal(t, "go", frames[2].RoutedIntent)
	require.Equal(t, "foyer", frames[2].Frame.Metadata.State, "go east → foyer")

	require.Equal(t, "go", frames[3].RoutedIntent)
	require.Equal(t, "bar.lit", frames[3].Frame.Metadata.State, "cloak hung → bar is lit")

	// The Frame is the slice-1 screen: Text is the ansi-stripped twin of ANSI,
	// and it carries the room body the human sees.
	for i, f := range frames {
		require.NotEmpty(t, f.Frame.Text, "frame %d must carry the rendered screen", i)
		require.Equal(t, 100, f.Frame.Width)
		require.Equal(t, 1.0, f.Confidence, "cloak cassette confidence is 1.0")
	}
}

// TestDrive_ReplayNoneNoLiveFallthrough (task 2.2): the teeth. Under
// --record none a cassette miss errors and the INJECTED failing live harness is
// never called.
func TestDrive_ReplayNoneNoLiveFallthrough(t *testing.T) {
	// "look" has no cloakroom/foyer-state entry that matches this exact input
	// under bar.dark; drive from the start with an utterance guaranteed to miss.
	cmd, out := newDriveCmd(t, "an utterance that is nowhere in the cassette\n")
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	fail := &failingLive{}
	// Wrap the cassette in a none-mode VCR with the failing live behind it.
	vcr, err := harness.NewVCR(harness.VCRModeNone, cloakCassettePath, fail)
	require.NoError(t, err)

	err = runDrive(cmd, driveCmdConfig{
		appPath:         cloakAppPath,
		tracePath:       tracePath,
		harnessType:     "replay",
		cassette:        cloakCassettePath,
		recordMode:      "none",
		cols:            100,
		rows:            30,
		harnessOverride: vcr,
	})
	// The turn itself errors (recording miss bubbles up), but drive surfaces it
	// as an `exit: error` frame, not a fatal command error — the loop continues
	// to the next line. With a single miss line, the command returns nil.
	require.NoError(t, err)

	frames := decodeDriveFrames(t, out.String())
	require.Len(t, frames, 1)
	require.Equal(t, driveExitError, frames[0].Exit)
	require.Equal(t, 0, fail.calls, "replay+none miss must NEVER call the live harness")
}

// TestDrive_RoundTripRecordThenReplay (task 2.3): a --record new run backed by a
// deterministic stub live harness grows a cassette; replaying that cassette
// under --record none reproduces identical frames with NO live calls.
func TestDrive_RoundTripRecordThenReplay(t *testing.T) {
	dir := t.TempDir()
	cassette := filepath.Join(dir, "grown.yaml")

	// Phase 1: record. Stub live maps the two inputs to intents.
	stub := &scriptLive{reply: map[string]string{
		"go west":        "go",
		"hang the cloak": "hang_cloak",
	}}
	vcr, err := harness.NewVCR(harness.VCRModeNew, cassette, stub)
	require.NoError(t, err)

	recCmd, recOut := newDriveCmd(t, "go west\nhang the cloak\n")
	err = runDrive(recCmd, driveCmdConfig{
		appPath:         cloakAppPath,
		tracePath:       filepath.Join(dir, "rec.jsonl"),
		cassette:        cassette,
		recordMode:      "new",
		cols:            100,
		rows:            30,
		harnessOverride: vcr,
	})
	require.NoError(t, err)
	// At least one input reached the live harness (some free text may route via
	// the orchestrator's deterministic/semantic tiers before the harness — that
	// is correct behavior, not a failure). The point of the round-trip is that
	// whatever DID reach live was recorded.
	require.GreaterOrEqual(t, stub.calls, 1, "record phase must call the stub live for a novel input")
	recFrames := decodeDriveFrames(t, recOut.String())
	require.Len(t, recFrames, 2)

	// Phase 2: replay the grown cassette under --record none with a FAILING live
	// harness — identical frames, zero live calls.
	fail := &failingLive{}
	vcr2, err := harness.NewVCR(harness.VCRModeNone, cassette, fail)
	require.NoError(t, err)

	repCmd, repOut := newDriveCmd(t, "go west\nhang the cloak\n")
	err = runDrive(repCmd, driveCmdConfig{
		appPath:         cloakAppPath,
		tracePath:       filepath.Join(dir, "rep.jsonl"),
		cassette:        cassette,
		recordMode:      "none",
		cols:            100,
		rows:            30,
		harnessOverride: vcr2,
	})
	require.NoError(t, err)
	require.Equal(t, 0, fail.calls, "replay phase must make zero live calls")
	repFrames := decodeDriveFrames(t, repOut.String())
	require.Len(t, repFrames, 2)

	// The frames are identical between the recorded and replayed runs: same
	// routed intent, same resting state, same rendered screen.
	for i := range recFrames {
		require.Equal(t, recFrames[i].RoutedIntent, repFrames[i].RoutedIntent, "frame %d intent", i)
		require.Equal(t, recFrames[i].Frame.Metadata.State, repFrames[i].Frame.Metadata.State, "frame %d state", i)
		require.Equal(t, recFrames[i].Frame.Text, repFrames[i].Frame.Text, "frame %d screen must be identical", i)
	}
}

// TestDrive_StdinFreeTextRouting (task 2.4): free text submitted on stdin routes
// through orch.Turn (the routing path, not SubmitDirect) against the cloak
// cassette with no live harness.
func TestDrive_StdinFreeTextRouting(t *testing.T) {
	// "head south" is a free-text synonym the cassette maps to go(south).
	cmd, out := newDriveCmd(t, "head south\n")
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	err := runDrive(cmd, driveCmdConfig{
		appPath:     cloakAppPath,
		tracePath:   tracePath,
		harnessType: "replay",
		cassette:    cloakCassettePath,
		recordMode:  "none",
		cols:        80,
		rows:        24,
	})
	require.NoError(t, err)

	frames := decodeDriveFrames(t, out.String())
	require.Len(t, frames, 1)
	require.Equal(t, "go", frames[0].RoutedIntent, "free text 'head south' must route to go via the cassette")
	require.Equal(t, driveExitAccepted, frames[0].Exit)
}

// TestDrive_ReplayRequiresCassette guards the misconfiguration: --harness replay
// with no cassette is a clear error, not a silent live fall-through.
func TestDrive_ReplayRequiresCassette(t *testing.T) {
	cmd, _ := newDriveCmd(t, "go west\n")
	err := runDrive(cmd, driveCmdConfig{
		appPath:     cloakAppPath,
		tracePath:   filepath.Join(t.TempDir(), "trace.jsonl"),
		harnessType: "replay",
		recordMode:  "none",
		cols:        100,
		rows:        30,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires --cassette")
}

func TestDriveResolveProfilesUsesLocalConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
  synthetic-claude:
    backend: claude
    model: hf:zai-org/GLM-5.2
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: test-token
    quota:
      window: 1m
      tokens_per_window: 120000
      max_concurrent: 1
`), 0o644))

	profiles, defaultProfile, active, err := resolveDriveProfiles(driveCmdConfig{
		profileName: "synthetic-claude",
		configPath:  configPath,
	})
	require.NoError(t, err)
	require.Equal(t, "claude-native", defaultProfile)
	require.Contains(t, profiles, "synthetic-claude")
	require.NotNil(t, active)
	require.Equal(t, "hf:zai-org/GLM-5.2", active.Model)
	require.Equal(t, "https://api.synthetic.new/anthropic", active.Env["ANTHROPIC_BASE_URL"])
	require.Equal(t, int64(120000), active.Quota.TokensPerWindow)
}

func TestDriveResolveProfilesRejectsUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".kitsoki.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
harness_profiles:
  claude-native:
    backend: claude
`), 0o644))

	_, _, _, err := resolveDriveProfiles(driveCmdConfig{
		profileName: "missing",
		configPath:  configPath,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown --profile "missing"`)
}
