// Package host — IDE link seam for the host.ide.* verb handlers.
//
// IDE awareness: when the TUI has a live MCP-over-ws connection to the editor
// it injects that connection (an *ide.Link) into ctx as a host.IDELink. The
// host.ide.* handlers (ide_handlers.go) resolve it from ctx to pull editor
// diagnostics/selection/open-editors and to drive open-file/open-diff. When no
// link is in ctx (CLI one-shots, flow tests, headless runs, or the editor is
// disconnected) the handlers return a typed not-connected Result the story
// branches on — never a Go error. The boundary is the host.IDELink interface
// so internal/host never imports internal/ide (ide imports host for
// host.Handler; the dependency must not invert). See
// docs/architecture/transports.md ("The IDE link") and docs/architecture/hosts.md ("host.ide.*").
package host

import (
	"context"
	"encoding/json"
	"strings"
)

// IDELink is the minimal surface host.ide.* handlers need from the live IDE
// connection. *ide.Link satisfies it structurally; host never imports ide.
// A nil IDELink (none in ctx, or one reporting Connected()==false) means
// "no editor" — handlers return the typed not-connected Result, not an error.
type IDELink interface {
	// CallTool issues an MCP tools/call and returns the result envelope
	// (the {"content":[…],"isError":…} object). It returns a connection
	// error (ide.ErrNotConnected, which host treats as connection-failure)
	// when the socket is down.
	CallTool(ctx context.Context, tool string, args map[string]any) (json.RawMessage, error)
	Connected() bool
	IDEName() string
	Workspace() string
	Port() int
}

// ideLinkKey is the unexported context key for the injected IDE link.
type ideLinkKey struct{}

// WithIDELink injects the process IDE link into ctx so host.ide.* handlers can
// resolve it. Passing nil is safe — handlers then return the not-connected
// result, leaving headless/flow-test behavior unchanged. The link is the
// process-lifetime *ide.Link the TUI starts/stops; the orchestrator threads it
// into the host-dispatch context per turn. Mirrors WithPromptRenderer
// (prompt_render.go).
func WithIDELink(ctx context.Context, l IDELink) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ideLinkKey{}, l)
}

// IDELinkFromContext returns the IDE link previously injected with WithIDELink,
// or nil when none was injected (the not-connected path).
func IDELinkFromContext(ctx context.Context) IDELink {
	if v, ok := ctx.Value(ideLinkKey{}).(IDELink); ok {
		return v
	}
	return nil
}

// envScrubIDE removes the IDE auto-connect signals from a subprocess env so an
// inner `claude` (or bash_mcp child) does NOT open its OWN socket to the editor
// while kitsoki holds the one link (epic shared decision #1). It:
//   - drops CLAUDE_CODE_SSE_PORT (the integrated-terminal port seed), and
//   - sets CLAUDE_CODE_AUTO_CONNECT_IDE=false (unsetting the port alone is not
//     enough — the inner claude also rediscovers a link by scanning
//     ~/.claude/ide/*.lock against its cwd, the SAME discovery kitsoki uses).
//
// The returned env is a fresh slice; the input is not mutated.
// ENABLE_IDE_INTEGRATION is intentionally NOT touched — it is stale in the
// verified extension version [wire §7].
//
// Callers gate this on a CONNECTED link in ctx
// (IDELinkFromContext(ctx) != nil && link.Connected()); when no link is present
// it is never called, so the subprocess env is byte-identical to today.
func envScrubIDE(env []string) []string {
	const (
		portKey        = "CLAUDE_CODE_SSE_PORT"
		autoConnectKey = "CLAUDE_CODE_AUTO_CONNECT_IDE"
	)
	out := make([]string, 0, len(env)+1)
	autoConnectSet := false
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, portKey+"="):
			// Drop the integrated-terminal port seed entirely.
			continue
		case strings.HasPrefix(kv, autoConnectKey+"="):
			out = append(out, autoConnectKey+"=false")
			autoConnectSet = true
		default:
			out = append(out, kv)
		}
	}
	if !autoConnectSet {
		out = append(out, autoConnectKey+"=false")
	}
	return out
}
