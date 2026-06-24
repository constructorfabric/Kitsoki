package server_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
)

// fakeMetaDriver is a func-field server.MetaDriver for the dispatch tests.
type fakeMetaDriver struct {
	modes []server.MetaModeInfo
	send  server.MetaSendResult
}

func (f *fakeMetaDriver) Modes(context.Context) ([]server.MetaModeInfo, error) {
	return f.modes, nil
}
func (f *fakeMetaDriver) Enter(_ context.Context, mode, chatID string) (server.MetaSession, error) {
	return server.MetaSession{ChatID: "chat-1", ModeKey: mode, Messages: []server.MetaMessage{}}, nil
}
func (f *fakeMetaDriver) Send(_ context.Context, _, _, _ string) (server.MetaSendResult, error) {
	return f.send, nil
}
func (f *fakeMetaDriver) NewChat(_ context.Context, mode, _ string) (server.MetaSession, error) {
	return server.MetaSession{ChatID: "chat-2", ModeKey: mode, Messages: []server.MetaMessage{}}, nil
}
func (f *fakeMetaDriver) Transcript(context.Context, string) ([]server.MetaMessage, error) {
	return []server.MetaMessage{{Role: "user", Text: "hi"}, {Role: "assistant", Text: "hello"}}, nil
}

// metaProvider embeds the stub provider and adds the optional MetaSelf hook.
type metaProvider struct {
	*stubProvider
	self server.MetaDriver
}

func (m *metaProvider) MetaSelf() (server.MetaDriver, bool) {
	if m.self == nil {
		return nil, false
	}
	return m.self, true
}

// putMeta registers an entry whose Meta seam is the given driver.
func (p *stubProvider) putMeta(sessionID string, meta server.MetaDriver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[sessionID] = server.Entry{
		Source: &stubSource{header: runstatus.SessionHeader{SessionID: sessionID}, def: testDef()},
		Meta:   meta,
	}
}

func TestMeta_PerSession_ModesAndSend(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	fake := &fakeMetaDriver{
		modes: []server.MetaModeInfo{
			{Key: "story.edit", Label: "Story edit", Group: "story", ReadOnly: false},
			{Key: "story.ask", Label: "Story Q&A", Group: "story", ReadOnly: true},
		},
		send: server.MetaSendResult{
			Assistant:       "done",
			ChatID:          "chat-1",
			ReloadRequested: true,
			ChangedFiles:    []string{"meta-edits.log"},
		},
	}
	p.putMeta("s1", fake)
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var modesOut struct {
		Modes []server.MetaModeInfo `json:"modes"`
	}
	rpcCall(t, ts, "runstatus.meta.modes", map[string]any{"session_id": "s1"}, &modesOut)
	require.Len(t, modesOut.Modes, 2)
	assert.Equal(t, "story.edit", modesOut.Modes[0].Key)

	var sendOut server.MetaSendResult
	rpcCall(t, ts, "runstatus.meta.send", map[string]any{
		"session_id": "s1", "mode": "story.edit", "chat_id": "chat-1", "input": "make it dark",
	}, &sendOut)
	assert.Equal(t, "done", sendOut.Assistant)
	assert.True(t, sendOut.ReloadRequested)
	assert.Equal(t, []string{"meta-edits.log"}, sendOut.ChangedFiles)
}

func TestMeta_Enter_MissingMode(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.putMeta("s1", &fakeMetaDriver{})
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, msg := rpcCallExpectError(t, ts, "runstatus.meta.enter", map[string]any{"session_id": "s1"})
	assert.Equal(t, -32000, code, "missing mode should be a server error")
	assert.Contains(t, msg, "mode")
}

func TestMeta_NoMetaDriver_ReadOnly(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	// Entry with no Meta seam (a read-only surface analogue).
	p.put("s1", runstatus.SessionHeader{SessionID: "s1"}, testDef())
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, msg := rpcCallExpectError(t, ts, "runstatus.meta.modes", map[string]any{"session_id": "s1"})
	assert.Equal(t, -32001, code, "an entry without a meta driver should report codeReadOnly")
	assert.Contains(t, msg, "read-only")
}

func TestMeta_UnknownSession(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, _ := rpcCallExpectError(t, ts, "runstatus.meta.modes", map[string]any{"session_id": "ghost"})
	assert.Equal(t, -32002, code, "unknown session_id should report codeNotFound")
}

func TestMeta_Self_RoutesOnEmptySession(t *testing.T) {
	t.Parallel()
	self := &fakeMetaDriver{modes: []server.MetaModeInfo{{Key: "kitsoki.ask", Group: "kitsoki", ReadOnly: true}}}
	p := &metaProvider{stubProvider: newStubProvider(), self: self}
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var modesOut struct {
		Modes []server.MetaModeInfo `json:"modes"`
	}
	rpcCall(t, ts, "runstatus.meta.modes", map[string]any{"session_id": ""}, &modesOut)
	require.Len(t, modesOut.Modes, 1)
	assert.Equal(t, "kitsoki.ask", modesOut.Modes[0].Key)
}

func TestMeta_Self_UnavailableWithoutHook(t *testing.T) {
	t.Parallel()
	// stubProvider does NOT implement MetaSelfProvider, so an empty session_id
	// has no self driver to route to.
	p := newStubProvider()
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, msg := rpcCallExpectError(t, ts, "runstatus.meta.modes", map[string]any{"session_id": ""})
	assert.Equal(t, -32001, code, "no self meta hook → read-only")
	assert.Contains(t, msg, "read-only")
}
