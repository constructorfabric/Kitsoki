# TUI: One-Click "Report bug" from the Meta menu

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tui (web operator surface) — carries one **runtime** seam (the
capture/anonymize/write RPC + the artifacts-folder convention), called out
under Impact. Single focused proposal; if the anonymizer grows its own
ruleset surface, split it into a `runtime` child.
**Epic:**   — standalone

## Why

Filing a useful bug against a running web session today is all friction. The
operator either (a) opens the existing agentic `kitsoki.bug` / `story.bug`
meta modes (`internal/app/builtin_meta_modes.go:71-109`) and *describes* the
problem in prose, or (b) drops to a shell and runs `kitsoki bug create`
(`cmd/kitsoki/bug.go:86`). Neither captures **what was actually on screen or
on the wire** — the screenshot and the network trace that turn "it broke" into
a reproducible report. By the time anyone looks, the session is gone.

The web UI is the one surface where that evidence is cheap to grab: the page
is rendered in a browser, and the runstatus server mediates every RPC/SSE call
the app makes. We should let the operator file a complete, evidence-backed bug
in one click.

## What changes

Add a **"Report bug"** item to the Meta dropdown
(`tools/runstatus/src/components/meta/MetaButton.vue`, the `uiModes` `v-for`).
Clicking it captures three evidence artifacts — a **screenshot** (`html-to-image`),
a short **rolling DOM/session recording** (`rrweb`), and the recent **network
trace** (HAR) — and posts them to a new backend RPC that anonymizes the HAR and
files a bug under `issues/bugs/`, with the artifacts colocated in a per-ticket
artifacts folder. One sentence: **the Meta menu can now file a screenshot +
session recording + anonymized HAR bug without leaving the page.**

The split of *where* each artifact is produced follows one rule — **don't ask
the client to do work the server already does faithfully.** The HAR comes from
the server, which mediates every RPC/SSE call and therefore sees request/response
*bodies* that no in-page JavaScript can. Only the two genuinely browser-bound
captures (the rendered screenshot and the DOM mutation stream) run client-side,
and each is scrubbed *at capture* so nothing sensitive is ever serialized.

This is distinct from the existing `*.bug` *agentic* modes — those open a chat
overlay for a conversation. "Report bug" is a deterministic capture-and-file
action, not a conversation: it files a raw evidence packet and returns. Triage
(dedup, severity, enrichment) is handled **async** afterward by the existing
dogfood pipeline — no agent hand-off in the click path.

## Impact

- **Code (web):** `tools/runstatus/src/components/meta/MetaButton.vue` (new
  menu item, capture orchestration); `tools/runstatus/src/data/live-source.ts`
  (new `reportBug` RPC client, alongside `metaStream`); a small capture helper
  module that drives the screenshot (visible-DOM scrub → `html-to-image`) and
  the rrweb rolling buffer, and bundles them with the capture token.
- **New web deps:** `html-to-image` and `rrweb` (both MIT). No existing
  screenshot/recording dep in `tools/runstatus/package.json` today.
- **Code (backend, runtime seam):** new RPC `runstatus.bug.report` in
  `internal/runstatus/server/server.go` (registered near the `runstatus.meta.*`
  handlers, ~line 845); a HAR ring-buffer recorder wrapping the RPC/SSE
  transport; a deterministic anonymizer package; the file-writer that reuses /
  extends `kitsoki bug create` (`cmd/kitsoki/bug.go`).
- **Issues convention:** a new **per-ticket artifacts folder** alongside the
  flat `issues/bugs/<id>.md` — documented in `issues/README.md` and
  `docs/stories/bugs.md`.
- **Docs on ship:** `docs/tui/web-ui.md` (the Meta-menu surface) and
  `issues/README.md` (artifacts-folder layout).

## Mental model

The Meta menu is the operator's "I want to do something *about* this session"
drawer. "Report bug" is the camera shutter: it freezes the page and the recent
wire traffic, scrubs anything sensitive, and drops a labelled evidence packet
into the shared backlog — no typing required, no context lost.

## Layout

```
Meta ▾ (bottom-right)
┌──────────────────────────┐
│ ✦ Edit story             │
│ ✦ Story Q&A              │
│ ✦ Kitsoki help           │
│ ──────────────────────── │  ← divider
│ 🐞 Report bug            │  ← NEW
└──────────────────────────┘

After click (inline, non-modal toast in the launcher):
┌──────────────────────────┐
│ Capturing… screenshot ✓  │
│            HAR ✓ scrub ✓  │
│ Filed issues/bugs/        │
│   2026-…-web-….md  [open] │
└──────────────────────────┘
```

The item is hidden in snapshot/artifact (read-only) mode, exactly like the
existing Meta button hide logic (`MetaButton.vue`).

## Rendering changes

The new menu item is a list entry rendered by the same `v-for` over menu items
in `MetaButton.vue:44-63` — no hand-rolled markup, it follows the existing item
shape. The progress/result toast is a small typed status block driven by the
capture state machine (`idle → capturing → scrubbing → filed | error`), not ad
hoc strings. Everything else in the launcher is unchanged.

