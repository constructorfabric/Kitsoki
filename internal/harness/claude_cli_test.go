package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/world"
)

// ─── parseValidatedPayload tests ──────────────────────────────────────────────

func TestParseValidatedPayload_Success(t *testing.T) {
	raw := []byte(`{"intent":"go","slots":{"direction":"south"},"confidence":0.9}`)
	params, err := harness.ParseValidatedPayloadForTest(raw)
	require.NoError(t, err)
	assert.Equal(t, "transition", params.Name)
	intent, slots, conf := harness.ParseTransitionArgsForTest(params)
	assert.Equal(t, "go", intent)
	assert.Equal(t, "south", slots["direction"])
	assert.InDelta(t, 0.9, conf, 0.001)
}

func TestParseValidatedPayload_NoSlots(t *testing.T) {
	raw := []byte(`{"intent":"look"}`)
	params, err := harness.ParseValidatedPayloadForTest(raw)
	require.NoError(t, err)

	args := params.Arguments.(map[string]any)
	slots, ok := args["slots"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, slots)
}

func TestParseValidatedPayload_MissingIntent(t *testing.T) {
	raw := []byte(`{"slots":{}}`)
	_, err := harness.ParseValidatedPayloadForTest(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "intent")
}

func TestParseValidatedPayload_InvalidJSON(t *testing.T) {
	_, err := harness.ParseValidatedPayloadForTest([]byte(`not json`))
	require.Error(t, err)
}

