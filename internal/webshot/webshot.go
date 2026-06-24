// Package webshot is the reusable headless web->PNG seam: it hands a caller a
// PNG of the REAL kitsoki web SPA for any state — the web twin of `kitsoki shot`
// (which rasterises the terminal Frame, internal/tui/shot).
//
// The hard parts were already proven by the skills' Playwright harness — boot a
// `kitsoki web` no-LLM, drive it to a state, screenshot it deterministically
// (tools/runstatus/tests/playwright/_helpers/server.ts + demo.ts). This package
// captures that as a VALUE rather than leaving it inside a `.spec.ts`: one
// (story, state, world) — or a live session reference — in, one PNG out.
//
//	(story, state, world)  ──▶  headless kitsoki web  ──▶  headless browser  ──▶  PNG
//	   or: a live session         (--flow/--host-cassette,     (web-shot.ts,
//	       (store key + id)         no LLM, :ephemeral addr)     page.screenshot)
//
// # Dependency injection
//
// The two heavy dependencies are seams so the unit tests stay green with NO
// browser and NO server:
//
//   - [ServerProvider] ensures a kitsoki web is serving the target state and
//     returns its base URL + a teardown. The default [HandlerServer] serves an
//     http.Handler the CALLER builds in the deterministic flow/cassette posture
//     (cmd/kitsoki builds it via the same plumbing `kitsoki tour` uses) on an
//     OS-chosen localhost port — so this internal package never imports the
//     package-main SessionRegistry, and the no-LLM posture is the caller's to
//     guarantee. internal/tour.Run proves the listener/health pattern reused here.
//   - [BrowserInvoker] rasterises a served URL to a PNG. The default
//     [NodeInvoker] shells the maintained tools/runstatus/web-shot.ts; tests
//     inject a stub so no Chromium is needed.
//
// # No-LLM posture
//
// A shot never constructs or calls a harness/agent: it only serves a handler
// (built no-LLM by the caller) and screenshots it. The determinism is a function
// of (story, state, world, viewport). See the package tests for the structural
// assertion that a shot performs no live harness/agent work.
package webshot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DefaultViewport is the fixed capture/render viewport every shot uses — the
// 1600x900 the skills' DEMO_VIEWPORT uses (tools/runstatus/.../demo.ts). The
// capture viewport equals the render viewport (the rrweb invariant): a PNG taken
// at a different size than the page rendered at would not be the SPA a human sees.
var DefaultViewport = Viewport{Width: 1600, Height: 900}

// Viewport is the fixed browser viewport (== capture size) for a shot.
type Viewport struct {
	Width  int
	Height int
}

// String formats the viewport as the WxH the web-shot.ts CLI accepts.
func (v Viewport) String() string { return fmt.Sprintf("%dx%d", v.Width, v.Height) }

// Spec selects WHICH state to screenshot. It is one of two forms:
//
//   - Spec form: an explicit (StoryPath, State, World) with no live session. The
//     caller boots a fresh flow-driven kitsoki web that reaches the state
//     deterministically (Open question 3 in the proposal: reaching an arbitrary
//     state is driven via a flow/replay, not a "start at state X" entrypoint —
//     deferred; for now the shot photographs the served home/session surface).
//   - Live form: SessionID names an already-running session reachable on the
//     served handler (the studio's slice-7 handle drives it); the shot
//     photographs that session's current web view.
//
// Exactly one of StoryPath / SessionID is set. Viewport defaults to
// [DefaultViewport] when zero.
type Spec struct {
	// StoryPath is the story whose state to shoot (spec form). Mutually
	// exclusive with SessionID.
	StoryPath string
	// State is the target room/state name (spec form; advisory — see the note
	// on the Spec form above).
	State string
	// World seeds story world variables for the spec form (advisory).
	World map[string]any

	// SessionID names a live session already running on the served handler
	// (live form). Mutually exclusive with StoryPath.
	SessionID string

	// Viewport overrides [DefaultViewport] when non-zero.
	Viewport Viewport

	// Query appends SPA query parameters to the hash route. It is intentionally
	// route-agnostic so callers can deep-link browser affordances like focused
	// chat context without teaching webshot about those features.
	Query map[string]string

	// AssertText, when non-empty, asks the browser helper to verify each string
	// appears in the rendered document before it screenshots. This keeps MCP
	// render smokes honest without making the Go side parse pixels.
	AssertText []string
}

// live reports whether the spec is the live-session form.
func (s Spec) live() bool { return s.SessionID != "" }

// validate enforces the "exactly one source" rule.
func (s Spec) validate() error {
	switch {
	case s.StoryPath == "" && s.SessionID == "":
		return errors.New("webshot: Spec needs either StoryPath (spec form) or SessionID (live form)")
	case s.StoryPath != "" && s.SessionID != "":
		return errors.New("webshot: Spec sets both StoryPath and SessionID; pick one")
	default:
		return nil
	}
}

// viewport resolves the effective viewport (default when unset).
func (s Spec) viewport() Viewport {
	if s.Viewport.Width == 0 || s.Viewport.Height == 0 {
		return DefaultViewport
	}
	return s.Viewport
}

// ServerProvider ensures a kitsoki web is serving the target state and returns
// its base URL (e.g. "http://127.0.0.1:54321") plus a stop func the caller must
// invoke to tear it down. It is a seam so a shot can EITHER boot a fresh no-LLM
// server ([HandlerServer]) OR point at a live one — and so tests can stub the
// whole network half away.
type ServerProvider interface {
	Serve(ctx context.Context) (base string, stop func(), err error)
}

