# demo-video-loop

Produce (or refresh) a **deterministic, no-LLM tour demo video** of a feature,
then **gate it with the kitsoki-ui-qa vision review** — and loop until the video
actually *proves* the feature, or a budget ceiling stops it. A **maker** authors
the video plus its QA inputs; two gates judge it — a **deterministic
video-validation script** (watch-speed, frames, no `ERROR.txt`, written this
turn) and the **`qa.sh` exit code** (does the video *show* each scenario). **The
loop runs itself**: a failed QA report becomes the next maker's feedback, every
iteration is persisted as a numbered artifact, and the run ends on a verdict, not
a guess.

This is a specialization of [`stories/cherny-loop/`](../cherny-loop/) — maker →
checker → budget-guard — with the worktree as **input** (no minting) and *two*
checkers in series.

## The loop

```
            ┌──────── retry (after maker error) ────────┐
            ▼                                            │
generating(root) ── video gate PASS ─ to_qa ──► qa ──────┤ QA PASS  → @exit:achieved
   │  ▲       │                                    │      │
   │  │       │ video gate FAIL, budget left       │      │ QA FAIL, budget left
   │  └──── loop_again (feedback) ◄────────────────┘      │
   │          (QA report → next maker turn)               │
   ├ budget hit (cost / iteration) ──────────────────────┤ budget hit → @exit:exhausted
   └ abort ──────────────────────────────────────────────┘ abort      → @exit:abandoned
```

`generating` is the **root** — the operator lands where the work happens, with no
`idle`/`begin` pass-through turn. **After entry, no further prodding is needed**:
`generating` auto-emits `to_qa` once its deterministic gate passes, `qa`
auto-emits `loop_again` on a failed verdict (carrying the QA report as feedback),
and back again — maker → video gate → QA → repeat — until a gate passes or a
budget stops it.

- **generating** (the maker half + the deterministic video gate) — the **maker**
  (`host.agent.task`) acts on any prior feedback first, then authors a tour demo
  video with the **kitsoki-ui-demo** skill *and* writes the **kitsoki-ui-qa**
  inputs (a `feature.md` that *is* the change, and observable `scenarios.yaml`).
  Then `scripts/validate-video.sh` runs — deterministic code that cannot be talked
  into passing. A valid video auto-emits `to_qa`; an invalid one with budget left
  auto-emits `loop_again` carrying the gate's one-line reason as feedback.
- **qa** (the QA half) — shells out to `.agents/skills/kitsoki-ui-qa/scripts/qa.sh
  … --strict`. **The exit code IS the gate** (`0`=ship / `1`=blocking-scenario
  fail / `2`=pipeline error). A pass auto-emits `mark_achieved` (→
  `@exit:achieved`); a fail reads `qa-report.md` and auto-emits `loop_again`,
  re-entering `generating` with the report as the next maker's feedback.

The two checkers are deliberately different in kind: the **maker is an agent**;
the **video gate is deterministic code** and the **QA gate is an exit code**.
Neither gate can be argued into passing.

## Entry state

