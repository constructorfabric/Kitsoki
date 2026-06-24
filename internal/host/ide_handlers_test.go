package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/ide"
)

// compile-time proof that *ide.Link satisfies host.IDELink structurally — the
// boundary the orchestrator wires across without host importing ide.
var _ host.IDELink = (*ide.Link)(nil)

// fakeLink is a tiny in-process host.IDELink for the connected-path tests. It
// returns canned MCP result envelopes per tool name and never opens a socket.
type fakeLink struct {
	connected bool
	// results maps a tool name to the raw MCP result envelope CallTool returns.
	results map[string]json.RawMessage
	// err, when set, is returned by CallTool instead of a result (used for the
	// not-connected-mid-call and infra-error cases).
	err error

	lastTool string
	lastArgs map[string]any
}

func (f *fakeLink) CallTool(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	f.lastTool = tool
	f.lastArgs = args
	if f.err != nil {
		return nil, f.err
	}
	return f.results[tool], nil
}
func (f *fakeLink) Connected() bool   { return f.connected }
func (f *fakeLink) IDEName() string   { return "Visual Studio Code" }
func (f *fakeLink) Workspace() string { return "/ws" }
func (f *fakeLink) Port() int         { return 12345 }

// envelope builds an MCP tools/call result envelope wrapping text.
func envelope(text string, isError bool) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	})
	return b
}

// --- Not-connected fallback: nil link and disconnected link both return the
// typed connected:false Result with each verb's empty slot present. ---

func TestIDEHandlers_NotConnected(t *testing.T) {
	cases := []struct {
		name    string
		h       host.Handler
		wantKey string
		wantVal any
	}{
		{"get_diagnostics", host.IDEGetDiagnosticsHandler, "diagnostics", []any{}},
		{"get_selection", host.IDEGetSelectionHandler, "file", ""},
		{"get_open_editors", host.IDEGetOpenEditorsHandler, "editors", []any{}},
		{"open_file", host.IDEOpenFileHandler, "ok", false},
		{"open_diff", host.IDEOpenDiffHandler, "ok", false},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/no-link", func(t *testing.T) {
			res, err := tc.h(context.Background(), nil)
			if err != nil {
				t.Fatalf("nil-link must not error, got %v", err)
			}
			if res.Data["connected"] != false {
				t.Fatalf("connected: want false, got %v", res.Data["connected"])
			}
			if !reflect.DeepEqual(res.Data[tc.wantKey], tc.wantVal) {
				t.Fatalf("%s: want %v, got %v", tc.wantKey, tc.wantVal, res.Data[tc.wantKey])
			}
		})
		t.Run(tc.name+"/disconnected-link", func(t *testing.T) {
			ctx := host.WithIDELink(context.Background(), &fakeLink{connected: false})
			res, err := tc.h(ctx, nil)
			if err != nil {
				t.Fatalf("disconnected link must not error, got %v", err)
			}
			if res.Data["connected"] != false {
				t.Fatalf("connected: want false, got %v", res.Data["connected"])
			}
		})
	}
}

// --- A drop mid-call (CallTool returns ide.ErrNotConnected) also yields the
// typed not-connected Result, not a Go error. ---

func TestIDEHandlers_DropMidCallNotConnected(t *testing.T) {
	link := &fakeLink{connected: true, err: ide.ErrNotConnected}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetDiagnosticsHandler(ctx, nil)
	if err != nil {
		t.Fatalf("drop must not surface as Go error, got %v", err)
	}
	if res.Data["connected"] != false {
		t.Fatalf("connected: want false on drop, got %v", res.Data["connected"])
	}
}

// --- A non-connection infra error surfaces as a Go error. ---

func TestIDEHandlers_InfraError(t *testing.T) {
	link := &fakeLink{connected: true, err: errors.New("boom")}
	ctx := host.WithIDELink(context.Background(), link)
	if _, err := host.IDEGetSelectionHandler(ctx, nil); err == nil {
		t.Fatal("infra error must surface as a Go error")
	}
}

// --- Connected path against a stubbed link asserts Result.Data shape. ---

func TestIDEGetDiagnostics_Connected(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getDiagnostics": envelope(`{"diagnostics":[{"file":"/a.go","message":"x","severity":"error"}]}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetDiagnosticsHandler(ctx, map[string]any{"path": "/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["connected"] != true {
		t.Fatalf("connected: want true, got %v", res.Data["connected"])
	}
	if link.lastTool != "getDiagnostics" {
		t.Fatalf("tool: want getDiagnostics, got %q", link.lastTool)
	}
	if link.lastArgs["uri"] != "/a.go" {
		t.Fatalf("path must be sent as uri, got %v", link.lastArgs)
	}
	diags, ok := res.Data["diagnostics"].([]any)
	if !ok || len(diags) != 1 {
		t.Fatalf("diagnostics: want 1 entry, got %v", res.Data["diagnostics"])
	}
}

