# Runtime: Visual producers (slidey + contact sheet)

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../visual-outputs.md

## Why

The substrate slice ([`media-artifact-substrate.md`](media-artifact-substrate.md))
records a finished visual file as an artifact, but a story still has to
*produce* that file by hand-rolling a `host.run` shell-out
(`internal/host/handlers.go:43`) — assembling argv, knowing where the
render tool lives, parsing its output, and locating the result. The render
tools already exist and are proven:

- **slidey** (standalone repo at `~/code/slidey`, driven by the
  `slidey-authoring` skill) turns one JSON scene spec into an MP4 (Puppeteer
  frames + `edge-tts` narration + `ffmpeg` mux), a PDF (one vector page per
  reveal), and an interactive HTML app — all deterministic, no LLM in the
  render loop. kitsoki already ships an example spec at
  `docs/decks/arch-and-usage.json`.
- **`contact-sheet.sh`** (`.agents/skills/kitsoki-ui-demo/scripts/contact-sheet.sh`)
  tiles numbered PNGs into one storyboard montage via `ffmpeg` — already
  reused across the `kitsoki-ui-demo` and `kitsoki-ui-qa` skills.

This slice wraps them as first-class, deterministic host calls that return
an artifact for the substrate to register — so a story step says "render
this spec" rather than memorizing a CLI.

## What changes

One sentence: **two producer host calls — `host.slidey.render` and
`host.contact_sheet` — invoke the existing render tools deterministically
and hand the output file(s) to the substrate's media-artifact emission.**

- `host.slidey.render` takes a spec (a `world` JSON value or a path) + an
  output format (`mp4 | pdf | html`), runs slidey, and returns the produced
  file path + suggested `mime`/`kind` for emission.
- `host.contact_sheet` takes a directory of PNGs (or a glob) + tiling
  options and returns the montage PNG.
- Both are pure subprocess wrappers — no LLM, fully replayable under the
  flow/cassette test system (a stubbed producer returns a fixture file so
  tests never invoke `node`/`ffmpeg`).

## Impact

- **Code seams:** new handlers beside the existing host calls in
  `internal/host/` (model on `RunHandler`, `internal/host/handlers.go:43`,
  and `CypilotArtifactsHandler`, `internal/host/cypilot_artifacts.go:89`,
  which already shells out to an external `cpt` binary — the closest
  precedent for "host call that wraps an external CLI").
- **Vocabulary:** two host calls — table below.
- **Stories affected:** none today; opt-in. The pitch-video / deck work
  (memory: *kitsoki-pitch-video*) becomes a story step instead of a manual
  skill invocation.
- **Backward compat:** additive; tools remain usable standalone via their
  skills.
