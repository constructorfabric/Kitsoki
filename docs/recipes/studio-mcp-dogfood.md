# Dogfood Through Studio MCP

Use this pattern when the claim you want to test is not just "an agent can fix
this", but "Kitsoki can drive this workflow through Kitsoki's own MCP surface."
It works from Claude Code, Codex, or any other MCP-capable client, and it is most
useful when the worker model is not Claude: the outer client only operates the
studio, while Kitsoki chooses the real maker model through a harness profile.

The guide has two halves:

- **Technical:** how to attach the MCP server, run a constrained driver, reload
  after a fix, and verify the work.
- **Logical:** how to keep the run honest, traceable, and useful instead of
  turning it into another ungrounded agent transcript.

## Part 1: Technical Runbook

### Configure The MCP Server

The repo-local Claude shape is already checked in:

```json
{
  "mcpServers": {
    "kitsoki": {
      "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "stories"]
    }
  }
}
```

Claude Code reads that as `.mcp.json`. The repo also installs
`.claude/agents/kitsoki-mcp-driver.md`, a constrained agent whose tool list is
only the Kitsoki studio MCP surface.

Codex uses its MCP TOML configuration rather than `.mcp.json`. Add Kitsoki with
the CLI from this repo root:

```sh
codex mcp add kitsoki -- kitsoki mcp --stories-dir stories
codex mcp list
```

If you prefer explicit project config, put the equivalent server entry in the
Codex config that your session loads, then restart the Codex session. Active MCP
servers are attached at session startup.

### Start With A Transport Smoke

Every driver run starts by proving the attached server is current:

1. `studio.ping` confirms the server is reachable and reports the version.
2. `studio.handles` shows existing studio handles and any sessions you might
   need to close or reuse.
3. If the run depends on recently added fields, make a harmless call that uses
   them before spending a live session.

Stale servers were a recurring failure in recent sessions. Rebuilding Kitsoki
does not update an already-attached MCP process. If tools are missing, schemas
fail to load, or `session.new` rejects fields like `profile`, `harness`,
`trace`, or `initial_world`, reload the MCP attachment before diagnosing the
story.

### Run A Pure Kitsoki Driver

For Claude, use the checked-in driver:

```sh
claude -p "$(cat .context/your-drive-brief.md)" \
  --mcp-config .mcp.json \
  --strict-mcp-config \
  --allowedTools "mcp__kitsoki__studio_ping,mcp__kitsoki__studio_handles,mcp__kitsoki__studio_work,mcp__kitsoki__story_read,mcp__kitsoki__story_write,mcp__kitsoki__story_validate,mcp__kitsoki__story_graph,mcp__kitsoki__story_test,mcp__kitsoki__session_new,mcp__kitsoki__session_attach,mcp__kitsoki__session_drive,mcp__kitsoki__session_submit,mcp__kitsoki__session_continue,mcp__kitsoki__session_answer,mcp__kitsoki__session_status,mcp__kitsoki__session_world,mcp__kitsoki__session_inspect,mcp__kitsoki__session_trace,mcp__kitsoki__session_close,mcp__kitsoki__render_tui,mcp__kitsoki__render_tui_png,mcp__kitsoki__render_web,mcp__kitsoki__issue_create"
```

The helper `tools/mcp-drive/drive.sh` is convenient for headless Claude runs, but
its default allowlist includes shell and read tools for some verification flows.
For strict dogfood authenticity, override it:

```sh
MCP_DRIVE_TOOLS="mcp__kitsoki__studio_ping,mcp__kitsoki__studio_handles,mcp__kitsoki__studio_work,mcp__kitsoki__story_read,mcp__kitsoki__story_write,mcp__kitsoki__story_validate,mcp__kitsoki__story_graph,mcp__kitsoki__story_test,mcp__kitsoki__session_new,mcp__kitsoki__session_attach,mcp__kitsoki__session_drive,mcp__kitsoki__session_submit,mcp__kitsoki__session_continue,mcp__kitsoki__session_answer,mcp__kitsoki__session_status,mcp__kitsoki__session_world,mcp__kitsoki__session_inspect,mcp__kitsoki__session_trace,mcp__kitsoki__session_close,mcp__kitsoki__render_tui,mcp__kitsoki__render_tui_png,mcp__kitsoki__render_web,mcp__kitsoki__issue_create" \
  tools/mcp-drive/drive.sh --prompt-file .context/your-drive-brief.md
```

