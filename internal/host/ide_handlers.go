// Package host — host.ide.* verb handlers (editor awareness over the live
// MCP-over-ws Link).
//
// Each handler resolves the IDE link from ctx (IDELinkFromContext). When no
// link is present — or it reports Connected()==false, or a call fails because
// the socket dropped — the handler returns the typed not-connected Result
// (data.connected==false with each verb's normal output slot present-but-empty)
// and a nil error, so a story can branch on a single `data.connected` field
// rather than special-casing infra errors. Only a genuine infra failure (a
// non-connection error from the link) surfaces as a Go error.
//
// The verb→tool mapping and arg/result shapes are documented in
// docs/architecture/hosts.md ("host.ide.*"). open_diff is a BLOCKING verdict
// gate: the editor suspends the tools/call response until the operator accepts
// or rejects the diff in-editor, so the turn suspends until then; the verdict is
// surfaced as data.verdict for a story to bind and branch on (publish vs refine).
package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"kitsoki/internal/store"
)

// ideNotConnected is the typed Result a host.ide.* handler returns when no IDE
// link is connected. The story branches on data.connected == false; this is an
// expected state, never a Go error. Each verb passes its own empty-shaped slots
// in extra so a `bind:` against e.g. data.diagnostics still resolves (to an
// empty list).
func ideNotConnected(extra map[string]any) Result {
	data := map[string]any{"connected": false}
	for k, v := range extra {
		data[k] = v
	}
	return Result{Data: data}
}

// isIDENotConnected reports whether err is the ide package's not-connected
// sentinel. host must not import internal/ide (that would invert the
// dependency), so it matches by the sentinel's stable message rather than
// errors.Is against ide.ErrNotConnected. A dropped editor is an expected,
// story-branchable state — not an infra error.
func isIDENotConnected(err error) bool {
	return err != nil && strings.Contains(err.Error(), "ide: not connected")
}

// unwrapIDEResult parses the MCP tools/call result envelope (the
// {"content":[…],"isError":…} object Client.CallTool returns) into its
// decision-relevant parts: the parsed payload (content[0].text JSON-decoded
// when it parses as JSON, else the raw text under "text"), and the tool-level
// isError flag. When the envelope has no content the payload is nil.
//
// A tool-level isError==true is surfaced by the caller as Result.Error, never
// a Go error (a failed editor request is a domain-level outcome).
func unwrapIDEResult(raw json.RawMessage) (payload any, text string, isError bool) {
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &env) != nil {
		return nil, "", false
	}
	isError = env.IsError
	for _, c := range env.Content {
		if c.Text != "" {
			text = c.Text
			break
		}
	}
	if text == "" {
		return nil, "", isError
	}
	var parsed any
	if json.Unmarshal([]byte(text), &parsed) == nil {
		return parsed, text, isError
	}
	return nil, text, isError
}

