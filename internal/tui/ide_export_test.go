// ide_export_test.go — white-box helpers + an in-memory IDE link fake for the
// slice-2 `/ide` tests (footer chip, ambient-selection capture). Compiled only
// under `go test` (package tui). The fake satisfies the unexported
// ideLinkHandle so tests in package tui_test can drive the connected paths
// without a real ws socket.
package tui

import (
	"context"
	"encoding/json"

	"kitsoki/internal/host"
	"kitsoki/internal/ide"
)

// FakeIDELink is an in-memory ideLinkHandle for tests: it reports a fixed
// connected state and IDE identity, and answers CallTool from a canned map keyed
// by tool name (the MCP result envelope a real editor would return). It opens no
// socket.
type FakeIDELink struct {
	connected bool
	ideName   string
	workspace string
	port      int
	// toolResults maps a tool name to the raw MCP result envelope returned by
	// CallTool (the {"content":[…],"isError":…} object). Missing names return
	// ErrNotConnected so a test can simulate a drop.
	toolResults map[string]json.RawMessage
}

// NewFakeIDELink builds a connected fake reporting the given identity. Use
// SetSelection to canned the getCurrentSelection response.
func NewFakeIDELink(ideName, workspace string, port int) *FakeIDELink {
	return &FakeIDELink{
		connected:   true,
		ideName:     ideName,
		workspace:   workspace,
		port:        port,
		toolResults: map[string]json.RawMessage{},
	}
}

// SetSelection cans the getCurrentSelection MCP result so captureIDEAmbient
// reads the given file/text. rangeObj is the optional editor range map.
func (f *FakeIDELink) SetSelection(file, text string, rangeObj map[string]any) {
	payload := map[string]any{"file": file, "text": text}
	if rangeObj != nil {
		payload["range"] = rangeObj
	}
	inner, _ := json.Marshal(payload)
	env, _ := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(inner)}},
		"isError": false,
	})
	f.toolResults["getCurrentSelection"] = env
}

// SetOpenEditors cans the getOpenEditors MCP result so readActiveEditor sees the
// given editors. Each entry is an open-editor item (e.g. {"file":…,"active":…}).
func (f *FakeIDELink) SetOpenEditors(editors []map[string]any) {
	list := make([]any, len(editors))
	for i, e := range editors {
		list[i] = e
	}
	inner, _ := json.Marshal(map[string]any{"editors": list})
	env, _ := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(inner)}},
		"isError": false,
	})
	f.toolResults["getOpenEditors"] = env
}

func (f *FakeIDELink) CallTool(_ context.Context, tool string, _ map[string]any) (json.RawMessage, error) {
	if raw, ok := f.toolResults[tool]; ok {
		return raw, nil
	}
	return nil, ide.ErrNotConnected
}

func (f *FakeIDELink) Connected() bool   { return f.connected }
func (f *FakeIDELink) IDEName() string   { return f.ideName }
func (f *FakeIDELink) Workspace() string { return f.workspace }
func (f *FakeIDELink) Port() int         { return f.port }

func (f *FakeIDELink) Candidates(context.Context) ([]ide.Lock, error) { return nil, nil }
func (f *FakeIDELink) ConnectLock(context.Context, ide.Lock) (ide.LinkInfo, error) {
	return ide.LinkInfo{Connected: true, IDEName: f.ideName, Workspace: f.workspace, Port: f.port}, nil
}
func (f *FakeIDELink) Close() error { f.connected = false; return nil }

// compile-time proof the fake satisfies the handle the model holds.
var _ ideLinkHandle = (*FakeIDELink)(nil)

// SetIDELinkForTest installs a fake link on the model and (mirroring the
// production connect path) pushes it onto the orchestrator.
func SetIDELinkForTest(m *RootModel, f *FakeIDELink) {
	if f == nil {
		m.ideLink = nil
		m.orch.SetIDELink(nil)
		return
	}
	m.ideLink = f
	m.orch.SetIDELink(f)
}

// SetIDEDenyForTest seeds the ambient-attach deny list.
func SetIDEDenyForTest(m *RootModel, patterns []string) { m.ideDeny = patterns }

// IDEFooterChipForTest exposes the rendered footer IDE chip.
func IDEFooterChipForTest(m RootModel) string { return ideFooterChip(m) }

// CaptureIDEAmbientForTest runs the at-submit ambient capture and returns the
// updated model (so the test can read the echo from the transcript and the
// pending ambient value).
func CaptureIDEAmbientForTest(m RootModel) RootModel { return m.captureIDEAmbient() }

// PendingIDEAmbientForTest exposes the ambient context captured for the next
// turn.
func PendingIDEAmbientForTest(m RootModel) host.IDEAmbient { return m.pendingIDEAmbient }

// HandleIDESlashForTest dispatches the `/ide` subcommand family and returns the
// updated model.
func HandleIDESlashForTest(m RootModel, args []string) RootModel {
	model, _ := m.handleIDESlash(args)
	rm, _ := model.(RootModel)
	return rm
}
