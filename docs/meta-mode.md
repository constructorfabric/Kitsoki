# Meta mode — Persistent Sidebar Conversations with Named Agents

A **meta mode** is a named overlay on a running kitsoki app. The user
fires `/meta` and steps off the story into a multi-turn conversation
with a specific agent — most often `story-author`, who can edit the
story directly. When the user fires `/onpath` the overlay closes, the
orchestrator resumes from the state it was paused at, and any files
the agent touched are reloaded.

Meta mode replaces the old single-shot **edit mode**. Where edit mode
allowed exactly one prompt → diff → apply cycle, a meta-mode chat is a
persistent conversation backed by a chat row in `internal/chats/`. The
agent and the user can refine across as many turns as they like, and
re-triggering `/meta` from the same state resumes the same chat
exactly where it was left.

This document is the user-facing reference. For the conceptual model
of states, intents, and the FSM proper, see
[`state-machine.md`](state-machine.md); meta mode is the richer
surface that supersedes the bare off-path escape hatch described in
§11 of that doc.

---

## 1. What it is

Meta mode is an **overlay**. While a meta-mode session is active the
orchestrator's FSM is paused; the TUI hands the prompt and transcript
to the meta controller (`internal/metamode`). The agent receives the
user's message together with a per-turn snapshot of where the player
was (state, view, world, app file, recent trace). The agent talks
back, optionally editing files in the story directory. On exit the
TUI re-renders the saved state and play resumes — unchanged unless the
agent edited a file the orchestrator reloads from.

The TUI surfaces meta sessions in two ways:

- A banner pinned above the chat transcript (e.g. `*** meta:story —
  discussing cloak-of-darkness ***`).
- A side-panel "Meta mode" cheatsheet that replaces the on-path
  actions menu while in meta. The cheatsheet lists `/onpath`,
  `/meta list`, `/meta new`, and `/meta resume <id>` so the user
  always sees what they can do.

Meta sessions are **persistent by default**. They live in the chats
store and can be listed, resumed, archived, and inspected from the
CLI exactly like oracle chats. The key is
`(AppID, "meta:<modeName>", scopeKey=state_path)` — same state means
the same chat resumes; a different state opens a new one.

---

## 2. The two YAML blocks

Meta mode is configured by two top-level blocks on the app manifest:
`agents:` (declarative agents — system prompt + tools + cwd) and
`meta_modes:` (named overlays that pick one of those agents and add
trigger / banner / persist / return policy). YAML parsing is strict:
unknown fields fail load.

### 2.1 `agents:` — per-context agent definitions

```yaml
agents:
  story-author:                            # overrides the bundled builtin
    system_prompt_path: "./agents/story_author.md"
    model: claude-opus-4-7
    tools:
      - authoring.propose
      - authoring.apply
      - authoring.discard
    cwd: ""

  jira-mentor:
    system_prompt: |
      You are a senior engineer mentoring junior developers as they
      pick up tickets. Be concise. Ask clarifying questions before
      proposing code.
    tools:
      - tickets.get
      - chat.resolve

  weather-bot:
    system_prompt_path: "./agents/weather.md"
    cwd: "${WEATHER_FIXTURE_DIR}"
    tools:
      - host.weather.forecast
```

Fields on one `AgentDecl`:

| Field                | Required | Notes |
|----------------------|----------|-------|
| `system_prompt`      | xor      | Inline prompt body. Mutually exclusive with `system_prompt_path`. |
| `system_prompt_path` | xor      | Path to a markdown file, **relative to the YAML file's directory**. Loaded at startup; missing file is a load error. |
| `model`              | no       | Model override (e.g. `claude-opus-4-7`). Empty string inherits the host default. |
| `tools`              | no       | Tool allow-list. Short (`authoring.propose`) or fully-qualified (`host.authoring.propose`) — the loader normalises to the fully-qualified form. |
| `cwd`                | no       | Working directory for the agent's tool calls. `os.ExpandEnv`-expanded at load time; an unset `${VAR}` reference is a load error (never silently empty). |

