package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/host"
	"kitsoki/internal/ide"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// commands_ide.go — the `/ide` slash command: connect/disconnect the live
// MCP-over-ws link to the editor and report status. The link substrate is
// internal/ide (slice 1); this file is slice 2's operator surface. Connect
// dials asynchronously (a ws handshake) and reports back via ideConnectDoneMsg;
// disconnect and status are synchronous. Once connected the same *ide.Link is
// pushed onto the orchestrator (SetIDELink) so per-turn host.ide.* dispatch
// resolves it, and the footer chip + ambient-selection capture light up. See
// docs/tui/README.md ("Editor awareness: /ide").

// ideLinkHandle is the slice-2 view of the IDE link: the host.IDELink subset
// the orchestrator/footer/capture read, plus the lifecycle methods the `/ide`
// command drives. *ide.Link is the production implementation; tests inject an
// in-memory fake so the footer chip and ambient-capture paths run without a
// real ws socket. Keeping it an interface (rather than the concrete *ide.Link)
// is the seam that makes those paths fast-testable.
type ideLinkHandle interface {
	host.IDELink
	Candidates(ctx context.Context) ([]ide.Lock, error)
	ConnectLock(ctx context.Context, lock ide.Lock) (ide.LinkInfo, error)
	Close() error
}

// ideConnectDoneMsg carries the result of an async `/ide connect` dial back to
// Update. link is the freshly-dialed handle on success (nil on failure); err
// distinguishes "no editor found" (ide.ErrNoIDE) from a dial failure so the
// transcript can phrase it precisely.
type ideConnectDoneMsg struct {
	link ideLinkHandle
	info ide.LinkInfo
	err  error
}

// handleIDESlash dispatches the `/ide [subcommand]` family. Bare `/ide`
// connects when off and shows status when already connected (the convenience
// alias). connect/disconnect/status are explicit.
func (m RootModel) handleIDESlash(args []string) (tea.Model, tea.Cmd) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "":
		// Bare /ide: connect if off, else show status. There is no args[0]
		// here (sub == "" only when args is empty), so pass no connect args —
		// slicing args[1:] on the empty slice would panic ([1:0]).
		if m.ideConnected() {
			m.transcript.AppendBlock(m.renderIDEStatusBlock())
			return m, nil
		}
		return m.ideConnect(nil)
	case "connect":
		return m.ideConnect(args[1:])
	case "disconnect":
		return m.ideDisconnect()
	case "status":
		m.transcript.AppendBlock(m.renderIDEStatusBlock())
		return m, nil
	default:
		m.transcript.AppendBlock(m.ideBlock(
			fmt.Sprintf("unknown subcommand %q — try /ide [connect|disconnect|status]", sub)))
		return m, nil
	}
}

// ideConnect discovers candidate lock files and dials. When exactly one matches
// (or an arg selects one) it dials asynchronously; when several match and none
// is selected it prints a picker so the operator can re-run `/ide connect <n>`.
func (m RootModel) ideConnect(args []string) (tea.Model, tea.Cmd) {
	if m.ideConnected() {
		m.transcript.AppendBlock(m.ideBlock(
			fmt.Sprintf("already connected to %s", m.ideLink.IDEName())))
		return m, nil
	}

	cwd, _ := os.Getwd()
	link := m.ideLink
	if link == nil {
		link = ide.NewLink(cwd, nil)
	}
	m.ideLink = link

	candidates, err := link.Candidates(context.Background())
	if err != nil {
		m.transcript.AppendBlock(m.ideBlock(fmt.Sprintf("discovery failed: %v", err)))
		return m, nil
	}
	if len(candidates) == 0 {
		m.transcript.AppendBlock(m.ideBlock("no editor found — open this workspace in VS Code (or a fork) and retry"))
		return m, nil
	}

	// Picker: when several lock files match and the operator did not pick
	// one, show the numbered list and dial nothing yet.
	if len(candidates) > 1 {
		if len(args) == 0 {
			m.transcript.AppendBlock(m.renderIDEPickerBlock(candidates))
			return m, nil
		}
		idx, perr := parsePickIndex(args[0], len(candidates))
		if perr != nil {
			m.transcript.AppendBlock(m.ideBlock(perr.Error()))
			return m, nil
		}
		return m.ideDialAsync(link, candidates[idx])
	}

	// Exactly one candidate — dial it.
	return m.ideDialAsync(link, candidates[0])
}

