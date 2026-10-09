package tui_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/jobs"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

func buildWorkTestModel(t *testing.T) (tea.Model, app.SessionID, *jobs.JobStore, *chats.Store, *sql.DB) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "work.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	cs, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithJobStore(js),
		tuipkg.WithChatStore(cs),
	)
	return m, sid, js, cs, s.DB()
}

func TestWorkSlashListsActiveAsyncWork(t *testing.T) {
	m, sid, js, cs, db := buildWorkTestModel(t)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, js.UpsertJob(ctx, &jobs.Job{
		ID:          "job-running",
		SessionID:   sid,
		Kind:        "host.agent.task",
		Status:      jobs.JobRunning,
		OriginState: "foyer",
		CreatedAt:   now.Add(-2 * time.Minute),
		UpdatedAt:   now.Add(-time.Minute),
	}))
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     now.Add(-30 * time.Second),
		Severity:      jobs.SeverityActionRequired,
		Title:         "Review PR #42",
		TeleportState: "foyer",
		OriginKind:    "external",
		OriginRef:     "github:acme/repo/pr/42",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}))
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     "other-session",
		CreatedAt:     now.Add(-20 * time.Second),
		Severity:      jobs.SeverityActionRequired,
		Title:         "Other PR #99",
		TeleportState: "foyer",
		OriginKind:    "external",
		OriginRef:     "github:acme/repo/pr/99",
		OriginURL:     "https://github.com/acme/repo/pull/99",
	}))
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     now.Add(-5 * time.Second),
		Severity:      jobs.SeveritySuccess,
		Title:         "Background check complete",
		TeleportState: "foyer",
		TeleportJobID: "job-running",
		OriginKind:    "job",
		OriginRef:     "job:job-running",
	}))
	require.NoError(t, js.UpsertJob(ctx, &jobs.Job{
		ID:          "job-other",
		SessionID:   "other-session",
		Kind:        "host.agent.task",
		Status:      jobs.JobRunning,
		OriginState: "foyer",
		CreatedAt:   now.Add(-90 * time.Second),
		UpdatedAt:   now.Add(-10 * time.Second),
	}))
	require.NoError(t, js.RequestClarification(ctx, "job-other", jobs.ClarificationSchema{
		Prompt: "Which environment should I use?",
		Fields: map[string]string{"answer": "string"},
	}))

	chat, err := cs.Create(ctx, "cloak", "agent", "scope", "Review proposal")
	require.NoError(t, err)
	_, err = cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          chat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "continue queued review",
		OriginSessionID: string(sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, chat.ID, "user", "please review the proposal", nil)
	require.NoError(t, err)
	_, err = cs.AppendMessage(ctx, chat.ID, "assistant", "queued review context is ready", nil)
	require.NoError(t, err)

	dispatchingChat, err := cs.Create(ctx, "cloak", "agent", "scope-dispatching", "Dispatching review")
	require.NoError(t, err)
	dispatchingDrive, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          dispatchingChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "dispatching review",
		OriginSessionID: string(sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = cs.ClaimDrive(ctx, dispatchingDrive.DriveID)
	require.NoError(t, err)

	failedChat, err := cs.Create(ctx, "cloak", "agent", "scope-failed", "Failed review")
	require.NoError(t, err)
	failedDrive, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          failedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "failed review",
		OriginSessionID: string(sid),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = cs.ClaimDrive(ctx, failedDrive.DriveID)
	require.NoError(t, err)
	require.NoError(t, cs.MarkDriveFailed(ctx, failedDrive.DriveID, "claude exited 1"))

	otherQueued, err := cs.Create(ctx, "cloak", "agent", "other-queue", "Other queued review")
	require.NoError(t, err)
	_, err = cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          otherQueued.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "continue other queued review",
		OriginSessionID: "other-session",
		OriginState:     "foyer",
	})
	require.NoError(t, err)

	bg, err := cs.Create(ctx, "cloak", "agent", "scope-bg", "Background Claude")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE chats SET session_id = ? WHERE id = ?`, string(sid), bg.ID)
	require.NoError(t, err)
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{ChatID: bg.ID, TmuxSession: "kit-bg"})
	require.NoError(t, err)
	_, err = cs.DetachPTY(ctx, bg.ID)
	require.NoError(t, err)

	other, err := cs.Create(ctx, "cloak", "agent", "other-scope", "Other session Claude")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE chats SET session_id = ? WHERE id = ?`, "other-session", other.ID)
	require.NoError(t, err)
	_, err = cs.AttachPTY(ctx, chats.AttachPTYOptions{ChatID: other.ID, TmuxSession: "kit-other"})
	require.NoError(t, err)
	_, err = cs.DetachPTY(ctx, other.ID)
	require.NoError(t, err)

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	currentWork := transcriptAfter(t, tx, "active work: 7 item(s)")
	require.Contains(t, tx, "active work: 7 item(s)")
	require.Contains(t, tx, "notification")
	require.Contains(t, tx, "Review PR #42")
	require.Contains(t, tx, "Background check complete")
	require.Contains(t, tx, "github:acme/repo/pr/42")
	require.Contains(t, tx, "https://github.com/acme/repo/pull/42")
	require.Contains(t, tx, "/inbox 1")
	require.NotContains(t, tx, "Other PR #99")
	require.NotContains(t, tx, "job-other")
	require.Contains(t, tx, "job")
	require.Contains(t, tx, "host.agent.task")
	requireContainsNear(t, currentWork, "host.agent.task", "/inbox")
	require.Contains(t, tx, "dispatching")
	require.Contains(t, tx, "dispatching review")
	require.Contains(t, tx, "failed review")
	require.Contains(t, tx, "claude exited 1")
	requireContainsNear(t, currentWork, "failed review", "/chat show "+failedChat.ID)
	require.Contains(t, tx, "queued")
	require.Contains(t, tx, "continue queued review")
	requireContainsNear(t, currentWork, "continue queued review", "/chat show "+chat.ID)
	require.NotContains(t, tx, "continue other queued review")
	require.Contains(t, tx, "chat")
	require.Contains(t, tx, "Background Claude")
	require.Contains(t, tx, "/sessions attach 1")
	require.NotContains(t, tx, "Other session Claude")
	requireBefore(t, currentWork, "Review PR #42", "host.agent.task")
	requireBefore(t, currentWork, "failed review", "host.agent.task")
	requireBefore(t, currentWork, "host.agent.task", "dispatching review")
	requireBefore(t, currentWork, "dispatching review", "continue queued review")
	requireBefore(t, currentWork, "continue queued review", "Background Claude")
	requireBefore(t, currentWork, "Background Claude", "Background check complete")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	cached := tuipkg.CachedSessionListForTest(rm)
	require.Len(t, cached, 1)
	require.Equal(t, bg.ID, cached[0].ChatID)

	m = runTurnBlocking(t, m, "/work --all")
	tx = extractTranscript(t, m)
	allWork := transcriptAfter(t, tx, "active work (all sessions): 11 item(s)")
	require.Contains(t, tx, "active work (all sessions): 11 item(s)")
	require.Contains(t, tx, "Other PR #99")
	require.Contains(t, tx, "job-other")
	requireContainsNear(t, allWork, "job-other", "Which environment should I use?")
	require.Contains(t, tx, "Background Claude")
	require.Contains(t, tx, "current session")
	require.Contains(t, tx, "dispatching review")
	require.Contains(t, tx, "failed review")
	require.Contains(t, tx, "continue other queued review")
	require.Contains(t, tx, "Other session Claude")
	require.Contains(t, tx, "session other-session")
	require.Contains(t, tx, "/sessions attach 2")
	requireBefore(t, allWork, "Other PR #99", "job-other")
	requireBefore(t, allWork, "Review PR #42", "job-running")
	requireBefore(t, allWork, "job-other", "job-running")
	requireBefore(t, allWork, "Other session Claude", "Background check complete")
	requireContainsNear(t, allWork, "Review PR #42", "/inbox 2")
	requireNotContainsNear(t, allWork, "Other PR #99", "/inbox")

	rm, ok = tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	cached = tuipkg.CachedSessionListForTest(rm)
	require.Len(t, cached, 2)
	require.ElementsMatch(t, []string{bg.ID, other.ID}, []string{cached[0].ChatID, cached[1].ChatID})

	m = runTurnBlocking(t, m, "/chat show "+chat.ID)
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "chat context")
	require.Contains(t, tx, "Review proposal")
	require.Contains(t, tx, "scope")
	require.Contains(t, tx, "please review the proposal")
	require.Contains(t, tx, "queued review context is ready")
}

