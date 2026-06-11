// Package video is the deterministic video frame seam for the mockup-video
// studio epic (docs/architecture/hosts.md).
//
// It does exactly two pure/deterministic things and no interpretation:
//
//  1. Frame — grab a single still PNG from a video at a timestamp, via
//     ffmpeg (`ffmpeg -ss <t> -i <video> -frames:v 1 <out.png>`). This is
//     the ONE extractor (epic shared decision 2): both the host.video.frame
//     handler (internal/host) and the slice-2 web RPC call Frame; ffmpeg is
//     invoked from exactly one place. The runner is injected (a Runner), so
//     tests substitute a fake that copies a fixture PNG and CI never shells
//     out to ffmpeg.
//
//  2. Chapters — a producer-agnostic sidecar mapping each moment of a video
//     back to the scene/step that produced it. Every producer (slidey deck,
//     tour walkthrough) emits the SAME shape so the feedback panel reads one
//     uniform chapter list and the refine step dispatches on
//     SourceRef.Kind (epic shared decision 1). The sidecar is a sibling file
//     (<video>.chapters.json) read/written by the helpers here.
//
// # Non-goals
//
//   - Recording artifacts — the visual-outputs substrate owns the single
//     write site; Frame returns a path, it does not register a datapoint.
//   - Producing the video — that is the slidey/tour producers.
//   - Re-encoding / trimming / concatenating — single-frame extraction only.
package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Runner runs an external command and reports a non-zero-exit / spawn
// failure as an error. It mirrors the os/exec contract narrowly so tests
// can substitute a fake without importing os/exec. dir is the working
// directory ("" = process cwd).
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, dir, name string, args ...string) (string, string, error)

// Run implements Runner.
func (f RunnerFunc) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	return f(ctx, dir, name, args...)
}

// ErrFFmpegNotFound is returned by Frame when the ffmpeg binary is not on
// PATH. Callers (the host handler / web RPC) translate this into a clear
// Result.Error rather than crashing, the same graceful-degradation contract
// the visual-output producers follow.
var ErrFFmpegNotFound = errors.New("ffmpeg not found — install ffmpeg and ensure it is on PATH")

// execRunner is the production Runner: it shells out via os/exec.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	stderr := ""
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		stderr = string(ee.Stderr)
	}
	return string(out), stderr, err
}

// DefaultRunner is the production Runner backed by os/exec. Inject a fake in
// tests via Frame's runner parameter; never reach for this in CI paths.
var DefaultRunner Runner = execRunner{}

// lookFFmpeg is the ffmpeg-on-PATH probe; overridable in tests via
// SetLookFFmpegForTest so the not-found path can be exercised without
// mutating the host PATH.
var lookFFmpeg = func() error {
	_, err := exec.LookPath("ffmpeg")
	return err
}

// Frame grabs a single still PNG from videoPath at tMs (milliseconds from
// the start) and writes it to a freshly-created temp PNG, returning its
// absolute path. The extraction is deterministic: the same (video, tMs)
// yields the same PNG bytes.
//
// runner is the injected command runner; pass DefaultRunner in production
// and a fixture-copying fake in tests. A nil runner falls back to
// DefaultRunner so the host handler need not thread it when it has none.
//
// Returns ErrFFmpegNotFound when ffmpeg is absent. Mirrors the producers'
// PATH-based discovery (no env override needed — ffmpeg is assumed on PATH
// exactly like contact-sheet and slidey).
func Frame(ctx context.Context, runner Runner, videoPath string, tMs int) (pngPath string, err error) {
	if runner == nil {
		runner = DefaultRunner
	}
	if videoPath == "" {
		return "", errors.New("video.Frame: video path is required")
	}
	if tMs < 0 {
		return "", fmt.Errorf("video.Frame: t_ms must be >= 0, got %d", tMs)
	}
	if err := lookFFmpeg(); err != nil {
		return "", ErrFFmpegNotFound
	}

	absVideo, _ := filepath.Abs(videoPath)

	out, err := os.CreateTemp("", "kitsoki-frame-*.png")
	if err != nil {
		return "", fmt.Errorf("video.Frame: create temp png: %w", err)
	}
	outPath := out.Name()
	_ = out.Close()

	// ffmpeg -ss <seconds> -i <video> -frames:v 1 -y <out.png>
	// -ss before -i is an input seek (fast); milliseconds expressed as a
	// fractional second keeps the call deterministic for any t_ms.
	seek := strconv.FormatFloat(float64(tMs)/1000.0, 'f', 3, 64)
	_, stderr, runErr := runner.Run(ctx, "",
		"ffmpeg",
		"-ss", seek,
		"-i", absVideo,
		"-frames:v", "1",
		"-y",
		outPath,
	)
	if runErr != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("video.Frame: ffmpeg failed: %w: %s", runErr, stderr)
	}
	return outPath, nil
}

