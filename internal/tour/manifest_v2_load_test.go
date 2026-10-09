package tour

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadTourManifest_DetectsV2 proves LoadTourManifest recognizes a
// "version: 2" manifest file, downconverts it via ConvertV2ToV1, and hands
// back the same TourManifest shape the chromedp renderer already knows how
// to drive — the mechanism behind "internal/tour renders v2".
func TestLoadTourManifest_DetectsV2(t *testing.T) {
	const y = `
version: 2
id: how-to-save
origin: https://app.example.com
steps:
  - id: step-save
    route: home
    target:
      testid: save-btn
      text: Save
    popover:
      title: Save your work
      body: Click here
      side: bottom
    kind: gate
    advanceOn:
      event: click
`
	dir := t.TempDir()
	path := filepath.Join(dir, "tour.yaml")
	if err := os.WriteFile(path, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := LoadTourManifest(path)
	if err != nil {
		t.Fatalf("LoadTourManifest: %v", err)
	}
	if m.Export != "how-to-save" {
		t.Errorf("Export = %q, want how-to-save", m.Export)
	}
	if len(m.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(m.Steps))
	}
	s := m.Steps[0]
	if s.Target != "save-btn" {
		t.Errorf("Target = %q, want save-btn", s.Target)
	}
	if s.Title != "Save your work" || s.Placement != "bottom" {
		t.Errorf("popover mapping: %+v", s)
	}
	if s.Kind != "action" || s.Advance != "click-target" {
		t.Errorf("kind/advance = %q/%q", s.Kind, s.Advance)
	}
}

// TestLoadTourManifest_V1StillLoadsUnchanged proves adding v2 detection did
// not disturb the legacy no-version-field path.
func TestLoadTourManifest_V1StillLoadsUnchanged(t *testing.T) {
	const y = `
export: LEGACY
steps:
  - id: s1
    route: home
    title: Intro
    body: Welcome.
    placement: center
    kind: explain
    advance: next
`
	dir := t.TempDir()
	path := filepath.Join(dir, "tour.yaml")
	if err := os.WriteFile(path, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadTourManifest(path)
	if err != nil {
		t.Fatalf("LoadTourManifest: %v", err)
	}
	if m.Export != "LEGACY" || len(m.Steps) != 1 {
		t.Errorf("m = %+v", m)
	}
}
