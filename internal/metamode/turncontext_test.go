package metamode

import (
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

// stubContextSource is a ContextSource that returns canned world / view /
// imported-manifest data so BuildTurnContext can be exercised without a live
// orchestrator.
type stubContextSource struct {
	world    world.World
	machine  machine.Machine
	manifest *app.AppDef
}

func (s stubContextSource) CurrentWorld(app.SessionID) world.World { return s.world }
func (s stubContextSource) Machine() machine.Machine               { return s.machine }
func (s stubContextSource) AppDef() *app.AppDef                    { return s.manifest }

// stubMachine renders a fixed view regardless of state/world.
type stubMachine struct {
	machine.Machine
	view string
}

func (m stubMachine) RenderState(app.StatePath, world.World) (string, error) {
	return m.view, nil
}

func TestBuildTurnContext_PopulatesAllFields(t *testing.T) {
	src := stubContextSource{
		world:    world.World{Vars: map[string]any{"wearing_cloak": true}},
		machine:  stubMachine{view: "> You are in the foyer."},
		manifest: &app.AppDef{LoadedManifests: []string{"/abs/robbery/app.yaml"}},
	}

	got := BuildTurnContext(src, "sid-1", "main.foyer", "/abs/oregon/app.yaml", "/tmp/trace.jsonl")

	if got.StatePath != "main.foyer" {
		t.Errorf("StatePath = %q, want main.foyer", got.StatePath)
	}
	if got.AppFile != "/abs/oregon/app.yaml" {
		t.Errorf("AppFile = %q", got.AppFile)
	}
	if got.TracePath != "/tmp/trace.jsonl" {
		t.Errorf("TracePath = %q", got.TracePath)
	}
	if got.RenderedView != "> You are in the foyer." {
		t.Errorf("RenderedView = %q", got.RenderedView)
	}
	if v, ok := got.World["wearing_cloak"].(bool); !ok || !v {
		t.Errorf("World[wearing_cloak] = %v, want true", got.World["wearing_cloak"])
	}
	if len(got.ImportedManifestPaths) != 1 || got.ImportedManifestPaths[0] != "/abs/robbery/app.yaml" {
		t.Errorf("ImportedManifestPaths = %v", got.ImportedManifestPaths)
	}
}

func TestBuildTurnContext_NilAppDef(t *testing.T) {
	src := stubContextSource{
		world:   world.World{},
		machine: stubMachine{view: ""},
	}
	got := BuildTurnContext(src, "sid", "idle", "", "")
	if len(got.ImportedManifestPaths) != 0 {
		t.Errorf("ImportedManifestPaths = %v, want empty", got.ImportedManifestPaths)
	}
}
