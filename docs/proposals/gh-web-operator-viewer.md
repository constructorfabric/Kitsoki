# TUI: Web viewer — artifacts + operator drive

**Status:** Draft v1. Nothing implemented yet — confirmed on a re-audit
alongside the shipped `docs/architecture/github-agent.md` dispatch write-up: no artifact gallery and no
GitHub-OAuth-gated operator-drive surface exist. The gh-agent job list
(`/runs`, `/run/<job-id>`) is a plain HTML table with no auth, no media
rendering, and no drive affordance — it is the run-link target this slice
would build *on top of*, not an implementation of it.
**Kind:**   tui
**Epic:**   kitsoki-github-agent.md

> This is a "tui" proposal for the **web** operator surface — the runstatus
> Vue 3 SPA under `tools/runstatus/`, not the terminal TUI under `internal/tui/`.
> The kind's two hard rules carry over with one substitution: rendering stays
> **data-driven through typed-view elements + Vue components** (no hand-rolled
> markup in `*.vue` that bypasses `ViewElement.vue`), and every surface touching
> the live SSE stream gets a **Playwright** test that captures the combined
> RPC-result-plus-rendered-DOM state — the web analogue of the template's
> CapturedIO combined-I/O rule. Where the terminal template says "pongo2", read
> "Vue component + the JSON-RPC method it reads".

## Why