For Codex, use the same constraint in the session prompt or agent wrapper: the
driver may call only `mcp__kitsoki__...` tools. The supervising Codex session can
still verify the result afterward with normal tools, but the delegated driver
should not use shell, filesystem, git, or GitHub tools for the core workflow.

### Drive The Story

Use this sequence unless the story's README says otherwise:

1. Read the story first: `story.graph`, `story.read`, and the relevant room
   files. Seed the exact world keys the story reads, not intuitive aliases.
2. Open one session with the complete seed:

   ```json
   {
     "story_path": "stories/bugfix/app.yaml",
     "harness": "live",
     "profile": "codex-native",
     "trace": ".artifacts/mcp-dogfood/bugfix-ticket-123.trace.jsonl",
     "initial_world": {
       "ticket_id": "ticket-123",
       "base_commit": "<buggy-sha>",
       "test_cmd": "go test ./internal/foo"
     }
   }
   ```

3. Prefer `session.submit` for deterministic menu choices. Use `session.drive`
   only when you are intentionally testing free-text routing.
4. Check state with `session.status`, then targeted `session.world` reads. Use
   `session.inspect` only when you need the full snapshot.
5. On surprises, read `session.trace`. The trace is the ground truth for routing,
   host calls, agent calls, and swallowed `on_error` arcs.
6. Close abandoned sessions with `session.close`, especially before reusing a
   trace path.

The profile chooses the worker model. For example, `profile: "codex-native"` can
drive the maker through an OpenAI-backed profile, while the outer Claude or Codex
client merely clicks studio tools.

### Verify Without Spending More LLM

After any story edit, the driver should run MCP-native deterministic gates:

- `story.validate`
- `story.test`
- `render.tui` or `render.web` when the claim is visual

For visual/web claims, add a visual MCP pass before accepting the result:

1. Open the live web surface, not the generic observer page. Use the reserved
   `route` query key when the target is a session route:

   ```json
   {
     "kind": "web",
     "session_id": "<session>",
     "query": {
       "route": "/s/<session>/chat",
       "visual_annotate": "1"
     }
   }
   ```

2. Run `visual.observe` and check that the returned regions match the task. A
   Slidey deck annotation/refine scenario should expose `media`, `deck`, and
   `annotation` regions, not just `chat` or `trace`.
3. If the workflow depends on pointing at an element, confirm that the observed
   surface exposes actionable semantic handles or anchors. A screenshot alone is
   not sufficient proof for a spatial-edit workflow.
4. Re-check the downstream prompt/trace for the anchor payload. In Slidey refine
   flows the target scene should render as JSON, not a Go/template object string.

The supervisor should independently verify the worktree or commit as well. Do
not accept "the driver says it passed" as the proof. Check the trace, the diff,
untracked files, and the relevant tests. A common failure pattern is "green on
disk, not committed" or a regression flow that encodes the wrong expected
behavior.

When a live dogfood run proves a scenario, convert it into a deterministic asset:

```sh
kitsoki trace to-flow .artifacts/mcp-dogfood/run.trace.jsonl
```

Then keep future tests, demos, and videos on the no-LLM flow/cassette path.

### Report Or Fix A Bug

If the bug is in the story and the MCP surface can express the fix:

1. Patch with `story.write`.
2. Inspect the returned validation result.
3. Run `story.validate` and `story.test`.
4. Re-drive the failing path.
5. Commit only the changed files for that fix.

