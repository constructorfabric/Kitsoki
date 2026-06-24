package transport_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/transport"
)

// fakeTransport is a minimal Transport for registry tests.
type fakeTransport struct {
	id     string
	posts  []transport.Message
	keys   []transport.SessionKey
	postID string
	err    error
	closed bool
}

func (f *fakeTransport) ID() string { return f.id }

func (f *fakeTransport) Post(_ context.Context, key transport.SessionKey, msg transport.Message) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.keys = append(f.keys, key)
	f.posts = append(f.posts, msg)
	if f.postID == "" {
		return "msg-1", nil
	}
	return f.postID, nil
}

func (f *fakeTransport) Close() error { f.closed = true; return nil }

func TestRegistry_RegisterAndPost(t *testing.T) {
	r := transport.NewRegistry()
	t.Cleanup(func() { _ = r.Close() })

	ft := &fakeTransport{id: "fake"}
	r.Register(ft)

	got, ok := r.Get("fake")
	require.True(t, ok)
	require.Same(t, ft, got)

	id, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "fake", Thread: "T-1"},
		transport.Message{Title: "hi", Body: "world"},
	)
	require.NoError(t, err)
	assert.Equal(t, "msg-1", id)
	require.Len(t, ft.posts, 1)
	assert.Equal(t, transport.DefaultBotMarker, ft.posts[0].BotMarker, "DefaultBotMarker should be filled in by the registry")
	assert.False(t, ft.posts[0].Timestamp.IsZero(), "Timestamp should be filled in by the registry")
}

func TestRegistry_PostNotFound(t *testing.T) {
	r := transport.NewRegistry()
	_, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "missing", Thread: "T-1"},
		transport.Message{Body: "x"},
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, transport.ErrTransportNotFound))
}

func TestRegistry_DuplicateRegistrationPanics(t *testing.T) {
	r := transport.NewRegistry()
	r.Register(&fakeTransport{id: "dup"})
	require.Panics(t, func() {
		r.Register(&fakeTransport{id: "dup"})
	})
}

func TestRegistry_NilOrEmptyIDPanics(t *testing.T) {
	r := transport.NewRegistry()
	require.Panics(t, func() { r.Register(nil) })
	require.Panics(t, func() { r.Register(&fakeTransport{id: ""}) })
}

func TestSessionKey_String(t *testing.T) {
	k := transport.SessionKey{Transport: "jira", Thread: "PLTFRM-12345"}
	assert.Equal(t, "jira:PLTFRM-12345", k.String())
}

func TestRegistry_RetainsCustomBotMarker(t *testing.T) {
	r := transport.NewRegistry()
	ft := &fakeTransport{id: "fake"}
	r.Register(ft)

	_, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "fake", Thread: "T-1"},
		transport.Message{Body: "x", BotMarker: "[custom]"},
	)
	require.NoError(t, err)
	assert.Equal(t, "[custom]", ft.posts[0].BotMarker)
}

func TestRegistry_IDsAndClose(t *testing.T) {
	r := transport.NewRegistry()
	a := &fakeTransport{id: "a"}
	c := &fakeTransport{id: "c"}
	b := &fakeTransport{id: "b"}
	r.Register(a)
	r.Register(c)
	r.Register(b)

	assert.Equal(t, []string{"a", "b", "c"}, r.IDs())

	require.NoError(t, r.Close())
	assert.True(t, a.closed && b.closed && c.closed)
}

// ── TUITransport ─────────────────────────────────────────────────────────────

func TestTUITransport_BuffersPosts(t *testing.T) {
	tt := transport.NewTUITransport()
	t.Cleanup(func() { _ = tt.Close() })

	id1, err := tt.Post(context.Background(),
		transport.SessionKey{Transport: "tui", Thread: "S-1"},
		transport.Message{Title: "first", Body: "one"},
	)
	require.NoError(t, err)
	require.NotEmpty(t, id1)

	id2, err := tt.Post(context.Background(),
		transport.SessionKey{Transport: "tui", Thread: "S-1"},
		transport.Message{Title: "second", Body: "two"},
	)
	require.NoError(t, err)
	require.NotEmpty(t, id2)
	require.NotEqual(t, id1, id2)

	assert.Equal(t, 2, tt.Pending())

	drained := tt.Drain()
	require.Len(t, drained, 2)
	assert.Equal(t, "first", drained[0].Msg.Title)
	assert.Equal(t, "second", drained[1].Msg.Title)
	assert.Equal(t, 0, tt.Pending(), "Drain should empty the buffer")
}

func TestTUITransport_RegistersIntoRegistry(t *testing.T) {
	r := transport.NewRegistry()
	t.Cleanup(func() { _ = r.Close() })

	tt := transport.NewTUITransport()
	r.Register(tt)

	id, err := r.Post(context.Background(),
		transport.SessionKey{Transport: "tui", Thread: "S-1"},
		transport.Message{Body: "via registry"},
	)
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.Equal(t, 1, tt.Pending())
	posts := tt.Drain()
	assert.Equal(t, "via registry", posts[0].Msg.Body)
	assert.Equal(t, transport.DefaultBotMarker, posts[0].Msg.BotMarker)
}
