package video_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/video"
)

// fakeRunner records the argv it was asked to run and copies a fixture PNG to
// the requested output, so Frame's plumbing is exercised without ffmpeg.
type fakeRunner struct {
	fixture string
	gotArgs []string
}

func (f *fakeRunner) Run(_ context.Context, _ string, _ string, args ...string) (string, string, error) {
	f.gotArgs = args
	out := args[len(args)-1] // last positional is the output png
	b, err := os.ReadFile(f.fixture)
	if err != nil {
		return "", "", err
	}
	return "", "", os.WriteFile(out, b, 0o644)
}

func TestFrame_CopiesFixtureAndPassesSeek(t *testing.T) {
	t.Parallel()
	video.SetLookFFmpegForTest(t, func() error { return nil })

	fr := &fakeRunner{fixture: "testdata/frame-1x1.png"}
	png, err := video.Frame(context.Background(), fr, "in.mp4", 14_300)
	if err != nil {
		t.Fatalf("Frame: %v", err)
	}
	defer os.Remove(png)

	if filepath.Ext(png) != ".png" {
		t.Errorf("png path %q has no .png ext", png)
	}
	if _, err := os.Stat(png); err != nil {
		t.Errorf("png not written: %v", err)
	}
	// 14_300 ms → "14.300" second seek.
	want := "14.300"
	found := false
	for i, a := range fr.gotArgs {
		if a == "-ss" && i+1 < len(fr.gotArgs) && fr.gotArgs[i+1] == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected -ss %s in argv, got %v", want, fr.gotArgs)
	}
}

func TestFrame_FFmpegNotFound(t *testing.T) {
	t.Parallel()
	video.SetLookFFmpegForTest(t, func() error { return os.ErrNotExist })

	_, err := video.Frame(context.Background(), &fakeRunner{}, "in.mp4", 0)
	if err != video.ErrFFmpegNotFound {
		t.Fatalf("want ErrFFmpegNotFound, got %v", err)
	}
}

func TestFrame_RejectsBadInput(t *testing.T) {
	t.Parallel()
	video.SetLookFFmpegForTest(t, func() error { return nil })
	if _, err := video.Frame(context.Background(), &fakeRunner{}, "", 0); err == nil {
		t.Error("empty video path should error")
	}
	if _, err := video.Frame(context.Background(), &fakeRunner{}, "in.mp4", -1); err == nil {
		t.Error("negative t_ms should error")
	}
}

func TestChaptersRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	vid := filepath.Join(dir, "out.mp4")

	want := []video.Chapter{
		{Index: 0, ID: "intro", Label: "Intro", StartMs: 0, EndMs: 5000,
			Source: video.SourceRef{Kind: "slidey", SpecPath: "deck.json", SceneID: "intro"}},
		{Index: 1, ID: "step-1", Label: "Open", StartMs: 5000, EndMs: 9000,
			Source: video.SourceRef{Kind: "tour", SpecPath: "tour.json", StepID: "step-1"}},
	}
	path, err := video.WriteChapters(vid, want)
	if err != nil {
		t.Fatal(err)
	}
	if path != vid+".chapters.json" {
		t.Errorf("sidecar path = %q", path)
	}

	got, err := video.ReadChapters(vid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "intro" || got[1].Source.StepID != "step-1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestReadChapters_MissingIsNotExist(t *testing.T) {
	t.Parallel()
	_, err := video.ReadChapters(filepath.Join(t.TempDir(), "nope.mp4"))
	if !os.IsNotExist(err) {
		t.Fatalf("want IsNotExist, got %v", err)
	}
}