// ideDialAsync stores the link on the model (so a redial reuses it) and returns
// a tea.Cmd that dials off the UI goroutine, reporting back via
// ideConnectDoneMsg. The handshake is a real ws round-trip; doing it inline
// would block the render loop.
func (m RootModel) ideDialAsync(link ideLinkHandle, lock ide.Lock) (tea.Model, tea.Cmd) {
	m.ideLink = link
	m.transcript.AppendBlock(m.ideBlock(
		fmt.Sprintf("connecting to %s (port %d)…", displayIDEName(lock.IDEName), lock.Port)))
	cmd := func() tea.Msg {
		info, err := link.ConnectLock(context.Background(), lock)
		return ideConnectDoneMsg{link: link, info: info, err: err}
	}
	return m, cmd
}

// handleIDEConnectDone finalizes an async dial: on success it pushes the link
// onto the orchestrator (so host.ide.* dispatch and the ide.connected world key
// resolve it) and prints the connected status block; on failure it reports the
// error and drops the half-open link.
func (m RootModel) handleIDEConnectDone(msg ideConnectDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// A failed dial leaves no usable link; drop it so the footer stays off.
		if m.ideLink != nil {
			_ = m.ideLink.Close()
			m.ideLink = nil
		}
		m.orch.SetIDELink(nil)
		if errors.Is(msg.err, ide.ErrNoIDE) {
			m.transcript.AppendBlock(m.ideBlock("no editor found — open this workspace in VS Code (or a fork) and retry"))
		} else {
			m.transcript.AppendBlock(m.ideBlock(fmt.Sprintf("connect failed: %v", msg.err)))
		}
		return m, nil
	}
	m.ideLink = msg.link
	// Push the live link onto the orchestrator so dispatchHostCalls injects
	// it (host.WithIDELink) and the inner-claude env scrub engages.
	m.orch.SetIDELink(msg.link)
	m.transcript.AppendBlock(m.renderIDEStatusBlock())
	return m, nil
}

// ideDisconnect closes the link and detaches it from the orchestrator, which
// restores the normal agent subprocess env (the scrub is gated on a connected
// link) and flips the footer chip off.
func (m RootModel) ideDisconnect() (tea.Model, tea.Cmd) {
	if !m.ideConnected() {
		m.transcript.AppendBlock(m.ideBlock("not connected"))
		return m, nil
	}
	name := m.ideLink.IDEName()
	_ = m.ideLink.Close()
	m.ideLink = nil
	// Detach from the orchestrator: IDELinkFromContext(ctx) goes nil, so the
	// env scrub stops and host.ide.* return the not-connected result again.
	m.orch.SetIDELink(nil)
	m.transcript.AppendBlock(m.ideBlock(fmt.Sprintf("disconnected from %s", displayIDEName(name))))
	return m, nil
}

// ideConnected reports whether a live link is held.
func (m RootModel) ideConnected() bool {
	return m.ideLink != nil && m.ideLink.Connected()
}

