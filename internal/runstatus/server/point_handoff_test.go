package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// TestPoint_FreshTokenServesChromelessSPA: a GET /point with a freshly-minted
// token serves the bundled SPA (200 + HTML). The chrome-less behaviour itself is
// the SPA reading ?chromeless=1 (covered by the Playwright spec); here we assert
// the route gates on the token and serves the app.
func TestPoint_FreshTokenServesChromelessSPA(t *testing.T) {
	ps := NewPointServer()
	ts := httptest.NewServer(ps.Handler())
	defer ts.Close()

	token, _ := ps.Mint(host.SpatialRequest{Prompt: "point at it"})

	resp, err := http.Get(ts.URL + "/point?token=" + token + "&chromeless=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	// A valid token reaches the SPA-serving stage: 200 when the SPA is bundled,
	// or 503 (web.ErrNotBuilt) in a unit binary where only assets/.gitkeep is
	// embedded. The point is it is NOT 404 — the token was accepted. The actual
	// chrome-less HTML render is covered by the Playwright spec (real SPA build).
	require.NotEqual(t, http.StatusNotFound, resp.StatusCode, "a fresh token must not be rejected")
	require.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, resp.StatusCode)
	if resp.StatusCode == http.StatusOK {
		assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	}
}

// TestPoint_UnknownTokenIs404: a GET /point with a token nobody minted 404s
// (never leaks whether it existed).
func TestPoint_UnknownTokenIs404(t *testing.T) {
	ps := NewPointServer()
	ts := httptest.NewServer(ps.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/point?token=deadbeef&chromeless=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestPoint_ConsumedTokenIs404: once the return endpoint consumes a token, the
// GET /point route 404s — the non-negotiable consumed-token guard.
func TestPoint_ConsumedTokenIs404(t *testing.T) {
	ps := NewPointServer()
	ts := httptest.NewServer(ps.Handler())
	defer ts.Close()

	token, ch := ps.Mint(host.SpatialRequest{})

	// Consume via the return endpoint.
	body := []byte(`{"visual":{"point":{"x":10,"y":20}}}`)
	resp, err := http.Post(ts.URL+"/point/return?token="+token, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// The bundle reached the parked channel.
	select {
	case bundle := <-ch:
		assert.Equal(t, 10, bundle.Point.X)
		assert.Equal(t, 20, bundle.Point.Y)
	case <-time.After(time.Second):
		t.Fatal("return endpoint did not deliver the bundle to the parked channel")
	}

	// A subsequent GET 404s — the token is consumed.
	get, err := http.Get(ts.URL + "/point?token=" + token)
	require.NoError(t, err)
	get.Body.Close()
	assert.Equal(t, http.StatusNotFound, get.StatusCode)

	// And a second return 404s too.
	resp2, err := http.Post(ts.URL+"/point/return?token="+token, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

// TestPoint_ExpiredTokenIs404: a token past its TTL 404s on both GET and return.
func TestPoint_ExpiredTokenIs404(t *testing.T) {
	h := newPointHandoff()
	token, _ := h.mint(host.SpatialRequest{})

	// Force expiry by rewinding the slot's deadline.
	h.mu.Lock()
	h.pending[token].expires = time.Now().Add(-time.Minute)
	h.mu.Unlock()

	_, ok := h.valid(token)
	assert.False(t, ok, "expired token must not validate")
	assert.False(t, h.consume(token, host.VisualAmbient{}), "expired token must not consume")
}

// TestPointReturn_HandsWellFormedBundle: the return endpoint decodes the full
// visual bundle (frame, point, element with positional bbox) into a
// host.VisualAmbient and hands it to the parked turn.
func TestPointReturn_HandsWellFormedBundle(t *testing.T) {
	ps := NewPointServer()
	ts := httptest.NewServer(ps.Handler())
	defer ts.Close()

	token, ch := ps.Mint(host.SpatialRequest{MediaHandle: "vid#abc"})

	body := []byte(`{"visual":{
		"frame_handle":"frame#deadbeef",
		"media_handle":"vid#abc",
		"point":{"x":120,"y":48},
		"route":"/s/sess",
		"element":{"selector":"[data-testid=run]","role":"button","text":"Run","bbox":[10,20,30,40]}
	}}`)
	resp, err := http.Post(ts.URL+"/point/return?token="+token, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case b := <-ch:
		assert.Equal(t, "frame#deadbeef", b.FrameHandle)
		assert.Equal(t, "vid#abc", b.MediaHandle)
		assert.Equal(t, 120, b.Point.X)
		assert.Equal(t, 48, b.Point.Y)
		require.NotNil(t, b.Element)
		assert.Equal(t, "[data-testid=run]", b.Element.Selector)
		assert.Equal(t, "button", b.Element.Role)
		assert.Equal(t, [4]int{10, 20, 30, 40}, b.Element.Bbox)
	case <-time.After(time.Second):
		t.Fatal("bundle never delivered")
	}
}

// TestPointHandoff_CancelDropsToken: cancel drops a pending token so a late
// return 404s (the parked turn gave up).
func TestPointHandoff_CancelDropsToken(t *testing.T) {
	h := newPointHandoff()
	token, _ := h.mint(host.SpatialRequest{})
	h.cancel(token)
	assert.False(t, h.consume(token, host.VisualAmbient{}))
}

// TestPoint_RegisteredOnFullServerHandler: the routes are wired into the full
// Server.Handler() too (kitsoki web), not only the standalone PointServer.
func TestPoint_RegisteredOnFullServerHandler(t *testing.T) {
	s := newServer(nil, newConfig(nil))
	token, _ := s.points.mint(host.SpatialRequest{})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/point?token=" + token)
	require.NoError(t, err)
	resp.Body.Close()
	// Registered + token accepted: reaches the SPA-serving stage (200 bundled /
	// 503 not-built), NOT 404 (which would mean the route isn't wired in).
	require.NotEqual(t, http.StatusNotFound, resp.StatusCode, "the /point route must be registered on the full handler")
	assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, resp.StatusCode)

	_ = context.Background()
}
