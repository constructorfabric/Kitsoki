---
name: kitsoki-debugging
description: Diagnose kitsoki-dev dogfood / bugfix / feature-story bugs. Use when the user reports "going back to idle", "silent bounce", "stuck at <state>", or any "this room didn't do what I expected" complaint. Drives the same state machine the TUI uses, against the real on-disk repo state, without spinning up a session. Surfaces the underlying host-call errors that the TUI's `on_error:` arcs swallow.
---

# Kitsoki Debugging

The TUI hides almost everything useful behind `on_error: idle` arcs. When the user says "it keeps going to idle", **assume the TUI is lying about what really failed** and reach for the tools below.

## When you read a user's "bounce to idle" report

You have four primary tools, in order of cost:

1. **`kitsoki turn`** — one-shot a turn against the real repo, dump host calls + errors as JSON. Cheap, repeatable, runs against the actual on-disk state. **Use first.**
2. **Trace JSONL** at `/tmp/kitsoki-dogfood-trace.jsonl` — what the user actually saw, including every `machine.transition`, `host.on_error.redirect`, `machine.effect.applied`. **Use to confirm the user's trace matches your reproduction.**
3. **Go tests under `internal/orchestrator/dogfood_smoke_test.go`** — `t.TempDir()` + real `git init` + real host registry, oracle stubbed. **Use to lock in regressions.**
4. **The actual TUI** — slow, hard to script, hard to inspect mid-flight. Use last.

## Step 1 — reproduce with `kitsoki turn`

Build a fresh binary into `/tmp` so you're not racing the user's running TUI:

```sh
go build -o /tmp/kitsoki-fixed ./cmd/kitsoki
```

Then dump the user's world state into a JSON file. The trace JSONL has every `turn.done` event with a `view_rendered` field — its prelude tells you state path, workspace_id, feature_branch, etc. Build the world file from those values:

```sh
cat > /tmp/world.json <<'EOF'
{
  "core__bf__ticket_id":          "<from trace>",
  "core__bf__workspace_id":       "bf-<ticket>",
  "core__bf__feature_branch":     "fix/<ticket>",
  "core__bf__workdir":            ".worktrees/bf-<ticket>",
  "core__bf__base_branch":        "main",
  "core__bf__bf_autostart_attempted": true,
  "core__bf__bugfix_mode":        "full",
  "core__bf__judge_mode":         "human",
  "core__bf__judge_confidence_threshold": 0.8,
  "core__bf__propose_fix_artifact": {
    "summary_title":    "...",
    "summary_markdown": "...",
    "affected_files":   ["..."],
    "confidence": 0.9
  }
}
EOF
```

Then fire the turn:

```sh
/tmp/kitsoki-fixed turn stories/kitsoki-dev/app.yaml \
  --state core.bf.proposing \
  --intent core__bf__accept \
  --world @/tmp/world.json \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('next:',d.get('next_state'),'err:',d.get('error_message')); [print(c.get('namespace'),'->',(c.get('error') or 'ok')[:140]) for c in d.get('host_calls',[])]"
```