If the bug is in Kitsoki runtime or the MCP server itself, the strict driver
should file it through `issue.create` instead of reaching for non-MCP tools.
Include:

- what the driver was trying to do
- exact MCP calls and key arguments
- expected vs actual behavior
- `include_trace: true` and `include_inspect: true` when a handle reproduced it
- `tui_png`, `tui_text`, or `web` assets when the issue is visual

When you fix an MCP/runtime bug outside the strict driver, reload the MCP server:

```sh
make install
kitsoki mcp-test --stories-dir ./stories --workspace stories/kitsoki-dev
codex mcp list
```

Then restart or reconnect Claude/Codex so the attached server is the new binary.
If the tool list says connected but tools are unavailable, suspect a schema
registration failure and smoke with `kitsoki mcp-test`.

## Part 2: Logical Guide

### Treat It As A Two-Agent Protocol

The driver acts only through Kitsoki MCP. The supervisor verifies evidence. Keep
those roles separate:

- The driver is constrained, trace-producing, and story-aware.
- The supervisor is skeptical, checks git/tests/artifacts, and decides whether
  to merge, file, or rerun.

This separation is the value proposition for non-Claude users: Claude, Codex, or
another local/open model can be the outer operator, while Kitsoki routes the real
workflow through a local story, a chosen profile, and a durable trace.

### Require Evidence Over Narrative

A driver can narrate a run that never happened, or confuse committed work with
dirty work. A valid dogfood result has evidence:

- explicit trace path
- session status and relevant world keys
- trace events for routing and agent calls
- flow-test or targeted test output
- diff and untracked-file check
- issue URL for any MCP gap

If the agent says "done" but there is no trace, no diff, or no deterministic
gate, the result is not yet evidence.

### Improve The Story, Not The Prompt Around It

Dogfood friction is product data. When a session fails because a room is unclear,
a world key is missing, a worktree is cut from the wrong base, or the UI hides the
real state, fix the story/runtime generally. Do not hand-feed the driver a
case-specific workaround and call the story good.

Good fixes are generic:

- add a missing MCP capability
- clarify a room prompt for the whole class of bugs
- add a deterministic flow fixture
- expose a trace/status field the driver needs
- improve the story's review/accept/refine loop

Bad fixes name one ticket, assume one branch, or bypass the failing room.

### Keep Live LLM Use Deliberate

Automated tests must stay no-LLM. Use live sessions only when the question
requires genuine model behavior:

- free-text routing quality
- story prompts and agent contracts
- provider/profile selection
- long-running delegated workflows

Once live behavior is validated, capture it as a trace and turn it into replay or
flow coverage.

### Make MCP Gaps First-Class Findings

If a strict driver cannot develop, test, run, introspect, trace, or debug a story
through MCP, that is not an excuse to use shell from inside the driver. It is an
MCP product gap. File it with `issue.create`, include the trace, and then decide
whether the supervisor should fix the gap before rerunning.

The clean rule is:

> If the claim is "Kitsoki can do this through Kitsoki," the driver may only use
> Kitsoki MCP. Anything missing from that surface is part of the finding.

### Checklist

- [ ] Driver has only `mcp__kitsoki__...` tools.
- [ ] `studio.ping` and `studio.handles` passed before the run.
- [ ] `session.new` used explicit `story_path`, `harness`, `profile`, `trace`,
      and complete `initial_world`.
- [ ] The driver read the story schema/rooms before seeding.
- [ ] Every surprising outcome was checked against `session.trace`.
- [ ] Story edits went through `story.write`, then `story.validate` and
      `story.test`.
- [ ] Visual claims were checked with `visual.open` / `visual.observe` on the
      real target route, with task-relevant regions and anchors present.
- [ ] Live behavior was converted to deterministic flow/cassette coverage when
      useful.
- [ ] MCP gaps were filed through `issue.create`.
- [ ] The supervisor independently checked trace, diff, untracked files, and
      tests before accepting the result.
- [ ] MCP was rebuilt and the client attachment restarted after server/tool
      changes.
