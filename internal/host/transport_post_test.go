package host_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/transport"
)

func TestTransportPost_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.transport.post"); !ok {
		t.Fatal("host.transport.post was not registered by RegisterBuiltins")
	}
}

func TestTransportPost_NoRegistryInContext(t *testing.T) {
	res, err := host.TransportPostHandler(context.Background(), map[string]any{
		"transport": "tui",
		"thread":    "S-1",
		"body":      "x",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Error)
}

func TestTransportPost_DispatchesIntoRegistry(t *testing.T) {
	tt := transport.NewTUITransport()
	reg := transport.NewRegistry()
	reg.Register(tt)
	t.Cleanup(func() { _ = reg.Close() })

	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport": "tui",
		"thread":    "S-1",
		"phase_id":  "phase_1",
		"title":     "Reproduction",
		"body":      "Step 1: ...",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error, "no error expected")
	require.NotEmpty(t, res.Data["message_id"])

	posts := tt.Drain()
	require.Len(t, posts, 1)
	assert.Equal(t, "phase_1", posts[0].Msg.PhaseID)
	assert.Equal(t, "Reproduction", posts[0].Msg.Title)
	assert.Equal(t, "Step 1: ...", posts[0].Msg.Body)
	assert.Equal(t, "tui", posts[0].Key.Transport)
	assert.Equal(t, "S-1", posts[0].Key.Thread)
	assert.Equal(t, transport.DefaultBotMarker, posts[0].Msg.BotMarker)
}

func TestTransportPost_TransportNotRegistered(t *testing.T) {
	reg := transport.NewRegistry()
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport": "missing",
		"thread":    "S-1",
		"body":      "x",
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Error)
}

func TestTransportPost_RequiredArgs(t *testing.T) {
	reg := transport.NewRegistry()
	reg.Register(transport.NewTUITransport())
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	cases := []map[string]any{
		{"thread": "S-1", "body": "x"},                // missing transport
		{"transport": "tui", "body": "x"},             // missing thread
	}
	for _, args := range cases {
		res, err := host.TransportPostHandler(ctx, args)
		require.NoError(t, err)
		assert.NotEmpty(t, res.Error)
	}
}

// TestTransportPost_BodyAcceptsStructuredPayload covers the production case
// where `bind` writes a host-result object (e.g. the validated submit() dict
// from `host.oracle.ask_with_mcp`) into a world slot, and the next checkpoint
// effect's `body:` template renders that slot.  Without the JSON-coercion
// fallback in `bodyArg`, the type assertion silently drops the dict and the
// transport receives an empty body — the bug discovered live on PLTFRM-89912.
// The handler must coerce maps to JSON so the comment is non-empty.
func TestTransportPost_BodyAcceptsStructuredPayload(t *testing.T) {
	tt := transport.NewTUITransport()
	reg := transport.NewRegistry()
	reg.Register(tt)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	bodyDict := map[string]any{
		"summary_markdown": "## Phase complete\nAll checks pass.",
		"verdict":          "approved",
	}
	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport": "tui",
		"thread":    "S-1",
		"title":     "Coverage Review",
		"body":      bodyDict, // structured payload, not a string
	})
	require.NoError(t, err)
	assert.Empty(t, res.Error)
	posts := tt.Drain()
	require.Len(t, posts, 1)
	got := posts[0].Msg.Body
	assert.NotEmpty(t, got, "body must not silently drop a structured payload")
	assert.Contains(t, got, "summary_markdown", "JSON serialization preserves keys")
	assert.Contains(t, got, "approved", "JSON serialization preserves values")
}

// TestTransportPost_BodyAcceptsStringVerbatim confirms the common case still
// works after the type-coercion change: a plain string body passes through
// unmodified (no JSON wrapping).
func TestTransportPost_BodyAcceptsStringVerbatim(t *testing.T) {
	tt := transport.NewTUITransport()
	reg := transport.NewRegistry()
	reg.Register(tt)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport": "tui",
		"thread":    "S-1",
		"body":      "hello world",
	})
	require.NoError(t, err)
	assert.Empty(t, res.Error)
	posts := tt.Drain()
	require.Len(t, posts, 1)
	assert.Equal(t, "hello world", posts[0].Msg.Body)
}