A GitHub collaborator mentions `@kitsoki`, gets back a comment with a run link
(epic shared decision #3, minted by slice #2), clicks it — and lands on a SPA
that can show them the *trace* but nothing else they came for. Two gaps:

- **No artifact browser.** The run produced screenshots, a deck, a PRD, a
  rendered video — and the SPA has no way to list or open them. The only media
  path today is `ViewElement.vue` rendering a `media` element *inside a chat
  bubble* when a room happens to emit one (`tools/runstatus/src/components/ViewElement.vue:48`),
  served by-handle from `GET /artifact/<id>` (`internal/runstatus/server/server.go:508`).
  There is no gallery that enumerates everything a run produced.
- **No way to steer.** The remote viewer is a spectator. The operator-ask
  bridge (`docs/architecture/operator-ask.md`) already lets a *local* operator
  answer an agent's question, submit input, and pick an intent through the
  SPA — but on a hosted run reached from GitHub, none of those controls are
  authorized or wired to a GitHub identity, and nothing the remote viewer does
  is mirrored back to the thread that asked for the work.

This slice closes both: an artifact gallery, and an authorized GitHub user
driving the conversation with kitsoki acking each driven action on the thread.

## What changes

Add three regions to the existing SPA, all additive: an **artifact gallery**
listing and inline-rendering a run's media, a **GitHub-thread header** showing
"this run came from issue/PR #N" with a link, and an **operator-drive** path
that lets an OAuth-authorized GitHub user answer-ask / submit / pick-intent
against the hosted run — each driven action recording the acting GitHub login
and posting an ack back to the originating thread via the slice-#1 comment
substrate.

One sentence: **turn the hosted trace viewer into a driveable cockpit for the
GitHub user who summoned the run, gallery included, with every action
attributed and echoed to the thread.**

## Impact

- **Code:** `tools/runstatus/src/components/` — new `ArtifactGallery.vue` +
  `ArtifactCard.vue`, new `GitHubThreadHeader.vue`; reuse of `InputBar.vue`,
  `OperatorQuestionModal.vue`, and `ViewElement.vue`'s media substrate.
- **Rendering:** new gallery + header components reading new/existing JSON-RPC
  methods; the chat-bubble `media` rendering and the trace timeline are
  unchanged.
- **RPC:** one new read method `runstatus.run.artifacts` (the gallery's index,
  fed by the shared artifact-job substrate) and one new `runstatus.run.origin` (the GitHub thread
  linkage); the drive controls reuse `runstatus.session.submit` /
  `runstatus.session.continue` / `runstatus.session.answer_question`, gated by
  the OAuth identity already injected as `slots.author`.
- **Input:** the three drive controls (answer-ask, submit, pick-intent), gated
  on OAuth write-access; an ack posted to GitHub per driven action.
- **Docs on ship:** `docs/tui/web-ui.md` (a new "Hosted run: artifacts +
  operator drive" section).

## Mental model

The hosted run is a **shared cockpit, not a recording**. The trace timeline and
state diagram are the instruments; the artifact gallery is the photo roll of
everything the run produced; the drive controls are the yoke. The yoke is
**locked** unless the person holding it is a GitHub user with write access to
the originating repo — and every time they pull it, kitsoki announces the move
back on the thread, in its single voice (epic shared decision #4), so the
GitHub conversation and the web cockpit never silently diverge.

## Layout

```
Before (current SPA):                       After (+gallery +header +drive):
┌──────────────────────────────────┐        ┌──────────────────────────────────┐
│ session header                   │        │ ⎇ from issue #142 · repo/x  ↗ ──┐│  ← GitHubThreadHeader
├───────────────┬──────────────────┤        ├───────────────┬──────────────────┤
│ state diagram │  trace timeline  │        │ state diagram │  trace timeline  │
│  (mermaid)    │  (virtualized)   │        │  (mermaid)    │  (virtualized)   │
│               │                  │        │               │                  │
│               │  ┌────────────┐  │        │               │  ┌────────────┐  │
│               │  │detail draw.│  │        │               │  │detail draw.│  │
│               │  └────────────┘  │        ├───────────────┴──────────────────┤
├───────────────┴──────────────────┤        │ Artifacts  [img][img][▶vid][pdf] │  ← ArtifactGallery
│ chat transcript                  │        │            [deck ▦]              │
│ > input bar  (local only)        │        ├──────────────────────────────────┤
└──────────────────────────────────┘        │ chat transcript                  │
                                             │ > input bar / answer-ask modal   │  ← drive surface,
                                             │   (enabled iff OAuth write-access)│    auth-gated
                                             └──────────────────────────────────┘
```

The header is a thin always-present strip above the existing two-pane body. The
gallery is a new horizontal strip between the trace pane and the transcript. The
drive surface is the *existing* transcript + input bar, now reachable on a
hosted run and gated by identity rather than hidden.

## Rendering changes

Data-driven through Vue components reading typed RPC results — no hand-rolled
markup, mirroring the template's "layout is data, not printf" rule.

**New components**

- `GitHubThreadHeader.vue` — reads `runstatus.run.origin` →
  `{kind: "issue"|"pull", number, repo, url, title}` and renders the linkage
  strip ("from issue #142 · repo/x ↗"). Absent origin (a local run) → the strip
  does not render, so the component is inert outside the GitHub loop.
- `ArtifactGallery.vue` — reads `runstatus.run.artifacts` → an ordered list of
  `{handle, mime, label, kind, poster?}` rows from the shared artifact-job
  trace/artifact service (`artifact-driven-stories.md` /
  `trace-artifact-service.md` — hosted index state in Postgres, blobs on the
  filesystem, served by-handle unchanged). Lays out one `ArtifactCard.vue`
  per row.
- `ArtifactCard.vue` — renders one artifact inline by MIME, **reusing the exact
  by-handle substrate `ViewElement.vue` already uses** (`src=/artifact/<handle>`,
  `internal/runstatus/server/server.go:508`): `image/*` → `<img loading="lazy">`,
  `video/*` → `<video controls>` with `poster=/artifact/<handle>/poster`
  (`internal/runstatus/server/server.go:1981`), `application/pdf` → an embedded
  PDF viewer, `kind: "slideshow"` → the slidey deck substrate `ViewElement.vue`
  already maps a `slideshow` media kind to (`tools/runstatus/src/components/ViewElement.vue:70`).
  The MIME→substrate switch is **factored out of `ViewElement.vue`** into a
  shared composable so the gallery and the chat bubble render media identically
  — one authority for "how kitsoki shows a video", per `docs/AGENTS.md`.

**What stays unchanged**

- `StateDiagram.vue` (mermaid), `TraceTimeline.vue` (virtualized), the detail
  drawer (`EventDetail.vue` / `HostBuiltinDetail.vue` / `HostCliDetail.vue`),
  and `ChatTranscript.vue` — untouched.
- The three distinct thinking/tool surfaces (the live chat bubble, the Agent
  Actions drawer, the MetaOverlay) are untouched; the gallery is a *fourth*
  region and must not be confused with them (memory:
  web-ui-two-thinking-surfaces). The artifact gallery is the run's *outputs*,
  not its *thinking*.

## Input & commands

The drive surface is the existing transcript controls, now reachable on a
hosted run and **authorization-gated**. There are no slash commands; the web
analogue is the three operator actions, each routed through the same JSON-RPC
the local web UI uses and each carrying the acting GitHub identity.

| Control | RPC | Records identity | GitHub ack |
|---|---|---|---|
| Answer an operator-ask question | `runstatus.session.answer_question {session_id, question_id, answers}` (`internal/runstatus/server/operator_questions.go:277`) | yes | "@login answered: …" |
| Submit free-text / send | `runstatus.session.submit` (`internal/runstatus/server/server.go:873`) | yes (`slots.author`) | "@login replied: …" |
| Pick an intent (form/quick-action) | `runstatus.session.submit` / `runstatus.session.continue` with the intent slots emitted by `InputBar.vue:445` | yes | "@login chose: …" |

**Auth gate.** Per epic shared decision #1, a hosted run's drive controls are
authorized by GitHub OAuth identity. **Round 1 restricts driving to the
issue/PR author (the owner) only** — the OAuth login must match the originating
issue/PR author. Repo-write collaborators are the **round-2** target and stay
locked in v1. The server already resolves an operator identity and injects it as
`slots.author` before driving (`internal/runstatus/server/identity_test.go:21`);
this slice replaces the default-actor source with the **OAuth login** on a
hosted run, and **refuses** submit/continue/answer when the login is not the
issue/PR owner (returns an RPC error; the SPA renders the input bar disabled with
a "sign in with GitHub to drive this run" affordance). Everyone else — read-access
spectators and write-access collaborators alike, in round 1 — sees the gallery +
trace + header but a locked yoke.

**Ack back to GitHub.** On a successful driven action the server emits an
*intent to speak* consumed by the **slice-#1 comment substrate** (epic shared
decision #4) — kitsoki posts a one-line ack attributed to `@login` to the
single rolling status comment on the originating issue/PR. This slice does not
format or dedup the comment; it hands the substrate the event and the resolved
identity. The substrate owns the edit-vs-new-comment policy.

## Rendering tests

Playwright specs under `tools/runstatus/tests/playwright/`, each driving a real
`kitsoki web` through the no-LLM replay harness (`--harness replay --recording`
+ `--host-cassette`, per the `kitsoki-ui-demo` convention and CLAUDE.md — no
real LLM, no real GitHub). Each is the web analogue of the template's
combined-I/O rule: it asserts on **the rendered DOM after the SSE settle**, not
on the RPC result alone. Critically, **observer/trace rows lag the trace RPC by
one ~500 ms SSE tick** (memory: web-observer-sse-tick-vs-rpc-settle), and
expanded rows swallow center-clicks — so specs settle on the SSE-delivered DOM
and click `.trace-timeline__row-main`, never the raw RPC return.

- `gh-artifact-gallery.spec.ts` — serves a run whose `runstatus.run.artifacts`
  index lists an image, a video, a PDF, and a slidey deck; asserts one
  `ArtifactCard` per row, that each `src` resolves to `/artifact/<handle>` with
  a **200 + correct MIME** at the network layer (mirroring `media-artifact.spec.ts`'s
  `artifactImageOk`/`artifactVideoOk` assertions), and that the video card's
  `poster` hits `/artifact/<handle>/poster`. Fails without the gallery (no cards
  render) or without the shared artifact index (index 404).
- `gh-thread-header.spec.ts` — asserts `GitHubThreadHeader.vue` renders the
  "from issue #N" strip with the right repo/number/link from
  `runstatus.run.origin`, and that on a run **without** origin the strip is
  absent (the component is inert locally).
- `gh-operator-drive-authorized.spec.ts` — with an OAuth identity that **is the
  originating issue/PR author**, drives all three controls: opens an
  operator-ask question via the SSE feed and answers it through
  `OperatorQuestionModal.vue` (settling on the SSE tick, not the answer RPC
  return), submits free text, and picks an intent; asserts each driven action
  carried `slots.author = <login>` (via the capture-driver pattern,
  `internal/runstatus/server/identity_test.go`) and that a GitHub-ack intent was
  emitted to the (cassetted) comment substrate.
- `gh-operator-drive-denied.spec.ts` — with a **non-owner** identity (a
  read-access spectator, an absent identity, **and a write-access collaborator —
  who is round-2, locked in v1**), asserts the input bar and answer modal are
  **disabled**, that a forced submit/answer RPC returns an auth error, and that
  **no** GitHub ack is emitted. The negative twin of the authorized spec; fails
  if the gate is missing (a non-owner could drive).

Each spec is confirmed to FAIL without its change (gallery absent → no cards;
gate absent → spectator drives; header missing → no linkage strip).

## Migration plan

Purely additive to the shipped SPA — no surface is replaced or removed.

- The gallery, header, and OAuth-gated drive path render only when their RPC
  returns data; a local `kitsoki web` run (no origin, default-actor identity,
  no artifact index) renders exactly as today. There is no cutover and no
  parallel-run window — the new regions are dark until a hosted GitHub run
  populates them.
- The shared media composable is extracted from `ViewElement.vue` first and
  proven equivalent (the existing `media-artifact.spec.ts` must still pass
  unchanged) before the gallery consumes it.

## Tasks

```
## 1. Render
- [ ] 1.1 Extract MIME→media-substrate switch from ViewElement.vue into a shared composable; media-artifact.spec.ts still green
- [ ] 1.2 ArtifactGallery.vue + ArtifactCard.vue, reading runstatus.run.artifacts (shared artifact-job index)
- [ ] 1.3 GitHubThreadHeader.vue, reading runstatus.run.origin
- [ ] 1.4 Wire both new regions into the SPA layout (additive)

## 2. Drive
- [ ] 2.1 OAuth identity → slots.author source on a hosted run; write-access gate on submit/continue/answer_question
- [ ] 2.2 Enable/disable the input bar + answer modal on the resolved identity
- [ ] 2.3 Emit per-action GitHub-ack intent to the slice-#1 comment substrate (acting login attached)

## 3. Prove + document
- [ ] 3.1 Playwright specs (replay harness, no LLM/GitHub; verified to fail without the change; SSE-tick settle respected)
- [ ] 3.2 Manual hosted-run pass against a cassetted run; screenshot the new cockpit
- [ ] 3.3 Add the "Hosted run: artifacts + operator drive" section to docs/tui/web-ui.md; trim/delete this proposal
```

## What we lose, honestly

- **The viewer stops being purely passive.** A hosted trace link was a
  read-only artifact anyone with the URL could open; once it can drive a live
  session, the URL's blast radius is gated entirely on the OAuth check being
  correct. A bug in the write-access gate is now an *action* bug, not just a
  disclosure bug — hence the explicit `gh-operator-drive-denied.spec.ts`.
- **Two media paths must stay in lockstep.** The chat-bubble media and the
  gallery now share a composable; the extraction adds a seam that, if it ever
  drifts, makes a video render two different ways. The cost of the
  one-authority refactor is that `ViewElement.vue` no longer owns its media
  rendering outright.
- **The thread can get chattier.** Every driven action wants to ack on GitHub.
  We lean entirely on slice #1's single-rolling-comment dedup to keep that from
  becoming a flood; this slice has no independent throttle and would flood
  without it.

## Open questions

1. **One ack per action vs. a coalesced "operator drove N times" summary.**
   Per-action acks are clearest but noisiest; a coalesced summary is quieter but
   loses the play-by-play. *Lean: per-action intent emitted, let the slice-#1
   substrate coalesce — keep the throttle in one place.*
2. **Gallery population — live vs. on-complete.** Does the gallery stream
   artifacts as the run produces them (extra SSE wiring) or list them from the
   index once available? *Lean: poll/refresh `runstatus.run.artifacts` on the
   existing SSE tick; no new stream channel for v1.*
3. **Spectator visibility of the gallery.** Should a read-only viewer see the
   artifact gallery at all, or only write-access drivers? *Lean: gallery +
   trace + header visible to any repo-read identity; only the yoke is
   write-gated.*

## Non-goals

- **The serving backend.** The run/artifact index and the by-handle blob serving
  the gallery consumes are owned by the shared artifact-job epic
  (`trace-artifact-service.md`); this slice only *renders* them.
- **Collaborator drive.** Round 1 authorizes the issue/PR **owner** only;
  extending drive to repo-write collaborators is **round 2**, out of scope here.
- **The auth mechanism itself.** GitHub-App OAuth, installation tokens, and the
  comment substrate are **slice #1** (`gh-event-ingress.md`); this slice
  *consumes* the resolved identity and the substrate, and does not design either.
- **The demo video.** The tour over the GitHub↔kitsoki loop is **slice #5**
  (`kitsoki-github-demo.md`).
- **The terminal TUI.** No `internal/tui/` changes; the terminal operator
  reaches a hosted run through the existing web link.
