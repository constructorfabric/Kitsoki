package storyboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/tour"
	"kitsoki/internal/video"
)

// validBoard returns a storyboard that passes Validate with zero issues when
// rooted at a dir containing the binding files (see writeBindingFiles).
func validBoard() *Storyboard {
	return &Storyboard{
		Version: 1,
		ID:      "agent-actions",
		Title:   "Agent Actions drawer",
		Goal:    "Show that every agent tool call in a session is inspectable from the observer.",
		Surface: "web",
		Format:  "rrweb",
		Binding: &Binding{
			Story: "stories/demo/app.yaml",
			Flow:  "stories/demo/flows/demo.yaml",
		},
		Scenes: []Scene{
			{
				ID:        "welcome",
				Title:     "Welcome",
				Purpose:   "Orient the viewer before anything moves.",
				Narration: "Every kitsoki session records each agent action. This tour opens a session and inspects one.",
				Screen:    "Home screen with the story catalogue",
				Route:     "home",
				DwellMs:   6000,
				Expect:    []string{"A centered popover introduces the demo"},
			},
			{
				ID:        "open-session",
				Title:     "Open a session",
				Purpose:   "Get onto the surface the feature lives on.",
				Narration: "We start a new session from the story card and land in the interactive view.",
				Route:     "home",
				Anchor:    "new-session-btn",
				Kind:      "action",
				Advance:   "route-match",
				DwellMs:   6000,
				Expect:    []string{"The New Session button is spotlighted", "The view changes to the interactive route"},
			},
			{
				ID:        "drive-turn",
				Title:     "Drive one turn",
				Purpose:   "Populate the trace so the drawer has something to show.",
				Narration: "One intent is driven so the agent acts and the timeline lights up with its tool calls.",
				Route:     "interactive",
				Anchor:    "chat-transcript",
				Drive: []tour.DriveAction{
					{Type: tour.DriveClickIntent, Intent: "start"},
					{Type: tour.DriveWaitState, State: "working"},
					{Type: tour.DriveDwellMs, Ms: 2000},
				},
				DwellMs: 7000,
				Expect:  []string{"The transcript shows an agent reply", "The state badge shows a non-initial state"},
			},
			{
				ID:        "inspect-call",
				Title:     "Inspect a tool call",
				Purpose:   "Deliver the promise: the call detail is readable.",
				Narration: "Expanding a row reveals the full tool call with its arguments and result, exactly as the agent saw it.",
				Route:     "any",
				Anchor:    "trace-event-row",
				DwellMs:   8000,
				Expect:    []string{"An expanded row shows tool-call arguments and result"},
			},
		},
	}
}

func writeBindingFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{"stories/demo/app.yaml", "stories/demo/flows/demo.yaml"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestValidBoardHasNoIssues(t *testing.T) {
	root := writeBindingFiles(t)
	issues := validBoard().Validate(ValidateOptions{Root: root})
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got: %v", issues)
	}
}