A declaration whose name matches a kitsoki builtin (today only
`story-author`) **replaces** the builtin for that app. Unknown agent
references — `meta_modes[*].agent`, `off_path.agent`, future
selection sites — fail load with the known-agents list in the error
message.

### 2.2 `meta_modes:` — named overlays

```yaml
meta_modes:
  story:
    trigger: meta
    label:   "improve the story"
    banner:  "*** meta:story — discussing cloak-of-darkness ***"
    agent:   story-author
    persist: true
    cwd:     ""
    tools:                                 # optional override of agent.Tools
      - authoring.propose
      - authoring.apply
    return:
      intent:  onpath
      message: "back on the path."
```

Fields on one `MetaModeDef`:

| Field             | Required | Notes |
|-------------------|----------|-------|
| `trigger`         | yes      | Slash command suffix. `trigger: meta` becomes `/meta`. Triggers do NOT register a global intent; they are TUI slash commands only. Trigger collisions across meta modes and with declared `intents:` names are load errors. |
| `agent`           | yes      | Name of an agent in `agents:` or a builtin. Validated at load time. |
| `label`           | no       | Human label for listings and side-panel chrome. Used as the chat row's title when set; otherwise the mode name is the title. |
| `banner`          | no       | Text rendered above the chat transcript while the overlay is active. If unset, the TUI synthesises `*** meta:<name> ***`. |
| `persist`         | no       | Tri-state. Unset (`nil`) defaults to `true`. Explicit `false` opts out of persistence (the controller still uses the same chat row shape but the intent is "ephemeral conversation"). |
| `cwd`             | no       | Working directory override for the agent in this mode. Env-expanded at load time. Falls through to `agent.cwd`, then to `dir(app_file)` per turn. |
| `tools`           | no       | Tool allow-list override. When unset the mode inherits `agent.tools`. |
| `return.intent`   | no       | Exit slash command. Defaults to `onpath`, i.e. `/onpath`. |
| `return.message`  | no       | A short note surfaced in the transcript on exit, above the re-rendered state view. |

The proposal-era field `Persist *bool` is realised through
`MetaModeDef.PersistOrDefault()` — author intent reads as nil → true,
explicit false to opt out. The default exit intent is similarly read
through `MetaModeDef.ExitIntentOrDefault()` which returns `"onpath"`
when `return` or `return.intent` is unset.

---

## 3. The `story-author` builtin

Kitsoki ships one bundled agent: `story-author`. Its prompt lives at
[`internal/agents/story_author.md`](../internal/agents/story_author.md)
and is embedded into the binary via `//go:embed`. Tool allowlist:
`host.authoring.propose`, `host.authoring.apply`,
`host.authoring.discard`. Model and cwd inherit from the host
defaults.

The agent's role is to be the conversational editor for a story. The
prompt teaches it the kitsoki app layout (`app.yaml`, `rooms/`,
`prompts/`, `scripts/`), the schema invariants (every `invoke:` in
`hosts:`, every `world.*` reference declared, dotted vs slash target
paths), and the [context]-preamble protocol (see §6) so it can pin
edits to the right file. It runs with its **normal Claude toolset**
(`Read`, `Glob`, `Grep`, `Bash`, `Edit`, `Write`, …); the
authoring-tool tokens are kept warm as a legacy path but the current
prompt instructs the agent to just edit files directly.

To override `story-author` for one app — different house style, a
narrower tool surface, a domain-specific cwd — declare an `agents:`
entry with the same name:

```yaml
agents:
  story-author:
    system_prompt_path: "./agents/our_story_author.md"
    tools: [host.authoring.propose, host.authoring.apply]
```

### 3.1 Other builtins

Three more agents ship pre-registered alongside `story-author`:

