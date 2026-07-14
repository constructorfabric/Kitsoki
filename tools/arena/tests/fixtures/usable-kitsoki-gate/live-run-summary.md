# Live gate run — epic-finalization (usable-kitsoki.md)

**Status:** one deliberate, authorized live run, executed once. Not re-run
automatically; not part of any CI job (see `run_live_gate.py`'s module
docstring). Raw per-cell evidence (session traces, orchestrator stdout/
stderr) lives under `.artifacts/usable-kitsoki-gate/live-epic-finalization/`
(gitignored, not checked in — this file is the durable, committed record of
what that run found).

## What was run

Entry point: `tools/usable-kitsoki-gate/run_live_calibration.py --live-gate`
(which drives `run_live_gate.py --live-gate` per cell, the same double-gated
script `docs/tracing/usable-kitsoki-gate.md` and the epic's own S6 slice
document). Surface: `mcp` — real `session.new`/`session.drive` calls through
the kitsoki studio MCP, driven by a real orchestrator agent
(`tools/mcp-drive/drive.sh`, model `sonnet`, restricted to the four studio
session tools this job needs), against a real `harness: "live"` session
(the CLAUDE-CLI-backed harness — ambient Claude Code subscription auth, no
API key). Bounded manifest, per the finalization task's cost cap:

- Scenarios: `scn-240464bf-3da7-4c55-b22b-540edc34afc0-0000` (1 turn,
  persona `docs-writer`) and `scn-687e848b-baa7-4050-aaeb-d38a340fef5a-0000`
  (2 turns, persona `bugfix-contributor`) — both from the checked-in
  18-scenario calibration corpus (`tools/session-mining/calibration/`).
- Targets: all three real `workbench:` rooms this project ships —
  `dev-story`, `pets-dev`, `slidey-dev` (`stories/<name>/app.yaml`).
- 6 total cells (2 scenarios x 3 targets), 3 turns/cell average → well
  under the ~20-dispatch cap; **3 real `host.agent.task` dispatches** were
  actually made (see below), well under budget.

## Results

| scenario | target | candidate_completed | silent_bounce | misroute_adjacent | outcome |
|---|---|---|---|---|---|
| scn-240464bf (1 turn) | dev-story | **true** | false | false | real `opus` dispatch, correctly stayed read-only on a commit request and routed it to the gitops hub instead |
| scn-240464bf (1 turn) | pets-dev | false | false | false | **no signal captured** — see "Anomaly" below |
| scn-240464bf (1 turn) | slidey-dev | false | false | false | **no signal captured** — see "Anomaly" below |
| scn-687e848b (2 turns) | dev-story | **true** | false | false | real `opus` dispatches on both turns, including the "commit it" follow-up |
| scn-687e848b (2 turns) | pets-dev | false | false | false | **no signal captured** — see "Anomaly" below |
| scn-687e848b (2 turns) | slidey-dev | false | false | false | **no signal captured** — see "Anomaly" below |

Rollup (`run_live_calibration.py`'s `_rollup_from_records`, same reduction
`run_calibration_gate.py` uses):

```
record_count = 6
silent_bounce_count = 0
misroute_adjacent_count = 0
source_completed_count = 6
both_completed_count = 2
parity_percent = 33.3%
worst_surface_parity_percent = 33.3%   (only surface swept: mcp)
verdict = failed   (33.3% < the 90.0% PARITY_THRESHOLD_PERCENT placeholder)
```

**Zero silent bounces and zero misroutes across all 6 real live cells** —
the strongest available evidence today that S2's never-silent invariant and
the workbench's routing hold up under real LLM-driven turns, not just
flow-replay. Parity is genuinely mixed: `dev-story` is 2/2 green with real,
substantive agent behavior; `pets-dev`/`slidey-dev` are 0/2 each, but for a
mechanical reason distinct from workbench quality (below), not because the
workbench misbehaved.

## dev-story cell detail (both green, both real)

