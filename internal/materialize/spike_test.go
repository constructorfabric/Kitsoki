// spike_test.go proves the load-bearing assumption the node-artifact-
// materialization plan calls out before anything else is built on it: that a
// deterministic, no-LLM, no-hosts story (POG's stories/materialize-work-item
// pilot) can be driven headless, room by room, via the orchestrator's direct
// intent-submission path (NewSession / RunInitialOnEnter / SubmitDirect /
// LoadJourney) — with a per-room hook shape suitable for a jobs.Scheduler
// Heartbeat call at each room entry/exit.
//
// DriveOperation exists but drives a whole operation with no per-turn hook
// and expects an operation run handle in world — see materialize.go's
// package doc for why materialize drives turn-by-turn instead. This test
// exercises the actual primitives materialize.go builds on: [orchestrator.Orchestrator.NewSession],
// [orchestrator.Orchestrator.RunInitialOnEnter],
// [orchestrator.Orchestrator.PatchWorld], [orchestrator.Orchestrator.SubmitDirect],
// and [orchestrator.Orchestrator.LoadJourney].
package materialize

import (
	"context"
	"os"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// pilotStoryAppPath is the slice-2 deterministic pilot story in the sibling
// POG checkout, located via the POG_PILOT_STORY_APP env var. The spike is
// only meaningful against the real story, but a missing checkout (e.g. a
// bare kitsoki CI runner with no POG sibling) skips rather than fails — this
// is a one-time integration proof, not a hermetic unit test; materialize.go's
// own tests use an in-tree fixture story instead.
func TestSpike_DriveHeadless_PilotStory(t *testing.T) {
	pilotStoryAppPath := os.Getenv("POG_PILOT_STORY_APP")
	if pilotStoryAppPath == "" {
		t.Skip("spike requires POG_PILOT_STORY_APP pointing at the POG checkout's stories/materialize-work-item/app.yaml")
	}
	if _, err := os.Stat(pilotStoryAppPath); err != nil {
		t.Skipf("spike requires the POG checkout's pilot story at %s (not found: %v)", pilotStoryAppPath, err)
	}

	def, err := app.Load(pilotStoryAppPath)
	if err != nil {
		t.Fatalf("app.Load(%s): %v", pilotStoryAppPath, err)
	}
	m, err := machine.New(def)
	if err != nil {
		t.Fatalf("machine.New: %v", err)
	}
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("store.OpenMemory: %v", err)
	}
	defer st.Close()

	orch := orchestrator.New(def, m, st, &noopHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
		t.Fatalf("RunInitialOnEnter: %v", err)
	}

	// Seed invocation params the way materialize.go's real handler will:
	// after the initial on_enter, before driving any turns.
	if err := orch.PatchWorld(ctx, sid, map[string]any{
		"node_id":  "wi-3-1-product-websites",
		"depth":    3,
		"audience": "public",
		"gate":     "reviewers assigned",
	}); err != nil {
		t.Fatalf("PatchWorld: %v", err)
	}

	// Per-room hook: record every room the drive passes through, in order,
	// exactly the shape a jobs.Scheduler.Heartbeat(jobID, {stage, status})
	// call needs at each room entry/exit.
	var rooms []string
	recordRoom := func() {
		j, err := orch.LoadJourney(sid)
		if err != nil {
			t.Fatalf("LoadJourney: %v", err)
		}
		rooms = append(rooms, string(j.State))
	}

	recordRoom() // initial room (gather), entered by RunInitialOnEnter

	// Drive to rest with "next" — the pilot story's only advancing intent —
	// until the settled room no longer offers "next" (the outcome's
	// AllowedIntents, which reflect the *state's* declared "on:" arcs, not
	// just global intent registration), mirroring how a headless job handler
	// drives a deterministic story with no human/LLM in the loop.
	hasNext := true
	for i := 0; i < 10 && hasNext; i++ {
		before, err := orch.LoadJourney(sid)
		if err != nil {
			t.Fatalf("LoadJourney: %v", err)
		}
		outcome, err := orch.SubmitDirect(ctx, sid, "next", nil)
		if err != nil {
			t.Fatalf("SubmitDirect(next) from %s: %v", before.State, err)
		}
		recordRoom()
		hasNext = false
		for _, in := range outcome.AllowedIntents {
			if in == "next" {
				hasNext = true
				break
			}
		}
	}

	want := []string{"gather", "draft", "verify", "done"}
	if len(rooms) != len(want) {
		t.Fatalf("room sequence = %v, want %v", rooms, want)
	}
	for i, w := range want {
		if rooms[i] != w {
			t.Errorf("room[%d] = %q, want %q (full sequence: %v)", i, rooms[i], w, rooms)
		}
	}

	// The story's own state (not just the room path) should reflect a
	// completed materialization — proving the drive isn't just walking
	// rooms but actually running each room's on_enter effects.
	final, err := orch.LoadJourney(sid)
	if err != nil {
		t.Fatalf("LoadJourney (final): %v", err)
	}
	if got := final.World.Get("status"); got != "complete" {
		t.Errorf("final world status = %v, want %q", got, "complete")
	}
	if got := final.World.Get("artifact_path"); got != ".artifacts/wi-3-1-product-websites/brief.md" {
		t.Errorf("final world artifact_path = %v, want %q", got, ".artifacts/wi-3-1-product-websites/brief.md")
	}
}