- **`default-oracle`** — vanilla helpful-assistant prompt with no
  tools and no privileged surface. Acts as the unspecified-caller
  fallback (off-path runtime once it ships LLM dispatch; future
  free-form chat). Apps override under the same name to give
  unspecified callers a different default.
- **`kitsoki-engineer`** — edits Go code in the kitsoki repo, runs
  the test suite, and (with user direction) opens PRs. Tool surface:
  claude's filesystem + shell built-ins (`Read`, `Write`, `Edit`,
  `Bash`, `Glob`, `Grep`). DefaultCwd is `${KITSOKI_REPO}`; the
  agent is reached through the builtin `kitsoki.edit` meta mode and
  is silently omitted when that env var isn't set.
- **`kitsoki-explainer`** — read-only sibling of `kitsoki-engineer`
  (`Read`, `Glob`, `Grep` only). Reached through `kitsoki.ask`.
- **`story-bug-reporter`** — gathers reproduction context and files a
  story bug by invoking `kitsoki bug create --target story` (see
  `cmd/kitsoki/bug.go`). Tool surface:
  `Bash(kitsoki bug create*)` — a single-command pattern that
  forbids the agent from running anything else. Reached through
  the builtin `story.bug` meta mode.
- **`kitsoki-bug-reporter`** — same shape against `--target kitsoki`,
  reached through the builtin `kitsoki.bug` meta mode.
- **`story-explainer`** — read-only sibling of `story-author`
  (`Read`, `Glob`, `Grep` only). Reached through `story.ask`.

### 3.2 Builtin meta_modes

The loader injects six meta_modes that every app gets without
declaring them in YAML (mirrors the agent-builtin pattern, see
`internal/app/builtin_meta_modes.go`). Map keys follow a
`group.verb` convention so a single namespace (`story.*`,
`kitsoki.*`) can carry multiple verbs (`edit`, `ask`, `bug`)
without inventing ad-hoc names. Each group has a `default` verb
that bare `/meta <group>` resolves to (`edit` for both builtin
groups):

- **`story.edit`** — `/meta story edit` (default verb for `story`,
  so bare `/meta story` resolves here). Agent `story-author`.
  Per-app keying.
- **`story.ask`** — `/meta story ask`. Read-only Q&A about the
  story, agent `story-explainer`, tools `Read`/`Glob`/`Grep`.
- **`story.bug`** — `/meta story bug`. Files a story bug via
  `kitsoki bug create --target story`; agent
  `story-bug-reporter`.
- **`kitsoki.edit`** — `/meta kitsoki edit` (default verb, so
  bare `/meta kitsoki` resolves here). Agent `kitsoki-engineer`,
  cwd `${KITSOKI_REPO}`. Chat row keys against the synthetic
  app_id `kitsoki-self` so the conversation persists across every
  app — a `kitsoki.edit` session started while playing cloak is
  the same row the user reopens while playing dev-story.
- **`kitsoki.ask`** — `/meta kitsoki ask`. Read-only Q&A about
  kitsoki source, agent `kitsoki-explainer`.
- **`kitsoki.bug`** — `/meta kitsoki bug`. Files a kitsoki bug
  via `kitsoki bug create --target kitsoki`; agent
  `kitsoki-bug-reporter`.

The entire `kitsoki.*` group is omitted from the injection set
when `KITSOKI_REPO` is unset.

**Oracle verb for read-only metas.** The two read-only modes above
(`story.ask`, `kitsoki.ask`) — and any app-declared meta whose agent's
`Tools` contains only read-only entries — use `host.oracle.ask` as
their underlying oracle verb (oracle-split proposal §2.3, decision
D14). The loader cross-checks: a meta whose agent carries `Edit` or
`Write` in its tool list cannot be wired to `ask`; it must use
`host.oracle.converse` (Phase 7) or `host.oracle.task` (Phase 4)
instead. When declaring a read-only `/meta` overlay, set the agent's
tools to the read-only allowlist and omit `bash_profile:` unless
`Bash` is needed. The `host.oracle.ask` handler enforces the read-only
contract at call time as a safety net even if the loader check is
bypassed.

