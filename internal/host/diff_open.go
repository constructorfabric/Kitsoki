// Package host — host.diff.open: review a change in the best available diff
// surface and capture a verdict when (and only when) the surface can produce
// one.
//
// host.diff.open is the front door for "open this change for review, and tell
// me what they decided." It resolves a SURFACE by capability and returns a
// typed result {verdict, reviewed, surface}:
//
//  1. IDE connected (an IDELink in ctx, Connected()==true): reuse the openDiff
//     MCP plumbing (the same call IDEOpenDiffHandler drives) and CAPTURE the
//     operator's accept/reject — returned as {verdict: "accept"|"reject",
//     reviewed: true, surface: "ide"} and recorded as a gate decision.
//  2. No IDE, a system difftool available: shell a BLOCKING subprocess via the
//     host.run machinery (exec.CommandContext) — view-only, returns
//     {verdict: null, reviewed: true, surface: "difftool:<name>"}. No verdict
//     event is emitted; the operator's next intent is the recorded decision.
//  3. Neither: {verdict: null, reviewed: false, surface: "none"} so the room
//     falls back to inline rendering / open_file.
//
// PHASE A (synchronous-await) only: the turn blocks while the operator reviews,
// consistent with a long host.run / the already-awaiting open_diff. The
// responsive turn-suspend/resume seam (Phase B / ide-integration.md #2) is NOT
// cut here.
//
// Two input modes, both supported (mirroring openDiff's native shape and the
// already-applied-edits case):
//   - {path, new_text}            — review proposed content not yet on disk.
//   - {paths: [...], base: "HEAD"} — review already-applied working-tree edits
//     against a base; the difftool shows working-tree-vs-base.
//
// The IDE openDiff accept/reject RETURN shape is CONFIRMED (ide-integration.md
// #1) against the vscode-kitsoki extension's real DiffController
// (tools/vscode-kitsoki/src/ide-diff.ts): {ok, verdict: "accepted"|"rejected"}
// — a real-socket round-trip via tools/vscode-kitsoki/tests/ide-bridge.e2e.test.ts
// (openDiff args: path/new_text/new_text_path/title) and the newer
// tools/vscode-kitsoki/tests/vscode-bugfix-walk.e2e.spec.ts (a real VS Code
// window). parseDiffVerdict also accepts the older text-token acknowledgement
// (FILE_SAVED/DIFF_ACCEPTED/…) documented for Claude Code's own IDE
// integration, for editors that never adopt the structured {verdict} shape.
package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"time"

	"kitsoki/internal/store"
)

// diffSurfaceNone / diffSurfaceIDE are the two fixed surface labels; the
// difftool surface is "difftool:<name>" (e.g. "difftool:code"), built by
// resolveDifftool so the trace records which external tool reviewed the change.
const (
	diffSurfaceNone = "none"
	diffSurfaceIDE  = "ide"
)

// DiffOpenHandler implements host.diff.open. It resolves the best diff surface
// by capability and returns {verdict, reviewed, surface} (see the package doc).
//
// Surface resolution order: connected IDE link → system difftool → none. Only
// the IDE path produces a verdict; the difftool path is view-only and the none
// path means the room must render inline.
func DiffOpenHandler(ctx context.Context, args map[string]any) (Result, error) {
	// 1. IDE connected → reuse the openDiff plumbing and capture the verdict.
	if link := IDELinkFromContext(ctx); link != nil && link.Connected() {
		return diffOpenIDE(ctx, link, args)
	}

	// 2. System difftool available → blocking view-only subprocess.
	if tool := resolveDifftool(args); tool != nil {
		return diffOpenDifftool(ctx, tool)
	}

	// 3. Neither → the room falls back to inline rendering.
	return Result{Data: map[string]any{
		"surface":  diffSurfaceNone,
		"reviewed": false,
		"verdict":  nil,
	}}, nil
}

