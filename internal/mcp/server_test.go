package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/store"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// loadCloakApp loads the Cloak of Darkness test app.
func loadCloakApp(t *testing.T) *app.AppDef {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err, "load cloak app")
	return def
}

// openInMemoryStore opens a test-isolated in-memory store.
func openInMemoryStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// createTestSession creates a session and returns its ID.
func createTestSession(t *testing.T, s store.Store, def *app.AppDef) app.SessionID {
	t.Helper()
	ctx := context.Background()
	sid, err := s.CreateSession(ctx, def)
	require.NoError(t, err)
	return sid
}

// connectInProcess creates an in-process MCP client-server pair using
// the SDK's InMemoryTransports. Returns the client session.
func connectInProcess(ctx context.Context, t *testing.T, srv *kitsokimcp.Server) *mcpsdk.ClientSession {
	t.Helper()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err, "server connect")
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err, "client connect")
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTransition issues a transition tool call and returns the raw result.
func callTransition(ctx context.Context, cs *mcpsdk.ClientSession, args kitsokimcp.TransitionArgs) (*mcpsdk.CallToolResult, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var argsMap map[string]any
	if err := json.Unmarshal(argsJSON, &argsMap); err != nil {
		return nil, err
	}
	return cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "transition",
		Arguments: argsMap,
	})
}

// parseOK unmarshals a successful TransitionOK from the tool result content.
func parseOK(t *testing.T, res *mcpsdk.CallToolResult) kitsokimcp.TransitionOK {
	t.Helper()
	require.False(t, res.IsError, "expected ok result, got error: %v", contentText(res))
	var ok kitsokimcp.TransitionOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	return ok
}

// parseError unmarshals a TransitionError from an error tool result.
func parseError(t *testing.T, res *mcpsdk.CallToolResult) kitsokimcp.TransitionError {
	t.Helper()
	require.True(t, res.IsError, "expected error result, got ok: %v", contentText(res))
	var te kitsokimcp.TransitionError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &te))
	return te
}

// contentText returns the text content of the first content block.
func contentText(res *mcpsdk.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

// ─── tests ───────────────────────────────────────────────────────────────────

// TestTransition_ValidIntent tests a successful transition from the foyer.
func TestTransition_ValidIntent(t *testing.T) {
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	srv := kitsokimcp.NewServer(m, s, def)

	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	sid := createTestSession(t, s, def)

	// go west from foyer to cloakroom.
	res, err := callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: string(sid),
		Intent:    "go",
		Slots:     map[string]any{"direction": "west"},
	})
	require.NoError(t, err)

	ok := parseOK(t, res)
	assert.True(t, ok.OK)
	assert.Equal(t, "cloakroom", ok.State)
	assert.NotEmpty(t, ok.View)
	assert.Contains(t, ok.Menu, "hang_cloak")
}

// TestTransition_MissingRequiredSlot checks that a missing required slot returns
// the correct MISSING_SLOTS error envelope.
func TestTransition_MissingRequiredSlot(t *testing.T) {
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	srv := kitsokimcp.NewServer(m, s, def)

	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	sid := createTestSession(t, s, def)

	// Call `go` without the required `direction` slot.
	res, err := callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: string(sid),
		Intent:    "go",
		Slots:     map[string]any{}, // missing direction
	})
	require.NoError(t, err)

	te := parseError(t, res)
	assert.False(t, te.OK)
	require.NotNil(t, te.Error)
	assert.Equal(t, "MISSING_SLOTS", string(te.Error.Code))
	assert.Contains(t, te.Error.MissingSlots, "direction")
}

// TestTransition_IntentNotAllowed checks that calling an intent not allowed in the
// current state returns INTENT_NOT_ALLOWED_IN_STATE.
func TestTransition_IntentNotAllowed(t *testing.T) {
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	srv := kitsokimcp.NewServer(m, s, def)

	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	sid := createTestSession(t, s, def)

	// `hang_cloak` is only allowed in the cloakroom, not in the foyer.
	res, err := callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: string(sid),
		Intent:    "hang_cloak",
		Slots:     map[string]any{},
	})
	require.NoError(t, err)

	te := parseError(t, res)
	assert.False(t, te.OK)
	require.NotNil(t, te.Error)
	assert.Equal(t, "INTENT_NOT_ALLOWED_IN_STATE", string(te.Error.Code))
	assert.Contains(t, te.Error.AllowedIntents, "go")
}

// TestTransition_MultipleSteps tests that two consecutive transitions are both
// applied correctly (session state persists between calls).
func TestTransition_MultipleSteps(t *testing.T) {
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	srv := kitsokimcp.NewServer(m, s, def)

	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	sid := createTestSession(t, s, def)

	// Step 1: go west → cloakroom.
	res, err := callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: string(sid),
		Intent:    "go",
		Slots:     map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	ok1 := parseOK(t, res)
	assert.Equal(t, "cloakroom", ok1.State)

	// Step 2: hang the cloak.
	res, err = callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: string(sid),
		Intent:    "hang_cloak",
		Slots:     map[string]any{},
	})
	require.NoError(t, err)
	ok2 := parseOK(t, res)
	assert.Equal(t, "cloakroom", ok2.State)
}

// TestTransition_MissingSessionID checks that an empty session_id returns an error.
func TestTransition_MissingSessionID(t *testing.T) {
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	srv := kitsokimcp.NewServer(m, s, def)

	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	res, err := callTransition(ctx, cs, kitsokimcp.TransitionArgs{
		SessionID: "", // missing
		Intent:    "go",
		Slots:     map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	// Should be a structured error, not a protocol error.
	assert.True(t, res.IsError, "expected error for missing session_id")
}