func TestValidateLints(t *testing.T) {
	root := writeBindingFiles(t)
	cases := []struct {
		name     string
		mutate   func(*Storyboard)
		want     string // substring of an expected issue
		severity Severity
	}{
		{"bad version", func(s *Storyboard) { s.Version = 2 }, "version must be 1", SeverityError},
		{"bad id", func(s *Storyboard) { s.ID = "Agent Actions" }, "kebab-case", SeverityError},
		{"missing goal", func(s *Storyboard) { s.Goal = "" }, "goal is required", SeverityError},
		{"bad surface", func(s *Storyboard) { s.Surface = "desktop" }, "surface", SeverityError},
		{"bad format", func(s *Storyboard) { s.Format = "gif" }, "format", SeverityError},
		{"no scenes", func(s *Storyboard) { s.Scenes = nil }, "at least one scene", SeverityError},
		{"dup scene id", func(s *Storyboard) { s.Scenes[1].ID = "welcome" }, "duplicate scene id", SeverityError},
		{"missing purpose", func(s *Storyboard) { s.Scenes[0].Purpose = "" }, "purpose is required", SeverityError},
		{"missing narration", func(s *Storyboard) { s.Scenes[0].Narration = "" }, "narration is required", SeverityError},
		{"missing expect", func(s *Storyboard) { s.Scenes[0].Expect = nil }, "expect is required", SeverityError},
		{"missing dwell", func(s *Storyboard) { s.Scenes[0].DwellMs = 0 }, "dwellMs is required", SeverityError},
		{"bad route", func(s *Storyboard) { s.Scenes[0].Route = "observer" }, "route", SeverityError},
		{"bad placement", func(s *Storyboard) { s.Scenes[0].Placement = "middle" }, "placement", SeverityError},
		{"bad drive", func(s *Storyboard) {
			s.Scenes[2].Drive = append(s.Scenes[2].Drive, tour.DriveAction{Type: "type-and-send"})
		}, "requires text", SeverityError},
		{"missing binding path", func(s *Storyboard) { s.Binding.Flow = "stories/demo/flows/missing.yaml" }, "not found", SeverityError},
		{"unknown scenario", func(s *Storyboard) { s.Scenario = &ScenarioRef{ID: "nope"} }, "cannot be verified", SeverityWarn},
		{"rushed dwell", func(s *Storyboard) { s.Scenes[3].DwellMs = 300 }, "reading budget", SeverityWarn},
		{"raw intent name", func(s *Storyboard) { s.Scenes[0].Narration = "Click core__prd__start to begin the run" }, "raw internal intent name", SeverityWarn},
		{"no binding", func(s *Storyboard) { s.Binding = nil }, "no binding declared", SeverityWarn},
		{"empty scene", func(s *Storyboard) {
			s.Scenes[0].Anchor, s.Scenes[0].Screen, s.Scenes[0].Drive = "", "", nil
		}, "shows nothing specific", SeverityWarn},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := validBoard()
			tc.mutate(sb)
			issues := sb.Validate(ValidateOptions{Root: root})
			for _, issue := range issues {
				if issue.Severity == tc.severity && strings.Contains(issue.Msg, tc.want) {
					return
				}
			}
			t.Fatalf("wanted a %s issue containing %q, got: %v", tc.severity, tc.want, issues)
		})
	}
}

func TestTotalScreenTimeFloor(t *testing.T) {
	root := writeBindingFiles(t)
	sb := validBoard()
	for i := range sb.Scenes {
		sb.Scenes[i].DwellMs = 1500
	}
	sb.Scenes[2].Drive = nil
	hasFloorWarning := func(issues []Issue) bool {
		for _, issue := range issues {
			if strings.Contains(issue.Msg, "estimated screen time") {
				return true
			}
		}
		return false
	}
	if !hasFloorWarning(sb.Validate(ValidateOptions{Root: root})) {
		t.Fatal("expected a total screen time warning")
	}
	// A deliberate clip opts out via minTotalMs.
	sb.MinTotalMs = 5000
	if hasFloorWarning(sb.Validate(ValidateOptions{Root: root})) {
		t.Fatal("minTotalMs override should silence the floor warning")
	}
}

func TestScenarioRefAgainstCatalog(t *testing.T) {
	root := writeBindingFiles(t)
	catalog := `{"scenarios":[{"id":"bugfix","transports":{"allowed":["tui","web"]}}]}`
	p := filepath.Join(root, "tools", "product-journey", "scenarios.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(catalog), 0o644); err != nil {
		t.Fatal(err)
	}

	sb := validBoard()
	sb.Scenario = &ScenarioRef{ID: "bugfix", Transport: "web"}
	if issues := sb.Validate(ValidateOptions{Root: root}); len(issues) != 0 {
		t.Fatalf("allowed transport should validate clean, got: %v", issues)
	}
	sb.Scenario.Transport = "vscode"
	if issues := sb.Validate(ValidateOptions{Root: root}); !HasErrors(issues) {
		t.Fatalf("disallowed transport should error, got: %v", issues)
	}
	sb.Scenario = &ScenarioRef{ID: "missing-scenario"}
	if issues := sb.Validate(ValidateOptions{Root: root}); !HasErrors(issues) {
		t.Fatalf("unknown scenario should error, got: %v", issues)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.storyboard.yaml")
	body := "version: 1\nid: x\ntitle: X\ngoal: g\nsurface: web\nformat: rrweb\nscens: []\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Fatal("expected strict parse to reject the misspelled key 'scens'")
	}
}

