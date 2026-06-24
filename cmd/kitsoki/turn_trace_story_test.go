package main

// turn_trace_story_test.go — `kitsoki turn --trace` reconstructs the story from
// the trace itself when --app is omitted, so a continued turn keeps working
// after the story files on disk are gone.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/store"
)

func copyDirForTest(t *testing.T, src, dst string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	}))
}

func newTraceTurnCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd
}

func TestTraceTurn_ReconstructsStoryFromTrace(t *testing.T) {
	// Copy the cloak story so we can delete it after seeding the trace.
	storyDir := filepath.Join(t.TempDir(), "cloak")
	copyDirForTest(t, filepath.Join("..", "..", "testdata", "apps", "cloak"), storyDir)
	appPath := filepath.Join(storyDir, "app.yaml")
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	// Turn 1, WITH --app: records the base story snapshot + the turn.
	require.NoError(t, runTraceTurn(newTraceTurnCmd(), appPath, tracePath, "go", []string{"direction=west"}))

	// The story files are gone — only the trace remains.
	require.NoError(t, os.RemoveAll(storyDir))

	// Turn 2, WITHOUT --app: the story is reconstructed from the trace alone.
	// hang_cloak is valid in cloakroom and transitions (self), so the turn is
	// accepted and runTraceTurn returns nil.
	err := runTraceTurn(newTraceTurnCmd(), "", tracePath, "hang_cloak", nil)
	require.NoError(t, err, "continued turn must reconstruct the story from the trace with no --app and no story files on disk")
}

func TestTraceTurn_NoAppNoSnapshotIsClearError(t *testing.T) {
	// A trace that predates story snapshots (header only) can't be reconstructed
	// without --app; the error should say so rather than panic.
	tracePath := filepath.Join(t.TempDir(), "empty.jsonl")
	sink, err := store.OpenJSONL(tracePath) // writes header only
	require.NoError(t, err)
	require.NoError(t, sink.Close()) // release the flock so runTraceTurn can reopen

	err = runTraceTurn(newTraceTurnCmd(), "", tracePath, "go", []string{"direction=west"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reconstruct story from trace")
}
