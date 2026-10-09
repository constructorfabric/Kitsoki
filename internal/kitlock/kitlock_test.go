package kitlock

import (
	"path/filepath"
	"testing"
)

func TestLoadMissingReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	lf, err := Load(Path(dir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if lf.Version != 1 {
		t.Errorf("Version = %d, want 1", lf.Version)
	}
	if len(lf.Kits) != 0 {
		t.Errorf("Kits = %v, want empty", lf.Kits)
	}
	if Exists(Path(dir)) {
		t.Error("Exists should be false before any Save")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)

	lf := New()
	lf.Kits["dev-story"] = &Entry{
		Source:   "@kitsoki/dev-story",
		Version:  "1.0.0",
		TreeHash: "abc123",
	}
	lf.Kits["object-graph"] = &Entry{
		Source:   "git+https://example.com/org/object-graph@v0.3.0",
		Version:  "0.3.0",
		Commit:   "deadbeef",
		TreeHash: "def456",
	}
	if err := Save(path, lf); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !Exists(path) {
		t.Fatal("Exists should be true after Save")
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if len(got.Kits) != 2 {
		t.Fatalf("Kits = %v, want 2 entries", got.Kits)
	}
	if got.Kits["dev-story"].Source != "@kitsoki/dev-story" || got.Kits["dev-story"].Version != "1.0.0" {
		t.Errorf("dev-story entry = %+v", got.Kits["dev-story"])
	}
	if got.Kits["object-graph"].Commit != "deadbeef" {
		t.Errorf("object-graph entry = %+v", got.Kits["object-graph"])
	}
	if want := []string{"dev-story", "object-graph"}; !equalSlices(got.SortedNames(), want) {
		t.Errorf("SortedNames = %v, want %v", got.SortedNames(), want)
	}
}

func TestPath(t *testing.T) {
	got := Path("/foo/bar")
	want := filepath.Join("/foo/bar", ".kitsoki", "kits.lock")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
