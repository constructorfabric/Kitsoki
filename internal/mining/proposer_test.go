package mining

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// stubMapper returns a fixed status (and records what it was asked to classify).
type stubMapper struct {
	status    MapStatus
	gotRecipe Recipe
	gotInv    []InventoryRow
}

func (m *stubMapper) Classify(_ context.Context, r Recipe, inv []InventoryRow) (MapStatus, error) {
	m.gotRecipe = r
	m.gotInv = inv
	return m.status, nil
}

// stubDrafter returns a fixed delta. It records the rung it was asked to draft
// at so the rung-selection assertion can prove the proposer passes the lightest
// fit.
type stubDrafter struct {
	files    map[string][]byte
	artifact json.RawMessage
	gotRung  int
}

func (d *stubDrafter) Draft(_ context.Context, _ Recipe, rung int) (DraftResult, error) {
	d.gotRung = rung
	return DraftResult{Files: d.files, Artifact: d.artifact}, nil
}

func goodAuthorArtifact() json.RawMessage {
	return json.RawMessage(`{
		"files_changed": ["app.yaml"],
		"files_changed_display": "1 file: app.yaml",
		"diff_path": ".artifacts/mining/r-bind/author.diff",
		"flows_green": true,
		"summary_markdown": "Bind iface.ticket to host.local_files.ticket so the repeated free-form ticket lookup routes deterministically."
	}`)
}

func authorSchema(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "stories", "dev-story-mining", "schemas", "author_artifact.json"))
	require.NoError(t, err)
	return b
}

// recorderSink captures appended mining events in memory.
type recorderSink struct {
	events []recordedEvent
}

type recordedEvent struct {
	kind    store.EventKind
	payload json.RawMessage
}

func (r *recorderSink) AppendMiningEvent(_ app.SessionID, kind store.EventKind, payload json.RawMessage) error {
	r.events = append(r.events, recordedEvent{kind: kind, payload: payload})
	return nil
}

func sessionSink(r *recorderSink) *SessionSink {
	return &SessionSink{SID: "sess-1", Sink: r}
}

// TestRungFor locks in the lightest-rung-per-kind policy from the proposal's
// table.
func TestRungFor(t *testing.T) {
	cases := []struct {
		kind      DeltaKind
		needsRoom bool
		want      int
	}{
		{KindBinding, false, 1},
		{KindWorld, false, 1},
		{KindIntent, false, 1}, // slot-template synonym
		{KindIntent, true, 2},  // a new intents: entry that needs a room
		{KindStubWire, false, 2},
		{KindGate, false, 2},
		{KindDevStoryEnrich, false, 2},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, RungFor(c.kind, c.needsRoom),
			"RungFor(%s, needsRoom=%v)", c.kind, c.needsRoom)
	}
}

// TestPropose_DropsAlreadyModeled proves the dedup invariant: a recipe the
// mapper classifies ALREADY-MODELED never surfaces and emits no event.
func TestPropose_DropsAlreadyModeled(t *testing.T) {
	mapper := &stubMapper{status: StatusAlreadyModeled}
	drafter := &stubDrafter{files: map[string][]byte{"app.yaml": []byte("x")}, artifact: goodAuthorArtifact()}
	rec := &recorderSink{}
	p := &Proposer{
		PriorityThreshold: 0.5,
		Mapper:            mapper,
		Drafter:           drafter,
		AuthorSchema:      authorSchema(t),
		StageRoot:         t.TempDir(),
	}

	recipe := Recipe{ID: "r1", Priority: 0.9, Kind: KindBinding, Target: store.MiningTargetRootInstance, Theme: "ticket lookup"}
	inv := []InventoryRow{{Kind: KindBinding, Theme: "ticket lookup"}}

	prop, err := p.Propose(context.Background(), recipe, inv, sessionSink(rec))
	require.Error(t, err)
	var dropped ErrDropped
	assert.ErrorAs(t, err, &dropped)
	assert.Nil(t, prop)
	assert.Empty(t, rec.events, "ALREADY-MODELED must not surface a proposal event")
	// The dedup must have been handed the regenerated inventory, not a cache.
	assert.Equal(t, inv, mapper.gotInv)
}

// TestPropose_DropsBelowThreshold proves the threshold gate short-circuits
// before any agent pass (the mapper is never called).
func TestPropose_DropsBelowThreshold(t *testing.T) {
	mapper := &stubMapper{status: StatusGap}
	drafter := &stubDrafter{}
	p := &Proposer{PriorityThreshold: 0.8, Mapper: mapper, Drafter: drafter, StageRoot: t.TempDir()}

	_, err := p.Propose(context.Background(), Recipe{ID: "r1", Priority: 0.2, Kind: KindBinding}, nil, nil)
	var dropped ErrDropped
	require.ErrorAs(t, err, &dropped)
	assert.Equal(t, Recipe{}, mapper.gotRecipe, "mapper must not be called below threshold")
}

