# kitsoki agent — CLI and JSON-RPC reference

`kitsoki agent <verb>` and `kitsoki agent-serve` expose the five
agent handlers as a direct CLI and as a long-running unix-socket
daemon. Use them from validator scripts, CI jobs, or any subprocess
that needs a structured LLM call without spinning up a full state
machine.

## The five verbs

| Verb | Handler | When to use |
|---|---|---|
| `extract` | `host.agent.extract` | Tiered resolver: synonyms → slot template → LLM. Returns typed JSON + `resolved_by`. |
| `decide` | `host.agent.decide` | Typed LLM verdict (schema required; submit-handler auto-attached; read-only tools optional). |
| `ask` | `host.agent.ask` | Read-only inspection call: read tools + Bash under a profile; no mutation. Returns prose + optional typed JSON. |
| `task` | `host.agent.task` | Agentic verb with full tool surface, acceptance loop, and replay artifacts. |
| `converse` | `host.agent.converse` | Free-form conversational session with `permission_mode` control. |

The verbs map 1:1 to the handlers documented in
[`docs/architecture/hosts.md`](../../architecture/hosts.md#hostagentextract). All flags mirror the
`with:` fields accepted by each handler.

---

## kitsoki agent extract

```
kitsoki agent extract \
  --input    <text or - for stdin> \
  --schema   path/to/schema.json \
  [--resolvers-yaml path/to/resolvers.yaml] \
  [--agent   <agent-name>] \
  [--parent-session <session-id>]
```

`--schema` is required; extraction without a target schema is
an error.

---

## kitsoki agent decide

```
kitsoki agent decide \
  --prompt   path/to/prompt.md \
  --schema   path/to/verdict-schema.json \
  [--agent   <agent-name>] \
  [--args-json '{"key":"value"}'] \
  [--validator-cmd "bash check.sh"] \
  [--parent-session <session-id>]
```

`--schema` is required. The agent attaches a submit-tool that enforces
the schema before returning; no raw-text scraping in the caller.

---

## kitsoki agent ask

```
kitsoki agent ask \
  --prompt     path/to/prompt.md \
  [--agent     <agent-name>] \
  [--working-dir /path/to/worktree] \
  [--schema    path/to/output-schema.json] \
  [--args-json '{"key":"value"}'] \
  [--parent-session <session-id>]
```

Read-only tools only. Use `task` if mutation tools are needed.

---

## kitsoki agent task

```
kitsoki agent task \
  --agent             <agent-name> \
  [--working-dir      /path/to/worktree] \
  [--acceptance-schema path/to/schema.json] \
  [--acceptance-cmd   "bash accept.sh"] \
  [--context-prompt   path/to/context.md] \
  [--parent-session   <session-id>]
```

---

## kitsoki agent converse

```
kitsoki agent converse \
  --message         "What should I do about X?" \
  [--chat-id        <existing-chat-id>] \
  [--agent          <agent-name>] \
  [--permission-mode default|acceptEdits|bypassPermissions] \
  [--background] \
  [--parent-session <session-id>]
```

---

## Output format

When stdout is not a TTY, output is a single JSON object:

```json
{
  "data": { "submitted": { ... } },
  "error": null
}
```

When stdout is a TTY, prose is printed with a summary header. Pipe to
`jq` to extract fields in scripts:

```sh
verdict=$(kitsoki agent decide \
  --prompt prompts/judge_validating.md \
  --schema schemas/judge_verdict.json \
  --args-json "{...}" | jq -r '.data.submitted.verdict')
```

---

## Trace continuity — KITSOKI_SESSION_ID

Every agent call records its inputs and output as a labeled decision in
the session trace. To connect subprocess decisions to the parent session
(so the full tree is queryable in one place), set `KITSOKI_SESSION_ID`
before launching the subprocess:

```sh
export KITSOKI_SESSION_ID="$(kitsoki session id)"
kitsoki agent decide --prompt ... --schema ...
```

The state machine sets this automatically for `host.run` subprocesses.
In validator scripts that are launched from `host.run`, `KITSOKI_SESSION_ID`
is already present in the environment.

---

## Auto-delegation to the daemon — KITSOKI_AGENT_SOCK

When `KITSOKI_AGENT_SOCK` is set in the environment, each `kitsoki
agent <verb>` invocation skips forking a new process and instead sends
a JSON-RPC call to the daemon already listening at that path. This is
useful in triage loops that call the agent dozens of times — the daemon
amortises model warm-up and connection overhead.

```sh
# Start the daemon (background):
kitsoki agent-serve --socket /tmp/kitsoki-agent.sock &

# All subsequent CLI calls route through the socket automatically:
export KITSOKI_AGENT_SOCK=/tmp/kitsoki-agent.sock
kitsoki agent decide --prompt ... --schema ...
```

---

## kitsoki agent-serve

Long-running JSON-RPC 2.0 server over a unix socket. Each connection
carries one newline-delimited request and receives one newline-delimited
response before closing.

```
kitsoki agent-serve [--socket /path/to/agent.sock]
```

Socket path resolution order:
1. `--socket` flag
2. `KITSOKI_AGENT_SOCK` environment variable
3. `/tmp/kitsoki-agent-<pid>.sock`

### JSON-RPC method shapes

#### agent.extract

```json
{
  "jsonrpc": "2.0", "id": 1,
  "method": "agent.extract",
  "params": {
    "input":           "text to extract from",
    "schema":          "path/to/schema.json",
    "resolvers_yaml":  "path/to/resolvers.yaml",
    "agent":           "optional-agent-name",
    "parent_session_id": "optional-session-id"
  }
}
```

#### agent.decide

```json
{
  "jsonrpc": "2.0", "id": 2,
  "method": "agent.decide",
  "params": {
    "prompt":            "path/to/prompt.md",
    "schema":            "path/to/verdict.json",
    "agent":             "optional-agent-name",
    "args_json":         "{\"key\":\"value\"}",
    "validator_cmd":     "optional shell command",
    "parent_session_id": "optional-session-id"
  }
}
```

#### agent.ask

```json
{
  "jsonrpc": "2.0", "id": 3,
  "method": "agent.ask",
  "params": {
    "prompt":            "path/to/prompt.md",
    "agent":             "optional-agent-name",
    "working_dir":       "/path/to/workdir",
    "schema":            "path/to/schema.json",
    "args_json":         "{\"key\":\"value\"}",
    "parent_session_id": "optional-session-id"
  }
}
```

#### agent.task

```json
{
  "jsonrpc": "2.0", "id": 4,
  "method": "agent.task",
  "params": {
    "agent":             "agent-name",
    "working_dir":       "/path/to/workdir",
    "acceptance_schema": "path/to/schema.json",
    "acceptance_cmd":    "bash check.sh",
    "context_prompt":    "path/to/context.md",
    "parent_session_id": "optional-session-id"
  }
}
```

#### agent.converse

```json
{
  "jsonrpc": "2.0", "id": 5,
  "method": "agent.converse",
  "params": {
    "message":           "What should I do?",
    "chat_id":           "optional-chat-id",
    "agent":             "optional-agent-name",
    "permission_mode":   "default",
    "background":        false,
    "parent_session_id": "optional-session-id"
  }
}
```

### Error codes

| Code | Meaning |
|---|---|
| `-32700` | Parse error — request is not valid JSON. |
| `-32600` | Invalid request — missing required JSON-RPC fields. |
| `-32601` | Method not found — `method` is not a recognised `agent.*` name. |
| `-32000` | Domain error — handler rejected the call (e.g. missing schema). |

---

## Python client (minimal)

```python
import json, socket

def agent_rpc(sock_path: str, method: str, params: dict) -> dict:
    req = {"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.connect(sock_path)
        s.sendall((json.dumps(req) + "\n").encode())
        buf = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            buf += chunk
            if b"\n" in buf:
                break
    resp = json.loads(buf.split(b"\n")[0])
    if resp.get("error"):
        raise RuntimeError(f"RPC error {resp['error']['code']}: {resp['error']['message']}")
    return resp.get("result", {})

# Example: run the judge from a validator script
result = agent_rpc(
    "/tmp/kitsoki-agent.sock",
    "agent.decide",
    {
        "prompt":    "stories/bugfix/prompts/judge_validating.md",
        "schema":    "stories/bugfix/schemas/judge_verdict.json",
        "args_json": json.dumps({
            "ticket_id":      "PROJ-123",
            "artifact_title": "Tests pass, race condition not reproducible",
            "artifact_body":  "...",
        }),
    },
)
verdict = result["data"]["submitted"]
print(verdict["verdict"], verdict["confidence"])
```

This is the supported replacement for the old direct `claude -p` validator
script pattern. Keep judge calls in the story graph via `host.agent.decide`
or, for compatibility shims, through the `agent.decide` socket/CLI surface so
the decision is schema-checked and traced.

---

## kitsoki migrate-agent

Codemod that updated `app.yaml` files from pre-agent-split verb names to the
five-verb schema above. All in-tree stories have been migrated (Phases 6–9);
the tool remains available for out-of-tree apps. See `kitsoki migrate-agent --help`.

Classification rules summary:

| Original verb | Rule | New verb |
|---|---|---|
| `agent.talk` | `chat_id` present → converse | `converse` |
| `agent.talk` | no `chat_id` | `ask` |
| `agent.ask_with_mcp` | `schema` present | `decide` |
| `agent.ask_with_mcp` | no `schema` | `ask` |
| `agent.decide_with_prompt` | `validator_cmd` is a worker process | `task` |
| `agent.decide_with_prompt` | `validator_cmd` is a checker, or absent | `decide` |
| `agent.extract` | — | `extract` (unchanged) |
| `agent.decide` | — | `decide` (unchanged) |
| `agent.ask` | — | `ask` (unchanged) |

Ambiguous calls (mutation tool hint + no explicit `validator_cmd`, or
`is_worker` hint ambiguous) are left unchanged and a
`.migrate-agent.todo` file is emitted alongside the YAML for manual
review.
