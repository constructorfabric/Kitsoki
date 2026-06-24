package orchestrator_test

// story_record_test.go — the orchestrator records the effective story into the
// trace at session start, records a diff on /reload (or /meta), and the trace
// alone is enough to reconstruct the machine and keep replaying after the
// story files on disk change or vanish.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

const cloakAppPath = "../../testdata/apps/cloak/app.yaml"

func newRecordingOrch(t *testing.T, appPath, tracePath string) (*orchestrator.Orchestrator, *store.JSONLSink) {
	t.Helper()
	def, err := app.Load(appPath)
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	return orch, sink
}

func TestRecordEffectiveStory_baseSnapshotAtSessionStart(t *testing.T) {
	t.Parallel()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	orch, sink := newRecordingOrch(t, cloakAppPath, tracePath)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RecordEffectiveStory(ctx, sid))

	hist := sink.History()
	require.NotEmpty(t, hist)
	first := hist[0]
	require.Equal(t, store.StorySnapshot, first.Kind, "story snapshot is the first event")
	require.Equal(t, int64(0), int64(first.Turn))

	var p struct {
		AppID string            `json:"app_id"`
		Entry string            `json:"entry"`
		Hash  string            `json:"hash"`
		Files map[string]string `json:"files"`
	}
	require.NoError(t, json.Unmarshal(first.Payload, &p))
	require.Equal(t, "app.yaml", p.Entry)
	require.NotEmpty(t, p.Hash)
	require.Contains(t, p.Files, "app.yaml")
	require.Contains(t, p.Files, "views/base.pongo")

	// Idempotent: a second call with an unchanged story records nothing new.
	n := len(sink.History())
	require.NoError(t, orch.RecordEffectiveStory(ctx, sid))
	require.Len(t, sink.History(), n, "unchanged story is a no-op")
}

func TestRecordEffectiveStory_reloadDiffAndSelfContainedReplay(t *testing.T) {
	t.Parallel()
	storyDir := filepath.Join(t.TempDir(), "cloak")
	require.NoError(t, copyTree(filepath.Dir(cloakAppPath), storyDir))
	appPath := filepath.Join(storyDir, "app.yaml")
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")

	orch, sink := newRecordingOrch(t, appPath, tracePath)
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	require.NoError(t, orch.RecordEffectiveStory(ctx, sid)) // base snapshot

	// One turn: foyer → cloakroom.
	out, err := orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "west"})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	w := orch.CurrentWorld(sid)

	// Mutate the story on disk: edit a view, delete a doc file.
	viewPath := filepath.Join(storyDir, "views", "base.pongo")
	orig, err := os.ReadFile(viewPath)
	require.NoError(t, err)
	editedView := append(orig, []byte("\n{# hot-reloaded edit #}\n")...)
	require.NoError(t, os.WriteFile(viewPath, editedView, 0o644))
	require.NoError(t, os.Remove(filepath.Join(storyDir, "APP.md")))

	// Reload + record — the /reload and /meta funnel.
	_, err = orch.Reload(appPath, out.NewState)
	require.NoError(t, err)
	require.NoError(t, orch.RecordEffectiveStory(ctx, sid))

	// A story.changed event captured the edit and the deletion. Without the
	// post-reload RecordEffectiveStory call there is no such event — this
	// assertion is what fails if the wiring is dropped.
	var diff *store.Event
	for i := range sink.History() {
		if sink.History()[i].Kind == store.StoryChanged {
			ev := sink.History()[i]
			diff = &ev
		}
	}
	require.NotNil(t, diff, "reload must record a story.changed diff")

	var dp struct {
		Changed map[string]string `json:"changed"`
		Removed []string          `json:"removed"`
	}
	require.NoError(t, json.Unmarshal(diff.Payload, &dp))
	require.Contains(t, dp.Changed, "views/base.pongo")
	gotBytes, err := base64.StdEncoding.DecodeString(dp.Changed["views/base.pongo"])
	require.NoError(t, err)
	require.Equal(t, editedView, gotBytes, "diff carries the new file bytes")
	require.Contains(t, dp.Removed, "APP.md", "deletion is recorded")

	// Self-contained replay: blow away the story on disk, then reconstruct the
	// machine FROM THE TRACE and render the current room. This must succeed
	// with no story files present.
	history := sink.History()
	latest := app.TurnNumber(0)
	for _, ev := range history {
		if ev.Turn > latest {
			latest = ev.Turn
		}
	}
	files, entry, err := store.StoryAtTurn(history, latest)
	require.NoError(t, err)
	require.NotContains(t, files, "APP.md", "reconstruction reflects the deletion")

	require.NoError(t, os.RemoveAll(storyDir)) // the disk story is gone

	def2, cleanup, err := app.LoadFromFiles(files, entry)
	require.NoError(t, err)
	defer cleanup()
	m2, err := machine.New(def2)
	require.NoError(t, err)
	view, err := m2.RenderState(out.NewState, w)
	require.NoError(t, err)
	require.NotEmpty(t, view, "replay renders the room from the trace-embedded story alone")
}