An app suppresses or replaces a builtin by declaring its own
`meta_modes.<group.verb>` with the same key. Trigger collisions
between a builtin and a user-declared mode surface through normal
validation (the injection step runs before `validateMetaModes`).
Trigger uniqueness is per-group, so `story.bug` and `kitsoki.bug`
(both trigger `bug`) coexist.

The single-token `/meta bug` and `/meta self` triggers from prior
versions are gone — no aliases, no deprecation period. There was
no user base to preserve, so the clean break is cheaper than
carrying compatibility shims. Use `/meta story bug` /
`/meta kitsoki bug` and `/meta kitsoki edit` instead.

---

## 4. Per-call agent selection

Outside meta mode, any state's `on_enter:` or transition `effects:`
can target an agent for a single LLM call via the `agent:` argument
on any `host.oracle.*` verb:

```yaml
states:
  forecast:
    on_enter:
      - invoke: host.oracle.converse
        with:
          agent: weather-bot          # named agent from agents:
          args:
            location: "{{ world.where }}"
          chat_id: "{{ world.chat_id }}"
          question: "What is the forecast for {{ world.where }}?"
        bind: { answer: answer }
```

When `agent:` is set, the handler resolves the name in the
process-wide registry (`host.SetAgentRegistry` is called at startup
by `cmd/kitsoki`), renders the agent's `SystemPrompt` through `expr`
with the caller's `args:` map, and uses the agent's `Tools` as the
MCP tool allowlist hint plus the agent's `DefaultCwd` as `working_dir`
when the caller does not supply one.

Constraints:

- `agent:` and `prompt_path:` / `prompt:` are **mutually exclusive**.
  Setting both is an error (`agent: and prompt_path: ... are mutually
  exclusive`).
- Unknown agent names are an error, **not** a silent fall-back to
  prompt-driven dispatch. The error includes the list of registered
  agents so authoring typos surface immediately.
- The `claude_session_id` / `chat_id` plumbing is unchanged; the agent
  path participates in the same per-chat resume flow the prompt-path
  flow already supports.

Precedence across selection sites, highest to lowest:

```
per-call `agent:` arg > meta_modes[mode].agent > off_path.agent > app default
```

Today the orchestrator-side off-path runtime still uses the legacy
prompt-driven dispatch (see §11), so `off_path.agent:` loads,
validates, but does not yet drive an LLM call. The slot is reserved
so the future runtime stays declaratively configured.

---

## 5. Slash commands

The TUI dispatches the following while the user is on path or in
meta mode. The forms are all literal — there is no intent-router
involvement.

