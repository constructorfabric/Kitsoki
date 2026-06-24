package tui_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

// recordingCloakModel builds a cloak RootModel whose orchestrator writes to a
// JSONL eventSink, so a test can read back the ide.context_captured events the
// ambient capture records.
func recordingCloakModel(t *testing.T) (tuipkg.RootModel, *store.JSONLSink) {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "trace.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)

	orch := orchestrator.New(def, mach, s, h, orchestrator.WithEventSink(sink))
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
	return rm, sink
}

func idePayloadsFromSink(t *testing.T, sink *store.JSONLSink) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range sink.History() {
		if ev.Kind != store.IDEContextCaptured {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		out = append(out, p)
	}
	return out
}

// TestIDECapture_RecordedInTrace: a connected ambient capture is recorded as an
// ide.context_captured event — both when an editor file rides the turn and when
// nothing usable is found (so a "connected but nothing rode" report is
// diagnosable from the trace).
func TestIDECapture_RecordedInTrace(t *testing.T) {
	t.Parallel()
	const doc = "/home/cloud-user/code/kitsoki/docs/notes.md"

	t.Run("active file ride is recorded", func(t *testing.T) {
		rm, sink := recordingCloakModel(t)
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetOpenEditors([]map[string]any{{"file": doc, "active": true}})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)

		recs := idePayloadsFromSink(t, sink)
		require.Len(t, recs, 1)
		require.Equal(t, "active_editor", recs[0]["source"])
		require.Equal(t, doc, recs[0]["file"])
		require.Equal(t, true, recs[0]["injected"])
		require.Equal(t, true, recs[0]["connected"])
	})

	t.Run("ambiguous focus records source none + reason", func(t *testing.T) {
		rm, sink := recordingCloakModel(t)
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetOpenEditors([]map[string]any{
			{"file": "/a/one.go", "active": false},
			{"file": "/a/two.go", "active": false},
		})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)

		recs := idePayloadsFromSink(t, sink)
		require.Len(t, recs, 1)
		require.Equal(t, "none", recs[0]["source"])
		require.Equal(t, false, recs[0]["injected"])
		require.Equal(t, "ambiguous_focus", recs[0]["reason"])
	})

	t.Run("disconnected records nothing", func(t *testing.T) {
		rm, sink := recordingCloakModel(t)
		// No link set → not connected.
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Empty(t, idePayloadsFromSink(t, sink),
			"a disconnected turn must not write an ide.context_captured event")
	})
}

// TestIDEStatus_OffWhenDisconnected asserts /ide status reports the off state
// when no link is connected, and renders no footer chip.
func TestIDEStatus_OffWhenDisconnected(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	require.Empty(t, tuipkg.IDEFooterChipForTest(rm), "no chip when disconnected")

	rm = tuipkg.HandleIDESlashForTest(rm, []string{"status"})
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "off", "status should report off when disconnected; got %q", content)
}

// TestIDEBare_DoesNotPanic is the regression guard for the bare `/ide` crash:
// with empty args the dispatcher took the `sub == ""` branch and sliced
// args[1:] on the empty slice, panicking "slice bounds out of range [1:0]".
// Bare /ide must instead route to connect (here: a disconnected fake whose
// discovery finds nothing, so it reports "no editor found").
func TestIDEBare_DoesNotPanic(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	// A closed fake is disconnected and its Candidates() returns nothing, so
	// the connect path is deterministic and hits no real lock files.
	fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
	_ = fake.Close()
	tuipkg.SetIDELinkForTest(&rm, fake)

	rm = tuipkg.HandleIDESlashForTest(rm, nil)
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "no editor found",
		"bare /ide should route to connect, not panic")
}

// TestIDEStatus_ReportsConnectedDetails asserts /ide status surfaces the
// editor name, workspace, and port from the live link.
func TestIDEStatus_ReportsConnectedDetails(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	fake := tuipkg.NewFakeIDELink("VS Code", "/home/cloud-user/code/kitsoki", 25118)
	tuipkg.SetIDELinkForTest(&rm, fake)

	rm = tuipkg.HandleIDESlashForTest(rm, []string{"status"})
	content := tuipkg.GetTranscriptContent(rm)
	require.Contains(t, content, "VS Code")
	require.Contains(t, content, "/home/cloud-user/code/kitsoki")
	require.Contains(t, content, "25118")
}

// TestIDEDisconnect_DetachesAndFlipsChipOff asserts /ide disconnect closes the
// link, detaches it from the orchestrator, and the footer chip goes off.
func TestIDEDisconnect_DetachesAndFlipsChipOff(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1234)
	tuipkg.SetIDELinkForTest(&rm, fake)
	require.NotEmpty(t, tuipkg.IDEFooterChipForTest(rm), "chip on while connected")

	rm = tuipkg.HandleIDESlashForTest(rm, []string{"disconnect"})
	require.Empty(t, tuipkg.IDEFooterChipForTest(rm), "chip off after disconnect")
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "disconnected")
}

