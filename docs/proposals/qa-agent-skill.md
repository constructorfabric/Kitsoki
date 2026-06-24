# Tooling: `story-qa` — a Claude agent that QAs a story by using it

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tooling (agent skill)
**Epic:**   [story-qa-agent.md](story-qa-agent.md) (slice 4)

## Why

The [`mcp-studio`](../architecture/mcp-studio.md) epic gives the substrate — a human-fidelity
`Frame`, a headless free-text driver (`kitsoki drive`), terminal + web
screenshots, and the `session.*`/`render.*` MCP tools that expose them. This
slice is the actor that uses them. A flow fixture proves a story *transitions
correctly*; it cannot
tell you the menu is bewildering, the objective takes two needless turns,
a view buries the one thing the operator needs, or a room dead-ends with
no obvious way forward. Those are judgement calls — exactly what a Claude
agent driving the story *as a persona* can make, and exactly the bugs the
AI author writes blind today.

## What changes

**One sentence:** a `story-qa` agent skill takes an *app + persona +
scenario*, drives the story turn-by-turn through `kitsoki drive` (reading
each `Frame`, optionally `kitsoki shot` for a visual look, deciding its
own next input), and emits a scored UX report — view quality, navigability,
intuitiveness, objective-achievability — with embedded screenshots and a
concrete, file-grounded bug list.

## Impact

- **New:** `.agents/skills/story-qa/SKILL.md` (+ the project-local Claude
  symlink created by `make setup`). Optionally a thin
  `cmd/kitsoki` wrapper or a `tools/story-qa/` runner if the loop wants
  orchestration beyond what a subagent prompt can hold.
- **Consumes:** the [`mcp-studio`](../architecture/mcp-studio.md) tools (`session.drive` —
  frames + free-text routing; `render.tui_png`/`render.web` — PNGs), or the
  underlying `kitsoki drive` / `kitsoki shot` CLIs directly. Nothing in the
  engine changes.
- **Artifacts:** reports + screenshots go to `.artifacts/story-qa/<run>/`
  (CLAUDE.md: never committed). Cassettes captured during a live run go
  beside the story (`stories/<name>/recording.yaml`) so the run is
  re-playable.
