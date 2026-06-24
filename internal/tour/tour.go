package tour

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	"kitsoki/internal/video"
)

// Config holds everything Run needs to render a tour. The HTTP handler that
// serves the embedded SPA + RPC surface is INJECTED (Handler), because the
// concrete SessionRegistry lives in package main and an internal package cannot
// import it — cmd/kitsoki/tour.go builds the no-LLM server (reusing web.go's
// runtimeBase + registry plumbing) and hands its Handler in. This is the DI
// seam the proposal calls out.
type Config struct {
	// Manifest is the parsed tour (from a feature catalog or a --manifest file).
	Manifest *TourManifest
	// Handler serves the SPA + RPC + SSE surface the browser drives. Required.
	Handler http.Handler
	// OutDir receives <VideoBase>.mp4 + .chapters.json + per-step PNGs.
	OutDir string
	// VideoBase is the output base name (<VideoBase>.mp4). Defaults to "tour".
	VideoBase string
	// Pace scales every dwell/reveal (0 = instant, 1 = watch speed).
	Pace float64
	// Headless launches Chrome headless (default true; set false to debug).
	Headless bool
	// Viewport sizes the browser / video. Zero falls back to 1600x900.
	ViewportW, ViewportH int
	// FPS is the stitched MP4 frame rate. Zero falls back to 30.
	FPS int
	// Runner is the injected ffmpeg runner (DI for tests). Nil = DefaultRunner.
	Runner video.Runner
	// ChromePath overrides the Chrome/Chromium executable. Empty = auto-discover.
	ChromePath string
}

// Result reports what Run produced.
type Result struct {
	VideoPath    string          // <OutDir>/<VideoBase>.mp4 (empty if ffmpeg absent/failed)
	ChaptersPath string          // <OutDir>/<VideoBase>.mp4.chapters.json
	StepsPath    string          // <OutDir>/<VideoBase>.mp4.steps.json
	Chapters     []video.Chapter // the per-step chapters
	Steps        []StepShot      // per-step PNG → spec-location records (deterministic)
	PNGPaths     []string        // per-step poster PNG paths (derived from Steps)
	FrameCount   int             // screencast frames captured
}

