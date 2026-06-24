package studio_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

func TestRunstatusProvider_ExposesStudioDrivingHandle(t *testing.T) {
	ctx := context.Background()
	srv, sess := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	sh, err := sess.ResolveSession(handle)
	require.NoError(t, err)
	require.NotEmpty(t, sh.SID)

	provider := studio.NewRunstatusProvider(sess)
	current, ok := provider.CurrentSession()
	require.True(t, ok)
	assert.Equal(t, string(sh.SID), current)

	entry, ok := provider.Get(string(sh.SID))
	require.True(t, ok, "provider resolves by kitsoki session id")
	require.NotNil(t, entry.Source)
	require.NotNil(t, entry.Driver)

	snap, err := entry.Source.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(sh.SID), snap.Session.SessionID)
	assert.Equal(t, "foyer", snap.Session.CurrentState)

	list := provider.List()
	require.Len(t, list, 1)
	assert.Equal(t, string(sh.SID), list[0].SessionID)
	assert.Equal(t, "foyer", list[0].CurrentState)

	require.NoError(t, sess.CloseSession(handle))
	_, ok = provider.Get(string(sh.SID))
	assert.False(t, ok, "closed handles disappear from the web provider")
	_, ok = provider.CurrentSession()
	assert.False(t, ok)
}
