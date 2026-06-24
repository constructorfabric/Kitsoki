//go:build ide_live

// Opt-in fidelity test against a REAL running editor. This is the antidote to
// the bug class that let /ide ship broken: the hermetic e2e can only be as
// honest as its stub payloads, and a stub authored from a wrong guess agrees
// with an equally-wrong parser. This test connects to a live editor over the
// real lock file + ws MCP, dumps the ACTUAL wire bytes each verb returns, and
// asserts the host handlers normalise them to non-empty output. Run it whenever
// the protocol might have drifted to refresh internal/ide/stubserver_test.go's
// cannedToolPayload from reality.
//
// It is build-tagged (`ide_live`) so it never runs in CI (no editor there):
//
//	# 1. Open this repo in VS Code / Cursor / Windsurf (so a lock file exists).
//	# 2. Run from the repo root:
//	go test -tags ide_live ./internal/ide/ -run TestLive -v
//
// The -v output prints the raw envelope for each verb; copy the shapes into
// cannedToolPayload when they differ. A failure means either no editor is
// connected or a verb's real shape no longer normalises — both are exactly the
// signal we want.

package ide

import (
	"context"
	"os"
	"testing"
	"time"

	"kitsoki/internal/host"
)

func TestLive_RealEditorWireShapes(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	link := NewLink(cwd, nil) // nil discoverer → real ~/.claude/ide/*.lock discovery
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := link.Connect(ctx)
	if err != nil {
		t.Skipf("no live editor to connect to (open this workspace in an editor first): %v", err)
	}
	defer func() { _ = link.Close() }()
	t.Logf("connected: ide=%q workspace=%q port=%d", info.IDEName, info.Workspace, info.Port)

	// Dump the raw envelope for each read verb so the stub fixtures can be
	// refreshed from reality.
	for _, tool := range []string{"getCurrentSelection", "getOpenEditors", "getDiagnostics"} {
		raw, err := link.CallTool(ctx, tool, map[string]any{})
		if err != nil {
			t.Errorf("%s: CallTool error: %v", tool, err)
			continue
		}
		t.Logf("RAW %s = %s", tool, string(raw))
	}

	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	hctx := host.WithIDELink(ctx, link)

	// get_open_editors must normalise the real shape (tabs/fileName/uri) to a
	// non-empty editors list with a parseable path — the exact thing that was
	// silently broken.
	res, err := reg.Invoke(hctx, "host.ide.get_open_editors", nil)
	if err != nil {
		t.Fatalf("get_open_editors: %v", err)
	}
	t.Logf("normalised get_open_editors = %v", res.Data)
	eds, _ := res.Data["editors"].([]any)
	if len(eds) == 0 {
		t.Error("get_open_editors normalised to ZERO editors against a live editor — " +
			"the wire shape likely drifted; check RAW getOpenEditors above and update cannedToolPayload + editorFilePath")
	} else if first, ok := eds[0].(map[string]any); ok {
		hasPath := false
		for _, k := range []string{"fileName", "file", "path", "fsPath", "uri", "fileUrl"} {
			if s, ok := first[k].(string); ok && s != "" {
				hasPath = true
				break
			}
		}
		if !hasPath {
			t.Errorf("open editor item has no parseable path field: %v", first)
		}
	}

	// get_selection must at least connect and unwrap without error; whether a
	// file/text is present depends on editor focus, so only log it.
	res, err = reg.Invoke(hctx, "host.ide.get_selection", nil)
	if err != nil {
		t.Fatalf("get_selection: %v", err)
	}
	t.Logf("normalised get_selection = file=%v text-empty=%v range-nil=%v",
		res.Data["file"], res.Data["text"] == "", res.Data["range"] == nil)
	if res.Data["connected"] != true {
		t.Error("get_selection must report connected against a live editor")
	}
}