func TestIDEGetSelection_Connected(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getCurrentSelection": envelope(`{"file":"/a.go","text":"sel","range":{"start":1}}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetSelectionHandler(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if link.lastTool != "getCurrentSelection" {
		t.Fatalf("must use getCurrentSelection (live), got %q", link.lastTool)
	}
	if res.Data["file"] != "/a.go" || res.Data["text"] != "sel" {
		t.Fatalf("selection shape wrong: %v", res.Data)
	}
	if res.Data["range"] == nil {
		t.Fatal("range should be populated")
	}
}

func TestIDEGetOpenEditors_Connected(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getOpenEditors": envelope(`{"editors":[{"file":"/a.go","active":true}]}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetOpenEditorsHandler(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	eds, ok := res.Data["editors"].([]any)
	if !ok || len(eds) != 1 {
		t.Fatalf("editors: want 1, got %v", res.Data["editors"])
	}
}

// TestIDEGetSelection_VSCodeWireShape: the real getCurrentSelection returns the
// file under "filePath" (not "file") and the range under "selection" (not
// "range"). The handler must normalise both, stripping any file:// scheme.
func TestIDEGetSelection_VSCodeWireShape(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getCurrentSelection": envelope(
				`{"filePath":"file:///repo/a.go","text":"","selection":{"start":{"line":3,"character":0},"end":{"line":3,"character":0}}}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetSelectionHandler(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["file"] != "/repo/a.go" {
		t.Fatalf("filePath must normalise to file (scheme stripped); got %v", res.Data["file"])
	}
	if res.Data["range"] == nil {
		t.Fatal("selection must normalise to range")
	}
}

// TestIDEGetOpenEditors_TabsWireShape: the real getOpenEditors returns the list
// under "tabs" (VS Code TabGroups API), with paths under "fileName"/"uri". The
// handler must accept "tabs" as an alias for "editors".
func TestIDEGetOpenEditors_TabsWireShape(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getOpenEditors": envelope(
				`{"tabs":[{"uri":"file:///repo/a.go","fileName":"/repo/a.go","isActive":true}]}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetOpenEditorsHandler(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	eds, ok := res.Data["editors"].([]any)
	if !ok || len(eds) != 1 {
		t.Fatalf("tabs must surface as editors; got %v", res.Data["editors"])
	}
}

func TestIDEOpenFile_Connected(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openFile": envelope("ok", false)},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEOpenFileHandler(ctx, map[string]any{"path": "/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["ok"] != true || res.Data["connected"] != true {
		t.Fatalf("open_file shape wrong: %v", res.Data)
	}
	if link.lastArgs["path"] != "/a.go" {
		t.Fatalf("path not forwarded: %v", link.lastArgs)
	}
}

func TestIDEOpenDiff_Connected_NonBlocking(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openDiff": envelope("ack", false)},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEOpenDiffHandler(ctx, map[string]any{
		"path": "/a.go", "new_text": "x", "title": "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["ok"] != true {
		t.Fatalf("open_diff must return ok:true on ack, got %v", res.Data)
	}
	if link.lastTool != "openDiff" {
		t.Fatalf("tool: want openDiff, got %q", link.lastTool)
	}
}

// open_diff surfaces the editor's accept/reject verdict, so a story can bind
// data.verdict and branch (publish vs re-refine).
func TestIDEOpenDiff_SurfacesVerdict(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openDiff": envelope(`{"ok":true,"verdict":"rejected"}`, false)},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEOpenDiffHandler(ctx, map[string]any{
		"path": "/a.md", "new_text": "v2", "title": "t",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["verdict"] != "rejected" {
		t.Fatalf("verdict: want rejected, got %v", res.Data["verdict"])
	}
	if link.lastArgs["new_text"] != "v2" {
		t.Fatalf("new_text not forwarded: %v", link.lastArgs)
	}
}

// A connected editor that returns no verdict defaults to "accepted" (the v1
// proceed-anyway posture) so a non-gating editor still advances the story.
func TestIDEOpenDiff_DefaultsVerdictAccepted(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openDiff": envelope(`{"ok":true}`, false)},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEOpenDiffHandler(ctx, map[string]any{"path": "/a.md", "new_text": "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Data["verdict"] != "accepted" {
		t.Fatalf("verdict: want accepted default, got %v", res.Data["verdict"])
	}
}

// --- Tool-level isError surfaces as Result.Error, not a Go error. ---

func TestIDEHandlers_ToolIsError(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"getDiagnostics": envelope("no LSP", true)},
	}
	ctx := host.WithIDELink(context.Background(), link)
	res, err := host.IDEGetDiagnosticsHandler(ctx, nil)
	if err != nil {
		t.Fatalf("isError must not be a Go error, got %v", err)
	}
	if res.Error == "" {
		t.Fatal("isError:true must populate Result.Error")
	}
}

// --- Handlers are registered and pass ValidateAllowList. ---

func TestIDEHandlers_Registered(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	verbs := []string{
		"host.ide.get_diagnostics", "host.ide.get_selection",
		"host.ide.get_open_editors", "host.ide.open_file", "host.ide.open_diff",
	}
	for _, v := range verbs {
		if _, ok := r.Get(v); !ok {
			t.Fatalf("%s not registered", v)
		}
	}
	if err := r.ValidateAllowList(verbs); err != nil {
		t.Fatalf("ValidateAllowList: %v", err)
	}
}