// emitIDEContextCaptured records one host.ide.get_* pull on the EventSink in
// ctx (the same sink agent events use, injected in dispatchHostCalls). It is
// the labeled datapoint behind an editor-informed decision: it pins the IDE
// provenance (verb, request args, workspace, port) and a sha256-prefix digest
// of the raw response so the decision's editor input is auditable WITHOUT
// re-opening the socket — and without leaking the raw selection/diagnostic
// text into the trace (selection-privacy lean). Best-effort: a nil sink (flow
// tests, headless) or a marshal failure is a silent no-op, never a reason to
// fail the host call. Emitted only by the read verbs; open_file/open_diff are
// side-effect-only and carry no captured context.
func emitIDEContextCaptured(ctx context.Context, link IDELink, verb string, request map[string]any, rawResponse json.RawMessage) {
	sink := EventSinkFromAgentCtx(ctx)
	if sink == nil {
		return
	}
	sum := sha256.Sum256(rawResponse)
	body := map[string]any{
		"verb":            verb,
		"request":         request,
		"response_digest": hex.EncodeToString(sum[:])[:16],
		"port":            link.Port(),
		"workspace":       link.Workspace(),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	oc := AgentCallCtxFrom(ctx)
	_ = sink.Append(store.Event{
		Turn:      oc.Turn,
		Ts:        time.Now(),
		Kind:      store.IDEContextCaptured,
		StatePath: oc.StatePath,
		Payload:   json.RawMessage(raw),
	})
}

// IDEGetDiagnosticsHandler implements host.ide.get_diagnostics.
//
// Maps to the editor's getDiagnostics tool. Arg `path` is sent through as
// `path` (CONFIRMED: a real-socket capture against the vscode-kitsoki
// extension's IdeTools.getDiagnostics — tools/vscode-kitsoki/src/ide-tools.ts,
// which reads `args.path` — found the handler was sending `uri` instead, so
// the narrowing arg was silently dropped every call; fixed here. See
// tools/vscode-kitsoki/tests/vscode-bugfix-walk.e2e.spec.ts, whose walk drives
// `validating`'s unconditional `host.ide.get_diagnostics` pull through a real
// VS Code + real IdeServer). Result.Data on success:
// {"diagnostics":[…],"connected":true}; not-connected:
// {"connected":false,"diagnostics":[]}.
func IDEGetDiagnosticsHandler(ctx context.Context, args map[string]any) (Result, error) {
	link := IDELinkFromContext(ctx)
	if link == nil || !link.Connected() {
		return ideNotConnected(map[string]any{"diagnostics": []any{}}), nil
	}

	toolArgs := map[string]any{}
	if p, ok := args["path"].(string); ok && p != "" {
		toolArgs["path"] = p
	}

	raw, err := link.CallTool(ctx, "getDiagnostics", toolArgs)
	if err != nil {
		if isIDENotConnected(err) {
			return ideNotConnected(map[string]any{"diagnostics": []any{}}), nil
		}
		return Result{}, err
	}
	payload, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}
	emitIDEContextCaptured(ctx, link, "get_diagnostics", toolArgs, raw)

	data := map[string]any{"connected": true, "diagnostics": []any{}}
	switch v := payload.(type) {
	case []any:
		// The tool may return a bare diagnostics array.
		data["diagnostics"] = v
	case map[string]any:
		if d, ok := v["diagnostics"]; ok {
			data["diagnostics"] = d
		}
	}
	return Result{Data: data}, nil
}

// IDEGetSelectionHandler implements host.ide.get_selection.
//
// Maps to getCurrentSelection (the LIVE selection, NOT getLatestSelection).
// Result.Data on success: {"file":…,"text":…,"range":{…},"connected":true};
// not-connected: {"connected":false,"file":"","text":"","range":null}.
func IDEGetSelectionHandler(ctx context.Context, args map[string]any) (Result, error) {
	link := IDELinkFromContext(ctx)
	if link == nil || !link.Connected() {
		return ideNotConnected(map[string]any{"file": "", "text": "", "range": nil}), nil
	}

	raw, err := link.CallTool(ctx, "getCurrentSelection", map[string]any{})
	if err != nil {
		if isIDENotConnected(err) {
			return ideNotConnected(map[string]any{"file": "", "text": "", "range": nil}), nil
		}
		return Result{}, err
	}
	payload, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}
	emitIDEContextCaptured(ctx, link, "get_selection", map[string]any{}, raw)

	data := map[string]any{"connected": true, "file": "", "text": "", "range": nil}
	if m, ok := payload.(map[string]any); ok {
		// File path: editors disagree on the key. VS Code's getCurrentSelection
		// returns "filePath"; others use "file"/"fileName", or only a "uri"/
		// "fileUrl" (file://…). Take the first present, stripping the scheme.
		data["file"] = firstStringField(m, "file", "filePath", "fileName", "uri", "fileUrl")
		if v, ok := m["text"]; ok {
			data["text"] = v
		}
		// Range: "range" or VS Code's "selection".
		if v, ok := m["range"]; ok {
			data["range"] = v
		} else if v, ok := m["selection"]; ok {
			data["range"] = v
		}
	}
	return Result{Data: data}, nil
}

// firstStringField returns the first key whose value is a non-empty string,
// stripping a file:// scheme so callers always see a plain path.
func firstStringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimPrefix(s, "file://")
		}
	}
	return ""
}

