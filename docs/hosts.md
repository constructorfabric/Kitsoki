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

---

## Cheat sheet

| Handler | Purpose |
|---|---|
| [`host.run`](#hostrun) | Execute a shell command in a working directory. |
| [`host.oracle.ask`](#hostoracleask) | One-shot Claude call driven by a prompt template. |
| [`host.oracle.talk`](#hostoracletalk) | Conversational Claude session, optionally chat-aware. |
| [`host.oracle.ask_with_mcp`](#hostoracleask_with_mcp) | One-shot Claude call with MCP tools (e.g. typed-JSON validators). |
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

## host.oracle.ask

One-shot Claude call. The prompt is a Markdown file on disk; arguments
are interpolated with `{{ args.X }}`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `prompt_path` | string | yes | Relative paths resolve against the app dir; absolute paths are used as-is. |
| `working_dir` | string | no | CWD for the spawned `claude`; defaults to the prompt's directory. |
| any other key | any | no | Surfaced as `{{ args.<key> }}` inside the prompt. |

Returns: `{ stdout, exit_code, ok }`. The handler strips one trailing
newline from stdout — useful when binding the result back into world.

The spawned `claude` runs with `--permission-mode bypassPermissions`,
so Bash/Read/Grep/Glob/Web tools are available. Write the prompt as if
you're talking to Claude Code. End with a clear contract — "your final
message is the literal X and nothing else" makes binding `stdout`
trivial.

---

## host.oracle.talk

Conversational Claude session — multi-turn, with optional persistence.

| Field | Type | Required | Notes |
|---|---|---|---|
| `question` | string | yes | The user's prompt for this turn. |
| `chat_id` | string | no | Enables **chat-aware mode**: append messages to the persistent transcript and reuse the chat row's `claude_session_id`. Acquires the per-chat singleton lock for the turn. |
| `session_id` | string | no | Legacy non-chat path — round-tripped so the caller can persist it in world and resume. Ignored when `chat_id` is set. |
| `working_dir` | string | no | CWD for the spawned `claude`. |

Returns:

| Field | Type | Notes |
|---|---|---|
| `answer` | string | Claude's reply text. |
| `session_id` | string | Claude's session ID (the SDK's, not kitsoki's). |
| `chat_id` | string | Echoes the input. |
| `claude_session_id` | string | Same as `session_id`; named for clarity. |
| `transcript_seq` | int | The transcript row sequence — useful for clients tracking position. |

Use `host.oracle.talk` when the user is in a sustained conversation;
use `host.oracle.ask` when you want a one-shot prompt-templated reply.

---

## host.oracle.ask_with_mcp

One-shot Claude call with MCP servers attached. Same shape as
`host.oracle.ask`, plus `mcp_servers:` and an optional `chat_id`.

| Field | Type | Required | Notes |
|---|---|---|---|
| `prompt_path` | string | yes | Same semantics as `host.oracle.ask`. |
| `mcp_servers` | map | no | `{ <name>: { command, args, env } }` — passed to `claude --mcp-config`. |
| `validator` | string | no | When set, runs `kitsoki mcp-validator` on Claude's tool output and retries on schema failure. |
| `chat_id` | string | no | Same chat-aware semantics as `host.oracle.talk`. |
| `working_dir` | string | no | CWD for the spawned `claude`. |
| any other key | any | no | Surfaced as `{{ args.<key> }}` in the prompt. |

The most common pattern: hand Claude an MCP-validated typed-JSON tool
and instruct the prompt to "submit your answer via the `submit` tool".
The validator captures the payload as `bind: foo: submitted` and the
handler returns it as `Result.Data.submitted`.

The handler also implements an **abandonment-recovery retry loop**:
if `claude` exits without a final message (network drop, etc.), the
handler resumes the same Claude session via `--resume` and retries
up to `--max-retries` times. See `internal/host/oracle_ask_with_mcp.go`.

---

## host.transport.post

Post a message to a registered transport.

| Field | Type | Required | Notes |
|---|---|---|---|
| `transport` | string | yes | Transport ID — `"tui"`, `"jira"`. |
| `thread` | string | yes | The external thread (`"PLTFRM-12345"`, `<session-uuid>`). |
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

---

## Adding your own host

See [`developer-guide.md` §5.2](developer-guide.md#52-adding-a-new-built-in-host-handler).
The contract is small: implement `host.Handler` (a function with
signature `func(ctx, args, store) (Result, error)`), document the
`with:` and bind-able result keys, and register it in
`internal/host/handlers.go`.
