package store_test

// story_test.go — the effective story is captured into the trace and
// reconstructed back out byte-faithfully, so a trace is a self-contained,
// deterministic replay even after the story files change or vanish.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

const cloakApp = "../../testdata/apps/cloak/app.yaml"

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestCollectEffectiveStory_capturesTreeAndIsStable(t *testing.T) {
	t.Parallel()
	def, err := app.Load(cloakApp)
	require.NoError(t, err)

	es, err := store.CollectEffectiveStory(def)
	require.NoError(t, err)

	require.Equal(t, "app.yaml", es.Entry, "entry is the root manifest relative to the capture root")
	require.Contains(t, es.Files, "app.yaml")
	require.Contains(t, es.Files, "views/base.pongo", "view templates are part of the effective story")
	require.Contains(t, es.Files, "intents/foyer.yaml", "included manifests are captured by the tree walk")
	require.NotEmpty(t, es.Hash)

	// Hash is stable across calls over the same on-disk bytes.
	es2, err := store.CollectEffectiveStory(def)
	require.NoError(t, err)
	require.Equal(t, es.Hash, es2.Hash)
}

func TestStoryAtTurn_appliesDiffsInOrder(t *testing.T) {
	t.Parallel()

	base := &store.EffectiveStory{
		Entry: "app.yaml",
		Files: map[string][]byte{
			"app.yaml":           []byte("v1"),
			"views/base.pongo":   []byte("base-v1"),
			"intents/foyer.yaml": []byte("foyer"),
		},
	}
	base.Hash = "h0" // hash field unused by StoryAtTurn; payload carries it explicitly below

	snapPayload := store.StorySnapshotPayload("cloak", base)
	// Diff at turn 2: modify the view, delete the intents file.
	edited := &store.EffectiveStory{
		Entry: "app.yaml",
		Files: map[string][]byte{
			"app.yaml":         []byte("v1"),
			"views/base.pongo": []byte("base-v2"),
		},
		Hash: "h1",
	}
	diffPayload, changed := store.StoryChangedPayload("h0", base.Files, edited)
	require.True(t, changed)

	history := store.History{
		{Kind: store.StorySnapshot, Turn: 0, Payload: mustJSON(t, snapPayload)},
		{Kind: store.TurnEnded, Turn: 1, Payload: mustJSON(t, map[string]any{"outcome": "transitioned"})},
		{Kind: store.StoryChanged, Turn: 2, Payload: mustJSON(t, diffPayload)},
	}

	// As of turn 1 (before the diff) we get the base story.
	files1, entry1, err := store.StoryAtTurn(history, 1)
	require.NoError(t, err)
	require.Equal(t, "app.yaml", entry1)
	require.Equal(t, []byte("base-v1"), files1["views/base.pongo"])
	require.Contains(t, files1, "intents/foyer.yaml")

	// As of turn 2 the diff is applied: view changed, intents removed.
	files2, _, err := store.StoryAtTurn(history, 2)
	require.NoError(t, err)
	require.Equal(t, []byte("base-v2"), files2["views/base.pongo"])
	require.NotContains(t, files2, "intents/foyer.yaml", "removed file must be dropped")
	require.Equal(t, edited.Files, files2, "reconstruction equals the edited file set byte-for-byte")
}

func TestLoadFromFiles_roundTripsCloak(t *testing.T) {
	t.Parallel()
	def, err := app.Load(cloakApp)
	require.NoError(t, err)
	es, err := store.CollectEffectiveStory(def)
	require.NoError(t, err)

	// Reconstruct the AppDef from the captured files alone (no original dir).
	def2, cleanup, err := app.LoadFromFiles(es.Files, es.Entry)
	require.NoError(t, err)
	defer cleanup()
	require.Equal(t, def.App.ID, def2.App.ID)

	// Re-collecting from the materialised tree yields the same content hash.
	es2, err := store.CollectEffectiveStory(def2)
	require.NoError(t, err)
	require.Equal(t, es.Hash, es2.Hash, "round-trip is byte-faithful")
}

func TestBuildJourney_ignoresStoryEvents(t *testing.T) {
	t.Parallel()
	def, err := app.Load(cloakApp)
	require.NoError(t, err)
	es, err := store.CollectEffectiveStory(def)
	require.NoError(t, err)

	// A minimal history that transitions foyer → cloakroom, with story events
	// interleaved. The story events must not affect the fold.
	transition := store.Event{
		Kind:    store.TransitionApplied,
		Turn:    1,
		Payload: mustJSON(t, map[string]any{"from": "foyer", "to": "cloakroom"}),
	}
	withStory := store.History{
		{Kind: store.StorySnapshot, Turn: 0, Payload: mustJSON(t, store.StorySnapshotPayload("cloak", es))},
		transition,
		{Kind: store.StoryChanged, Turn: 1, Payload: mustJSON(t, map[string]any{"hash": "x"})},
	}
	withoutStory := store.History{transition}

	jWith, err := store.BuildJourney(def, "foyer", world.World{Vars: map[string]any{}}, withStory)
	require.NoError(t, err)
	jWithout, err := store.BuildJourney(def, "foyer", world.World{Vars: map[string]any{}}, withoutStory)
	require.NoError(t, err)

	require.Equal(t, jWithout.State, jWith.State, "story events are no-ops for the fold")
	require.Equal(t, app.StatePath("cloakroom"), jWith.State)
}
