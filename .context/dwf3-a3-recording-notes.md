# WS-A A3 — gears-rust onboard recording notes

Slice: `.context/dev-workflows-surface-matrix-plan.md` WS-A A3 — "a recorded
onboard of a fresh gears-rust checkout → `trace to-flow` → committed
flow+cassette; a product-journey onboarding scenario replayable from it."

## What was recorded

A **real, fresh-checkout simulation** of gears-rust:

```sh
git clone --local /Users/brad/code/gears-rust <scratch>/gears-rust-fresh
cd <scratch>/gears-rust-fresh
git remote set-url origin https://github.com/bsacrobatix/gears-rust.git
rm -rf .kitsoki.yaml .kitsoki   # strip onboarding state; leave everything else
```

(the real gears-rust checkout at `/Users/brad/code/gears-rust` was never
written to; only the scratch clone was touched, and only under
`/private/tmp/.../scratchpad`.)

Onboarded through the real dev-story onboard path, driven headlessly via
`kitsoki session create` / `kitsoki session continue` (the exact headless
recipe `docs/project-onboarding.md` documents), from inside the kitsoki
worktree with real host effects (no `test flows` stubs):

```sh
go run ./cmd/kitsoki session create --app stories/dev-story/app.yaml --db "$DB" --key local:gears-onboard
go run ./cmd/kitsoki session continue ... --intent landing_capture --slots '{"request":"onboard <scratch>/gears-rust-fresh"}'
go run ./cmd/kitsoki session continue ... --intent init_discovered
go run ./cmd/kitsoki session continue ... --intent draft_profile      # the one live LLM call
go run ./cmd/kitsoki session continue ... --intent profile_drafted
go run ./cmd/kitsoki session continue ... --intent quit               # bail from the (broken) validate view, see below
go run ./cmd/kitsoki session continue ... --intent confirm_init
go run ./cmd/kitsoki session continue ... --intent init_applied
```

Real deterministic discovery ran (`stories/dev-story/scripts/init_discover.py`
via `host.run`) against the actual scratch checkout and correctly detected:

- stack: `rust project`, commands `make dev` / `make test` / `make build`
- VCS: git, default branch `docs/kitsoki-integration`, remote
  `https://github.com/bsacrobatix/gears-rust.git`
- **A2 external ticket-repo passthrough** (the deliverable this slice was
  asked to exercise): `tracker: github`, `ticket_repo:
  bsacrobatix/gears-rust` — bound automatically from the git remote, with
  zero operator input. The applied `.kitsoki/project-profile.yaml` carries
  `tracker.provider: github`, `tracker.repo: bsacrobatix/gears-rust`, and
  `kitsoki.instance.bindings.ticket: host.gh.ticket` — i.e. `pick_ticket` /
  triage on gears-rust binds to real GitHub issues, read-only, as A2
  intended.

`confirm_init` / `init_applied` really wrote `.kitsoki.yaml`,
`.kitsoki/project-profile.yaml`, `.kitsoki/stories/gears-rust-fresh-dev/`, and
the toolkit into the scratch checkout (verified on disk; not committed
anywhere — the scratch clone is thrown away after this recording).

## The one live-LLM call (subscription-backed, no API key)

`draft_profile`'s `on_enter` fires `host.agent.decide` (`profile_drafter`
persona, `prompts/init_profile_draft.md`) through the ambient `claude` CLI
harness (same subscription auth `kitsoki run`/`kitsoki web` use by default —
no `ANTHROPIC_API_KEY`, no separate profile flag needed for `kitsoki
session continue --intent`, since direct-intent dispatch never touches the
*routing* harness and the agent-effect exec defaults to the ambient CLI).
Real cost: **$0.254**, ~80s wall time, model `claude-sonnet-4-6`.