// captureIDEAmbient reads the operator's live editor context at turn-submit and,
// when present, not deny-ruled, and changed since the last turn it rode, stashes
// it on the model (pendingIDEAmbient) for injection onto the turn ctx and
// appends exactly one settled-line echo. It is a no-op (clears the pending
// value, emits nothing) when the link is off, there is no usable context, the
// active file matches the deny list, or the context is identical to the one
// that already rode a prior turn — so a turn with no *new* editor context
// behaves exactly as a turn with no editor. Returns the updated model.
//
// Two layers of context, in priority order: the active *selection* (highlighted
// text) wins; with nothing selected it falls back to the *active open file* so
// "reference the open doc" works without highlighting (the model gets the path
// and can read it with its own tools). Inject-on-change keeps held context from
// silently re-shaping every follow-up turn: only fresh context feeds the prompt
// and prints an echo.
func (m RootModel) captureIDEAmbient() RootModel {
	m.pendingIDEAmbient = host.IDEAmbient{}

	// Link off: nothing IDE about this turn — reset the tracker and record
	// nothing (a "not connected" event every turn would be pure noise).
	if !m.ideConnected() {
		m.lastIDEAmbient = host.IDEAmbient{}
		return m
	}

	cand, source, reason, detail := m.readIDEContext()
	injected := false
	switch {
	case cand.File == "":
		// Connected but no usable context (nothing selected, no clear active
		// file, or deny-ruled). Reset the change-tracker; `reason` says why.
		m.lastIDEAmbient = host.IDEAmbient{}
	case cand == m.lastIDEAmbient:
		// Unchanged since it last rode a turn — don't re-inject or re-echo.
	default:
		m.pendingIDEAmbient = cand
		m.lastIDEAmbient = cand
		injected = true
		// Exactly one settled line per turn carrying *new* editor context,
		// rendered through the inline-routing settled-line path as a clean
		// system line (ideSelectionEcho) with no routing decoration — the echo
		// is the operator's source of truth for what rode the turn.
		ir := m.newInlineRouter()
		m.transcript.AppendBlock(ir.ideSelectionEcho(ideAmbientEcho(cand)))
	}

	// Record the capture in the session trace (always, while connected) so
	// what the editor link surfaced for this turn — including when nothing
	// rode — is auditable like any other decision input. Best-effort.
	m.orch.RecordIDEContext(context.Background(), m.sid, orchestrator.IDECaptureRecord{
		Connected: true,
		Source:    source,
		File:      cand.File,
		Lines:     cand.Lines,
		Range:     cand.Range,
		Injected:  injected,
		Reason:    reason,
		Detail:    detail,
	})
	return m
}

// readIDEContext resolves the editor context for this turn in priority order:
//
//  1. A live selection (get_selection with highlighted text) — source "selection".
//  2. The active text editor's file with no highlight (get_selection returns the
//     focused file but empty text) — source "active_editor". This is the most
//     reliable "the open doc" signal: it names the one editor the cursor is in,
//     unambiguous even with many tabs open.
//  3. The active open tab (get_open_editors) — the fallback when the editor
//     reports no active text editor (e.g. focus is in the terminal). Ambiguous
//     when several tabs are open and none is flagged active.
//
// Returns the ambient plus a source tag and, when nothing usable was found, a
// reason and the raw getOpenEditors envelope as diagnostic detail for the trace.
func (m RootModel) readIDEContext() (host.IDEAmbient, string, string, string) {
	if sel := m.readIDESelection(); sel.File != "" {
		if sel.Selection != "" {
			return sel, "selection", "", ""
		}
		// File but no highlight: the focused document itself rides (path only).
		return host.IDEAmbient{File: sel.File}, "active_editor", "", ""
	}
	amb, reason, detail := m.readActiveEditor()
	if amb.File != "" {
		return amb, "active_editor", "", ""
	}
	return host.IDEAmbient{}, "none", reason, detail
}

// ideAmbientEcho renders the settled-line echo for what rode the turn: the
// selection line count when text is highlighted, else the focused open file.
func ideAmbientEcho(a host.IDEAmbient) string {
	if a.Selection != "" {
		return fmt.Sprintf("⧉ Selected %d %s from %s", a.Lines, pluralLines(a.Lines), a.File)
	}
	return fmt.Sprintf("⧉ Editor open on %s", a.File)
}

// readActiveEditor reads the operator's focused open file via the slice-1
// host.ide.get_open_editors handler and returns it as a selection-less
// IDEAmbient (File set, Selection empty) plus an empty reason; or the zero
// value with a reason ("not_connected" | "no_open_editors" | "ambiguous_focus"
// | "deny_ruled") describing why nothing usable was found, for the trace. This
// is the no-selection fallback so the open document still feeds the turn — path
// only, no file read (the agent reads it with its own tools if it needs the
// body).
func (m RootModel) readActiveEditor() (host.IDEAmbient, string, string) {
	if !m.ideConnected() {
		return host.IDEAmbient{}, "not_connected", ""
	}
	ctx := host.WithIDELink(context.Background(), m.ideLink)
	res, err := host.IDEGetOpenEditorsHandler(ctx, nil)
	if err != nil || res.Data == nil {
		return host.IDEAmbient{}, "not_connected", ""
	}
	if connected, _ := res.Data["connected"].(bool); !connected {
		return host.IDEAmbient{}, "not_connected", ""
	}
	editors, _ := res.Data["editors"].([]any)
	file, reason := activeEditorFile(editors)
	if file == "" {
		// Detection found no usable path. Capture the raw getOpenEditors
		// envelope (paths/labels — no file body or selection) so an
		// unexpected editor wire-shape is fixable straight from the trace.
		return host.IDEAmbient{}, reason, m.rawOpenEditorsEnvelope()
	}
	if m.ideFileDenied(file) {
		return host.IDEAmbient{}, "deny_ruled", ""
	}
	return host.IDEAmbient{File: file}, "", ""
}

