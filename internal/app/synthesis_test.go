package app

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"kitsoki/internal/kit"
)

// repoRootForTest resolves the kitsoki repo root from the test's package dir so
// @kitsoki/dev-story resolves. The app package lives at <repo>/internal/app, so
// findRepoRoot walks up to the go.mod with module kitsoki.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := findRepoRoot(".")
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	return root
}

func TestSynthesizeRoot_Rung0(t *testing.T) {
	root := repoRootForTest(t)
	def, err := SynthesizeRoot(nil, root)
	if err != nil {
		t.Fatalf("rung-0 synthesize: %v", err)
	}
	// The root resolves to the synthesized alias, folded as a compound wrapper.
	if got, _ := def.Root.(string); got != RootAlias {
		t.Fatalf("root = %v, want %q", def.Root, RootAlias)
	}
	if _, ok := def.States[RootAlias]; !ok {
		t.Fatalf("expected folded wrapper state %q in states (got %v keys)", RootAlias, len(def.States))
	}
	// dev-story's default bindings carry through: the iface defaults appear in
	// the unioned host allow-list.
	if !containsStr(def.Hosts, "host.local_files.ticket") {
		t.Fatalf("expected dev-story default ticket handler in hosts, got %v", def.Hosts)
	}
	if !containsStr(def.Hosts, "host.slidey.render") {
		t.Fatalf("expected dev-story visual producer host in synthesized root hosts, got %v", def.Hosts)
	}
	// app.id is the repo basename (provenance).
	if def.App.ID == "" {
		t.Fatal("synthesized app.id is empty")
	}
}

// TestDevStoryKitRootHostsCoverExpandedStory keeps the manifest-driven
// synthesized-root allow-list in lockstep with the fully expanded dev-story
// subtree. SynthesizeRoot imports that subtree with hosts: declared, so a
// missing entry would otherwise make every implicit root fail to load.
func TestDevStoryKitRootHostsCoverExpandedStory(t *testing.T) {
	root := repoRootForTest(t)
	manifest, err := kit.Load(filepath.Join(root, "stories", "dev-story", kit.ManifestFileName))
	if err != nil {
		t.Fatalf("load dev-story kit manifest: %v", err)
	}
	storyPath := filepath.Join(root, "stories", "dev-story", "app.yaml")
	bytes, err := os.ReadFile(storyPath)
	if err != nil {
		t.Fatalf("read dev-story: %v", err)
	}
	story, parseErrs := parseAndMerge(bytes, storyPath, filepath.Dir(storyPath))
	if len(parseErrs) != 0 {
		t.Fatalf("parse dev-story: %v", parseErrs)
	}
	if importErrs := resolveImports(story, storyPath, filepath.Dir(storyPath), []string{canonicalPath(storyPath)}, nil); len(importErrs) != 0 {
		t.Fatalf("expand dev-story imports: %v", importErrs)
	}

	declared := make(map[string]struct{}, len(manifest.Root.Hosts))
	for _, host := range manifest.Root.Hosts {
		declared[host] = struct{}{}
	}
	var missing []string
	for _, host := range story.Hosts {
		if _, ok := declared[host]; !ok {
			missing = append(missing, host)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("dev-story kit root.hosts must cover the pre-interface expanded subtree under hosts: declared; missing %v", missing)
	}
}

func TestSynthesizeRootWithResolver_Rung0ForeignRepo(t *testing.T) {
	repoRoot := repoRootForTest(t)
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "go.mod"), []byte("module example.com/plain\n"), 0o644); err != nil {
		t.Fatalf("write foreign go.mod: %v", err)
	}
	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", nil
		}
		return filepath.Join(repoRoot, "stories", name, "app.yaml"), nil
	}

	def, err := SynthesizeRootWithResolver(nil, foreign, resolver)
	if err != nil {
		t.Fatalf("rung-0 synthesize in foreign repo: %v", err)
	}
	if def.App.ID != filepath.Base(foreign) {
		t.Fatalf("app.id = %q, want repo basename %q", def.App.ID, filepath.Base(foreign))
	}
	if _, ok := def.States[RootAlias]; !ok {
		t.Fatalf("expected folded wrapper state %q in states", RootAlias)
	}
}