// ProbeDurationMs returns the duration of videoPath in milliseconds, via
// ffprobe. It is used to distribute chapter windows evenly across scenes when
// the slidey spec carries no per-scene timing. Returns 0 (and a nil error from
// the host-level caller's perspective) is not done here — a probe failure is
// surfaced so the caller can decide; callers that treat 0 as "unknown" should
// ignore the error.
func ProbeDurationMs(videoPath string) (int, error) {
	if videoPath == "" {
		return 0, errors.New("video.ProbeDurationMs: video path is required")
	}
	absVideo, _ := filepath.Abs(videoPath)
	// ffprobe -v error -show_entries format=duration -of csv=p=0 <video>
	stdout, stderr, runErr := DefaultRunner.Run(context.Background(), "",
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		absVideo,
	)
	if runErr != nil {
		return 0, fmt.Errorf("video.ProbeDurationMs: ffprobe failed: %w: %s", runErr, stderr)
	}
	secs, perr := strconv.ParseFloat(strings.TrimSpace(stdout), 64)
	if perr != nil {
		return 0, fmt.Errorf("video.ProbeDurationMs: parse duration %q: %w", stdout, perr)
	}
	return int(secs * 1000), nil
}

// ── Chapter sidecar ─────────────────────────────────────────────────────────

// SourceRef names the producing unit a chapter came from, so a flagged
// moment resolves back to an editable source (epic shared decision 1).
type SourceRef struct {
	// Kind is the producer: "slidey" or "tour".
	Kind string `json:"kind"`
	// SpecPath is the producer's source file (slidey JSON spec / tour spec).
	SpecPath string `json:"spec_path"`
	// SceneID is set when Kind == "slidey" (the scene's id/index).
	SceneID string `json:"scene_id,omitempty"`
	// StepID is set when Kind == "tour" (the TourStep's id).
	StepID string `json:"step_id,omitempty"`
	// Line is the optional 1-based line in SpecPath the unit starts at.
	Line int `json:"line,omitempty"`
}

// Chapter maps a [StartMs, EndMs) window of the video to its SourceRef.
type Chapter struct {
	Index   int       `json:"index"`
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	StartMs int       `json:"start_ms"`
	EndMs   int       `json:"end_ms"`
	Source  SourceRef `json:"source_ref"`
}

// SidecarPath returns the canonical sidecar path for a video:
// "<video>.chapters.json" (epic cross-cutting Q1 — sibling file).
func SidecarPath(videoPath string) string {
	return videoPath + ".chapters.json"
}

// WriteChapters writes chapters to videoPath's sidecar (<video>.chapters.json)
// as a JSON array, returning the sidecar path. The video's media handle may
// carry this path as an optional chapters_handle.
func WriteChapters(videoPath string, chapters []Chapter) (sidecarPath string, err error) {
	if chapters == nil {
		chapters = []Chapter{}
	}
	b, err := json.MarshalIndent(chapters, "", "  ")
	if err != nil {
		return "", fmt.Errorf("video.WriteChapters: marshal: %w", err)
	}
	path := SidecarPath(videoPath)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("video.WriteChapters: write %s: %w", path, err)
	}
	return path, nil
}

// ReadChapters reads and parses a video's chapter sidecar. A missing sidecar
// is reported via os.IsNotExist(err) so callers can degrade to "no chapters"
// rather than failing (slice-2 contract: a video without a sidecar still
// plays).
func ReadChapters(videoPath string) ([]Chapter, error) {
	b, err := os.ReadFile(SidecarPath(videoPath))
	if err != nil {
		return nil, err
	}
	var chapters []Chapter
	if err := json.Unmarshal(b, &chapters); err != nil {
		return nil, fmt.Errorf("video.ReadChapters: parse %s: %w", SidecarPath(videoPath), err)
	}
	return chapters, nil
}