// TestIDEIndicator_RendersWithConcurrentLogging is the NON-NEGOTIABLE combined
// I/O test: it captures View() output while slog writes concurrently and the
// link toggles connected/disconnected, asserting the footer chip is correct and
// uncorrupted. Must fail without the footer element (no chip today).
func TestIDEIndicator_RendersWithConcurrentLogging(t *testing.T) {
	// Not parallel: rebinds slog default.
	orch, sid := setupCloak(t)
	rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))

	captured := tuipkg.CaptureSlog(t)
	defer captured.Restore()

	// Connected: the chip must render with the editor name and ✓.
	fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 25118)
	tuipkg.SetIDELinkForTest(&rm, fake)

	// Drive View() while logging concurrently, mirroring the real terminal
	// where slog output and the live transcript share stderr.
	var connectedView string
	helper := tuipkg.NewConcurrentIOTester(t, captured)
	helper.LogConcurrently(func() {
		for i := 0; i < 50; i++ {
			slog.Info("agent.event", "turn", i)
		}
	}).RenderConcurrently(func() {
		for i := 0; i < 50; i++ {
			connectedView = rm.View()
		}
	})

	require.Contains(t, connectedView, "⧉ ide: VS Code ✓",
		"footer chip must render the connected indicator")
	// The chip glyph must never bleed into a slog line.
	captured.AssertNoMixedOutput("INFO", "⧉ ide:", "ide chip in log lines")

	// Disconnected: the chip disappears from the View().
	tuipkg.SetIDELinkForTest(&rm, nil)
	offView := rm.View()
	require.NotContains(t, offView, "⧉ ide:", "chip hidden when disconnected")
}

// TestSelectionEcho_AppearsOncePerTurn asserts exactly one
// `⧉ Selected N lines from <file>` line per turn carrying a selection, the
// matching ambient context is staged for the prompt, and nothing is echoed when
// disconnected or when the file is deny-ruled.
func TestSelectionEcho_AppearsOncePerTurn(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)

	const file = "/home/cloud-user/code/kitsoki/internal/foo/x.go"
	const sel = "line one\nline two\nline three"

	t.Run("connected with selection echoes once and stages args.ide", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection(file, sel, map[string]any{
			"start": map[string]any{"line": float64(12), "character": float64(0)},
			"end":   map[string]any{"line": float64(14), "character": float64(8)},
		})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)

		content := tuipkg.GetTranscriptContent(rm)
		echo := "⧉ Selected 3 lines from " + file
		require.Equal(t, 1, strings.Count(content, echo),
			"exactly one selection echo per turn; got %q", content)

		amb := tuipkg.PendingIDEAmbientForTest(rm)
		require.Equal(t, file, amb.File)
		require.Equal(t, sel, amb.Selection)
		require.Equal(t, 3, amb.Lines)
		require.Equal(t, "12:0-14:8", amb.Range)
	})

	t.Run("disconnected echoes nothing", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.NotContains(t, tuipkg.GetTranscriptContent(rm), "⧉ Selected")
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File)
	})

	t.Run("deny-ruled file attaches nothing", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection("/secrets/creds.env", "TOKEN=abc", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)
		tuipkg.SetIDEDenyForTest(&rm, []string{"*.env"})

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.NotContains(t, tuipkg.GetTranscriptContent(rm), "⧉ Selected",
			"deny-ruled file must not echo")
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File,
			"deny-ruled file must not stage ambient context")
	})

	t.Run("no active editor and no open tabs echoes nothing", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		// Empty file = no active text editor; no open tabs either.
		fake.SetSelection("", "", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.NotContains(t, tuipkg.GetTranscriptContent(rm), "⧉ Selected")
		require.NotContains(t, tuipkg.GetTranscriptContent(rm), "⧉ Editor open")
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File)
	})

	t.Run("cursor in a file with no highlight rides as the active document", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		// getCurrentSelection reports the focused file with empty text.
		fake.SetSelection(file, "", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		amb := tuipkg.PendingIDEAmbientForTest(rm)
		require.Equal(t, file, amb.File, "the focused file rides even with no selection")
		require.Empty(t, amb.Selection, "no highlighted text")
		require.Contains(t, tuipkg.GetTranscriptContent(rm), "⧉ Editor open on "+file)
	})
}

