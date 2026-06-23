// spatial_prompter.go — the TUI surface's host.SpatialPrompter.
//
// The terminal can't render pixels, so when a dispatched agent wants the
// operator to point at something (host.SpatialPrompterFrom is consulted, and
// AskSpatial blocks the turn — exactly like TUIOperatorPrompter.Ask blocks on a
// forwarded question), the TUI does NOT draw a picker. Instead it:
//
//	AskSpatial(req)                         [turn goroutine, blocks]
//	  ├─ start a transient PointServer (server.NewPointServer) on a loopback
//	  │     port and mint a one-time token for req
//	  ├─ prog.Send(spatialPointMsg)          → Update prints an OSC 8 link to
//	  │     /point?token=…&chromeless=1 + a "pointing… ↗ open in browser" status
//	  ├─ best-effort open the link in the operator's browser (the /open opener)
//	  └─ block on the token's channel until the window POSTs the bundle back
//	       (or ctx cancels), then tear the server down
//
// If no browser/loopback server can be reached (headless/CI), AskSpatial returns
// errSpatialUnavailable and the caller degrades to "proceed text-only" — the
// turn must never hard-block forever (proposal "What we lose, honestly").
//
// Lifecycle mirrors TUIOperatorPrompter exactly: construct with
// NewTUISpatialPrompter BEFORE tea.NewProgram, Attach(prog) once it exists,
// Detach() on teardown. An AskSpatial with no bound program degrades to
// unavailable so the agent proceeds text-only (the headless posture).
package tui

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/server"
)

// spatialPointMsg carries a minted spatial-handoff link into the bubbletea
// program. url is the full /point?token=…&chromeless=1 address; prompt is the
// agent's reason ("point at the control you mean"), shown in the status. doneCh
// is closed by Update once the link + status are painted, so AskSpatial only
// returns control to its blocking select after the operator can actually see the
// link.
type spatialPointMsg struct {
	url    string
	prompt string
	doneCh chan struct{}
}

// spatialClearMsg tells the Update loop to retire the blocking "pointing…"
// status (the bundle came back, the operator abandoned, or the turn cancelled).
type spatialClearMsg struct{}

// browserOpener opens a URL in the operator's browser. Injected so tests never
// launch a real browser; production uses osOpenURL.
type browserOpener func(url string) error

// TUISpatialPrompter implements host.SpatialPrompter for the TUI. Allocate with
// NewTUISpatialPrompter BEFORE the program exists; bind via Attach once it does.
// Until Attach (or after Detach) AskSpatial reports unavailable so the agent
// proceeds text-only.
type TUISpatialPrompter struct {
	// mu guards prog. Held briefly on every AskSpatial / Attach / Detach so a
	// teardown can't race a mid-turn point.
	mu   sync.Mutex
	prog *tea.Program

	// open opens the minted link in the operator's browser. Defaults to
	// osOpenURL; overridable in tests.
	open browserOpener

	// listen binds the transient PointServer's loopback listener. Defaults to a
	// 127.0.0.1:0 net.Listen; overridable in tests (and the seam that lets a
	// CI/headless run force "no browser reachable").
	listen func() (net.Listener, error)
}

// NewTUISpatialPrompter returns an unbound prompter wired to the production
// browser opener + loopback listener. Safe to thread through RootModel before
// tea.NewProgram exists; it surfaces nothing until Attach.
func NewTUISpatialPrompter() *TUISpatialPrompter {
	return &TUISpatialPrompter{
		open:   osOpenURL,
		listen: func() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") },
	}
}

// Attach binds the prompter to prog. After Attach, AskSpatial paints the link
// via prog.Send. Safe to call from any goroutine; passing nil is a no-op.
func (p *TUISpatialPrompter) Attach(prog *tea.Program) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prog = prog
}

// Detach clears the bound program; subsequent AskSpatial calls report
// unavailable. Typically deferred alongside p.Run().
func (p *TUISpatialPrompter) Detach() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prog = nil
}

// errSpatialUnavailable is returned when AskSpatial can't reach a surface (no
// bound program, no loopback listener, no browser). The caller degrades to
// "proceed text-only" — the turn never hard-blocks.
var errSpatialUnavailable = errors.New("tui spatial prompter: spatial input unavailable")