// Run renders the tour: it serves the injected Handler on a localhost listener,
// launches headless Chrome, navigates home, injects window.__startTourWithSteps,
// walks each step (executing its drive[] actions and opening a chapter), captures
// the screencast, then stitches an MP4 + writes the chapter sidecar + per-step
// PNGs. It is fully no-LLM: the determinism comes from the flow/cassette posture
// baked into the injected Handler.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Manifest == nil {
		return nil, errors.New("tour.Run: Manifest is required")
	}
	if cfg.Handler == nil {
		return nil, errors.New("tour.Run: Handler is required")
	}
	if cfg.VideoBase == "" {
		cfg.VideoBase = "tour"
	}
	if cfg.OutDir == "" {
		return nil, errors.New("tour.Run: OutDir is required")
	}
	if cfg.ViewportW == 0 {
		cfg.ViewportW = 1600
	}
	if cfg.ViewportH == 0 {
		cfg.ViewportH = 900
	}
	if cfg.FPS == 0 {
		cfg.FPS = 30
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("create out dir: %w", err)
	}

	// ── Serve the SPA on a localhost listener (OS-chosen port; no contention) ──
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	srv := &http.Server{Handler: cfg.Handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	base := "http://" + ln.Addr().String()
	if err := waitHealthy(ctx, base, 20*time.Second); err != nil {
		return nil, err
	}

	// ── Launch headless Chrome ────────────────────────────────────────────────
	allocOpts := append([]chromedp.ExecAllocatorOption{},
		chromedp.Flag("headless", cfg.Headless),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.WindowSize(cfg.ViewportW, cfg.ViewportH),
	)
	if cfg.ChromePath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(cfg.ChromePath))
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, append(chromedp.DefaultExecAllocatorOptions[:], allocOpts...)...)
	defer allocCancel()
	// Drop the benign "unknown <enum> value" unmarshal warnings cdproto emits
	// when Chrome sends a CDP enum value newer than the pinned cdproto knows
	// (e.g. IPAddressSpace=Loopback) — they do not affect the render. Real
	// errors still surface via the Run/chromedp.Run error returns.
	browserCtx, browserCancel := chromedp.NewContext(allocCtx, chromedp.WithErrorf(filteredErrorf))
	defer browserCancel()

	// A render budget so a stuck wait-state cannot hang forever.
	runCtx, runCancel := context.WithTimeout(browserCtx, 5*time.Minute)
	defer runCancel()

	// Force-start the browser so the screencast attaches to a live target.
	if err := chromedp.Run(runCtx); err != nil {
		return nil, fmt.Errorf("start browser: %w", err)
	}

	// ── Start the screencast capture ──────────────────────────────────────────
	cap, err := newScreencastCapturer(cfg.OutDir, cfg.Runner)
	if err != nil {
		return nil, err
	}

	chapters := newChapterRecorder(cfg.Manifest.SpecPath)

	// ── Navigate home + inject the tour ───────────────────────────────────────
	if err := chromedp.Run(runCtx,
		chromedp.Navigate(base+"/#/"),
		chromedp.WaitVisible(`[data-testid="home-view"]`, chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("navigate home: %w", err)
	}
	if err := cap.start(runCtx); err != nil {
		return nil, fmt.Errorf("start screencast: %w", err)
	}
	defer cap.stop(runCtx)

	if err := startTour(runCtx, cfg.Manifest.Steps); err != nil {
		return nil, err
	}

	// ── Walk the steps ────────────────────────────────────────────────────────
	exec := newExecutor(runCtx, cfg.Pace)
	shots, err := walkSteps(runCtx, cfg, exec, chapters, cap)
	if err != nil {
		return nil, err
	}

	// ── Seal chapters; stitch the MP4; write the sidecars ─────────────────────
	cap.stop(runCtx)
	chs := chapters.list()
	pngs := make([]string, len(shots))
	for i, s := range shots {
		pngs[i] = filepath.Join(cfg.OutDir, s.PNG)
	}
	res := &Result{Chapters: chs, Steps: shots, PNGPaths: pngs, FrameCount: cap.frameCount()}

	videoPath := filepath.Join(cfg.OutDir, cfg.VideoBase+".mp4")
	stitchErr := cap.stitch(ctx, videoPath, cfg.FPS)
	if stitchErr != nil {
		// Honest partial: a render with no ffmpeg still produced PNGs + sidecars.
		// Surface the stitch error but keep the chapter + step sidecars (written
		// against the would-be MP4 path so the sibling convention holds).
		if sidecar, werr := video.WriteChapters(videoPath, chs); werr == nil {
			res.ChaptersPath = sidecar
		}
		if steps, werr := writeStepShots(videoPath, shots); werr == nil {
			res.StepsPath = steps
		}
		return res, fmt.Errorf("stitch video: %w", stitchErr)
	}
	res.VideoPath = videoPath
	sidecar, err := video.WriteChapters(videoPath, chs)
	if err != nil {
		return res, fmt.Errorf("write chapters: %w", err)
	}
	res.ChaptersPath = sidecar
	steps, err := writeStepShots(videoPath, shots)
	if err != nil {
		return res, fmt.Errorf("write step shots: %w", err)
	}
	res.StepsPath = steps
	return res, nil
}

// filteredErrorf is chromedp's error logger with the benign cdproto
// enum-unmarshal warnings suppressed (see WithErrorf at the call site).
func filteredErrorf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "could not unmarshal event") {
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}

// waitHealthy polls base/ until it returns 200 or the deadline passes.
func waitHealthy(ctx context.Context, base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("server not healthy after %s (last: %v)", timeout, lastErr)
}
