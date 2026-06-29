# Driving GLM-5.2 to a clean bake-off pass: six infra bugs and an over-specified oracle

On 2026-06-29 a single external bake-off cell — fix `kitsoki/bug9`
("concurrent dogfood sessions share one checkout, destructive git clobbers
WIP") with **GLM-5.2** as the worker and **GPT-5.5** as the orchestrator, on a
disposable root VM driving Kitsoki through the studio MCP — went from "scores
`failed` on an unchanged tree" to a legitimate `oracle_pass: true,
quality: "solved"`.

The interesting part is not that GLM-5.2 can fix a bug. It is *what stood
between a capable model and a passing verdict*: six stacked infrastructure
faults and one over-specified test. None of them were model-quality problems,
and almost all of them were **invisible** until the run was driven end-to-end
on the real machine. That is the case for using the harness aggressively —
each real run flushes out a class of silent failure that no green unit test
would have caught.

## The shape of the failure

Every early run ended the same way: `verdict: failed`, `cost_usd: 0`,
`agent_calls: 0`, an unchanged worktree. The natural reading — "the model
didn't do anything useful" — was wrong every single time. The model never got
a fair turn. The verdict was an infrastructure artifact.

The only way to tell the difference was the trace. Three signals did all the
diagnostic work:

- `agent.stream` event count — `0` means the worker never made an LLM call;
  `>0` means it really ran.
- `agent.call.start` with no matching `agent.call.complete`, and a trace that
  ends *exactly* at that `start` — a silent stall, not a crash.
- `machine.state_entered` / `machine.transition` — how far the pipeline
  actually advanced (`bf.idle → reproducing → proposing → implementing →
  testing → reviewing → validating → done`).

## Six infrastructure faults, peeled one at a time

Each fix exposed the next failure. This is the normal texture of bringing a
live harness up on fresh infrastructure; the value is that every fault is now
encoded in `provision_vm.sh` / `drive.sh` / `config.toml` so a new VM never
hits it again.

1. **Score CLI leaked the verdict as an exit code.** `bench.py score` returned
   the oracle's pass/fail as the *process* exit status, so a legitimate
   `failed` looked like a transient error to `drive_cell.sh`'s retry ladder —
   ~2.5h of backoff retrying an already-complete score. Fix: a completed grade
   exits 0; the verdict is data in the result JSON, not an execution status.

2. **claude-code refuses `--dangerously-skip-permissions` as root.** The VM
   runs as root; the worker harness fast-failed ~530ms per attempt with empty
   output before any LLM call. Fix: `IS_SANDBOX=1` (the documented escape hatch
   for a disposable sandbox).

3. **codex does not forward parent env to MCP subprocesses.** With
   `IS_SANDBOX` exported in the shell, a *direct* drive worked but the
   codex-orchestrated run still fast-failed: codex spawns the kitsoki studio
   MCP (which forks the worker) with a bare environment. Fix: declare the
   worker MCP's env in `~/.codex/config.toml` `[mcp_servers.kitsoki.env]`
   (keeps the secret off argv), gated by a `drive.sh` knob.

4. **codex's MCP tool timeout was shorter than a worker turn.** A real GLM-5.2
   turn runs many minutes; codex's default ~60s per-tool timeout aborted
   `session.drive`/`session.submit` mid-turn, so the transition never
   persisted. Fix: `tool_timeout_sec = 3600`.

5. **The quota limiter deadlocked on a large call.** This one is a genuine
   Kitsoki code defect, not just a config gap. `internal/host/quota_control.go`
   computes a reservation's `effectiveTokens` as the *running average* of
   observed calls. A GLM-5.2 reproducer turn that autonomously reads the repo
   billed **1.4M tokens** against a **120k** window — so every subsequent
   reservation's effective estimate (the 1.4M average) *permanently* exceeded
   the window cap. The limiter waited a window, rolled it, re-checked
   `1.4M > 120k`, and throttled again — an infinite loop. Symptom: the judge's
   `agent.call.start` with **zero events after it for ~48 minutes**, until the
   MCP tool timeout killed the whole drive. The persisted state even poisons
   fresh runs. Workaround: raise `tokens_per_window` well above the largest
   single turn (the synthetic.new subscription is flat-rate) and clear the
   poisoned state file. **Real fix still owed:** the limiter must never make an
   impossible-to-satisfy reservation — cap `effective` at the window, or admit
   a single over-budget call.

6. **The fresh VM had no git identity.** With every other fault cleared,
   GLM-5.2 drove all the way through implementation and wrote a complete
   284-line fix — and then the host `git.commit` step failed with "Author
   identity unknown", the implementer errored, the pipeline bounced to
   `bf.idle`, and the cell scored `failed` on an *uncommitted but complete*
   fix. Fix: set a global git identity in provisioning.

After (6), the pipeline ran the entire state machine to a terminal `done` with
committed work. The model was never the bottleneck.

## Then the model passed — and the oracle was wrong

With the pipeline healthy, the verdict became a real pass/fail question. Two
genuinely model-facing levers closed the gap:

**The ticket steers the fix layer.** bug9 is fixable at two layers: the
*orchestrator* (derive distinct `workspace_id`s so two sessions never collide)
or the *host* (`workspace.create` refuses a colliding create from a different
session). The hidden oracle exercises the **host** layer directly —
`host.GitWorktreeHandler` with the *same* id and a *different* `session_id`
must be refused. GLM first fixed it at the orchestrator layer, which is
defensible but invisible to a host-level test, and it even forced a third
commit patching unrelated tests its broad change had broken. Sharpening the
manifest ticket to pin the required **behavior** at the host provider (the
worst case of an identical on-disk id; must refuse and name the owner;
same-session re-entry still succeeds) — *without naming the implementation* —
moved GLM cleanly to the host layer with a surgical `git_worktree.go` change.
This is fair prompt engineering, not a leak.

**Oracles must assert behavior, not one implementation's prose.** GLM's
host-layer fix was correct — it refused session B with:

```
workspace.create: checkout "bf-…" is already in use by session "session-A" …
refusing to hand a different session ("session-B") the same live tree …
```

The oracle failed it anyway, because it asserted
`strings.Contains(err, "already checked out by session")` — the *exact prose*
of the canonical fix. GLM's message named the owning session and described the
ownership conflict perfectly; it just said "in use by" instead of "checked out
by." The brittle assertion was the bug, not the fix. The correction: accept any
ownership-conflict wording (`checked out by` / `in use by` / `owned by` /
`held by`) plus the owning session id. After loosening, all three legs were
re-verified:

- baseline `ea2ca55a` — **RED** (bug present, oracle catches it)
- canonical fix `67ac5fb1` — **GREEN** (regression safety, GREEN leg intact)
- GLM-5.2's fix — **GREEN**

The rule that falls out: **never pin an oracle to a single human-readable
phrase, and never leak that phrase into the ticket** (teaching-to-the-test).
Assert the observable contract — a loud refusal that names the owner, WIP
preserved, same-session re-entry still OK.

## Verdict

```
oracle_pass: true   oracle_status: pass   quality: solved
candidate: glm-5.2  treatment: kitsoki    provider: synthetic.new
```

GLM-5.2 produced a host-layer ownership sidecar in `internal/host/git_worktree.go`
that refuses cross-session checkout sharing, preserves the owner's uncommitted
WIP, and still short-circuits legitimate same-session re-entry — passing a
sound oracle that a no-op baseline still fails.

## What this case study is really about

The model was capable the whole time. What the aggressive end-to-end run bought
was a list of **silent** failure modes that only appear when a real worker runs
through real infrastructure: a verdict leaked as an exit code, an env that
doesn't cross a subprocess boundary, a timeout shorter than a turn, a quota
average that deadlocks, a missing git identity, and an oracle that tests prose.
Five of six are now permanent provisioning guarantees; one is an open Kitsoki
defect with a ticket; and the oracle is now a behavior contract instead of a
string match.

That is the argument for using the harness as a forcing function: **drive it
for real, read the trace, and every run pays for itself in flushed-out
faults** — long before any of them could bite a customer-facing run.

## Pointers

- Harness/provisioning: `tools/bugfix-bakeoff/external/provision_vm.sh`,
  `tools/mcp-drive/drive.sh`, `tools/bugfix-bakeoff/external/drive_cell.sh`
- The bug spec + oracle: `tools/bugfix-bakeoff/external/projects/kitsoki/manifest.yaml`,
  `…/projects/kitsoki/testdata/bug9.go`
- The quota defect: `internal/host/quota_control.go` (`effectiveTokens` /
  `tryReserve`)
- Related: [glm52-quota-dogfood.md](glm52-quota-dogfood.md),
  [bugfix-bakeoff.md](bugfix-bakeoff.md)
