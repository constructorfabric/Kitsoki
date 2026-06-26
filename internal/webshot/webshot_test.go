package webshot

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ── Test doubles ────────────────────────────────────────────────────────────
//
// These keep the default suite GREEN with NO browser and NO real kitsoki web:
// the server half is a httptest server that 200s on / (so the real
// HandlerServer boot/health/teardown path runs) while recording any RPC/agent
// hits, and the browser half is a stub that writes a synthetic PNG of the
// requested viewport. The real Chromium path is exercised only by the GATED
// TestShot_E2E below.

// stubInvoker writes a synthetic, decodable PNG of req.Viewport so a shot has a
// real image to return without launching Chromium. It records the URL it was
// asked to capture so a test can assert URL construction.
type stubInvoker struct {
	gotURL        string
	gotViewport   Viewport
	gotAssertText []string
	calls         int32
}

func (s *stubInvoker) Capture(_ context.Context, req CaptureRequest) error {
	atomic.AddInt32(&s.calls, 1)
	s.gotURL = req.URL
	s.gotViewport = req.Viewport
	s.gotAssertText = append([]string(nil), req.AssertText...)
	img := image.NewRGBA(image.Rect(0, 0, req.Viewport.Width, req.Viewport.Height))
	// Paint one pixel so the PNG is unambiguously non-empty.
	img.Set(0, 0, color.RGBA{R: 1, A: 255})
	f, err := os.Create(req.OutPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	if req.SemanticOutPath != "" {
		if err := os.WriteFile(req.SemanticOutPath, []byte(`{"ok":true,"actions":[]}`), 0o644); err != nil {
			return err
		}
	}
	if req.RRWebOutPath != "" {
		return os.WriteFile(req.RRWebOutPath, []byte(`{"schemaVersion":1,"source":"kitsoki-visual-record","events":[{"type":4},{"type":2}]}`), 0o644)
	}
	return nil
}

// noLLMHandler is a stand-in kitsoki-web handler: it 200s on GET / (so the real
// HandlerServer health poll passes) and flips a flag if anything ever touches an
// agent/LLM-shaped endpoint during a shot — proving the shot itself performs no
// live harness/agent work.
type noLLMHandler struct {
	rootHits    int32
	agentHits   int32
	lastNonRoot string
}

func (h *noLLMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		atomic.AddInt32(&h.rootHits, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><title>kitsoki web (no-LLM stub)</title>"))
		return
	}
	h.lastNonRoot = r.URL.Path
	// Any agent/LLM-shaped path during a shot is a posture violation.
	if strings.Contains(r.URL.Path, "agent") || strings.Contains(r.URL.Path, "llm") {
		atomic.AddInt32(&h.agentHits, 1)
	}
	w.WriteHeader(http.StatusOK)
}

// ── Tests (no browser, no LLM) ──────────────────────────────────────────────

// TestShot_ReturnsPNGOfKnownState is the headline rendering test: a shot of a
// known state returns a decodable, non-empty PNG of the expected viewport. It
// runs the REAL HandlerServer (boot + health + teardown on an ephemeral port)
// and a STUB browser invoker, so it is green with no Chromium.
//
// Verified to fail before the seam existed: there was no internal/webshot.Shot —
// the package did not compile, so this test could not be written, let alone
// pass. With the seam in place it exercises the full Shot contract end to end
// minus the real browser.
func TestShot_ReturnsPNGOfKnownState(t *testing.T) {
	h := &noLLMHandler{}
	inv := &stubInvoker{}

	want := DefaultViewport
	out, err := Shot(context.Background(), Spec{StoryPath: "stories/bugfix", State: "triage"}, Options{
		Server:  &HandlerServer{Handler: h, HealthTimeout: 5 * time.Second},
		Browser: inv,
	})
	if err != nil {
		t.Fatalf("Shot: %v", err)
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	if got := img.Bounds().Dx(); got != want.Width {
		t.Errorf("PNG width = %d, want %d", got, want.Width)
	}
	if got := img.Bounds().Dy(); got != want.Height {
		t.Errorf("PNG height = %d, want %d", got, want.Height)
	}
	if inv.gotViewport != want {
		t.Errorf("capture viewport = %v, want %v (capture==render invariant)", inv.gotViewport, want)
	}
	if atomic.LoadInt32(&inv.calls) != 1 {
		t.Errorf("browser invoked %d times, want 1", inv.calls)
	}
}

func TestShotWithSemantic_ReturnsOptionalObservation(t *testing.T) {
	h := &noLLMHandler{}
	inv := &stubInvoker{}

	out, err := ShotWithSemantic(context.Background(), Spec{StoryPath: "stories/bugfix", State: "triage"}, Options{
		Server:  &HandlerServer{Handler: h, HealthTimeout: 5 * time.Second},
		Browser: inv,
	})
	if err != nil {
		t.Fatalf("ShotWithSemantic: %v", err)
	}
	if len(out.PNG) == 0 {
		t.Fatal("ShotWithSemantic returned empty PNG")
	}
	if string(out.SemanticJSON) != `{"ok":true,"actions":[]}` {
		t.Fatalf("SemanticJSON = %q", out.SemanticJSON)
	}
	if !strings.Contains(string(out.RRWebJSON), `"source":"kitsoki-visual-record"`) {
		t.Fatalf("RRWebJSON = %q", out.RRWebJSON)
	}
}

func TestTargetURL_AppendsHashQuery(t *testing.T) {
	got, err := TargetURL("http://127.0.0.1:12345", Spec{
		SessionID: "sid-123",
		Query: map[string]string{
			"chat":  "chat-456",
			"embed": "1",
		},
	})
	if err != nil {
		t.Fatalf("TargetURL: %v", err)
	}
	if want := "http://127.0.0.1:12345#/s/sid-123?chat=chat-456&embed=1"; got != want {
		t.Fatalf("TargetURL = %q, want %q", got, want)
	}
}

func TestTargetURL_RouteQueryOverridesHashRoute(t *testing.T) {
	got, err := TargetURL("http://127.0.0.1:12345", Spec{
		SessionID: "sid-123",
		Query: map[string]string{
			"route":         "/s/sid-123/chat",
			"visual_region": "media",
		},
	})
	if err != nil {
		t.Fatalf("TargetURL: %v", err)
	}
	if want := "http://127.0.0.1:12345#/s/sid-123/chat?visual_region=media"; got != want {
		t.Fatalf("TargetURL = %q, want %q", got, want)
	}
}

func TestTargetURL_RouteQueryMustBeHashRoute(t *testing.T) {
	_, err := TargetURL("http://127.0.0.1:12345", Spec{
		SessionID: "sid-123",
		Query:     map[string]string{"route": "https://example.test/"},
	})
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("TargetURL error = %v, want route validation", err)
	}
}

// TestShot_NoLLMPosture asserts a shot performs NO live harness/agent work:
// the only endpoint the boot/health/capture path hits on the served handler is
// GET / (health), and no agent/LLM-shaped path is ever touched. The
// determinism is the served handler's (built no-LLM by the caller); this guards
// the seam from sneaking in a live call of its own.
func TestShot_NoLLMPosture(t *testing.T) {
	h := &noLLMHandler{}
	inv := &stubInvoker{}

	_, err := Shot(context.Background(), Spec{StoryPath: "stories/bugfix"}, Options{
		Server:  &HandlerServer{Handler: h, HealthTimeout: 5 * time.Second},
		Browser: inv,
	})
	if err != nil {
		t.Fatalf("Shot: %v", err)
	}
	if got := atomic.LoadInt32(&h.agentHits); got != 0 {
		t.Errorf("shot hit an agent/LLM endpoint %d time(s); want 0 (last non-root path %q)", got, h.lastNonRoot)
	}
	if got := atomic.LoadInt32(&h.rootHits); got == 0 {
		t.Errorf("health poll never hit GET / (rootHits=0)")
	}
	// The stub invoker never calls back into the server (it writes a synthetic
	// PNG), so the only server traffic a shot generates is the health poll —
	// proving the shot drives no live harness turn.
	if h.lastNonRoot != "" {
		t.Errorf("shot hit a non-root server path %q; a shot should only health-poll", h.lastNonRoot)
	}
}

// TestTargetURL_SpecAndLiveForms locks the URL construction: the spec form
// lands on the SPA home hash route, the live form on the session hash route.
func TestTargetURL_SpecAndLiveForms(t *testing.T) {
	base := "http://127.0.0.1:54321"
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"spec form -> home", Spec{StoryPath: "stories/bugfix", State: "triage"}, base + "#/"},
		{"live form -> session", Spec{SessionID: "abc-123"}, base + "#/s/abc-123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := TargetURL(base, tc.spec)
			if err != nil {
				t.Fatalf("TargetURL: %v", err)
			}
			if got != tc.want {
				t.Errorf("TargetURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestShot_TearsDownServer proves the boot server is shut down once the shot
// returns: a second listener can bind the freed port shape and, more directly,
// the served handler stops answering. We assert teardown by checking the base
// URL the server reported is no longer reachable after Shot returns.
func TestShot_TearsDownServer(t *testing.T) {
	h := &noLLMHandler{}
	// Capture the base URL the HandlerServer chose, via a probe ServerProvider
	// that wraps the real one and records the base before handing it back.
	real := &HandlerServer{Handler: h, HealthTimeout: 5 * time.Second}
	probe := &probeServer{inner: real}

	_, err := Shot(context.Background(), Spec{StoryPath: "stories/bugfix"}, Options{
		Server:  probe,
		Browser: &stubInvoker{},
	})
	if err != nil {
		t.Fatalf("Shot: %v", err)
	}
	if probe.base == "" {
		t.Fatal("probe never observed a base URL")
	}
	// After Shot returns, the server must be torn down: GET / should fail.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	// Give the async Shutdown a beat.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(probe.base + "/")
		if err != nil {
			return // unreachable == torn down: pass
		}
		_ = resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("server at %s still reachable after Shot returned (not torn down)", probe.base)
}

// probeServer wraps a ServerProvider and records the base URL it returns so a
// test can probe teardown without reaching into HandlerServer internals.
type probeServer struct {
	inner ServerProvider
	base  string
}

func (p *probeServer) Serve(ctx context.Context) (string, func(), error) {
	base, stop, err := p.inner.Serve(ctx)
	p.base = base
	return base, stop, err
}

// TestShot_RejectsAmbiguousSpec proves the "exactly one source" rule.
func TestShot_RejectsAmbiguousSpec(t *testing.T) {
	cases := []Spec{
		{}, // neither
		{StoryPath: "stories/bugfix", SessionID: "x"}, // both
	}
	for _, spec := range cases {
		if _, err := Shot(context.Background(), spec, Options{
			Server:  &HandlerServer{Handler: &noLLMHandler{}},
			Browser: &stubInvoker{},
		}); err == nil {
			t.Errorf("Shot(%+v) = nil error, want validation error", spec)
		}
	}
}

// TestShot_RequiresSeams proves Shot does not silently fall back to the real
// network/Chromium seams: a missing Server or Browser is a loud error, not a
// browser launch.
func TestShot_RequiresSeams(t *testing.T) {
	if _, err := Shot(context.Background(), Spec{StoryPath: "x"}, Options{Browser: &stubInvoker{}}); err == nil {
		t.Error("Shot without Server = nil error, want required-seam error")
	}
	if _, err := Shot(context.Background(), Spec{StoryPath: "x"}, Options{Server: &HandlerServer{Handler: &noLLMHandler{}}}); err == nil {
		t.Error("Shot without Browser = nil error, want required-seam error")
	}
}

// TestNodeInvoker_BuildsWebShotArgv asserts the default invoker shells the
// maintained web-shot.ts with the expected argv (no Node required: the runner
// is stubbed). Guards the contract with the TS helper's CLI.
func TestNodeInvoker_BuildsWebShotArgv(t *testing.T) {
	rec := &recordingRunner{}
	inv := &NodeInvoker{RepoRoot: "/repo", Runner: rec}
	err := inv.Capture(context.Background(), CaptureRequest{
		URL:      "http://127.0.0.1:9/#/",
		OutPath:  "/tmp/out.png",
		Viewport: Viewport{Width: 1600, Height: 900},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if rec.dir != filepath.Join("/repo", "tools", "runstatus") {
		t.Errorf("cwd = %q, want /repo/tools/runstatus", rec.dir)
	}
	if rec.name != "pnpm" {
		t.Errorf("command = %q, want pnpm", rec.name)
	}
	joined := strings.Join(rec.args, " ")
	for _, want := range []string{
		"web-shot.ts",
		"--url http://127.0.0.1:9/#/",
		"--out /tmp/out.png",
		"--viewport 1600x900",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

func TestNodeInvoker_BuildsSemanticAndRRWebOutArgv(t *testing.T) {
	rec := &recordingRunner{}
	inv := &NodeInvoker{RepoRoot: "/repo", Runner: rec}
	err := inv.Capture(context.Background(), CaptureRequest{
		URL:             "http://127.0.0.1:9/#/",
		OutPath:         "/tmp/out.png",
		SemanticOutPath: "/tmp/semantic.json",
		RRWebOutPath:    "/tmp/session.rrweb.json",
		Viewport:        Viewport{Width: 1600, Height: 900},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	joined := strings.Join(rec.args, " ")
	if !strings.Contains(joined, "--semantic-out /tmp/semantic.json") {
		t.Errorf("argv %q missing --semantic-out", joined)
	}
	if !strings.Contains(joined, "--rrweb-out /tmp/session.rrweb.json") {
		t.Errorf("argv %q missing --rrweb-out", joined)
	}
}

func TestNodeInvoker_BuildsAssertTextArgv(t *testing.T) {
	rec := &recordingRunner{}
	inv := &NodeInvoker{RepoRoot: "/repo", Runner: rec}
	err := inv.Capture(context.Background(), CaptureRequest{
		URL:        "http://127.0.0.1:9/#/",
		OutPath:    "/tmp/out.png",
		Viewport:   Viewport{Width: 1600, Height: 900},
		AssertText: []string{"Active work", "May I edit README.md?"},
	})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	joined := strings.Join(rec.args, " ")
	for _, want := range []string{
		"--assert-text Active work",
		"--assert-text May I edit README.md?",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

type recordingRunner struct {
	dir, name string
	args      []string
}

func (r *recordingRunner) Run(_ context.Context, dir, name string, args ...string) error {
	r.dir, r.name, r.args = dir, name, args
	return nil
}

// ── GATED end-to-end (real browser) ─────────────────────────────────────────

// TestShot_E2E exercises the REAL browser path through the maintained
// web-shot.ts against a real httptest-served page. It is GATED behind
// KITSOKI_WEBSHOT_E2E=1 because Chromium + the tools/runstatus node_modules may
// be absent in the default test env (CLAUDE.md: the default suite must use no
// browser and no LLM). It is never part of the default suite.
//
// Run it explicitly from a checkout with the helper installed:
//
//	cd tools/runstatus && pnpm install   # once
//	KITSOKI_WEBSHOT_E2E=1 go test ./internal/webshot/...
func TestShot_E2E(t *testing.T) {
	if os.Getenv("KITSOKI_WEBSHOT_E2E") != "1" {
		t.Skip("gated: set KITSOKI_WEBSHOT_E2E=1 (and install tools/runstatus deps + Chromium) to run the real browser capture")
	}
	repoRoot := findRepoRoot(t)
	// Serve a trivial page so the capture has something deterministic to shoot
	// (the SPA may be unbuilt — internal/runstatus/web.IndexHTML returns
	// ErrNotBuilt — so we do not depend on a built bundle here).
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<!doctype html><title>webshot e2e</title><body style='background:#0b1220'>"))
	}))
	defer ts.Close()

	out, err := Shot(context.Background(), Spec{SessionID: "e2e"}, Options{
		Server:  &fixedBaseServer{base: ts.URL},
		Browser: &NodeInvoker{RepoRoot: repoRoot},
		// 60s: a cold pnpm/tsx + Chromium launch is slow.
		HealthTimeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Shot (e2e): %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode e2e PNG: %v", err)
	}
	if img.Bounds().Dx() != DefaultViewport.Width || img.Bounds().Dy() != DefaultViewport.Height {
		t.Errorf("e2e PNG = %dx%d, want %dx%d", img.Bounds().Dx(), img.Bounds().Dy(), DefaultViewport.Width, DefaultViewport.Height)
	}
}

// fixedBaseServer is a ServerProvider that points at an already-running base URL
// (used by the gated E2E to avoid the package-main web handler).
type fixedBaseServer struct{ base string }

func (f *fixedBaseServer) Serve(context.Context) (string, func(), error) {
	return f.base, func() {}, nil
}

// findRepoRoot walks up from the test's cwd to the module root (the dir holding
// go.mod). Used only by the gated E2E to locate tools/runstatus/web-shot.ts.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod above %s", dir)
		}
		dir = parent
	}
}
