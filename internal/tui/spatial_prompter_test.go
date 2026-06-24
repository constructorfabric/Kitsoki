package tui

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// syncBuf is a goroutine-safe bytes.Buffer: the bubbletea renderer writes to the
// program output from its own goroutine while the test reads it, so unguarded
// access is a data race.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// spatialRootProgram builds a real RootModel wired with a real output buffer and
// runs it as a headless program (WITH the real renderer, so tea.Println output
// and the live line both reach the buffer). It returns the running program, the
// captured output buffer, and a stop func.
func spatialRootProgram(t *testing.T) (*tea.Program, *syncBuf, func()) {
	t.Helper()
	out := &syncBuf{}
	// prompt must be a constructed textarea: RootModel.View() calls
	// m.prompt.SetHeight, which panics on a zero-valued textarea.Model. Mirror
	// the production wiring (NewRootModel uses newPromptTextarea()).
	rm := RootModel{
		transcript: newTranscriptModel(120, 40),
		prompt:     newPromptTextarea(),
		width:      120,
		height:     40,
	}
	prog := tea.NewProgram(rm,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(out),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)
	runDone := make(chan struct{})
	go func() { _, _ = prog.Run(); close(runDone) }()
	// Let the message loop spin up so Send doesn't race the goroutine launch.
	time.Sleep(50 * time.Millisecond)
	return prog, out, func() {
		prog.Quit()
		select {
		case <-runDone:
		case <-time.After(2 * time.Second):
			t.Fatal("program did not exit")
		}
	}
}

// TestTUISpatialPrompter_PrintsOSC8LinkAndBlocks is the CapturedIO test the
// proposal/brief mandates (rendering-tests skill): when a spatial ambient is
// requested, the terminal output contains the OSC 8 hyperlink WITH the token
// and the blocking "pointing…" status, and the turn does NOT advance until the
// handoff returns. It touches concurrent I/O (the prompter's AskSpatial blocks
// on the return channel while the program renders), so it asserts on the
// combined captured output.
//
// VERIFIED TO FAIL WITHOUT THE CHANGE: with handleSpatialPoint stubbed to a
// no-op (no AppendBlock of the OSC 8 link + no AppendLive of the status), the
// "OSC 8 escape present" and "token present" and "blocking status present"
// assertions all fail — the captured buffer carries neither the link nor the
// status. Confirmed by reverting handleSpatialPoint's body locally.
func TestTUISpatialPrompter_PrintsOSC8LinkAndBlocks(t *testing.T) {
	prog, out, stop := spatialRootProgram(t)
	defer stop()

	// A real loopback PointServer is started inside AskSpatial; inject a no-op
	// browser opener so the test never launches a real browser, and capture the
	// link the opener was handed (it carries the token + chromeless flag).
	var openedURL string
	p := &TUISpatialPrompter{
		open:   func(u string) error { openedURL = u; return nil },
		listen: func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
	}
	p.Attach(prog)

	type result struct {
		bundle host.VisualAmbient
		err    error
	}
	done := make(chan result, 1)
	go func() {
		b, e := p.AskSpatial(context.Background(), "sid", host.SpatialRequest{Prompt: "point at the run button"})
		done <- result{b, e}
	}()

	// The OSC 8 link + blocking status must surface in the captured output.
	var captured string
	require.Eventually(t, func() bool {
		captured = out.String()
		return strings.Contains(captured, "\x1b]8;;") &&
			strings.Contains(captured, spatialBlockingStatus)
	}, 2*time.Second, 10*time.Millisecond,
		"OSC 8 link + blocking status never reached the terminal output")

	// The link carries the one-time token (the opener was handed the exact URL).
	require.NotEmpty(t, openedURL, "browser opener was never called with the link")
	assert.Contains(t, openedURL, "token=")
	assert.Contains(t, openedURL, "chromeless=1")
	assert.Contains(t, openedURL, "/point?")

	// The captured terminal output's clickable label + the agent's prompt are
	// present (the operator can read what to do even without OSC 8 support).
	ra := NewRenderingAnalyzer(t, captured)
	ra.AssertContains(spatialPointLabel)
	ra.AssertContains("point at the run button")

	// The token in the printed OSC 8 link matches the token in the opened URL —
	// they are the same one-time handoff.
	token := tokenFromURL(t, openedURL)
	require.NotEmpty(t, token)
	assert.Contains(t, captured, token,
		"the OSC 8 link printed to the terminal must carry the same token")

	// The turn has NOT advanced: AskSpatial is still blocked on the return.
	select {
	case r := <-done:
		t.Fatalf("AskSpatial returned before the handoff completed: %+v", r)
	case <-time.After(150 * time.Millisecond):
		// Still blocked — correct.
	}

	// Drive the handoff to completion by POSTing the bundle to the return
	// endpoint, exactly as the chrome-less window does on Send. The return URL is
	// the same base as the opened link.
	returnURL := strings.Replace(openedURL, "/point?", "/point/return?", 1)
	postBundle(t, returnURL, `{"visual":{"point":{"x":7,"y":9},"element":{"selector":"[data-testid=run]","role":"button","text":"Run","bbox":[1,2,3,4]}}}`)

	// Now AskSpatial returns with the operator's bundle.
	select {
	case r := <-done:
		require.NoError(t, r.err)
		assert.Equal(t, 7, r.bundle.Point.X)
		require.NotNil(t, r.bundle.Element)
		assert.Equal(t, "[data-testid=run]", r.bundle.Element.Selector)
	case <-time.After(2 * time.Second):
		t.Fatal("AskSpatial did not return after the handoff completed")
	}
}