`root: generating`. The operator (or an importer's seed) lands directly in the
loop step. The first turn runs the maker and the video gate; from there the loop
self-drives.

## Exits

Every exit `requires: [terminal_reason]` (set on the routing transition) so an
importer always knows *why* the loop ended:

| Exit | `terminal_reason` | Meaning |
|---|---|---|
| `@exit:achieved` | `achieved` | `qa.sh` returned exit 0 — the video proves the feature. |
| `@exit:exhausted` | `iteration_budget` or `cost_budget` | budget ceiling hit before QA passed. |
| `@exit:abandoned` | `aborted` | operator `abort`. |

Termination is evaluated every turn after a *failed* gate, in priority order:
**goal met** (QA pass) always wins; then **cost ceiling**
(`session_cost_usd >= cost_budget_usd`, only when `cost_budget_usd > 0`); then
**iteration ceiling** (`iteration >= iteration_budget`). This is copied from
cherny-loop's §Termination discipline.

## Input contract (`world_in:`)

An importer (e.g. a dev-story tail, or a CI gate) supplies these; everything else
in `world` is engine-computed.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `worktree_path` | string | `""` | cwd pinned on every maker turn, the video gate, and `qa.sh`. The worktree is **input** — this story does not mint one. |
| `base_ref` | string | `"main"` | branch/ref to diff against; `added-diff.sh` feeds the ADDED lines to the maker as "what changed". |
| `proposal_path` | string | `""` | path (relative to the worktree) to a proposal/PRD; read once and handed to the maker. Empty ⇒ skipped. |
| `feature_slug` | string | `"demo-video"` | names the spec / manifest / video / QA artifacts. |
| `video_expectation` | string | `"auto"` | `new` \| `update` \| `auto` — hint to the maker; both `new` and `update` require the video be (re)written *this turn*. |
| `iteration_budget` | int | `5` | cycle ceiling (grounded as sound/slightly-conservative — see below). |
| `cost_budget_usd` | number | `0` | $ ceiling on the engine-maintained `session_cost_usd`; `0` disables it (opt-in). |

## Intent surface

The loop is mostly **self-driving** via internal routing intents emitted from
`on_enter`; the operator-facing surface is small.

| Intent | Surface | Effect |
|---|---|---|
| `to_qa` | internal (emitted by `generating`) | video gate passed → enter `qa`. |
| `mark_achieved` | internal (emitted by `qa`) | QA passed → `@exit:achieved`. |
| `mark_cost_exhausted` | internal (either room) | cost ceiling hit → `@exit:exhausted`. |
| `mark_iter_exhausted` | internal (either room) | iteration ceiling hit → `@exit:exhausted`. |
| `loop_again` (slot: `feedback`) | internal (either room) | gate failed, budget left → re-enter `generating` with feedback. |
| `retry` | operator (`generating`, after a maker error) | clear `last_error` and re-run the step **without double-counting the iteration**. |
| `abort` | operator (both rooms) | `@exit:abandoned`. |
| `look` | operator (both rooms) | re-render the current room. |

Both rooms end with a catch-all default (`"*"`) that holds the room and nudges
the operator — the loop never silently bounces.

## Self-driving runs read as a conversation (not just a trace)

This loop advances with **no operator input** — it cascades maker → video gate →
QA → loop → terminal the moment the session is created. So its progress is narrated
with `say:` breadcrumbs (`Iteration 1/5 · …`, `QA iteration 1 → FAIL ✗ — looping
back …`, `QA iteration 2 → PASS ✓`). The web InteractiveView surfaces those
`machine.say` breadcrumbs as **conversation bubbles** (a distinct "Loop" role —
see `tools/runstatus/src/stores/run.ts` `chatEntries` + `ChatTranscript`), so an
autonomous run is **followable as a conversation**, not only as rows in the
developer trace. This is the runtime side of the kitsoki-ui-qa principle that
*every conversation must provide meaningful feedback as it progresses, even when
no operator input is required* (kitsoki-ui-qa SKILL §8 / EVIDENCE RULE 9).

The tour demo of this story (`tools/runstatus/.../demo-video-loop-video.spec.ts` +
`src/tour/demo-video-loop-manifest.ts`) stays on that conversation surface and
additionally drives the trace to **tell the story**: each beat expands the proving
rows (the maker's submission, the video gate, the QA gate) and pulses the field
that matters (an `exit_code`, the PASS/FAIL stdout) via `window.__tourTrace`
(exposed by `TraceTimeline`). The deterministic, no-LLM demo passes its own
`qa.sh --strict` gate.

## Refine an existing video, not just create one

`video_expectation` (input contract above) drives the maker's mode: `new` authors
a demo from scratch; `update` (or `auto` when a canonical `<slug>.mp4` already
exists) **refines the existing cut** — editing only what the gate/QA feedback names
and re-recording so the file is fresh this turn. Refinement is also intrinsic to
the loop: every loop-back iteration hands the QA report to the maker as feedback,
so iteration ≥ 2 is by definition a refine. The `flows/refine_existing.yaml`
fixture exercises the `update` input mode end-to-end (no LLM).

## Host requirements

| Host call | Used for |
|---|---|
| `host.agent.task` | the **maker** — authors the demo video + QA inputs each iteration. Invoke `id: maker-<iteration>`; `working_dir` pinned to `worktree_path`; `acceptance.schema: schemas/maker_output.json`. |
| `host.run` | every gate and helper: `compute-diff` (`once:`), `read-proposal` (`once:`, when a `proposal_path` is set), `stamp-epoch`, `validate-<iteration>` (the video gate), `qa-<iteration>` (the QA gate), `read-qa-report` (when QA failed). All use `fail_on_error: false` so a non-zero exit **binds a tri-state result** rather than bouncing to `on_error`. |
| `host.artifacts_dir` | persist each iteration's record (`thread: iteration-<iteration>`) — the run trail. |

The maker invoke uses `on_error: generating`; a maker failure re-enters
`generating` with `last_error` set, the room surfaces it, and `retry` clears it.
Because the whole maker sequence is guarded on `last_error == ''`, an error
re-entry does **not** re-increment `iteration`.

## The two gate scripts — the contract

The gates are committed shell scripts, run by `host.run` from the worktree cwd
(repo-relative path, the same idiom as every other story). **These scripts are the
contract** — the rooms shell out to them and bind their result; they do not
re-derive the logic in YAML.

### `scripts/added-diff.sh <base_ref>`

Prints the **ADDED** lines of the branch diff against `<base_ref>` (additions
only; leading `+` stripped, `+++` header dropped) so the maker sees *what the
branch added*. It **never errors the loop**: missing/unknown base, a non-ancestor
base, or no git at all all degrade to empty output + exit 0 (a blank diff is a
valid "(none)"). Uses `git diff --merge-base` to tolerate a divergent base.

### `scripts/validate-video.sh <video_path> <frames_dir> <expectation> <since_epoch>`

The **deterministic video-validation gate** — exit 0 iff the produced video is the
real, shippable, watch-speed deliverable. Each check below is grounded in a *real
failure mode* from history, **not** "build green" (SCENARIOS-BRIEF §2):

- **Canonical name** — `video_path` ends in `.mp4` and is **not** `*.fast.mp4` /
  `*.SHORT-*.mp4` / `*.webm`. kitsoki-ui-demo down-names under-dwelled runs, so a
  canonical `.mp4` already encodes "watch-speed". *(Guards the
  `WEB_CHAT_PACE=0` 6-second-flash trap and the 1-second fast-validate overwrite —
  `b8e1a7d4:1243`.)*
- **Exists and non-empty.**
- **`ffprobe` duration ≥ `${KITSOKI_MIN_DEMO_SECONDS:-25}`** — read from the file,
  not from a pipe exit code. *(Guards the false-green-via-`tail` trap that burned
  `567b00fb`, and the 6s flash.)*
- **`frames_dir` holds ≥ 1 `*.png`** — frame count corroborates the recording.
- **No `ERROR.txt`** in the video's directory — the deterministic record-success
  signal (`288732e3`); artifacts live at **repo-root** `.artifacts/<name>/`.
- **mtime ≥ `since_epoch`** — proves the video was (re)written *this turn*, covering
  both `new` and `update` and rejecting a stale artifact from a prior iteration.

On failure it prints a one-line reason to stdout, which the room binds into
`video_validate_reason` and carries to the next maker turn as feedback.

The QA gate is the **third** script — the skill's own
`.agents/skills/kitsoki-ui-qa/scripts/qa.sh … --strict`, invoked by the `qa` room.
Its **exit code is the gate** (`0`/`1`/`2`); `--strict` makes `unsupported`
(a claim no frame supports — "the video doesn't *show* this") blocking, which is
the entire point of the loop (qa gate contract; `b5138ffe:1908`).

## Verify

Run the gate scripts for real, then the deterministic flow fixtures (no LLM, no
cost):

```sh
# added-diff: ADDED lines vs a base ref (run from a worktree of this repo)
bash stories/demo-video-loop/scripts/added-diff.sh main | head

# video gate: rejects a non-canonical / too-short / stale artifact (exit 1)
bash stories/demo-video-loop/scripts/validate-video.sh /tmp/x.fast.mp4 /tmp/frames auto 0
echo "exit=$?"   # 1 — fast/under-dwelled artifact

# video gate: rejects a missing canonical file (exit 1)
bash stories/demo-video-loop/scripts/validate-video.sh /tmp/missing.mp4 /tmp/frames auto 0
echo "exit=$?"   # 1 — file does not exist

# load check (both rooms, all three exits, the full intent surface render clean)
go run ./cmd/kitsoki render stories/demo-video-loop/app.yaml -o /tmp/dvl-check.md

# the deterministic, no-LLM flow fixtures (the release gate)
go run ./cmd/kitsoki test flows stories/demo-video-loop/app.yaml
```

The flow fixtures under `flows/` assert host-call contracts with
`expect_host_calls` / `expect_no_host_calls`, so they prove both the final state
and the side effects (which gate ran, with which `id:`) that got there — covering
the happy path (video gate → QA pass → `@exit:achieved`), the **refine** input
mode (`video_expectation: update`), the video-gate-fail loop-back, the QA-fail
loop-back carrying the report, budget exhaustion (cost and iteration), and
maker-error recovery via `retry`.

## Grounded in real history

The shape of this loop and the *specific* checks in `validate-video.sh` are not
invented — they reproduce real `record → QA` loops mined from this repo's
transcripts. The two mining reports
([`.context/demo-video-loop/mining-ui-demo.md`](../../.context/demo-video-loop/mining-ui-demo.md)
and
[`.context/demo-video-loop/mining-ui-qa.md`](../../.context/demo-video-loop/mining-ui-qa.md))
cite real `*.jsonl` sessions; the design (`.context/demo-video-loop/DESIGN.md`) and
[`SCENARIOS-BRIEF.md`](../../.context/demo-video-loop/SCENARIOS-BRIEF.md) distill
them.

- **The closest analogue is this loop QAing *itself*.** The cherny-loop story —
  the maker-loop story this one specializes — was itself demo'd and QA'd
  **4/4 scenarios, `--strict`, adversary 0 downgrades** in session `b400e4f5`
  ("QA PASS — 4/4 … adversary 0 downgrades … 2 blank-scan warnings are advisory",
  `b400e4f5:863`). That is the worked precedent for QAing *this* loop, and why the
  realistic demo of demo-video-loop is scoped to ~4 observable scenarios.
- **The video gate is grounded in real footguns, not "build green":** the
  false-green-via-`tail` exit ("All 5 actually failed (the earlier 'exit 0' was
  `tail`'s exit, not Playwright's)", `567b00fb`); the `WEB_CHAT_PACE=0` fast-validate
  silently overwriting the watch-speed MP4 into a 1-second flash (`b8e1a7d4:1243`);
  and `ERROR.txt` as the record-success signal (`288732e3`). The gate's
  canonical-name + ffprobe-duration + no-`ERROR.txt` + mtime checks each target one
  of these.
- **`unsupported` blocks under `--strict` because that is the whole point.** The
  richest end-to-end loop (gears PRD→DESIGN, `b5138ffe`) first returned
  `strict exit=1` with `fail conversation-legible` + 4× `unsupported` from a
  jump-to-bottom scroll, fixed with a staged pan + scroll-coverage guard, then all
  11 passed across 37 frames. `unsupported` ("the video doesn't *show* this") is
  the signal the loop exists to enforce.
- **The decision gates the machine can't reach escalate, not fake-pass.** History
  shows content/editorial correctness ("the demo should drive the chat pane, not the
  observation pane", `b8e1a7d4:1260`) and real-vs-cassette world state (the `gh`
  "17 issues" determinism leak, `356c3f6b`) as **human** gates — this loop gates on
  the verdict and surfaces the report rather than papering over them.
- **Cycle reality (sanity-checking `iteration_budget=5`):** ~1 cycle for a clean
  first capture, **2–3** as the realistic median, and **~6 `qa.sh` invocations** in
  the worst real bug-fix loop (`b5138ffe`) — which collapse into fewer *record→QA
  cycles* since several `qa.sh` calls share one recording. The default budget of
  **5** covers the median and the worst observed loop; the loop **fails fast**
  (the deterministic video gate runs before any QA spend) so cycles stay cheap.

For the broader story-QA runbook this loop's analogue (cherny-loop) is the worked
example: [`docs/stories/story-qa.md`](../../docs/stories/story-qa.md).