## Input & commands

| Command / key | Does | Notes |
|---|---|---|
| Meta ▾ → **Report bug** | Capture screenshot + rrweb session + HAR, anonymize, file ticket | Hidden in read-only/snapshot mode |
| `[open]` on result toast | Reveal the filed `issues/bugs/<id>.md` | Best-effort; copies path if no reveal host |

Capture pipeline (client captures visual evidence → server attaches + anonymizes
HAR → server files):

1. **Screenshot** — client-side, `html-to-image` over the app root, producing a
   PNG of the rendered DOM (no permission prompt). `html-to-image` over
   `html2canvas`: actively maintained, faster, better fonts/SVG/TypeScript;
   `html2canvas` is slow and `dom-to-image` is unmaintained.
   **Before rasterizing, scrub the *visible* DOM:** walk for sensitive nodes
   (the same selectors rrweb masks — `input[type=password]`, `.cc-number`,
   `[data-bug-redact]` — plus value-shaped heuristics) and overlay/mask them, so
   the screenshot can't accidentally bake in a secret that's on screen. The
   raster is the one artifact a reviewer reads at a glance, so it must be clean
   by construction, not by after-the-fact redaction.
2. **Session recording** — client-side, `rrweb`. A bounded rolling buffer (last
   ~30s of DOM mutations + the opening full snapshot) records *how the operator
   reached the broken state*, not just the final frame. rrweb's built-in privacy
   config does the masking **at record time** (block selectors, mask text, ignore
   subtrees via `data-rrweb-ignore`) — form values never enter the buffer. This
   is the rrweb-standard engine under Sentry/PostHog/OpenReplay, and the rendered
   screenshot of step 1 can be reproduced from any replay frame.
3. **HAR** — **server-side ring buffer** (authoritative). The runstatus server
   already mediates every `/rpc`, `/rpc/turn-stream`, and `/rpc/meta-stream` call
   (`internal/runstatus/server/server.go`); a bounded recorder keeps the last N
   request/response pairs and serializes them as a HAR 1.2 archive on demand.
   This sees request/response *bodies* that no page JS can, and requires zero
   client instrumentation — the client sends only a capture token; the server
   attaches the HAR. *Optional augmentation:* if the page is running where
   `devtools.network.getHAR()` is available (a devtools/extension context), the
   client may attach that full-browser HAR alongside the server's; in a plain
   page it simply isn't present and is skipped. The server ring buffer is the
   floor, `getHAR()` is a bonus when it happens to exist.
4. **Anonymize** — deterministic, server-side, over the HAR before anything is
   written: strip `Authorization`/`Cookie`/`Set-Cookie` headers and known
   session-token query params; redact absolute paths under `$HOME`; redact
   configured secret-shaped values. Ruleset lives in one package, unit-tested
   against fixture HARs. (The screenshot and rrweb stream are already scrubbed at
   capture — step 1's DOM scan and step 2's rrweb masking — so the server only
   anonymizes the HAR it produced.)
5. **File** — `runstatus.bug.report` writes `issues/bugs/<id>.md` (reusing the
   `kitsoki bug create` body/frontmatter logic, `cmd/kitsoki/bug.go:154-190`)
   plus a sibling artifacts folder containing `screenshot.png`, `session.rrweb.json`,
   and `har.json`, and links them from the ticket body. Returns `{id, path}`.

### Per-ticket artifacts folder

Tickets are flat `issues/bugs/<id>.md` today; `host.local_files.ticket` and the
dogfood reader glob `*.md` (`issues/README.md`). To keep that discovery intact,
artifacts go in a **sibling** folder, not a folder-form ticket:

```
issues/bugs/
├── 2026-06-12T…Z-web-….md          ← still a flat .md, still globbed
└── 2026-06-12T…Z-web-….artifacts/  ← NEW, ignored by the *.md glob
    ├── screenshot.png        ← html-to-image, visible-DOM scrubbed
    ├── session.rrweb.json    ← rrweb rolling buffer, masked at record time
    └── har.json              ← server ring buffer, anonymized
```

The ticket body gets a `## Artifacts` section linking the two files relatively.
*Lean:* sibling `<id>.artifacts/` over a `<id>/` folder so the existing `*.md`
ticket reader needs zero changes (verify against `host.local_files.ticket`).

**The ticket and its artifacts are committed to the repo** for now — same as the
hand-filed seeds already in `issues/bugs/`. This is the deliberate v1 stance
(evidence is in-repo, immediately searchable by the dogfood app); it raises the
anonymization stakes (§ "What we lose" + Open question §3), to be revisited
before kitsoki is released widely.

## Rendering tests

The web surface is Vue, not the Go TUI, so the concurrent-I/O rendering-test
rule doesn't bind here. Coverage instead:

