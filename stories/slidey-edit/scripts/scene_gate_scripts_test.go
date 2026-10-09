package scripts_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// These tests pin the slidey-edit story's scene-targeting + gate scripts against
// the REAL kitsoki-pitch deck. Expectations (scene count + the targeted scene's
// content) are DERIVED from the deck here rather than hardcoded, so the test
// tracks the living artifact instead of rotting on every editorial deck change —
// while still cross-checking resolve_scene's output against an independent parse.
// All slidey-specific knowledge lives in the story scripts — kitsoki core stays
// producer-agnostic — so the regression for "the edit landed on the wrong slide"
// is anchored here, where that logic actually lives. No LLM, no render.

const kitsokiPitchDeck = "docs/decks/kitsoki-pitch.slidey.json"

// deckScenes parses the deck's scene array. sceneLabel mirrors resolve_scene's
// label fallback (title → eyebrow → lede), the same precedence the script uses.
func deckScenes(t *testing.T, root string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, kitsokiPitchDeck))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Scenes []map[string]any `json:"scenes"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse %s: %v", kitsokiPitchDeck, err)
	}
	return spec.Scenes
}

func sceneLabel(s map[string]any) string {
	for _, k := range []string{"title", "eyebrow", "lede"} {
		if v, ok := s[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd() // stories/slidey-edit/scripts
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above cwd")
		}
		dir = parent
	}
}

func runScript(t *testing.T, name string, inputs map[string]any) map[string]any {
	t.Helper()
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories", "slidey-edit", "scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewProductionInspector(root))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: name, Source: src, Inputs: inputs, Capabilities: slideyScriptCapabilities()})
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return res.Outputs
}

func slideyScriptCapabilities() starlarkhost.CapabilitySpec {
	return starlarkhost.CapabilitySpec{
		World: true,
		FS: starlarkhost.FSCapability{
			ReadPatterns:  []string{"**"},
			WritePatterns: []string{"**"},
		},
	}
}

func TestResolveScene_ViewedSceneWins(t *testing.T) {
	const idx = 9
	scenes := deckScenes(t, repoRoot(t))
	if idx >= len(scenes) {
		t.Fatalf("deck has %d scenes; targeted index %d is out of range", len(scenes), idx)
	}
	wantCount := int64(len(scenes))
	wantLabel := sceneLabel(scenes[idx]) // title → eyebrow → lede, mirroring resolve_scene

	out := runScript(t, "resolve_scene.star", map[string]any{
		"spec_path":     "docs/decks/kitsoki-pitch.slidey.json",
		"current_scene": "9",
	})
	if got := out["scene_index"]; got != int64(idx) {
		t.Fatalf("scene_index = %v, want %d", got, idx)
	}
	if got := out["scene_count"]; got != wantCount {
		t.Fatalf("scene_count = %v, want %d (real kitsoki-pitch, deck-derived)", got, wantCount)
	}
	scene, _ := out["scene"].(map[string]any)
	if scene == nil {
		t.Fatalf("scene is nil; want the resolved scene %d object", idx)
	}
	// resolve_scene must hand back THIS slide's content — cross-checked against an
	// independent parse of the deck so the test tracks editorial deck changes
	// instead of pinning a specific slide's title. The label falls back through
	// title → eyebrow → lede, which is exactly why resolve_scene does the same.
	label, _ := scene["title"].(string)
	if label == "" {
		label, _ = scene["eyebrow"].(string)
	}
	if label == "" {
		label, _ = scene["lede"].(string)
	}
	if label != wantLabel {
		t.Fatalf("scene %d label = %q, want %q (deck-derived)", idx, label, wantLabel)
	}
	if sl, _ := out["scene_label"].(string); wantLabel != "" && !strings.Contains(sl, wantLabel) {
		t.Fatalf("scene_label = %q, want it to contain %q", sl, wantLabel)
	}
	if sceneJSON, _ := out["scene_json"].(string); sceneJSON == "" {
		t.Fatalf("scene_json is empty; want serialized scene content")
	}
}

func TestResolveScene_FallsBackToAnchorRef(t *testing.T) {
	out := runScript(t, "resolve_scene.star", map[string]any{
		"spec_path":  "docs/decks/kitsoki-pitch.slidey.json",
		"anchor_ref": "9/image",
	})
	if got := out["scene_index"]; got != int64(9) {
		t.Fatalf("anchor-ref fallback scene_index = %v, want 9", got)
	}
}

func TestResolveScene_NoSignalDoesNotGuess(t *testing.T) {
	out := runScript(t, "resolve_scene.star", map[string]any{
		"spec_path": "docs/decks/kitsoki-pitch.slidey.json",
	})
	// The whole bug was silently editing slide 1. With no viewed-scene and no
	// anchor, we must NOT guess — return -1 so the room asks which slide.
	if got := out["scene_index"]; got != int64(-1) {
		t.Fatalf("no-signal scene_index = %v, want -1 (no guessing)", got)
	}
}

func TestGateEditedScene_RejectsStrayEdit(t *testing.T) {
	out := runScript(t, "gate_edited_scene.star", map[string]any{
		"scene_index": int64(9),
		"edited":      []any{"9/image", "3/card_0"}, // 3/* strayed off the viewed slide
	})
	if ok, _ := out["ok"].(bool); ok {
		t.Fatalf("gate ok = true, want false (a stray scene-3 edit must fail)")
	}
	stray, _ := out["stray"].([]any)
	if len(stray) != 1 || stray[0] != "3/card_0" {
		t.Fatalf("stray = %v, want [3/card_0]", stray)
	}
}

func TestGateEditedScene_AcceptsOnSceneEdit(t *testing.T) {
	out := runScript(t, "gate_edited_scene.star", map[string]any{
		"scene_index": int64(9),
		"edited":      []any{"9/image", "9/caption"},
	})
	if ok, _ := out["ok"].(bool); !ok {
		t.Fatalf("gate ok = false, want true (all edits on scene 9)")
	}
}

func TestGateEditedScene_EmptyEditIsNotOK(t *testing.T) {
	out := runScript(t, "gate_edited_scene.star", map[string]any{
		"scene_index": int64(9),
		"edited":      []any{},
	})
	// A no-op refine (reviser changed nothing) is not a wrong-slide violation,
	// but it's not a success either — the room surfaces "nothing changed".
	if ok, _ := out["ok"].(bool); ok {
		t.Fatalf("gate ok = true for empty edit, want false")
	}
}