func TestParseValidatedPayload_Table(t *testing.T) {
	type want struct {
		intent  string
		errFrag string
	}
	tests := []struct {
		name string
		raw  string
		want want
	}{
		{name: "clean", raw: `{"intent":"read_message","slots":{}}`, want: want{intent: "read_message"}},
		{name: "with confidence", raw: `{"intent":"hang_cloak","confidence":0.7}`, want: want{intent: "hang_cloak"}},
		{name: "missing intent", raw: `{"slots":{}}`, want: want{errFrag: "intent"}},
		{name: "garbage", raw: `not json`, want: want{errFrag: "decode"}},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var params mcp.CallToolParams
			var err error
			params, err = harness.ParseValidatedPayloadForTest([]byte(tc.raw))
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

// ─── Prompt composition tests ─────────────────────────────────────────────────

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

	prefix := harness.BuildStablePrefixForTest(appDef)
	assert.Contains(t, prefix, "Cloak of Darkness")
	assert.Contains(t, prefix, "go")
	assert.Contains(t, prefix, "direction")
	assert.Contains(t, prefix, "transition")
}

// TestClaudeCLIHarness_SystemPromptOverridesClaudeDefault verifies that a
// non-empty system prompt is passed via --system-prompt (which replaces Claude
// Code's built-in default) together with --exclude-dynamic-system-prompt-sections,
// and that neither flag appears when no system prompt is supplied.
func TestClaudeCLIHarness_SystemPromptOverridesClaudeDefault(t *testing.T) {
	cfg := harness.ClaudeCLIConfig{Model: "claude-haiku-4-5"}

	bare := harness.BuildClaudeArgsForTest(cfg)
	assert.NotContains(t, bare, "--system-prompt")
	assert.NotContains(t, bare, "--append-system-prompt")
	assert.NotContains(t, bare, "--exclude-dynamic-system-prompt-sections")
	assert.NotContains(t, bare, "--no-session-persistence")

	args := harness.BuildClaudeArgsWithSystemPromptForTest(cfg, "ROUTER PROMPT")
	assert.Contains(t, args, "--exclude-dynamic-system-prompt-sections")
	assert.NotContains(t, args, "--append-system-prompt",
		"router prompt must replace, not append to, Claude Code's default")
	assert.NotContains(t, args, "--no-session-persistence")

	idx := -1
	for i, a := range args {
		if a == "--system-prompt" {
			idx = i
			break
		}
	}
	require.GreaterOrEqual(t, idx, 0, "args should include --system-prompt")
	require.Less(t, idx+1, len(args), "--system-prompt must be followed by its value")
	assert.Equal(t, "ROUTER PROMPT", args[idx+1])
}

func TestClaudeCLIHarness_ClaudeSessionColdWarmCopyAndReset(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	appDef := func(id string) *app.AppDef {
		return &app.AppDef{
			App: app.AppMeta{ID: id, Title: "Session Cache Test"},
			Intents: map[string]app.Intent{
				"go": {
					Title: "Go",
					Slots: map[string]app.Slot{
						"direction": {Type: "string"},
					},
				},
			},
		}
	}

	var calls [][]string
	exec := func(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
		calls = append(calls, append([]string(nil), args...))
		if err := writeValidatedTransition(args, "go"); err != nil {
			return "", err
		}
		return `{"type":"result","subtype":"success","result":"ok"}`, nil
	}

	h, err := harness.NewClaudeCLI(appDef("session-cache"), harness.ClaudeCLIConfig{
		ClaudeBin:  exe,
		KitsokiBin: exe,
		Exec:       exec,
	})
	require.NoError(t, err)

	runTurn := func(t *testing.T, h *harness.ClaudeCLIHarness, n int, text string) {
		t.Helper()
		_, err := h.RunTurn(context.Background(), harness.TurnInput{
			SessionID:      app.SessionID("session-cache-test"),
			TurnNumber:     app.TurnNumber(n),
			StatePath:      app.StatePath("main"),
			UserText:       text,
			World:          world.New(),
			AllowedIntents: []string{"go"},
		})
		require.NoError(t, err)
	}

	runTurn(t, h, 1, "go north")
	runTurn(t, h, 2, "go south")

	require.Len(t, calls, 2)
	cold := calls[0]
	warm := calls[1]
	coldID := flagValue(cold, "--session-id")
	require.NotEmpty(t, coldID)
	assert.Empty(t, flagValue(cold, "--resume"))
	assert.Contains(t, cold, "--system-prompt")
	assert.NotContains(t, cold, "--no-session-persistence")
	assert.NotEmpty(t, flagValue(cold, "--mcp-config"))

	assert.Equal(t, coldID, flagValue(warm, "--resume"))
	assert.Empty(t, flagValue(warm, "--session-id"))
	assert.NotContains(t, warm, "--system-prompt")
	assert.NotContains(t, warm, "--exclude-dynamic-system-prompt-sections")
	assert.NotContains(t, warm, "--no-session-persistence")
	assert.NotEmpty(t, flagValue(warm, "--mcp-config"))

	copy := h.WithClaudeModel("claude-opus-4-5")
	runTurn(t, copy, 3, "go east")
	require.Len(t, calls, 3)
	copyCold := calls[2]
	copyID := flagValue(copyCold, "--session-id")
	require.NotEmpty(t, copyID)
	assert.Empty(t, flagValue(copyCold, "--resume"))
	assert.NotEqual(t, coldID, copyID)
	assert.Equal(t, "claude-opus-4-5", flagValue(copyCold, "--model"))
	assert.Contains(t, copyCold, "--system-prompt")

	h.SetAppDef(appDef("session-cache-reset"))
	runTurn(t, h, 4, "go west")
	require.Len(t, calls, 4)
	resetCold := calls[3]
	resetID := flagValue(resetCold, "--session-id")
	require.NotEmpty(t, resetID)
	assert.Empty(t, flagValue(resetCold, "--resume"))
	assert.NotEqual(t, coldID, resetID)
	assert.Contains(t, resetCold, "--system-prompt")
}

// TestClaudeCLIHarness_RoutingComposesKitsokiAndProject verifies the router's
// system prompt is composed through internal/sysprompt: it carries the kitsoki
// Layer-1 grounding and the app's Layer-2 project context (app.context) above
// the routing prefix + output contract (Layer 3).
func TestClaudeCLIHarness_RoutingComposesKitsokiAndProject(t *testing.T) {
	appDef := &app.AppDef{
		App: app.AppMeta{
			ID:      "cloak",
			Title:   "Cloak of Darkness",
			Context: "PROJECT-CONTEXT-ROUTING",
		},
		Intents: map[string]app.Intent{
			"go": {Title: "Go", Description: "Move in a direction."},
		},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{})
	require.NoError(t, err)

	sp := harness.RoutingSystemPromptForTest(h, harness.TurnInput{
		StatePath:      "foyer",
		UserText:       "go south",
		World:          world.New(),
		AllowedIntents: []string{"go"},
	})

	kIdx := strings.Index(sp, "operating inside **kitsoki**")
	pIdx := strings.Index(sp, "PROJECT-CONTEXT-ROUTING")
	// The routing prefix / output contract (Layer 3) lands last.
	tIdx := strings.Index(sp, "Output Contract")
	require.GreaterOrEqual(t, kIdx, 0, "kitsoki Layer 1 must be present in the router prompt")
	require.GreaterOrEqual(t, pIdx, 0, "project Layer 2 (app.context) must be present")
	require.GreaterOrEqual(t, tIdx, 0, "routing Layer 3 (output contract) must be present")
	assert.Less(t, kIdx, pIdx, "kitsoki precedes project")
	assert.Less(t, pIdx, tIdx, "project precedes the routing task layer")
}

// ─── Exec plumbing test (uses fake-claude.sh) ─────────────────────────────────

// TestClaudeCLIHarness_ExecPlumbing runs the fake-claude.sh stub. The stub
// inspects --mcp-config, extracts the validator's --output path, and writes
// a canned validated payload there — exercising the full happy-path
// including config materialization, capture-file read-back, and parse.
func TestClaudeCLIHarness_ExecPlumbing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude.sh requires bash; skipping on Windows")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	fakeBin := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-claude.sh")

	fi, err := os.Stat(fakeBin)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&0o111, "fake-claude.sh is not executable")

	appDef := &app.AppDef{
		App: app.AppMeta{ID: "cloak", Title: "Cloak of Darkness"},
		Intents: map[string]app.Intent{
			"go": {
				Title: "Go",
				Slots: map[string]app.Slot{
					"direction": {Type: "string"},
				},
			},
		},
	}

	// KitsokiBin must point at *something* that exists; the stub doesn't
	// actually spawn it, so we reuse the fake-claude binary to satisfy
	// the existence check.
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{
		ClaudeBin:  fakeBin,
		KitsokiBin: fakeBin,
		Exec:       host.RunClaudeOneShotForHarness,
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

func TestClaudeCLIHarness_RunStructuredUsesCallerSchema(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)

	callerSchema := json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["class","confidence"],
		"properties":{
			"class":{"type":"string","enum":["intent","help","room_request","meta_edit"]},
			"intent":{"type":"string"},
			"confidence":{"type":"number"}
		}
	}`)
	var seenPrompt string
	var seenSchema []byte
	exec := func(_ context.Context, _ string, args []string, stdin, _ string) (string, error) {
		seenPrompt = stdin
		configPath := flagValue(args, "--mcp-config")
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return "", err
		}
		var cfg struct {
			MCPServers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return "", err
		}
		for _, server := range cfg.MCPServers {
			schemaPath := flagValue(server.Args, "--schema")
			outputPath := flagValue(server.Args, "--output")
			if schemaPath == "" || outputPath == "" {
				continue
			}
			seenSchema, err = os.ReadFile(schemaPath)
			if err != nil {
				return "", err
			}
			payload := []byte(`{"class":"intent","intent":"root__bugfix_report","confidence":0.95}`)
			if err := os.WriteFile(outputPath, payload, 0o600); err != nil {
				return "", err
			}
			return `{"type":"result","subtype":"success","result":"ok"}`, nil
		}
		return "", errors.New("validator schema/output args missing")
	}

	h, err := harness.NewClaudeCLI(&app.AppDef{App: app.AppMeta{ID: "structured"}}, harness.ClaudeCLIConfig{
		ClaudeBin:  exe,
		KitsokiBin: exe,
		Exec:       exec,
	})
	require.NoError(t, err)
	payload, err := h.RunStructured(context.Background(), harness.StructuredInput{
		SessionID:  "session",
		TurnNumber: 1,
		StatePath:  "root.landing",
		Prompt:     "classify the operator's bug report",
		SchemaJSON: callerSchema,
	})
	require.NoError(t, err)
	assert.JSONEq(t, string(callerSchema), string(seenSchema),
		"structured execution must attach the caller schema verbatim, not an empty transition enum")
	assert.Equal(t, "classify the operator's bug report", seenPrompt)
	assert.JSONEq(t, `{"class":"intent","intent":"root__bugfix_report","confidence":0.95}`, string(payload))
}

// TestClaudeCLIHarness_NoSubmitError verifies the harness returns a
// meaningful error when the LLM never calls the submit tool (capture
// file absent after exit).
func TestClaudeCLIHarness_NoSubmitError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake-claude-nosubmit.sh requires bash; skipping on Windows")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	fakeBin := filepath.Join(filepath.Dir(thisFile), "testdata", "fake-claude-nosubmit.sh")

	fi, err := os.Stat(fakeBin)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&0o111)

	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "x"},
		Intents: map[string]app.Intent{"look": {}},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{
		ClaudeBin:  fakeBin,
		KitsokiBin: fakeBin,
		Exec:       host.RunClaudeOneShotForHarness,
	})
	require.NoError(t, err)

	in := harness.TurnInput{
		StatePath:      "start",
		UserText:       "huh",
		World:          world.New(),
		AllowedIntents: []string{"look"},
	}
	_, err = h.RunTurn(context.Background(), in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not call")
}

// ─── ErrClaudeCLIUnavailable test ─────────────────────────────────────────────

func TestClaudeCLIHarness_BinaryNotFound(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{
		ClaudeBin: "/no/such/path/to/claude",
	})
	require.NoError(t, err)

	in := harness.TurnInput{
		StatePath:      "start",
		UserText:       "hello",
		World:          world.New(),
		AllowedIntents: []string{},
	}

	_, err = h.RunTurn(context.Background(), in)
	require.Error(t, err)
	assert.ErrorIs(t, err, harness.ErrClaudeCLIUnavailable)
}

// ─── Default model tests ──────────────────────────────────────────────────────

func TestClaudeCLIHarness_DefaultModel(t *testing.T) {
	cfg := harness.ClaudeCLIConfig{}
	args := harness.BuildClaudeArgsForTest(cfg)

	found := false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) {
			assert.Equal(t, harness.DefaultClaudeModel, args[i+1])
			found = true
		}
	}
	assert.True(t, found, "args should include --model flag")
}

func TestClaudeCLIHarness_ExplicitModel(t *testing.T) {
	cfg := harness.ClaudeCLIConfig{Model: "claude-sonnet-4-5"}
	args := harness.BuildClaudeArgsForTest(cfg)

	found := false
	for i, arg := range args {
		if arg == "--model" && i+1 < len(args) {
			assert.Equal(t, "claude-sonnet-4-5", args[i+1])
			found = true
		}
	}
	assert.True(t, found)
}

func TestClaudeCLIHarness_NewClaudeDefaultsToHaiku(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{})
	require.NoError(t, err)
	require.NotNil(t, h)
	copy := h.WithClaudeModel("")
	_ = copy
}

func TestClaudeCLIHarness_WithClaudeModel(t *testing.T) {
	appDef := &app.AppDef{
		App:     app.AppMeta{ID: "test"},
		Intents: map[string]app.Intent{},
	}
	h, err := harness.NewClaudeCLI(appDef, harness.ClaudeCLIConfig{})
	require.NoError(t, err)

	h2 := h.WithClaudeModel("claude-opus-4-5")
	require.NotNil(t, h2)
	require.NotSame(t, h, h2)

	h3 := h2.WithClaudeModel("")
	require.NotNil(t, h3)
}
