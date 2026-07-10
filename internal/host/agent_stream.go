// Package host — shared streaming helper for all host.agent.* handlers.
//
// Every new verb (decide/ask/task/extract/converse) funnels its claude
// invocation through AgentStreamer so the streaming invariant
// (invariant 3) is upheld by construction rather
// than by per-handler discipline:
//
//   - Every call uses --output-format stream-json ("stream-json everywhere"),
//     so it captures per-invocation token usage and emits live events.
//   - Events are teed to a StreamSink in real time when one is in ctx (the TUI
//     wires one per turn); otherwise they go to slog only. There is no separate
//     buffered-text path.
//   - All five verbs produce a ClaudeRun; handlers extract what they need
//     (text, submitted JSON, etc.) from it using existing parsing helpers.
//
// Usage:
//
//	cr, sessionID, err := AgentStreamer{
//	    Bin:         bin,
//	    CLIArgs:     cliArgs,
//	    Stdin:       rendered,
//	    WorkingDir:  workingDir,
//	}.Run(ctx)
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
)

// AgentStreamer is the shared entry point for every host.agent.* claude
// invocation. It selects between stream-json and buffered-text output based
// on whether a StreamSink is wired into ctx, applies --verbose when needed,
// and returns a unified ClaudeRun + sessionID pair.
//
// CLIArgs must already contain the base flags (-p, --permission-mode, …) but
// MUST NOT include --output-format or --verbose — AgentStreamer adds those
// based on ctx.
//
// Per-call `tools:` (the effect-level override) wins over agent.Tools per
// precedence rule D5. Callers are responsible for resolving
// the effective tool list before calling AgentStreamer; this type only
// handles the transport.
type AgentStreamer struct {
	// Bin is the path to the claude binary (from resolveAgentBin).
	Bin string
	// CLIArgs are the base command-line flags for this call.
	// Must not include --output-format or --verbose.
	//
	// L11 convention: every element must start with '-'. Positional args must
	// not appear here; pass prompt content via Stdin and file paths via
	// --mcp-config / --append-system-prompt / etc. AgentStreamer.Run asserts
	// this invariant at runtime: panics in tests (to surface misconfiguration
	// early), logs a warning in production.
	CLIArgs []string
	// Stdin is the prompt text rendered and stripped of source-color sentinels.
	Stdin string
	// WorkingDir is the cwd for the claude subprocess.
	WorkingDir string
	// SessionID, when non-empty, is injected as KITSOKI_SESSION_ID into the
	// subprocess environment per-subprocess (not via os.Setenv). This is the
	// preferred propagation path for trace continuity; callers obtain the value
	// from extractSessionIDCtx(ctx) or a parent_session_id param.
	SessionID string
	// Sandbox, when non-nil, routes the subprocess through the agent runtime
	// registry and emits agent.runtime.start/end trace events. Nil preserves
	// the historical direct transport path.
	Sandbox *AgentSandboxSpec
}

// Run dispatches the claude invocation. "stream-json everywhere": every agent
// call runs as --output-format stream-json so it captures per-invocation token
// usage and emits live progress events — to slog always, and to a StreamSink
// when one is installed in ctx (the TUI wires one per turn). There is no longer
// a separate buffered-text path; the no-sink case simply has no sink to tee to.
// The ClaudeRunner stub from ctx is honoured so tests don't fork subprocesses.
//
// Returns (run, sessionID, error). sessionID is populated from the stream's
// system.init / result event (empty when the run produced no session id).
// checkCLIArgs asserts that no element of CLIArgs that is not preceded by a
// flag name starts without '-'. Flag values (e.g. the "bypassPermissions" in
// "--permission-mode bypassPermissions") are exempt because they immediately
// follow a "-"-prefixed flag name. Panics in tests to surface misconfiguration
// early; logs slog.Warn in production.
func (s AgentStreamer) checkCLIArgs(ctx context.Context) {
	prevWasFlag := true // treat the implicit "start" as "after a flag"
	for _, a := range s.CLIArgs {
		if strings.HasPrefix(a, "-") {
			// This element is a flag name. If it contains '=' the value is
			// inlined (--flag=value) so the next element is NOT a value.
			prevWasFlag = !strings.Contains(a, "=")
		} else {
			if !prevWasFlag {
				// Not immediately following a flag — this is a positional arg.
				msg := "AgentStreamer.CLIArgs contains a positional argument: " + a + " (must start with '-'; positional args go in Stdin or via named flags)"
				if testing.Testing() {
					panic(msg)
				}
				slog.WarnContext(ctx, msg)
			}
			prevWasFlag = false
		}
	}
}