// TestPropose_GapBindingStagesRung1 proves an ENRICH/GAP binding recipe drafts
// at rung 1, stages under .artifacts/mining/<id>/, and emits a
// MiningProposalRaised carrying the rung + draft path.
func TestPropose_GapBindingStagesRung1(t *testing.T) {
	stage := t.TempDir()
	mapper := &stubMapper{status: StatusGap}
	drafter := &stubDrafter{
		files:    map[string][]byte{"app.yaml": []byte("app: x\n")},
		artifact: goodAuthorArtifact(),
	}
	rec := &recorderSink{}
	p := &Proposer{
		PriorityThreshold: 0.5,
		Mapper:            mapper,
		Drafter:           drafter,
		AuthorSchema:      authorSchema(t),
		StageRoot:         stage,
	}

	recipe := Recipe{ID: "r-bind", Priority: 0.95, Kind: KindBinding, Target: store.MiningTargetRootInstance, Theme: "ticket lookup"}
	prop, err := p.Propose(context.Background(), recipe, nil, sessionSink(rec))
	require.NoError(t, err)
	require.NotNil(t, prop)

	assert.Equal(t, 1, prop.Rung, "a binding delta is rung 1")
	assert.Equal(t, 1, drafter.gotRung, "the drafter is asked for the lightest rung")
	assert.Equal(t, filepath.Join(stage, "r-bind"), prop.DraftPath)

	// The staged file is on disk under the staging dir (never the live tree).
	staged, err := os.ReadFile(filepath.Join(prop.DraftPath, "app.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "app: x\n", string(staged))

	// Exactly one MiningProposalRaised with the right shape.
	require.Len(t, rec.events, 1)
	assert.Equal(t, store.MiningProposalRaised, rec.events[0].kind)
	var raised store.MiningProposalRaisedPayload
	require.NoError(t, json.Unmarshal(rec.events[0].payload, &raised))
	assert.Equal(t, "r-bind", raised.RecipeID)
	assert.Equal(t, string(KindBinding), raised.Kind)
	assert.Equal(t, 1, raised.Rung)
	assert.Equal(t, prop.DraftPath, raised.DraftPath)
}

// TestPropose_GateRecipeStagesRung2 proves a gate-kind recipe drafts at rung 2.
func TestPropose_GateRecipeStagesRung2(t *testing.T) {
	mapper := &stubMapper{status: StatusGap}
	drafter := &stubDrafter{
		files:    map[string][]byte{"rooms/deploy.yaml": []byte("room: deploy\n")},
		artifact: goodAuthorArtifact(),
	}
	p := &Proposer{
		PriorityThreshold: 0.5,
		Mapper:            mapper,
		Drafter:           drafter,
		AuthorSchema:      authorSchema(t),
		StageRoot:         t.TempDir(),
	}
	prop, err := p.Propose(context.Background(), Recipe{ID: "r-gate", Priority: 0.9, Kind: KindGate}, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, prop.Rung)
	assert.Equal(t, 2, drafter.gotRung)
}

// TestPropose_RejectsSchemaInvalidDraft proves a draft whose author_artifact
// fails the schema never surfaces (fail-fast before the operator).
func TestPropose_RejectsSchemaInvalidDraft(t *testing.T) {
	mapper := &stubMapper{status: StatusGap}
	drafter := &stubDrafter{
		files:    map[string][]byte{"app.yaml": []byte("x")},
		artifact: json.RawMessage(`{"flows_green": true}`), // missing files_changed + summary_markdown
	}
	rec := &recorderSink{}
	p := &Proposer{
		PriorityThreshold: 0.5,
		Mapper:            mapper,
		Drafter:           drafter,
		AuthorSchema:      authorSchema(t),
		StageRoot:         t.TempDir(),
	}
	_, err := p.Propose(context.Background(), Recipe{ID: "r-bad", Priority: 0.9, Kind: KindBinding}, nil, sessionSink(rec))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "schema")
	assert.Empty(t, rec.events, "a schema-invalid draft must not surface")
}

// TestPropose_RejectsBrokenDryLoad proves a structurally-broken staged tree
// fails the dry app.Load and never surfaces.
func TestPropose_RejectsBrokenDryLoad(t *testing.T) {
	mapper := &stubMapper{status: StatusGap}
	// A draft that overwrites the entry manifest with invalid YAML.
	drafter := &stubDrafter{
		files:    map[string][]byte{"app.yaml": []byte(":\n  not: valid: app")},
		artifact: goodAuthorArtifact(),
	}
	rec := &recorderSink{}
	p := &Proposer{
		PriorityThreshold: 0.5,
		Mapper:            mapper,
		Drafter:           drafter,
		AuthorSchema:      authorSchema(t),
		StageRoot:         t.TempDir(),
		LiveEntry:         "app.yaml",
		LiveFiles:         map[string][]byte{"app.yaml": []byte("app: x\n")},
	}
	_, err := p.Propose(context.Background(), Recipe{ID: "r-broken", Priority: 0.9, Kind: KindBinding}, nil, sessionSink(rec))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dry app.Load")
	assert.Empty(t, rec.events)
}