// IDEGetOpenEditorsHandler implements host.ide.get_open_editors.
//
// Maps to getOpenEditors. Result.Data on success:
// {"editors":[…],"connected":true}; not-connected:
// {"connected":false,"editors":[]}.
func IDEGetOpenEditorsHandler(ctx context.Context, args map[string]any) (Result, error) {
	link := IDELinkFromContext(ctx)
	if link == nil || !link.Connected() {
		return ideNotConnected(map[string]any{"editors": []any{}}), nil
	}

	raw, err := link.CallTool(ctx, "getOpenEditors", map[string]any{})
	if err != nil {
		if isIDENotConnected(err) {
			return ideNotConnected(map[string]any{"editors": []any{}}), nil
		}
		return Result{}, err
	}
	payload, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}
	emitIDEContextCaptured(ctx, link, "get_open_editors", map[string]any{}, raw)

	data := map[string]any{"connected": true, "editors": []any{}}
	switch v := payload.(type) {
	case []any:
		data["editors"] = v
	case map[string]any:
		// The editor may key the list as "editors" or "tabs" (VS Code's
		// TabGroups API returns {"tabs":[…]}). Accept either.
		if e, ok := v["editors"]; ok {
			data["editors"] = e
		} else if tabs, ok := v["tabs"]; ok {
			data["editors"] = tabs
		}
	}
	return Result{Data: data}, nil
}

// IDEOpenFileHandler implements host.ide.open_file.
//
// Maps to openFile. Args: path (required), range (optional, passed through as
// the editor's range object). Result.Data on success:
// {"ok":true,"connected":true}; not-connected:
// {"connected":false,"ok":false}.
func IDEOpenFileHandler(ctx context.Context, args map[string]any) (Result, error) {
	link := IDELinkFromContext(ctx)
	if link == nil || !link.Connected() {
		return ideNotConnected(map[string]any{"ok": false}), nil
	}

	toolArgs := map[string]any{}
	if p, ok := args["path"].(string); ok && p != "" {
		toolArgs["path"] = p
	}
	if r, ok := args["range"]; ok && r != nil {
		toolArgs["range"] = r
	}

	raw, err := link.CallTool(ctx, "openFile", toolArgs)
	if err != nil {
		if isIDENotConnected(err) {
			return ideNotConnected(map[string]any{"ok": false}), nil
		}
		return Result{}, err
	}
	_, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}
	return Result{Data: map[string]any{"connected": true, "ok": true}}, nil
}

// IDEOpenDiffHandler implements host.ide.open_diff.
//
// Maps to openDiff. BLOCKING verdict gate: the editor opens a side-by-side diff
// (the on-disk file vs new_text) and withholds the tools/call response until the
// operator accepts or rejects it in the editor, so this call (and the turn that
// dispatched it) SUSPENDS until then — link.CallTool has no timeout and awaits
// the response. The editor applies new_text on accept. The returned verdict is
// surfaced as data.verdict so a story can `bind:` it and branch (publish on
// accept, re-refine on reject). An editor that returns no verdict (older,
// non-gating) defaults to "accepted" — the v1 proceed-anyway behaviour.
//
// Args: path (required), new_text OR new_text_path (the proposed content inline
// or a staged file the editor reads), title (optional). Result.Data on success:
// {"ok":true,"connected":true,"verdict":"accepted"|"rejected"}; not-connected:
// {"connected":false,"ok":false}.
func IDEOpenDiffHandler(ctx context.Context, args map[string]any) (Result, error) {
	link := IDELinkFromContext(ctx)
	if link == nil || !link.Connected() {
		return ideNotConnected(map[string]any{"ok": false}), nil
	}

	toolArgs := map[string]any{}
	if p, ok := args["path"].(string); ok && p != "" {
		toolArgs["path"] = p
	}
	if nt, ok := args["new_text"].(string); ok {
		toolArgs["new_text"] = nt
	}
	// new_text_path lets the story hand the editor a staged draft FILE instead of
	// inlining a large doc through the MCP envelope; the editor reads it.
	if ntp, ok := args["new_text_path"].(string); ok && ntp != "" {
		toolArgs["new_text_path"] = ntp
	}
	if t, ok := args["title"].(string); ok && t != "" {
		toolArgs["title"] = t
	}

	raw, err := link.CallTool(ctx, "openDiff", toolArgs)
	if err != nil {
		if isIDENotConnected(err) {
			return ideNotConnected(map[string]any{"ok": false}), nil
		}
		return Result{}, err
	}
	payload, text, isError := unwrapIDEResult(raw)
	if isError {
		return Result{Error: text}, nil
	}
	// Surface the accept/reject verdict. Default to "accepted" when the editor
	// returns no verdict so a non-gating editor preserves forward progress.
	verdict := "accepted"
	if m, ok := payload.(map[string]any); ok {
		if v, ok := m["verdict"].(string); ok && v != "" {
			verdict = v
		}
	}
	return Result{Data: map[string]any{"connected": true, "ok": true, "verdict": verdict}}, nil
}
