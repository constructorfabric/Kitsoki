package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestGitHubFileBug_UploadSuccess proves the opt-in release-asset path: evidence
// is uploaded as release assets and the issue body links the public asset URLs.
func TestGitHubFileBug_UploadSuccess(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "screenshot.png")
	har := filepath.Join(dir, "har.json")
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(har, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var releaseCreated bool
	var uploads int
	var issueArgv string
	runner := func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		j := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "--version":
			return "gh version 2.x\n", "", 0, nil
		case strings.HasPrefix(j, "release view"):
			return "", "release not found", 1, nil // missing → triggers create
		case strings.HasPrefix(j, "release create"):
			releaseCreated = true
			return "", "", 0, nil
		case strings.HasPrefix(j, "release upload"):
			uploads++
			return "", "", 0, nil
		case strings.HasPrefix(j, "issue create"):
			issueArgv = j
			return "https://github.com/o/r/issues/777\n", "", 0, nil
		}
		return "", "unexpected: " + j, 1, nil
	}
	restore := host.SetExecRunnerForTest(runner)
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            "o/r",
		Title:           "web: gate",
		Body:            "body",
		TraceRef:        "trace://abc",
		UploadArtifacts: true,
		Evidence: []host.EvidenceFile{
			{Name: "screenshot.png", Path: shot, Image: true, Label: "Screenshot"},
			{Name: "har.json", Path: har, Label: "HAR"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if !releaseCreated {
		t.Error("expected release create when release missing")
	}
	if uploads != 2 {
		t.Errorf("expected 2 uploads, got %d", uploads)
	}
	// Asset URLs returned and threaded into the body.
	for _, name := range []string{"screenshot.png", "har.json"} {
		if !strings.HasPrefix(res.Assets[name], "https://github.com/o/r/releases/download/kitsoki-artifacts/") {
			t.Errorf("asset %q url = %q", name, res.Assets[name])
		}
	}
	for _, want := range []string{
		"uploaded as GitHub release assets",
		"![Screenshot](https://github.com/o/r/releases/download/kitsoki-artifacts/",
		"[HAR](https://github.com/o/r/releases/download/kitsoki-artifacts/",
	} {
		if !strings.Contains(issueArgv, want) {
			t.Errorf("issue body missing %q\nargv: %s", want, issueArgv)
		}
	}
	if strings.Contains(issueArgv, "not uploaded to GitHub") {
		t.Error("upload path must drop the not-uploaded disclaimer")
	}
}

// TestGitHubFileBug_UploadFailureFallsBack proves a graceful fallback: when the
// upload fails, the issue is still filed with developer-local path references.
func TestGitHubFileBug_UploadFailureFallsBack(t *testing.T) {
	var issueArgv string
	runner := func(ctx context.Context, d, name string, args ...string) (string, string, int, error) {
		j := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "--version":
			return "gh version 2.x\n", "", 0, nil
		case strings.HasPrefix(j, "release view"):
			return "", "", 0, nil // exists
		case strings.HasPrefix(j, "release upload"):
			return "", "boom: network down", 1, nil // upload fails
		case strings.HasPrefix(j, "issue create"):
			issueArgv = j
			return "https://github.com/o/r/issues/55\n", "", 0, nil
		}
		return "", "unexpected: " + j, 1, nil
	}
	restore := host.SetExecRunnerForTest(runner)
	defer restore()

	res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
		Repo:            "o/r",
		Title:           "x",
		Body:            "b",
		UploadArtifacts: true,
		Evidence: []host.EvidenceFile{
			{Name: "log.txt", Path: ".artifacts/b/log.txt", Label: "Log"},
		},
	})
	if err != nil {
		t.Fatalf("GitHubFileBug: %v", err)
	}
	if res.Number != "55" {
		t.Fatalf("issue should still be filed; number=%q", res.Number)
	}
	// Fell back to local-path rendering.
	for _, want := range []string{
		"These files are not uploaded to GitHub.",
		"Log: `.artifacts/b/log.txt`",
	} {
		if !strings.Contains(issueArgv, want) {
			t.Errorf("fallback body missing %q", want)
		}
	}
	if res.Assets["log.txt"] != ".artifacts/b/log.txt" {
		t.Errorf("fallback asset should be local path, got %q", res.Assets["log.txt"])
	}
}

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
