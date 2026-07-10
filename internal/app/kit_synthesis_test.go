package app

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	"kitsoki/internal/kit"
	"kitsoki/internal/storyauthoring"
)

// syntheticKitDir resolves the in-tree synthetic-kit fixture used to exercise
// the kit/v1 loader + BuildKitImporter/SynthesizeKit end to end (S1). It is
// fixture-only testdata — kitsoki itself carries no domain packs.
func syntheticKitDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("testdata/kits/synthetic-kit")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, kit.ManifestFileName)); statErr != nil {
		t.Fatalf("synthetic-kit fixture missing at %s: %v", dir, statErr)
	}
	return dir
}

func TestKitManifest_LoadsAndValidatesAgainstKitV1Schema(t *testing.T) {
	dir := syntheticKitDir(t)

	res, err := kit.ValidateFile(filepath.Join(dir, kit.ManifestFileName))
	if err != nil {
		t.Fatalf("ValidateFile: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected synthetic-kit/kit.yaml to validate against kit/v1, got errors: %v", res.Schema)
	}

	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if manifest.Identity() != "@kitsoki-test/synthetic" {
		t.Fatalf("Identity() = %q, want @kitsoki-test/synthetic", manifest.Identity())
	}
	if !manifest.HasStory("greeter") {
		t.Fatalf("expected synthetic-kit to provide story greeter, got %v", manifest.Provides.Stories)
	}
	if _, ok := manifest.Parameters["greeting"]; !ok {
		t.Fatalf("expected synthetic-kit to declare parameter greeting, got %v", manifest.Parameters)
	}
}

func TestBuildKitImporter_UnknownStoryRejected(t *testing.T) {
	dir := syntheticKitDir(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if _, _, err := BuildKitImporter(manifest, "not-a-story", "", nil); err == nil {
		t.Fatal("expected error for a story not in provides.stories")
	}
}

func TestBuildKitImporter_UnknownParameterRejected(t *testing.T) {
	dir := syntheticKitDir(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	spec := &KitImportSpec{Parameters: map[string]any{"frobnicate": "x"}}
	if _, _, err := BuildKitImporter(manifest, "greeter", "", spec); err == nil {
		t.Fatal("expected error for a parameter not declared in the kit manifest")
	}
}

func TestSynthesizeKit_Rung0(t *testing.T) {
	dir := syntheticKitDir(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	def, err := SynthesizeKit(manifest, "greeter", "", &KitImportSpec{Entry: "idle"})
	if err != nil {
		t.Fatalf("SynthesizeKit: %v", err)
	}
	if got, _ := def.Root.(string); got != "greeter" {
		t.Fatalf("root = %v, want %q", def.Root, "greeter")
	}
	if _, ok := def.States["greeter"]; !ok {
		t.Fatalf("expected folded wrapper state %q in states (got %v keys)", "greeter", len(def.States))
	}
	if !containsStr(def.Hosts, "host.run") {
		t.Fatalf("expected greeter's host.run to be unioned into hosts (inherit mode), got %v", def.Hosts)
	}
	// Default reporter binding folds through untouched (no override supplied).
	if _, ok := def.States["greeter"].States["idle"]; !ok {
		t.Fatalf("expected greeter's idle state nested under the alias wrapper")
	}
}

func TestSynthesizeKit_ParametersFoldIntoWorld(t *testing.T) {
	dir := syntheticKitDir(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}

	def, err := SynthesizeKit(manifest, "greeter", "greet", &KitImportSpec{
		Entry:      "idle",
		Parameters: map[string]any{"greeting": "howdy"},
	})
	if err != nil {
		t.Fatalf("SynthesizeKit: %v", err)
	}
	v, ok := def.World["greeting"]
	if !ok {
		t.Fatalf("expected world key greeting after parameter fold; world keys: %v", def.World)
	}
	if v.Default != "howdy" {
		t.Fatalf("world.greeting default = %v, want howdy", v.Default)
	}
	if got, _ := def.Root.(string); got != "greet" {
		t.Fatalf("root = %v, want alias %q", def.Root, "greet")
	}
}

// TestKitImporterRoundTrip is the "Synthesize ≡ Load(emit)" property BuildRootImporter
// carries (cmd/kitsoki's TestMaterializeRoundTrip): emitting the UN-folded
// BuildKitImporter def to YAML and reloading it via app.Load must produce a
// structurally identical AppDef to the one SynthesizeKit folds directly.
func TestKitImporterRoundTrip(t *testing.T) {
	dir := syntheticKitDir(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	spec := &KitImportSpec{
		Entry:      "idle",
		Parameters: map[string]any{"greeting": "howdy"},
	}

	synth, err := SynthesizeKit(manifest, "greeter", "greet", spec)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	unfolded, _, err := BuildKitImporter(manifest, "greeter", "greet", spec)
	if err != nil {
		t.Fatalf("BuildKitImporter: %v", err)
	}
	yamlBytes, err := goyaml.Marshal(unfolded)
	if err != nil {
		t.Fatalf("marshal unfolded importer: %v", err)
	}
	outPath := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(outPath, yamlBytes, 0o644); err != nil {
		t.Fatalf("write emitted: %v", err)
	}

	loaded, err := Load(outPath)
	if err != nil {
		t.Fatalf("reload emitted app.yaml: %v\n---\n%s", err, string(yamlBytes))
	}

	normalizeKitDef(synth)
	normalizeKitDef(loaded)
	if !reflect.DeepEqual(synth, loaded) {
		t.Fatalf("emitted app.yaml does not round-trip to the synthesized def\nsynth root=%v states=%d world=%d hosts=%d\nloaded root=%v states=%d world=%d hosts=%d",
			synth.Root, len(synth.States), len(synth.World), len(synth.Hosts),
			loaded.Root, len(loaded.States), len(loaded.World), len(loaded.Hosts))
	}
}

// normalizeKitDef strips load-provenance fields that legitimately differ
// between a synthesized def and a file-loaded one, mirroring
// cmd/kitsoki/materialize_test.go's normalize helper.
func normalizeKitDef(def *AppDef) {
	def.BaseDir = ""
	def.LoadedManifests = nil
	def.ImportWrappers = nil
	def.App = AppMeta{}
	normalizeStoryAuthoringPaths(def)
	sort.Strings(def.Hosts)
}

func normalizeStoryAuthoringPaths(def *AppDef) {
	if def == nil || def.States == nil {
		return
	}
	s := def.States[storyauthoring.RoomState]
	if s == nil {
		return
	}
	for i := range s.View.Elements {
		el := &s.View.Elements[i]
		if el.Kind != "kv" {
			continue
		}
		for j := range el.Pairs {
			key, _ := el.Pairs[j].Key.(string)
			if key == "Story root" {
				el.Pairs[j].Value = "<story-dir>"
			}
		}
	}
	if len(s.OnEnter) == 0 || s.OnEnter[0].With == nil {
		return
	}
	s.OnEnter[0].With["working_dir"] = "<story-dir>"
	ctx, _ := s.OnEnter[0].With["context"].(map[string]any)
	args, _ := ctx["args"].(map[string]any)
	if args == nil {
		return
	}
	args["story_dir"] = "<story-dir>"
	args["app_file"] = "<app-file>"
}
