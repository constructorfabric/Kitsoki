package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultTracePath_Shape verifies the path structure:
//   - ends with .jsonl
//   - contains the sha8 prefix (8 hex chars)
//   - contains the slug suffix (transport:thread with unsafe chars replaced by '-')
func TestDefaultTracePath_Shape(t *testing.T) {
	t.Parallel()
	path := DefaultTracePath("myapp", "jira", "PLTFRM-12345")

	if !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("path %q does not end with .jsonl", path)
	}

	// The filename is <sha8>-<slug>.jsonl.
	base := filepath.Base(path)
	// sha8 is 8 hex chars; slug is "jira-PLTFRM-12345". The filename leads
	// with the sha8, so it must NOT begin with the slug's "jira" prefix.
	if strings.HasPrefix(base, "jira") {
		t.Errorf("filename should begin with the sha8, not the slug, got %q", base)
	}

	// slug replaces ':' with '-'
	if !strings.Contains(base, "jira-PLTFRM-12345") {
		t.Errorf("expected slug 'jira-PLTFRM-12345' in filename, got %q", base)
	}

	// sha8 must be exactly 8 hex characters before the first '-'
	parts := strings.SplitN(base, "-", 2)
	if len(parts) < 2 {
		t.Fatalf("expected sha8-slug form, got %q", base)
	}
	sha8 := parts[0]
	if len(sha8) != 8 {
		t.Errorf("expected 8-char sha8 prefix, got %q (len %d)", sha8, len(sha8))
	}
	for _, c := range sha8 {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("sha8 %q contains non-hex character %q", sha8, c)
		}
	}

	// Path must include the app slug as a directory component.
	dir := filepath.Dir(path)
	if !strings.HasSuffix(dir, "myapp") {
		t.Errorf("expected 'myapp' as final dir component of %q", dir)
	}
}

// TestDefaultTracePath_SlashInThread verifies that '/' in the thread component
// is replaced by '-' in the slug (path-safety).
func TestDefaultTracePath_SlashInThread(t *testing.T) {
	t.Parallel()
	path := DefaultTracePath("app", "bitbucket", "proj/REPO-42")
	base := filepath.Base(path)
	if strings.Contains(base, "/") {
		t.Errorf("filename %q still contains '/' from thread component", base)
	}
	if !strings.Contains(base, "bitbucket-proj-REPO-42") {
		t.Errorf("expected 'bitbucket-proj-REPO-42' slug in filename, got %q", base)
	}
}

// TestDefaultTracePath_Collision verifies that two different transport:thread
// values produce different paths (the sha8 prefix distinguishes them even when
// the slug might coincide after unsafe-char substitution).
func TestDefaultTracePath_Collision(t *testing.T) {
	t.Parallel()
	p1 := DefaultTracePath("app", "jira", "ABC-1")
	p2 := DefaultTracePath("app", "jira", "ABC-2")
	if p1 == p2 {
		t.Errorf("different keys produced the same path: %q", p1)
	}
}

// TestDefaultTracePath_Determinism verifies that the same inputs always produce
// the same output (no random/timestamp component).
func TestDefaultTracePath_Determinism(t *testing.T) {
	t.Parallel()
	for i := 0; i < 5; i++ {
		p := DefaultTracePath("myapp", "jira", "PLTFRM-99")
		expected := DefaultTracePath("myapp", "jira", "PLTFRM-99")
		if p != expected {
			t.Errorf("call %d: got %q, want %q", i, p, expected)
		}
	}
}

// TestDefaultTracePath_UnsafeAppName verifies that app IDs with slashes
// or spaces are sanitised in the directory component.
func TestDefaultTracePath_UnsafeAppName(t *testing.T) {
	t.Parallel()
	path := DefaultTracePath("my/app with spaces", "t", "k")
	// The app directory component must not contain '/' (other than the
	// directory separators inserted by filepath.Join).
	// Split off the sessions/<app> portion.
	parts := strings.Split(path, string(filepath.Separator))
	// Find "sessions" index and check the next part.
	for i, p := range parts {
		if p == "sessions" && i+1 < len(parts) {
			appDir := parts[i+1]
			if strings.Contains(appDir, "/") || strings.Contains(appDir, " ") {
				t.Errorf("app dir component %q is not slug-sanitised", appDir)
			}
			return
		}
	}
	t.Errorf("could not locate 'sessions' dir component in %q", path)
}
