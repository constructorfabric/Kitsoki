package tour

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"

	"kitsoki/internal/video"
)

// chapterRecorder accumulates per-step [start,end) windows during a tour
// render, the Go port of the JS ChapterRecorder (server.ts). Elapsed wall-clock
// since the recorder's t0 is the video timeline, so the windows line up with
// the stitched MP4. open() seals the prior chapter and starts a new one; list()
// seals the final open chapter and returns the collected chapters.
type chapterRecorder struct {
	t0       time.Time
	mu       sync.Mutex
	chapters []video.Chapter
	cur      *openChapter
	specPath string
}

type openChapter struct {
	id      string
	label   string
	startMs int
}

func newChapterRecorder(specPath string) *chapterRecorder {
	return &chapterRecorder{t0: time.Now(), specPath: specPath}
}

// elapsedMs is the video-timeline position now.
func (c *chapterRecorder) elapsedMs() int { return int(time.Since(c.t0).Milliseconds()) }

// open begins a chapter for stepID, sealing any currently-open one first.
func (c *chapterRecorder) open(stepID, label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
	c.cur = &openChapter{id: stepID, label: label, startMs: c.elapsedMs()}
}

func (c *chapterRecorder) closeLocked() {
	if c.cur == nil {
		return
	}
	o := c.cur
	c.chapters = append(c.chapters, video.Chapter{
		Index:   len(c.chapters),
		ID:      o.id,
		Label:   o.label,
		StartMs: o.startMs,
		EndMs:   c.elapsedMs(),
		Source: video.SourceRef{
			Kind:     "tour",
			SpecPath: c.specPath,
			StepID:   o.id,
		},
	})
	c.cur = nil
}

// list seals the final open chapter and returns the collected chapters.
func (c *chapterRecorder) list() []video.Chapter {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeLocked()
	out := make([]video.Chapter, len(c.chapters))
	copy(out, c.chapters)
	return out
}

// screencastCapturer streams Page.startScreencast frames into numbered PNGs on
// disk, then stitches them into an H.264 MP4 with ffmpeg. Frame delivery from
// CDP is wall-clock-variable (epic shared decision 3 / screencast-cadence
// risk), so each frame carries its CDP metadata timestamp; the MP4 is stitched
// at a fixed output fps from the ordered frames, which keeps a steady cadence
// regardless of delivery jitter.
//
// The capturer is started before the tour walk and stopped after, around the
// chromedp browser context. It must run on the SAME chromedp context the tour
// drives, so the screencast targets the driven page.
type screencastCapturer struct {
	frameDir string
	runner   video.Runner

	mu     sync.Mutex
	frames []capturedFrame
	t0     time.Time
}

type capturedFrame struct {
	seq    int
	offMs  int // ms from t0 (CDP metadata timestamp preferred, else arrival)
	pngHnd string
}

// newScreencastCapturer prepares a fresh frame directory under parent.
func newScreencastCapturer(parent string, runner video.Runner) (*screencastCapturer, error) {
	dir := filepath.Join(parent, "frames")
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("clear frame dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create frame dir: %w", err)
	}
	if runner == nil {
		runner = video.DefaultRunner
	}
	return &screencastCapturer{frameDir: dir, runner: runner}, nil
}

// start begins the screencast on ctx (a chromedp context). It registers a
// frame listener that decodes each base64 PNG to disk and ACKs it (so Chrome
// keeps streaming), then issues Page.startScreencast. The listener runs until
// the context is cancelled.
func (s *screencastCapturer) start(ctx context.Context) error {
	s.t0 = time.Now()
	chromedp.ListenTarget(ctx, func(ev any) {
		fr, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		// ACK immediately so Chrome continues streaming; do it in a goroutine
		// off the event-dispatch path (chromedp serialises listener calls).
		go func(sessionID int64) {
			_ = chromedp.Run(ctx, page.ScreencastFrameAck(sessionID))
		}(fr.SessionID)
		s.writeFrame(fr)
	})
	return chromedp.Run(ctx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatPng).
			WithEveryNthFrame(1),
	)
}

