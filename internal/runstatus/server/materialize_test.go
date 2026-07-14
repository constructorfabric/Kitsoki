package server_test

// materialize_test.go — httptest flow coverage for the graph.materialize.*
// RPC family and /rpc/materialize-stream SSE endpoint (node-artifact-
// materialization plan slice 4). Drives the actual slice-2 pilot story in
// the sibling POG checkout (stories/materialize-work-item) end to end
// through start + stream, following server_test.go's rpcCall /
// rpcCallExpectError conventions and turn_stream_test.go's SSE-frame-reading
// pattern.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
)

// materializeFixtureRoot builds a disposable fixture repo root (F3,
// federated-materialize follow-up to F1's catalog-allowlist) at a fresh
// t.TempDir() containing:
//
//   - pog/catalog.yaml: a fresh copy of testdata/materialize-catalog.yaml.
//     graph.materialize.start/.checks now resolve `catalog` as an ALIAS
//     through s.graphAllowlist() (materialize.go's
//     resolveMaterializeCatalog), the same mechanism graph.propose and
//     friends use — there is no raw-path back door left to hand a test
//     fixture in directly, so the fixture catalog must live at
//     <root>/pog/catalog.yaml to be addressable as alias "pog" at all. This
//     also gets the write-back isolation the old materializeCatalogCopy
//     helper existed for: graph.materialize.start's write-back
//     (internal/materialize/writeback.go, slice 6) edits the catalog file in
//     place, so every test needs its own disposable copy, not the checked-in
//     fixture, or repeated runs would leave it mutated with the previous
//     run's job id/timestamp.
//   - stories: a symlink to the real POG checkout's stories/ dir (located
//     via POG_REPO_ROOT, see pogRepoRoot), so the fixture catalog's
//     materialize.story: stories/materialize-work-item path — resolved
//     RepoRoot-relative, per internal/materialize.Request's RepoRoot doc
//     comment — still finds the real pilot story even though RepoRoot itself
//     is now this disposable fixture root (derived from the resolved
//     "pog" alias's catalog path, two filepath.Dir() calls up, exactly as
//     materialize.go's resolveMaterializeCatalog does in production).
//
// Because RepoRoot is now derived from the fixture root rather than pinned
// to the real POG checkout, a job's artifact write-back also lands under
// <root>/.artifacts/ — fully disposable with the temp dir, so callers no
// longer need to separately clean up the real POG checkout's .artifacts/.
func materializeFixtureRoot(t *testing.T) string {
	t.Helper()
	pogRoot := pogRepoRoot(t)
	root := t.TempDir()

	homeDir := filepath.Join(root, "pog")
	require.NoError(t, os.MkdirAll(homeDir, 0o755))
	raw, err := os.ReadFile("testdata/materialize-catalog.yaml")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(homeDir, "catalog.yaml"), raw, 0o644))

	require.NoError(t, os.Symlink(filepath.Join(pogRoot, "stories"), filepath.Join(root, "stories")))

	return root
}

// pogRepoRoot locates the POG checkout that carries the node-artifact-
// materialization plan's pilot story (stories/materialize-work-item) and its
// bound work-item type_registry entry, via the POG_REPO_ROOT env var. This
// flow test is only meaningful against the real story; an unset var or
// missing checkout (e.g. a bare kitsoki CI runner with no POG sibling) skips
// rather than fails — mirroring internal/materialize/spike_test.go's
// convention.
func pogRepoRoot(t *testing.T) string {
	t.Helper()
	root := os.Getenv("POG_REPO_ROOT")
	if root == "" {
		t.Skip("requires POG_REPO_ROOT pointing at a POG checkout with stories/materialize-work-item")
	}
	if _, err := os.Stat(root + "/stories/materialize-work-item/app.yaml"); err != nil {
		t.Skipf("requires the POG checkout's pilot story at %s (not found: %v)", root, err)
	}
	return root
}

func newMaterializeServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newMaterializeServerAt(t, materializeFixtureRoot(t))
}