// AskSpatial implements host.SpatialPrompter. It mints a one-time token on a
// transient PointServer, paints an OSC 8 link + blocking status, opens the link
// in the browser, and blocks until the operator submits a bundle or ctx is
// cancelled. The sessionID argument is unused: the TUI hosts a single live
// session.
func (p *TUISpatialPrompter) AskSpatial(ctx context.Context, _ string, req host.SpatialRequest) (host.VisualAmbient, error) {
	p.mu.Lock()
	prog := p.prog
	p.mu.Unlock()
	if prog == nil {
		return host.VisualAmbient{}, errSpatialUnavailable
	}

	// Start a transient loopback PointServer for this one handoff. A failure to
	// bind (sandboxed/headless) degrades to unavailable rather than blocking.
	ln, err := p.listen()
	if err != nil {
		return host.VisualAmbient{}, errSpatialUnavailable
	}
	ps := server.NewPointServer()
	httpSrv := &http.Server{Handler: ps.Handler()}
	go func() { _ = httpSrv.Serve(ln) }()
	// Shutdown (graceful) rather than Close: the return handler delivers the
	// bundle onto the channel BEFORE it finishes writing its HTTP response, so
	// AskSpatial's <-ch unblocks while that response is still in flight. A hard
	// Close here would reset the connection out from under the operator's Send
	// (the browser sees an EOF / connection reset even though the bundle landed).
	// Shutdown drains in-flight requests first; the short ctx bounds a wedged one.
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	token, ch := ps.Mint(req)
	defer ps.Cancel(token)

	link := pointURL(ln.Addr().String(), token)

	// Paint the link + blocking status on the program, and wait until it is
	// actually rendered before opening the browser / blocking — so the operator
	// always has the clickable link in their scrollback even if window.close()
	// never fires.
	painted := make(chan struct{})
	prog.Send(spatialPointMsg{url: link, prompt: req.Prompt, doneCh: painted})
	<-painted

	// Best-effort browser open. A failure is non-fatal: the OSC 8 link is
	// already printed, so the operator can click it themselves.
	if p.open != nil {
		_ = p.open(link)
	}

	select {
	case bundle := <-ch:
		prog.Send(spatialClearMsg{})
		return bundle, nil
	case <-ctx.Done():
		prog.Send(spatialClearMsg{})
		return host.VisualAmbient{}, ctx.Err()
	}
}

// spatialPointLabel is the clickable text the OSC 8 link wraps. Kept as a const
// so the CapturedIO test can assert the link is present without coupling to the
// surrounding prose.
const spatialPointLabel = "↗ Open the pointing window"

// spatialBlockingStatus is the live line shown while the turn blocks on the
// operator pointing. Mirrors the operator-question pending status.
const spatialBlockingStatus = "pointing… ↗ open in browser"

// handleSpatialPoint paints the spatial-handoff link + blocking status. The turn
// is still in flight (the prompter's AskSpatial is blocked on the return
// channel), so we append the clickable OSC 8 link to scrollback (it must persist
// even if the operator never clicks) and show a live "pointing…" status, then
// close msg.doneCh so AskSpatial proceeds to open the browser and block.
//
// The link is appended as a raw block (not via the markdown/SlashOutput
// pipeline) so the OSC 8 escape bytes survive verbatim — a terminal without OSC
// 8 support prints the label as plain text and drops the escapes.
func (m RootModel) handleSpatialPoint(msg spatialPointMsg) (tea.Model, tea.Cmd) {
	prompt := msg.prompt
	if prompt == "" {
		prompt = "The assistant needs you to point at something."
	}
	link := osc8Hyperlink(msg.url, spatialPointLabel)
	m.transcript.AppendBlock("⟳ " + prompt + "\n  " + link)
	// Settle any in-flight live line before the blocking status takes the slot.
	if m.transcript.hasLive() {
		m.transcript.FinalizeLive("")
	}
	m.transcript.AppendLive(spatialBlockingStatus)
	if msg.doneCh != nil {
		close(msg.doneCh)
	}
	return m, nil
}

// handleSpatialClear retires the blocking "pointing…" status once the bundle
// returned (or the operator abandoned / the turn cancelled). The turn itself
// resumes via the normal turn-outcome path — this only clears the live line.
func (m RootModel) handleSpatialClear(_ spatialClearMsg) (tea.Model, tea.Cmd) {
	if m.transcript.hasLive() {
		m.transcript.FinalizeLive("")
	}
	return m, nil
}

// pointURL builds the chrome-less window address. The SPA uses hash routing and
// reads the chromeless flag off the query before the hash, so both ride on the
// same URL the token does.
func pointURL(addr, token string) string {
	q := url.Values{}
	q.Set("token", token)
	q.Set("chromeless", "1")
	return "http://" + addr + "/point?" + q.Encode()
}

// osOpenURL opens a URL in the operator's browser via the same OS opener
// osOpenArtifact uses (open / xdg-open). Unlike artifact opening it does NOT
// consult $EDITOR — a URL belongs in a browser, not a text editor.
func osOpenURL(rawURL string) error {
	return osOpenBrowser(rawURL)
}

// Compile-time assertion that *TUISpatialPrompter satisfies the seam.
var _ host.SpatialPrompter = (*TUISpatialPrompter)(nil)
