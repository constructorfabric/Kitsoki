# Epic: Conversation-driven development (CDD)

**Status:** Draft v1. Methodology + slice sketch for review. Slice 1
(conversation cases from mining) is absorbed by
[`scenario-foundry.md`](scenario-foundry.md) under the `usable-kitsoki`
epic, which ships it against concrete session-mining/harness plumbing
rather than the feature-catalog codegen. Slices 2–4 (mockup demo
binding, corpus expansion loop, build handoff) stand as scoped below;
nothing implemented; cutting the remaining child proposals is still the
first task after this epic is reviewed.
**Kind:**   epic
**Slices:** 4 (0/4 shipped; slice 1 absorbed elsewhere, not cut here)

## Why

kitsoki builds conversation-shaped software — a story *is* a conversation
graph — and the repo just finished the machinery that makes a conversation
**watchable and checkable**: the feature catalog ([`features/`](../../features/CLAUDE.md))
is the single source of truth that code-generates tour manifests, demo
bindings, and vision-QA scenarios, and one make target each records a
tour-narrated MP4 (`make demo-feature`) and gates it against the scenarios
(`make feature-qa`, [`Makefile:221`](../../Makefile)).

But that machinery only points **backwards**. A catalog entry is written
after its feature ships ([`features/chat-stream.yaml`](../../features/chat-stream.yaml)
documents a surface that already exists); new ideas enter as prose
proposals, and the first "can I actually *see* it?" moment comes after
implementation — the most expensive possible place to discover the
conversation is wrong. Meanwhile the evidence about which conversations
matter accumulates with nowhere structured to land: chat-history mining
([`session-idea-mining`](../skills/session-idea-mining/SKILL.md),
[`tools/session-mining/`](../../tools/session-mining/README.md)) ends in a
brief, demo review discussion ends in feedback notes
([`docs/tui/video-review.md`](../tui/video-review.md)), research ends in a
report.

CDD inverts the entry point. For conversation-shaped software the
conversation is the cheapest complete spec: mock a few happy-path
dialogues as catalog entries, render each as a full tour-driven demo video
over **static mockups** (no engine, no LLM), QA that the video honestly
depicts the conversation, and put the watchable product in front of humans
*before* building it. Review, research, and session mining then expand the
corpus as reviewable case diffs — and the corpus drives what gets built.
When implementation ships, the same case re-records against the real
engine with its turns unchanged; the demo that sold the feature becomes
the regression evidence that guards it.

## What changes

Once all four slices ship:

- A capability can enter the repo as a **conversation case** before any
  code exists: a `features/<case>.yaml` with `kind: conversation`,
  `stage: mocked`, a persona + goal, and the dialogue turns (what the
  operator says, what the app observably shows). The existing codegen
  derives the tour-step skeleton and the `qa.scenarios` claims from the
  turns, so the case, its tour, and its QA contract are one artifact that
  cannot drift (`make features-check` already enforces the bijection).
- `make demo-feature FEATURE=<case>` records that conversation as a
  tour-narrated MP4 **with no engine behind it** — the real SPA rendering
  a hand-authored static trace/flow where the surfaces exist, HTML mockup
  scenes (the shipped [`mockup-video` story](../stories/mockup-video.md))
  where they don't. `make feature-qa` gates the video against the case the
  same way it gates live demos today.
- The corpus has a visible funnel — `mocked → demoed → accepted →
  building → live` per case, emitted by `make features-index` — and three
  expansion inputs that each land as **case diffs, reviewed like code**:
  discussion (watching demos, `/review` flag-a-moment feedback notes,
  design-pipeline conversations), research (deep-research of comparable
  products' conversation patterns), and session mining (what users already
  try to say, harvested from real transcripts).
- An `accepted` case pre-fills the dev-story **design pipeline**
  ([`stories/dev-story/README.md`](../../stories/dev-story/README.md), the
  `design*` rooms) — the case *is* the usage section and the acceptance
  criteria — and flows on through decomposition
  ([`work-decomposition.md`](work-decomposition.md)) to implementation.
  Shipping flips the case's `demo:` binding from mockups to
  `story + flow + hostCassette` (the shape
  [`features/chat-stream.yaml:11`](../../features/chat-stream.yaml) uses
  today) with the conversation text untouched — that unchanged-turns
  re-record *is* the acceptance test, and the gated QA on the live binding
  is the regression gate thereafter.

## Impact

- **Spans:** tooling (feature-catalog schema + codegen in
  `tools/runstatus/scripts/features/`, one Playwright recording posture),
  process (the loop, its cadence, skill wiring), story (the handoff into
  dev-story's design pipeline; optionally a `stories/cdd/` process story
  later). No engine, TUI, or trace-format change.
