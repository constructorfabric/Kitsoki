package mining

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSlug pins the recap.sh:49-51 transform: every '/' and '.' in the absolute
// repo path becomes '-'. This is the load-bearing identity behind first-launch
// detection and the per-slug watermark.
func TestSlug(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/brad/code/Kitsoki", "-Users-brad-code-Kitsoki"},
		{"/Users/brad/code/Kitsoki/.worktrees/ad-hoc-workbench",
			"-Users-brad-code-Kitsoki--worktrees-ad-hoc-workbench"},
		{"/tmp/a.b.c", "-tmp-a-b-c"},
		{"/no-dots-here", "-no-dots-here"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, Slug(c.in), "slug(%q)", c.in)
	}
}

// TestResolverDir maps a repo path to ~/.claude/projects/<slug> against an
// injected HomeDir (never the real ~).
func TestResolverDir(t *testing.T) {
	home := t.TempDir()
	r := TranscriptResolver{HomeDir: home}

	dir, err := r.Dir("/Users/brad/code/Kitsoki")
	require.NoError(t, err)
	want := filepath.Join(home, ".claude", "projects", "-Users-brad-code-Kitsoki")
	assert.Equal(t, want, dir)
}

// TestResolverResolve confirms presence-aware resolution: a slug dir that exists
// is returned, a missing one yields an empty set (the "no history" no-op), and
// extra transcript_dirs are appended only when they exist.
func TestResolverResolve(t *testing.T) {
	home := t.TempDir()
	r := TranscriptResolver{HomeDir: home}

	repo := "/Users/brad/code/Kitsoki"
	primary := filepath.Join(home, ".claude", "projects", Slug(repo))

	// No history yet ⇒ empty (benign no-op, not an error).
	dirs, err := r.Resolve(repo, nil)
	require.NoError(t, err)
	assert.Empty(t, dirs, "no slug dir on disk ⇒ nothing to mine")

	// Create the slug dir ⇒ it resolves.
	require.NoError(t, os.MkdirAll(primary, 0o755))
	dirs, err = r.Resolve(repo, nil)
	require.NoError(t, err)
	require.Equal(t, []string{primary}, dirs)

	// An extra transcript dir that exists is appended; a missing one is dropped.
	extra := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	dirs, err = r.Resolve(repo, []string{extra, missing})
	require.NoError(t, err)
	assert.Equal(t, []string{primary, extra}, dirs)
}