// rawOpenEditorsEnvelope fetches the raw getOpenEditors MCP envelope for
// diagnosis when active-editor detection found nothing. Truncated; best-effort
// (a call error is returned as the detail string).
func (m RootModel) rawOpenEditorsEnvelope() string {
	raw, err := m.ideLink.CallTool(context.Background(), "getOpenEditors", map[string]any{})
	if err != nil {
		return "getOpenEditors call error: " + err.Error()
	}
	s := string(raw)
	const cap = 900
	if len(s) > cap {
		s = s[:cap] + "…(truncated)"
	}
	return s
}

// activeEditorFile returns the path of the editor flagged active; when none is
// flagged but exactly one editor is open it returns that one (the common
// single-doc case). The second return is a reason when no path is chosen:
// "no_open_editors" or "ambiguous_focus" (several editors, none active — we
// don't guess). Editor item fields are read defensively (file/path/uri,
// active/isActive) since the wire shape varies by editor.
func activeEditorFile(editors []any) (string, string) {
	var paths []string
	for _, e := range editors {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		p := editorFilePath(m)
		if p == "" {
			continue
		}
		if isActiveEditor(m) {
			return p, ""
		}
		paths = append(paths, p)
	}
	switch len(paths) {
	case 0:
		return "", "no_open_editors"
	case 1:
		return paths[0], ""
	default:
		return "", "ambiguous_focus"
	}
}

// editorFilePath extracts a filesystem path from an open-editor item, trying the
// common key names and stripping a file:// scheme so the echo and prompt show a
// plain path.
func editorFilePath(m map[string]any) string {
	for _, k := range []string{"fileName", "file", "path", "fsPath", "uri", "fileUrl"} {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimPrefix(s, "file://")
		}
	}
	return ""
}

// isActiveEditor reports whether an open-editor item is the focused tab.
func isActiveEditor(m map[string]any) bool {
	for _, k := range []string{"active", "isActive"} {
		if b, ok := m[k].(bool); ok && b {
			return true
		}
	}
	return false
}

// readIDESelection reads the active text editor through the slice-1
// host.ide.get_selection handler (getCurrentSelection). It returns an IDEAmbient
// with the focused file always set when one is reported — Selection carries the
// highlighted text, or "" when the cursor sits in a file with nothing selected
// (the caller treats a file-with-no-text as the active document). It returns the
// zero value only when there is no active editor at all (link off, not-connected,
// no file reported) or the file is deny-ruled. The handler returns the typed
// not-connected/empty result rather than an error, so this never fails a turn.
func (m RootModel) readIDESelection() host.IDEAmbient {
	if !m.ideConnected() {
		return host.IDEAmbient{}
	}
	ctx := host.WithIDELink(context.Background(), m.ideLink)
	res, err := host.IDEGetSelectionHandler(ctx, nil)
	if err != nil || res.Data == nil {
		return host.IDEAmbient{}
	}
	if connected, _ := res.Data["connected"].(bool); !connected {
		return host.IDEAmbient{}
	}
	file, _ := res.Data["file"].(string)
	text, _ := res.Data["text"].(string)
	if strings.TrimSpace(file) == "" {
		// No active text editor (e.g. focus is in the terminal) — let the
		// caller fall back to the open-tabs probe.
		return host.IDEAmbient{}
	}
	if m.ideFileDenied(file) {
		return host.IDEAmbient{}
	}
	return host.IDEAmbient{
		File:      file,
		Selection: text,
		Lines:     selectionLineCount(text),
		Range:     ideRangeLabel(res.Data["range"]),
	}
}

