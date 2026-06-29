---
# triage-marathon: ALREADY-FIXED in main — 31c96b09 — per-session validator schema resolution
id: 2026-06-04T020918Z-imports-rel-asset-path-resolved-against-importer
title: "imported room's relative host-call asset paths (schema/prompt) resolve against the importing app's dir, not the defining story's"
target: kitsoki
filed_at: 2026-06-04T02:09:18Z
status: fixed
severity: P1
component: runtime
kitsoki_rev: 2df6142
trace_ref: "/home/cloud-user/.kitsoki/sessions/kitsoki-dev/b7531687-tui-c60f0e83-2c9c-4d05-a8de-dfccef180849.jsonl"
external: {}
assignee: ""
url: "issues/bugs/2026-06-04T020918Z-imports-rel-asset-path-resolved-against-importer.md"
---

## Body

Filed live from a kitsoki-dev dogfood session, in `core.proposal` (the
proposal discovery + brief room, defined in `dev-story/rooms/proposal.yaml`
and reached via the `core` import). The brief stayed stuck at **0/5 checks**
across multiple turns even though the conversation flowed normally.

Root cause: the `brief_distill` `host.agent.task` that writes the brief every
turn references a relative asset path:

```yaml
- invoke: host.agent.task
  id: brief_distill
  with:
    acceptance:
      schema: schemas/brief-distill.json     # ← relative to the DEFINING story
    context:
      prompt: prompts/proposal_brief_distill.md
```

The schema exists at `dev-story/schemas/brief-distill.json`. But when
`dev-story` is imported into `kitsoki-dev` under alias `core`, the engine
resolves that relative path against the **importing app's** directory rather
than the story that **defines** the room:

```
host.agent.task: build validator MCP config:
  schema ".../.kitsoki/stories/kitsoki-dev/schemas/brief-distill.json" not found:
  stat .../.kitsoki/stories/kitsoki-dev/schemas/brief-distill.json: no such file or directory
```

`.kitsoki/stories/kitsoki-dev/schemas/` does not exist — the schemas live under
`stories/dev-story/schemas/`. So `brief_distill` throws every turn,
`on_error: proposal` swallows it back into the same room, and the brief is
never written. The interviewer (`host.agent.converse`) keeps working because
it doesn't reference a relative asset file, which is why the room *feels* fine
while silently doing nothing useful.

This is general, not a one-off: any imported room that names a relative
`schema:` or `prompt:` asset is broken under import, and `dev-story` has
several such task/ask sites.

### Steps to reproduce

1. `kitsoki run .kitsoki/stories/kitsoki-dev/app.yaml`.
2. Enter the proposal pipeline and reach `core.proposal` (discovery + brief).
3. Talk for a few turns. Watch "Brief checks" — it stays at 0/5.
4. Inspect `world.last_error` / the session trace: every turn shows
   `build validator MCP config: schema ".../kitsoki-dev/schemas/brief-distill.json" not found`.

### Expected vs actual

**Expected:** a relative `schema:`/`prompt:`/asset path in a `host.*` call
resolves against the base directory of the **story that defines the room**
(`dev-story/`), so an imported room finds its own assets regardless of which
app imported it. The brief fills as the user talks.

**Actual:** the path is resolved against the **importing** app's directory
(`kitsoki-dev/`), the asset isn't found, the host call errors every turn, and
`on_error:` masks it — the room silently never advances its brief.

### Proposed fix sketch

- At import/fold time, tag each room (and its host-call effects) with the base
  directory of the story that defined it. Host-call asset resolution
  (`acceptance.schema`, `context.prompt`, and any other relative file ref)
  must use that defining-story base, not the top-level app's base.
- Audit every relative-path field in the host-call schema for the same
  importer-vs-definer ambiguity; `prompt:` is almost certainly affected too —
  it's only masked here because the MCP/schema config is built first and errors
  before the prompt is loaded.
- Consider failing **loudly at load/fold time** when an imported room
  references an asset path that won't resolve, instead of deferring to a
  per-turn runtime error that `on_error:` then swallows.

### Severity rationale

P1 — composition-by-import is the kitsoki-dev architecture's one load-bearing
edge (the whole dogfood instance is `dev-story` imported under `core`). This
silently breaks an imported room's core function on every turn, hidden behind
`on_error:`. Copying the schema into `kitsoki-dev/schemas/` would "fix" this
one symptom while leaving every other imported asset path broken — exactly the
paper-over `stories/CLAUDE.md` forbids.

### Workaround applied (REVERT when fixed)

To keep a live dogfood session moving (`web-story-hub` brief), the two assets
`brief_distill` needs were copied into the **importing** story so the broken
importer-relative resolution finds them:

- `.kitsoki/stories/kitsoki-dev/schemas/brief-distill.json` (copy of
  `stories/dev-story/schemas/brief-distill.json`)
- `.kitsoki/stories/kitsoki-dev/prompts/proposal_brief_distill.md` (copy of
  `stories/dev-story/prompts/proposal_brief_distill.md`)

This is a paper-over of the symptom for ONE task site only — every other
imported task/ask site with a relative asset path is still broken. **Delete
both copies once the runtime resolves imported-room asset paths against the
defining story.**

### Files involved

- `internal/orchestrator/` (import fold + host-dispatch) — carry the
  defining-story base dir through to host-call asset resolution.
- `internal/host/agent_converse.go` / agent task + ask handlers — resolve
  `schema:` / `prompt:` against the defining-story base.
- Import rewriter / loader — optionally validate imported asset paths at fold
  time and fail loudly.