- **Component test** (`tools/runstatus/`, mirroring the
  `OperatorQuestionModal` test from commit d035b42) — Report-bug item renders,
  is hidden in read-only mode, and drives the capture state machine to a filed
  result against a mocked `reportBug` RPC.
- **Backend test** — `runstatus.bug.report` writes the `.md` + `.artifacts/`
  pair and returns the id; **anonymizer test** asserts auth headers, cookies,
  and `$HOME` paths are scrubbed from a fixture HAR (deterministic, no LLM, no
  network — per CLAUDE.md).
- **Playwright** — extend an existing spec (e.g. `meta-mode.spec.ts`) to click
  Report bug against a throwaway repo and assert the ticket + artifacts land.

## Tasks

```
## 1. Backend seam
- [ ] 1.1 HAR ring-buffer recorder over the RPC/SSE transport (bounded)
- [ ] 1.2 Anonymizer package + fixture-HAR unit tests (auth/cookie/$HOME/secrets)
- [ ] 1.3 `runstatus.bug.report` RPC: write <id>.md (reuse bug.go) + <id>.artifacts/
- [ ] 1.4 issues/README.md + docs/stories/bugs.md: artifacts-folder convention

## 2. Web surface
- [ ] 2.1 "Report bug" menu item in MetaButton.vue (hidden in read-only)
- [ ] 2.2 rrweb rolling-buffer recorder started at app mount (masked, bounded ~30s)
- [ ] 2.3 Screenshot: visible-DOM scrub → html-to-image; capture state machine + result toast
- [ ] 2.4 Opportunistic devtools.network.getHAR() attach when present
- [ ] 2.5 reportBug RPC client in live-source.ts (bundles screenshot + rrweb + token)

## 3. Prove + document
- [ ] 3.1 Component test (renders/hidden/files) + backend + anonymizer tests
- [ ] 3.2 Playwright: click → ticket + artifacts land in a throwaway repo
- [ ] 3.3 Manual run; screenshot the new surface
- [ ] 3.4 Migrate the Meta-menu surface into docs/tui/web-ui.md; delete this proposal
```

## What we lose, honestly

- **Screenshot fidelity.** `html-to-image` rasterizes the DOM, not the real
  compositor output — CSS it doesn't fully support (some filters, cross-origin
  images, canvas/video) renders imperfectly. It's "good enough to see what the
  operator saw," not a pixel-perfect capture. `getDisplayMedia` would be exact
  but prompts the user and can capture the wrong window. The rrweb recording
  partly compensates: a reviewer can replay the real DOM if the raster is off.
- **Always-on recording cost.** The rrweb rolling buffer must be *running* from
  app mount to have anything at click time. That's a continuous (small) cost on
  every session, masked-by-default, bounded to ~30s of events. It's off in
  read-only/snapshot mode (where Report bug is hidden anyway).
- **HAR completeness.** Both the server ring buffer and the rrweb buffer are
  bounded; a bug whose cause scrolled out of the window won't be captured. We
  log the buffer depth/horizon in the archive so reviewers know the limit.
- **Anonymization is best-effort.** Client masking (rrweb config + the visible-
  DOM scan) catches the configured shapes; the server HAR scrub catches known
  header/path/secret shapes; a novel secret format can still leak. Artifacts are
  committed to the repo, so this is a real risk — §3 below.

## Open questions

1. **Screenshot mechanism:** `html-to-image` (no prompt, approximate) vs.
   `getDisplayMedia` (exact, prompts, can grab wrong surface). *Decided:*
   `html-to-image` for v1, backed by the rrweb replay for fidelity; revisit
   `getDisplayMedia` only if complaints arise.
2. **HAR source.** *Decided:* **server ring buffer** is the authoritative source
   — it sees bodies and asks nothing of the client. `devtools.network.getHAR()`
   is attached opportunistically *only when present* (devtools/extension
   context); it is never required, and the feature is whole without it.
3. **Anonymization hardening (pre-wide-release, not a v1 blocker).** Since
   artifacts are committed (decided: in-repo for now), the redaction ruleset is
   the only thing standing between a missed secret and git history. Before wide
   release we likely want either an operator review-before-file step or a
   stricter allowlist-shaped scrub. v1 ships the deterministic ruleset + a
   "review HAR" link in the result toast; the hardening lands before release.

**Resolved:** triage is async with no agent hand-off in the click path; tickets
and artifacts are committed to the repo for now (to evolve before wide release);
HAR is captured server-side (authoritative, sees bodies, no client work) with
`getHAR()` as an opportunistic bonus; visual evidence is client-side rrweb +
`html-to-image`, each scrubbed at capture.

## Non-goals

- The agentic `story.bug` / `kitsoki.bug` conversation modes — unchanged; this
  sits beside them.
- Bug triage, dedup, or resolution workflow (the dogfood pipeline already owns
  that via `.kitsoki/stories/kitsoki-dev/`).
- Capturing artifacts from the Go TUI or from `kitsoki run` — web-surface only.
- A general client-side network-capture/devtools panel — we only assemble a HAR
  for the report.
