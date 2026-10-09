package codeact

import (
	"context"
	"encoding/json"
	"testing"
)

// This file is the S3 RED gate for codeact tracing + record/replay (see
// docs/goals/codeact/decomposition.yaml, slice s3-tracing-replay, goal G3).
// It pins the contract the implementer builds against:
//
//   - Result gains a Trace []TraceStep field: one entry per loop iteration,
//     carrying at minimum the emitted snippet and the resulting observation
//     or error, so the orchestrator can journal every codeact step into the
//     event log (see internal/journal for the existing Entry/Writer shape
//     this should eventually feed — journaling into the real event log is
//     follow-up wiring, not asserted here; this file only pins the in-memory
//     Result-level trace shape it must be built from).
//   - A minimal JSON-roundtrippable Cassette format (Cassette/CassetteStep),
//     built from a recorded Trace, mirroring the granularity decision to
//     cassette codeact per-step exchanges (analogous to
//     internal/testrunner's CassetteEpisode for other host.agent.* verbs, and
//     internal/host/starlark's HTTPCassette/HTTPEpisode for builtin-call
//     exchanges — see .context/codeact-design-decisions.md if present).
//   - A ReplayAgent that satisfies the Agent interface purely by replaying a
//     Cassette, never invoking any live/scripted agent logic, and that
//     returns an error (rather than silently improvising) if asked for a
//     step beyond what the cassette recorded. This is what proves "zero-LLM
//     replay": swapping Params.Agent for a *ReplayAgent must reproduce the
//     exact same Result.Terminated/Payload with no scripted-agent step
//     functions ever called past what was recorded.
//
// None of TraceStep, Cassette, CassetteStep, or ReplayAgent exist yet — this
// file is expected to fail to compile until a later slice adds them.

// TestRun_JournalsTraceField asserts Run's Result exposes a Trace with one
// entry per executed step, each entry carrying the emitted snippet and the
// resulting observation (or structured error), in order.
func TestRun_JournalsTraceField(t *testing.T) {
	schema := func(payload map[string]any) error {
		if _, ok := payload["sha"].(string); !ok {
			return errNotAString("sha")
		}
		return nil
	}

	agent := &scriptedAgent{steps: []func(int, map[string]any, *ErrorEnvelope) Emission{
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			return Emission{Snippet: "def main(ctx):\n    return {\"seen\": ctx.world.get(\"target\")}\n"}
		},
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			return Emission{Done: true, Payload: map[string]any{"sha": "abc123"}}
		},
	}}

	res, err := Run(context.Background(), Params{
		Budget: 5,
		World:  map[string]any{"target": "regression-commit"},
		Agent:  agent,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Contract: Result.Trace has exactly one TraceStep per loop iteration
	// (StepResult already recorded these as res.Steps; Trace is the stable,
	// serializable projection tracing/replay tooling is built against).
	if len(res.Trace) != len(res.Steps) {
		t.Fatalf("expected Trace to journal one entry per step (%d), got %d", len(res.Steps), len(res.Trace))
	}

	step0 := res.Trace[0]
	if step0.Step != 0 {
		t.Fatalf("expected Trace[0].Step == 0, got %d", step0.Step)
	}
	if step0.Snippet == "" {
		t.Fatalf("expected Trace[0].Snippet to carry the emitted snippet, got empty")
	}
	if step0.Observation["seen"] != "regression-commit" {
		t.Fatalf("expected Trace[0].Observation to carry step 0's observation, got %v", step0.Observation)
	}
	if step0.Error != "" {
		t.Fatalf("expected Trace[0].Error to be empty on a successful step, got %q", step0.Error)
	}

	step1 := res.Trace[1]
	if step1.Step != 1 {
		t.Fatalf("expected Trace[1].Step == 1, got %d", step1.Step)
	}
	if !step1.Done {
		t.Fatalf("expected Trace[1].Done to be true for the terminating done() step")
	}
	if step1.Payload["sha"] != "abc123" {
		t.Fatalf("expected Trace[1].Payload to carry the done() payload, got %v", step1.Payload)
	}
}

// TestReplay_ZeroLLMReproducesTrajectory records a codeact trajectory with a
// scripted agent, cassettes it, then replays the cassette through Run with a
// ReplayAgent standing in for Params.Agent. It asserts:
//
//  1. The replayed Result reproduces the exact same Terminated/Payload as the
//     original live run.
//  2. The Cassette survives a JSON round-trip (it is the artifact committed
//     to disk for `kitsoki test flows` to load, per s3's acceptance
//     criteria).
//  3. Replay makes zero calls into anything resembling a live/scripted
//     agent: ReplayAgent is the *only* Agent driving the replayed Run, and if
//     asked for a step beyond what the cassette holds, it errors rather than
//     inventing a snippet — proving the trajectory is fully determined by the
//     cassette, not by falling back to live behavior.
func TestReplay_ZeroLLMReproducesTrajectory(t *testing.T) {
	schema := func(payload map[string]any) error {
		if _, ok := payload["sha"].(string); !ok {
			return errNotAString("sha")
		}
		return nil
	}

	recordingAgent := &scriptedAgent{steps: []func(int, map[string]any, *ErrorEnvelope) Emission{
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			return Emission{Snippet: "def main(ctx):\n    return {\"seen\": ctx.world.get(\"target\")}\n"}
		},
		func(step int, obs map[string]any, errEnv *ErrorEnvelope) Emission {
			return Emission{Done: true, Payload: map[string]any{"sha": "abc123"}}
		},
	}}

	world := map[string]any{"target": "regression-commit"}

	live, err := Run(context.Background(), Params{
		Budget: 5,
		World:  world,
		Agent:  recordingAgent,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("live Run: %v", err)
	}
	if live.Terminated != TerminatedDone {
		t.Fatalf("expected live run to terminate done, got %s", live.Terminated)
	}

	// Build the cassette from the live trace and round-trip it through JSON,
	// since that's the on-disk artifact `kitsoki test flows` will load.
	cas := NewCassetteFromTrace(live.Trace)
	raw, err := json.Marshal(cas)
	if err != nil {
		t.Fatalf("marshal cassette: %v", err)
	}
	var reloaded Cassette
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatalf("unmarshal cassette: %v", err)
	}
	if len(reloaded.Steps) != len(live.Trace) {
		t.Fatalf("expected cassette to round-trip %d steps, got %d", len(live.Trace), len(reloaded.Steps))
	}

	replayAgent := &ReplayAgent{Cassette: reloaded}

	replayed, err := Run(context.Background(), Params{
		Budget: 5,
		World:  world,
		Agent:  replayAgent,
		Schema: schema,
	})
	if err != nil {
		t.Fatalf("replayed Run: %v", err)
	}

	if replayed.Terminated != live.Terminated {
		t.Fatalf("expected replay to reproduce Terminated=%s, got %s", live.Terminated, replayed.Terminated)
	}
	if replayed.Payload["sha"] != live.Payload["sha"] {
		t.Fatalf("expected replay to reproduce Payload %v, got %v", live.Payload, replayed.Payload)
	}

	// Proving zero-LLM: a ReplayAgent asked for a step beyond what the
	// cassette recorded must error, not fabricate a snippet. This is the
	// property that distinguishes "replay" from "a second scripted agent
	// that happens to agree" — it can never go off-script.
	exhausted := &ReplayAgent{Cassette: Cassette{Steps: reloaded.Steps[:1]}}
	if _, err := exhausted.Next(context.Background(), 1, nil, nil); err == nil {
		t.Fatalf("expected ReplayAgent.Next to error when asked for a step beyond the cassette, got nil error")
	}
}
