package storydigest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeBindsRuntimeClosureAndIgnoresFixtures(t *testing.T) {
	root := t.TempDir()
	story := filepath.Join(root, ".kitsoki", "stories", "ci")
	write(t, filepath.Join(story, "app.yaml"), `app:
  id: closure
  version: 0.1.0
  title: Closure
  author: Test
  license: CC0
intents:
  run: { description: run, examples: [run], priority: 1 }
root: idle
states:
  idle:
    view: [{ prose: idle }]
    on:
      run: [{ target: done }]
  done:
    terminal: true
    view: [{ prose: done }]
`)
	write(t, filepath.Join(story, "rooms", "included.yaml"), "state: included\n")
	write(t, filepath.Join(story, "prompts", "review.md"), "review v1\n")
	write(t, filepath.Join(story, "views", "status.pongo"), "status v1\n")
	write(t, filepath.Join(story, "scripts", "verdict.star"), "def main(ctx): return {}\n")
	write(t, filepath.Join(story, "scripts", "verdict.star.yaml"), "schema: host-starlark/v1\n")
	write(t, filepath.Join(story, "schemas", "verdict.json"), "{}\n")
	write(t, filepath.Join(story, "flows", "fixture.yaml"), "fixture: v1\n")
	write(t, filepath.Join(root, ".kitsoki", "kits.lock"), "schema: kitsoki-lock/v1\n")

	first, err := Compute(root, filepath.Join(".kitsoki", "stories", "ci", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		".kitsoki/stories/ci/app.yaml",
		".kitsoki/stories/ci/rooms/included.yaml",
		".kitsoki/stories/ci/prompts/review.md",
		".kitsoki/stories/ci/views/status.pongo",
		".kitsoki/stories/ci/scripts/verdict.star",
		".kitsoki/stories/ci/scripts/verdict.star.yaml",
		".kitsoki/stories/ci/schemas/verdict.json",
		".kitsoki/kits.lock",
	} {
		if !contains(first.Files, path) {
			t.Fatalf("closure missing %s: %#v", path, first.Files)
		}
	}
	if contains(first.Files, ".kitsoki/stories/ci/flows/fixture.yaml") {
		t.Fatalf("flow fixture entered runtime closure: %#v", first.Files)
	}

	write(t, filepath.Join(story, "prompts", "review.md"), "review v2\n")
	second, err := Compute(root, filepath.Join(".kitsoki", "stories", "ci", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if second.Digest == first.Digest {
		t.Fatal("prompt mutation did not change story closure digest")
	}

	write(t, filepath.Join(story, "flows", "fixture.yaml"), "fixture: v2\n")
	third, err := Compute(root, filepath.Join(".kitsoki", "stories", "ci", "app.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if third.Digest != second.Digest {
		t.Fatal("fixture-only mutation changed runtime closure digest")
	}
}

func TestComputeRejectsStorySymlink(t *testing.T) {
	root := t.TempDir()
	story := filepath.Join(root, "story")
	write(t, filepath.Join(story, "app.yaml"), `app:
  id: closure
  version: 0.1.0
  title: Closure
  author: Test
  license: CC0
intents: { run: { description: run, examples: [run], priority: 1 } }
root: idle
states:
  idle:
    view: [{ prose: idle }]
    on: { run: [{ target: idle }] }
`)
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(t, outside, "secret\n")
	if err := os.Symlink(outside, filepath.Join(story, "prompt.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Compute(root, filepath.Join("story", "app.yaml")); err == nil {
		t.Fatal("expected symlink dependency rejection")
	}
}

func write(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
