
- add bug report mode
- generate test from trace
- in proposal review mode, make the input and proposal different colors from the rest of the text
- when user presses enter, immediately add their input into the chat window and show thinking there, block input until resolved (can keep some spinner in the input area too)
- check that we're really doing the mcp validation method - i think we're maybe not based on some bugs
- use specific claude agents for each room/intent where oracle/claude is invoked
- extensible stories - reusable dev story w/ company and project-specific aspects
- live discovery of story aspects as the user navigates to different projects, projects can define their own story aspects
- visually distinguish between user commands that were interpreted deterministically vs those that use the LLM, when the LLM is used show the actual filled intent that was selected w/ confidence level
- cache of natural language to intents to avoid calling claude again
- expose oracle API so scripts can funnel their LLM usage via the standard interface instead of invoking claude -p individually, possibly bypassing configuration.  this would mean that scripts can use a generic API, and the interface can choose codex vs claude (in the future), and handle the tracing, playback and testing with a standardized mechanism.  scripts would then never use claude directly.
- background jobs on VMs, dispatch and track, survive intermittent connectivity (dev laptop to VM w/ VPN and closed lid issues)
- react UI
- meta mode: ask questions or improve the story itself (replace edit mode).
- self mode: ask questions or improve hally itself.
- better testing for proposal mode - should work like conversation (w/ peristent convo)
- remote job mode: monitor and control sessions on VMs

## Tech debt

### View rendering: unify structured + prose content
➜  devstory git:(devstory) ✗ hally run stories/devstory/app.yaml --trace /tmp/devstory-trace.jsonl --trace-pretty /tmp/devstory-trace.log
Error: validate hosts: host: unregistered hosts declared in app manifest: [host.oracle.talk]
Usage:
  hally run <app.yaml> [flags]

Flags:
      --claude-model string   model passed to claude -p --model (default: claude-haiku-4-5-20251001); use 'opus' for higher quality at higher cost
      --db string             path to SQLite session database (default: $XDG_DATA_HOME/hally/sessions.db)
      --harness claude        harness type: claude|live|replay|recording (default: claude if claude binary on PATH, else live if ANTHROPIC_API_KEY set, else replay)
  -h, --help                  help for run
      --oracle string         path to oracle YAML file (required for --harness replay)
      --record string         path to output JSONL recording (for --harness recording)
      --trace string          write JSONL trace events to this file; '-' writes to stderr
      --trace-level string    minimum trace level: debug|info|warn|error (default: debug when --trace is set) (default "debug")
      --trace-pretty string   write human-readable trace to this file in parallel; '-' writes to stderr
      --trace-redact          redact sensitive values (API keys, etc.) in trace output (default true)

error: validate hosts: host: unregistered hosts declared in app manifest: [host.oracle.talk]
**Problem.** The TUI runs every state's `view:` through Glamour with
`glamour.WithPreservedNewLines()`. That's necessary for structured views
(e.g. the Terminal Room's `propose "…"` examples, menu-like bullet lists)
where each authored line must stay on its own line. But the same setting
caps pure-prose views at their hand-wrapped width — `cloak` foyer is
authored at ~65 chars/line, so it sits in a narrow column even on a
150-col terminal. Shrinking works (Glamour re-wraps longer-than-panel
lines); growing past the authored wrap is a no-op.

Stopgap picked 2026-04-23: leave `WithPreservedNewLines` on; document
that prose views expand only up to the author's wrap width. See the
transcript.go comment near `renderMarkdown`.

**Real fix direction.** Introduce a typed "view element" system so the
state can declare *what kind of content* each block is, and the renderer
styles accordingly. Sketch:

```yaml
view:
  - prose: |
      You are in a spacious hall, splendidly decorated in red and gold,
      with glittering chandeliers overhead. ...
  - list:
      title: "Available areas"
      items:
        - { key: "Start a new task", value: "jira search" }
        - { key: "Check my inbox",   value: "notifications" }
  - code: |
      propose "list files in /tmp"
  - kv:
      Workspace: "{{ world.current_workspace }}"
      Last result: "{{ world.proposal_result }}"
  - template: |
      Legacy free-form view; renders via current Glamour pipeline.
```

Benefits:

- **Prose** reflows to the full panel width. No more hand-wrap cap.
- **List** renders as an aligned two-column layout that the renderer
  sizes to the viewport (no more fragile hardcoded spaces between
  "terminal" and "(run commands)" being collapsed by Markdown).
- **Code** preserves layout exactly (monospace, whitespace intact).
- **KV** handles the "Workspace: x / Last result: y" pattern that
  Markdown today either merges into one line or renders awkwardly.
- **Template** is the escape hatch for apps that need raw Glamour.

Author-side migration would be opt-in: the existing `view: "<string>"`
form keeps today's Glamour-rendered behaviour (mapped to `- template:
<string>` internally). New apps can use the typed elements from day 1.

Runtime-side: a small `internal/tui/elements/` package with a renderer
per element kind. `transcriptModel` asks each element to render at
(viewportWidth), concatenates results, word-wraps prose internally.

Also likely swaps Glamour for direct lipgloss rendering in most cases —
Glamour is overkill when we control the structure. Keep Glamour only
inside the `template` escape hatch.

**Adjacent issues this would also solve.**

- Column-aligned lists (dev-story's main-room menu) currently rely on
  Markdown-collapsed multi-space runs, which look wrong at non-default
  widths.
- The Terminal Room's "Propose a command to run, e.g.: / indented
  examples" structure is a list dressed as prose.
- Off-path banner, proposal diff, trace embeds — all currently shoved
  into the same Glamour pipe and styled implicitly.
- LLM-driven proposals (per the apply-proposal doc) would be easier to
  author as element arrays than as opaque Markdown blobs.

**Blocking?** No current user is blocked — cloak's prose-narrow issue is
cosmetic and devstory's structured views already render correctly. Pick
this up when either (a) a new app needs column-aligned lists that don't
break on resize, or (b) someone tries to add richer UI (diffs, inline
images, panels).