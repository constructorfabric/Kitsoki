---
# triage-marathon: ALREADY-FIXED in main — 224cea6e/8133a50d — {% verbatim %} wrap in metamode adapter
id: 2026-05-18T045825Z-metamode-prompt-pongo2-unescaped-template-tags-in-transcript
title: "metamode: prompt render fails with pongo2 EOF when transcript/view contains '{{' or '{%' (story-author edit turn dies)"
target: kitsoki
filed_at: 2026-05-18T04:58:25Z
filed_by: cloud-user
status: fixed
severity: P1
component: metamode
kitsoki_rev: 3ff850e
trace_ref: "/tmp/kitsoki-dogfood-trace.jsonl @ 2026-05-18T04:58:25.622983616Z"
external: {}
assignee: ""
url: "issues/bugs/2026-05-18T045825Z-metamode-prompt-pongo2-unescaped-template-tags-in-transcript.md"
related:
  - 2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency
---

## Body

The metamode `story-author` (and presumably every other meta-agent
that goes through the same adapter) crashes mid-turn when the prompt
it tries to render contains pongo2-flavoured template syntax (`{{ }}`
or `{% %}`) inside any of the interpolated regions — most easily
triggered by quoting a room's `view:` template back to the user in a
prior reply, since that quote then re-enters the next turn's prompt
via the transcript.

### Observed

From `/tmp/kitsoki-dogfood-trace.jsonl`:

```
{"time":"2026-05-18T04:58:25.622983616Z","level":"ERROR",
 "msg":"metamode.agent.error",
 "chat_id":"01KRWQ2R9JFV169A1EFTB4NTDT",
 "mode":"edit",
 "err":"metamode.AgentAdapter: host.agent.ask_with_mcp:
   render prompt \"/tmp/kitsoki-prompt-2658909342.txt\":
   render: pongo2 template \"You are the `story-author` agent for
   kitsoki. A \\\"story\\\" is a directory…\":
   [Error (where: parser) in <string> | Line 356 Col 140 near
    '│                              \\n│ world.cor\\n[/user]\\n']
   Unexpectedly reached EOF, no tag end found."}
```

i.e. pongo2 saw an opening `{{` or `{%` earlier in the prompt that
never closed; parsing reached EOF at the tail of the `[user]` block
inside the system-rendered story-author template. The `│` (`│`)
glyphs are the TUI transcript-pane box borders, and `world.cor` is
the truncated tail of a `world.core__…` reference.

### Root cause (probable)

The metamode adapter assembles its prompt by **substituting the
chat transcript, current view, and world dump into a pongo2 template
without first escaping pongo2 metacharacters in the substituted
content**. When the user (or the previous assistant turn) included a
literal `{{ … }}` or `{% … %}` — e.g. quoting a room's view template
back as a code snippet — pongo2 sees those as live tags in the next
render and fails the whole turn.

Two amplifying factors:

1. **TUI line-wrap + box borders.** The transcript is wrapped to the
   pane width and each wrapped line is prefixed with `│ `. A pongo2
   tag opened at column N on one line can have its closing `}}` /
   `%}` re-flowed to the next line, with a `│ ` between them. Even
   if the adapter tried a naive same-line match, it'd miss these.
2. **The error is fatal for the turn.** There's no graceful
   fallback; the metamode edit hangs / fails and the user has to
   refresh.

### Repro

1. `kitsoki run stories/kitsoki-dev/app.yaml`
2. Open metamode (Tab) and start an `edit` session.
3. Ask the story-author about `core.ticket_search`. In its reply it
   will quote view template fragments containing `{{ world.… }}`
   and `{% if world.… %}` etc.
4. On the next turn (any further user message in the same chat),
   the adapter re-renders the prompt with that transcript content.
   `metamode.agent.error` fires; the turn dies.

Hit reliably in the trace at `04:58:25` after a turn that quoted
`stories/dev-story/rooms/ticket_search.yaml`.

### Expected

The metamode adapter should be able to render arbitrary user /
assistant / view / transcript content into its prompt without that
content being re-parsed as pongo2. Either:

- Switch the outer template engine to one where the substituted
  values are inserted as opaque strings (Go `text/template`'s
  `{{ . }}` substitution does this; pongo2 also has a `{% raw %}`
  block).
- Or escape `{`/`%` in all substituted content before handing it to
  pongo2.

### Suggested fix sketch

- **Find the prompt builder** — likely `internal/metamode/adapter.go`
  or `internal/host/agent_ask_with_mcp.go`. Locate where it calls
  `pongo2.FromString(...).Execute(ctx)` with a context that includes
  things like `transcript`, `view`, `world`, `user_message`,
  `assistant_message`.
- **Wrap each substituted value in `{% raw %}…{% endraw %}`** before
  concatenation, OR pre-escape `{{` → `{{ "{{" }}` and `{%` →
  `{{ "{%" }}` per pongo2's documented escape pattern. The raw-block
  approach is cleaner and survives line-wrapping.
- **Add a regression test** under `internal/metamode/` that
  round-trips a transcript containing `{{ world.x }}` /
  `{% if … %}` literals and asserts the prompt renders without
  error.

### Notes

- This is **not** a story-side bug; story YAML files are correct.
  No fix lives under `stories/`.
- Because the trigger is "agent reply contains the same template
  syntax stories use" — i.e. the most natural thing an authoring
  agent does — it fires constantly during dogfood sessions. Bumping
  severity to P1 for that reason; the metamode UX is broken until
  this lands.
- Related: `2026-05-18T045257Z-localfiles-ticket-rank-by-severity-and-recency`
  is the previous bug filed via the same dogfood session.