func (s *screencastCapturer) writeFrame(fr *page.EventScreencastFrame) {
	raw, err := base64.StdEncoding.DecodeString(fr.Data)
	if err != nil {
		return
	}
	s.mu.Lock()
	seq := len(s.frames)
	off := int(time.Since(s.t0).Milliseconds())
	s.mu.Unlock()

	name := filepath.Join(s.frameDir, fmt.Sprintf("frame-%06d.png", seq))
	if err := os.WriteFile(name, raw, 0o644); err != nil {
		return
	}
	s.mu.Lock()
	s.frames = append(s.frames, capturedFrame{seq: seq, offMs: off, pngHnd: name})
	s.mu.Unlock()
}

// stop ends the screencast. Best-effort: a stop error after a successful walk
// should not fail the render.
func (s *screencastCapturer) stop(ctx context.Context) {
	_ = chromedp.Run(ctx, page.StopScreencast())
}

// frameCount is the number of frames captured so far.
func (s *screencastCapturer) frameCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

// stitch encodes the captured PNG frames into an H.264 MP4 at outPath using
// ffmpeg, mirroring the Playwright spec's saveVideoAsMp4 settings (libx264 /
// preset slow / crf 20 / yuv420p / +faststart). The frames are fed in capture
// order via an ffconcat list whose per-frame durations come from the CDP
// arrival offsets, so a frame Chrome held (no repaint) holds in the MP4 too —
// matching what the viewer saw. Returns video.ErrFFmpegNotFound semantics
// implicitly via the runner error when ffmpeg is absent.
func (s *screencastCapturer) stitch(ctx context.Context, outPath string, fps int) error {
	s.mu.Lock()
	frames := make([]capturedFrame, len(s.frames))
	copy(frames, s.frames)
	s.mu.Unlock()

	if len(frames) == 0 {
		return fmt.Errorf("no frames captured")
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].seq < frames[j].seq })

	// Build an ffconcat demuxer list: each frame followed by its duration (the
	// gap to the next frame's arrival offset). The final frame gets a default
	// hold. ffconcat needs the last file repeated for the final duration to
	// apply.
	var b []byte
	b = append(b, []byte("ffconcat version 1.0\n")...)
	for i, f := range frames {
		b = append(b, []byte(fmt.Sprintf("file '%s'\n", filepath.Base(f.pngHnd)))...)
		var durMs int
		if i+1 < len(frames) {
			durMs = frames[i+1].offMs - f.offMs
		} else {
			durMs = 500
		}
		if durMs < 1 {
			durMs = 1
		}
		b = append(b, []byte(fmt.Sprintf("duration %.3f\n", float64(durMs)/1000.0))...)
	}
	// Repeat the last file so its duration is honored by the concat demuxer.
	b = append(b, []byte(fmt.Sprintf("file '%s'\n", filepath.Base(frames[len(frames)-1].pngHnd)))...)

	listPath := filepath.Join(s.frameDir, "frames.ffconcat")
	if err := os.WriteFile(listPath, b, 0o644); err != nil {
		return fmt.Errorf("write ffconcat list: %w", err)
	}

	// ffmpeg -f concat -i frames.ffconcat -vf "fps,scale even" -c:v libx264 ...
	vf := fmt.Sprintf("fps=%d,scale=trunc(iw/2)*2:trunc(ih/2)*2", fps)
	_, stderr, err := s.runner.Run(ctx, s.frameDir,
		"ffmpeg",
		"-y", "-loglevel", "error",
		"-f", "concat",
		"-safe", "0",
		"-i", "frames.ffconcat",
		"-vf", vf,
		"-c:v", "libx264",
		"-preset", "slow",
		"-crf", "20",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-an",
		outPath,
	)
	if err != nil {
		return fmt.Errorf("ffmpeg stitch failed: %w: %s", err, stderr)
	}
	return nil
}
