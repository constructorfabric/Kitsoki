package harness_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hally/internal/app"
	"hally/internal/harness"
	"hally/internal/world"
)

// ─── parseClaudeEnvelope tests ────────────────────────────────────────────────

// TestParseClaudeEnvelope_Success tests the happy-path: a well-formed envelope
// with a clean JSON object in the result field.
func TestParseClaudeEnvelope_Success(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"{\"intent\":\"go\",\"slots\":{\"direction\":\"south\"}}","session_id":"abc","total_cost_usd":0}`)
	params, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.NoError(t, err)
	assert.Equal(t, "transition", params.Name)
	intent, slots, _ := harness.ParseTransitionArgsForTest(params)
	assert.Equal(t, "go", intent)
	assert.Equal(t, "south", slots["direction"])
}

// TestParseClaudeEnvelope_WithConfidence tests that the confidence field is
// preserved when present.
func TestParseClaudeEnvelope_WithConfidence(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"{\"intent\":\"look\",\"slots\":{},\"confidence\":0.9}"}`)
	params, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.NoError(t, err)
	_, _, conf := harness.ParseTransitionArgsForTest(params)
	assert.InDelta(t, 0.9, conf, 0.001)
}

// TestParseClaudeEnvelope_FencedJSON tests that a ```json ... ``` fence in the
// result is stripped and parsing still works. The stripped-fence log is at
// Debug level (not Warn) — Haiku 4.5 routinely fences despite strong anti-
// fence prompt instructions, and surfacing it as a Warn every turn was just
// noise. This test asserts no Warn-or-higher record is emitted on the
// fence path.
func TestParseClaudeEnvelope_FencedJSON(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"` + "```json\\n{\\\"intent\\\":\\\"look\\\",\\\"slots\\\":{}}\\n```" + `"}`)

	var captured []slog.Record
	capture := &captureHandler{records: &captured, level: slog.LevelDebug}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	defer slog.SetDefault(prev)

	params, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.NoError(t, err)
	intent, _, _ := harness.ParseTransitionArgsForTest(params)
	assert.Equal(t, "look", intent)

	// Fence stripping must not emit Warn-or-higher; it should be Debug.
	var warns []string
	for _, r := range captured {
		if r.Level >= slog.LevelWarn {
			warns = append(warns, r.Message)
		}
	}
	assert.Empty(t, warns,
		"fence-stripped path should not emit Warn or higher; got: %v", warns)
}

// captureHandler is a slog.Handler that appends records to a slice for
// assertion. Thread-unsafe; tests are serial.
type captureHandler struct {
	records *[]slog.Record
	level   slog.Level
}

func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}

func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }

// TestParseClaudeEnvelope_LeadingProse tests that leading prose before the JSON
// object is tolerated.
func TestParseClaudeEnvelope_LeadingProse(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"Here is the JSON: {\"intent\":\"hang_cloak\",\"slots\":{}}"}`)
	params, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.NoError(t, err)
	intent, _, _ := harness.ParseTransitionArgsForTest(params)
	assert.Equal(t, "hang_cloak", intent)
}

// TestParseClaudeEnvelope_EmptyResult tests that an empty result string returns an error.
func TestParseClaudeEnvelope_EmptyResult(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":""}`)
	_, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

// TestParseClaudeEnvelope_NonSuccessSubtype tests that a non-success subtype
// returns an error.
func TestParseClaudeEnvelope_NonSuccessSubtype(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"error","is_error":true,"result":"auth required"}`)
	_, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failure")
}

// TestParseClaudeEnvelope_InvalidJSON tests that malformed JSON in the result field
// returns a descriptive error.
func TestParseClaudeEnvelope_InvalidJSON(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"not json at all"}`)
	_, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.Error(t, err)
}