// diffOpenIDE drives the editor's openDiff tool (the same call
// IDEOpenDiffHandler issues) but — unlike open_diff v1 — AWAITS and CAPTURES
// the operator's accept/reject verdict rather than discarding it. Phase A: the
// turn blocks on the ws response, consistent with the already-awaiting
// open_diff. On success it records the verdict as a gate decision.
func diffOpenIDE(ctx context.Context, link IDELink, args map[string]any) (Result, error) {
	// CONFIRMED arg keys (ide-integration.md #1): {path,new_text,title} for a
	// single proposed-content diff (Mode B — real DiffController support, see
	// ide-diff.ts's openModeB()); {paths,base} is the already-applied-edits
	// Mode A shape reviewing_external sends, now ALSO implemented by the real
	// extension (DiffController.openModeA(), dwf4-ide-mode-a) — one diff tab
	// per changed file (left = content at `base` via `git show`, right = the
	// on-disk file), all sharing ONE collective accept/reject verdict.
	// Mirrors IDEOpenDiffHandler.
	toolArgs := map[string]any{}
	if p, ok := args["path"].(string); ok && p != "" {
		toolArgs["path"] = p
	}
	if nt, ok := args["new_text"].(string); ok {
		toolArgs["new_text"] = nt
	}
	if t, ok := args["title"].(string); ok && t != "" {
		toolArgs["title"] = t
	}
	if paths, ok := args["paths"]; ok && paths != nil {
		toolArgs["paths"] = paths
	}
	if base, ok := args["base"].(string); ok && base != "" {
		toolArgs["base"] = base
	}

	raw, err := link.CallTool(ctx, "openDiff", toolArgs)
	if err != nil {
		if isIDENotConnected(err) {
			// Link dropped between the connectivity check and the call: degrade to
			// the not-reviewed surface so the room falls back, never an infra error.
			return Result{Data: map[string]any{
				"surface":  diffSurfaceNone,
				"reviewed": false,
				"verdict":  nil,
			}}, nil
		}
		return Result{}, err
	}
	payload, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}

	verdict := parseDiffVerdict(payload, text)
	data := map[string]any{
		"surface":  diffSurfaceIDE,
		"reviewed": true,
		"verdict":  verdict, // "accept" | "reject" | nil (editor returned neither)
	}
	if verdict != nil {
		emitDiffGateDecided(ctx, diffSurfaceIDE, verdict.(string), toolArgs)
	}
	return Result{Data: data}, nil
}

// parseDiffVerdict extracts the operator's accept/reject from the editor's
// openDiff result. It returns "accept", "reject", or nil (the editor returned
// neither — e.g. the tab was closed without a decision).
//
// ide-integration.md #1: the real openDiff RETURN shape is CONFIRMED (see
// diffOpenIDE's doc comment) — this is the contract the stub server is coded
// to, matching the real vscode-kitsoki DiffController. It accepts, in order:
//   - a structured payload {"verdict": "accept"|"reject"} (or {"accepted":bool}),
//   - else a text token: a body containing FILE_SAVED / DIFF_ACCEPTED / ACCEPT →
//     accept; DIFF_REJECTED / REJECT → reject (Claude Code's documented
//     openDiff acknowledgement tokens).
func parseDiffVerdict(payload any, text string) any {
	if m, ok := payload.(map[string]any); ok {
		if v, ok := m["verdict"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "accept", "accepted":
				return "accept"
			case "reject", "rejected":
				return "reject"
			}
		}
		if acc, ok := m["accepted"].(bool); ok {
			if acc {
				return "accept"
			}
			return "reject"
		}
	}
	upper := strings.ToUpper(text)
	switch {
	case strings.Contains(upper, "FILE_SAVED"), strings.Contains(upper, "DIFF_ACCEPTED"), strings.Contains(upper, "ACCEPT"):
		return "accept"
	case strings.Contains(upper, "DIFF_REJECTED"), strings.Contains(upper, "REJECT"):
		return "reject"
	}
	return nil
}

// emitDiffGateDecided records the IDE verdict as a gate_decided-shaped event on
// the EventSink in ctx — the moat: a recorded, reconstructable decision pinning
// the surface, the verdict, and the diff identity (paths / title). It mirrors
// the orchestrator's GateDecided payload (decider:"human", chosen_intent:the
// verdict) so a runstatus/TUI consumer renders it like any other gate. The
// difftool / none paths emit NO verdict event (recording a fabricated verdict
// for a view-only surface would be a lie in the trace) — only this path, only
// when the editor actually returned a verdict.
//
// Best-effort: a nil sink (flow tests, headless) is a silent no-op, never a
// reason to fail the host call.
func emitDiffGateDecided(ctx context.Context, surface, verdict string, diffArgs map[string]any) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	body := map[string]any{
		"state":             string(oc.StatePath),
		"decider":           "human",
		"chosen_intent":     verdict,
		"available_intents": []string{"accept", "reject"},
		"bailed_to_human":   false,
		"surface":           surface,
		"verdict":           verdict,
		"diff":              diffIdentity(diffArgs),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        time.Now(),
		Kind:      store.GateDecided,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
	})
}

// diffIdentity distils the diff-identifying args (path/paths/title/base) into a
// compact object so a recorded verdict is reconstructable without re-opening
// the editor. Omits empties so the trace stays terse.
func diffIdentity(args map[string]any) map[string]any {
	id := map[string]any{}
	for _, k := range []string{"path", "paths", "title", "base"} {
		if v, ok := args[k]; ok && v != nil && v != "" {
			id[k] = v
		}
	}
	return id
}

