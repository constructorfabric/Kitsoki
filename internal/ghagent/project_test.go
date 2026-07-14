package ghagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func TestProjectRouteResolverUsesOnboardedProjectStory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kitsoki", "stories", "sample-dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kitsoki.yaml"), []byte("story_dirs:\n  - .kitsoki/stories\nproject_profile: .kitsoki/project-profile.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(root, ".kitsoki", "stories", "sample-dev", "app.yaml")
	if err := os.WriteFile(appPath, []byte(`app:
  id: sample-dev
  version: 0.1.0
world:
  repo_root: { type: string, default: "." }
  workdir: { type: string, default: "." }
  ticket_repo: { type: string, default: "o/r" }
root: idle
states:
  idle:
    description: "Idle."
    view: "Idle."
`), 0o644); err != nil {
		t.Fatal(err)
	}

	mention := Mention{
		Item:      host.GitHubInboxItem{Kind: "issue", Number: "42", Title: "@kitsoki bug"},
		Repo:      "o/r",
		OriginRef: "github:o/r/issue/42",
		Trigger:   DefaultMentionTrigger,
	}
	route, applied, err := (ProjectRouteResolver{Root: root}).Apply(DefaultLabelStoryMap()["bug"], mention)
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("project route was not applied")
	}
	if route.AppPath != appPath {
		t.Fatalf("AppPath=%q, want %q", route.AppPath, appPath)
	}
	if !strings.HasPrefix(route.Story, "project:") {
		t.Fatalf("Story=%q, want project: prefix", route.Story)
	}
	if filepath.Base(route.BeatFixture) != projectBugBeat {
		t.Fatalf("BeatFixture=%q, want %s", route.BeatFixture, projectBugBeat)
	}
	if got := route.World["ticket_type"]; got != "bug" {
		t.Fatalf("ticket_type=%v, want bug", got)
	}
}

func TestProjectRouteResolverFallsBackWithoutOnboarding(t *testing.T) {
	mention := Mention{Item: host.GitHubInboxItem{Kind: "issue", Number: "42", Title: "@kitsoki bug"}}
	route, applied, err := (ProjectRouteResolver{Root: t.TempDir()}).Apply(DefaultLabelStoryMap()["bug"], mention)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("project route applied for a repo without .kitsoki.yaml")
	}
	if route.Story != "stories/bugfix" {
		t.Fatalf("Story=%q, want default bugfix route", route.Story)
	}
}

func TestRunStorySessionProjectBeats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	appPath := filepath.Join(root, "stories", "dev-story", "app.yaml")
	tests := []struct {
		name    string
		fixture string
		kind    string
		number  string
	}{
		{name: "feature", fixture: projectBeatPath(projectFeatureBeat), kind: "issue", number: "99"},
		{name: "bug", fixture: projectBeatPath(projectBugBeat), kind: "issue", number: "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RunStorySession(context.Background(), Route{
				Story:       "project:.kitsoki/stories/sample-dev/app.yaml",
				AppPath:     appPath,
				BeatFixture: tt.fixture,
			}, &jobs.GHJob{
				JobID:        "job-" + tt.name,
				OriginRef:    "github:o/r/issue/" + tt.number,
				Repo:         "o/r",
				ObjectKind:   tt.kind,
				ObjectNumber: tt.number,
			})
			if err != nil {
				t.Fatalf("RunStorySession: %v", err)
			}
			if result.Turns < 1 {
				t.Fatalf("Turns=%d, want >=1", result.Turns)
			}
		})
	}
}