| Command                  | Where                | Effect |
|--------------------------|----------------------|--------|
| `/meta`                  | on-path              | Enters the lexicographically-first declared meta mode. Equivalent to typing `/meta <first-name>` explicitly. |
| `/meta <name>`           | on-path              | Enters the named meta mode. Unknown names produce a polite "(meta mode: unknown name)" hint. |
| `/meta list`             | on-path or meta      | Inline-lists every meta chat for this app. Columns: `ID` (first 8 chars), `MODE`, `SCOPE` (state path), `UPDATED` (`YYYY-MM-DD HH:MM` local), `PREVIEW` (first 50 chars of the first user turn). Archived rows are excluded. |
| `/meta new`              | meta only            | Archives the active chat row (status → `archived`) and opens a fresh one in the same `(mode, scope)`. The transcript is reset; the banner is re-emitted. Outside meta this prints a usage hint. |
| `/meta resume <prefix>`  | on-path or meta      | Resolves an ID prefix (minimum 3 chars) against the app's meta chats and enters that chat. An ambiguous prefix prints every matching ID; no chat is entered. |
| `/meta done`             | meta only            | Archives the active chat (status → `archived`) AND exits meta mode. Use when you're finished with the conversation and don't want it cluttering `/meta list` or the foyer panel. Differs from `/onpath` (which exits without archiving) and from `/meta new` (which archives + opens a fresh row). The confirmation includes the chat ID so you can recover via `/meta resume <prefix>` if you change your mind. |
| `/onpath`                | meta only            | Exits the overlay without archiving — the chat persists for resume. Default exit intent; overridable per mode via `return.intent`. The TUI prints the session-summary line (`✓ meta session: N turns, M edit(s) ...`) and re-renders the saved state. |
| `/attach`                | meta only            | Suspends the TUI and hands the terminal to a live `claude --resume <claude-session-id>` session inside tmux, against the active meta chat. The chat row's `claude_session_id` is minted on first attach (with `--session-id`); subsequent attaches use `--resume`. Detach with the tmux prefix + `d` (default Ctrl-B then d) to return to kitsoki — the tmux session keeps running with claude inside, so the conversation persists across the TUI's lifetime. While attached, the tmux status bar shows `kitsoki ❘ <chat>` on the left and an inbox-count badge on the right (severity-coloured for action-required vs info). See [`claude-code-sessions-proposal.md`](proposals/claude-code-sessions-proposal.md) §4.2 / §9.3 for the design context. |
| `/sessions list`         | on-path or meta      | Prints a styled, numbered table of every active claude session on this host (every `chat_pty_sessions` row, attached or background). Columns: `#`, `CHAT`, `MODE` (`attached` or `background`), `IDLE` (`HH:MM:SS` plus `"(Nm ago)"` when stale >5 min), `SCOPE`. The numbering is cached on the TUI so `/sessions attach <N>` can resolve it without typing chat IDs. |
| `/sessions attach <N>`   | on-path or meta      | Suspends the TUI and attaches to session `<N>` from the most recent `/sessions list` output. Same handoff lifecycle as `/attach` but lets you hop between background claude conversations across chats (including cross-app `self` chats) without leaving the TUI. Detach with the tmux prefix + `d`. |

Mode dispatch uses exact match on the first slash arg, so a meta
mode literally named `list`, `new`, `resume`, or `done` would be
unreachable via `/meta <name>` (the subcommand wins). Pick mode
names outside that reserved set.

When a meta-mode session is active the side panel renders the
"Meta mode" cheatsheet — the on-path actions menu is irrelevant
because the FSM is paused. Other slash commands (e.g. `/inbox`,
oracle entry) are deliberately not processed while in meta.

---

## 6. What the agent receives each turn

The controller prepends a `[context]` preamble to every user
message before handing it to the oracle. The literal text of the
user's message lives inside a `[user]` block. Persisted transcripts
contain only the user's text — the preamble is a per-turn derived
artefact, not author-written.

```text
[context]
state: bar.dark
app_file: /abs/path/to/app.yaml
trace_file: /tmp/kitsoki-meta-trace-1234.jsonl
view: |
  It's pitch dark. You can't see a thing.
world:
  disturbance: 0
  wearing_cloak: true
[/context]

[user]
make the cloakroom warmer in tone
[/user]
```

Fields:

- `state` — the FSM state path (`main.foyer`, `bar.dark`) captured at
  the moment the user submitted the turn.
- `app_file` — absolute path to the manifest YAML. The agent uses
  this to know which file to edit; the legacy `authoring.propose`
  dispatch also auto-fills it from this value when omitted.
- `trace_file` — absolute path to a JSONL file the engine keeps
  current with recent session events (state transitions, host calls,
  intent routings, world mutations). The agent can `Read` it to
  recover session history without asking the user. The path is either
  a per-session temp file the TUI rewrites on every `Send` from the
  in-memory `RingBuffer`, or — when `--trace <path>` is set — the
  exact same file the engine is already keeping. Both come from
  `internal/trace`.
