package host

import (
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/video"
)

func TestChaptersFromSlideySpec_EvenSplitAndIDs(t *testing.T) {
	spec := []byte(`{
	  "total_ms": 9000,
	  "scenes": [
	    {"type": "title", "title": "Kitsoki"},
	    {"id": "arch", "title": "Architecture"},
	    {"type": "cta", "narration": "go"}
	  ]
	}`)
	chs, err := chaptersFromSlideySpec(spec, "deck.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 3 {
		t.Fatalf("got %d chapters", len(chs))
	}
	// Even split: 9000/3 = 3000 each.
	if chs[0].StartMs != 0 || chs[0].EndMs != 3000 || chs[1].StartMs != 3000 {
		t.Errorf("windows: %+v", chs)
	}
	// Synthesised id when absent; explicit id honoured.
	if chs[0].ID != "scene-0" || chs[1].ID != "arch" {
		t.Errorf("ids: %q %q", chs[0].ID, chs[1].ID)
	}
	// Label falls back to type when title absent.
	if chs[2].Label != "cta" {
		t.Errorf("label fallback: %q", chs[2].Label)
	}
	if chs[1].Source.Kind != "slidey" || chs[1].Source.SpecPath != "deck.json" || chs[1].Source.SceneID != "arch" {
		t.Errorf("source_ref: %+v", chs[1].Source)
	}
}

func TestChaptersFromSlideySpec_PerSceneDuration(t *testing.T) {
	spec := []byte(`{"scenes": [
	  {"id": "a", "duration_ms": 2000},
	  {"id": "b", "duration_ms": 4000}
	]}`)
	chs, err := chaptersFromSlideySpec(spec, "d.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	if chs[0].EndMs != 2000 || chs[1].StartMs != 2000 || chs[1].EndMs != 6000 {
		t.Errorf("explicit-duration windows: %+v", chs)
	}
}

// TestChaptersFromSlideyScenes_EvenSplitFromRenderedDuration is the fix for the
// zero-width-window bug: a spec with NO per-scene/total timing must still yield
// real, contiguous, non-zero windows by distributing the rendered video's
// actual duration across the N scenes. This is the exact shape of the demo
// deck: 6 scenes, no timing, a 59907ms render → ~10s windows that tile [0,
// 59907] exactly (the final window snaps to the total so there is no rounding
// gap or overhang).
func TestChaptersFromSlideyScenes_EvenSplitFromRenderedDuration(t *testing.T) {
	scenes := make([]slideyScene, 6)
	chs := chaptersFromSlideyScenes(scenes, 0 /*totalMs*/, 59907 /*renderedMs*/, "deck.json")
	if len(chs) != 6 {
		t.Fatalf("got %d chapters", len(chs))
	}
	// No zero-width windows; contiguous; tiles [0, 59907] exactly.
	if chs[0].StartMs != 0 {
		t.Errorf("first window must start at 0, got %d", chs[0].StartMs)
	}
	if chs[len(chs)-1].EndMs != 59907 {
		t.Errorf("last window must end at total 59907, got %d", chs[len(chs)-1].EndMs)
	}
	for i, c := range chs {
		if c.EndMs <= c.StartMs {
			t.Errorf("window %d is zero/negative width: %+v", i, c)
		}
		if i > 0 && c.StartMs != chs[i-1].EndMs {
			t.Errorf("window %d not contiguous: starts %d, prev ended %d", i, c.StartMs, chs[i-1].EndMs)
		}
	}
	// A flag at 0:26 (26000ms) must land inside scene-2 ([19968,29952)), not
	// scene-0 — proving the resolver can no longer collapse everything to
	// scene-0. (evenMs = 59907/6 = 9984.)
	const flagMs = 26000
	var hit *video.Chapter
	for i := range chs {
		if chs[i].StartMs <= flagMs && flagMs < chs[i].EndMs {
			hit = &chs[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("no chapter contains %dms", flagMs)
	}
	if hit.ID != "scene-2" {
		t.Errorf("flag @%dms resolved to %q, want scene-2", flagMs, hit.ID)
	}
}

func TestChaptersFromSlideySpec_NoTimingNoRenderStillMaps(t *testing.T) {
	// With renderedMs=0 (no video probed) the windows may collapse to zero
	// width, but the scene→source map is still complete.
	chs, err := chaptersFromSlideySpec([]byte(`{"scenes":[{"id":"a"},{"id":"b"}]}`), "d.json", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 2 || chs[1].Source.SceneID != "b" {
		t.Errorf("no-timing chapters: %+v", chs)
	}
}

// TestWriteSlideyChapters_ProbeStubbedNoFFmpeg proves the producer path uses
// the injected duration probe (no real ffmpeg/ffprobe in CI) and writes a
// sidecar with real windows.
func TestWriteSlideyChapters_ProbeStubbedNoFFmpeg(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "deck.json")
	if err := os.WriteFile(specPath, []byte(`{"scenes":[{"id":"a"},{"id":"b"},{"id":"c"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(dir, "out.mp4")
	if err := os.WriteFile(videoPath, []byte("not-a-real-mp4"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := probeDurationMs
	probeDurationMs = func(string) (int, error) { return 30000, nil }
	defer func() { probeDurationMs = orig }()

	sidecar, err := writeSlideyChapters(specPath, videoPath)
	if err != nil {
		t.Fatal(err)
	}
	chs, err := video.ReadChapters(videoPath)
	if err != nil {
		t.Fatalf("read sidecar %s: %v", sidecar, err)
	}
	if len(chs) != 3 || chs[2].EndMs != 30000 || chs[0].EndMs != 10000 {
		t.Errorf("expected even 10s windows tiling 30s, got %+v", chs)
	}
}
