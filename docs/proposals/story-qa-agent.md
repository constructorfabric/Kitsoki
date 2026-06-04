# Epic: Story QA agent — drive a story as a human would

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 4 (0/4 shipped)

## Why

The `devstory` story (and every operator story) is built by an AI agent
and driven by a human. **Every bug only the human sees is one the AI
wrote blind** — that framing is the whole reason
[`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) exists. The
shipped trace + `turn` + `inspect` surfaces let the AI *probe* a story;
they don't let it *use* one. Two gaps keep the AI blind:

1. **It can't see what the human sees.** The closest surface,
   `view_rendered` on `turn.done`, is a width-80, ANSI-stripped,
   `IdentityGlamour` projection (`machine.go:2215`,
   `orchestrator/journal_write.go:344`) — *and* it is the room body only.
   The human reads that body re-flowed at their real terminal width with
   Glamour styling **plus** the chrome the TUI paints around it: the
   routing/thinking line, the action-required banner, the divider, the
   input prompt (`> » # ? …`), the per-room footer, and the
   room·state·mode·queue status row (`tui.go:4216-4242`). No function
   anywhere returns the assembled screen. An AI reviewing `view_rendered`
   reviews a view no human ever sees.
2. **It can't walk the story with judgement.** Flow fixtures replay a
   *fixed* input list through the machine; they never let an actor read a
   view, decide what to type next, and discover that the menu is confusing
   or the objective is two turns further than it should be. There is no
   persistent driver that takes **free text**, routes it (live or from a
   cassette), and hands back the human-fidelity screen.

This epic gives the AI a **Claude QA agent** that, handed a *persona* and
a *scenario*, walks a story end-to-end — reading the exact screen a human
would, optionally looking at a real screenshot of it, deciding its own
inputs — and reports on view quality, navigability, intuitiveness, and
whether the process objective is actually reachable. The same machinery
catches rendering bugs (deterministic, replay-safe) and UX dead-ends
(needs a live model) before a human ever hits them.

## What changes

Once every slice ships:

- **One shared frame composer** returns the exact screen a human sees —
  room body + all chrome — at any width, as a bundle: `text` (for the
  agent to read), `ansi` (for a screenshot), and `metadata` (state path,
  allowed intents, mode, world digest) so the agent reasons without
  re-parsing. The live TUI and every headless caller render through it, so
  they cannot drift.
- **`kitsoki drive`** runs the real turn loop headlessly and
  interactively: a persistent trace-backed session that accepts **free
  text** each turn, routes it through `--harness live` *or*
  `--harness replay`, and emits the frame bundle per turn as JSONL. It
  carries VCR-style record/playback modes (`once | none | all | new`) so a
  live exploratory run can capture a cassette that a later run replays for
  free. This supersedes the scripted `kitsoki drive` sketched in
  [`ai-collaboration-proposal.md`](ai-collaboration-proposal.md) §1 — an
  *agent* decides its next input from what it just saw, so the loop is
  interactive, not a pre-written input file.
- **`kitsoki shot`** rasterizes a frame's ANSI to a PNG (real monospace +
  color), so any state can be handed to Claude for visual review.
- **A `story-qa` agent skill** drives the loop against a persona +
  scenario, scores a UX rubric, and emits a markdown report with embedded
  screenshots and a concrete, file-grounded bug list.

## Impact

- **Spans:** tui (frame seam, screenshot), runtime/cli (`drive` + VCR
  modes), tooling (the agent skill + rubric).
- **Net surface:** `internal/tui/` (new frame composer extracted from
  `RootModel.View`), `internal/harness/` (VCR modes over the existing
  `RecordingHarness`/`ReplayHarness`), new `cmd/kitsoki/drive.go` and
  `cmd/kitsoki/shot.go`, new `docs/skills/story-qa/`.
- **Builds on:** the [`view-rendering-readability`](view-rendering-readability.md)
  epic — that epic makes the typed tree canonical and clean; this one
  composes and captures whatever it renders. Fidelity improves for free as
  that epic lands.
