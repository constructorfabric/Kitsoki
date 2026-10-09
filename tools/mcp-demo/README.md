# MCP demo — a coding agent driving kitsoki over MCP (Claude Code TUI POC)

A **tour-driven demo video** of an external coding agent using the kitsoki studio
MCP server (`kitsoki mcp`) end to end: authoring a story, checking it, testing it,
driving a live session, and *seeing* the kitsoki TUI — all over MCP. **Claude Code
(terminal) is the POC**; codex and copilot slot in by swapping a cassette.

It generalizes the VS Code demo→QA pipeline (`tools/vscode-kitsoki`) to a terminal
surface: instead of Playwright filming a real editor, it films an **xterm.js
terminal** replaying a committed **termcast** cassette, through the *same* shared
pipeline — camera (1600×900), `ChapterRecorder` sidecar, 25s duration floor, and
the `kitsoki-ui-qa` gates (blank / pacing / placeholder + the vision review).

This is not the real Kitsoki TUI. For browser-driven evidence of a live TUI
process, use `tools/tui-bridge`, which connects xterm.js to a real PTY spawned by
`kitsoki tui-serve`.

## No-LLM by construction

The replay plays a static cassette in a terminal and **never spawns a model or the
live MCP server** — structurally impossible to incur LLM cost (enforced by
`scripts/lint-no-llm.mjs`). The authenticity comes from **record once, replay
forever**: a *single gated* live `claude` ↔ `kitsoki mcp` session is captured to a
cassette, then replayed for free, identically, on every render. The synthetic
cassette in `casts/` is the deterministic default and the QA fixture; it needs no
capture at all.

## Layout

```
casts/                      termcast cassettes (the agent's session, agent-agnostic)
  types.ts                  the termcast format + ANSI helpers
  claude-code.cast.ts       synthetic Claude-Code session (no-LLM default + QA fixture)
  index.ts                  registry + resolveCast() (MCP_DEMO_AGENT / MCP_DEMO_CAST_JSON)
player/
  index.html                xterm.js terminal "window" on a branded studio backdrop
  serve.mjs                 dep-free static server (player + xterm dist), one origin
tests/
  mcp-terminal.e2e.spec.ts  replays a cast beat-by-beat, films it through the pipeline
  _helpers/                 camera.ts + demo.ts + recorder.ts (shared demo contracts)
scripts/
  lint-no-llm.mjs              the no-LLM / camera / chapters gate for this surface
  streamjson-to-termcast.mjs   claude -p stream-json → draft termcast (the capture path used)
  capture-live.py              alt: a PTY recorder (real claude → asciicast .cast)
  segment-cast.mjs             alt: asciicast .cast → draft termcast
mcp.capture.json            MCP config registering kitsoki mcp for the capture
                            (binds --stories-dir + --workspace to the demo story)
casts/claude-code-live.json the captured-live cassette (committed; agent "claude-code-live")
```

## Run it (no LLM)

```bash
cd tools/mcp-demo
pnpm install                                  # first time
pnpm run lint:no-llm                           # camera · chapters · replays-cast · no-spawn
pnpm run validate                              # WEB_CHAT_PACE=0 — fast assert (throwaway .fast.mp4)
pnpm run record                                # watch-speed → .artifacts/mcp-demo/claude-code.mp4 (+ .chapters.json)
```

QA the recorded video (the `kitsoki-ui-qa` skill — vision review via the local
`claude` CLI, plus the deterministic scans):

```bash
make mcp-qa            # from the repo root — blank/pacing/placeholder + grounded vision verdict
```

The QA contract lives in `.agents/skills/kitsoki-ui-qa/templates/mcp-feature.md`
and `mcp-scenarios.yaml`.

## The termcast format

A cassette is a list of narrated **beats**; each beat is one chapter in the video
(its id/label become the `chapters.json` rail) and carries the caption to show plus
the terminal chunks to play — `type` (operator keystrokes, char-by-char) or `out`
(agent / tool output, written fast). `data` may contain ANSI. See `casts/types.ts`.

## Add another agent (codex / copilot)

The harness is agent-agnostic. Author a `casts/<agent>.cast.ts` (or capture one, see
below), register it in `casts/index.ts`, then:

```bash
MCP_DEMO_AGENT=codex pnpm run record
```

## Record once (GATED — real LLM, explicit go-ahead only)

This is the **only** step that uses a real LLM. Do it deliberately, never in CI
(AGENTS.md: tests never use a real LLM; live is opt-in). It captures an authentic
Claude-Code session into a cassette that then replays for free. Kitsoki itself
stays no-LLM throughout (sessions drive under `harness: replay`); the sole model
in the loop is the external `claude` agent being recorded.

The reliable path is **headless stream-json** — it yields exact tool names,
inputs, and kitsoki results, which render faithfully (incl. the real `render.tui`
frame). Drive against a story whose intents are direct transitions (so the session
is driven by `session.submit`, no routing cassette / no LLM inside kitsoki); the
committed cassette was captured against a small deterministic `barista` story.

```bash
# from the kitsoki repo root — task in a file (see capture/task.txt for the one used):
claude -p "$(cat tools/mcp-demo/casts/capture-task.txt)" \
  --mcp-config tools/mcp-demo/mcp.capture.json \
  --allowedTools 'mcp__kitsoki__*' \
  --output-format stream-json --verbose \
  > .artifacts/mcp-demo/capture/claude-stream.jsonl

# stream-json → draft termcast (renders real cards; story.read shows source,
# render.tui shows the real screen), then point the spec at it:
node tools/mcp-demo/scripts/streamjson-to-termcast.mjs \
     .artifacts/mcp-demo/capture/claude-stream.jsonl --agent claude-code-live \
     > tools/mcp-demo/casts/claude-code-live.json
#   ↑ curate captions / holdMs / result trims before committing
cd tools/mcp-demo && MCP_DEMO_CAST_JSON=casts/claude-code-live.json pnpm run record
```

Claude Code maps the dotted tool names to `mcp__kitsoki__story_write`,
`mcp__kitsoki__session_submit`, `mcp__kitsoki__render_tui`, … — hence the
`mcp__kitsoki__*` allowlist (no mid-run permission prompts to pollute the capture).

> Alternative — a verbatim *terminal* capture (rich TUI bytes) rather than
> stream-json: `scripts/capture-live.py` (a dep-free PTY recorder → asciicast) +
> `scripts/segment-cast.mjs` (asciicast → draft termcast). Use when you want the
> exact terminal rendering instead of re-rendered structured events.