- **Docs on ship:** the skill's `SKILL.md`; a pointer from
  `docs/testing.md` and the `kitsoki-story-authoring` skill ("proof your
  story with `story-qa` before shipping").

## How it runs

The skill is a subagent given a tight contract:

```
INPUT:  app.yaml + persona + scenario + mode(live|replay) + widths[]
LOOP (per turn, until objective met / dead-end / max turns):
  1. read the latest Frame JSON from `kitsoki drive` (frame.text + metadata)
  2. (optional) `kitsoki shot --frame f.json` → look at the PNG
  3. judge THIS screen against the rubric; note any finding
  4. decide the next input AS THE PERSONA (free text, not an intent name)
  5. write that line to drive's stdin; observe routed_intent + new frame
OUTPUT: a scored report + screenshots + a bug list
```

It drives via `kitsoki drive ... --harness <mode>` (slice 2). In **live**
mode it explores freely and records a cassette (`--record new`); the same
scenario then re-runs in **replay** mode (`--record none`) for a free,
deterministic regression pass. Per epic shared decision 3, every finding
is tagged with the mode that produced it — rendering/navigation findings
are replay-safe; "the objective was hard to reach" needs live.

### Persona + scenario

A small YAML/markdown the human (or another agent) writes:

```yaml
persona: |
  A backend engineer, comfortable in a terminal, impatient, skims.
  Has never seen this story before. Won't read long help text.
scenario: |
  A pod is crash-looping on mc-clean-24794. Get to a reproduced bug
  with a proposed fix, using only what the screens tell you.
objective: reach the `done` state with a recorded fix
max_turns: 25
```

The persona constrains *how* the agent reads and types (impatient skimmer
vs. careful first-timer surface different bugs); the scenario gives it a
goal so "is the objective achievable" is measurable.

## The rubric

Each turn's frame is scored against fixed dimensions; the report
aggregates. Scores are evidence-bearing (every low score cites the frame
and, where visual, the screenshot):

| Dimension | The question | Evidence |
|---|---|---|
| **View quality** | Is the screen readable, well-laid-out, free of rendering bugs (overlap, mis-wrap, color clash, truncation)? | frame text + screenshot |
| **Navigability** | From this screen, is it clear what I can do and how to get where I want? Are the allowed intents discoverable? | `frame.metadata.allowed_intents` vs. what the view advertises |
| **Intuitiveness** | Did my natural free-text phrasing route to what I expected? Surprising/failed routes? | `routed_intent` + confidence vs. the agent's expectation |
| **Objective progress** | Did this turn move me toward the goal, or sideways/backward? | state path delta toward `objective` |
| **Dead-ends / traps** | Any state with no obvious forward move, or a loop? | repeated state, rejected inputs |

A run yields a per-dimension score, a turn-by-turn trace with the screen
the agent saw, and a **bug list**: each item a concrete, reproducible
finding (`state`, the input that triggered it, the frame, severity, and —
where the agent can localize it — the likely story file/room, e.g.
`stories/bugfix/rooms/reproducing.yaml`).

## Why this catches what flow tests can't

- A flow fixture asserts `input X → intent Y → state Z`. `story-qa`
  asserts *"a real person with this goal could find their way"* — a
  different and complementary bar.
- The agent reads the **human-fidelity** frame (slice 1), so a view that
  renders fine at 80 cols but mis-wraps at 120 is caught — the agent
  sweeps `widths[]`.
- The screenshot (slice 3) catches visual bugs that pass a text read.
- The free-text routing (slice 2, live) tests the *actual* intent-matching
  a human triggers, not a hand-picked intent name.

## Verification

- **Self-test against a known-bad story:** seed a story with a deliberate
  UX bug (a room whose view doesn't mention how to proceed) and assert
  `story-qa` flags it. This is the skill's own regression test.
- **Replay determinism:** a recorded scenario re-run in replay mode
  produces the same frames (the teeth live in slice 2); the skill asserts
  its rendering/navigation findings are stable across the replay.
- **No-LLM default:** the replay pass needs no model. The live exploratory
  pass needs one and is run **only on request** (memory: no-LLM-tests,
  Kitsoki-positioning) — never in CI by default.

## Tasks

```
## 1. Skill
- [ ] 1.1 .agents/skills/story-qa/SKILL.md: the drive loop, persona/scenario contract, rubric, report shape
- [ ] 1.2 Verify `make setup` links it into `.claude/skills/story-qa`
- [ ] 1.3 Report template (.artifacts/story-qa/<run>/report.md + screenshots/)

## 2. Drive
- [ ] 2.1 Live exploratory pass: --harness live --record new, capture cassette
- [ ] 2.2 Replay regression pass: --harness replay --record none, stable findings
- [ ] 2.3 Width sweep (e.g. 80 + 120) per scenario

## 3. Prove + document
- [ ] 3.1 Known-bad-story self-test (skill flags the seeded UX bug)
- [ ] 3.2 Run against bugfix + dev-story; file findings as bugs (docs/stories/bugs.md format)
- [ ] 3.3 Wire into kitsoki-story-authoring loop + testing.md; trim/delete this proposal + close the epic
```

## Open questions

1. **Subagent prompt vs. orchestrated runner.** Can the whole loop live in
   a `SKILL.md` + subagent, or does it need a `tools/story-qa/` driver
   that shells `drive`/`shot` and feeds the agent? *Lean: start as a
   subagent skill driving the CLIs directly; promote to a runner only if
   the loop bookkeeping (cassette lifecycle, width sweep, artifact paths)
   outgrows a prompt.*
2. **One persona per run, or a panel?** *Lean: one persona/scenario per
   run for a clean report; the human queues several. A panel
   (skimmer + careful + adversarial) is a natural follow-up once one works.*
3. **Severity + auto-filing.** Should high-severity findings auto-file via
   the bug CLU (`docs/stories/bugs.md`)? *Lean: report first; auto-file
   behind an explicit `--file-bugs` flag.*

## Non-goals

- Replacing flow fixtures / `kitsoki test` — this is exploratory UX QA on
  top of the deterministic correctness gate, not a substitute.
- Fixing the bugs it finds. It reports (and optionally files); a human or
  the dev-story drives the fix.
- Judging story *content correctness* (is the proposed fix technically
  right) — that's the operator's domain; `story-qa` judges the
  *experience* of using the story.
