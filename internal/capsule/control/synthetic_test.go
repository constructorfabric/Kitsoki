package control

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSyntheticProviderMaterializesLegacyDefinition(t *testing.T) {
	project := t.TempDir()
	spec := filepath.Join(project, "capsules", "clean", "capsule.yaml")
	if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, []byte("name: clean\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: hello.txt\n      content: hi\n    - action: commit\n      message: init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defs := FileDefinitionStore{ProjectRoot: project}
	def, err := defs.Get(context.Background(), "clean")
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(project, ".capsules", "clean")
	p := SyntheticProvider{ProjectRoot: project}
	got, err := p.Create(context.Background(), def, Instance{Path: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if got.Head == "" {
		t.Fatal("missing synthetic git head")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".kitsoki-capsule")); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(context.Background(), Instance{Path: workspace}); err != nil {
		t.Fatal(err)
	}
}
