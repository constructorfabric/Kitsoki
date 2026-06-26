package scripts_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// These tests pin the slidey-edit story's scene-targeting + gate scripts against
// the REAL kitsoki-pitch deck (35 scenes; the "Cat Wrangling" image is scene 9).
// All slidey-specific knowledge lives in the story scripts — kitsoki core stays
// producer-agnostic — so the regression for "the edit landed on the wrong slide"
// is anchored here, where that logic actually lives. No LLM, no render.

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
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: name, Source: src, Inputs: inputs})
	if err != nil {
		t.Fatalf("run %s: %v", name, err)
	}
	return res.Outputs
}

func TestResolveScene_ViewedSceneWins(t *testing.T) {
	out := runScript(t, "resolve_scene.star", map[string]any{
		"spec_path":     "docs/decks/kitsoki-pitch.slidey.json",
		"current_scene": "9",
	})
	if got := out["scene_index"]; got != int64(9) {
		t.Fatalf("scene_index = %v, want 9", got)
	}
	if got := out["scene_count"]; got != int64(35) {
		t.Fatalf("scene_count = %v, want 35 (real kitsoki-pitch)", got)
	}
	scene, _ := out["scene"].(map[string]any)
	if scene == nil {
		t.Fatalf("scene is nil; want the resolved scene 9 object")
	}
	// Scene 9 is the "Cat Wrangling" image slide — the exact slide the user wants
	// to edit. Proves the reviser would receive THIS slide's content.
	if title, _ := scene["title"].(string); title != "Cat Wrangling" {
		t.Fatalf("scene 9 title = %q, want \"Cat Wrangling\"", title)
	}
	sceneJSON, _ := out["scene_json"].(string)
	if sceneJSON == "" || !strings.Contains(sceneJSON, `"Cat Wrangling"`) {
		t.Fatalf("scene_json = %q, want serialized scene content", sceneJSON)
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
