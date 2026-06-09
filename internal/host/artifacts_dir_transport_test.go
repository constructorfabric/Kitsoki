package host_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/journal"
)

func TestArtifactsDirTransport_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.artifacts_dir"); !ok {
		t.Fatal("host.artifacts_dir missing from registry")
	}
}

func TestArtifactsDirTransport_WritesUnderRoot(t *testing.T) {
	root := t.TempDir()
	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "reproducing_TKT-200_0",
		"title":          "Reproduction",
		"body":           "Confirmed reproducible on linux.",
		"phase_id":       "reproducing_TKT-200_0",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	path, _ := res.Data["path"].(string)
	if want := filepath.Join(root, "reproducing_TKT-200_0.md"); path != want {
		t.Fatalf("path: %q want %q", path, want)
	}
	raw, _ := os.ReadFile(path)
	s := string(raw)
	for _, want := range []string{"### Reproduction", "Confirmed reproducible on linux.", "_phase: reproducing_TKT-200_0_"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in file: %s", want, s)
		}
	}
}

func TestArtifactsDirTransport_AppendsSecondCall(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"first chunk", "second chunk"} {
		res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
			"artifacts_root": root,
			"thread":         "design",
			"body":           body,
		})
		if err != nil || res.Error != "" {
			t.Fatalf("call %d: %v / %s", i, err, res.Error)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(root, "design.md"))
	s := string(raw)
	if !strings.Contains(s, "first chunk") || !strings.Contains(s, "second chunk") {
		t.Fatalf("missing chunks: %s", s)
	}
	if !strings.Contains(s, "\n---\n") {
		t.Fatalf("missing separator between appends: %s", s)
	}
}

func TestArtifactsDirTransport_ReplaceMode(t *testing.T) {
	root := t.TempDir()
	for i, body := range []string{"old", "new"} {
		mode := "append"
		if i == 1 {
			mode = "replace"
		}
		_, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
			"artifacts_root": root,
			"thread":         "code",
			"body":           body,
			"mode":           mode,
		})
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(root, "code.md"))
	s := string(raw)
	if strings.Contains(s, "old") {
		t.Fatalf("replace did not overwrite: %s", s)
	}
	if !strings.Contains(s, "new") {
		t.Fatalf("replace missing new content: %s", s)
	}
}

func TestArtifactsDirTransport_RequiresThreadAndBody(t *testing.T) {
	cases := []map[string]any{
		{"body": "x"},
		{"thread": "y"},
	}
	for i, args := range cases {
		res, err := host.ArtifactsDirTransportHandler(context.Background(), args)
		if err != nil {
			t.Fatalf("case %d infra: %v", i, err)
		}
		if res.Error == "" {
			t.Fatalf("case %d: expected domain error, got %v", i, res.Data)
		}
	}
}

func TestArtifactsDirTransport_StructuredBodyJSON(t *testing.T) {
	root := t.TempDir()
	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "feature_TKT-100_0",
		"body": map[string]any{
			"summary_title": "Feature artifact",
			"phase_count":   1,
		},
	})
	if err != nil || res.Error != "" {
		t.Fatalf("call: %v / %s", err, res.Error)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "feature_TKT-100_0.md"))
	s := string(raw)
	if !strings.Contains(s, "summary_title") {
		t.Fatalf("structured body not rendered as JSON: %s", s)
	}
}

// ── Media-emit tests ─────────────────────────────────────────────────────────

