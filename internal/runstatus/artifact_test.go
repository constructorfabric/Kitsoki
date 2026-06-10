package runstatus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus"
)

const fakeIndex = `<!doctype html><html><head><title>x</title></head>` +
	`<body><div id="app"></div><script type="module">boot()</script></body></html>`

func TestRenderArtifact_Injection(t *testing.T) {
	t.Parallel()

	snap := []byte(`{"session":{"session_id":"s-1"},"events":[]}`)
	out, err := runstatus.RenderArtifact([]byte(fakeIndex), snap, runstatus.ArtifactOptions{
		Name:         "demo",
		Commit:       "abc123",
		Branch:       "main",
		BuiltAt:      time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC),
		RegenComment: runstatus.RegenComment("fixtures/demo.snapshot.json", ".artifacts/demo.html"),
	})
	require.NoError(t, err)
	html := string(out)

	// Regen comment is at the very top.
	assert.True(t, strings.HasPrefix(html, "<!--"), "regen comment must lead the file")
	assert.Contains(t, html, "kitsoki export-status --from-snapshot fixtures/demo.snapshot.json")

	// Snapshot tag is present and sits before the SPA's own boot <script>, so
	// it is in the DOM when main.ts reads #kitsoki-snapshot.
	tagIdx := strings.Index(html, `id="kitsoki-snapshot"`)
	bootIdx := strings.Index(html, `<script type="module">boot()`)
	require.NotEqual(t, -1, tagIdx, "snapshot tag must be injected")
	require.NotEqual(t, -1, bootIdx)
	assert.Less(t, tagIdx, bootIdx, "snapshot tag must precede the boot script")
	assert.Contains(t, html, `"session_id":"s-1"`, "snapshot JSON is inlined")

	// Banner is the first child of <body> and carries the provenance fields.
	bodyIdx := strings.Index(html, "<body>")
	bannerIdx := strings.Index(html, "kitsoki-artifact-banner")
	require.NotEqual(t, -1, bannerIdx)
	assert.Less(t, bodyIdx, bannerIdx)
	assert.Contains(t, html, "demo")
	assert.Contains(t, html, "abc123")
	assert.Contains(t, html, "2026-05-31T12:00:00Z")
}

func TestRenderArtifact_EmptyIndex(t *testing.T) {
	t.Parallel()
	_, err := runstatus.RenderArtifact(nil, []byte(`{}`), runstatus.ArtifactOptions{})
	require.Error(t, err, "an empty SPA index must be rejected")
}

// TestRenderArtifact_InlinesSidecars verifies prompt_file/system_prompt_file
// references are inlined from disk (relative to SidecarDir) and the file refs
// dropped, so the artifact is self-contained under file://.
func TestRenderArtifact_InlinesSidecars(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "p.txt"), []byte("THE PROMPT"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "s.txt"), []byte("THE SYSTEM"), 0o644))

	snap := []byte(`{"events":[{"attrs":{"prompt_file":"p.txt","system_prompt_file":"s.txt"}}]}`)
	out, err := runstatus.RenderArtifact([]byte(fakeIndex), snap, runstatus.ArtifactOptions{
		Name:       "sidecars",
		SidecarDir: dir,
	})
	require.NoError(t, err)
	html := string(out)

	assert.Contains(t, html, "THE PROMPT")
	assert.Contains(t, html, "THE SYSTEM")
	// The file-ref keys are dropped once inlined.
	assert.NotContains(t, html, "prompt_file")
	assert.NotContains(t, html, "system_prompt_file")
}

// TestRenderArtifact_InlinesTranscriptSidecars verifies an event's
// transcript_ref pointer is resolved against SidecarDir and the verbatim
// <call_id>.jsonl events + .timings offsets are inlined into attrs.transcript,
// so a static-export HTML can render the "Agent actions" drawer with no server.
func TestRenderArtifact_InlinesTranscriptSidecars(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tdir := filepath.Join(dir, "transcripts")
	require.NoError(t, os.MkdirAll(tdir, 0o755))
	const callID = "4e96533378e89461"
	jsonl := `{"type":"assistant","message":{"content":[{"type":"text","text":"reading"}]}}
{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":12,"output_tokens":4}}
`
	require.NoError(t, os.WriteFile(filepath.Join(tdir, callID+".jsonl"), []byte(jsonl), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tdir, callID+".timings"), []byte("0 0\n1 250\n"), 0o644))

	snap := []byte(`{"events":[{"attrs":{"call_id":"` + callID + `","verb":"task",` +
		`"transcript_ref":{"format":"claude-stream-json","path":"transcripts/` + callID + `.jsonl","events":2,"schema_version":1}}}]}`)
	out, err := runstatus.RenderArtifact([]byte(fakeIndex), snap, runstatus.ArtifactOptions{
		Name:       "transcript",
		SidecarDir: dir,
	})
	require.NoError(t, err)
	html := string(out)

	// The inlined transcript carries the verbatim event text and the timings.
	assert.Contains(t, html, `"transcript"`)
	assert.Contains(t, html, "reading")
	assert.Contains(t, html, "250")
	// The pointer survives (it carries the affordance badge count).
	assert.Contains(t, html, "transcript_ref")
}

// TestRenderArtifact_NoSidecarDir leaves the snapshot JSON untouched when no
// SidecarDir is given (in-process snapshots already carry inline prompts).
func TestRenderArtifact_NoSidecarDir(t *testing.T) {
	t.Parallel()

	snap := []byte(`{"events":[{"attrs":{"prompt_file":"p.txt"}}]}`)
	out, err := runstatus.RenderArtifact([]byte(fakeIndex), snap, runstatus.ArtifactOptions{})
	require.NoError(t, err)
	// Pass-through: the ref survives because inlining was not requested.
	assert.Contains(t, string(out), "prompt_file")
}
