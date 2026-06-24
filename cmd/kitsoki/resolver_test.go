package main

import (
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/basestories"
	"kitsoki/internal/kitrepo"

	"github.com/stretchr/testify/require"
)

// TestEmbeddedDevStoryResolvesWithoutCheckout is the slice-1 end-to-end load
// smoke: a foreign repo carrying ONLY a tiny instance that imports
// `@kitsoki/dev-story` loads against the embedded story library when no
// kitsoki checkout is present and no --kitsoki-repo override is set. No LLM is
// involved — this is a pure load (parse + fold + validate).
//
// Skips when the library was not staged into the test binary (a bare `go test`
// without `make embed-stories`), matching basestories.ErrNotStaged.
func TestEmbeddedDevStoryResolvesWithoutCheckout(t *testing.T) {
	// Hermetic cache; never touch the developer's ~/.cache/kitsoki.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// No override, no persisted-repo leakage: the resolver must use the
	// embedded fallback, not an ambient checkout.
	t.Setenv(kitrepo.EnvVar, "")

	// Bail out early with a skip (not a failure) when unstaged.
	if _, err := basestories.Materialize(t.Context()); err == basestories.ErrNotStaged {
		t.Skip("story library not staged into the test binary; run `make embed-stories`")
	}

	// Foreign repo: a tmpdir with NO go.mod/.kitsoki-root so findRepoRoot fails
	// and only the embedded fallback can resolve the import.
	repo := t.TempDir()
	instanceDir := filepath.Join(repo, ".kitsoki", "myteam")
	require.NoError(t, os.MkdirAll(instanceDir, 0o755))
	instance := `app: { id: myteam-dev, version: 0.1.0 }
imports:
  core:
    source: "@kitsoki/dev-story"
root: main
states:
  main: { view: "myteam" }
`
	appPath := filepath.Join(instanceDir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(instance), 0o644))

	def, err := app.LoadWithResolver(appPath, nil, buildImportResolver())
	require.NoError(t, err, "@kitsoki/dev-story must resolve from the embedded library with no checkout")
	require.NotNil(t, def)
	// dev-story folds under alias `core`; its own ../bugfix etc. resolve
	// relative to the materialized dev-story dir.
	require.Contains(t, def.States, "core", "dev-story should fold under the `core` alias")
}
