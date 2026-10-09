package reportmeta_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/buildinfo"
	"kitsoki/internal/reportmeta"
)

func TestCaptureIncludesStoryAndPublicStoryChecksums(t *testing.T) {
	root := t.TempDir()
	storyDir := filepath.Join(root, "stories", "bug")
	require.NoError(t, os.MkdirAll(storyDir, 0o755))
	appPath := filepath.Join(storyDir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(`app:
  id: report-bug
  version: 1.2.3
root: start
states:
  start:
    terminal: true
    description: Start here.
    view: |
      Start here.
`), 0o644))

	def, err := app.Load(appPath)
	require.NoError(t, err)

	snap := reportmeta.Capture(root, def)
	require.Equal(t, "report-bug", snap.Story.AppID)
	require.Equal(t, "1.2.3", snap.Story.Version)
	require.Equal(t, "stories/bug/app.yaml", snap.Story.Entry)
	require.True(t, strings.HasPrefix(snap.Story.ChecksumSHA256, "sha256:"), snap.Story.ChecksumSHA256)
	require.Len(t, snap.PublicStories, 1)
	require.Equal(t, "bug", snap.PublicStories[0].Name)
	require.Equal(t, "report-bug", snap.PublicStories[0].AppID)
	require.Equal(t, "public", snap.PublicStories[0].Source)
	require.Equal(t, "stories/bug/app.yaml", snap.PublicStories[0].Path)
	require.True(t, strings.HasPrefix(snap.PublicStories[0].ChecksumSHA256, "sha256:"), snap.PublicStories[0].ChecksumSHA256)

	fields := map[string]string{}
	for _, f := range snap.Fields() {
		fields[f.Key] = f.Value
	}
	require.Equal(t, "report-bug", fields["story_app_id"])
	require.Equal(t, "1.2.3", fields["story_app_version"])
	require.True(t, strings.HasPrefix(fields["story_checksum_sha256"], "sha256:"), fields["story_checksum_sha256"])
	require.Contains(t, fields["public_stories_json"], `"name":"bug"`)
	require.Contains(t, fields["public_stories_json"], `"checksum_sha256":"sha256:`)
}

func TestCapturePrefersStampedBuildInfoRevision(t *testing.T) {
	oldVersion := buildinfo.Version
	oldRevision := buildinfo.Revision
	oldRevisionShort := buildinfo.RevisionShort
	t.Cleanup(func() {
		buildinfo.Version = oldVersion
		buildinfo.Revision = oldRevision
		buildinfo.RevisionShort = oldRevisionShort
	})

	buildinfo.Version = "buildinfo-version"
	buildinfo.Revision = "1234567890abcdef1234567890abcdef12345678"
	buildinfo.RevisionShort = "1234567"

	snap := reportmeta.Capture(t.TempDir(), nil)
	require.Equal(t, "buildinfo-version", snap.Engine.Version)
	require.Equal(t, "1234567890abcdef1234567890abcdef12345678", snap.Engine.Revision)
	require.Equal(t, "1234567", snap.Engine.RevisionShort)

	fields := map[string]string{}
	for _, f := range snap.Fields() {
		fields[f.Key] = f.Value
	}
	require.Equal(t, "1234567890abcdef1234567890abcdef12345678", fields["engine_revision"])
	require.Equal(t, "1234567", fields["engine_revision_short"])
}
