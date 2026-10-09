package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
	"kitsoki/internal/runstatus/server"
)

// uiKitDir is the S3c fixture kit (this package's own testdata — separate
// from internal/app's S1 loader fixture): a story plus a trivial ui/panel.mjs
// asset, just enough to exercise the /kit/<ns>/<kit>/ui/* static route and
// the index.html registry/import-map injection end to end.
const uiKitDir = "testdata/kits/uikit"

func newUIKitDispatcher(t *testing.T) *kitendpoint.Dispatcher {
	t.Helper()
	def, err := kit.LoadDir(uiKitDir)
	require.NoError(t, err)
	kits := kit.NewRegistry()
	require.NoError(t, kits.Add(def))
	reg := host.NewRegistry()
	return kitendpoint.NewDispatcher(kits, reg)
}

func newUIKitServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(server.NewMulti(newStubProvider(), server.WithKits(newUIKitDispatcher(t))).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestKitUI_ServesAsset(t *testing.T) {
	ts := newUIKitServer(t)
	resp, err := http.Get(ts.URL + "/kit/kitsoki-test/uikit/ui/panel.mjs")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "export default")
}

func TestKitUI_UnknownKit404s(t *testing.T) {
	ts := newUIKitServer(t)
	resp, err := http.Get(ts.URL + "/kit/kitsoki-test/no-such-kit/ui/panel.mjs")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestKitUI_NamespaceMismatch404s(t *testing.T) {
	ts := newUIKitServer(t)
	resp, err := http.Get(ts.URL + "/kit/wrong-namespace/uikit/ui/panel.mjs")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestKitUI_PathTraversalIsRejected(t *testing.T) {
	ts := newUIKitServer(t)
	resp, err := http.Get(ts.URL + "/kit/kitsoki-test/uikit/ui/../../../../etc/passwd")
	require.NoError(t, err)
	defer resp.Body.Close()
	// The traversal segments are either collapsed by URL/path cleaning back
	// under ui/ (yielding a 404 for a nonexistent file) or rejected outright
	// — either way, never a 200.
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}

func TestKitUI_NoDispatcher404s(t *testing.T) {
	ts := httptest.NewServer(server.NewMulti(newStubProvider()).Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/kit/kitsoki-test/uikit/ui/panel.mjs")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestIndexHTML_InjectsKitRegistryAndImportMap(t *testing.T) {
	ts := newUIKitServer(t)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Skip("runstatus SPA not built into this test binary (assets/index.html placeholder only)")
	}
	require.Equal(t, http.StatusOK, resp.StatusCode)

	html := string(body)
	assert.Contains(t, html, `id="kitsoki-kits"`)
	assert.Contains(t, html, `"kit":"uikit"`)
	assert.Contains(t, html, `type="importmap"`)
	assert.Contains(t, html, `@kitsoki-test/uikit/ui/panel`)
	assert.Contains(t, html, `/kit/kitsoki-test/uikit/ui/panel.mjs`)
	// The injected scripts must land inside <head>, before its close tag.
	headClose := strings.Index(strings.ToLower(html), "</head>")
	kitsScript := strings.Index(html, `id="kitsoki-kits"`)
	require.NotEqual(t, -1, headClose)
	require.NotEqual(t, -1, kitsScript)
	assert.Less(t, kitsScript, headClose)
}

func TestIndexHTML_NoInjectionWithoutDispatcher(t *testing.T) {
	ts := httptest.NewServer(server.NewMulti(newStubProvider()).Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Skip("runstatus SPA not built into this test binary")
	}
	assert.NotContains(t, string(body), `id="kitsoki-kits"`)
}
