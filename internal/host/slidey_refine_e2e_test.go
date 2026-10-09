package host_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestSlideyRefineChangesHandleAndBytes is the REAL end-to-end regression for
// the slidey-edit refine loop's "the deck never updates" bug. Every existing
// flow/cassette stubs host.slidey.render AND host.artifacts_dir with a FIXED
// handle ("slidey-edit#1"), so none of them exercise the chain that actually
// breaks in the running web UI:
//
//	edit the spec on disk → re-render to the SAME html path → re-emit →
//	the artifact handle must change (content-addressed) → the iframe src
//	changes → the browser drops the stale cached render.
//
// This drives the genuine builtins (real `slidey` CLI + real artifacts_dir
// transport) over an actual scene-element edit and asserts BOTH the rendered
// bytes and the emitted handle change. If artifactHandleID ever regresses to
// hashing the destination path again, the handle stays constant here and this
// test fails — exactly the user-visible "still not updated" symptom.
//
// Gated on `slidey` being resolvable (PATH or SLIDEY_HOME). It runs the real
// renderer (node) but incurs NO LLM cost, so it's safe under `make test` where
// slidey is present and skips cleanly where it isn't.
func TestSlideyRefineChangesHandleAndBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("real slidey render e2e is skipped under -short")
	}
	if !slideyAvailable() {
		t.Skip("slidey not on PATH and SLIDEY_HOME unset — skipping real-render e2e")
	}

	// Work against a private copy of the baked deck so the test never mutates
	// the committed fixture.
	repoRoot := repoRootFromTest(t)
	bakedSpec := filepath.Join(repoRoot, "stories", "slidey-edit", "baked", "deck.json")
	srcBytes, err := os.ReadFile(bakedSpec)
	if err != nil {
		t.Fatalf("read baked deck: %v", err)
	}

	work := t.TempDir()
	specPath := filepath.Join(work, "deck.json")
	if err := os.WriteFile(specPath, srcBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	artifactsRoot := filepath.Join(work, "artifacts")

	// slidey's `bundle` step synthesizes narration audio for every deck scene
	// via a live edge-tts network call (see slidey/web/build-narration-audio.mjs)
	// with NO caching — two bundles of the byte-identical spec each get a fresh
	// TTS response, and the neural voice service is not bit-reproducible across
	// calls. That made this test flake on the "unchanged spec -> unchanged
	// handle" sanity check for a reason that has nothing to do with the
	// content-addressing this test exists to guard: the rendered HTML really
	// did change, but only because of nondeterministic narration audio, not a
	// hashing bug. Narration synthesis is documented as additive/best-effort in
	// slidey (it degrades to a silent fallback when edge-tts isn't resolvable),
	// so scoping this test's PATH to exclude edge-tts exercises the exact same
	// deterministic fallback path without touching the sister repo, and keeps
	// this test hermetic (no live network TTS calls, per repo policy of never
	// incurring real external-service cost/nondeterminism in automated tests).
	ctx := ctxWithoutEdgeTTS(t)

	// renderAndEmit runs the real render → emit chain the rendering room runs,
	// returning the content-addressed handle id and the rendered html bytes.
	renderAndEmit := func(label string) (string, []byte) {
		t.Helper()
		rres, rerr := host.SlideyRenderHandler(ctx, map[string]any{
			"spec_path": specPath,
			"format":    "html",
		})
		if rerr != nil {
			t.Fatalf("[%s] render infra: %v", label, rerr)
		}
		if rres.Error != "" {
			t.Fatalf("[%s] render domain: %s", label, rres.Error)
		}
		htmlPath, _ := rres.Data["path"].(string)
		if htmlPath == "" {
			t.Fatalf("[%s] render returned no path", label)
		}

		eres, eerr := host.ArtifactsDirTransportHandler(context.Background(), map[string]any{
			"thread":         "slidey-edit",
			"src_path":       htmlPath,
			"kind":           "slideshow",
			"mime":           "text/html",
			"artifacts_root": artifactsRoot,
		})
		if eerr != nil {
			t.Fatalf("[%s] emit infra: %v", label, eerr)
		}
		if eres.Error != "" {
			t.Fatalf("[%s] emit domain: %s", label, eres.Error)
		}
		handle, _ := eres.Data["handle"].(map[string]any)
		id, _ := handle["id"].(string)
		if id == "" {
			t.Fatalf("[%s] emit returned no handle id", label)
		}
		emittedPath, _ := eres.Data["path"].(string)
		bytes, rerr2 := os.ReadFile(emittedPath)
		if rerr2 != nil {
			t.Fatalf("[%s] read emitted html: %v", label, rerr2)
		}
		return id, bytes
	}

	handleA, htmlA := renderAndEmit("before")

	// Sanity: re-rendering the SAME spec must yield the SAME handle (stable
	// content-addressing — caching still works when nothing changed).
	handleAagain, _ := renderAndEmit("before-again")
	if handleAagain != handleA {
		t.Fatalf("unchanged spec produced a different handle: %s != %s (content-addressing not idempotent)", handleAagain, handleA)
	}

	// Now edit a real scene element on disk — exactly what the reviser agent is
	// supposed to do: change scene 1, card 0's label.
	editCardLabel(t, specPath, "REFINED-COWBOY")

	handleB, htmlB := renderAndEmit("after")

	if handleB == handleA {
		t.Fatalf("handle did NOT change after editing the deck: still %s — the iframe src never changes so the browser shows the stale cached render (the exact 'still not updated' bug)", handleA)
	}
	if string(htmlA) == string(htmlB) {
		t.Fatal("rendered html bytes are identical after the edit — the render did not pick up the spec change")
	}
	if !containsBytes(htmlB, "REFINED-COWBOY") {
		t.Fatal("edited label 'REFINED-COWBOY' is absent from the re-rendered deck — the edit did not reach the rendered output")
	}
}

