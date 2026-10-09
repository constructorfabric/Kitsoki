package studio_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// story_search_test.go — verification for story.list / story.search over the
// real on-disk Cloak story. Deterministic and LLM-free.

func TestStoryList_ListsFiles(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	var out struct {
		OK    bool     `json:"ok"`
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	}
	res, err := callTool(ctx, cs, "story.list", map[string]any{"dir": cloakDir(t)})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.True(t, out.OK)
	assert.Contains(t, out.Files, "app.yaml")

	// Glob narrows to the manifest only.
	var globbed struct {
		Files []string `json:"files"`
	}
	res, err = callTool(ctx, cs, "story.list", map[string]any{"dir": cloakDir(t), "glob": "*.yaml"})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &globbed))
	assert.Contains(t, globbed.Files, "app.yaml")
}

func TestStorySearch_FindsHits(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	type hit struct {
		File string `json:"file"`
		Line int    `json:"line"`
		Text string `json:"text"`
	}
	var out struct {
		OK   bool  `json:"ok"`
		Hits []hit `json:"hits"`
	}
	res, err := callTool(ctx, cs, "story.search", map[string]any{"dir": cloakDir(t), "pattern": "cloakroom"})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.True(t, out.OK)
	require.NotEmpty(t, out.Hits, "expected at least one 'cloakroom' hit in the cloak story")
	var inApp bool
	for _, h := range out.Hits {
		assert.Greater(t, h.Line, 0)
		if h.File == "app.yaml" {
			inApp = true
		}
	}
	assert.True(t, inApp, "expected a 'cloakroom' hit in app.yaml; hits=%+v", out.Hits)

	// A pattern that cannot match returns an empty (not errored) result.
	res, err = callTool(ctx, cs, "story.search", map[string]any{"dir": cloakDir(t), "pattern": "zzz-no-such-token-zzz"})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.Empty(t, out.Hits)

	// Missing pattern is rejected.
	res, err = callTool(ctx, cs, "story.search", map[string]any{"dir": cloakDir(t)})
	assertRejected(t, res, err)
}