func TestWorkSlashListsCurrentOperation(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "operation-work-test"},
		Root: "idle",
		Operations: map[string]*app.OperationPolicy{
			"bugfix_full": {
				Title:           "Fix bug",
				Mode:            "autonomous",
				ExecutionMode:   "one-shot",
				RunInBackground: true,
				StopOn:          []string{"needs-human"},
			},
		},
		Intents: map[string]app.Intent{
			"go": {Description: "Run the operation."},
		},
		States: map[string]*app.State{
			"idle": {
				On: map[string][]app.Transition{
					"go": {{
						Target:    "needs-human",
						Operation: "bugfix_full",
						Effects: []app.Effect{{
							Set: map[string]any{
								"status":             "needs-human",
								"needs_human_reason": "Regression gate was never RED.",
							},
						}},
					}},
				},
			},
			"needs-human": {Terminal: true},
		},
	}
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	orch := orchestrator.New(def, mach, s, nil)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	out, err := orch.SubmitDirect(context.Background(), sid, "go", nil)
	require.NoError(t, err)

	initialView, err := orch.RenderState(out.NewState, orch.CurrentWorld(sid))
	require.NoError(t, err)
	model := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithResumedJourney(out.NewState, orch.CurrentWorld(sid), out.TurnNumber),
	))

	model = runTurnBlocking(t, model, "/work")
	tx := extractTranscript(t, model)
	work := transcriptAfter(t, tx, "active work: 1 item(s)")
	analyzer := tuipkg.NewRenderingAnalyzer(t, work)
	analyzer.AssertStructure("operation", "waiting", "Fix bug")
	analyzer.AssertContains("reason needs-human")
	analyzer.AssertContains("Regression gate was never RED.")
	analyzer.AssertContains("/work summary")
	requireContainsNear(t, work, "Fix bug", "reason needs-human")
	requireContainsNear(t, work, "Fix bug", "/work summary")
}

