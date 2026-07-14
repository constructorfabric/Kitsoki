package studio

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveTracePath_OverrideWins confirms an explicit trace: arg is used
// verbatim, unchanged from before this fix.
func TestResolveTracePath_OverrideWins(t *testing.T) {
	got, err := resolveTracePath("/tmp/explicit-trace.jsonl", "stories/bugfix/app.yaml", "")
	if err != nil {
		t.Fatalf("resolveTracePath: %v", err)
	}
	if got != "/tmp/explicit-trace.jsonl" {
		t.Fatalf("expected override path verbatim, got %q", got)
	}
}

// TestResolveTracePath_DefaultIsDiscoverable is the regression test for
// issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md:
// with no override, the trace must land under ~/.kitsoki/sessions/<app>/, not
// an anonymous $TMPDIR file, so `kitsoki trace --app <app> --latest` can find
// it after the fact.
func TestResolveTracePath_DefaultIsDiscoverable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := resolveTracePath("", "stories/bugfix/app.yaml", "fixed-session-key")
	if err != nil {
		t.Fatalf("resolveTracePath: %v", err)
	}

	wantDir := filepath.Join(home, ".kitsoki", "sessions", "bugfix")
	gotDir := filepath.Dir(got)
	if gotDir != wantDir {
		t.Fatalf("expected trace under %q (discoverable by app), got dir %q (full path %q)", wantDir, gotDir, got)
	}
	if !strings.Contains(filepath.Base(got), "mcp") {
		t.Fatalf("expected the mcp transport label in the trace filename, got %q", filepath.Base(got))
	}

	// Same story + same key must be deterministic (matches store.DefaultTracePath's
	// sha8-of-transport:thread contract), so re-attaching with the same key finds
	// the same file rather than fragmenting across runs.
	got2, err := resolveTracePath("", "stories/bugfix/app.yaml", "fixed-session-key")
	if err != nil {
		t.Fatalf("resolveTracePath (second call): %v", err)
	}
	if got != got2 {
		t.Fatalf("expected the same key to resolve to the same path: %q vs %q", got, got2)
	}
}

// TestResolveTracePath_NoKeyStillDiscoverableAndUnique confirms session.new's
// common case (no explicit key) still lands under the discoverable app dir,
// and two calls don't collide.
func TestResolveTracePath_NoKeyStillDiscoverableAndUnique(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	a, err := resolveTracePath("", "stories/bugfix/app.yaml", "")
	if err != nil {
		t.Fatalf("resolveTracePath: %v", err)
	}
	b, err := resolveTracePath("", "stories/bugfix/app.yaml", "")
	if err != nil {
		t.Fatalf("resolveTracePath: %v", err)
	}
	wantDir := filepath.Join(home, ".kitsoki", "sessions", "bugfix")
	if filepath.Dir(a) != wantDir || filepath.Dir(b) != wantDir {
		t.Fatalf("expected both under %q, got %q and %q", wantDir, a, b)
	}
	if a == b {
		t.Fatalf("expected two key-less calls to generate distinct trace files, got the same path twice: %q", a)
	}
}

func TestAppSlugFromStoryPath(t *testing.T) {
	cases := map[string]string{
		"stories/bugfix/app.yaml":    "bugfix",
		"stories/dev-story/app.yaml": "dev-story",
		"app.yaml":                   "app",
		"":                           "app",
		filepath.Join("a", "b.yaml"): "a",
	}
	for in, want := range cases {
		if got := appSlugFromStoryPath(in); got != want {
			t.Errorf("appSlugFromStoryPath(%q) = %q, want %q", in, got, want)
		}
	}
}