What you get:
- `next_state` — where the session actually lands (vs. the user's report).
- `error_message` — set when the intent itself is rejected (INTENT_NOT_ALLOWED, GUARD_FAILED, MISSING_SLOTS).
- A list of every host invocation with its full error string. **This is the layer the TUI's `on_error:` arcs swallow.**

If `next_state` is the room the user expects, the bug is fixed (or never existed against current code) and the user is running a stale binary — tell them to rebuild. If it's something else, the host call that errored tells you exactly where to look.

### Gotchas with `kitsoki turn`

- World keys are fully-qualified after import-folding: a bugfix-story var named `workspace_id` becomes `core__bf__workspace_id` when invoked through `stories/kitsoki-dev/app.yaml`. Get the names wrong and the room's `on_enter:` chain sees defaults.
- Intent names are also import-folded: `core__bf__accept`, not `accept`.
- Skipping a required `propose_fix_artifact` (etc.) on the world doesn't fail the intent — it fails some host call inside the target room's `on_enter:` with a confusing template render error. If you see `effect ... render` errors, your world is incomplete.
- `--input "..."` routes through the real LLM harness (claude-cli) and burns budget. **Use `--intent` for diagnosis.**

## Step 2 — cross-check the trace

`/tmp/kitsoki-dogfood-trace.jsonl` (path is `KITSOKI_TRACE_FILE` env or the TUI's default) is a slog JSONL log. Useful greps:

```sh
# Every turn boundary the user saw, latest first
tac /tmp/kitsoki-dogfood-trace.jsonl | grep -m 10 turn.done | jq -c '{turn, state_path, new_state}'

# Did an on_error redirect fire? (added in this skill's era)
grep host.on_error.redirect /tmp/kitsoki-dogfood-trace.jsonl | tail

# What host calls fired during a specific turn?
grep '"turn":6' /tmp/kitsoki-dogfood-trace.jsonl | grep 'effect.applied.*invoke'
```

Note: `HostReturned` is a **store event**, not a slog log. It carries the actual error from the handler, but the trace JSONL does NOT include it. To see host errors you must either use `kitsoki turn` or inspect the store events directly.

## Step 3 — host errors live in the store events, not the trace

If you need to inspect what really happened in a *real session* (not a one-shot), pull store events from the persisted DB:

```go
hist, _ := s.LoadHistory(sid)
for i := len(hist)-1; i >= 0; i-- {
    if hist[i].Kind == store.HostReturned {
        t.Logf("%s", string(hist[i].Payload))
    }
}
```

A `HostReturned` event with an `"error"` field is a host call that failed. If the source room had `on_error: <target>`, that's where you bounced — even though the trace shows no transition log for it (the redirect is logged as `host.on_error.redirect` in the orchestrator's slog, separate from `machine.transition`).

## Step 4 — pin the fix with a Go test

`internal/orchestrator/dogfood_smoke_test.go` has the pattern:

- `setupDogfoodRepo(t)` builds a real `git init` repo at `t.TempDir()` and copies the live `stories/` + `issues/` trees into it.
- `newSmokeOrchestrator(t, repoRoot)` wires the real host registry with the oracle stubbed (no LLM cost).
- Drive turns via `orch.SubmitDirect(ctx, sid, intent, slots)` exactly as the TUI does.
- Mutate the repo between turns (`os.RemoveAll`, `os.WriteFile`, `exec.Command("git", "worktree", "prune")`) to simulate real-world corruption shapes.

Two existing tests show the patterns:

- `TestDogfoodSmoke_ContinueFromProposingReachesImplementing` — pruned-worktree shape (dir + registration gone).
- `TestDogfoodSmoke_ProposingAccept_RegisteredWorktreeDirtyTree` — registered-worktree + dirty unrelated file shape (the trickier production case where path-comparison bugs hide).

## Patterns that hide bugs (and how to expose them)

| Symptom in TUI | Underlying cause | How to confirm |
|---|---|---|
| Bounce to idle, no diagnostic | Host call errored, `on_error: idle` fired silently | `kitsoki turn` — the `host_calls[]` array shows the actual error |
| Stuck in a room despite typing accept | Intent rejected (`INTENT_NOT_ALLOWED_IN_STATE`, missing slots, guard false) | `kitsoki turn` returns `mode:"rejected"` with `error_code` + `error_message` |
| Implementing crashes after a process restart | `bf_autostart_attempted=true` pinned but workspace gone | World has the flag; `git worktree list` doesn't show the dir |
| Commit fails with `git.commit: ` (empty message) | git's "nothing to commit" goes to **stdout**, not stderr; lenient-mode checks missed it | Check `gitCommit` in `internal/host/git_vcs.go` reads both streams |
| Worktree create says "already exists" but you can't find it | Path-comparison bug: relative vs absolute | `git worktree list --porcelain` always emits absolute; handlers must `filepath.Abs` or match by basename |

## A note on `on_error: idle` as an anti-pattern

The bugfix story's room arcs use `on_error: idle` heavily. This makes pipelines "fail safe" by landing back at a known parked state — but at the cost of erasing the diagnostic. Authors should be wary:

- A `on_error: idle` with no `last_error` surfacing in the destination view = silent failure.
- Always check that the destination room shows `world.last_error` somewhere so the operator gets a hint.
- For host calls whose failure modes are recoverable (e.g. "worktree already exists" → reuse, "no upstream tracking" → skip), prefer making the handler idempotent over relying on `on_error:` redirects.

## Happy-path tests are not enough

A test that asserts `next_state` advanced is NOT enough to prove the room did its job. Rooms can advance after running a no-op `on_enter:` chain — the user sees a clean transition while none of the actual work happened.

Concrete trap (regression-of-record): the bugfix `implementing` room was supposed to apply the proposed fix to the worktree. For months the room's `on_enter:` was `workspace.sync + vcs.commit + say "Fix applied"` — no oracle, no edits, no actual code change. Tests that only checked `next_state == implementing` (and `next_state == testing` on accept) passed every time. The bug surfaced only when the user noticed the testing room reporting "Fix not applied — repro tests still fail; files unchanged."

When writing smoke tests for a room that *should* produce side effects, assert the side effects directly:

- After a commit-step, run `git show --name-only HEAD` and assert the file you expected to be committed is there.
- After an oracle-edit step, write a real file from the stub and check the file lands in HEAD.
- After a workspace.create, `stat` the dir.
- After a host call that binds, check the world key got bound.

If your test would pass against a room whose `on_enter:` is empty, your test isn't testing what you think.

## Type-assertion landmines in handler args

YAML `{{ world.x }}` references where `x` is a list render as the underlying Go slice. Depending on how the runtime resolved the value, that slice may arrive at the handler as `[]any` *or* `[]string`. A handler that only checks `args["files"].([]any)` silently treats `[]string` as no-list-passed and falls through to its default behavior. Look at `gitCommit` in `internal/host/git_vcs.go` for the both-shapes pattern.

Same trap applies to `[]int`, `[]map[string]any`, nested `[]any` of any element type. When debugging "this handler dropped my list", check both type shapes before anything else.
