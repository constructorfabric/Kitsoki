package host_test

// semantic_sidecar_test.go — the v2 semantic-sidecar reader. Proves, with a temp
// dir + fixture JSON (no LLM, no network):
//
//   1. The pairing rule: `out.mp4` ⇒ `out.semantic.json`.
//   2. A present, well-formed sidecar parses; `ref` round-trips VERBATIM; the
//      optional label/selector/bbox/t_ms hints decode.
//   3. A MISSING sidecar is found=false with a nil error (not every artifact
//      declares semantics — the graceful case).
//   4. A malformed sidecar surfaces a non-nil error (found=false).
//   5. The DI seam: WithSemanticSidecarReader / FromCtx round-trips an injected
//      reader, and nil is a no-op.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

const sidecarFixture = `{
  "plugin": "slidey",
  "schema_version": 1,
  "elements": [
    {"ref": "scene-3.title", "label": "Title", "selector": "[data-slidey-el=scene-3.title]",
     "bbox": [120, 80, 640, 90], "t_ms": 4200},
    {"ref": "scene-3.cta", "label": "Call to action"}
  ]
}`

func TestSemanticSidecarPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "/a/b/out.semantic.json", host.SemanticSidecarPath("/a/b/out.mp4"))
	assert.Equal(t, "/a/b/page.semantic.json", host.SemanticSidecarPath("/a/b/page.html"))
	// Extension-less path just gets the suffix appended.
	assert.Equal(t, "/a/b/render.semantic.json", host.SemanticSidecarPath("/a/b/render"))
}

func TestDiskSemanticSidecarReader_Present(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifact := filepath.Join(dir, "out.mp4")
	require.NoError(t, os.WriteFile(host.SemanticSidecarPath(artifact), []byte(sidecarFixture), 0o644))

	sc, found, err := host.DiskSemanticSidecarReader{}.ReadSemanticSidecar(artifact)
	require.NoError(t, err)
	require.True(t, found, "a present sidecar must be found")

	assert.Equal(t, "slidey", sc.Plugin)
	assert.Equal(t, 1, sc.SchemaVersion)
	require.Len(t, sc.Elements, 2)

	assert.Equal(t, "scene-3.title", sc.Elements[0].Ref, "ref round-trips verbatim")
	assert.Equal(t, "Title", sc.Elements[0].Label)
	assert.Equal(t, "[data-slidey-el=scene-3.title]", sc.Elements[0].Selector)
	assert.Equal(t, [4]int{120, 80, 640, 90}, sc.Elements[0].Bbox)
	assert.Equal(t, 4200, sc.Elements[0].TMs)

	// The second element carries only ref + label — the optional hints are absent.
	assert.Equal(t, "scene-3.cta", sc.Elements[1].Ref)
	assert.Equal(t, [4]int{}, sc.Elements[1].Bbox)

	// The web/template projection round-trips ref verbatim and omits zero hints.
	m := sc.AsMap()
	els := m["elements"].([]map[string]any)
	assert.Equal(t, "scene-3.title", els[0]["ref"])
	_, hasBbox := els[1]["bbox"]
	assert.False(t, hasBbox, "an element with no bbox must omit it in the projection")
}

func TestDiskSemanticSidecarReader_Missing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifact := filepath.Join(dir, "lonely.mp4") // no sibling sidecar written

	sc, found, err := host.DiskSemanticSidecarReader{}.ReadSemanticSidecar(artifact)
	require.NoError(t, err, "a missing sidecar is not an error")
	assert.False(t, found, "no sidecar ⇒ found=false")
	assert.Empty(t, sc.Elements)
}

func TestDiskSemanticSidecarReader_Malformed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	artifact := filepath.Join(dir, "broken.mp4")
	require.NoError(t, os.WriteFile(host.SemanticSidecarPath(artifact), []byte("{not json"), 0o644))

	_, found, err := host.DiskSemanticSidecarReader{}.ReadSemanticSidecar(artifact)
	require.Error(t, err, "a present-but-malformed sidecar must surface an error")
	assert.False(t, found)
}

func TestSemanticSidecarReader_DI(t *testing.T) {
	t.Parallel()

	t.Run("injected reader round-trips via ctx", func(t *testing.T) {
		t.Parallel()
		want := host.SemanticSidecar{Plugin: "diagram", Elements: []host.SemanticElement{{Ref: "node-7"}}}
		r := host.SemanticSidecarReaderFunc(func(p string) (host.SemanticSidecar, bool, error) {
			return want, true, nil
		})
		ctx := host.WithSemanticSidecarReader(context.Background(), r)

		got := host.SemanticSidecarReaderFromCtx(ctx)
		require.NotNil(t, got)
		sc, found, err := got.ReadSemanticSidecar("anything")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "diagram", sc.Plugin)
		assert.Equal(t, "node-7", sc.Elements[0].Ref)
	})

	t.Run("nil reader is a no-op", func(t *testing.T) {
		t.Parallel()
		ctx := host.WithSemanticSidecarReader(context.Background(), nil)
		assert.Nil(t, host.SemanticSidecarReaderFromCtx(ctx),
			"no reader wired ⇒ FromCtx is nil (no semantic picks offered)")
	})
}