// TestEmitTourRoundTrip is the capture-readiness guarantee: the emitted
// manifest must load through the same tour.LoadTourManifest the renderer uses.
func TestEmitTourRoundTrip(t *testing.T) {
	sb := validBoard()
	data, err := sb.EmitTourYAML()
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "tour.yaml")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := tour.LoadTourManifest(p)
	if err != nil {
		t.Fatalf("emitted manifest does not load: %v\n%s", err, data)
	}
	if len(m.Steps) != len(sb.Scenes) {
		t.Fatalf("got %d steps for %d scenes", len(m.Steps), len(sb.Scenes))
	}
	for i, step := range m.Steps {
		sc := sb.Scenes[i]
		if step.ID != sc.ID || step.Body != sc.Narration || step.DwellMs != sc.DwellMs {
			t.Fatalf("step %d does not mirror scene %q: %+v", i, sc.ID, step)
		}
	}
	// Defaults are filled the way the renderer expects.
	if m.Steps[0].Kind != "explain" || m.Steps[0].Advance != "next" {
		t.Fatalf("explain/next defaults not applied: %+v", m.Steps[0])
	}
	if m.Steps[1].Kind != "action" || m.Steps[1].Advance != "route-match" || m.Steps[1].WaitForTarget != "new-session-btn" {
		t.Fatalf("scene overrides not carried: %+v", m.Steps[1])
	}
}

func TestEmitQAScenarios(t *testing.T) {
	sb := validBoard()
	off := false
	sb.Scenes[2].Required = &off
	data, err := sb.EmitQAScenariosYAML()
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{
		"feature: Agent Actions drawer",
		"id: drive-turn",
		"required: false",
		"- The transcript shows an agent reply",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("qa scenarios missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "required: true") != 3 {
		t.Fatalf("expected 3 required scenarios:\n%s", out)
	}
}

func TestRenderMarkdown(t *testing.T) {
	out := string(validBoard().RenderMarkdown())
	for _, want := range []string{
		"# Storyboard: Agent Actions drawer (`agent-actions`)",
		"> Show that every agent tool call",
		"| # | Scene | Dwell | Anchor | Drive |",
		"## 3. Drive one turn (`drive-turn`)",
		"**Why:** Populate the trace",
		"- [ ] An expanded row shows tool-call arguments and result",
		"click intent `start`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestCheckChapters(t *testing.T) {
	sb := validBoard()
	chapters := func(t *testing.T, chs []video.Chapter) string {
		t.Helper()
		base := filepath.Join(t.TempDir(), "demo.mp4")
		if _, err := video.WriteChapters(base, chs); err != nil {
			t.Fatal(err)
		}
		return base
	}
	full := []video.Chapter{
		{Index: 0, ID: "welcome", StartMs: 0, EndMs: 6100},
		{Index: 1, ID: "open-session", StartMs: 6100, EndMs: 11500},
		{Index: 2, ID: "drive-turn", StartMs: 11500, EndMs: 21000},
		{Index: 3, ID: "inspect-call", StartMs: 21000, EndMs: 29500},
	}

	t.Run("clean", func(t *testing.T) {
		issues, err := CheckChapters(sb, chapters(t, full))
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 {
			t.Fatalf("expected clean check, got: %v", issues)
		}
	})
	t.Run("missing scene", func(t *testing.T) {
		issues, err := CheckChapters(sb, chapters(t, full[:3]))
		if err != nil {
			t.Fatal(err)
		}
		if !HasErrors(issues) {
			t.Fatalf("dropped scene should error, got: %v", issues)
		}
	})
	t.Run("under-dwelled window", func(t *testing.T) {
		short := append([]video.Chapter{}, full...)
		short[3] = video.Chapter{Index: 3, ID: "inspect-call", StartMs: 21000, EndMs: 23000}
		issues, err := CheckChapters(sb, chapters(t, short))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, issue := range issues {
			if issue.Severity == SeverityWarn && strings.Contains(issue.Msg, "under the planned dwell") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected an under-dwell warning, got: %v", issues)
		}
	})
	t.Run("unplanned chapter", func(t *testing.T) {
		extra := append(append([]video.Chapter{}, full...),
			video.Chapter{Index: 4, ID: "outro", StartMs: 29500, EndMs: 33000})
		issues, err := CheckChapters(sb, chapters(t, extra))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, issue := range issues {
			if strings.Contains(issue.Msg, "unplanned scene") {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected an unplanned-scene warning, got: %v", issues)
		}
	})
	t.Run("sidecar path accepted directly", func(t *testing.T) {
		base := chapters(t, full)
		issues, err := CheckChapters(sb, base+".chapters.json")
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 0 {
			t.Fatalf("expected clean check via sidecar path, got: %v", issues)
		}
	})
}
