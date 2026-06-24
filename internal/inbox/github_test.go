package inbox_test

import (
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
)

func TestGitHubOriginRefUsesExplicitRepo(t *testing.T) {
	got := inbox.GitHubOriginRef("acme/repo", host.GitHubInboxItem{
		Kind:   "pr",
		Number: "42",
		URL:    "https://github.com/other/project/pull/42",
	})
	if got != "github:acme/repo/pr/42" {
		t.Fatalf("origin ref = %q", got)
	}
}

func TestGitHubOriginRefInfersRepoFromURL(t *testing.T) {
	got := inbox.GitHubOriginRef("", host.GitHubInboxItem{
		Kind:   "issue",
		Number: "7",
		URL:    "https://github.com/acme/repo/issues/7",
	})
	if got != "github:acme/repo/issue/7" {
		t.Fatalf("origin ref = %q", got)
	}
}

func TestGitHubOriginRefFallsBackWhenRepoUnknown(t *testing.T) {
	got := inbox.GitHubOriginRef("", host.GitHubInboxItem{
		Kind:   "issue",
		Number: "7",
		URL:    "https://example.com/acme/repo/issues/7",
	})
	if got != "github:issue/7" {
		t.Fatalf("origin ref = %q", got)
	}
}
