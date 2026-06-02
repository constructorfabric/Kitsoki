# Built-in Host Handlers

Hosts are the only escape hatch from the pure machine. Apps invoke them
through effects; the orchestrator dispatches to the registry; the
handler returns a `Result` that may carry `Data` (a typed map) and/or
an `Error` envelope.

This document is the user-facing reference for every built-in. The
authoritative source is `internal/host/`. To extend the registry with
your own handler, see
[`developer-guide.md` §5.2](developer-guide.md#52-adding-a-new-built-in-host-handler).

For the effect-level shape (`invoke:`, `with:`, `bind:`, `on_error:`,
`background:`, `on_complete:`) see `kitsoki docs app-schema`.

For named-capability composition (`host_interfaces:` declared on a
sub-story, rebound by importers) see [`imports.md`](imports.md) §11.

For invoking oracle handlers directly from scripts, CI jobs, or
validator subprocesses — without a running state machine — see
[`docs/oracle-cli.md`](oracle-cli.md). That document covers
`kitsoki oracle <verb>`, `kitsoki oracle-serve` (unix-socket daemon),
the JSON-RPC method shapes, and `KITSOKI_SESSION_ID` trace continuity.

## Registry dispatch and prefix-fallback

The host registry resolves handler names via exact match first, then
falls back to the longest registered prefix split on `.`. So
`Get("host.diary.announce")` returns:

1. The handler registered exactly at `host.diary.announce` if any.
2. Otherwise the handler registered at `host.diary` if any.
3. Otherwise not found.

This makes multi-op `host_interface` dispatch work without forcing
authors to register every `<binding>.<op>` combination. Register one
handler per op when each op has a different surface; register a single
carrier handler when the op name is dispatched from `with:` args.

---

## Cheat sheet

| Handler | Purpose |
|---|---|
| [`host.run`](#hostrun) | Execute a shell command in a working directory. |
| [`host.oracle.extract`](#hostoracleextract) | Tiered resolver: synonyms → slot_template → llm. Returns typed JSON + `resolved_by`. |
| [`host.oracle.ask`](#hostoracleask) | Read-only inspection call: read tools + Bash under a profile; no mutation. Returns prose + optional typed JSON. |
| [`host.oracle.decide`](#hostoracledecide) | Typed LLM verdict (schema required; submit auto-attached; read-only tools optional). |
| [`host.oracle.task`](#hostoracletask) | Agentic verb with full tool surface, acceptance loop, and replay artifacts (Mode A/B/C). |
| [`host.oracle.converse`](#hostoracleconverse) | Free-form conversational Claude session with permission_mode control. |
| [`host.transport.post`](#hosttransportpost) | Post a message to a registered transport (TUI / Jira / Bitbucket). |
| [`host.workspace_manager.get`](#hostworkspace_managerget) | Load a structured workspace context (repos, issue, PRs). |
| [`host.jobs.answer_clarification`](#hostjobsanswer_clarification) | Resume a paused background job with the user's answer. |
| [`host.chat.resolve`](#hostchatresolve) | Get-or-create a persistent chat thread for a `(app, room, scope_key)`. |
| [`host.chat.list`](#hostchatlist) | List chat threads matching `(app, room, scope_key)`. |
| [`host.chat.transcript`](#hostchattranscript) | Fetch a chat's transcript. |
| [`host.chat.create`](#hostchatcreate) | Explicitly create a new chat thread. |
| [`host.chat.fork`](#hostchatfork) | Fork a chat — copy messages, fresh Claude session. |
| [`host.chat.archive`](#hostchatarchive) | Soft-delete a chat. |
| [`host.chat.rename`](#hostchatrename) | Update a chat's title. |
| [`host.chat.suggest_title`](#hostchatsuggest_title) | Ask Claude to propose a title from the transcript. |
| [`host.chat.resolve_ref`](#hostchatresolve_ref) | Resolve a chat reference (id, alias, or "current") to a chat row. |
| [`host.chat.drive`](#hostchatdrive) | Enqueue a turn against a chat; optionally `await` completion. |

Every handler must be present in the app's top-level `hosts:`
allow-list to be invokable.

---

## host.run

Execute a shell command or, in argv mode, a program with explicit
arguments. The default `host` for "shell out and capture stdout".

| Field | Type | Required | Notes |
|---|---|---|---|
| `cmd` | string | yes | The program (argv-mode) or shell command (bash-mode). |
| `args` | list | no | Present → argv-mode: `cmd` is exec'd directly with these positional args, no shell. Use this whenever an argument is templated from world or slot data. |
| `cwd` | string | no | Working directory. |
| `fail_on_error` | bool | no | Default `false`. When `true`, a non-zero exit populates `Result.Error` so `on_error:` fires instead of returning success-with-data. |

Returns:

| Field | Type | Notes |
|---|---|---|
| `stdout` | string | Combined stdout/stderr. |
| `exit_code` | int | |
| `ok` | bool | True iff `exit_code == 0`. |
| `stdout_json` | any | Set when stdout's last non-empty line parses as a single JSON document. Lets CLIs that emit a structured envelope be bound directly via `bind: foo: stdout_json`. |
| `stdout_json_parse_error` | string | Set (and `stdout_json` absent) when the last line looked like JSON but failed to parse. |

### Background usage

`host.run` is the canonical example for `background: true`. The
`stdout` / `exit_code` / `stdout_json` fields end up in
`world.last_job_result` when the job terminates.

---

## Oracle verb summary

Five verbs ordered by blast radius. Pick the narrowest one that fits.

| Verb | Blast radius | Schema required | Mutation | Transcript |
|---|---|---|---|---|
| `host.oracle.extract` | Deterministic-first | yes | no | no |
| `host.oracle.decide` | LLM-only verdict | yes | no | no |
| `host.oracle.ask` | LLM inspection | optional | no | no |
| `host.oracle.task` | Agentic write | yes (acceptance) | yes | journal |
| `host.oracle.converse` | Open conversation | no | optional | ChatStore |

**Choosing a verb:**

1. Can a synonym list or slot template answer the input? → `extract`.
2. Does the call require a typed structured verdict with no file mutations? → `decide`.
3. Do you just need prose or an optional typed annotation from a read-only agent? → `ask`.
4. Does the agent need to edit files, run commands, or loop until a `submit()` is accepted? → `task`.
5. Is this a multi-turn conversation the user drives? → `converse`.

All five verbs share the same streaming path (`OracleStreamer.Run`), the same
agent-declaration lookup, and the same `KITSOKI_SESSION_ID` propagation. The
persona table pattern — one named agent per role, declared in `agents:` — is
documented with worked examples in `stories/bugfix/AGENT-BRIEF.md` and
`stories/bugfix/README.md`.

---

## host.oracle.ask

Read-only inspection verb (oracle-split Phase 3). The LLM gets read
tools — Read, Grep, Glob, WebFetch, WebSearch, Bash under a profile,
read-only MCP servers — but cannot mutate anything. One-shot; no
transcript persistence. Returns prose; returns typed JSON too when
`schema:` is set.

| Field | Type | Required | Notes |
|---|---|---|---|
| `prompt_path` (or `prompt`) | string | yes | Path to a prompt template file. Relative paths resolve against `KITSOKI_APP_DIR`; absolute paths are used as-is. |
| `agent` | string | no | Name of an entry in `agents:`. Supplies `SystemPrompt`, `Model`, `Tools`, `BashProfile`, `DefaultCwd`. |
| `system_prompt` | string | no | Inline persona; wins over `agent.SystemPrompt` when both are set. |
| `working_dir` | string | no | CWD for the spawned `claude`. Precedence: per-call > `agent.DefaultCwd` > prompt file directory. |
| `args` | map | no | Explicit prompt-template variables. Surfaced as `{{ args.X }}` inside the prompt. Falls back to the full call-args map for legacy compatibility. |
| `schema` | string | no | Path to a JSON schema. When set, kitsoki attaches a `submit` MCP tool and returns `submitted` alongside `stdout`. |
| `tools` | list of string | no | Per-call tool override. Wins over `agent.Tools` (D5). Must still be a subset of the read-only allowlist; `Edit` and `Write` are always rejected. |

Returns:

| Field | Type | Notes |
|---|---|---|
| `stdout` | string | Claude's text reply (source-color wrapped). |
| `exit_code` | int | Claude's exit code. |
| `ok` | bool | True iff `exit_code == 0`. |
| `submitted` | any | Parsed JSON payload. Present only when `schema:` is set and the LLM called `submit()`. |

### Tool surface

The handler enforces the read-only contract at two levels:

1. **Loader** — rejects `Edit` and `Write` in any agent's `Tools` that
   is referenced by an `ask` call.
2. **Handler safety net** — rejects mutation tools at call time
   regardless of how the call was assembled (CLI, test, direct Go call).

Allowed tools: `Read`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Bash`
(under a profile), and any MCP server whose declaration carries
`read_only: true`.

### Bash profiles

When `Bash` is in the effective tool list the agent **must** declare a
`bash_profile:`. Three profiles are supported:

| Profile | YAML shape | What it allows |
|---|---|---|
| `read-only` | `bash_profile: read-only` | Built-in allowlist: `grep`, `find`, `cat`, `head`, `tail`, `ls`, `git`, `jq`, `rg`, `wc`, `stat`, `awk`, `sed`, `sort`, `uniq`, `cut`, `tr`, `echo`, `printf`, `env`, `which`, `type`, `python3`. Shell metacharacters (`;`, `\|`, `&`, backticks, `$()`) are always rejected. |
| `commands` | `bash_profile: { commands: [git, jq] }` | Explicit argv0 allowlist. Shell metacharacters rejected. |
| `sandboxed-write` | `bash_profile: { sandboxed-write: /tmp }` | Any command; writes confined to a per-call scratch directory; network denied via `HTTP_PROXY`. |

```yaml
agents:
  failure-explainer:
    system_prompt_path: prompts/explain.md
    model: claude-sonnet-4-6
    tools: [Read, Grep, Bash]
    bash_profile:
      commands: [git, jq, grep]
```

### Read-tool snapshot cap (D9)

Every read-tool call's output is captured in the journal so the LLM
span is replayable from recording. Outputs over **256 KiB** are stored
as a `sha256` hash plus the first 4 KiB; replay detects "divergent
input" by comparing the hash, but cannot reconstruct the full bytes
from the journal alone. The cap is configurable per app (default
`ReadSnapshotCap = 256 KiB` in `internal/host/read_snapshot.go`) but
not per call. See also: `CaptureReadSnapshot`, `DigestMatches` in
`internal/host/read_snapshot.go` — these helpers are shared by
`decide` and `extract` (Phases 2 and 5).

### Examples

```yaml
invoke: host.oracle.ask
with:
  prompt_path: prompts/explain_failure.md
  working_dir: "{{ world.repo_root }}"
  args:
    failing_test: "{{ world.failure_id }}"
  agent: failure-explainer
bind:
  explanation: stdout
on_error: room_ask_failed
```

With schema (typed JSON alongside prose):

```yaml
invoke: host.oracle.ask
with:
  prompt_path: prompts/explain_failure.md
  agent: failure-explainer
  schema: schemas/explanation.json
bind:
  explanation: stdout
  classification: submitted
on_error: room_ask_failed
```

---

## host.oracle.converse

Free-form open-ended conversation with persistent transcript (oracle-split
Phase 7).

`converse` is distinct from `host.oracle.task` in that there is no
`acceptance` loop and no synthetic "done" signal — the user or the
surrounding state machine decides when the conversation ends. The
agent may have full mutation tools; what gates mutation is Claude
Code's own permission system, selected by `permission_mode:`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `question` | string | yes | The user's prompt for this turn. |
| `chat_id` | string | recommended | When set AND a ChatStore is in context, enables **chat-aware mode**: appends messages to the persistent transcript, reuses the chat row's `claude_session_id` across turns, and acquires the per-chat singleton lock. |
| `agent` | string | no | Named agent from `agents:` block. Supplies `SystemPrompt`, `Model`, `Tools`, and `DefaultCwd`. Per-call `system_prompt:` wins over `agent.SystemPrompt` (D5 precedence rule). |
| `permission_mode` | string | no | `ask` / `bypassPermissions` / `denyAll`. Default: `bypassPermissions` (matches legacy `talk` behaviour). |
| `working_dir` | string | no | CWD for the spawned `claude`. `agent.DefaultCwd` is used when this is absent. |
| `session_id` | string | no | Non-chat path only — round-tripped so the caller can persist it. |
| `system_prompt` | string | no | Per-call system prompt override; wins over `agent.SystemPrompt`. |
| `tools` | list | no | Per-call tool allowlist; wins over `agent.Tools` (D5). |

### permission_mode values

| Value | Behaviour |
|---|---|
| `ask` | Operator confirms each mutation through the TUI before the agent proceeds. |
| `bypassPermissions` | No confirmation prompts; mutations run without asking. Matches the old `talk` default. |
| `denyAll` | Mutation tools are rejected; useful for sandboxed off-path explorations. |

### background mode (D15)

`converse` preserves `background: true` (used by `dev-story`'s
`oracle_active` room for fire-and-poll submission). When `background: true`
is set on the effect, the orchestrator dispatches the handler as a
background job and binds the job ID into world. The handler itself runs
normally; `background:` is a dispatch-time flag, not a handler-level flag.

### Returns

| Field | Type | Notes |
|---|---|---|
| `answer` | string | Claude's reply text. |
| `session_id` | string | The Claude session ID (new or echoed). |
| `chat_id` | string | Echoes the input (chat-aware path only). |
| `claude_session_id` | string | Same as `session_id` (chat-aware path only). |
| `transcript_seq` | int | Seq of the assistant message row (chat-aware path only). |

### Replay semantics (D10)

`converse` spans are recorded as transcript in ChatStore, not in the
journal. Replay tooling renders them as an opaque block rather than
re-running the conversation — conversations are the artifact and do not
replay deterministically:

```
converse(chat=abc, seq=[12..18]) — 6 turns, see ChatStore
```

### Example

```yaml
invoke: host.oracle.converse
with:
  chat_id: "{{ world.chat_id }}"
  question: "{{ in.text }}"
  agent: dev-story-pair
  permission_mode: ask
bind:
  answer: answer
  transcript_seq: transcript_seq
on_error: room_converse_failed
```

---

## host.oracle.task

The agentic call. The LLM may Edit, Write, and Bash freely inside the declared
working directory. Every tool call produces a `task.tool` journal event. The
handler drives an acceptance loop until the LLM's `submit()` call passes schema
validation (plus an optional `post_cmd` verifier) or the retry budget is
exhausted.

**`agent:` is mandatory.** A task call without a named agent has no documented
tool allowlist or working directory; the loader rejects it at app-load time.

### Arguments

| Field | Type | Required | Notes |
|---|---|---|---|
| `agent` | string | yes | Named agent from the top-level `agents:` block. The agent declares tools, model, cwd, and `external_side_effect`. |
| `working_dir` | string | no | CWD for the agent subprocess; wins over `agent.DefaultCwd`. |
| `acceptance.schema` | string | yes | Path to a JSON Schema file. The LLM must call `submit()` with a payload that validates against this schema. |
| `acceptance.post_cmd` | string | no | Verifier command run after schema validation passes. Exit code 0 = accepted; non-zero = rejected (LLM gets the stdout as rejection reason). |
| `acceptance.post_cmd_args` | map | no | `{ key: value }` forwarded as `--key value` to the post_cmd subprocess. |
| `acceptance.max_retries` | int | no | Retry budget for the acceptance loop (default: 5). |
| `context.prompt` | string | no | Prompt text or path injected into the agent's first turn as stdin. |
| `context.args` | map | no | Template variables for `context.prompt`. |

### Return values

Bound via the effect's `bind:` block:

| Key | Type | Notes |
|---|---|---|
| `submitted` | any | The JSON payload the LLM passed to `submit()`. |
| `task_trace_id` | string | Child span ID pointing at the nested task trace. |
| `files_changed` | []string | Sorted list of mutated paths (git-relative when working_dir is a git tree). |
| `final_diff` | string | Unified diff of all changes (also written to the journal under `task.end`). |
| `replay_mode` | string | One of `file_diff`, `sandboxed_write`, or `external_side_effect`. See Mode A/B/C below. |

### Replay modes (Mode A/B/C)

The `replay_mode` field on the `task.end` journal event classifies the task for
replay tooling:

**Mode A — `file_diff`**  
Agent tools are limited to `Read`, `Edit`, `Write`, and `Bash` with no
`WebFetch`/`WebSearch`/non-`read_only` MCP. The task mutates only the working
directory. Replay is deterministic from `(initial_state_hash, final_diff)`:

```
git checkout <initial_state_hash> && git apply <final_diff>
```

The loader infers Mode A when the agent's tool surface contains no external
tools. The author confirms with `external_side_effect: false` on the agent
declaration.

**Mode B — `sandboxed_write`**  
Agent uses a `sandboxed-write` Bash profile (per-call scratch dir, network
denied). The trace captures both the working-tree diff and the scratch-dir
contents as a tarball appended to the journal. Replay requires the diff plus
the scratch tarball.

**Mode C — `external_side_effect`**  
Agent has unrestricted `Bash`, `WebFetch`/`WebSearch`, or write-capable MCP.
Recorded only; not replayable. The `kitsoki replay --mode file_diff` command
skips Mode C spans and prints a summary:
`"skipped N external-side-effect spans."` Authors must declare
`external_side_effect: true` on the agent; the loader infers it from the tool
surface and warns when declaration and inference disagree.

### Built-in sub-oracle MCP tools

Task agents automatically receive three built-in MCP tools scoped to the
parent session:

- `kitsoki.oracle.extract` — invoke `host.oracle.extract` as a child span
- `kitsoki.oracle.decide` — invoke `host.oracle.decide` as a child span
- `kitsoki.oracle.ask` — invoke `host.oracle.ask` as a child span

These tools ensure that sub-LLM calls by the agent join the parent trace
rather than escaping it. Their invocations appear as child spans under
the parent `task.tool` entry in the trace tree.

### KITSOKI_SESSION_ID propagation

Every subprocess spawned by the agent (the `Bash` tool, the `post_cmd`
acceptance subprocess) inherits the `KITSOKI_SESSION_ID` environment variable
from the parent. Any `kitsoki oracle <verb>` call made from within those
subprocesses attaches to the parent trace automatically.

### Journal event kinds

| Kind | When emitted |
|---|---|
| `task.tool` | Once per tool call (rolled-up; stream emits `task.tool.start` + `task.tool.end`). |
| `task.acceptance.attempt` | Once per acceptance loop iteration. |
| `task.end` | Terminal event; carries `files_changed`, `final_diff`, `replay_mode`, `initial_state_hash`. |

### Example

```yaml
invoke: host.oracle.task
with:
  agent: bug-fix-implementer
  working_dir: ".bug-fix/{{ world.ticket }}/worktree"
  acceptance:
    schema: schemas/fix_proposal.json
    post_cmd: python3 -m bugfix verify-impl
    post_cmd_args:
      ticket: "{{ world.ticket }}"
    max_retries: 5
  context:
    prompt: prompts/implement.md
    args:
      ticket: "{{ world.ticket }}"
      reproduction: "{{ world.reproduction_artifact }}"
bind:
  proposal: submitted
  task_trace_id: trace_id
  files_changed: files_changed
on_error: room_implementing_failed
```

---

## host.oracle.extract

Tiered deterministic-first resolver: maps a free-text input to a typed
JSON payload using up to three resolver tiers tried in declaration order.
First match wins.

**Tiers:**

1. `synonyms` — author-curated phrase → payload (YAML file). Case-insensitive.
   Comma-separated keys match multiple phrases to the same payload.
2. `slot_template` — slot-grammar YAML (same syntax, captures `{slot}` patterns).
3. `llm` — LLM fallback; same read-only tool surface as `host.oracle.ask`.

An optional `validator:` block runs after any tier match (read-only sandbox).
Rejection falls through to the next tier for deterministic results; for LLM it
counts as a no-match for that call.

| Field | Type | Required | Notes |
|---|---|---|---|
| `input` | string | yes | Free-text to resolve. |
| `schema` | string | yes | Path to a JSON Schema file. Applied to every tier's output. |
| `resolvers` | list | no | Ordered resolver list (see below). |
| `validator` | map | no | `{ post_cmd, post_cmd_args }` — runs in a read-only sandbox. |
| `working_dir` | string | no | cwd for the LLM tier. |
| `agent` | string | no | Fallback agent name (used when `resolvers[].llm.agent` is absent). |
| `prompt` | string | no | Fallback prompt path (used when no `resolvers: []` list and no per-resolver `llm.prompt`). |

**Resolver list format:**

```yaml
resolvers:
  - synonyms: ./synonyms.yaml
  - slot_template: ./templates.yaml
  - llm:
      prompt: ./extract.md
      agent: extractor
```

**Returns:**

| Field | Type | Notes |
|---|---|---|
| `submitted` | any | The resolved payload (nil when `resolved_by: no_match`). |
| `resolved_by` | string | `synonyms` \| `slot_template` \| `llm` \| `no_match` |
| `claude_session_id` | string | Populated when the LLM tier matched; empty otherwise. |

On `no_match`, `Result.Error` is set so `on_error:` can fire a fallback.

**Synonym file format (`synonyms.yaml`):**

```yaml
"go north,head north,north": { direction: "north" }
wade: { action: "wade" }
```

Keys are comma-separated phrases (case-insensitive). Values are the typed payload.

**Progressive determinism (`kitsoki extract suggest-synonym`):**

After an LLM-tier resolution, run:

```
kitsoki extract suggest-synonym --db <db> <session-id> <call-id>
```

where `<call-id>` is `turn:seq`, a plain turn number (when only one extract
call on that turn), or a 1-based index. The command prints a YAML snippet ready
to paste into the synonyms file, moving the next identical input to the
deterministic tier.

---

## host.transport.post

Post a message to a registered transport.

| Field | Type | Required | Notes |
|---|---|---|---|
| `transport` | string | yes | Transport ID — `"tui"`, `"jira"`. |
| `thread` | string | yes | The external thread (`"PROJ-12345"`, `<session-uuid>`). |
| `body` | string | yes | Markdown by convention; the transport converts to its native markup. Maps and slices are pretty-printed as JSON. |
| `phase_id` | string | no | Identifies the originating phase; transports use it for de-dup. |
| `title` | string | no | Used as a section header where the transport supports it. |
| `bot_marker` | string | no | Prepended to the body so polling drivers can filter their own output (default `"[kitsoki]"`). |

Returns: `{ message_id }` — opaque, transport-specific.

See [`transports.md`](transports.md) for the implementations.

---

## host.workspace_manager.get

Shells out to a `workspace-manager` CLI and parses the JSON output
into a typed `Workspace` (id, root path, repos, issue, PRs). Fields
are validated against
[`internal/workspace/schema.json`](../internal/workspace/schema.json).

| Field | Type | Required | Notes |
|---|---|---|---|
| `workspace_id` | string | yes | Identifier the external CLI understands. |

Returns the parsed object as `Result.Data`. Bind individual fields
(`bind: { workspace_root: root_path, … }`) or copy the whole map
(`bind: { workspace: "" }` on an `any`-typed world key).

---

## host.jobs.answer_clarification

Resume a background job that called `host.RequestClarification`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `job_id` | string | yes | The job ID from the inbox notification. |
| `answer` | any | yes | Whatever the clarification schema requested. |

The orchestrator persists the answer and the handler's poll loop
returns it as raw JSON. Full round-trip in
[`background-jobs/authoring.md`](background-jobs/authoring.md).

---

## host.chat.* — persistent chat threads

Chats are global, persistent multi-turn conversations scoped by
`(app_id, room, scope_key)`. They have their own per-chat singleton
lock so the TUI and an external driver can both interact with the
same session without racing on a chat. Backed by `internal/chats/`.

The full CLI surface is `kitsoki chat new|list|show|continue|fork|archive|unlock`.

### host.chat.resolve

Get-or-create a chat. Idempotent — cheap to call from `on_enter:` so a
room always knows its chat.

| Field | Type | Required | Notes |
|---|---|---|---|
| `app` | string | yes | App ID. |
| `room` | string | yes | Logical room name. |
| `scope_key` | string | no | Sub-scope inside the room (e.g. a workspace ID). |
| `title` | string | no | Title to use if a new chat is created. |

Returns: `{ chat_id, title, status, is_new }`.

### host.chat.list

| Field | Type | Required | Notes |
|---|---|---|---|
| `app` | string | yes | Filter by app. |
| `room` | string | yes | Filter by room. |
| `scope_key` | string | no | Filter by scope. |

Returns: `{ rendered, chats, count }` — `rendered` is a Markdown block
suitable for inlining into a `view:`. `chats` is a list of
`{id, title, message_count, last_active_at, status}`.

### host.chat.transcript

| Field | Type | Required | Notes |
|---|---|---|---|
| `chat_id` | string | yes | |
| `since_seq` | int | no | Return rows newer than this sequence. |
| `max_turns` | int | no | Default 20. |

Returns: `{ rendered, messages, latest_seq, title }`.

### host.chat.create

Explicitly create a new chat. Use `host.chat.resolve` instead unless
you want a guaranteed-new row.

### host.chat.fork

Copy messages into a new chat with `parent_chat_id` set; the new chat's
`claude_session_id` is cleared so the next turn starts a fresh Claude
session.

| Field | Type | Required |
|---|---|---|
| `chat_id` | string | yes |
| `title` | string | no |

### host.chat.archive

Soft-delete (`status = "archived"`). The chat is hidden from `list`
unless `all_status: true`.

### host.chat.rename

| Field | Type | Required |
|---|---|---|
| `chat_id` | string | yes |
| `title` | string | yes |

### host.chat.suggest_title

Ask Claude to propose a 4-8 word title from the transcript.

| Field | Type | Required |
|---|---|---|
| `chat_id` | string | yes |

Returns: `{ title }`.

### host.chat.resolve_ref

Resolve a free-form reference (chat ID, partial ID, alias, or
`"current"`) to a concrete `chat_id`. Used by the TUI's chat picker.

### host.chat.drive

Enqueue a turn against a chat and optionally run it synchronously.
The async path mirrors the `background_jobs` pattern: the handler
returns immediately with a `drive_id` and the turn runs out of band
(via `kitsoki chat queue dispatch <drive-id>` or a future periodic
drainer). The sync path acquires the chat singleton lock, runs
`claude -p --resume <id>` headlessly, and returns the result text +
the new `chat_messages.seq`.

The handler lives at
[`internal/host/chat_handlers.go:ChatDriveHandler`](../internal/host/chat_handlers.go);
the dispatcher (claim → claude → mark-terminal) lives in
[`internal/host/chat_dispatch.go`](../internal/host/chat_dispatch.go).

| Field | Type | Required | Notes |
|---|---|---|---|
| `chat_id` | string | one of | Target chat ULID. |
| `chat_ref` | string | one of | User-supplied reference (position, prefix, free-text). Resolved through `host.chat.resolve_ref`; requires `app`+`room` in the same args. Mutually exclusive with `chat_id`. |
| `app`, `room`, `scope_key` | strings | with `chat_ref` | Resolution scope. Ignored when `chat_id` is supplied. |
| `payload` | string | yes | User-message text for the turn. |
| `transport` | string | no | Originating surface tag (`tui`, `jira`, `bitbucket`, `mcp`, `job`, `state_machine`, `cli`). Default `state_machine`. |
| `thread` | string | no | Correlation thread (e.g. `PROJ-123#42`). |
| `actor` | string | no | Originating actor id. |
| `correlation_id` | string | no | Caller-side correlation token. |
| `await` | bool | no | `true` → block until the turn lands (or fails). `false` (default) → return immediately after Enqueue. |
| `timeout_seconds` | int | no | `await:true` only. Lock-contention budget; the dispatcher polls on a 1s cadence (matching the lock-heartbeat tick) until acquired or the budget elapses. Default 300. |
| `working_dir` | string | no | cwd for the claude subprocess in the `await:true` path. |

**Returns (always, both async and sync):**

| Key | Type | Notes |
|---|---|---|
| `drive_id` | string | Allocated ULID of the queued row. |
| `chat_id` | string | The resolved chat ULID. |
| `enqueued_at` | int64 | `chat_input_queue.received_at` (`UnixMicro`). |

**Additionally for `await:true`:**

| Key | Type | Notes |
|---|---|---|
| `status` | string | `"done"` or `"failed"`. |
| `result_seq` | int | `chat_messages.seq` of the assistant reply (when `done`). |
| `result_text` | string | Assistant reply text (when `done`). |
| `error` | string | Error message (when `failed`). |

**Errors** (Result.Error, prefix-distinguished for `on_error:` routing):

- `host.chat.drive: chat_not_found …` — `chat_id` / `chat_ref` didn't resolve.
- `host.chat.drive: chat_busy …` — `await:true` and the lock stayed contended past `timeout_seconds`.
- `host.chat.drive: drive_failed …` — `await:true` and the turn errored (non-zero claude exit, persistence failure, etc.).

**`on_complete` chain.** The proposal §9.2 specifies that
`await:false` drives optionally carry an `on_complete:` effect set
declared in the calling state, fired as a synthetic turn when the
drive completes. The drive row already persists the serialized
chain plus `origin_session_id` / `origin_state` so the followup
just needs to wire the orchestrator-side consumer (subscribe to
drive-terminal events; bind `world.last_drive_result`; run
`machine.RunEffects(origin_state, world, chain)`). Until that
lands, `await:false` callers should poll
`kitsoki chat queue list <chat-id>` or use `await:true` for
synchronous results.

**Example (sync, with `chat_ref`):**

```yaml
effects:
  - invoke: host.chat.drive
    with:
      chat_ref: "{{ world.bugfix_chat_ref }}"
      app: bugfix
      room: live_coding
      scope_key: "{{ world.ticket_id }}"
      payload: "Please summarize phase 7."
      await: true
      timeout_seconds: 60
    bind:
      summary: result_text
```

---

## Agent declaration

Named agents live in the top-level `agents:` block of `app.yaml`.
Each entry bundles the system prompt, model, tool surface, and (for
the new oracle verbs) the Bash restriction profile and external-side-effect
flag into a reusable persona that any `host.oracle.*` call can reference
by name via `agent: <name>` in the effect's `with:` block.

```yaml
agents:
  failure-explainer:
    system_prompt_path: prompts/explain_failure.md
    model: claude-sonnet-4-6
    tools: [Read, Grep, Glob, Bash, WebFetch]
    bash_profile:
      commands: [git, jq, grep, kubectl]   # required when Bash is in tools + ask/decide
    external_side_effect: true             # WebFetch → inferred true; explicit confirms

  file-only-implementer:
    system_prompt_path: prompts/implementer.md
    model: claude-sonnet-4-6
    tools: [Read, Edit, Write, Bash]
    # No bash_profile needed — Bash is unrestricted in task/converse verbs.
    external_side_effect: false            # file mutations only — no network
```

### Fields

| Field | Required | Notes |
|---|---|---|
| `system_prompt` xor `system_prompt_path` | Yes | Exactly one must be set. The loader resolves the path and inlines the text. |
| `model` | No | Forwarded as `--model` to claude. Defaults to the engine model when absent. |
| `tools` | No | Forwarded as `--allowedTools <csv>`. Normalised to `host.X` form by the loader. |
| `cwd` | No | Default working directory for claude when the effect omits `working_dir:`. Env vars (`$VAR`, `${VAR}`) are expanded at load time. |
| `bash_profile` | Conditional | Required when `Bash` is in `tools` and the agent is used with `host.oracle.ask` or `host.oracle.decide`. Three forms (see below). |
| `external_side_effect` | No | Declares whether the agent touches external state (network, remote APIs). The loader infers a default from the tool surface and emits a warn-line when declared and inferred values disagree. |

### `bash_profile` forms

The three allowed forms restrict what Bash commands the LLM may run.
They apply only to `ask` and `decide` calls; `task` and `converse` use
unrestricted Bash.

```yaml
bash_profile: read-only          # built-in allowlist: grep, find, cat, ls, git, jq, …
bash_profile:
  commands: [git, jq, grep]      # explicit argv0 allowlist
bash_profile:
  sandboxed_write: /tmp/scratch  # writes to scratch dir only; network denied via HTTP_PROXY
```

### Precedence rules

**Per-call `tools:` wins over `agent.Tools` (D5).** When an effect
declares both `tools:` and `agent: <name>` where the agent also has
tools, the effect's list takes priority. The handler emits a `slog.Warn`
when it detects the conflict so accidental overrides surface in the trace.

**Per-call `working_dir:` wins over `agent.DefaultCwd`.** The fallback
chain is: effect `working_dir:` > `agent.cwd` > prompt-file directory
(for `host.oracle.ask`).

### `external_side_effect` inference

The loader infers `external_side_effect` from the tool list when the
field is absent:

- `host.WebFetch` or `host.WebSearch` in `tools` → inferred `true`
- all other tool combinations → inferred `false`

An explicit declaration overrides the inference. A mismatch (e.g.
declaring `false` on an agent with `WebFetch`) is a warn-line at load
time, not an error — the author's explicit value wins.

---

## host.oracle.decide

Reasoning verdict call. LLM judgment is required; the schema is mandatory;
`submit` is auto-attached. The agent may optionally have a read-only tool
surface. No mutation tools, ever — the handler rejects `Edit`, `Write`, and
`NotebookEdit` at call time (the loader also rejects them at app-load).

**Distinct from `host.oracle.ask`:** `ask` returns prose (schema optional);
`decide` returns a typed verdict (schema required) and supports a read-only
semantic validator. Same read-only tool surface, different output contract.

**Distinct from `host.oracle.extract`:** `extract` can be answered by a
synonym, regex, or slot template; `decide` cannot — the LLM's judgment is the
point. "Is this PR diff a security concern?" is `decide`. "Map this utterance
to one of {start, status, cancel}" is `extract`.

### Args

| Field | Type | Required | Notes |
|---|---|---|---|
| `prompt` | string | one of | Inline prompt text. Rendered with `{{ args.X }}`. Mutually exclusive with `prompt_path`. |
| `prompt_path` | string | one of | Path to a prompt template file. Relative paths resolve against the app dir. Mutually exclusive with `prompt`. |
| `schema` | string | yes | Path to the JSON schema the verdict must conform to. Auto-attaches the kitsoki MCP validator so the LLM must call `submit()` before exiting. |
| `agent` | string | no | Named agent (from `agents:` in app.yaml). Supplies `system_prompt`, `model`, `tools`, and `cwd`. |
| `working_dir` | string | no | CWD for the claude subprocess. Defaults to `agent.DefaultCwd` when set, otherwise empty. |
| `args` | map | no | Template variables for the prompt (`{{ args.X }}`). When omitted, the full call-args map is used (legacy path). |
| `validator` | map | no | Optional read-only post-command semantic validator. See "Validator block" below. |
| `mcp_servers` | map | no | Additional MCP servers to attach (`{ name: { command, args, env } }`). Merged with the auto-attached submit validator. |
| `tools` | list | no | Per-call tool override. Wins over `agent.Tools` (D5 in the oracle-split proposal). Mutation tools are rejected. |

### Returns

| Field | Type | Notes |
|---|---|---|
| `submitted` | any | Schema-validated verdict JSON from the LLM's `submit()` call. Absent when the LLM exits without submitting. |
| `rationale` | string | Claude's free-text reasoning emitted alongside `submit`. Source-color wrapped. |
| `exit_code` | int | Claude's exit code. |
| `ok` | bool | `exit_code == 0`. |
| `claude_session_id` | string | Recorded in trace; not intended for YAML binding. |
| `validator_attempts` | int | Number of validator subprocess runs. Only present when `validator:` was declared. |

### Mutation-tool contract

`Edit`, `Write`, and `NotebookEdit` are hard-rejected by the handler at call
time. The loader additionally rejects them at app-load when the agent is used
in a `decide` call. Authors who need agentic work (file edits, Bash mutations)
should use `host.oracle.task`.

Read-only tools (`Read`, `Grep`, `Glob`, `WebFetch`, `WebSearch`, `Bash` under
a profile) are permitted and forwarded as `--allowedTools` to claude.

### Validator block

The optional `validator:` block runs a read-only subprocess after each
`submit()` call to enforce semantic constraints that the JSON schema cannot
express (catalog lookups, arithmetic checks, cross-reference validation).

The subprocess runs under `internal/host/validator_sandbox.go` (Linux:
`unshare -n` network isolation; macOS: `sandbox-exec`; Windows: requires
`unsafe_validator_no_sandbox: true`). A non-zero exit triggers a re-submit
nudge; zero exit accepts the verdict.

```yaml
validator:
  post_cmd: "python3 -m myapp verify-verdict"
  post_cmd_args:
    catalog: "data/catalog.yaml"
  post_cmd_cwd: "tools/verifiers"
  max_retries: 3
```

| Field | Type | Notes |
|---|---|---|
| `post_cmd` | string | Verifier program (e.g. `python3 -m myapp verify`). |
| `post_cmd_args` | map | Key/value pairs forwarded as `--key value` to the subprocess. Sorted by key for deterministic argv. |
| `post_cmd_cwd` | string | CWD for the subprocess. Relative paths resolve against the app dir. |
| `max_retries` | int | Per-submission retry budget. Default 5. |

### Examples

Minimal judge call:

```yaml
effects:
  - invoke: host.oracle.decide
    with:
      prompt: prompts/judge_pr.md
      schema: schemas/pr_verdict.json
      args:
        pr_id: "{{ world.pr_id }}"
    bind:
      verdict: submitted
      rationale: rationale
    on_error: room_judge_failed
```

With a named agent (read-only tools + custom model):

```yaml
agents:
  pr-judge:
    system_prompt_path: prompts/judge_system.md
    model: claude-opus-4-5
    tools: [Read, Grep, Glob]

effects:
  - invoke: host.oracle.decide
    with:
      prompt: prompts/judge_pr.md
      schema: schemas/pr_verdict.json
      agent: pr-judge
      working_dir: "{{ world.repo_root }}"
      args:
        pr_id: "{{ world.pr_id }}"
    bind:
      verdict: submitted
      rationale: rationale
```

With a semantic validator:

```yaml
effects:
  - invoke: host.oracle.decide
    with:
      prompt: prompts/judge_pr.md
      schema: schemas/pr_verdict.json
      agent: pr-judge
      validator:
        post_cmd: "python3 -m myapp verify-verdict"
        post_cmd_args:
          catalog: "data/catalog.yaml"
        max_retries: 3
    bind:
      verdict: submitted
      rationale: rationale
      validator_attempts: validator_attempts
```

---

## Migration history

All oracle call sites in this codebase were migrated from `host.oracle.ask_with_mcp`
and `host.oracle.talk` to the five-verb schema above during oracle-split Phases 6–9
(see git log for the `oracle-split` commit series). The `kitsoki migrate-oracle`
codemod (`cmd/kitsoki/migrate_oracle.go`) automated the bulk of the migration;
the classification rules it applies are documented in [`oracle-cli.md`](oracle-cli.md).

One Go-level entry point survives the migration: `host.OracleAskWithMCPHandler`
in `internal/host/oracle_ask_with_mcp.go` is called from `internal/metamode/adapter.go`
for chat-aware metamode. It is **not** a registered verb — apps cannot invoke
`host.oracle.ask_with_mcp` via YAML; the loader will reject it as unknown.
Future work folds the chat-aware metamode path onto `host.oracle.converse` (or a
dedicated chat-aware oracle abstraction); that work removes the leftover entry
point and its tests.

---

## Adding your own host

See [`developer-guide.md` §5.2](developer-guide.md#52-adding-a-new-built-in-host-handler).
The contract is small: implement `host.Handler` (a function with
signature `func(ctx, args, store) (Result, error)`), document the
`with:` and bind-able result keys, and register it in
`internal/host/handlers.go`.