- `view` — the literal markdown view the user is staring at, rendered
  as a YAML literal block so multi-line text survives without escape
  gymnastics.
- `world` — every resolved world variable, keys sorted, values
  truncated to 200 runes with a trailing `…` when cut.

Empty fields are omitted entirely — the preamble never carries
placeholder lines like `state: ""`. When all fields are empty (a
non-TUI caller, e.g. `kitsoki turn` in a unit test) the preamble is
suppressed altogether.

The story-author prompt explains all of this to the agent. Custom
agents written to the same protocol get the same preamble for free —
nothing in the preamble is `story-author`-specific.

---

## 7. How edits land

There is no proposal-review step. The agent runs with its standard
Claude toolset (`Read`, `Write`, `Edit`, `Bash`, …) and `cwd` set to
the story directory. When the user agrees to a change, the agent
just edits the file in place.

The controller snapshots the story directory tree (mtime + size of
every file, recursively, skipping hidden dirs and `node_modules`)
**before** the LLM call and again **after**. Any file whose stat
changed is recorded on `SendResult.ChangedFiles` and triggers an
orchestrator reload before the next turn. The reload is a full
re-validation: the loader parses the manifest fresh, the orchestrator
swaps in the new `AppDef`, and the current state is rebound if it
still exists in the new graph.

The TUI surfaces this in two places:

- After each turn that produced a reload:

  ```text
  (✓ saved + reloaded — edit #1 this session)
    changed: app.yaml, prompts/intro.md
  ```

  If the current state no longer exists in the reloaded app, an
  extra line appears:

  ```text
  (your current state no longer exists in the new app — restart
  to enter the new graph)
  ```

- On exit (the `/onpath` summary):

  ```text
  (✓ meta session: 3 turns, 2 edit(s) applied + reloaded)
    files touched: app.yaml, prompts/intro.md, rooms/bar.yaml
  ```

  When no files changed: `(meta session: 3 turns, no file
  changes)`. Zero-turn sessions show `(meta session: no turns)`.

The legacy structured-token dispatcher (`<<<propose>>>` /
`<<<apply>>>` / `<<<discard>>>` blocks parsed out of the agent's
reply, see `internal/host/authoring_tools.go`) is still wired up so
older chats that resume with the previous prompt continue to work,
but the bundled `story-author.md` no longer documents the protocol.
New chats are driven entirely by direct file edits.

---

## 8. Chat persistence

Every meta-mode session is backed by a row in the chats store keyed
by:

```
(AppID, room = "meta:<modeName>", scopeKey = state_path)
```

In practice:

- Re-triggering `/meta story` from the same state resumes the same
  chat — the agent sees the prior turns, the `claude_session_id` is
  threaded through with `--resume`, and the proposal ledger persists
  across reentries.
- A different entry state opens a different chat. `/meta story` from
  `foyer` and `/meta story` from `bar.dark` are independent
  conversations.
- `/meta new` archives the active row (status → `archived`) and
  resolves a fresh one with the same `(AppID, room, scopeKey)`. The
  archived row stays inspectable via `/meta resume <prefix>` or via
  the CLI.

From outside the TUI:

```bash
# List every meta chat for the app, including archived rows
kitsoki chat list --room meta:story --all-status

# List only active meta chats
kitsoki chat list --room meta:story

# Filter to a single entry state
kitsoki chat list --room meta:story --scope bar.dark
```

The `--room` matcher takes the exact room key, so `--room meta:story`
matches only the `story` mode's chats. To inspect every mode at
once use a prefix match through the chats CLI's existing
`--scope-prefix` plumbing; that surface is unchanged from oracle
chats.

The chats store schema is the same one oracle and other chat-shaped
flows already use — see `internal/chats/` for the persistence
contract.

---

## 9. Where the implementation lives