// CaptureRequest is the browser-invoker's input: the served URL to screenshot,
// the output file path, and the fixed viewport (capture == render).
type CaptureRequest struct {
	URL        string
	OutPath    string
	Viewport   Viewport
	AssertText []string
}

// BrowserInvoker rasterises a served URL to a PNG written at OutPath. The
// default [NodeInvoker] shells tools/runstatus/web-shot.ts; a test injects a
// stub that writes a synthetic PNG so no Chromium is required.
type BrowserInvoker interface {
	Capture(ctx context.Context, req CaptureRequest) error
}

// Options configures a Shot. Both seams are injected; zero values are NOT
// auto-filled here (see [Shot]) so the no-browser/no-server unit tests must
// supply explicit stubs rather than silently falling through to the real ones.
type Options struct {
	// Server ensures (and tears down) a kitsoki web serving the state. Required.
	Server ServerProvider
	// Browser rasterises the served URL to a PNG. Required.
	Browser BrowserInvoker
	// HealthTimeout bounds the boot health-poll inside [HandlerServer]. Unused
	// by a stub Server. Zero falls back to 20s.
	HealthTimeout time.Duration
}

// Shot returns a PNG of the kitsoki web SPA at the state named by spec. It
// resolves the served base URL via opts.Server, constructs the target URL
// (the home surface for a spec form, the session route for the live form),
// rasterises it via opts.Browser, and returns the PNG bytes.
//
// Both opts.Server and opts.Browser are required — Shot does not silently fall
// back to the real network/Chromium seams, so a unit test that forgets to stub
// one fails loudly instead of trying to launch a browser. The CLI
// (cmd/kitsoki/web_shot.go) wires the real [HandlerServer] + [NodeInvoker].
func Shot(ctx context.Context, spec Spec, opts Options) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := spec.validate(); err != nil {
		return nil, err
	}
	if opts.Server == nil {
		return nil, errors.New("webshot: Options.Server is required")
	}
	if opts.Browser == nil {
		return nil, errors.New("webshot: Options.Browser is required")
	}

	base, stop, err := opts.Server.Serve(ctx)
	if err != nil {
		return nil, fmt.Errorf("webshot: serve: %w", err)
	}
	if stop != nil {
		defer stop()
	}

	target, err := TargetURL(base, spec)
	if err != nil {
		return nil, err
	}

	tmp, err := tempPNGPath()
	if err != nil {
		return nil, err
	}
	defer removeFile(tmp)

	if err := opts.Browser.Capture(ctx, CaptureRequest{
		URL:        target,
		OutPath:    tmp,
		Viewport:   spec.viewport(),
		AssertText: spec.AssertText,
	}); err != nil {
		return nil, fmt.Errorf("webshot: capture: %w", err)
	}

	png, err := readFile(tmp)
	if err != nil {
		return nil, fmt.Errorf("webshot: read capture %q: %w", tmp, err)
	}
	if len(png) == 0 {
		return nil, fmt.Errorf("webshot: capture produced an empty file %q", tmp)
	}
	return png, nil
}

// TargetURL builds the SPA URL to screenshot for spec against base. The SPA uses
// hash routing (createWebHashHistory, tools/runstatus/src/router.ts), so the
// live-session surface is base + "#/s/<id>" and the spec form is the home
// surface base + "#/".
func TargetURL(base string, spec Spec) (string, error) {
	if base == "" {
		return "", errors.New("webshot: empty server base URL")
	}
	if err := spec.validate(); err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("webshot: parse base %q: %w", base, err)
	}
	// Hash routing lives in the fragment; build it explicitly so url.Parse does
	// not swallow the SPA route into Path.
	if spec.live() {
		u.Fragment = "/s/" + spec.SessionID
	} else {
		u.Fragment = "/"
	}
	if len(spec.Query) > 0 {
		q := url.Values{}
		for k, v := range spec.Query {
			q.Set(k, v)
		}
		u.Fragment += "?" + q.Encode()
	}
	return u.String(), nil
}

// HandlerServer is the default [ServerProvider]: it serves an injected
// http.Handler on an OS-chosen localhost port and polls it healthy before
// returning. The handler is the no-LLM kitsoki web surface the caller built
// (flow/cassette posture) — exactly the DI pattern internal/tour.Run uses, so
// this package never imports the package-main SessionRegistry and the no-LLM
// guarantee stays the caller's.
type HandlerServer struct {
	// Handler is the SPA + RPC + SSE surface to serve. Required.
	Handler http.Handler
	// HealthTimeout bounds the boot health poll. Zero falls back to 20s.
	HealthTimeout time.Duration
}

// Serve binds Handler on 127.0.0.1:0, waits for GET / to return 200, and returns
// the base URL plus a stop func that shuts the server down.
func (h *HandlerServer) Serve(ctx context.Context) (string, func(), error) {
	if h.Handler == nil {
		return "", nil, errors.New("webshot: HandlerServer.Handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{Handler: h.Handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	stop := func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}

	base := "http://" + ln.Addr().String()
	timeout := h.HealthTimeout
	if timeout == 0 {
		timeout = 20 * time.Second
	}
	if err := waitHealthy(ctx, base, timeout); err != nil {
		stop()
		return "", nil, err
	}
	return base, stop, nil
}

// waitHealthy polls base/ until it returns 200 or the deadline passes. Mirrors
// internal/tour.waitHealthy / the Playwright _helpers waitForHealthy.
func waitHealthy(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("webshot: server not healthy after %s (last: %v)", timeout, lastErr)
}
