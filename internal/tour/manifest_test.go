package tour

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadTourManifest_DriveActions verifies the Go loader unmarshals the SAME
// drive: vocabulary the TS schema validates — the data form of the Playwright
// spec's imperative logic. This is requirement (a): the drive: schema compiling
// and validating on the Go side.
func TestLoadTourManifest_DriveActions(t *testing.T) {
	const y = `
export: DEMO_TOUR_STEPS
steps:
  - id: intro
    route: home
    title: Intro
    body: Welcome.
    placement: center
    kind: explain
    advance: next
    dwellMs: 1000
  - id: drive-it
    route: interactive
    target: chat-transcript
    title: Drive it
    body: Talk it through.
    placement: left
    kind: explain
    advance: next
    drive:
      - type: wait-state
        state: core.main
      - type: type-and-send
        text: prd
      - type: click-intent
        intent: core__prd__start
      - type: click-selector
        selector: '[data-testid="new-session-btn"]'
      - type: reveal-turn
      - type: dwell-ms
        ms: 500
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
	if len(m.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(m.Steps))
	}
	d := m.Steps[1].Drive
	if len(d) != 6 {
		t.Fatalf("want 6 drive actions, got %d", len(d))
	}
	cases := []struct {
		typ, field, want string
		num              int
	}{
		{DriveWaitState, "state", "core.main", 0},
		{DriveTypeAndSend, "text", "prd", 0},
		{DriveClickIntent, "intent", "core__prd__start", 0},
		{DriveClickSelector, "selector", "[data-testid=\"new-session-btn\"]", 0},
		{DriveRevealTurn, "", "", 0},
		{DriveDwellMs, "ms", "", 500},
	}
	for i, c := range cases {
		if d[i].Type != c.typ {
			t.Errorf("drive[%d] type = %q, want %q", i, d[i].Type, c.typ)
		}
		switch c.field {
		case "state":
			if d[i].State != c.want {
				t.Errorf("drive[%d] state = %q, want %q", i, d[i].State, c.want)
			}
		case "text":
			if d[i].Text != c.want {
				t.Errorf("drive[%d] text = %q, want %q", i, d[i].Text, c.want)
			}
		case "intent":
			if d[i].Intent != c.want {
				t.Errorf("drive[%d] intent = %q, want %q", i, d[i].Intent, c.want)
			}
		case "selector":
			if d[i].Selector != c.want {
				t.Errorf("drive[%d] selector = %q, want %q", i, d[i].Selector, c.want)
			}
		case "ms":
			if d[i].Ms != c.num {
				t.Errorf("drive[%d] ms = %d, want %d", i, d[i].Ms, c.num)
			}
		}
	}
}

// TestDriveAction_Validate rejects actions missing their required field — the
// Go mirror of the TS schema's superRefine cross-field check.
func TestDriveAction_Validate(t *testing.T) {
	bad := []DriveAction{
		{Type: DriveTypeAndSend},   // missing text
		{Type: DriveClickIntent},   // missing intent
		{Type: DriveClickSelector}, // missing selector
		{Type: DriveWaitState},     // missing state
		{Type: DriveDwellMs},       // missing ms
		{Type: "nope"},             // unknown
	}
	for _, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("expected %q to be invalid", a.Type)
		}
	}
	good := []DriveAction{
		{Type: DriveTypeAndSend, Text: "x"},
		{Type: DriveClickIntent, Intent: "i"},
		{Type: DriveClickSelector, Selector: "[data-testid=x]"},
		{Type: DriveWaitState, State: "s"},
		{Type: DriveDwellMs, Ms: 1},
		{Type: DriveRevealTurn},
	}
	for _, a := range good {
		if err := a.Validate(); err != nil {
			t.Errorf("expected %q to be valid: %v", a.Type, err)
		}
	}
}

// TestLoadTourManifest_RejectsDuplicateIDs guards the step-id uniqueness the
// chapter sidecar's source_ref keys on.
func TestLoadTourManifest_RejectsDuplicateIDs(t *testing.T) {
	const y = `
steps:
  - {id: a, route: home, title: A, body: b, placement: center, kind: explain, advance: next}
  - {id: a, route: home, title: A2, body: b, placement: center, kind: explain, advance: next}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.yaml")
	if err := os.WriteFile(path, []byte(y), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTourManifest(path); err == nil {
		t.Fatal("expected duplicate-id error")
	}
}

// TestLoadFeatureManifest_DevStoryCatalog loads the real dev-story-prd-design
// feature catalog (a committed source-of-truth manifest) and confirms the loader
// parses its tour + demo binding. This is the catalog-coupling guard: a rename
// of a feature field that breaks the Go loader fails here.
//
// (The example-prd-design catalog this guard originally exercised moved out to the
// the external target repo with slice 3 of the kitsoki-as-dependency epic; the self-targeting
// dev-story catalog is the in-repo equivalent.)
func TestLoadFeatureManifest_DevStoryCatalog(t *testing.T) {
	root := repoRootForTest(t)
	featurePath := filepath.Join(root, "features", "dev-story-prd-design.yaml")
	if _, err := os.Stat(featurePath); err != nil {
		t.Skipf("dev-story feature catalog not present: %v", err)
	}
	m, b, err := LoadFeatureManifest(featurePath, root)
	if err != nil {
		t.Fatalf("LoadFeatureManifest: %v", err)
	}
	if len(m.Steps) == 0 {
		t.Fatal("expected steps")
	}
	if m.SpecPath != filepath.Join("features", "dev-story-prd-design.yaml") {
		t.Errorf("SpecPath = %q, want repo-relative feature path", m.SpecPath)
	}
	if b.VideoBase == "" {
		t.Error("expected a VideoBase from the demo binding")
	}
}

// repoRootForTest walks up from the test's working dir to the repo root (the dir
// holding features/). internal/tour test cwd is the package dir.
func repoRootForTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "features")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repo root (features/) not found from test cwd")
		}
		dir = parent
	}
}