// newMaterializeServerAt builds the materialize RPC server against a caller-
// supplied fixture root (see materializeFixtureRoot), for tests that need to
// keep a handle on root for filesystem assertions (write-back, artifacts).
func newMaterializeServerAt(t *testing.T, root string) *httptest.Server {
	t.Helper()
	// The stub provider implements SeededSessionProvider, so materializeStart
	// will try the web-session drive path first; failing the seed here pins
	// these tests to the private-rig fallback (the CLI / no-registry path).
	// TestMaterialize_Start_DrivesWebSession covers the seeded path.
	p := newStubProvider()
	p.seededFn = func(context.Context, string, map[string]any) (string, error) {
		return "", errors.New("stub: no live sessions")
	}
	ts := httptest.NewServer(server.NewMulti(p, server.WithMaterializeRoot(root)).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// materializeStreamFrame mirrors the unexported server-side type for test
// decoding (same convention as turnStreamFrame in turn_stream_test.go).
type materializeStreamFrame struct {
	Type    string `json:"type"`
	GateID  string `json:"gate_id"`
	Passed  bool   `json:"passed"`
	StageID string `json:"stage_id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Path    string `json:"path"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func readMaterializeStreamFrames(t *testing.T, ts *httptest.Server, jobID string) []materializeStreamFrame {
	t.Helper()
	resp, err := http.Get(ts.URL + "/rpc/materialize-stream?job=" + jobID)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 on materialize-stream")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	var frames []materializeStreamFrame
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		var f materializeStreamFrame
		require.NoError(t, json.Unmarshal([]byte(raw), &f), "unmarshal frame: %s", raw)
		frames = append(frames, f)
		if f.Type == "done" {
			break
		}
	}
	return frames
}

// TestMaterialize_StartAndStream_PilotStory drives graph.materialize.start
// against a fixture catalog bound to the real pilot story, then reads the
// full /rpc/materialize-stream frame sequence for the job it starts,
// asserting the gate/stage/artifact/status/done frames the plan specifies —
// and finally cross-checks graph.materialize.status (the poll fallback)
// against what the stream showed.
func TestMaterialize_StartAndStream_PilotStory(t *testing.T) {
	root := materializeFixtureRoot(t)
	ts := newMaterializeServerAt(t, root)
	catalogPath := filepath.Join(root, "pog", "catalog.yaml")

	var start struct {
		JobID  string `json:"job_id"`
		Stages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"stages"`
	}
	rpcCall(t, ts, "graph.materialize.start", map[string]any{
		"catalog": "pog",
		"node_id": "wi-ready",
		"params":  map[string]any{"depth": 3, "audience": "public"},
	}, &start)

	require.NotEmpty(t, start.JobID)
	require.Len(t, start.Stages, 4)
	wantStageIDs := []string{"gather", "draft", "verify", "done"}
	for i, want := range wantStageIDs {
		assert.Equal(t, want, start.Stages[i].ID)
		assert.NotEmpty(t, start.Stages[i].Title)
	}
	// The pilot story's rooms carry description: text distinct from the bare
	// room id, proving the title lookup found app.State.Description.
	assert.Equal(t, "Gathering context", start.Stages[0].Title)

	frames := readMaterializeStreamFrames(t, ts, start.JobID)
	require.NotEmpty(t, frames)

	var gateFrames, stageFrames []materializeStreamFrame
	var artifactFrame *materializeStreamFrame
	var sawComplete, sawDone bool
	for i := range frames {
		f := frames[i]
		switch f.Type {
		case "gate":
			gateFrames = append(gateFrames, f)
		case "stage":
			stageFrames = append(stageFrames, f)
		case "artifact":
			af := f
			artifactFrame = &af
		case "status":
			if f.Status == "complete" {
				sawComplete = true
			}
		case "done":
			sawDone = true
		case "error":
			t.Fatalf("unexpected error frame: %s", f.Message)
		}
	}

	require.Len(t, gateFrames, 2, "expected one gate frame per materialize.gates entry")
	gateIDs := []string{gateFrames[0].GateID, gateFrames[1].GateID}
	assert.ElementsMatch(t, []string{"gate", "owner"}, gateIDs)
	for _, g := range gateFrames {
		assert.True(t, g.Passed)
	}

	// Stage frames are guaranteed regardless of when the EventSource
	// connects: the handler replays the server's accumulated snapshot on
	// connect (see materialize_stream.go's late-subscriber paragraph), so
	// even a stream opened after the deterministic pilot finished must show
	// every stage reaching "complete" — the stuck-pill regression this
	// replay exists to prevent. Emits are monotonic per stage, so the last
	// frame per stage is its high-water status.
	wantOrder := map[string]int{"gather": 0, "draft": 1, "verify": 2, "done": 3}
	lastStatus := map[string]string{}
	for _, sf := range stageFrames {
		_, ok := wantOrder[sf.StageID]
		require.True(t, ok, "unexpected stage id %q", sf.StageID)
		lastStatus[sf.StageID] = sf.Status
	}
	require.Len(t, lastStatus, len(wantOrder), "every stage should have at least one frame (snapshot replay)")
	for id, status := range lastStatus {
		assert.Equal(t, "complete", status, "stage %s should end complete", id)
	}

	// The terminal artifact/status/done sequence always fires, regardless of
	// which race the stream landed in above.
	require.NotNil(t, artifactFrame, "expected an artifact frame")
	assert.Equal(t, "document", artifactFrame.Kind)
	assert.Equal(t, ".artifacts/wi-ready/brief.md", artifactFrame.Path)

	assert.True(t, sawComplete, "expected a status=complete frame")
	assert.True(t, sawDone, "expected a terminal done frame")

	// The poll fallback should agree with what the stream showed. The
	// stream already drove the job to completion; the server's own
	// bookkeeping goroutine (trackMaterializeJob) updates independently, so
	// poll briefly rather than assuming it has already caught up.
	var status struct {
		Status string `json:"status"`
		Stages []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"stages"`
		Artifacts []struct {
			Path string `json:"path"`
		} `json:"artifacts"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rpcCall(t, ts, "graph.materialize.status", map[string]any{"job_id": start.JobID}, &status)
		if status.Status == "done" || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, "done", status.Status)

	require.Len(t, status.Stages, 4)
	for _, st := range status.Stages {
		assert.Equal(t, "complete", st.Status, "stage %s", st.ID)
	}
	require.Len(t, status.Artifacts, 1)
	assert.Equal(t, ".artifacts/wi-ready/brief.md", status.Artifacts[0].Path)

	// Write-back (slice 6): the job handler wrote the artifact's content to
	// disk under the POG repo root and persisted evidence: + materialization:
	// onto the node in the (copied) catalog — the durable truth the portal's
	// reload-on-done then reads. The handler writes this synchronously before
	// returning its Result, but the job is marked done by the scheduler right
	// after, so poll briefly rather than assume the write already landed.
	var artifactContent []byte
	var artifactErr error
	deadline = time.Now().Add(2 * time.Second)
	for {
		artifactContent, artifactErr = os.ReadFile(filepath.Join(root, ".artifacts", "wi-ready", "brief.md"))
		if artifactErr == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, artifactErr, "materialize job should have written the artifact to disk")
	assert.NotEmpty(t, artifactContent)

	catalogRaw, err := os.ReadFile(catalogPath)
	require.NoError(t, err)
	catalogText := string(catalogRaw)
	assert.Contains(t, catalogText, "path: .artifacts/wi-ready/brief.md", "evidence entry not written back")
	assert.Contains(t, catalogText, "materialization:", "materialization: block not written back")
	// The changeset-based write-back (yaml.Node tree edit) emits plain
	// scalars, not the old splice's quoted strings — assert unquoted.
	assert.Contains(t, catalogText, "job_id: "+start.JobID)
	assert.Contains(t, catalogText, "status: complete")
}

// TestMaterialize_Start_DrivesWebSession covers the seeded web-session path:
// when the provider can seed a live session for the bound story,
// graph.materialize.start drives THAT session (not a private rig) and
// returns its session_id, so the SPA's /#/s/<id> trace and /#/s/<id>/chat
// transcript routes can observe the drive. The stub provider here builds a
// real orchestrator rig per NewSessionSeeded — the in-process equivalent of
// the `kitsoki web` registry's NewSessionSeeded.
func TestMaterialize_Start_DrivesWebSession(t *testing.T) {
	root := materializeFixtureRoot(t)

	p := newStubProvider()
	var seededPaths []string
	p.seededFn = func(ctx context.Context, storyPath string, initialWorld map[string]any) (string, error) {
		seededPaths = append(seededPaths, storyPath)

		def, err := app.Load(storyPath)
		if err != nil {
			return "", err
		}
		m, err := machine.New(def)
		if err != nil {
			return "", err
		}
		st, err := store.OpenMemory()
		if err != nil {
			return "", err
		}
		t.Cleanup(func() { _ = st.Close() })

		orch := orchestrator.New(def, m, st, nil)
		sid, err := orch.NewSession(ctx)
		if err != nil {
			return "", err
		}

		sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "run.jsonl"))
		if err != nil {
			return "", err
		}
		t.Cleanup(func() { _ = sink.Close() })
		live := server.NewLiveSession(sink, def, string(sid), string(orch.InitialState()))
		orch.SetEventSink(live)

		if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
			return "", err
		}
		if err := orch.PatchWorld(ctx, sid, initialWorld); err != nil {
			return "", err
		}

		id := "web-session-" + string(sid)
		p.putEntry(id, server.Entry{
			Source: live,
			Driver: server.OrchestratorDriver{Orch: orch, SID: sid},
		})
		return id, nil
	}

	ts := httptest.NewServer(server.NewMulti(p, server.WithMaterializeRoot(root)).Handler())
	t.Cleanup(ts.Close)

	var start struct {
		JobID     string `json:"job_id"`
		SessionID string `json:"session_id"`
	}
	rpcCall(t, ts, "graph.materialize.start", map[string]any{
		"catalog": "pog",
		"node_id": "wi-ready",
		"params":  map[string]any{"depth": 2, "audience": "internal"},
	}, &start)

	require.NotEmpty(t, start.JobID)
	require.NotEmpty(t, start.SessionID, "seeded path should return the web session id")
	require.Len(t, seededPaths, 1)
	assert.True(t, filepath.IsAbs(seededPaths[0]), "story path handed to the provider must be absolute, got %q", seededPaths[0])

	// The stream's terminal done frame doubles as the job-finished wait.
	frames := readMaterializeStreamFrames(t, ts, start.JobID)
	stageComplete := map[string]bool{}
	var sawDone bool
	for _, f := range frames {
		if f.Type == "stage" && f.Status == "complete" {
			stageComplete[f.StageID] = true
		}
		if f.Type == "done" {
			sawDone = true
		}
		if f.Type == "error" {
			t.Fatalf("unexpected error frame: %s", f.Message)
		}
	}
	require.True(t, sawDone)
	for _, id := range []string{"gather", "draft", "verify", "done"} {
		assert.True(t, stageComplete[id], "stage %s should complete", id)
	}

	// .status carries the same session_id for a client that reloads mid-run.
	var status struct {
		Status    string `json:"status"`
		SessionID string `json:"session_id"`
	}
	rpcCall(t, ts, "graph.materialize.status", map[string]any{"job_id": start.JobID}, &status)
	assert.Equal(t, start.SessionID, status.SessionID)

	// The drive went through the REGISTERED session: its trace (what the
	// SPA's RunView/chat render) shows the turns, proving the viewer link
	// has something to open.
	var trace struct {
		Events []map[string]any `json:"events"`
	}
	rpcCall(t, ts, "runstatus.session.trace", map[string]any{"session_id": start.SessionID}, &trace)
	assert.NotEmpty(t, trace.Events, "the web session's trace should record the drive")

	// And the write-back still landed (same handler as the private path).
	deadline := time.Now().Add(2 * time.Second)
	var artifactErr error
	for {
		_, artifactErr = os.Stat(filepath.Join(root, ".artifacts", "wi-ready", "brief.md"))
		if artifactErr == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NoError(t, artifactErr, "materialize job should have written the artifact to disk")
}

// TestMaterialize_Start_RejectsUnmetGates confirms the "rejects with the
// unmet field list" contract: a node missing gate/owner never gets a job.
func TestMaterialize_Start_RejectsUnmetGates(t *testing.T) {
	ts := newMaterializeServer(t)

	code, msg := rpcCallExpectError(t, ts, "graph.materialize.start", map[string]any{
		"catalog": "pog",
		"node_id": "wi-not-ready",
	})
	assert.NotEqual(t, 0, code)
	assert.Contains(t, msg, "gate")
	assert.Contains(t, msg, "owner")
}

func TestMaterialize_Status_UnknownJob(t *testing.T) {
	ts := newMaterializeServer(t)

	code, msg := rpcCallExpectError(t, ts, "graph.materialize.status", map[string]any{"job_id": "nope"})
	assert.NotEqual(t, 0, code)
	assert.Contains(t, msg, "unknown job_id")
}

func TestMaterialize_Stream_UnknownJob404s(t *testing.T) {
	ts := newMaterializeServer(t)

	resp, err := http.Get(ts.URL + "/rpc/materialize-stream?job=nope")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestMaterialize_Cancel_UnknownJob confirms the cancel RPC reports an error
// (not a silent success) for an id the scheduler never issued.
func TestMaterialize_Cancel_UnknownJob(t *testing.T) {
	ts := newMaterializeServer(t)

	code, _ := rpcCallExpectError(t, ts, "graph.materialize.cancel", map[string]any{"job_id": "nope"})
	assert.NotEqual(t, 0, code)
}