| Concept                                  | Code |
|------------------------------------------|------|
| YAML schema (`agents:`, `meta_modes:`)   | `internal/app/types.go`, `internal/app/loader.go` |
| Agents registry + `story-author` builtin | `internal/agents/` |
| Meta-mode controller (Enter / Send / Exit, ledger, tree-diff reload, WithLock integration so meta turns serialize against drive dispatch / `chat continue` / `/attach`) | `internal/metamode/controller.go` |
| TUI overlay, slash-command dispatch, `/meta list` rendering, `/attach` + `/sessions list|attach` | `internal/tui/metamode.go`, `internal/tui/tui.go`, `internal/tui/meta_attach.go`, `internal/tui/sessions.go` |
| Per-call `agent:` arg on `host.oracle.*` verbs | `internal/host/oracle_ask.go`, `internal/host/oracle_decide.go`, `internal/host/oracle_extract.go`, `internal/host/oracle_task.go`, `internal/host/oracle_converse.go` |
| Authoring-tool dispatcher (legacy structured tokens) | `internal/host/authoring_tools.go` |
| Trace ring buffer + per-turn dump        | `internal/trace/ringbuffer.go` |
| Process-wide wiring (registry install, trace file, chat store, tmux client, inbox watcher) | `cmd/kitsoki/main.go` |
| Chat-attach lifecycle (chat lock → ensure tmux session → `pty_attached` → heartbeat → runTmux callback → `pty_background`) | `internal/chatattach/attach.go`, `internal/chatattach/kitsoki-tmux.conf` |
| tmux wrapper (kitsoki-owned socket, `HasSession`/`NewSession`/`AttachStreaming`/`SetStatusRight`/...) | `internal/tmux/` |
| Inbox status-bar watcher (pushes severity-coloured notification counts into the attached session's tmux `status-right` every 2s while `/attach` is live) | `internal/tui/meta_attach.go:runStatusBarWatcher` |

In-tree apps with a working `meta_modes.story`:
[`testdata/apps/cloak/app.yaml`](../testdata/apps/cloak/app.yaml),
[`testdata/apps/dev-story/app.yaml`](../testdata/apps/dev-story/app.yaml),
[`testdata/apps/background_jobs/app.yaml`](../testdata/apps/background_jobs/app.yaml),
[`testdata/apps/proposal_smoke/app.yaml`](../testdata/apps/proposal_smoke/app.yaml).

---

## 10. Oracle-verb mapping for meta and off-path (oracle-split Phase 7)

The oracle-split proposal (§2.5 / D14) defines two flavours for the
oracle call that backs each meta turn:

| Flavour | Oracle verb | Permission surface |
|---|---|---|
| Read-only metas (inspect, explain, Q&A) | `host.oracle.ask` | Read-only tool surface; enforced by loader |
| Free-form metas (edit files, run tools) | `host.oracle.converse` | `permission_mode:` gates mutation |

**Today** all meta turns go through the controller's built-in dispatch
path (not through a raw `host.oracle.*` effect). The mapping above is
the *declarative target* — the shape that `/meta <name>` entries will
declare in `app.yaml` once the controller is updated to route through
the oracle surface:

```yaml
meta_modes:
  advisor:
    agent: bugfix-advisor
    oracle_verb: converse        # free-form; may mutate
    permission_mode: ask         # operator confirms each mutation

  explainer:
    agent: story-explainer
    oracle_verb: ask             # read-only; loader enforces tool surface
```

For now the controller hardcodes `--permission-mode bypassPermissions`
(see §10 Limitations below). When the controller migrates to the
`host.oracle.*` surface, free-form metas that currently use
`bypassPermissions` will declare `oracle_verb: converse` and read-only
metas will declare `oracle_verb: ask`. The loader will cross-check the
declared verb against the agent's tool surface at app-load time.

---

## 11. Limitations

What you can rely on today, and what is still incomplete. Bring fresh
eyes to these before designing a new app around them.

- **Off-path runtime does not yet honour `off_path.agent:`.** The
  loader accepts and validates the slot, but the orchestrator-side
  off-path dispatch (`internal/orchestrator/outcome.go`) is still
  marked "not yet implemented" for the LLM call. Use a `meta_modes:`
  entry today for any "named, persistent sidebar conversation"
  requirement; `off_path:` itself remains the one-shot free-form
  banner described in [`state-machine.md`](state-machine.md) §11.
- **No `background_jobs.<name>.agent:` slot exists.** The
  meta-mode proposal sketched a first-class `background_jobs:` YAML
  type with an `agent:` field; that type has not landed. Background
  jobs today are declared via `effects:` (`background: true`) on
  transitions and do not select an agent. The slot will land when
  background jobs become a first-class declarative type (separate
  proposal, not part of meta mode).
- **One active meta mode at a time.** Nested overlays are not
  supported. Entering meta from inside meta is undefined; the TUI
  treats `/meta <name>` while already in meta as a usage hint.
- **Tool-gating is not enforced.** Every claude subprocess runs with
  `--permission-mode bypassPermissions`. An agent's declared `tools:`
  list is informational — it documents the intended toolset for
  prompt authors and code reviewers, but claude is free to call
  anything its built-in surface exposes. Treat the list as a
  contract between the prompt and the reviewer, not as a runtime
  guardrail. A future iteration may switch to `dontAsk` +
  `--allowed-tools` + `--disallowed-tools` when an agent's blast
  radius actually demands enforcement (e.g. `self` mode pushing
  commits).

What ships with kitsoki today (formerly under "Limitations"):

- **The `story.*` and `kitsoki.*` groups are builtin** (see §3.2).
  `kitsoki.edit` uses the `kitsoki-engineer` agent rooted at
  `${KITSOKI_REPO}` and keys its chat against a synthetic `app_id`
  so the conversation is the same row across every running app;
  `story.bug` uses the `story-bug-reporter` agent which files
  reports via `kitsoki bug create --target story` under the running
  app's `issues/bugs/` directory (and `kitsoki.bug` does the same
  against `${KITSOKI_REPO}/issues/bugs/`). Apps override any builtin
  by declaring a `meta_modes.<group.verb>` with the same key.
