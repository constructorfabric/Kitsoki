package host

import (
	"context"
	"testing"
)

// The gh CLI fallback is the last token source: env and secrets always win,
// and a logged-out / missing gh resolves to "" so missing-auth guidance still
// fires. These are package-internal tests because githubToken is unexported.

func TestGitHubToken_FallsBackToGHCLI(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restore := SetGHCLITokenForTest(func(context.Context) string { return "gh-cli-tok" })
	defer restore()

	if got := githubToken(context.Background()); got != "gh-cli-tok" {
		t.Fatalf("expected gh CLI fallback token, got %q", got)
	}
}

func TestGitHubToken_EnvWinsOverGHCLI(t *testing.T) {
	t.Setenv("GH_TOKEN", "env-tok")
	t.Setenv("HOME", t.TempDir())
	restore := SetGHCLITokenForTest(func(context.Context) string {
		t.Fatal("gh CLI fallback must not run when GH_TOKEN is set")
		return ""
	})
	defer restore()

	if got := githubToken(context.Background()); got != "env-tok" {
		t.Fatalf("expected env token to win, got %q", got)
	}
}

func TestGitHubToken_EmptyWhenGHCLILoggedOut(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restore := SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restore()

	if got := githubToken(context.Background()); got != "" {
		t.Fatalf("expected empty token, got %q", got)
	}
}