// TestTUISpatialPrompter_NoProgramIsUnavailable: with no bound program the
// prompter degrades to unavailable (the turn proceeds text-only) rather than
// blocking.
func TestTUISpatialPrompter_NoProgramIsUnavailable(t *testing.T) {
	p := NewTUISpatialPrompter()
	_, err := p.AskSpatial(context.Background(), "sid", host.SpatialRequest{})
	require.ErrorIs(t, err, errSpatialUnavailable)
}

// TestTUISpatialPrompter_NoListenerIsUnavailable: when no loopback listener can
// be bound (sandboxed/headless), the prompter degrades to unavailable rather
// than hard-blocking forever.
func TestTUISpatialPrompter_NoListenerIsUnavailable(t *testing.T) {
	prog, _, stop := spatialRootProgram(t)
	defer stop()
	p := &TUISpatialPrompter{
		open:   func(string) error { return nil },
		listen: func() (net.Listener, error) { return nil, assertErr{} },
	}
	p.Attach(prog)
	_, err := p.AskSpatial(context.Background(), "sid", host.SpatialRequest{})
	require.ErrorIs(t, err, errSpatialUnavailable)
}

// TestTUISpatialPrompter_CtxCancelUnblocks: a cancelled turn ctx unblocks a
// pending point (the operator never submitted).
func TestTUISpatialPrompter_CtxCancelUnblocks(t *testing.T) {
	prog, _, stop := spatialRootProgram(t)
	defer stop()
	p := &TUISpatialPrompter{
		open:   func(string) error { return nil },
		listen: func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
	}
	p.Attach(prog)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, e := p.AskSpatial(ctx, "sid", host.SpatialRequest{})
		errCh <- e
	}()
	// Let the link paint, then abandon.
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case e := <-errCh:
		require.ErrorIs(t, e, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("AskSpatial did not return on ctx cancel")
	}
}

type assertErr struct{}

func (assertErr) Error() string { return "no listener" }

// tokenFromURL extracts the `token` query value from a /point URL.
func tokenFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Query().Get("token")
}

// postBundle POSTs a JSON body to the return endpoint, as the chrome-less window
// does on Send, and fails the test on a transport error.
func postBundle(t *testing.T, returnURL, body string) {
	t.Helper()
	resp, err := http.Post(returnURL, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
