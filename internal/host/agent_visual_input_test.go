package host_test

// Spatial-attachment-trace slice 3 — the operator's screen-context bundle is
// recorded as `input.visual` on the oracle call event, the frame referenced BY
// HANDLE through the real artifact substrate (host.artifacts_dir →
// JournalArtifactResolver), with no LLM and no ffmpeg.
//
// These tests drive the REAL AgentConverseHandler with a FakeConverse runner
// (no `claude`, no network) and assert:
//
//  1. A converse call carrying a VisualAmbient records `input.visual` on the
//     AgentCalled event — frame_handle, point, element + bbox, schema_version —
//     round-tripping the bundle into the (mocked) oracle input. The frame_handle
//     is a real recorded artifact (a tiny checked-in PNG registered via
//     host.artifacts_dir), so the dangling-frame check passes.
//  2. A converse call with NO VisualAmbient records no `visual` key — the compat
//     case renders exactly as before.
//  3. A converse call whose frame_handle does NOT resolve to a recorded artifact
//     is REJECTED (Result.Error set, no AgentReturned), so the trace can never
//     carry a dangling frame reference.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// recordFixtureFrame copies the checked-in tiny PNG into a fresh artifacts root
// via the real host.artifacts_dir media-emit path (journal-backed), returning
// the artifact handle and a journal.Reader that resolves it. This is the same
// substrate host.video.frame hands its stills to, so the recorded handle is a
// genuine recorded artifact — not a synthetic string.
func recordFixtureFrame(t *testing.T) (handle string, reader journal.Reader, cleanup func()) {
	t.Helper()

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	jw, err := journal.NewSQLiteWriter(st.DB())
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	jr, err := journal.NewSQLiteReader(st.DB())
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}

	root := t.TempDir()
	// host.artifacts_dir copies BY src_path; the journal entry is stamped with
	// the dispatch session via SessionStamping, exactly as the orchestrator does.
	ctx := host.WithArtifactJournalWriter(
		context.Background(),
		journal.SessionStamping(jw, app.SessionID("visual-sess")),
	)
	res, err := host.ArtifactsDirTransportHandler(ctx, map[string]any{
		"thread":         "frame",
		"src_path":       filepath.Join("testdata", "frame-1x1.png"),
		"kind":           "image",
		"artifacts_root": root,
		"label":          "frame @ 0:14",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("record fixture frame: err=%v result.error=%q", err, res.Error)
	}
	h, _ := res.Data["handle"].(map[string]any)
	handle, _ = h["id"].(string)
	if handle == "" {
		t.Fatalf("no artifact handle returned: %+v", res.Data)
	}
	return handle, jr, func() { _ = st.Close() }
}

// fixtureAmbient builds a representative VisualAmbient for the recorded frame.
func fixtureAmbient(frameHandle string) host.VisualAmbient {
	var a host.VisualAmbient
	a.FrameHandle = frameHandle
	a.Point.X = 1180
	a.Point.Y = 540
	a.TMs = 14300
	a.MediaHandle = "art:media-7c02"
	a.Route = "/review?video=art:media-7c02"
	a.Element = &struct {
		Selector string `json:"selector"`
		Role     string `json:"role"`
		Text     string `json:"text"`
		Bbox     [4]int `json:"bbox"`
	}{
		Selector: "[data-testid=intent-btn-run]",
		Role:     "button",
		Text:     "Run",
		Bbox:     [4]int{1140, 520, 96, 40},
	}
	return a
}

