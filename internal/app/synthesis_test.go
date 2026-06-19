package app

import (
	"testing"
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
	// app.id is the repo basename (provenance).
	if def.App.ID == "" {
		t.Fatal("synthesized app.id is empty")
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

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
