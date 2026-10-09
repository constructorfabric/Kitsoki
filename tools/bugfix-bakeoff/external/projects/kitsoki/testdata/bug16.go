package main

// Hidden end-to-end oracle for bug1187. The candidate sees the public import
// layering contract, not this exact fixture.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/kitrepo"
)

func TestRepro_StoriesDirFallsThroughForMissingLibraryStory(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)
	t.Setenv(kitrepo.EnvVar, repoRoot)
	t.Setenv("KITSOKI_KIT_DEV_DEV_STORY", "")

	storiesDir := t.TempDir() // deliberately contains no dev-story
	downstream := filepath.Join(t.TempDir(), "app.yaml")
	require.NoError(t, os.WriteFile(downstream, []byte(`app:
  id: downstream
  version: "1"
imports:
  core:
    source: "@kitsoki/dev-story"
root: main
states:
  main:
    view: "downstream"
`), 0o644))

	def, err := app.LoadWithResolver(downstream, nil, studioImportResolver(storiesDir))
	require.NoError(t, err, "missing project override must fall through to the Kitsoki library")
	require.Contains(t, def.States, "core", "the imported library story must be folded into the downstream app")
}

func TestRepro_StoriesDirStillOverridesWhenPresent(t *testing.T) {
	storiesDir := t.TempDir()
	localDir := filepath.Join(storiesDir, "project-story")
	require.NoError(t, os.MkdirAll(localDir, 0o755))
	localApp := filepath.Join(localDir, "app.yaml")
	require.NoError(t, os.WriteFile(localApp, []byte("app:\n  id: project-story\n  version: \"1\"\nroot: idle\nworld: {}\nstates:\n  idle:\n    terminal: true\n"), 0o644))

	got, err := studioImportResolver(storiesDir)("project-story", "", true)
	require.NoError(t, err)
	require.Equal(t, localApp, got)
}