func TestAgentConverse_RecordsVisualInput(t *testing.T) {
	t.Parallel()

	frameHandle, reader, cleanup := recordFixtureFrame(t)
	defer cleanup()

	sink := &memSink{}
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
		SessionID: app.SessionID("visual-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("offpath"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithClaudeRunner(ctx, host.FakeConverse("that button is disabled because world.ready is false"))
	// The frame resolver is backed by the same journal the artifact was recorded
	// into, so a real handle resolves and the dangling-frame check passes.
	ctx = host.WithFrameResolver(ctx, host.FrameResolverFunc(func(h string) bool {
		return artifactInJournal(reader, "visual-sess", h)
	}))
	ctx = host.WithVisualAmbient(ctx, fixtureAmbient(frameHandle))

	res, err := host.AgentConverseHandler(ctx, map[string]any{
		"question": "why is this disabled here?",
	})
	if err != nil {
		t.Fatalf("AgentConverseHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	called := firstEventOfKind(t, sink, store.AgentCalled)
	var payload host.AgentCalledPayload
	if err := json.Unmarshal(called.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentCalled payload: %v", err)
	}
	if len(payload.Input) == 0 {
		t.Fatal("AgentCalled.input is empty; expected the visual bundle")
	}
	var input struct {
		Visual struct {
			SchemaVersion int    `json:"schema_version"`
			FrameHandle   string `json:"frame_handle"`
			Point         struct {
				X int `json:"x"`
				Y int `json:"y"`
			} `json:"point"`
			TMs         int    `json:"t_ms"`
			MediaHandle string `json:"media_handle"`
			Route       string `json:"route"`
			Element     struct {
				Selector string `json:"selector"`
				Role     string `json:"role"`
				Text     string `json:"text"`
				Bbox     []int  `json:"bbox"`
			} `json:"element"`
		} `json:"visual"`
	}
	if err := json.Unmarshal(payload.Input, &input); err != nil {
		t.Fatalf("unmarshal input.visual: %v", err)
	}

	v := input.Visual
	if v.SchemaVersion != host.VisualSchemaVersion {
		t.Errorf("schema_version = %d, want %d", v.SchemaVersion, host.VisualSchemaVersion)
	}
	if v.FrameHandle != frameHandle {
		t.Errorf("frame_handle = %q, want %q (recorded artifact, by handle not bytes)", v.FrameHandle, frameHandle)
	}
	if v.Point.X != 1180 || v.Point.Y != 540 {
		t.Errorf("point = (%d,%d), want (1180,540)", v.Point.X, v.Point.Y)
	}
	if v.TMs != 14300 {
		t.Errorf("t_ms = %d, want 14300", v.TMs)
	}
	if v.MediaHandle != "art:media-7c02" {
		t.Errorf("media_handle = %q", v.MediaHandle)
	}
	if v.Element.Selector != "[data-testid=intent-btn-run]" || v.Element.Role != "button" || v.Element.Text != "Run" {
		t.Errorf("element round-trip wrong: %+v", v.Element)
	}
	// Bbox is recorded for re-overlay (epic Q2 lean: yes).
	if len(v.Element.Bbox) != 4 || v.Element.Bbox[0] != 1140 || v.Element.Bbox[1] != 520 ||
		v.Element.Bbox[2] != 96 || v.Element.Bbox[3] != 40 {
		t.Errorf("element.bbox = %v, want [1140 520 96 40]", v.Element.Bbox)
	}

	// The frame rides BY HANDLE only — the recorded block must never inline bytes.
	if json.Valid(payload.Input) {
		if got := string(payload.Input); len(got) > 4096 {
			t.Errorf("input.visual is suspiciously large (%d bytes) — frame must ride by handle, not inlined bytes", len(got))
		}
	}
}

// TestAgentConverse_RecordsSemanticAnchor proves the v2 generalization end-to-end:
// a semantic_element anchor (no frame, no DOM element — a producer-declared pick)
// records as `input.visual.anchor.target` on the AgentCalled event, ref verbatim,
// schema_version stamped. No frame_handle ⇒ no FrameResolver needed.
func TestAgentConverse_RecordsSemanticAnchor(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
		SessionID: app.SessionID("sem-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("offpath"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithClaudeRunner(ctx, host.FakeConverse("that title scene reads well"))
	ctx = host.WithVisualAmbient(ctx, host.VisualAmbient{
		MediaHandle: "art:slidey-out",
		Anchor: host.AnnotationAnchor{
			Kind: host.AnchorSemanticElement,
			SemanticElement: &host.AnchorSemanticElementTarget{
				Plugin: "slidey",
				Ref:    "scene-3.title",
			},
		},
	})

	res, err := host.AgentConverseHandler(ctx, map[string]any{"question": "is this scene clear?"})
	if err != nil {
		t.Fatalf("AgentConverseHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	called := firstEventOfKind(t, sink, store.AgentCalled)
	var payload host.AgentCalledPayload
	if err := json.Unmarshal(called.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentCalled payload: %v", err)
	}
	var input struct {
		Visual struct {
			SchemaVersion int `json:"schema_version"`
			Anchor        struct {
				Target struct {
					Kind   string `json:"kind"`
					Plugin string `json:"plugin"`
					Ref    string `json:"ref"`
				} `json:"target"`
			} `json:"anchor"`
		} `json:"visual"`
	}
	if err := json.Unmarshal(payload.Input, &input); err != nil {
		t.Fatalf("unmarshal input.visual: %v", err)
	}
	if input.Visual.SchemaVersion != host.VisualSchemaVersion {
		t.Errorf("schema_version = %d, want %d", input.Visual.SchemaVersion, host.VisualSchemaVersion)
	}
	if input.Visual.Anchor.Target.Kind != "semantic_element" {
		t.Errorf("anchor.target.kind = %q, want semantic_element", input.Visual.Anchor.Target.Kind)
	}
	if input.Visual.Anchor.Target.Ref != "scene-3.title" {
		t.Errorf("anchor.target.ref = %q, want scene-3.title (verbatim)", input.Visual.Anchor.Target.Ref)
	}
}

func TestAgentConverse_NoVisual_Unchanged(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
		SessionID: app.SessionID("plain-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("offpath"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithClaudeRunner(ctx, host.FakeConverse("an answer"))
	// No VisualAmbient, no FrameResolver — the compat path.

	res, err := host.AgentConverseHandler(ctx, map[string]any{"question": "hi"})
	if err != nil {
		t.Fatalf("AgentConverseHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %q", res.Error)
	}

	called := firstEventOfKind(t, sink, store.AgentCalled)
	var payload host.AgentCalledPayload
	if err := json.Unmarshal(called.Payload, &payload); err != nil {
		t.Fatalf("unmarshal AgentCalled payload: %v", err)
	}
	if len(payload.Input) != 0 {
		// A no-visual converse call carries no input.visual: Input stays unset
		// exactly as before this slice.
		t.Errorf("AgentCalled.input should be unset for a no-visual call, got %s", string(payload.Input))
	}
}

func TestAgentConverse_DanglingFrame_Rejected(t *testing.T) {
	t.Parallel()

	// A resolver that knows NO handles — the recorded frame_handle is dangling.
	sink := &memSink{}
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
		SessionID: app.SessionID("dangle-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("offpath"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithClaudeRunner(ctx, host.FakeConverse("should never run"))
	ctx = host.WithFrameResolver(ctx, host.FrameResolverFunc(func(string) bool { return false }))
	ctx = host.WithVisualAmbient(ctx, fixtureAmbient("art:does-not-exist"))

	res, err := host.AgentConverseHandler(ctx, map[string]any{"question": "why?"})
	if err != nil {
		t.Fatalf("AgentConverseHandler returned a Go error (want Result.Error): %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error rejecting the dangling frame_handle, got none")
	}
	// The call is rejected before dispatch: no AgentReturned event recorded.
	for _, ev := range sink.events {
		if ev.Kind == store.AgentReturned {
			t.Error("a rejected dangling-frame call must not record an AgentReturned event")
		}
	}
}

// artifactInJournal reports whether handle names a recorded artifact in sid's
// typed journal — the test mirror of the orchestrator's resolver predicate.
func artifactInJournal(r journal.Reader, sid string, handle string) bool {
	seq, errFn := r.ReplayTyped(app.SessionID(sid))
	for entry := range seq {
		if entry.Kind != journal.KindArtifactEmitted {
			continue
		}
		var ev journal.ArtifactEvent
		if err := json.Unmarshal(entry.Body, &ev); err != nil {
			continue
		}
		if ev.ID == handle {
			_ = errFn()
			return true
		}
	}
	_ = errFn()
	return false
}

// firstEventOfKind returns the first sink event of kind k, failing the test if
// none is present.
func firstEventOfKind(t *testing.T, sink *memSink, k store.EventKind) store.Event {
	t.Helper()
	for _, ev := range sink.events {
		if ev.Kind == k {
			return ev
		}
	}
	t.Fatalf("no %s event in sink: %v", k, kinds(sink.events))
	return store.Event{}
}

// keep os import live for callers that may grow file-based fixtures.
var _ = os.Stat