func TestWorkDriveSlashDrivesCurrentOperation(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	artifactPath := filepath.Join(cwd, "artifacts", "done.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(artifactPath), 0o755))
	require.NoError(t, os.WriteFile(artifactPath, []byte("# done\n"), 0o644))

	const yamlSrc = `
app:
  id: operation-work-drive-test
  version: 0.1.0
world:
  refined: { type: bool, default: false }
intents:
  start: {}
  accept: {}
  refine: {}
  quit: {}
root: idle
operations:
  demo_run:
    title: Demo run
    mode: supervised
    execution_mode: one-shot
    run_in_background: true
    terminal_artifact: done_artifact
states:
  idle:
    view: Idle
    on:
      start:
        - target: reproducing
          operation: demo_run
  reproducing:
    view: Reproducing
    on:
      accept:
        - target: proposing
      refine:
        - target: reproducing
      quit:
        - target: needs-human
  proposing:
    view: Proposing
    on:
      accept:
        - target: done
          effects:
            - set:
                done_artifact: { summary_title: Done }
      quit:
        - target: needs-human
  needs-human:
    terminal: true
    view: Needs human
  done:
    terminal: true
    view: Done
`
	def, err := app.LoadBytes([]byte(yamlSrc))
	require.NoError(t, err)
	mach, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, mach, s, nil)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	journalStore := journal.NewMemStore()
	journalWriter := journal.NewMemWriter(journalStore)
	journalReader := journal.NewMemReader(journalStore)
	artifactBody, err := json.Marshal(journal.ArtifactEvent{
		ID:        "done_artifact",
		Path:      artifactPath,
		Producer:  "host.artifacts_dir",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, journalWriter.Append(journal.Entry{
		Session: sid,
		Turn:    0,
		Seq:     0,
		Kind:    journal.KindArtifactEmitted,
		Body:    artifactBody,
	}))
	started, err := orch.SubmitDirect(context.Background(), sid, "start", nil)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("reproducing"), started.NewState)

	initialView, err := orch.RenderState(started.NewState, orch.CurrentWorld(sid))
	require.NoError(t, err)
	rm := tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithResumedJourney(started.NewState, orch.CurrentWorld(sid), started.TurnNumber),
		tuipkg.WithJournalReader(journalReader),
	)
	var opened []string
	tuipkg.SetOpenArtifactForTest(&rm, func(path string) error {
		opened = append(opened, path)
		return nil
	})
	model := tea.Model(rm)

	model = runTurnBlocking(t, model, "/work")
	tx := extractTranscript(t, model)
	work := transcriptAfter(t, tx, "active work: 1 item(s)")
	analyzer := tuipkg.NewRenderingAnalyzer(t, work)
	analyzer.AssertStructure("operation", "running", "Demo run")
	analyzer.AssertContains("/work drive")
	analyzer.AssertContains("/work summary")
	requireContainsNear(t, work, "Demo run", "/work drive")
	requireContainsNear(t, work, "Demo run", "/work summary")

	model = runTurnBlocking(t, model, "/work drive")
	tx = extractTranscript(t, model)
	require.Contains(t, tx, "Done")
	require.Contains(t, tx, "(work drive: drove 2 turns via accept; stopped because the operation is completed)")

	journey, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("done"), journey.State)
	handle, ok := journey.World.Vars[app.OperationRunWorldKey].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", handle["status"])
	require.Equal(t, "done", handle["terminal_state"])
	require.Equal(t, false, journey.World.Vars["refined"], "driver must not choose refine")

	model = runTurnBlocking(t, model, "/work")
	tx = extractTranscript(t, model)
	work = transcriptAfter(t, tx, "active work: 1 item(s)")
	analyzer = tuipkg.NewRenderingAnalyzer(t, work)
	analyzer.AssertStructure("operation", "completed", "Demo run")
	analyzer.AssertContains("artifact done_artifact")
	analyzer.AssertContains("/work artifact")
	analyzer.AssertContains("/work summary")
	requireContainsNear(t, work, "Demo run", "artifact done_artifact")
	requireContainsNear(t, work, "Demo run", "/work artifact")

	model = runTurnBlocking(t, model, "/work summary")
	tx = extractTranscript(t, model)
	summary := transcriptAfter(t, tx, "operation summary")
	require.Contains(t, summary, "title: Demo run")
	require.Contains(t, summary, "status: completed")
	require.Contains(t, summary, "mode: supervised")
	require.Contains(t, summary, "execution: one-shot")
	require.Contains(t, summary, "terminal: done")
	require.Contains(t, summary, "artifact: done_artifact")
	require.Contains(t, summary, "actions: /work artifact")

	model = runTurnBlocking(t, model, "/work artifact")
	tx = extractTranscript(t, model)
	require.Equal(t, []string{artifactPath}, opened)
	require.Contains(t, tx, "open: opened "+artifactPath)
}

