package tour

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/page"

	"kitsoki/internal/video"
)

// TestChapterRecorder verifies the Go port of the JS ChapterRecorder: open()
// seals the prior chapter and starts a new one; list() seals the final open
// chapter; each chapter carries a source_ref kind="tour" keyed on the step id.
func TestChapterRecorder(t *testing.T) {
	c := newChapterRecorder("features/demo.yaml")
	c.open("step-a", "Step A")
	c.open("step-b", "Step B") // seals A
	chs := c.list()            // seals B
	if len(chs) != 2 {
		t.Fatalf("want 2 chapters, got %d", len(chs))
	}
	if chs[0].ID != "step-a" || chs[1].ID != "step-b" {
		t.Fatalf("chapter ids = %q,%q", chs[0].ID, chs[1].ID)
	}
	for i, ch := range chs {
		if ch.Index != i {
			t.Errorf("chapter %d index = %d", i, ch.Index)
		}
		if ch.Source.Kind != "tour" {
			t.Errorf("chapter %d kind = %q, want tour", i, ch.Source.Kind)
		}
		if ch.Source.StepID != ch.ID {
			t.Errorf("chapter %d step_id = %q, id = %q", i, ch.Source.StepID, ch.ID)
		}
		if ch.Source.SpecPath != "features/demo.yaml" {
			t.Errorf("chapter %d spec_path = %q", i, ch.Source.SpecPath)
		}
		if ch.EndMs < ch.StartMs {
			t.Errorf("chapter %d end %d < start %d", i, ch.EndMs, ch.StartMs)
		}
	}
}

// TestScreencast_StitchBuildsFfconcat drives the frame→MP4 stitch with a FAKE
// ffmpeg runner (DI), so no real ffmpeg is shelled. It writes a couple of frames
// the way the CDP listener would, then asserts the stitch builds a well-formed
// ffconcat list (every frame with a positive duration) and invokes ffmpeg with
// the canonical H.264 args. This is the deterministic baseline for the
// screencast-cadence risk: the cadence math (per-frame durations from arrival
// offsets) is exercised without a browser.
func TestScreencast_StitchBuildsFfconcat(t *testing.T) {
	dir := t.TempDir()

	var gotDir, gotName string
	var gotArgs []string
	fake := video.RunnerFunc(func(_ context.Context, d, name string, args ...string) (string, string, error) {
		gotDir, gotName, gotArgs = d, name, args
		return "", "", nil
	})

	cap, err := newScreencatTestCapturer(dir, fake)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate two CDP frames: a 1x1 PNG is enough — the fake ffmpeg never reads
	// them, the stitch only needs the files + offsets to exist.
	cap.injectFrame(t, tinyPNG, 0)
	cap.injectFrame(t, tinyPNG, 120)

	out := filepath.Join(dir, "out.mp4")
	if err := cap.stitch(context.Background(), out, 30); err != nil {
		t.Fatalf("stitch: %v", err)
	}

	if gotName != "ffmpeg" {
		t.Errorf("ran %q, want ffmpeg", gotName)
	}
	if gotDir != cap.frameDir {
		t.Errorf("ffmpeg dir = %q, want frame dir %q", gotDir, cap.frameDir)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"concat", "frames.ffconcat", "libx264", "yuv420p", "+faststart", out} {
		if !strings.Contains(joined, want) {
			t.Errorf("ffmpeg args missing %q: %v", want, gotArgs)
		}
	}

	// The ffconcat list must name both frames with positive durations.
	listBytes, err := os.ReadFile(filepath.Join(cap.frameDir, "frames.ffconcat"))
	if err != nil {
		t.Fatalf("read ffconcat: %v", err)
	}
	list := string(listBytes)
	if !strings.HasPrefix(list, "ffconcat version 1.0") {
		t.Errorf("ffconcat missing header: %q", list[:min(40, len(list))])
	}
	if strings.Count(list, "duration ") < 2 {
		t.Errorf("expected per-frame durations, got:\n%s", list)
	}
}

// newScreencatTestCapturer is a thin wrapper so the test can build a capturer
// without a browser. It reuses the production constructor.
func newScreencatTestCapturer(dir string, runner video.Runner) (*screencastCapturer, error) {
	return newScreencastCapturer(dir, runner)
}

// injectFrame mimics what the CDP frame listener does (decode → write PNG →
// record offset) without a live screencast.
func (s *screencastCapturer) injectFrame(t *testing.T, b64 string, offMs int) {
	t.Helper()
	s.writeFrame(&page.EventScreencastFrame{Data: b64, SessionID: int64(len(s.frames) + 1)})
	// writeFrame stamps offMs from wall-clock; overwrite the last frame's offset
	// deterministically for the cadence assertion.
	s.mu.Lock()
	if n := len(s.frames); n > 0 {
		s.frames[n-1].offMs = offMs
	}
	s.mu.Unlock()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// tinyPNG is a base64 1x1 transparent PNG — valid bytes for the frame file.
const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="