**Harness gap found:** the agent's `mcp__validator__submit` call was refused
twice ("Claude requested permissions to use mcp__validator__submit, but you
haven't granted it yet") — the studio validator tool the schema-forced
`decide` verb needs was not preauthorized for this headless dispatch path.
The agent recovered by emitting the schema JSON as prose instead
(`tool_bypassed=true` in the trace), and the room's schema-decode path
accepted it, so the turn still transitioned correctly — but this is the
same class of gap as the codex "tool_search defers MCP tools" finding
already tracked in memory (`codex-defers-mcp-tools-toolsearch.md`), now
also observed on the Claude/`host.agent.decide` path. Not filed as a
standalone ticket this slice (recovered gracefully, did not block the
recording) — worth a follow-up if it recurs on a `decide` call whose schema
answer genuinely requires the tool (this one didn't).

## A real story bug this recording found — fixed

Continuing past `profile_drafted` into `profile_validated` **hard-crashed**
every time, live:

```
error: orchestrator: SubmitDirect: machine.Turn: render state view for
"init_profile_validate": view[3] (kv) when: expr whitelist violation in
"world.init_profile_validation != {}": map literal expressions are not
allowed
```

Root cause: `stories/dev-story/rooms/init.yaml`'s `init_profile_validate`
view gated a `kv` block on `world.init_profile_validation != {}` — a `{}`
map literal, which `internal/expr/expr.go`'s whitelist visitor rejects
**unconditionally at compile time** (`MapNode` case), independent of the
runtime value. Every live render of this state (`kitsoki run`, `kitsoki
session continue`, `kitsoki turn`) hard-errored.

**The much bigger surprise:** the *existing* flow fixture
(`stories/dev-story/flows/init_llm_profile_draft.yaml`) already drove
through this exact state and reported `PASS` — because it had no
`expect_view_matches` on that turn, and `kitsoki test flows` does not
propagate a view-render error into a turn failure unless the fixture
explicitly asserts view content. So the bug was invisible to CI while being
100% fatal on every live/interactive path. This is itself a first-class
harness gap (a flow "PASS" that never actually proves the view renders) —
noted here per the marathon's evidence discipline; not fixed in this slice
(would mean deciding whether flow-test failures should hard-fail on ANY
view-render error by default, a broader behavior change out of this
slice's scope).

**Fix applied** (`stories/dev-story/rooms/init.yaml`):

```diff
-        when: "world.init_profile_validation != {}"
+        when: "len(world.init_profile_validation) > 0"
```

`len(...)` is an allowed builtin; this is semantically identical ("has this
been populated") without the disallowed map literal. Verified:

- reproduced the crash pre-fix via a no-LLM `kitsoki turn` stateless probe
  (`--state init_profile_validate --intent look`), confirmed it renders
  clean post-fix, with the same probe;
- added a regression pin (`expect_view_matches: "OK:"`) to the existing
  `init_llm_profile_draft.yaml` flow's `profile_drafted` turn, confirmed it
  **fails** on the pre-fix code and **passes** post-fix — so this class of
  "PASS but never rendered" bug can't recur silently in this fixture again.

Full `stories/dev-story` flow suite: **93/93 pass** after the fix (was
92/92 before this slice added `onboard_gears_rust.yaml`).

## The committed proof

Because the live path's `draft_profile → profile_drafted → profile_validated`
chain hits (and, until this slice, always fatally hit) the room bug above,
the recorded session took `draft_profile → profile_drafted → quit` (bail out
of the LLM-draft review before touching the fixed room) then
`confirm_init → init_applied` — the real deterministic accept path,
completing the onboard end-to-end. That is the exact turn sequence
converted to a flow:

- **Flow:** `stories/dev-story/flows/onboard_gears_rust.yaml`
- **Cassette:** `stories/dev-story/flows/onboard_gears_rust.cassette.yaml`
  (5 episodes: 3 `host.run` — discover/apply/install_tools — + 1
  `host.agent.decide` carrying the real LLM draft output + 1 more `host.run`)
- Converted via `kitsoki trace to-flow` from
  `~/.kitsoki/sessions/dev-story/7ea0fa8e-local-gears-onboard.jsonl` (kept
  locally; not committed — it is machine-local and carries the scratch
  clone's absolute paths, which are cosmetic replay text, not load-bearing).
- Replays green, **no-LLM**, via:
  ```sh
  go run ./cmd/kitsoki test flows stories/dev-story/app.yaml \
    --flows stories/dev-story/flows/onboard_gears_rust.yaml
  ```
  Confirmed via a `--trace-out` fresh trace that no `agent.call.start` event
  fires during replay (only `harness.dispatched`/`harness.called` — the
  direct-intent no-LLM path).

## Product-journey scenario

The generic `project-onboarding` scenario (`tools/product-journey/scenarios.json`)
already declares `flow-fixture` as its one playback-capable evidence kind
(WS-F F2). This slice ran it for real against `gears-rust` /
`core-maintainer` (terminal-first, matching the onboard×TUI cell) and
attached the committed flow as that slot's backing evidence:

```sh
python3 tools/product-journey/run.py --emit-run --project gears-rust \
  --persona core-maintainer --scenarios project-onboarding \
  --seed dwf3-a3-onboard --transport tui
python3 tools/product-journey/run.py --attach-evidence --run-dir <run-dir> \
  --scenario project-onboarding --evidence-kind flow-fixture \
  --evidence-path stories/dev-story/flows/onboard_gears_rust.yaml \
  --evidence-status captured
python3 tools/product-journey/run.py --review-run --run-dir <run-dir>
python3 tools/product-journey/run.py --validate-run --run-dir <run-dir>
```

`--validate-run` reports `valid` and `playback-evidence-backed` passes (the
WS-F F2 contract this slice's evidence had to satisfy) — confirmed with
`tools/product-journey/playback_evidence_test.py` also green. The review
gate itself reports `needs_evidence` (missing a key-interaction video and
findings records) — that is a broader WS-F/WS-G review-completeness gap
unrelated to the playback-evidence contract, out of this slice's scope; not
papered over here.

The run bundle is committed at
`.artifacts/product-journey/20260705T225534Z-gears-rust-core-maintainer-dwf3-a3-onboard/`
(force-added; `.artifacts/` is normally gitignored, matching the existing
`slidey-hybrid` precedent of committing a specific reviewable artifact bundle).

## Matrix

`tools/dev-workflow-matrix/manifest.yaml`'s `onboard × tui × gears-rust` cell
flips `gap → works`, with a `verdicts.mechanical` pointer at
`tools/dev-workflow-matrix/verdicts/onboard-tui-gears-rust.replay.json`
(a small, hand-written, committed completion-state verdict — this one-off
recording is not wired into `run_checks.py`'s dynamic check sweep, which only
maps the whole-story flow suites). `make dev-workflow-matrix` /
`make dev-workflow-matrix-check` both pass with the regenerated
`docs/testing/dev-workflow-matrix.md`.

## VS Code note (cross-ref only)

Per the plan's WS-A A3 line: the extension's `storiesDir` should
auto-discover the onboarded `.kitsoki/stories/<id>-dev/` instance this
pipeline just proved works on gears-rust. Recorded as a one-line pointer in
`docs/project-onboarding.md` (see "VS Code" note); another slice (WS-D D2)
implements the actual discovery.

## Quota / cost spent

One live LLM call (`draft_profile`'s `host.agent.decide`): **$0.254**,
~80s wall time, `claude-sonnet-4-6`, subscription-backed (no API key). No
other live LLM calls were made recording this slice.
