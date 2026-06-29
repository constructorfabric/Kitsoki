# Project onboarding — getting started with kitsoki in your project

You have the `kitsoki` binary installed (if not, do
[getting-started.md](getting-started.md) first). This guide takes *your*
repository — any language, any stack — from "kitsoki is on my PATH" to a
**fully working kitsoki environment** committed into the repo: a runnable
dev-story instance, the studio MCP registered for your coding agent, and the
kitsoki skill/agent toolkit installed.

> **The 30-second version.** From your project root, with only the `kitsoki`
> binary on PATH (no kitsoki checkout needed):
> ```sh
> kitsoki run @kitsoki/dev-story      # → type: onboard .
> ```
> `@kitsoki/dev-story` resolves the onboarding app from the binary's **embedded**
> story library. Walk the four onboarding rooms (review → apply) and you're done.
> The rest of this page explains what that produces and the standalone command
> behind it.

---

## What onboarding installs

Onboarding writes a small, **auditable, checked-in** set of files. None of it is
generated at runtime or hidden in a cache — it all lives in your repo so it
travels with every clone and every collaborator.

| Path | What | Why |
|---|---|---|
| `.kitsoki.yaml` | `story_dirs: [./.kitsoki/stories]` + `default_story` | so `kitsoki` discovers your instance |
| `.kitsoki/project-profile.yaml` | declarative profile (stack, commands, conventions, dev-story profile, onboarding baseline) | the discovered description of your project and the source for the implicit dev-story root |
| `.kitsoki/stories/<id>-dev/app.yaml` | a materialized dev-story **instance** that imports `@kitsoki/dev-story` | an editable snapshot for web discovery and project-local story extensions |
| `.kitsoki/stories/<id>-dev/README.md` | how to run the instance | — |
| `.mcp.json` | registers the **kitsoki studio MCP** server | so Claude Code / Cursor / any MCP client can drive kitsoki here |
| `.agents/skills/<name>/` · `.agents/agents/<name>.md` | the kitsoki skill + subagent **toolkit** (source of truth) | the Codex-standard location |
| `.claude/skills/<name>` · `.claude/agents/<name>.md` | relative symlinks into `.agents/` | so Claude Code discovers them |
| `.gitignore` | appended with kitsoki runtime entries | keeps sessions/artifacts out of git |

The layout mirrors the kitsoki repo's own convention exactly: `.agents/` is the
source of truth, `.claude/` is relative symlinks into it. Agent definitions are
Claude-specific, but they are still sourced from `.agents/agents` and linked the
same way (see the repo `AGENTS.md`).

The skills, agents, and base stories are **embedded in the binary**
(`internal/baseskills`, `internal/basestories`), so onboarding works in a
project that has no kitsoki source checkout on disk — the binary is the only
dependency.

---

## Two ways to run it

### 1. The onboarding pipeline (recommended)

The [dev-story](../stories/dev-story/README.md) hub ships a four-room onboarding
pipeline that **discovers** your project, lets you **review** the profile, then
**applies** everything above. Run the **embedded** dev-story from your project
root — no kitsoki checkout required, only the binary on PATH — and type an
onboarding request:

```sh
cd ~/code/my-project
kitsoki run @kitsoki/dev-story
#   > onboard .                 # or: onboard ~/code/my-project
#   > continue                  # review the discovered profile
#   > continue (confirm)        # apply: writes config + instance + toolkit + MCP
```

If the toolkit + MCP install fails (e.g. the binary was built without `make
embed-skills`), onboarding routes to a loud `init_tools_failed` read-out — it
will **not** silently report success — from which you can retry or finish later
with `kitsoki project-tools install`.

Discovery infers the project id, title, stack, and dev/test/build commands; the
apply step writes the files and runs the toolkit + MCP install. The full
mechanics — rooms, the discovery/apply scripts, the world keys, the no-LLM flow
fixture — are in
[stories/dev-story-onboarding.md](stories/dev-story-onboarding.md).

Headless equivalent (no TUI), useful for scripting or CI:

```sh
APP=@kitsoki/dev-story
kitsoki session create   --app "$APP" --key local:onboard
kitsoki session continue --app "$APP" --key local:onboard \
    --intent work --slots '{"request":"onboard /abs/path/to/my-project"}'
kitsoki session continue --app "$APP" --key local:onboard --intent init_discovered
kitsoki session continue --app "$APP" --key local:onboard --intent confirm_init
kitsoki session continue --app "$APP" --key local:onboard --intent init_applied
```

### 2. Just the toolkit + MCP — `kitsoki project-tools install`

If you only want the agent toolkit and the studio MCP (you already have an
instance, or you onboarded an older repo before this step existed), run the
standalone command the apply step calls:

```sh
cd ~/code/my-project
kitsoki project-tools install --target .
#   skills: 17 linked into .claude/skills
#   agents: 2 linked into .claude/agents
#   mcp:    registered kitsoki server in .../my-project/.mcp.json
```

It is idempotent: source trees are refreshed from the binary, our own symlinks
are re-pointed, an existing `.mcp.json` is **merged** (other servers preserved),
and a real file a human placed at a link path is left untouched. Add `--json`
for a machine-readable report.

---

## After onboarding — using it

**Run your instance.** From the project root, the default story is your new
instance:

```sh
kitsoki run                                # profile-driven implicit dev-story root
kitsoki run .kitsoki/stories/<id>-dev/app.yaml  # materialized snapshot, useful once edited
```

The implicit root reads `.kitsoki/project-profile.yaml`: command gates,
host-interface bindings, PRD/design placement, and ticket policy come from that
single profile. The materialized `.kitsoki/stories/<id>-dev/app.yaml` is still
checked in so teams can extend it deliberately, but the profile is the reusable
convention source.

For reusable onboarding tests, keep `onboarding.baseline_commit` pinned to the
commit before Kitsoki files were introduced and `first_onboarding_commit` pinned
to the first onboarding commit. Flow/cassette tests can replay from the baseline
with no LLM; real-LLM recording runs should be explicit and gated by the
profile's `recording_policy`.

**Drive kitsoki from your coding agent.** With `.mcp.json` registered, an MCP
client (Claude Code, Cursor, Claude Desktop) attached to this repo gets the
kitsoki **studio** tools — author/validate/test stories, drive sessions, render
the TUI/web — all through one facade. See
[architecture/mcp-studio.md](architecture/mcp-studio.md). The
`kitsoki-mcp-driver` agent (installed into `.claude/agents/`) is purpose-built
to orchestrate kitsoki entirely through that surface.

**Use the skills.** The installed skills (`.claude/skills/`) cover authoring,
debugging, UI demos/QA, dogfooding, and more — your agent discovers them
automatically.

---

## See also

- [getting-started.md](getting-started.md) — install the toolchain + binary
  first; §5 covers choosing the LLM provider/model.
- [stories/dev-story-onboarding.md](stories/dev-story-onboarding.md) — the
  onboarding pipeline in detail (the dev-story `init` rooms).
- [../stories/dev-story/README.md](../stories/dev-story/README.md) — the
  dev-story hub your instance imports.
- [architecture/mcp-studio.md](architecture/mcp-studio.md) — the studio MCP that
  `.mcp.json` registers.
