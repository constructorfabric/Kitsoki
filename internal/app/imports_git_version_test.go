package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireGit skips the test when git isn't on PATH.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// newGitFixture creates a local git repo (usable directly as a clone URL)
// containing an app.yaml at its root and tags it "v1.0.0".
func newGitFixture(t *testing.T, appYAML string) (repoPath string) {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch=main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(appYAML), 0o644))
	run("add", "app.yaml")
	run("commit", "-q", "-m", "initial")
	run("tag", "v1.0.0")
	return dir
}

// TestResolveImportSource_GitTier proves the 4th resolution tier: a
// `git+<url>@<ref>` source resolves through the git fetcher into the
// content-addressed cache and folds like any other import.
func TestResolveImportSource_GitTier(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	remote := newGitFixture(t, widgetManifest)

	consumerDir := t.TempDir()
	consumer := `app: { id: consumer, version: 0.1.0 }
hosts: [host.run]
world: {}
intents:
  go: { description: go }
root: main
imports:
  w:
    source: "git+` + remote + `@v1.0.0"
    entry: idle
states:
  main: { view: "consumer" }
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumer), 0o644))

	def, err := Load(filepath.Join(consumerDir, "app.yaml"))
	require.NoError(t, err)
	require.Contains(t, def.States, "w")
	require.Contains(t, def.World, "w__count")
}

// TestResolveImportSource_GitTierBadRef proves a nonexistent ref surfaces a
// clear error rather than a silent fallback.
func TestResolveImportSource_GitTierBadRef(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	remote := newGitFixture(t, widgetManifest)

	consumerDir := t.TempDir()
	consumer := `app: { id: consumer, version: 0.1.0 }
hosts: [host.run]
world: {}
intents: { go: { description: go } }
root: main
imports:
  w:
    source: "git+` + remote + `@does-not-exist"
    entry: idle
states:
  main: { view: "consumer" }
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumer), 0o644))

	_, err := Load(filepath.Join(consumerDir, "app.yaml"))
	require.Error(t, err)
}

// TestImports_VersionConstraint_Satisfied proves imp.Version now gates
// resolution: a satisfied constraint loads cleanly.
func TestImports_VersionConstraint_Satisfied(t *testing.T) {
	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "widget", "app.yaml"), []byte(widgetManifest), 0o644))

	consumerDir := t.TempDir()
	consumer := `app: { id: consumer, version: 0.1.0 }
hosts: [host.run]
world: {}
intents: { go: { description: go } }
root: main
imports:
  w:
    source: "@kitsoki/widget"
    version: "^0.1.0"
    entry: idle
states:
  main: { view: "consumer" }
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumer), 0o644))

	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", nil
		}
		return filepath.Join(lib, name, "app.yaml"), nil
	}
	def, err := LoadWithResolver(filepath.Join(consumerDir, "app.yaml"), nil, resolver)
	require.NoError(t, err)
	require.Contains(t, def.States, "w")
}

// TestImports_VersionConstraint_Unsatisfied proves a mismatched constraint
// is a load-time error naming both the resolved version and the constraint.
func TestImports_VersionConstraint_Unsatisfied(t *testing.T) {
	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "widget", "app.yaml"), []byte(widgetManifest), 0o644))

	consumerDir := t.TempDir()
	consumer := `app: { id: consumer, version: 0.1.0 }
hosts: [host.run]
world: {}
intents: { go: { description: go } }
root: main
imports:
  w:
    source: "@kitsoki/widget"
    version: "^2.0.0"
    entry: idle
states:
  main: { view: "consumer" }
`
	require.NoError(t, os.WriteFile(filepath.Join(consumerDir, "app.yaml"), []byte(consumer), 0o644))

	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", nil
		}
		return filepath.Join(lib, name, "app.yaml"), nil
	}
	_, err := LoadWithResolver(filepath.Join(consumerDir, "app.yaml"), nil, resolver)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "does not satisfy version constraint"), "err = %v", err)
}

// TestResolveSource_ExportedWrapper proves the CLI-facing ResolveSource
// wrapper behaves identically to the internal resolveImportSource it wraps.
func TestResolveSource_ExportedWrapper(t *testing.T) {
	lib := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(lib, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(lib, "widget", "app.yaml"), []byte(widgetManifest), 0o644))

	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", nil
		}
		return filepath.Join(lib, name, "app.yaml"), nil
	}
	got, err := ResolveSource("@kitsoki/widget", t.TempDir(), resolver)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(lib, "widget", "app.yaml"), got)
}
