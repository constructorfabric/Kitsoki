package host_test

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestGitHubFileBug_WithEvidence proves the slice-#2 orchestration: create the
// issue with an Artifacts section (developer-local evidence paths) and the
// ```kitsoki metadata block.
func TestGitHubFileBug_WithEvidence(t *testing.T) {
	var createdIssue bool
	var issueArgv string
	runner := func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		j := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "--version":
			return "gh version 2.x\n", "", 0, nil
		case strings.HasPrefix(j, "release"):
			t.Fatalf("evidence must not use gh release commands: %s", j)
		case strings.HasPrefix(j, "issue create"):
			createdIssue = true
			issueArgv = j
			return "https://github.com/o/r/issues/321\n", "", 0, nil
		}
		return "", "unexpected: " + j, 1, nil
	}
	restore := host.SetExecRunnerForTest(runner)
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:      "o/r",
		Title:     "web: surprising judge gate",
		Body:      "The gate fired where I didn't expect.",
		Severity:  "P2",
		Component: "web",
		Target:    "kitsoki",
		TraceRef:  "trace://x",
		FiledBy:   "brad",
		Evidence: []host.EvidenceFile{
			{Name: "screenshot.png", Path: ".artifacts/bug-reports/b1/screenshot.png", Image: true, Label: "Screenshot"},
			{Name: "har.json", Path: ".artifacts/bug-reports/b1/har.json", Label: "HAR (scrubbed)"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if !createdIssue {
		t.Fatal("expected issue create")
	}
	if res.Number != "321" || !strings.Contains(res.URL, "/issues/321") {
		t.Fatalf("issue result: %+v", res)
	}
	if res.Assets["screenshot.png"] == "" || res.Assets["har.json"] == "" {
		t.Fatalf("asset URLs missing: %+v", res.Assets)
	}
	// The issue body must carry the Artifacts section (developer-local paths) and
	// the ```kitsoki metadata block.
	for _, want := range []string{
		"## Artifacts",
		"These files are not uploaded to GitHub.",
		"Screenshot: `.artifacts/bug-reports/b1/screenshot.png` (screenshot)",
		"HAR (scrubbed): `.artifacts/bug-reports/b1/har.json`",
		"```kitsoki",
		"trace_ref: trace://x",
		"--label P2",
		"--label comp:web",
		"--label target:kitsoki",
	} {
		if !strings.Contains(issueArgv, want) {
			t.Errorf("issue create argv missing %q", want)
		}
	}
}

// TestGitHubFileBug_NoEvidence skips the release path entirely (text-only file).
func TestGitHubFileBug_NoEvidence(t *testing.T) {
	var touchedRelease bool
	runner := func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		j := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "--version":
			return "gh version 2.x\n", "", 0, nil
		case strings.HasPrefix(j, "release"):
			touchedRelease = true
			return "", "", 0, nil
		case strings.HasPrefix(j, "issue create"):
			return "https://github.com/o/r/issues/9\n", "", 0, nil
		}
		return "", "unexpected: " + j, 1, nil
	}
	restore := host.SetExecRunnerForTest(runner)
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo: "o/r", Title: "text only", Body: "no evidence", Severity: "P3",
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if touchedRelease {
		t.Fatal("no evidence → must not touch the release path")
	}
	if res.Number != "9" {
		t.Fatalf("number: %q", res.Number)
	}
}
