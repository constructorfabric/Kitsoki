// Package host — host.video.frame — deterministic single-frame still grab.
//
// Part of the mockup-video-studio epic (docs/architecture/hosts.md). This
// handler is a thin adapter over internal/video.Frame — the ONE extractor
// (epic shared decision 2) that also backs the slice-2 web RPC; ffmpeg is
// invoked from exactly that one place. The handler resolves a video
// handle|path, calls Frame, and hands the resulting PNG path off to the
// substrate (host.artifacts_dir) for registration — it never writes an
// artifact itself, the same discipline the visual-output producers follow.
package host

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"kitsoki/internal/video"
)

// videoFrameExtractor is the injected seam used by VideoFrameHandler so tests
// substitute a fake that copies a fixture PNG and CI never shells out to
// ffmpeg. Production wires it to internal/video.Frame with the default
// os/exec runner.
type videoFrameExtractor func(ctx context.Context, videoPath string, tMs int) (pngPath string, err error)

var videoFrame videoFrameExtractor = func(ctx context.Context, videoPath string, tMs int) (string, error) {
	return video.Frame(ctx, video.DefaultRunner, videoPath, tMs)
}

// SetVideoFrameExtractorForTest installs a fake extractor. Returns a restore
// func. Test-only — production code uses internal/video.Frame via the
// package default.
func SetVideoFrameExtractorForTest(f videoFrameExtractor) func() {
	prev := videoFrame
	videoFrame = f
	return func() { videoFrame = prev }
}

// VideoFrameHandler implements host.video.frame.
//
// Input args:
//   - video (string | map, required): the source video. Either an absolute
//     path string, or a media handle map carrying a "path" field (as emitted
//     by host.slidey.render / host.artifacts_dir).
//   - t_ms  (int, required): timestamp in milliseconds from the start of the
//     video to grab. Canonical addressing is by timestamp; scene/step
//     addressing is sugar the slice-2 RPC resolves before calling (proposal
//     open question 1).
//
// Output Result.Data:
//   - ok   (bool):   true when the frame was extracted.
//   - path (string): absolute path of the grabbed PNG (hand to the substrate
//     via host.artifacts_dir to register).
//   - mime (string): "image/png".
//   - kind (string): "image".
//
// On missing ffmpeg or extraction failure Result.Error is set (no Go error),
// so story on_error: arcs branch rather than crashing.
func VideoFrameHandler(ctx context.Context, args map[string]any) (Result, error) {
	videoPath, err := resolveVideoPath(args["video"])
	if err != nil {
		return Result{Error: fmt.Sprintf("host.video.frame: %v", err)}, nil
	}

	if _, present := args["t_ms"]; !present {
		return Result{Error: "host.video.frame: t_ms argument is required"}, nil
	}
	tMs, ok := coerceMs(args["t_ms"])
	if !ok {
		return Result{Error: fmt.Sprintf("host.video.frame: t_ms must be a number, got %T", args["t_ms"])}, nil
	}
	if tMs < 0 {
		return Result{Error: fmt.Sprintf("host.video.frame: t_ms must be >= 0, got %d", tMs)}, nil
	}

	pngPath, err := videoFrame(ctx, videoPath, tMs)
	if err != nil {
		if errors.Is(err, video.ErrFFmpegNotFound) {
			return Result{Error: "host.video.frame: " + video.ErrFFmpegNotFound.Error()}, nil
		}
		return Result{Error: fmt.Sprintf("host.video.frame: %v", err)}, nil
	}

	return Result{Data: map[string]any{
		"ok":   true,
		"path": pngPath,
		"mime": "image/png",
		"kind": "image",
	}}, nil
}

// coerceMs reads a millisecond count from int/int64/float64/string forms —
// templated `with:` args (e.g. "{{ world.flag_t_ms }}") arrive as strings.
func coerceMs(v any) (int, bool) {
	switch t := v.(type) {
	case int:
		return t, true
	case int64:
		return int(t), true
	case float64:
		return int(t), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(t))
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// resolveVideoPath extracts a filesystem path from the `video` arg, which may
// be a plain path string or a media-handle map carrying a "path" field.
func resolveVideoPath(v any) (string, error) {
	switch t := v.(type) {
	case string:
		p := strings.TrimSpace(t)
		if p == "" {
			return "", errors.New("video argument is required")
		}
		return p, nil
	case map[string]any:
		if p, ok := t["path"].(string); ok && strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p), nil
		}
		return "", errors.New("video handle has no `path` field")
	case nil:
		return "", errors.New("video argument is required")
	default:
		return "", fmt.Errorf("video argument must be a path string or a media handle map, got %T", v)
	}
}