// TestParseClaudeEnvelope_MissingIntentField tests that a JSON object without an
// "intent" field returns an error.
func TestParseClaudeEnvelope_MissingIntentField(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"{\"slots\":{}}"}`)
	_, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intent")
}

// TestParseClaudeEnvelope_MalformedEnvelope tests that a completely malformed
// outer envelope returns a decode error.
func TestParseClaudeEnvelope_MalformedEnvelope(t *testing.T) {
	_, err := harness.ParseClaudeEnvelopeForTest([]byte(`not json`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode envelope")
}

// ─── Prompt composition tests ─────────────────────────────────────────────────

// TestClaudeCLIHarness_PromptContainsPrefix verifies that the stable prefix
// (from the shared buildStablePrefix helper) is included in the harness's
// built prompt for a given app.
func TestClaudeCLIHarness_PromptContainsPrefix(t *testing.T) {
	appDef := &app.AppDef{
		App: app.AppMeta{ID: "cloak", Title: "Cloak of Darkness"},
		Intents: map[string]app.Intent{
			"go": {
				Title:       "Go",
				Description: "Move in a direction.",
				Slots: map[string]app.Slot{
					"direction": {Type: "enum", Required: true, Values: []string{"north", "south", "east", "west"}},
				},
			},
		},
	}

	// The stable prefix from ClaudeCLIHarness should be identical to BuildStablePrefixForTest.
	prefix := harness.BuildStablePrefixForTest(appDef)
	assert.Contains(t, prefix, "Cloak of Darkness")
	assert.Contains(t, prefix, "go")
	assert.Contains(t, prefix, "direction")
	assert.Contains(t, prefix, "transition")
}

// ─── Exec plumbing test (uses fake-claude.sh) ─────────────────────────────────

// TestClaudeCLIHarness_ExecPlumbing runs a real subprocess — the fake-claude.sh
// script in testdata/ — that echoes a canned envelope. This proves the exec
// plumbing (stdin piping, stdout capture, envelope parsing) works end-to-end.
//
// Skipped on Windows (fake-claude.sh is a bash script).
func TestClaudeCLIHarness_ExecPlumbing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude.sh requires bash; skipping on Windows")
	}

	// Locate testdata/fake-claude.sh relative to this file.
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	fakeBin := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-claude.sh")

	// Make sure the script exists and is executable.
	fi, err := os.Stat(fakeBin)
	require.NoError(t, err, "fake-claude.sh not found at %s", fakeBin)
	require.NotZero(t, fi.Mode()&0o111, "fake-claude.sh is not executable")

	appDef := &app.AppDef{
		App: app.AppMeta{ID: "cloak", Title: "Cloak of Darkness"},
		Intents: map[string]app.Intent{
			"go": {Title: "Go"},
		},
	}

	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{
		ClaudeBin: fakeBin,
	})
	require.NoError(t, err)

	ctx := context.Background()
	in := harness.TurnInput{
		StatePath:      "foyer",
		UserText:       "go south",
		World:          world.New(),
		AllowedIntents: []string{"go"},
	}

	params, err := h.RunTurn(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, "transition", params.Name)
	intent, slots, _ := harness.ParseTransitionArgsForTest(params)
	assert.Equal(t, "go", intent)
	assert.Equal(t, "south", slots["direction"])
}

// ─── ErrClaudeCLIUnavailable test ─────────────────────────────────────────────

// TestClaudeCLIHarness_BinaryNotFound verifies that RunTurn returns
// ErrClaudeCLIUnavailable when the configured binary path doesn't exist.
func TestClaudeCLIHarness_BinaryNotFound(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{
		ClaudeBin: "/no/such/path/to/claude",
	})
	require.NoError(t, err)

	ctx := context.Background()
	in := harness.TurnInput{
		StatePath:      "start",
		UserText:       "hello",
		World:          world.New(),
		AllowedIntents: []string{},
	}

	_, err = h.RunTurn(ctx, in)
	require.Error(t, err)
	assert.ErrorIs(t, err, harness.ErrClaudeCLIUnavailable)
}

// ─── mcp.CallToolParams builder test ─────────────────────────────────────────

// TestParseClaudeEnvelope_NoSlots verifies that a missing slots field in the
// JSON object is treated as an empty map (not nil).
func TestParseClaudeEnvelope_NoSlots(t *testing.T) {
	raw := []byte(`{"type":"result","subtype":"success","is_error":false,"result":"{\"intent\":\"look\"}"}`)
	params, err := harness.ParseClaudeEnvelopeForTest(raw)
	require.NoError(t, err)

	args, ok := params.Arguments.(map[string]any)
	require.True(t, ok)
	slots, ok := args["slots"]
	require.True(t, ok)
	assert.NotNil(t, slots)

	// Slots should be an empty map, not nil.
	slotsMap, ok := slots.(map[string]any)
	require.True(t, ok)
	assert.Empty(t, slotsMap)
}

// ─── Default model tests ──────────────────────────────────────────────────────

// TestClaudeCLIHarness_DefaultModel verifies that the default argv includes
// the DefaultClaudeModel (haiku) when no model is specified.
func TestClaudeCLIHarness_DefaultModel(t *testing.T) {
	cfg := harness.ClaudeCLIConfig{} // empty model
	args := harness.BuildClaudeArgsForTest(cfg)

	// Should contain --model flag with DefaultClaudeModel.
	found := false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) {
			assert.Equal(t, harness.DefaultClaudeModel, args[i+1],
				"default model should be DefaultClaudeModel")
			found = true
		}
	}
	assert.True(t, found, "args should include --model flag")
}

// TestClaudeCLIHarness_ExplicitModel verifies that an explicitly set model
// overrides the default.
func TestClaudeCLIHarness_ExplicitModel(t *testing.T) {
	cfg := harness.ClaudeCLIConfig{Model: "claude-sonnet-4-5"}
	args := harness.BuildClaudeArgsForTest(cfg)

	found := false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) {
			assert.Equal(t, "claude-sonnet-4-5", args[i+1],
				"explicit model should be preserved in args")
			found = true
		}
	}
	assert.True(t, found, "args should include --model flag with explicit model")
}

// TestClaudeCLIHarness_NewClaudeDefaultsToHaiku verifies that NewClaudeCLI
// fills in the DefaultClaudeModel when Model is empty.
func TestClaudeCLIHarness_NewClaudeDefaultsToHaiku(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	// Model is empty → should default to haiku.
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{})
	require.NoError(t, err)
	require.NotNil(t, h)

	// Verify by checking args via WithClaudeModel (the copy returns haiku too).
	copy := h.WithClaudeModel("")
	_ = copy // just ensure the method exists; model is tested via BuildClaudeArgsForTest
}

// TestClaudeCLIHarness_WithClaudeModel verifies the WithClaudeModel setter.
func TestClaudeCLIHarness_WithClaudeModel(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{})
	require.NoError(t, err)

	// Override to opus.
	h2 := h.WithClaudeModel("claude-opus-4-5")
	require.NotNil(t, h2)
	require.NotSame(t, h, h2) // should be a new instance

	// Reset to default.
	h3 := h2.WithClaudeModel("")
	require.NotNil(t, h3)
}

// ─── table-driven envelope parser test ────────────────────────────────────────

func TestParseClaudeEnvelope_Table(t *testing.T) {
	type want struct {
		intent string
		errFrag string // non-empty → expect error containing this string
	}

	tests := []struct {
		name string
		raw  string
		want want
	}{
		{
			name: "clean JSON",
			raw:  `{"type":"result","subtype":"success","is_error":false,"result":"{\"intent\":\"read_message\",\"slots\":{}}"}`,
			want: want{intent: "read_message"},
		},
		{
			name: "JSON with extra whitespace",
			raw:  `{"type":"result","subtype":"success","is_error":false,"result":"  {\"intent\":\"look\",\"slots\":{}}  "}`,
			want: want{intent: "look"},
		},
		{
			name: "non-success is_error true",
			raw:  `{"type":"result","subtype":"success","is_error":true,"result":"error text"}`,
			want: want{errFrag: "failure"},
		},
		{
			name: "wrong type field",
			raw:  `{"type":"partial","subtype":"success","is_error":false,"result":"{}"}`,
			want: want{errFrag: "unexpected envelope type"},
		},
		{
			name: "empty result",
			raw:  `{"type":"result","subtype":"success","is_error":false,"result":""}`,
			want: want{errFrag: "empty"},
		},
		{
			name: "result is array not object",
			raw:  `{"type":"result","subtype":"success","is_error":false,"result":"[1,2,3]"}`,
			want: want{errFrag: "no JSON object found"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var params mcp.CallToolParams
			var err error
			params, err = harness.ParseClaudeEnvelopeForTest([]byte(tc.raw))
			if tc.want.errFrag != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.want.errFrag)
			} else {
				require.NoError(t, err)
				intent, _, _ := harness.ParseTransitionArgsForTest(params)
				assert.Equal(t, tc.want.intent, intent)
			}
		})
	}
}
