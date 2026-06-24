package host_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/transport"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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
		{"thread": "S-1", "body": "x"},    // missing transport
		{"transport": "tui", "body": "x"}, // missing thread
	}
	for _, args := range cases {
		res, err := host.TransportPostHandler(ctx, args)
		require.NoError(t, err)
		assert.NotEmpty(t, res.Error)
	}
}

// TestTransportPost_BodyAcceptsStructuredPayload covers the production case
// where `bind` writes a host-result object (e.g. the validated submit() dict
// from `host.agent.ask_with_mcp`) into a world slot, and the next checkpoint
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

// TestTransportPost_Bitbucket verifies the end-to-end host→transport
// path for the Bitbucket driver: the handler must forward extra args
// (pr_project / pr_slug / pr_id) into Message.Extra so the transport can
// build the right REST URL.  A regression that drops the extras would
// surface as "all three are required" instead of a real HTTP call.
func TestTransportPost_Bitbucket(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotBody map[string]any
	)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":12345}`)),
			Request:    r,
		}, nil
	})}

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL:    "https://bitbucket.test",
		Token:      "TOK",
		HTTPClient: client,
	})
	require.NoError(t, err)
	reg := transport.NewRegistry()
	reg.Register(bt)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport":  "bitbucket",
		"thread":     "PLTFRM-89912",
		"phase_id":   "phase_13",
		"title":      "Validate",
		"body":       "All checks pass.",
		"pr_project": "PLTFRM",
		"pr_slug":    "cyberstack",
		"pr_id":      "302",
	})
	require.NoError(t, err)
	require.Empty(t, res.Error, "no error expected: %v", res.Error)
	assert.Equal(t, "12345", res.Data["message_id"])

	assert.Equal(t,
		"/rest/api/1.0/projects/PLTFRM/repos/cyberstack/pull-requests/302/comments",
		gotPath,
	)
	assert.Equal(t, "Bearer TOK", gotAuth)
	text, _ := gotBody["text"].(string)
	assert.True(t, strings.HasPrefix(text, "[kitsoki] "),
		"body must start with [kitsoki] marker, got: %q", text)
	assert.Contains(t, text, "*Validate*", "title should render as bold")
	assert.Contains(t, text, "All checks pass.")
}

// TestTransportPost_BitbucketMissingPRIDIsCleanError verifies that
// omitting a required Bitbucket coord produces a Result.Error envelope,
// NOT a Go panic.  The handler must surface the transport's validation
// failure through the on_error: arc rather than crashing the orchestrator.
func TestTransportPost_BitbucketMissingPRIDIsCleanError(t *testing.T) {
	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{Token: "t"})
	require.NoError(t, err)
	reg := transport.NewRegistry()
	reg.Register(bt)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport":  "bitbucket",
		"thread":     "PLTFRM-89912",
		"body":       "hi",
		"pr_project": "P",
		"pr_slug":    "s",
		// pr_id intentionally omitted
	})
	require.NoError(t, err, "missing pr_id must not be a Go-level error")
	require.NotEmpty(t, res.Error, "missing pr_id must produce a Result.Error")
	assert.Contains(t, res.Error, "pr_id")
}

// TestTransportPost_BitbucketIntCoordCoerces confirms that a YAML
// integer scalar bound into args (e.g. `pr_id: 302` rather than
// `pr_id: "302"`) is coerced to its decimal string form by the handler
// before being handed to the transport.  Tests the collectExtras path
// for non-string scalars.
func TestTransportPost_BitbucketIntCoordCoerces(t *testing.T) {
	var gotPath string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"id":1}`))),
			Request:    r,
		}, nil
	})}

	bt, err := transport.NewBitbucketTransport(transport.BitbucketConfig{
		BaseURL:    "https://bitbucket.test",
		Token:      "t",
		HTTPClient: client,
	})
	require.NoError(t, err)
	reg := transport.NewRegistry()
	reg.Register(bt)
	t.Cleanup(func() { _ = reg.Close() })
	ctx := transport.WithRegistry(context.Background(), reg)

	res, err := host.TransportPostHandler(ctx, map[string]any{
		"transport":  "bitbucket",
		"thread":     "T-1",
		"body":       "hi",
		"pr_project": "P",
		"pr_slug":    "s",
		"pr_id":      302, // numeric, not string
	})
	require.NoError(t, err)
	require.Empty(t, res.Error)
	assert.Contains(t, gotPath, "/pull-requests/302/")
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