// ── Difftool resolution + blocking exec ───────────────────────────────────────

// difftool is a resolved view-only diff surface: a name (for the
// "difftool:<name>" surface label) and the argv to exec. old/new are filled
// per-invocation when the resolver needs the file pair; for git difftool the
// argv is self-contained.
type difftool struct {
	name string
	argv []string
}

// resolveDifftool picks the system difftool to shell, in the order from
// diff-open-fallback.md Open question 2:
//
//  1. $KITSOKI_DIFFTOOL — an explicit argv (space-split), the operator's
//     override; surface "difftool:<argv0-base>".
//  2. `git difftool` — honours `git config diff.tool`; surface "difftool:git".
//     Only chosen when `git` is on PATH (the room is git-shaped: Mode A reviews
//     working-tree-vs-base, which git difftool shows natively).
//  3. `code --wait -d <old> <new>` — when `code` is on PATH; surface
//     "difftool:code". Requires {path, new_text} (Mode B) to have a file pair.
//
// Returns nil when none resolve → surface "none".
//
// NOTE (Phase A scope): the file-pair plumbing for `code --wait -d` (writing
// new_text to a temp file to diff against path) is intentionally minimal — the
// headline review case is Mode A `git difftool` over already-applied edits. A
// nil return is always safe (it degrades to "none").
func resolveDifftool(args map[string]any) *difftool {
	if env := strings.TrimSpace(getenvDifftool()); env != "" {
		argv := strings.Fields(env)
		if len(argv) > 0 {
			return &difftool{name: baseName(argv[0]), argv: argv}
		}
	}
	if _, err := lookPath("git"); err == nil {
		argv := []string{"git", "difftool", "--no-prompt"}
		if base, ok := args["base"].(string); ok && base != "" {
			argv = append(argv, base)
		}
		return &difftool{name: "git", argv: argv}
	}
	if _, err := lookPath("code"); err == nil {
		// `code --wait -d <old> <new>` blocks until the diff tab closes.
		if p, ok := args["path"].(string); ok && p != "" {
			return &difftool{name: "code", argv: []string{"code", "--wait", "-d", p, p}}
		}
	}
	return nil
}

// diffOpenDifftool shells the resolved difftool as a BLOCKING subprocess (the
// host.run machinery: exec.CommandContext, which SIGKILLs the child if ctx is
// cancelled). View-only: it returns reviewed:true, verdict:null, and the
// "difftool:<name>" surface. A non-zero exit is NOT a verdict (a difftool
// cannot produce accept/reject) — it is still "reviewed", consistent with the
// epic's "always view-only" lean; only an exec failure (binary vanished) is a
// reason to degrade to not-reviewed.
func diffOpenDifftool(ctx context.Context, tool *difftool) (Result, error) {
	cmd := exec.CommandContext(ctx, tool.argv[0], tool.argv[1:]...)
	if err := cmd.Run(); err != nil {
		// A non-zero exit is an *exit* error — the operator still saw the diff.
		// Only a genuine exec failure (binary not found / not started) means no
		// review happened; degrade to not-reviewed so the room falls back.
		if _, isExit := err.(*exec.ExitError); !isExit {
			return Result{Data: map[string]any{
				"surface":  diffSurfaceNone,
				"reviewed": false,
				"verdict":  nil,
			}}, nil
		}
	}
	return Result{Data: map[string]any{
		"surface":  "difftool:" + tool.name,
		"reviewed": true,
		"verdict":  nil, // view-only: no accept/reject signal, never fabricated
	}}, nil
}

// baseName returns the final path element of p (the program name), used for the
// "difftool:<name>" label when $KITSOKI_DIFFTOOL is an absolute path.
func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// getenvDifftool and lookPath are tiny indirection seams so tests can inject a
// fake difftool ($KITSOKI_DIFFTOOL) and assert resolution without a real git/
// code on PATH. They default to the real os implementations.
var getenvDifftool = func() string { return os.Getenv("KITSOKI_DIFFTOOL") }

var lookPath = exec.LookPath

// SetDiffLookPathForTest swaps the PATH-lookup the difftool resolver uses so a
// test can make `git`/`code` appear absent (the surface-"none" case) without
// depending on the host's real PATH. It returns a restore func; defer it. Test
// seam only — production always uses exec.LookPath.
func SetDiffLookPathForTest(fn func(string) (string, error)) (restore func()) {
	prev := lookPath
	lookPath = fn
	return func() { lookPath = prev }
}