- **Net surface:** a new catalog `kind` + `stage` field + turn vocabulary
  in `features/feature.schema.json` and the codegen; a static-mockup demo
  posture in `tests/playwright/_helpers/` (building on the file://
  snapshot posture in `_helpers/artifact.ts`); funnel output in
  `features-index`; authoring docs in `features/CLAUDE.md`; cross-links
  from the [`kitsoki-ui-demo`](../skills/kitsoki-ui-demo/SKILL.md) /
  [`kitsoki-ui-qa`](../skills/kitsoki-ui-qa/SKILL.md) skills.
- **LLM cost discipline unchanged:** recording stays no-LLM always
  (`docs/web/README.md` → "Deterministic, no-LLM"); the vision-QA gate
  stays gated behind explicit invocation (CLAUDE.md rule), run at stage
  transitions rather than in CI loops.
- **Docs on ship:** `docs/architecture/conversation-driven-development.md`
  (the loop), `features/CLAUDE.md` (case authoring), skill cross-links.

## The loop

The methodology is the seam between all four slices, so it lives here.

```
  1. MOCK ────────▶ 2. DEMO + QA ────────▶ 3. REVIEW ────────▶ 4. BUILD
  a happy-path      tour-narrated MP4      humans watch the    design pipeline →
  conversation      over static mockups    product before it   decomposition →
  case in           (no engine, no LLM);   exists; accept,     implement; re-record
  features/         vision gate: does      or flag moments     with turns UNCHANGED
  stage: mocked     the video depict       that loop back      against flow+cassette;
                    the case?              as case edits       stage: live
       ▲                                        │
       └woven──── EXPAND: discussion · research · session mining ◀──────┘
              (every input lands as a case diff, reviewed like code)
```

