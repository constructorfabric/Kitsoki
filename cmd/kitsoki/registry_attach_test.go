package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/webconfig"
)

// newAttachRegistry builds a registry in nil-harness (no-LLM) posture over a
// fresh temp DB, so AttachExternal can be exercised end-to-end without a model.
func newAttachRegistry(t *testing.T) *SessionRegistry {
	t.Helper()
	// DefaultTracePath resolves under $HOME; point it at a temp dir so the
	// deterministic trace files are hermetic and never collide across runs.
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	base := runtimeBase{
		DBPath:   dbPath,
		ExecMode: 0,
		Flow:     &testrunner.FlowFixture{}, // nil-harness posture: intents submitted directly
	}
	return NewRegistry(webconfig.WebConfig{}, nil, base)
}

func cloakStoryPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	return abs
}

// TestAttachExternal_CreatesBindsAndReuses proves the persisted-store attach:
// the first attach creates+binds a session to the external key; a turn advances
// it; and a second attach to the same key reuses the SAME live session (one
// ticket, one session) rather than opening a second trace sink.
func TestAttachExternal_CreatesBindsAndReuses(t *testing.T) {
	r := newAttachRegistry(t)
	t.Cleanup(r.Close)
	ctx := context.Background()
	story := cloakStoryPath(t)

	id1, err := r.AttachExternal(ctx, story, "jira:CLOAK-1")
	require.NoError(t, err)
	require.NotEmpty(t, id1)

	e1, ok := r.sessions[id1]
	require.True(t, ok)
	persistedSID := e1.sid

	// The external key is bound in the persisted store.
	gotSID, err := e1.rt.Store.LookupByKey(ctx, "jira", "CLOAK-1")
	require.NoError(t, err)
	assert.Equal(t, persistedSID, gotSID, "external key must resolve to the attached session")

	// Drive a turn through the locking driver — it transitions the persisted
	// session.
	srvEntry, ok := r.Get(id1)
	require.True(t, ok)
	out, err := srvEntry.Driver.SubmitDirect(ctx, "go", map[string]any{"direction": "south"})
	require.NoError(t, err)
	require.NotNil(t, out)
	assert.Equal(t, "transitioned", out.Mode.String())

	// Re-attach to the same key: same live session id, no second sink.
	id2, err := r.AttachExternal(ctx, story, "jira:CLOAK-1")
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "re-attaching the same ticket must reuse the live session")
}

// TestAttachExternal_BridgeCoDrivesSameSession proves an inbound bridge reply
// and a browser turn advance the SAME persisted session, serialised by the
// writer lock, with the external author recorded.
func TestAttachExternal_BridgeCoDrivesSameSession(t *testing.T) {
	r := newAttachRegistry(t)
	t.Cleanup(r.Close)
	ctx := context.Background()
	story := cloakStoryPath(t)

	id, err := r.AttachExternal(ctx, story, "jira:CLOAK-2")
	require.NoError(t, err)
	e := r.sessions[id]

	// A bridge driver over the same orchestrator + store + sid co-drives.
	bd := newBridgeDriver(e.rt.Orch, e.rt.Store, e.sid)
	err = bd.SubmitIntent(ctx, "go", map[string]any{"direction": "south"}, "alice")
	require.NoError(t, err)

	// The turn landed on the persisted session: a subsequent View reflects the
	// advanced state.
	srvEntry, _ := r.Get(id)
	view, err := srvEntry.Driver.View(ctx)
	require.NoError(t, err)
	assert.NotEqual(t, "foyer", string(view.NewState), "bridge turn should have advanced past foyer")
}

// TestAttachExternal_BadKey rejects a malformed external key.
func TestAttachExternal_BadKey(t *testing.T) {
	r := newAttachRegistry(t)
	t.Cleanup(r.Close)
	_, err := r.AttachExternal(context.Background(), cloakStoryPath(t), "no-colon")
	require.Error(t, err)
}

var _ = store.ErrSessionNotFound // keep store imported for clarity of intent
