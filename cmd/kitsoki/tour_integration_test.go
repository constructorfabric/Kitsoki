package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/tour"
	"kitsoki/internal/video"
)

// TestTour_EndToEnd renders a small self-driving tour against the bugfix story
// under the happy_llm flow + demo cassette (no-LLM posture) and asserts the full
// pipeline: the MP4 exists, the chapter sidecar parses and has one chapter per
// rendered step, and there is at least one PNG per step. It is the deterministic
// baseline for the screencast-cadence risk (epic shared decision 3).
//
// GATED. It needs a built SPA (go:embed assets), a Chrome/Chromium, and ffmpeg —
// none guaranteed in CI — so it runs only when KITSOKI_TOUR_E2E=1 and every
// dependency is present; otherwise it skips with a clear reason. NEVER uses a
// real LLM (the flow fixture + cassette stub every host.* call).
func TestTour_EndToEnd(t *testing.T) {
	if os.Getenv("KITSOKI_TOUR_E2E") != "1" {
		t.Skip("set KITSOKI_TOUR_E2E=1 to run the chromedp+ffmpeg tour render")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	requireChrome(t)

	root := repoRootForCmdTest(t)
	storyDir := filepath.Join(root, "stories", "bugfix")
	flow := filepath.Join(storyDir, "flows", "happy_llm.yaml")
	cassette := filepath.Join(storyDir, "flows", "demo.cassette.yaml")
	for _, p := range []string{storyDir, flow, cassette} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("fixture missing: %s", p)
		}
	}

	// A minimal self-driving tour: a centered home intro, the New-session
	// navigation (route-match action), and an interactive explain step that
	// dwells. Exercises navigation + chapters + PNGs + MP4 without coupling to
	// a specific room's intent vocabulary.
	manifest := &tour.TourManifest{
		Export:   "E2E_TOUR_STEPS",
		SpecPath: "test/e2e-tour.yaml",
		Steps: []tour.TourStep{
			{
				ID: "intro", Route: "home", Title: "Intro", Body: "Welcome.",
				Placement: "center", Kind: "explain", Advance: "next",
				WaitForTarget: "home-view", DwellMs: 600,
			},
			{
				ID: "new-session", Route: "home", Target: "new-session-btn",
				Title: "Spin up a run", Body: "Start a session.", Placement: "right",
				Kind: "action", Advance: "route-match", AdvanceRoute: "interactive",
				WaitForTarget: "new-session-btn", DwellMs: 400,
			},
			{
				ID: "chat", Route: "interactive", Target: "chat-transcript",
				Title: "The chat", Body: "Here is the room.", Placement: "left",
				Kind: "explain", Advance: "next", WaitForTarget: "chat-transcript",
				Drive: []tour.DriveAction{{Type: tour.DriveDwellMs, Ms: 300}},
			},
		},
	}

	handler, closeFn, err := buildTourServer([]string{storyDir}, flow, cassette, dbPathForTour())
	if err != nil {
		t.Fatalf("buildTourServer: %v", err)
	}
	defer closeFn()

	outDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	res, err := tour.Run(ctx, tour.Config{
		Manifest:  manifest,
		Handler:   handler,
		OutDir:    outDir,
		VideoBase: "e2e",
		Pace:      0, // instant — fast deterministic render
		Headless:  true,
	})
	if err != nil {
		t.Fatalf("tour.Run: %v", err)
	}

	if res.VideoPath == "" {
		t.Fatal("no MP4 produced")
	}
	if _, err := os.Stat(res.VideoPath); err != nil {
		t.Fatalf("MP4 missing: %v", err)
	}
	chs, err := video.ReadChapters(res.VideoPath)
	if err != nil {
		t.Fatalf("read chapters: %v", err)
	}
	if len(chs) != len(manifest.Steps) {
		t.Errorf("want %d chapters, got %d", len(manifest.Steps), len(chs))
	}
	if len(res.PNGPaths) < len(manifest.Steps) {
		t.Errorf("want >= %d PNGs, got %d", len(manifest.Steps), len(res.PNGPaths))
	}
	if res.FrameCount == 0 {
		t.Error("no screencast frames captured")
	}

	// The deterministic per-step sidecar: one StepShot per captured PNG, each
	// referencing its exact spec location and asserted at capture.
	if res.StepsPath == "" {
		t.Fatal("no steps sidecar path")
	}
	stepsData, err := os.ReadFile(res.StepsPath)
	if err != nil {
		t.Fatalf("read steps sidecar: %v", err)
	}
	var shots []tour.StepShot
	if err := json.Unmarshal(stepsData, &shots); err != nil {
		t.Fatalf("unmarshal steps sidecar: %v", err)
	}
	if len(shots) != len(res.PNGPaths) {
		t.Errorf("want %d step shots, got %d", len(res.PNGPaths), len(shots))
	}
	for i, s := range shots {
		if s.SpecRef.StepID == "" || s.SpecRef.Pointer == "" || s.PNG == "" || !s.TitleAsserted {
			t.Errorf("shot %d under-specified: %+v", i, s)
		}
	}
}

func requireChrome(t *testing.T) {
	t.Helper()
	for _, c := range []string{"google-chrome", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(c); err == nil {
			return
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return
		}
	}
	t.Skip("no Chrome/Chromium found")
}

func repoRootForCmdTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "features")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repo root not found")
		}
		dir = parent
	}
}