func TestArtifactsDirTransport_MediaEmit_CopiesFile(t *testing.T) {
	root := t.TempDir()
	// Create a tiny fixture file (synthetic MP4-like bytes — no real ffmpeg).
	src := filepath.Join(t.TempDir(), "output.mp4")
	if err := os.WriteFile(src, []byte("FAKEVIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "pitch_video",
		"src_path":       src,
		"kind":           "video",
		"label":          "Pitch MP4",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}

	// Destination must be under root, preserving the .mp4 extension.
	destPath, _ := res.Data["path"].(string)
	if want := filepath.Join(root, "pitch_video.mp4"); destPath != want {
		t.Fatalf("path: %q want %q", destPath, want)
	}
	raw, _ := os.ReadFile(destPath)
	if string(raw) != "FAKEVIDEO" {
		t.Fatalf("file contents wrong: %q", string(raw))
	}

	// handle must contain id, kind, mime, label.
	handle, _ := res.Data["handle"].(map[string]any)
	if handle == nil {
		t.Fatalf("handle missing from result: %v", res.Data)
	}
	if handle["kind"] != "video" {
		t.Fatalf("handle.kind: %v", handle["kind"])
	}
	if handle["label"] != "Pitch MP4" {
		t.Fatalf("handle.label: %v", handle["label"])
	}
	id, _ := handle["id"].(string)
	if id == "" {
		t.Fatalf("handle.id empty")
	}
	// ID must contain the stem (human-readable).
	if !strings.Contains(id, "pitch_video") {
		t.Fatalf("handle.id %q does not contain stem", id)
	}
}

func TestArtifactsDirTransport_MediaEmit_ImageKind(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "frame.png")
	if err := os.WriteFile(src, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "contact_sheet",
		"src_path":       src,
		"kind":           "image",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("call: %v / %s", err, res.Error)
	}
	destPath, _ := res.Data["path"].(string)
	if want := filepath.Join(root, "contact_sheet.png"); destPath != want {
		t.Fatalf("path: %q want %q", destPath, want)
	}
	handle, _ := res.Data["handle"].(map[string]any)
	if handle["kind"] != "image" {
		t.Fatalf("kind: %v", handle["kind"])
	}
}

func TestArtifactsDirTransport_MediaEmit_JournalEvent(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "demo.mp4")
	if err := os.WriteFile(src, []byte("FAKEVIDEO"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wire a real in-memory journal writer so we can inspect the emitted event.
	store := journal.NewMemStore()
	jw := journal.NewMemWriter(store)
	jr := journal.NewMemReader(store)

	ctx := host.WithArtifactJournalWriter(context.Background(), jw)
	res, err := host.ArtifactsDirTransportHandler(ctx, map[string]any{
		"artifacts_root": root,
		"thread":         "demo_video",
		"src_path":       src,
		"kind":           "video",
		"label":          "Demo",
	})
	if err != nil || res.Error != "" {
		t.Fatalf("call: %v / %s", err, res.Error)
	}

	// Replay the journal and find the artifact.emitted entry.
	handle, _ := res.Data["handle"].(map[string]any)
	emittedID, _ := handle["id"].(string)

	found := false
	seq, errFn := jr.ReplayTyped(app.SessionID(""))
	for entry := range seq {
		if entry.Kind != journal.KindArtifactEmitted {
			continue
		}
		var ev journal.ArtifactEvent
		if err2 := json.Unmarshal(entry.Body, &ev); err2 != nil {
			t.Fatalf("unmarshal ArtifactEvent: %v", err2)
		}
		if ev.ID == emittedID {
			found = true
			if ev.Kind != "video" {
				t.Fatalf("event.Kind: %q", ev.Kind)
			}
			if ev.Label != "Demo" {
				t.Fatalf("event.Label: %q", ev.Label)
			}
			if ev.Path == "" {
				t.Fatal("event.Path empty")
			}
			if ev.SizeBytes <= 0 {
				t.Fatalf("event.SizeBytes: %d", ev.SizeBytes)
			}
			break
		}
	}
	_ = errFn()
	if !found {
		t.Fatalf("artifact.emitted event with id %q not found in journal", emittedID)
	}
}

func TestArtifactsDirTransport_MediaEmit_PathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	// src_path is a valid file, but the thread contains ".." path traversal.
	src := filepath.Join(t.TempDir(), "safe.mp4")
	if err := os.WriteFile(src, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "../../../etc/passwd",
		"src_path":       src,
		"kind":           "video",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected domain error for path traversal, got ok path=%v", res.Data["path"])
	}
	if !strings.Contains(res.Error, "escapes") {
		t.Fatalf("error message should mention 'escapes': %s", res.Error)
	}
}

func TestArtifactsDirTransport_MediaEmit_RequiresKind(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(src, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "out",
		"src_path":       src,
		// kind deliberately omitted
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected domain error for missing kind, got %v", res.Data)
	}
}

func TestArtifactsDirTransport_MediaEmit_UnsupportedKindRejected(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(src, []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
		"artifacts_root": root,
		"thread":         "out",
		"src_path":       src,
		"kind":           "binary", // not in the allowed set
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatalf("expected domain error for unsupported kind, got %v", res.Data)
	}
}