// editCardLabel rewrites scene 1, card 0's label in the deck spec at path,
// mirroring the reviser's in-place edit of a resolved scene element.
func editCardLabel(t *testing.T, path, newLabel string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var deck map[string]any
	if err := json.Unmarshal(raw, &deck); err != nil {
		t.Fatal(err)
	}
	scenes, _ := deck["scenes"].([]any)
	if len(scenes) < 2 {
		t.Fatalf("deck has %d scenes, need >=2", len(scenes))
	}
	scene1, _ := scenes[1].(map[string]any)
	cards, _ := scene1["cards"].([]any)
	if len(cards) < 1 {
		t.Fatal("scene 1 has no cards")
	}
	card0, _ := cards[0].(map[string]any)
	card0["label"] = newLabel
	out, err := json.MarshalIndent(deck, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ctxWithoutEdgeTTS returns a context whose host.cliExec PATH override omits
// the directory containing the `edge-tts` binary, if any is resolvable. This
// makes slidey's narration-audio synthesis (which shells out to `edge-tts`)
// resolve as unavailable, so slidey falls back to its documented silent/no-
// narration path deterministically instead of making a live, non-reproducible
// TTS network call. When edge-tts isn't installed at all, the original PATH
// is used unchanged (nothing to strip).
func ctxWithoutEdgeTTS(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	ttsPath, err := exec.LookPath("edge-tts")
	if err != nil {
		return ctx
	}
	excludeDir := filepath.Dir(ttsPath)
	origPath := os.Getenv("PATH")
	var kept []string
	for _, dir := range filepath.SplitList(origPath) {
		if dir == excludeDir {
			continue
		}
		kept = append(kept, dir)
	}
	newPath := strings.Join(kept, string(os.PathListSeparator))
	return host.WithCLIExecEnv(ctx, map[string]string{"PATH": newPath})
}

func slideyAvailable() bool {
	if home := os.Getenv("SLIDEY_HOME"); home != "" {
		if _, err := os.Stat(filepath.Join(home, "src", "index.js")); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("slidey")
	return err == nil
}

func containsBytes(haystack []byte, needle string) bool {
	return len(needle) == 0 || indexBytes(haystack, needle) >= 0
}

func indexBytes(haystack []byte, needle string) int {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(haystack); i++ {
		match := true
		for j := range n {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// repoRootFromTest walks up from the cwd until it finds go.mod.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod) above test cwd")
		}
		dir = parent
	}
}
