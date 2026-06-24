package host

// End-to-end env-hygiene regression (shared decision #1): the agent subprocess
// env must, when kitsoki holds a CONNECTED IDE link, drop CLAUDE_CODE_SSE_PORT
// and force CLAUDE_CODE_AUTO_CONNECT_IDE=false so an inner `claude` (or bash_mcp
// child) does not open its OWN socket to the editor; with NO link the env is
// byte-identical to today.
//
// White-box (package host) so it exercises the real unexported pieces: the
// envScrubIDE helper, the envWithSessionID/envWithKitsokiBinOnPath composers,
// and the IDELinkFromContext gate — assembled in the SAME order the three real
// exec sites use (runClaudeOneShotReal, runClaudeStreamJSON, bash_mcp). We can't
// run claude (cost + the ClaudeRunner stub intercepts before env assembly), so
// we reproduce the exact site composition and capture the resulting cmd.Env. If
// a site's composition ever drifts from this reproduction, update both together.
//
// The link's Connected() state — the gate's load-bearing input — is proven end
// to end against a REAL *ide.Link in internal/ide/e2e_test.go
// (TestE2E_EnvHygiene_GateDrivenByRealLink); here we pin the byte-level scrub.

import (
	"context"
	"encoding/json"
	"testing"
)

// envGateLink is a tiny host.IDELink whose Connected() state we flip to drive
// the env-scrub gate. It never opens a socket (CallTool is unused on this path).
type envGateLink struct{ connected bool }

// compile-time proof envGateLink satisfies the gate's host.IDELink boundary.
var _ IDELink = (*envGateLink)(nil)

func (l *envGateLink) CallTool(context.Context, string, map[string]any) (json.RawMessage, error) {
	return nil, nil
}
func (l *envGateLink) Connected() bool   { return l.connected }
func (l *envGateLink) IDEName() string   { return "Stub Code" }
func (l *envGateLink) Workspace() string { return "/ws" }
func (l *envGateLink) Port() int         { return 4242 }

// composeAgentEnv reproduces the EXACT env assembly the real exec sites use:
//
//	envScrubIDE-gated( envWithSessionID( envWithKitsokiBinOnPath(base), sid ) )
//
// matching runClaudeOneShotReal (agent_runner.go) and runClaudeStreamJSON. The
// scrub is the OUTERMOST wrap (so it sees the port-bearing entry to drop) and is
// gated on a connected link in ctx — byte-for-byte what the sites do.
func composeAgentEnv(ctx context.Context, base []string, sid string) []string {
	env := envWithSessionID(envWithKitsokiBinOnPath(base), sid)
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		env = envScrubIDE(env)
	}
	return env
}

// composeBashMCPEnv reproduces the bash_mcp exec site: base + extraEnv, then the
// same ctx-gated scrub as the outermost wrap.
func composeBashMCPEnv(ctx context.Context, base, extraEnv []string) []string {
	env := append([]string(nil), base...)
	if len(extraEnv) > 0 {
		env = append(env, extraEnv...)
	}
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		env = envScrubIDE(env)
	}
	return env
}

// TestE2E_AgentEnv_ScrubbedWhenLinkConnected: a connected link in ctx ⇒ the
// composed agent subprocess env has NO CLAUDE_CODE_SSE_PORT and HAS
// CLAUDE_CODE_AUTO_CONNECT_IDE=false, while unrelated/session entries survive.
func TestE2E_AgentEnv_ScrubbedWhenLinkConnected(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"CLAUDE_CODE_SSE_PORT=25118", // the integrated-terminal port seed
		"HOME=/home/u",
	}
	ctx := WithIDELink(context.Background(), &envGateLink{connected: true})

	env := composeAgentEnv(ctx, base, "sess-1")

	if hasKey(env, "CLAUDE_CODE_SSE_PORT") {
		t.Fatalf("connected link: CLAUDE_CODE_SSE_PORT must be dropped, got %v", env)
	}
	if !hasEnv(env, "CLAUDE_CODE_AUTO_CONNECT_IDE=false") {
		t.Fatalf("connected link: AUTO_CONNECT_IDE must be forced false, got %v", env)
	}
	// Session id + unrelated entries are preserved through the scrub.
	if !hasEnv(env, "KITSOKI_SESSION_ID=sess-1") {
		t.Fatalf("session id must survive the scrub, got %v", env)
	}
	if !hasEnv(env, "HOME=/home/u") {
		t.Fatalf("unrelated entries must survive the scrub, got %v", env)
	}
	// Input base slice must not be mutated by the composition.
	if !hasEnv(base, "CLAUDE_CODE_SSE_PORT=25118") {
		t.Fatal("the base env slice must not be mutated")
	}
}