- **Docs on ship:** `docs/architecture/hosts.md` (the two host calls).

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.slidey.render` | `{spec \| spec_path, format: mp4\|pdf\|html, scenes?, context?} → {ok, path, mime, kind, exit_code, stdout}` | Shells `node <slidey>/src/index.js <spec> <out.<fmt>>`; deterministic, no LLM. |
| host call | `host.contact_sheet` | `{dir \| glob, cols?, tile_width?} → {ok, path, mime: image/png, kind: image}` | Shells `contact-sheet.sh`; PNG montage / slideshow storyboard. |

## The model

```
world.deck_spec (a JSON value, or docs/decks/*.json)
   └▶ host.slidey.render {spec: world.deck_spec, format: mp4}
          ├▶ resolve slidey home (SLIDEY_HOME | PATH | configured)   deterministic
          ├▶ exec node src/index.js spec.json out.mp4               subprocess, no LLM
          └▶ return {path: out.mp4, mime: video/mp4, kind: video}
   └▶ host.artifacts_dir {src_path: <path>, kind: media, ...}        (substrate, slice 1)
          └▶ record artifact + bind handle
```

The producer is **deterministic execution** — given the same spec it
renders the same bytes (no interpretive step). It returns a path; the
substrate (slice 1) does the recording. Producers do **not** record
artifacts themselves — that keeps one write site for the `artifact`
datapoint.

## Engine seams & invariants

- Both handlers reuse `RunHandler`'s subprocess machinery rather than
  re-implementing exec/exit-code/stdout-JSON parsing
  (`internal/host/handlers.go:88-135`).
- **Tool resolution:** `host.slidey.render` resolves slidey via
  `SLIDEY_HOME` → a configured path → `PATH` (a `slidey` bin), and
  `fail_on_error`-style returns a clear "slidey not found" `Result.Error`
  when absent — the producer degrades gracefully, matching how `ffmpeg` /
  `edge-tts` are already assumed-external (see the `slidey-authoring`
  skill's dependency notes). This is the epic's cross-cutting open question
  #1.
- **Spec validation:** slidey ships a `--validate` mode (JSON Schema) and an
  `--audit` geometry/overlap check; `host.slidey.render` runs `--validate`
  before the full render and surfaces a clear error rather than emitting a
  broken video.
- **No LLM, ever:** the render loop is subprocess-only. Tests stub the
  producer to return a checked-in fixture file (a 1-frame mp4 / tiny png)
  so CI never runs `node`/`puppeteer`/`ffmpeg` (CLAUDE.md; memory:
  *no-llm-tests*, *fast-tests*).

## Decision recording

These host calls record nothing new themselves — the produced file is
recorded by slice 1's `artifact` datapoint when handed to emission. The
subprocess invocation still lands as the usual `host.invoked`/`host.returned`
pair (`internal/journal/types.go:70,75`), giving full provenance of the
render command.

## Backward compatibility / migration

Additive. The standalone tools and their skills are untouched. No story
must adopt these; the deck/pitch-video flow can migrate onto them
incrementally.

## Tasks

```
## 1. Producers
- [ ] 1.1 host.slidey.render handler (resolve slidey, --validate, exec, return path/mime/kind)
- [ ] 1.2 host.contact_sheet handler (wrap contact-sheet.sh)
- [ ] 1.3 Graceful "tool not found" Result.Error for each

## 2. Test seam
- [ ] 2.1 Stub producers to return checked-in fixture files (no node/ffmpeg in CI)
- [ ] 2.2 Flow fixture: render → emit → handle bound (end-to-end with #1 substrate, no LLM)

## 3. Adopt + document
- [ ] 3.1 Migrate one real spec (docs/decks/arch-and-usage.json) into a story step end-to-end
- [ ] 3.2 hosts.md entries; resolve slidey dependency model (epic Q1); trim/delete this proposal
```

## Verification

A flow fixture stubs `host.slidey.render` to return a tiny fixture mp4,
emits it via slice 1, and asserts the bound handle + recorded `artifact`
datapoint — no `node`/`ffmpeg`/LLM. A separate, **gated** (not-by-default)
integration test runs the real slidey on `docs/decks/arch-and-usage.json`
to catch drift against the actual tool — opt-in only, per memory
*no-llm-tests* / *e2e-fidelity-and-boundary*.

## Open questions

1. **slidey dependency model** — deferred to the epic's cross-cutting Q1
   (submodule vs. vendor vs. external PATH). This slice assumes external for
   v1.
2. **Spec source.** Accept an inline JSON `world` value, a path, or both?
   Inline keeps the spec authorable in story YAML/world; a path reuses
   `docs/decks/`. *Lean: support both — `spec` (value) takes precedence
   over `spec_path`.*
3. **PNG-slideshow as a slidey format vs. contact sheet.** A "PNG slideshow"
   could be N per-reveal PNGs (a slidey export mode) or one tiled contact
   sheet. *Lean: contact sheet is the v1 "slideshow" artifact; per-reveal
   PNG export is a slidey-side follow-up.*

## Non-goals

- Re-implementing any render logic — these wrap existing tools only.
- Recording the artifact — slice 1 owns the single write site.
- Web display of the result — slice 3 (`web-media-rendering.md`).
