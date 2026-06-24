# Epic: kitsoki as a dependency — base stories + demos inside a foreign repo

**Status:** Implemented (code-complete + validated). All 4 slices shipped on
branch `kitsoki-as-dependency`; the gears team's own instance lives zero-config
in the gears repo under `stories/gears-rust/`, importing `@kitsoki/dev-story`
(PR #4082). Narrative moved to the
dev-story README, the `kitsoki-ui-demo` SKILL.md, `external-project-targeting.md`,
and the new [`kitsoki tour` reference](../web/tour.md). The dev-story PRD → Design
demo now renders binary-native (`kitsoki tour --feature dev-story-prd-design`) —
148s MP4, 11 chapters, validated by a `kitsoki-ui-qa` pass (8/9 beats proven from
frames; the multi-round-clarification beat is proven deterministically by the new
`steps.json` `states_asserted`). Three render bugs were fixed landing it: the
tour driver now types via the text-floor when a choice widget hides the composer;
the render runs one-shot (not staged) so `emit_intent` chains settle; and a
relative `--out` no longer breaks the ffmpeg stitch. **Remaining (release, not
implementation):** push the branch + the gears side and update PR #4082; delete
this file on merge (its narrative now lives in the docs above).
**Kind:**   epic
**Slices:** 4 (4/4 shipped and validated)

## Why

We built `stories/gears-rust/` — `dev-story` retargeted at an external repo
(`constructorfabric/gears-rust`) to drive its PRD → Design walk — plus a no-LLM,
tour-driven demo **video** of that walk. Both still live in the **kitsoki repo**
and depend on the kitsoki **source tree**: the instance imports its base via
`source: ../dev-story` (a relative path into kitsoki's `stories/`, whose
transitive closure is `bugfix`, `pr-refinement`, `implementation`, `cypilot`,
`code-review`, `prd`), and the video is rendered by a Playwright/pnpm spec
(`tools/runstatus/tests/playwright/gears-prd-design.spec.ts` + a TS tour
manifest + the SPA build), not by the binary.

The goal is to run `kitsoki` **naturally inside a foreign repo with only the
binary present**: that repo carries its own tiny instance that does
`import: { source: "@kitsoki/dev-story" }`, the kitsoki story library ships
**inside the binary** (overridable to a local kitsoki checkout via a CLI flag),
and the demo video **renders from the binary** (`kitsoki tour …`) with no
pnpm/Playwright. This resolves
[`external-project-targeting.md`](external-project-targeting.md) open question #1
(instance in the kitsoki repo vs owned by the target repo) in favour of
*the target repo owns its instance and imports the base from the binary*.

## What changes

Once every slice ships:

- The kitsoki binary **embeds the whole `stories/` library**. A story's
  `source: "@kitsoki/<name>"` resolves against the embedded library when no
  on-disk kitsoki checkout is found; `--kitsoki-repo <path>` overrides it to a
  live checkout for development. Runtime asset reads (prompts, schemas, `.star`,
  `views/`) of an embedded story keep working unchanged.
- A new `kitsoki tour` subcommand renders a deterministic no-LLM demo MP4 (+
  chapter sidecar + per-step screenshots) by driving the embedded web UI through
  headless Chrome — no Node/pnpm/Playwright. Tour manifests become
  **self-driving** (each step carries declarative `drive:` actions), so the
  binary, not a hand-written `.spec.ts`, can render any demo.
- `gears-rust` lives in the **gears repo** (zero-config `stories/gears-rust/`,
  discovered by the default `./stories` walk), importing the base via
  `@kitsoki/dev-story`; it runs there
  with `kitsoki web` and `kitsoki tour`. The kitsoki repo keeps only
  self-targeting (dogfood) stories.
- A parallel **dev-story (self-targeting) PRD → Design golden video**, rendered
  via `kitsoki tour`, becomes the *conversational-development* golden example at
  parity with the gears one.

## Impact

- **Spans:** runtime (slices 1, 2), story (slices 3, 4).
- **Net surface:** one new `internal/basestories` package + an embed of
  `stories/`; one loader resolver seam + a `--kitsoki-repo` flag; one new
  `kitsoki tour` subcommand (`internal/tour` + chromedp dep); a `drive:`
  extension to the tour-manifest schema; the gears instance moves out-of-tree;
  one new dev-story flow fixture + feature catalog entry + golden-pointer edit.
- **Docs on ship:** the dev-story README (PRD → Design demo section + "external
  targets live in their own repo" note), the `kitsoki-ui-demo` SKILL.md (golden
  pointer + the binary-native render path), and a short `kitsoki tour` reference
  under `docs/`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Base-story delivery | runtime | Embed all of `stories/` in the binary; `@kitsoki/<name>` resolves it; `--kitsoki-repo` overrides to a live checkout | — | Draft | _this epic (child TBD)_ |
| 2 | `kitsoki tour` subcommand | runtime | Render a no-LLM tour MP4 from the binary (headless Chrome + ffmpeg) driven by a declarative manifest | — | Draft | _this epic (child TBD)_ |
| 3 | Move gears-rust to the gears repo | story | Vendor the instance + templates + flow + feature catalog into the gears repo; import the base via `@kitsoki/dev-story` | 1, 2 | Draft | _this epic (child TBD)_ |
| 4 | Dev-story golden video | story | A parallel self-targeting PRD → Design demo, rendered via slice 2, made the conversational-development golden example | 1, 2 | Draft | _this epic (child TBD)_ |

## Sequencing

```
#1 (embed base) ──┐
                  ├──▶ #3 (gears-rust → gears repo)
#2 (kitsoki tour) ─┤
                  └──▶ #4 (dev-story golden video)
```

#1 and #2 are independent engine work and land in parallel. #3 needs the
embedded base (#1) and the binary renderer for its video (#2). #4 dogfoods both
#1 (self-targeting still loads dev-story) and #2 (it is rendered by the new
subcommand), and is the parity bar for the gears video.

## Slice 1 — Base-story delivery

A base (imported) story isn't just an `app.yaml`: at **load time** it reads
child manifests, `include:` globs, agent `system_prompt_path`, and
`.star`/`.star.yaml` sidecars; at **runtime** its rooms read `prompts/*.md`,
JSON schemas, `views/`, and `.star` scripts — resolved relative to the story's
own dir (load time via `baseDir`; runtime via the `KITSOKI_APP_DIR` env set in
`internal/app/loadfiles.go:52`, joined by `resolvePromptPath` in
`internal/host/agent_ask.go:415`). These **base-story reads** must come from
the embedded library; the disjoint set of **target-repo reads** —
`workdir`/`repo_root` publish paths, artifacts, `host.append_to_file`
(`append_file_transport.go`), `artifacts_dir_transport.go` — must stay on the OS
filesystem of the foreign checkout.

**Recommended approach: materialize-to-cache (not `fs.FS` plumbing).**
`go:embed all:stories/**` the whole library, but on first use **extract it to a
content-addressed cache dir** and resolve `@kitsoki/<name>` to that on-disk
path. Everything downstream (load *and* runtime) then works **unchanged on the
OS filesystem** — no rewrite of the ~20 `os.ReadFile`/`os.Stat` call sites,
`KITSOKI_APP_DIR` semantics intact.

- New `internal/basestories` package: `//go:embed all:stories/**` (built from the
  repo's `stories/` via a `go:generate`/`make embed-stories` staging step so it
  can't drift silently) + `Materialize() (root string, err error)` that extracts
  once to `${XDG_CACHE_HOME:-~/.cache}/kitsoki/stories/<binaryVersion-or-hash>/`
  (idempotent; version-keyed so a new binary re-extracts). Mirrors Go's module
  cache; principle of least surprise.
- `resolveImportSource` (`internal/app/imports.go:211`) gains a fallback: when
  `@kitsoki/<name>` finds **no** on-disk kitsoki root (today's `findRepoRoot`
  failure) **and** no `--kitsoki-repo` override, resolve against
  `basestories.Materialize()` instead of erroring. Order:
  `--kitsoki-repo` flag › discovered on-disk kitsoki root › embedded library.
- New persistent `--kitsoki-repo <path>` flag (and/or `KITSOKI_REPO` env) on the
  root command, threaded into the loader as an **injected resolver** (DI, not a
  package global).

**Critical files:** `internal/basestories/` (new), `internal/app/imports.go`
(`resolveImportSource` + injected resolver), `internal/app/loader.go` (thread the
override through `Load`/`LoadWithOverrides`), `cmd/kitsoki/main.go` (persistent
flag), a `make`/`go:generate` staging target. `webconfig.DiscoverStories` is
unaffected (it only walks the *local* story dirs).

**Alternative considered (not recommended):** parameterize the loader + runtime
on an `fs.FS` and read the embed directly — cleaner conceptually but touches ~20
`os.*` call sites across load and runtime and changes `resolvePromptPath` /
`KITSOKI_APP_DIR` semantics, for no user-visible benefit over the cache.

## Slice 2 — `kitsoki tour` subcommand

Move demo rendering out of the Playwright/pnpm harness and into the binary so a
foreign repo with only `kitsoki` can produce the MP4.

1. **Make the manifest self-driving.** Today the manifest (`features/*.yaml`
   `tour:` block / `*-manifest.ts`) carries *narration*
   (title/body/target/placement) while the *driving* (type "prd", click
   `core__prd__start`, wait `core.prd.idle`, paced reveal) is hand-written in the
   `.spec.ts`. The binary can't read `.spec.ts`. Extend each tour step with an
   optional ordered **`drive:`** list of declarative actions —
   `type-and-send: <text>`, `click-intent: <intent>`, `wait-state: <state>`,
   `reveal-turn`, `dwell-ms: <n>` — capturing the imperative spec logic as data.
   The TS schema (`tools/runstatus/scripts/features/schema.ts`) and `TourStep`
   type (`src/tour/types.ts`) get the mirror; a Go struct unmarshals the same
   YAML.
2. **A Go tour-runner** (`cmd/kitsoki/tour.go` + `internal/tour/`) that: starts
   the embedded web server in-process in the no-LLM posture (reusing the
   `cmd/kitsoki/web.go` `--flow`/`--host-cassette`/`--stories-dir` plumbing);
   launches headless Chrome via **chromedp** (pure-Go CDP, no Node), injects
   `window.__startTourWithSteps`, and executes each step's `drive:` actions
   (CDP `Runtime.evaluate` for clicks/typing, session-state polling for
   `wait-state`, paced reveal via the same easing the spec uses); captures video
   via CDP `Page.startScreencast` → frames → **ffmpeg** (already wrapped in
   `internal/video/video.go`) and emits the chapter sidecar reusing
   `internal/video.Chapter` + `WriteChapters` (port the JS `ChapterRecorder` to
   Go). Output: `<artifactDir>/<videoBase>.mp4` + `.chapters.json` + PNGs. Input:
   `kitsoki tour --feature <id>` (from a `features/` catalog the repo ships) or
   `--manifest <yaml>`, plus `--flow`, `--stories-dir`, `--out`, `--pace`,
   `--headless`.

**Critical files:** `cmd/kitsoki/tour.go` (new), `internal/tour/` (new),
`go.mod` (+`github.com/chromedp/chromedp`), reuse `internal/video/video.go`,
`internal/runstatus/web`, and the `cmd/kitsoki/web.go` bootstrap. The Playwright
golden example (`agent-actions-video.spec.ts`) stays as the JS-side reference;
the subcommand is additive.

## Slice 3 — Move gears-rust to the gears repo

The gears repo becomes a normal kitsoki host repo: the gears team's own instance
lives zero-config under `stories/gears-rust/` (`app.yaml`, `templates/`, `flows/` incl.
`prd_to_design_full.yaml`, `scenarios/`, the `drive:`-enabled tour manifest, and
`features/gears-prd-design.yaml`) — discovered by the default `./stories` walk
(`internal/webconfig` `defaultStoryDirs`), so no `.kitsoki.yaml` is needed. The
only instance edit is the import: `source: ../dev-story` →
`source: "@kitsoki/dev-story"`; the doc-profile `world:` keys are unchanged, with
`workdir`/`repo_root` now defaulting to `.` (the gears checkout). It runs there
with `kitsoki web` (discovers the instance under `stories/`) and
`kitsoki tour --feature gears-prd-design`.

In the **kitsoki repo**: delete `stories/gears-rust/**`,
`tools/runstatus/tests/playwright/gears-prd-design.spec.ts`,
`tools/runstatus/src/tour/gears-prd-design-manifest.ts`, and
`features/gears-prd-design.yaml`; update
[`external-project-targeting.md`](external-project-targeting.md) and the
dev-story README to point at the gears repo as the worked external example, with
a one-paragraph "external targets live in their own repo" note.

## Slice 4 — Dev-story golden video (conversational development)

A self-targeting parallel of the gears demo, rendered via slice 2, made the
**golden example** for conversational development.

- **Flow fixture (new):** `stories/dev-story/flows/prd_to_design_full.yaml` —
  mirror gears' single-session `main → prd → … → prd_published → continue →
  design → … → design_done → main` (`prd_author`/`design_author` task ids), but
  self-targeting: defaults (no external-target world keys), publishing to
  `docs/prd/<slug>.md` + `docs/proposals/<slug>.md`, and (unlike gears)
  **minting the feature ticket** (`design_ticket_dir: issues/features`). Wired
  as a `kitsoki test flows` fixture (no-LLM).
- **Tour manifest + catalog (new):** `features/dev-story-prd-design.yaml` with
  the slice-2 `drive:` actions, ~11 steps mirroring the gears manifest but
  narrating iterative clarification + brief-refinement as the
  conversational-development model ("kitsoki on kitsoki"), with the feature-ticket
  beat at publish.
- **Render via the binary:** `kitsoki tour --feature dev-story-prd-design` —
  proves slice 2 end-to-end. Parity bar = the gears video's quality (per-turn
  paced reveal, chat-pinned framing, chapter sidecar).
- **Designate golden:** update the `kitsoki-ui-demo` SKILL.md golden pointer
  (today `agent-actions`) to cite this as the conversational-development golden,
  add a Demo-video section to the dev-story README, and validate legibility with
  `kitsoki-ui-qa` against the rendered MP4.

## Shared decisions

1. **Embed the whole `stories/` library, cache-materialized** — not just the
   `dev-story` closure, and not `fs.FS`-plumbed. Override order:
   `--kitsoki-repo` › on-disk kitsoki root › embedded.
2. **Tour manifests become self-driving (`drive:` actions)** — the prerequisite
   that lets the binary, not a `.spec.ts`, render any demo. Both videos use it.
3. **chromedp over playwright-go** for the binary renderer — pending a
   screencast-cadence spike (decided in slice 2's child).
4. **External targets live in their own repo** going forward; the kitsoki repo
   keeps only self-targeting (dogfood) stories. gears-rust is the migration proof.

## Cross-cutting open questions

1. **Does the kitsoki repo keep a thin gears smoke test** (a CI job that
   `go run`s `kitsoki tour` against a checked-out gears fixture) or fully hand
   off validation to the gears repo? *Lean: hand off; keep only a `basestories`
   load test in the kitsoki repo.*
2. **Cache key for the materialized library** — binary version vs content hash.
   *Lean: content hash of the embed (version-independent, survives rebuilds with
   no story change). Decided in slice 1's child.*

## Non-goals

- A general remote/git-fetch module system — the chosen mechanism is embed +
  local-override, not a fetcher.
- Replacing the Playwright harness for the kitsoki repo's own existing demos —
  the subcommand is additive; `agent-actions` et al. stay as-is.
- Real-LLM anything — every flow/video is no-LLM via flow fixtures + cassettes
  (CLAUDE.md).