// TestE2E_AgentEnv_UnchangedWhenNoLink: with no link in ctx (headless / flow /
// /ide-not-connected) the composed env is byte-identical to the no-scrub
// baseline — the backward-compat guarantee. The port seed survives untouched and
// no AUTO_CONNECT entry is injected.
func TestE2E_AgentEnv_UnchangedWhenNoLink(t *testing.T) {
	base := []string{"PATH=/usr/bin", "CLAUDE_CODE_SSE_PORT=25118", "HOME=/home/u"}

	withScrubGate := composeAgentEnv(context.Background(), base, "sess-1")
	baselineNoScrub := envWithSessionID(envWithKitsokiBinOnPath(base), "sess-1")

	if len(withScrubGate) != len(baselineNoScrub) {
		t.Fatalf("no-link env length drifted: gated=%d baseline=%d", len(withScrubGate), len(baselineNoScrub))
	}
	for i := range baselineNoScrub {
		if withScrubGate[i] != baselineNoScrub[i] {
			t.Fatalf("no-link env differs at [%d]: %q vs %q", i, withScrubGate[i], baselineNoScrub[i])
		}
	}
	// Specifically: the port seed is still present and AUTO_CONNECT was NOT added.
	if !hasEnv(withScrubGate, "CLAUDE_CODE_SSE_PORT=25118") {
		t.Fatal("no link: the port seed must survive untouched")
	}
	if hasKey(withScrubGate, "CLAUDE_CODE_AUTO_CONNECT_IDE") {
		t.Fatal("no link: AUTO_CONNECT_IDE must not be injected")
	}
}

// TestE2E_AgentEnv_UnchangedWhenLinkDisconnected: a link present in ctx but
// reporting Connected()==false must NOT trigger the scrub (the gate is
// connected-AND-present). This is the "/ide ran but the editor dropped" path.
func TestE2E_AgentEnv_UnchangedWhenLinkDisconnected(t *testing.T) {
	base := []string{"PATH=/usr/bin", "CLAUDE_CODE_SSE_PORT=25118"}
	ctx := WithIDELink(context.Background(), &envGateLink{connected: false})

	env := composeAgentEnv(ctx, base, "")

	if !hasEnv(env, "CLAUDE_CODE_SSE_PORT=25118") {
		t.Fatalf("disconnected link: port seed must survive, got %v", env)
	}
	if hasKey(env, "CLAUDE_CODE_AUTO_CONNECT_IDE") {
		t.Fatalf("disconnected link: AUTO_CONNECT must not be injected, got %v", env)
	}
}

// TestE2E_BashMCPEnv_ScrubbedWhenLinkConnected mirrors the bash_mcp exec site:
// base + extraEnv, scrubbed when a connected link is in ctx, and untouched
// otherwise — proving all three exec sites share one gated-scrub contract.
func TestE2E_BashMCPEnv_ScrubbedWhenLinkConnected(t *testing.T) {
	base := []string{"PATH=/usr/bin", "CLAUDE_CODE_SSE_PORT=25118"}
	extra := []string{"KITSOKI_SANDBOX=1"}

	// Connected: scrubbed, extraEnv preserved.
	ctx := WithIDELink(context.Background(), &envGateLink{connected: true})
	env := composeBashMCPEnv(ctx, base, extra)
	if hasKey(env, "CLAUDE_CODE_SSE_PORT") {
		t.Fatalf("bash_mcp connected: port seed must be dropped, got %v", env)
	}
	if !hasEnv(env, "CLAUDE_CODE_AUTO_CONNECT_IDE=false") {
		t.Fatalf("bash_mcp connected: AUTO_CONNECT must be false, got %v", env)
	}
	if !hasEnv(env, "KITSOKI_SANDBOX=1") {
		t.Fatalf("bash_mcp connected: extraEnv must survive, got %v", env)
	}

	// No link: byte-identical to base+extra.
	plain := composeBashMCPEnv(context.Background(), base, extra)
	if !hasEnv(plain, "CLAUDE_CODE_SSE_PORT=25118") || hasKey(plain, "CLAUDE_CODE_AUTO_CONNECT_IDE") {
		t.Fatalf("bash_mcp no-link env must be unchanged, got %v", plain)
	}
}