- **Docs on ship:** `docs/architecture/developer-guide.md` §6 (the
  AI-collaborator surfaces), `docs/tui/`, `docs/testing.md`, the new
  skill's `SKILL.md`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Frame seam | tui | Extract one composer that returns the full human screen (body + chrome) as `{text, ansi, metadata}` at any width; the live TUI calls it too | — (soft: view-rendering #1/#2) | Draft | [`qa-frame-seam.md`](qa-frame-seam.md) |
| 2 | `kitsoki drive` | runtime | Interactive headless driver: persistent trace session, free-text input, `--harness live\|replay`, VCR record/playback modes; emits the frame per turn | 1 | Draft | [`qa-drive-command.md`](qa-drive-command.md) |
| 3 | `kitsoki shot` | tui | ANSI→PNG of a frame (monospace + color) for visual review | 1 | Draft | [`qa-screenshot.md`](qa-screenshot.md) |
| 4 | `story-qa` agent | tooling | The Claude subagent/skill: persona + scenario → drive loop → scored UX rubric + report + screenshots + bug list | 2, 3 | Draft | [`qa-agent-skill.md`](qa-agent-skill.md) |

## Sequencing

Slice 1 is the keystone — it defines the `Frame` bundle every other slice
consumes. Once it lands, 2 and 3 are independent and parallel; 4 sits on
top of both.

```
#1 (frame seam) ─┬─▶ #2 (kitsoki drive) ──┐
                 └─▶ #3 (kitsoki shot) ────┴─▶ #4 (story-qa agent)
```

## Shared decisions

These span slices; each child defers here.

1. **The `Frame` is the unit of fidelity.** Defined once in slice 1,
   consumed by 2/3/4. It carries `text` (ANSI-stripped, agent-readable),
   `ansi` (styled, screenshot-ready), and `metadata` (`state`,
   `allowed_intents`, `mode`, `world_digest`, terminal `width`/`height`).
   No consumer re-derives the screen; they all read the `Frame`.
2. **Don't fork the renderer.** The composer renders whatever the
   canonical typed tree + chrome produce today; it does **not** invent a
   second layout path. The `view-rendering-readability` epic owns making
   that tree clean and width-correct. Hand-rolled Go view strings stay
   forbidden (CLAUDE.md / `rendering-tests`).
3. **Cassette tension is explicit.** Free exploration needs a live model —
   a cassette can only answer inputs it already recorded. The canonical
   workflow is therefore **live-explore-while-recording → deterministic
   replay for regression**. View/rendering findings are deterministic and
   replay-safe; objective-achievability findings need live. The agent skill
   (#4) labels every finding with the mode that produced it.
4. **VCR scope = the routing harness, modes only.** Record
   (`harness.RecordingHarness`, `recording.go:38`) and replay
   (`harness.ReplayHarness`, `replay.go:67`) already exist for the routing
   harness; slice 2 adds the *modes* (`once | none | all | new`, after
   python-vcr) and unifies the record/replay file shape. **Cassette
   fidelity for oracle-call outputs** (`converse`/`decide`/`task` bodies,
   not just routing) is the parallel cassette-quality work
   ([`oracle-contract-eval.md`](oracle-contract-eval.md) and its
   companion) — named here as the dependency for *fully* LLM-free QA, not
   re-designed in this epic.

## Cross-cutting open questions

1. **Does the frame need the scrollback, or only the current screen?** A
   human sees prior turns scrolled above the live region. *Lean: the
   `Frame` is the **current** screen (last room body + live chrome); the
   driver's JSONL transcript already preserves the history of frames, so
   the agent has both — per-turn fidelity plus the running log.*
2. **Where does `width`/`height` come from headlessly?** *Lean: `--cols`
   /`--rows` flags on `drive`/`shot`, defaulting to 100×30; the agent
   sweeps a couple of widths (e.g. 80 and 120) to catch reflow bugs.*

## Non-goals

- **A new view renderer.** Owned by `view-rendering-readability`.
- **Oracle-output cassette fidelity** (converse/decide/task bodies). The
  parallel cassette-quality proposal; this epic consumes it.
- **Replacing flow fixtures or `kitsoki test`.** Those stay the
  deterministic correctness gate; this is exploratory UX QA on top.
- **A general terminal-recorder** (asciinema/vhs replacement). `shot` is a
  single-frame still for review, not a session recorder.
```