func transcriptAfter(t *testing.T, text, marker string) string {
	t.Helper()
	idx := strings.LastIndex(text, marker)
	require.NotEqual(t, -1, idx, "expected %q in transcript", marker)
	return text[idx:]
}

func requireBefore(t *testing.T, text, before, after string) {
	t.Helper()
	beforeIndex := strings.Index(text, before)
	afterIndex := strings.Index(text, after)
	require.NotEqual(t, -1, beforeIndex, "expected %q in transcript", before)
	require.NotEqual(t, -1, afterIndex, "expected %q in transcript", after)
	require.Less(t, beforeIndex, afterIndex, "expected %q before %q", before, after)
}

func requireContainsNear(t *testing.T, text, anchor, want string) {
	t.Helper()
	line := lineContaining(t, text, anchor)
	require.Contains(t, line, want)
}

func requireNotContainsNear(t *testing.T, text, anchor, unwanted string) {
	t.Helper()
	line := lineContaining(t, text, anchor)
	require.NotContains(t, line, unwanted)
}

func lineContaining(t *testing.T, text, anchor string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, anchor) {
			return line
		}
	}
	t.Fatalf("expected line containing %q in transcript", anchor)
	return ""
}

func TestWorkSlashNoStores(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView))

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	require.Contains(t, tx, "no job or chat store wired")
}