// ideFileDenied reports whether path matches any deny-list pattern. Each
// pattern is tried as a filepath.Match glob against both the full (cleaned)
// path and its base name, so "*.env" and "/abs/secrets/*" both deny as
// expected. A malformed pattern is treated as non-matching (filepath.Match
// returns an error only on bad syntax, which we ignore rather than denying
// everything).
func (m RootModel) ideFileDenied(path string) bool {
	if len(m.ideDeny) == 0 {
		return false
	}
	clean := filepath.Clean(path)
	base := filepath.Base(clean)
	for _, pat := range m.ideDeny {
		if ok, err := filepath.Match(pat, clean); err == nil && ok {
			return true
		}
		if ok, err := filepath.Match(pat, base); err == nil && ok {
			return true
		}
	}
	return false
}

// selectionLineCount counts the lines a selection spans. An empty trailing
// newline does not add a phantom line; a non-empty single line counts as 1.
func selectionLineCount(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	if n < 1 {
		n = 1
	}
	return n
}

// pluralLines returns "line" or "lines" for the echo's grammar.
func pluralLines(n int) string {
	if n == 1 {
		return "line"
	}
	return "lines"
}

// ideRangeLabel renders the editor selection range (a map[string]any with
// start/end {line,character}) as a compact "L:C-L:C" string for the ambient
// scope's `range` field. Returns "" when the range is absent or unparseable —
// the echo's line count is the authoritative source of truth, the range is a
// best-effort convenience for prompts.
func ideRangeLabel(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	start := positionLabel(m["start"])
	end := positionLabel(m["end"])
	switch {
	case start != "" && end != "":
		return start + "-" + end
	case start != "":
		return start
	default:
		return ""
	}
}

// positionLabel renders one {line,character} position as "line:character".
func positionLabel(raw any) string {
	m, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	line, lok := numField(m["line"])
	ch, _ := numField(m["character"])
	if !lok {
		return ""
	}
	return fmt.Sprintf("%d:%d", line, ch)
}

// numField coerces a JSON number (float64 after json.Unmarshal) or int to int.
func numField(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

// ideBlock renders a one-line `/ide` chat block via the blocks renderer
// (SlashOutput) — no hand-rolled ANSI.
func (m RootModel) ideBlock(line string) string {
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput("ide: " + line)
}

// renderIDEStatusBlock renders the read-only `/ide status` block: connected?,
// ideName, workspace, port.
func (m RootModel) renderIDEStatusBlock() string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	if !m.ideConnected() {
		return r.SlashOutput("ide: off (no editor connected) — /ide connect to attach")
	}
	var sb strings.Builder
	sb.WriteString(r.SlashOutput("ide: connected ✓"))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("    editor:    %s\n", displayIDEName(m.ideLink.IDEName())))
	sb.WriteString(fmt.Sprintf("    workspace: %s\n", emptyDash(m.ideLink.Workspace())))
	sb.WriteString(fmt.Sprintf("    port:      %d", m.ideLink.Port()))
	return sb.String()
}

// renderIDEPickerBlock lists the candidate lock files so the operator can
// re-run `/ide connect <n>`. Best-first order is preserved from Discover.
func (m RootModel) renderIDEPickerBlock(candidates []ide.Lock) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	var sb strings.Builder
	sb.WriteString(r.SlashOutput("ide: several editors match this workspace — pick one with /ide connect <n>"))
	sb.WriteString("\n")
	for i, c := range candidates {
		ws := ""
		if len(c.WorkspaceFolders) > 0 {
			ws = c.WorkspaceFolders[0]
		}
		sb.WriteString(fmt.Sprintf("    %d) %s · port %d · %s\n",
			i, displayIDEName(c.IDEName), c.Port, emptyDash(ws)))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// parsePickIndex parses a picker selection, validating it against the candidate
// count. The selection is 0-indexed to match the printed list.
func parsePickIndex(s string, n int) (int, error) {
	var idx int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &idx); err != nil {
		return 0, fmt.Errorf("not a number: %q", s)
	}
	if idx < 0 || idx >= n {
		return 0, fmt.Errorf("pick 0–%d", n-1)
	}
	return idx, nil
}

// displayIDEName falls back to a generic label when the lock omits ideName so
// the status/echo lines never render an empty editor name.
func displayIDEName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "editor"
	}
	return name
}

// emptyDash renders an em-dash for an empty string so status rows never show a
// blank value.
func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
