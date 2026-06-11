package host_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/video"
)

// TestVideoFrameHandler_StubbedExtractor exercises the handler with the
// extractor stubbed to return a checked-in 1x1 fixture PNG — no ffmpeg in CI.
func TestVideoFrameHandler_StubbedExtractor(t *testing.T) {
	const fixture = "testdata/frame-1x1.png"
	restore := host.SetVideoFrameExtractorForTest(func(_ context.Context, videoPath string, tMs int) (string, error) {
		if videoPath != "out.mp4" {
			t.Errorf("resolved video path = %q", videoPath)
		}
		if tMs != 14_300 {
			t.Errorf("t_ms = %d", tMs)
		}
		return fixture, nil
	})
	defer restore()

	res, err := host.VideoFrameHandler(context.Background(), map[string]any{
		"video": "out.mp4",
		"t_ms":  14_300,
	})
	if err != nil {
		t.Fatalf("Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Result.Error: %s", res.Error)
	}
	if res.Data["path"] != fixture {
		t.Errorf("path = %v", res.Data["path"])
	}
	if res.Data["mime"] != "image/png" || res.Data["kind"] != "image" || res.Data["ok"] != true {
		t.Errorf("unexpected data: %+v", res.Data)
	}
	if _, statErr := os.Stat(fixture); statErr != nil {
		t.Errorf("fixture missing: %v", statErr)
	}
}

func TestVideoFrameHandler_ResolvesHandleMap(t *testing.T) {
	restore := host.SetVideoFrameExtractorForTest(func(_ context.Context, videoPath string, _ int) (string, error) {
		if videoPath != "/x/out.mp4" {
			t.Errorf("path from handle = %q", videoPath)
		}
		return "testdata/frame-1x1.png", nil
	})
	defer restore()

	res, _ := host.VideoFrameHandler(context.Background(), map[string]any{
		"video": map[string]any{"id": "h#1", "path": "/x/out.mp4"},
		"t_ms":  0,
	})
	if res.Error != "" {
		t.Fatalf("Result.Error: %s", res.Error)
	}
}

func TestVideoFrameHandler_FFmpegNotFound(t *testing.T) {
	restore := host.SetVideoFrameExtractorForTest(func(_ context.Context, _ string, _ int) (string, error) {
		return "", video.ErrFFmpegNotFound
	})
	defer restore()

	res, err := host.VideoFrameHandler(context.Background(), map[string]any{"video": "out.mp4", "t_ms": 0})
	if err != nil {
		t.Fatalf("Go error: %v", err)
	}
	if res.Error == "" || !strings.Contains(res.Error, "ffmpeg not found") {
		t.Errorf("want ffmpeg-not-found Result.Error, got %q", res.Error)
	}
}

func TestVideoFrameHandler_ValidationErrors(t *testing.T) {
	cases := []struct{ name string; args map[string]any }{
		{"missing video", map[string]any{"t_ms": 0}},
		{"missing t_ms", map[string]any{"video": "out.mp4"}},
		{"handle no path", map[string]any{"video": map[string]any{"id": "h"}, "t_ms": 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := host.VideoFrameHandler(context.Background(), c.args)
			if err != nil {
				t.Fatalf("Go error: %v", err)
			}
			if res.Error == "" {
				t.Errorf("expected Result.Error for %s", c.name)
			}
		})
	}
}

