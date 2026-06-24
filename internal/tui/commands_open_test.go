package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tuipkg "kitsoki/internal/tui"
)

// openModel builds a cloak RootModel with a recording opener installed and
// returns the model plus a pointer to the slice of paths the opener was
// asked to open. The opener never launches a real process.
func openModel(t *testing.T) (tuipkg.RootModel, *[]string) {
	t.Helper()
	orch, sid := setupCloak(t)
	rm, ok := tuipkg.ExtractRootModel(buildModel(t, orch, sid))
	require.True(t, ok)
	var opened []string
	tuipkg.SetOpenArtifactForTest(&rm, func(path string) error {
		opened = append(opened, path)
		return nil
	})
	return rm, &opened
}

// TestOpenSlash_ResolvesRelativeAndOpens: /open <rel> resolves the path
// against the working directory and hands the absolute path to the opener,
// reporting success in the transcript.
func TestOpenSlash_ResolvesRelativeAndOpens(t *testing.T) {
	rm, opened := openModel(t)

	// A real file under the working dir so the existence check passes.
	dir := t.TempDir()
	rel := "brief.md"
	abs := filepath.Join(dir, rel)
	require.NoError(t, os.WriteFile(abs, []byte("# brief\n"), 0o644))

	rm, _ = tuipkg.HandleSlashCommandForTest(rm, "/open "+abs)

	require.Len(t, *opened, 1)
	require.Equal(t, abs, (*opened)[0])
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "opened")
}

// TestOpenSlash_MissingFileReports: a path that does not exist is reported,
// not handed to the opener.
func TestOpenSlash_MissingFileReports(t *testing.T) {
	rm, opened := openModel(t)

	rm, _ = tuipkg.HandleSlashCommandForTest(rm, "/open "+filepath.Join(t.TempDir(), "nope.md"))

	require.Empty(t, *opened)
	require.Contains(t, tuipkg.GetTranscriptContent(rm), "not found")
}

// TestOpenSlash_NoArgShowsUsage: bare /open prints usage and opens nothing.
func TestOpenSlash_NoArgShowsUsage(t *testing.T) {
	rm, opened := openModel(t)

	rm, _ = tuipkg.HandleSlashCommandForTest(rm, "/open")

	require.Empty(t, *opened)
	require.True(t, strings.Contains(tuipkg.GetTranscriptContent(rm), "usage"))
}
