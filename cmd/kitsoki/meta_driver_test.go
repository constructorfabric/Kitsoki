package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/webconfig"
)

// modeKeys collects the keys of a MetaDriver's advertised modes.
func modeKeys(t *testing.T, reg *SessionRegistry, sid string) map[string]bool {
	t.Helper()
	entry, ok := reg.Get(sid)
	require.True(t, ok)
	require.NotNil(t, entry.Meta)
	modes, err := entry.Meta.Modes(context.Background())
	require.NoError(t, err)
	keys := map[string]bool{}
	for _, m := range modes {
		keys[m.Key] = true
	}
	return keys
}

// TestMetaDriver_PerSession_AskAndEdit drives a live session's meta driver end
// to end against the no-LLM stub agent: read-only Story Q&A returns a reply
// and persists the transcript without a reload; Story edit mutates the story
// tree so the turn requests a reload. No LLM is touched.
func TestMetaDriver_PerSession_AskAndEdit(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	entry, ok := reg.Get(sid)
	require.True(t, ok)
	require.NotNil(t, entry.Meta, "a live session must carry a meta driver")
	md := entry.Meta

	// The builtin story modes are always available on a live session.
	keys := modeKeys(t, reg, sid)
	assert.True(t, keys["story.edit"], "story.edit must be available, got %v", keys)
	assert.True(t, keys["story.ask"], "story.ask must be available, got %v", keys)
	assert.True(t, keys["story.improve"], "story.improve must be available, got %v", keys)

	// Story Q&A: read-only, no reload, transcript persists (user + assistant).
	ask, err := md.Enter(ctx, "story.ask", "")
	require.NoError(t, err)
	require.NotEmpty(t, ask.ChatID)
	assert.Empty(t, ask.Messages, "a fresh chat starts empty")

	res, err := md.Send(ctx, "story.ask", ask.ChatID, "what state am I in?")
	require.NoError(t, err)
	assert.False(t, res.ReloadRequested, "a read-only Q&A turn must not request a reload")
	assert.NotEmpty(t, res.Assistant)

	msgs, err := md.Transcript(ctx, ask.ChatID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "transcript should hold the user turn + the assistant reply")
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)

	// Re-entering the same mode+scope resumes the SAME chat (persistence).
	reAsk, err := md.Enter(ctx, "story.ask", "")
	require.NoError(t, err)
	assert.Equal(t, ask.ChatID, reAsk.ChatID, "re-enter must resume the persistent chat")
	assert.Len(t, reAsk.Messages, 2, "resumed chat carries its transcript")

	// Story edit: edit-capable → the stub mutates the story tree → reload.
	edit, err := md.Enter(ctx, "story.edit", "")
	require.NoError(t, err)
	eres, err := md.Send(ctx, "story.edit", edit.ChatID, "make the foyer darker")
	require.NoError(t, err)
	assert.True(t, eres.ReloadRequested, "a story-edit turn must request a reload")
	assert.True(t, hasSuffix(eres.ChangedFiles, "meta-edits.log"),
		"changed files should include the edit marker, got %v", eres.ChangedFiles)
}

// TestMetaDriver_NewChat archives the active chat and opens a fresh, empty one.
func TestMetaDriver_NewChat(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)
	entry, _ := reg.Get(sid)
	md := entry.Meta

	first, err := md.Enter(ctx, "story.ask", "")
	require.NoError(t, err)
	_, err = md.Send(ctx, "story.ask", first.ChatID, "hello")
	require.NoError(t, err)

	fresh, err := md.NewChat(ctx, "story.ask", first.ChatID)
	require.NoError(t, err)
	assert.NotEqual(t, first.ChatID, fresh.ChatID, "new chat must mint a different row")
	assert.Empty(t, fresh.Messages, "the fresh chat starts empty")
}

// TestMetaDriver_ReloadInvalidatesController proves Reload drops the cached meta
// controller so a subsequent meta turn rebuilds against the reloaded AppDef.
func TestMetaDriver_ReloadInvalidatesController(t *testing.T) {
	storiesDir, appPath := writeStory(t, "mini", []byte(minimalStory))
	reg := NewRegistry(webconfig.WebConfig{}, []string{storiesDir}, deterministicBase(t))
	t.Cleanup(reg.Close)

	ctx := context.Background()
	sid, err := reg.NewSession(ctx, appPath)
	require.NoError(t, err)

	// Build the controller via Get.
	_, ok := reg.Get(sid)
	require.True(t, ok)
	reg.mu.Lock()
	require.NotNil(t, reg.sessions[sid].metaController, "Get should have cached a controller")
	reg.mu.Unlock()

	// Reload should invalidate it.
	_, err = reg.Reload(ctx, sid)
	require.NoError(t, err)
	reg.mu.Lock()
	assert.Nil(t, reg.sessions[sid].metaController, "Reload must drop the cached meta controller")
	reg.mu.Unlock()
}

// TestMetaSelf_KitsokiHelp exercises the home-screen (session-less) self driver:
// it exposes only the cross-app kitsoki.* modes and answers read-only.
func TestMetaSelf_KitsokiHelp(t *testing.T) {
	// kitsoki.* modes are gated on $KITSOKI_REPO; set it so they're injected.
	t.Setenv("KITSOKI_REPO", t.TempDir())

	reg := NewRegistry(webconfig.WebConfig{}, nil, deterministicBase(t))
	t.Cleanup(reg.Close)

	md, ok := reg.MetaSelf()
	require.True(t, ok, "self meta driver must be available")

	ctx := context.Background()
	modes, err := md.Modes(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, modes, "self driver must expose the kitsoki.* modes when KITSOKI_REPO is set")
	for _, m := range modes {
		assert.Equal(t, "kitsoki", m.Group, "self driver exposes only cross-app kitsoki.* modes, got %q", m.Key)
	}
	keys := map[string]bool{}
	for _, m := range modes {
		keys[m.Key] = true
	}
	assert.True(t, keys["kitsoki.improve"], "self driver must expose kitsoki.improve, got %v", keys)

	sess, err := md.Enter(ctx, "kitsoki.ask", "")
	require.NoError(t, err)
	res, err := md.Send(ctx, "kitsoki.ask", sess.ChatID, "what is kitsoki?")
	require.NoError(t, err)
	assert.False(t, res.ReloadRequested, "a read-only kitsoki.ask turn must not request a reload")
	assert.NotEmpty(t, res.Assistant)
}

func hasSuffix(xs []string, suffix string) bool {
	for _, x := range xs {
		if strings.HasSuffix(x, suffix) {
			return true
		}
	}
	return false
}