- **The foyer "meta sessions" panel** is available from the
  Esc-menu's "Meta sessions" entry. It lists every active meta
  chat the controller can see — including cross-app `kitsoki.*` chats
  merged in by `Controller.ListChats` — and resumes the picked
  row without typing `/meta resume <id>`.
- **`/attach` hands the terminal to claude.** While `/meta`
  drives turns through kitsoki's Go-side oracle adapter (with
  file-change detection and orchestrator reload), `/attach`
  flips the same chat row into a tmux-hosted `claude --resume`
  pane so the user gets claude's native UI. Detach with
  `Ctrl-B then d`; claude keeps running in the background.
  `/sessions list` and `/sessions attach <N>` let you hop
  between any active claude sessions — meta or otherwise — by
  position rather than chat ID. The kitsoki-shipped tmux config
  (`internal/chatattach/kitsoki-tmux.conf`) gives the attached
  pane a `kitsoki ❘ <chat>` status bar with severity-coloured
  inbox counts on the right; a watcher goroutine refreshes that
  count via `tmux set-option status-right` every 2 s while
  attached. The full kitsoki-rendered chrome (vt-emulator embed,
  proposal §8) is **not** built — tmux's own status bar carries
  the kitsoki identity in v1, with the chat-attach lifecycle in
  `internal/chatattach/`.
- **`host.chat.drive` is registered as a builtin host.** A
  state-machine effect can enqueue (or sync-run) a turn against
  any chat, with `chat_ref` resolution and a `timeout_seconds`
  lock-retry budget for the `await:true` path. The drive row
  carries an `on_complete` chain when the orchestrator sets one,
  but the orchestrator's firing-side consumer is not wired yet
  (followup work).