1. **Mock.** Author 2–4 happy-path conversation cases per capability area
   — persona, goal, turns. A turn is product language ("operator: *show me
   your teas*" / "shows: *a product carousel with quick-reply buttons*"),
   not engine vocabulary; cases may reference features that don't exist.
2. **Demo + QA.** The codegen derives the tour skeleton and QA claims from
   the turns; the author polishes narration the way
   [`features/chat-stream.yaml`](../../features/chat-stream.yaml) reads
   today. Record over static mockups; run the gated vision QA. For a
   mockup-bound case the gate answers "does this video honestly depict the
   conversation?" — catching both a dishonest demo and an incoherent case.
3. **Review.** Watch the demos with humans. Accepting moves the case to
   `accepted`; objections become `/review` feedback notes (already
   structured and source-targeted, [`docs/tui/video-review.md`](../tui/video-review.md))
   or direct case edits. This is deliberately the cheapest place to be
   wrong: a rejected conversation costs a YAML file and a re-record, not a
   build.
4. **Build.** The accepted case enters the design pipeline as a
   pre-validated idea (its turns are the usage evidence the
   `idea-completeness` gate asks for), gets decomposed, and ships.
   Flipping the demo binding to the real engine with unchanged turns
   closes the case; its demo and QA scenarios live on as the feature's
   regression evidence.

**Expand** runs continuously against the corpus, not as a phase: session
mining surfaces the conversations users already attempt
([`session-idea-mining`](../skills/session-idea-mining/SKILL.md) for "what
have we said about X", [`tools/session-mining/`](../../tools/session-mining/README.md)
for recurring patterns worth a story); research (the deep-research skill)
imports comparable products' conversation conventions; discussion mines
the demos themselves. Each produces **proposed case diffs** — new cases,
new turns, persona/edge variants — never free-floating prose.

## Slices

Not yet cut into child files; one-line scopes below are the cutting guide.

| # | Slice | Kind | Scope (one line) | Depends on | Status |
|---|---|---|---|---|---|
| 1 | Conversation cases in the catalog | tooling | Absorbed by [`scenario-foundry.md`](scenario-foundry.md) (`usable-kitsoki` epic) — mined `kind: conversation` scenarios compiled from real sessions, not catalog codegen | — | Absorbed |
| 2 | Mockup demo binding | tooling | Record a case with no engine: static-trace posture into the real SPA (extend `_helpers/artifact.ts`), HTML mockup scenes via the shipped `mockup-video` story where surfaces don't exist; same `demo-feature` / `feature-qa` rails | 1 | Not cut |
| 3 | Corpus expansion loop | process | Wire mining / research / `/review` flags to emit case diffs; cadence + review discipline; seed corpus authored | 1 (2 for flag-on-demo) | Not cut |
| 4 | Conversation→build handoff | story / process | `accepted` case pre-fills the design pipeline intake; ship flips the binding with turns unchanged; live QA becomes the regression gate | 1 | Not cut |

## Sequencing

```
#1 (catalog substrate) ──▶ #2 (mockup demos) ──▶ #3 (expansion loop)
                                            └──▶ #4 (build handoff)
```

#1 is the substrate everything reads. #2 makes a `stage: mocked` case
watchable — the loop is real from that moment. #3 and #4 are independent
once #2 exists and can land in either order; the seed corpus (#3) should
include at least one case that #4 then carries through to `live`, proving
the full loop end-to-end.

## Shared decisions

1. **The catalog is the corpus.** Conversation cases are `features/`
   entries, not a parallel tree — the catalog is already the single source
   of truth with codegen, schema validation, and staleness checks
   ([`features/CLAUDE.md`](../../features/CLAUDE.md)); CDD extends it to
   cover capabilities that don't exist yet rather than inventing a second
   registry that would drift from it.
2. **Conversation text is binding-independent.** Flipping a case from
   mockup-bound to engine-bound must not edit its turns. If the shipped
   product can't hold the mocked conversation, that's a finding (fix the
   product or formally amend the case in review) — never a silent rewrite.
   This is what makes the mocked demo an acceptance contract.
3. **Mock at the data seam when possible.** Prefer feeding the *real* SPA
   a hand-authored trace/flow (highest fidelity; the binding flip is then
   data-only) over hand-built HTML mockups; use the
   [`mockup-video` story](../stories/mockup-video.md) only where the UI
   surface itself doesn't exist yet. Both bind through the same `demo:`
   section.
4. **Expansion lands as case diffs.** Mining briefs, research reports, and
   feedback notes are inputs, not outputs — each ends in a proposed edit
   to the corpus, reviewed like code. No standing prose documents that
   restate the corpus.
5. **QA runs at stage transitions, not in loops.** Recording is free and
   deterministic; the vision gate drives the real `claude` CLI and stays
   gated (CLAUDE.md). Gate on `mocked → demoed` and on the binding flip to
   `live`; `features-check` (schema + staleness, no LLM) covers every
   build in between.

## Cross-cutting open questions

1. **Case shape: new `kind: conversation`, or a `conversation:` block on
   feature entries?** A case can span several features and a feature
   appears in several cases (happy path, error path, personas) — and a
   case must be able to exist before any feature entry does. *Lean: new
   kind with `features:` back-links; the existing `related:` mechanism
   already models the cross-linking.*
2. **Turn vocabulary.** Minimal and prose-first (`operator:` / `shows:` /
   `agent:` / `notes:`) vs. reusing the flow-fixture intent shape. *Lean:
   minimal — cases speak product language; flows speak engine language
   (intents + host stubs). Slice 1 defines the case→flow mapping used when
   a case graduates to a live binding, rather than forcing engine
   vocabulary on un-built conversations.*
3. **Where mockup fixtures live.** A `features/mockups/<case>/` subfolder
   vs. alongside the eventual story. *Lean: under `features/` while
   mockup-bound, deleted on the flip to `live` — mockups are scaffolding,
   not product.*
4. **Seed corpus.** *Lean: one app-on-kitsoki case (the LINE store
   browse→cart→checkout conversation already sketched in
   [`line-commerce-stories.md`](line-commerce-stories.md)) plus one
   kitsoki-product case — proving the loop works for both things kitsoki
   develops: itself, and apps built on it.*
5. **Relationship to [`lifecycle-taxonomy.md`](lifecycle-taxonomy.md).**
   Its Feature acceptance criteria + TestSpec (scenario → harness +
   fixture + evidence) objects are the natural eventual home for cases and
   their QA bindings. *Lean: don't block on it — ride the shipped
   `features/` catalog now; if the taxonomy ships, cases map onto its
   objects in that proposal's migration, and the `stage:` field folds into
   Feature `status`.*

## Non-goals

- **Real-LLM demo recordings.** Never — mockups and cassettes only; the
  no-LLM recording rule holds for CDD exactly as for shipped-feature
  demos.
- **A new video producer, player, or review surface.** CDD composes the
  shipped pieces: the demo skill's recorder, the `mockup-video` story, the
  media seam, the `/review` panel, the QA gate.
- **Auto-implementation from conversations.** Build still flows through
  the design pipeline and decomposition with humans at the gates; the case
  changes what enters that pipeline, not the pipeline.
- **General usability auditing.** [`kitsoki-ui-review`](../skills/kitsoki-ui-review/SKILL.md)
  (heuristic audit) and the [`story-qa`](../stories/story-qa.md) skill
  (TUI persona walks) keep their roles; CDD's gate asks only "does the
  product hold this conversation."
- **Replacing proposals.** Cases specify *conversations*; engine/runtime
  changes still get argued in proposals. A case is often the evidence a
  proposal cites.