func TestWorkSlashListsProposalReviewWorkWithoutStores(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithMineState(tuipkg.MineState{
			Enabled: true,
			Queue: []tuipkg.MineProposal{
				{
					ID:     "p-structure",
					Kind:   tuipkg.MineKindStructure,
					Title:  "Capture render gate",
					Detail: "Run make render after doc edits",
					Target: "states.docs",
				},
				{
					ID:     "p-write",
					Kind:   tuipkg.MineKindWriteMode,
					Title:  "May I edit README.md?",
					Target: "README.md",
				},
			},
		}),
	))

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	work := transcriptAfter(t, tx, "active work: 2 item(s)")
	require.Contains(t, work, "proposal")
	require.Contains(t, work, "write_mode")
	require.Contains(t, work, "May I edit README.md?")
	require.Contains(t, work, "README.md")
	require.Contains(t, work, "/mine accept p-write")
	require.Contains(t, work, "/mine refine p-write")
	require.Contains(t, work, "/mine dismiss p-write")
	require.Contains(t, work, "structure")
	require.Contains(t, work, "Capture render gate")
	require.Contains(t, work, "Run make render after doc edits")
	requireBefore(t, work, "May I edit README.md?", "Capture render gate")
}

func TestWorkSlashListsTraceBackedMiningProposalWithoutStores(t *testing.T) {
	orch, sid := setupCloak(t)
	w := orch.InitialWorld()
	initialView, err := orch.InitialView(w)
	require.NoError(t, err)
	history := store.History{
		miningWorkEvent(t, 1, time.Unix(10, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
			RecipeID:  "recipe-accepted",
			Kind:      "binding",
			Target:    "root-instance",
			Rung:      1,
			DraftPath: ".artifacts/mining/recipe-accepted",
		}),
		miningWorkEvent(t, 2, time.Unix(20, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
			RecipeID:  "recipe-pending",
			Kind:      "intent",
			Target:    "dev-story",
			Rung:      2,
			DraftPath: ".artifacts/mining/recipe-pending",
		}),
		miningWorkEvent(t, 3, time.Unix(30, 0), store.MiningProposalDecided, store.MiningProposalDecidedPayload{
			RecipeID:   "recipe-accepted",
			Verdict:    store.MiningVerdictAccept,
			By:         store.MiningByHuman,
			FlowsGreen: true,
		}),
	}
	m := tea.Model(tuipkg.NewRootModel(orch, sid, "", initialView,
		tuipkg.WithTraceHistory(func() (store.History, error) { return history, nil }),
	))

	m = runTurnBlocking(t, m, "/work")
	tx := extractTranscript(t, m)
	work := transcriptAfter(t, tx, "active work: 1 item(s)")
	require.Contains(t, work, "proposal")
	require.Contains(t, work, "intent")
	require.Contains(t, work, "intent proposal")
	require.Contains(t, work, "dev-story")
	require.Contains(t, work, "rung 2")
	require.Contains(t, work, ".artifacts/mining/recipe-pending")
	require.NotContains(t, work, "recipe-accepted")

	history = append(history, miningWorkEvent(t, 4, time.Unix(40, 0), store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID: "recipe-pending",
		Verdict:  store.MiningVerdictReject,
		By:       store.MiningByHuman,
	}))
	m = runTurnBlocking(t, m, "/work")
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "(work: no active async work)")
}

func miningWorkEvent(t *testing.T, turn int64, ts time.Time, kind store.EventKind, payload any) store.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return store.Event{
		Turn:    app.TurnNumber(turn),
		Ts:      ts,
		Kind:    kind,
		Payload: raw,
	}
}