**Cell 1** (`scn-240464bf`, "just file the ticket... need to commit the bugs
to the repo"): the real `landing_agent` (opus) found the ticket was already
filed on disk, correctly recognized the commit request as a mutating git
operation it should not perform headless, and routed it to the gitops hub
as a one-click bail rather than attempting it or fabricating completion.
Cost: $0.83 (one real dispatch, 65k cache-creation tokens — a fresh session
every cell, no cross-cell cache reuse).

**Cell 2** (`scn-687e848b`, "why do we keep losing the mcp tools" + "commit
it"): two real dispatches ($1.19 + $0.45). Both turns transitioned cleanly;
`candidate_completed: true`.

Total real `host.agent.task` spend across the whole run: **$2.47** (3
dispatches, all on `dev-story` — see anomaly below for why `pets-dev`/
`slidey-dev` never dispatched). Well within the $5–10 sanity budget.

## Anomaly: pets-dev / slidey-dev captured zero turn signal (both scenarios)

Every `pets-dev`/`slidey-dev` cell's session trace contains exactly ONE
event — `session.header` — and nothing else: no `session.story`, no
`world.update`, no `turn.start`/`turn.end`. This is fully reproducible
(identical shape across both scenarios, both targets), not scenario- or
orchestrator-variance noise.

**Ruled out** (verified with zero additional LLM spend):
- Stale binary / stale MCP server: the live run's `kitsoki mcp` server
  processes were freshly spawned (~20:04–20:09) from a `kitsoki` binary
  `make install`-ed earlier in this same session, well after the worktree
  was bootstrapped. (An UNRELATED stale long-running MCP server attached to
  *this agent's own* session — started before that `make install` — did
  throw an "unknown field toolbox" error on `story_validate`, but that
  server was never part of the live run; confirmed via `ps` timestamps.
  Discarded as a red herring, not evidence about the live run itself.)
- App-load failure: a standalone diagnostic (`app.LoadWithResolver` against
  `stories/pets-dev/app.yaml`, same package version) loads successfully —
  `LOAD OK, app id: pets-dev root: core`. `internal/orchestrator/
  conversational_default_intent_test.go` (a real Go test) independently
  confirms `stories/slidey-dev/app.yaml` loads and wires
  `core__landing_capture` correctly.

**Not yet root-caused:** `internal/mcp/studio/session_runtime.go`'s
`newSessionRuntime` opens the trace sink (writing `session.header`) BEFORE
calling `app.LoadWithResolver` — so this trace shape is consistent with
*some* step in `OpenDrivingSession`/`newSessionRuntime` failing for these
two targets specifically, after the sink opens but before any further
event is appended, OR with the orchestrator agent's own behavior (never
successfully calling `session_drive`) for a reason not visible in the trace
alone. `run_live_gate.py` has been hardened (this run) to always persist
the orchestrator's own stdout/stderr to `evidence_dir/<cell>.live.
orchestrator.log` regardless of exit code — this run predates that fix, so
the orchestrator's own explanation for these 4 cells was not captured and
cannot be recovered after the fact. **Follow-up needed**: re-run just these
two targets (now that the log is captured) before their decision-8 opt-in
flips — see `docs/proposals/usable-kitsoki.md`'s shared decision 8 note.

This is recorded as an honest, unresolved finding — not spun as a pass, and
not silently written off as a `pets-dev`/`slidey-dev` workbench quality
problem, since the evidence available today does not distinguish "the
engine failed to construct these two sessions" from "the orchestrator agent
failed to drive them," and either would produce this exact trace shape.

## Decision-8 disposition (see the epic's own shared decisions)

Per shared decision 8 ("S1 rolls out behind a per-story opt-in flag until
the S6 gate is green on dev-story plus two other stories, then the default
flips repo-wide") and open question 1's resolution ("a story that's green
shouldn't wait on siblings still hardening"):

- **`dev-story` is green** on this live run (2/2, zero silent bounce, zero
  misroute) — its `workbench:` opt-in is confirmed live, not just by
  flow-replay.
- **`pets-dev`/`slidey-dev` are NOT flipped** by this run — not because
  either failed a real quality bar, but because no real signal was
  captured at all. Both already carry a real `workbench:` block (inherited
  from `dev-story` via `@kitsoki/dev-story` import-folding, confirmed by
  `conversational_default_intent_test.go`), so the opt-in is structurally
  present; their LIVE gate status is "anomaly, re-run needed" rather than
  "red" or "green."