func TestSynthesizeRoot_Rung1_OverridesFold(t *testing.T) {
	root := repoRootForTest(t)
	spec := &RootSpec{
		Bindings: map[string]string{"transport": "host.append_to_file"},
		World:    map[string]any{"judge_mode": "llm_then_human"},
	}
	def, err := SynthesizeRoot(spec, root)
	if err != nil {
		t.Fatalf("rung-1 synthesize: %v", err)
	}
	// The world override reaches the instance-level world: default.
	v, ok := def.World["judge_mode"]
	if !ok {
		t.Fatalf("expected world key judge_mode after override fold; world keys: %d", len(def.World))
	}
	if v.Default != "llm_then_human" {
		t.Fatalf("judge_mode default = %v, want llm_then_human", v.Default)
	}
	// The binding override rebinds transport — host.append_to_file remains in
	// the allow-list (it is the default too, so presence is the weak check; the
	// stronger guarantee is that the bad-binding path below fails fast).
	if !containsStr(def.Hosts, "host.append_to_file") {
		t.Fatalf("expected transport handler in hosts, got %v", def.Hosts)
	}
}

func TestSynthesizeRoot_Rung1_ScriptBinding(t *testing.T) {
	root := writableSynthesisRepoRoot(t)
	if err := os.MkdirAll(filepath.Join(root, ".kitsoki", "providers"), 0o755); err != nil {
		t.Fatalf("mkdir providers: %v", err)
	}
	script := filepath.Join(root, ".kitsoki", "providers", "ticket.star")
	if err := os.WriteFile(script, []byte(`def main(ctx):
    return {"tickets": []}
`), 0o644); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.WriteFile(script+".yaml", []byte(`inputs:
  op: { type: string, required: false }
outputs:
  tickets:
    type: list
`), 0o644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	spec := &RootSpec{Bindings: map[string]string{"ticket": ".kitsoki/providers/ticket.star"}}
	def, err := SynthesizeRoot(spec, root)
	if err != nil {
		t.Fatalf("synthesize with script binding: %v", err)
	}
	if len(def.StarlarkHostBindings) != 1 {
		t.Fatalf("expected one starlark binding, got %+v", def.StarlarkHostBindings)
	}
	var handler, gotScript string
	for h, p := range def.StarlarkHostBindings {
		handler, gotScript = h, p
	}
	if gotScript != script {
		t.Fatalf("script binding resolved to %q, want %q", gotScript, script)
	}
	if !containsStr(def.Hosts, handler+".search") {
		t.Fatalf("expected synthesized ticket search host in allow-list, got %v", def.Hosts)
	}
}

func TestSynthesizeRoot_FailFast(t *testing.T) {
	root := repoRootForTest(t)

	if _, err := SynthesizeRoot(&RootSpec{Import: "not-dev-story"}, root); err == nil {
		t.Fatal("expected error for unknown root.import")
	}
	if _, err := SynthesizeRoot(&RootSpec{Bindings: map[string]string{"frobnicate": "host.x"}}, root); err == nil {
		t.Fatal("expected error for unknown binding iface")
	}
}

func TestDevStoryWorldKeys(t *testing.T) {
	root := repoRootForTest(t)
	keys, err := DevStoryWorldKeys(root)
	if err != nil {
		t.Fatalf("DevStoryWorldKeys: %v", err)
	}
	if _, ok := keys["judge_mode"]; !ok {
		t.Fatalf("expected judge_mode among dev-story world keys (got %d keys)", len(keys))
	}
}

func writableSynthesisRepoRoot(t *testing.T) string {
	t.Helper()
	realRoot := repoRootForTest(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module kitsoki\n"), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}
	if err := os.Symlink(filepath.Join(realRoot, "stories"), filepath.Join(root, "stories")); err != nil {
		t.Fatalf("symlink stories into temp repo: %v", err)
	}
	return root
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