func (s AgentStreamer) Run(ctx context.Context) (ClaudeRun, string, error) {
	s.checkCLIArgs(ctx)
	// stream-json + --verbose (claude requires --verbose alongside
	// --output-format stream-json in -p mode). emitStreamEvent tees to a sink
	// only when one is installed, so the no-sink case still streams to slog and
	// captures usage. sessionID is threaded so the subprocess inherits
	// KITSOKI_SESSION_ID.
	args := appendClaudeMCPPermissionSettings(ctx, s.CLIArgs)
	args = append(args, "--output-format", "stream-json", "--verbose")
	if s.Sandbox != nil {
		return s.runWithRuntime(ctx, args)
	}
	return runClaudeStreamJSON(ctx, s.Bin, args, s.Stdin, s.WorkingDir, s.SessionID)
}

// appendClaudeMCPPermissionSettings adds a per-dispatch settings overlay that
// pre-authorizes the MCP tools Kitsoki explicitly attached and allowlisted.
// Newer Claude Code builds can still stop a headless `-p` run with
// "requested permissions to use mcp__... but you haven't granted it yet" even
// when --allowedTools and --permission-mode bypassPermissions are present. The
// settings allowlist is the same approval surface "Always allow" writes to
// .claude/settings.local.json, but scoped to this subprocess invocation.
func appendClaudeMCPPermissionSettings(ctx context.Context, cliArgs []string) []string {
	if AgentBackendFromContext(ctx).Name() != "claude" {
		return append([]string(nil), cliArgs...)
	}
	allowed := mcpToolsFromAllowedToolsArgs(cliArgs)
	if len(allowed) == 0 {
		return append([]string(nil), cliArgs...)
	}
	settings := map[string]any{
		"permissions": map[string]any{
			"allow": allowed,
		},
	}
	b, err := json.Marshal(settings)
	if err != nil {
		slog.WarnContext(ctx, "agent.stream: marshal MCP permission settings", "err", err)
		return append([]string(nil), cliArgs...)
	}
	out := append([]string(nil), cliArgs...)
	return append(out, "--settings", string(b))
}

func mcpToolsFromAllowedToolsArgs(cliArgs []string) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i < len(cliArgs); i++ {
		a := cliArgs[i]
		var raw string
		switch {
		case a == "--allowedTools" || a == "--allowed-tools":
			if i+1 >= len(cliArgs) {
				continue
			}
			raw = cliArgs[i+1]
			i++
		case strings.HasPrefix(a, "--allowedTools="):
			raw = strings.TrimPrefix(a, "--allowedTools=")
		case strings.HasPrefix(a, "--allowed-tools="):
			raw = strings.TrimPrefix(a, "--allowed-tools=")
		default:
			continue
		}
		for _, tool := range splitClaudeToolList(raw) {
			if strings.HasPrefix(tool, "mcp__") && !seen[tool] {
				seen[tool] = true
				out = append(out, tool)
			}
		}
	}
	sort.Strings(out)
	return out
}

func splitClaudeToolList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if s := strings.TrimSpace(f); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (s AgentStreamer) runWithRuntime(ctx context.Context, args []string) (ClaudeRun, string, error) {
	backend := AgentBackendFromContext(ctx)
	inv := backend.TranslateInvocation(args, s.Stdin, s.WorkingDir)
	if inv.Cleanup != nil {
		defer inv.Cleanup()
	}
	spec := s.Sandbox.launchSpec(ctx, s.Bin, inv.Args, inv.Stdin, inv.WorkingDir, s.SessionID)
	running, policy, err := agentRuntimeRegistryFrom(ctx).Launch(ctx, spec)
	if err != nil {
		return ClaudeRun{Infra: fmt.Errorf("agent runtime launch: %w", err)}, "", nil
	}
	callID := CallIDFrom(ctx)
	appendAgentRuntimeStartEvent(ctx, callID, policy)
	res, waitErr := running.Wait(ctx)
	appendAgentRuntimeEndEvent(ctx, callID, policy, res, waitErr)
	if waitErr != nil {
		return ClaudeRun{Infra: fmt.Errorf("agent runtime wait: %w", waitErr)}, "", nil
	}
	reply, parsedSID, rawEvs, usage, cost := parseStreamJSONOutput(ctx, res.Stdout)
	if strings.TrimSpace(reply) == "" {
		reply = res.Stdout
	}
	cr := ClaudeRun{
		Stdout:    strings.TrimRight(reply, "\n"),
		Stderr:    res.Stderr,
		ExitCode:  res.ExitCode,
		RawEvents: rawEvs,
		Usage:     usage,
		CostUSD:   cost,
	}
	recordAgentUsage(ctx, usage, cost)
	return cr, parsedSID, nil
}
