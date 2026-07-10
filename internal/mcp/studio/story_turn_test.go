package studio_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// story_turn_test.go — verification for story.turn. Deterministic and LLM-free:
// it drives the classic Cloak of Darkness story (testdata/apps/cloak), a pure
// machine story with no host/LLM calls, through its foyer→cloakroom transition.

func cloakDir(t *testing.T) string {
	return filepath.Join(repoRoot(t), "testdata", "apps", "cloak")
}

func TestStoryTurn_AppliesTransition(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	var out struct {
		Mode        string         `json:"mode"`
		PrevState   string         `json:"prev_state"`
		NextState   string         `json:"next_state"`
		WorldBefore map[string]any `json:"world_before"`
		WorldAfter  map[string]any `json:"world_after"`
	}
	res, err := callTool(ctx, cs, "story.turn", map[string]any{
		"dir":    cloakDir(t),
		"state":  "foyer",
		"intent": "go",
		"slots":  map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))

	assert.Equal(t, "foyer", out.PrevState)
	assert.Equal(t, "cloakroom", out.NextState)
	assert.Contains(t, out.WorldBefore, "wearing_cloak")
	assert.Contains(t, out.WorldAfter, "wearing_cloak")
}

func TestStoryTurn_RequiresStateAndIntent(t *testing.T) {
	ctx := context.Background()
	cs := newStudioNoWorkspace(ctx, t)

	// Missing intent.
	res, err := callTool(ctx, cs, "story.turn", map[string]any{"dir": cloakDir(t), "state": "foyer"})
	assertRejected(t, res, err)

	// Missing state.
	res, err = callTool(ctx, cs, "story.turn", map[string]any{"dir": cloakDir(t), "intent": "go"})
	assertRejected(t, res, err)
}