// TestActiveEditor_RidesWhenNoSelection covers the no-selection fallback: with
// nothing highlighted, the focused open file rides the turn (path only) so
// "reference the open doc" works without selecting. Selection still wins when
// present, ambiguous focus rides nothing, and the deny list applies.
func TestActiveEditor_RidesWhenNoSelection(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	const doc = "/home/cloud-user/code/kitsoki/docs/notes.md"

	t.Run("active open file rides when nothing selected", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		// No selection set → getCurrentSelection is not-connected → fall back.
		fake.SetOpenEditors([]map[string]any{
			{"file": doc, "active": true},
			{"file": "/home/cloud-user/code/kitsoki/main.go", "active": false},
		})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		amb := tuipkg.PendingIDEAmbientForTest(rm)
		require.Equal(t, doc, amb.File)
		require.Empty(t, amb.Selection, "file-only fallback carries no selection text")
		require.Contains(t, tuipkg.GetTranscriptContent(rm), "⧉ Editor open on "+doc)
	})

	t.Run("single open editor rides even when not flagged active", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetOpenEditors([]map[string]any{{"file": doc, "active": false}})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, doc, tuipkg.PendingIDEAmbientForTest(rm).File)
	})

	t.Run("selection wins over open file", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection("/a/sel.go", "picked", nil)
		fake.SetOpenEditors([]map[string]any{{"file": "/a/other.go", "active": true}})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		amb := tuipkg.PendingIDEAmbientForTest(rm)
		require.Equal(t, "/a/sel.go", amb.File)
		require.Equal(t, "picked", amb.Selection)
	})

	t.Run("ambiguous focus rides nothing", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetOpenEditors([]map[string]any{
			{"file": "/a/one.go", "active": false},
			{"file": "/a/two.go", "active": false},
		})
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File,
			"several editors, none active — focus is ambiguous, ride nothing")
		require.NotContains(t, tuipkg.GetTranscriptContent(rm), "⧉ Editor open")
	})

	t.Run("deny-ruled active file rides nothing", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetOpenEditors([]map[string]any{{"file": "/secrets/x.env", "active": true}})
		tuipkg.SetIDELinkForTest(&rm, fake)
		tuipkg.SetIDEDenyForTest(&rm, []string{"*.env"})

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File)
	})
}

// TestSelectionEcho_InjectsOnlyOnChange asserts a selection feeds the turn (and
// echoes) only when it differs from the one that last rode a turn: a held
// selection must not silently re-shape every follow-up, a changed selection
// rides again, and a deselect resets the tracker so reselecting the same range
// counts as new.
func TestSelectionEcho_InjectsOnlyOnChange(t *testing.T) {
	t.Parallel()
	orch, sid := setupCloak(t)
	const file = "/home/cloud-user/code/kitsoki/internal/foo/x.go"

	t.Run("unchanged selection rides only the first turn", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection(file, "line one\nline two", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, file, tuipkg.PendingIDEAmbientForTest(rm).File)
		require.Equal(t, 1, strings.Count(tuipkg.GetTranscriptContent(rm), "⧉ Selected"))

		// Same selection on the next turn — no inject, no second echo.
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File,
			"unchanged selection must not ride a follow-up turn")
		require.Equal(t, 1, strings.Count(tuipkg.GetTranscriptContent(rm), "⧉ Selected"),
			"unchanged selection must not echo again")
	})

	t.Run("changed selection rides again", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection(file, "first", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, "first", tuipkg.PendingIDEAmbientForTest(rm).Selection)

		fake.SetSelection(file, "second different selection", nil)
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, "second different selection", tuipkg.PendingIDEAmbientForTest(rm).Selection,
			"a changed selection must ride the turn")
		require.Equal(t, 2, strings.Count(tuipkg.GetTranscriptContent(rm), "⧉ Selected"))
	})

	t.Run("deselect then reselect same range rides again", func(t *testing.T) {
		rm, _ := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
		fake := tuipkg.NewFakeIDELink("VS Code", "/ws", 1)
		fake.SetSelection(file, "same", nil)
		tuipkg.SetIDELinkForTest(&rm, fake)

		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, "same", tuipkg.PendingIDEAmbientForTest(rm).Selection)

		// Close the editor (no active text editor, no tabs) — resets the
		// change-tracker. An empty *file* (not just empty text) is "nothing
		// focused"; empty text with a file would still ride as the active doc.
		fake.SetSelection("", "", nil)
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Empty(t, tuipkg.PendingIDEAmbientForTest(rm).File)

		// Reselect the identical range — counts as new again.
		fake.SetSelection(file, "same", nil)
		rm = tuipkg.CaptureIDEAmbientForTest(rm)
		require.Equal(t, "same", tuipkg.PendingIDEAmbientForTest(rm).Selection,
			"reselecting after a deselect must ride again")
	})
}
