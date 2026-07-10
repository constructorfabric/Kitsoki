# loopy — kitsoki validator harness

`loopy` is the Acronis-internal harness that runs per-phase validator
scripts during automated bugfix and code-review pipelines. Each script
is invoked by `host.run` from within a kitsoki state machine; it exits
0 on accept, non-zero on reject, and writes structured JSON to stdout.

**All Claude calls go through `kitsoki agent`.** Validator scripts must
not shell out to `claude -p` directly. Every agent invocation issued
from a loopy script is recorded as a labeled decision under the parent
kitsoki session, enforces schema compliance at the agent layer (no
brittle regex scraping in the script), and lets the daemon amortise
model overhead when many validators run in parallel.

---

## Calling agent from a validator subprocess

### CLI path (no daemon)

Suitable for single validators or low-frequency calls.

```python
import json, subprocess, os

def agent_decide(prompt: str, schema: str, args: dict) -> dict:
    result = subprocess.run(
        [
            "kitsoki", "agent", "decide",
            "--prompt", prompt,
            "--schema", schema,
            "--args-json", json.dumps(args),
        ],
        capture_output=True,
        text=True,
        env=os.environ,   # inherits KITSOKI_SESSION_ID set by the state machine
        check=True,
    )
    data = json.loads(result.stdout)
    return data["data"]["submitted"]

verdict = agent_decide(
    prompt="stories/bugfix/prompts/judge_validating.md",
    schema="stories/bugfix/schemas/judge_verdict.json",
    args={
        "ticket_id":      "PROJ-123",
        "artifact_title": "Tests pass",
        "artifact_body":  "...",
    },
)
print(verdict["verdict"], verdict["confidence"])
```

### Socket path (daemon running)

Use this when the same agent instance will be called many times in a
loop (e.g. bulk CI triage). The daemon is started once; all callers
re-use the same connection.

```sh
# Start daemon before the pipeline begins:
kitsoki agent-serve --socket /tmp/kitsoki-agent.sock &
export KITSOKI_AGENT_SOCK=/tmp/kitsoki-agent.sock
```

```python
import json, os, socket

def agent_rpc(method: str, params: dict) -> dict:
    sock_path = os.environ["KITSOKI_AGENT_SOCK"]
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
        raise RuntimeError(f"RPC {resp['error']['code']}: {resp['error']['message']}")
    return resp.get("result", {})

result = agent_rpc("agent.decide", {
    "prompt":    "stories/bugfix/prompts/judge_validating.md",
    "schema":    "stories/bugfix/schemas/judge_verdict.json",
    "args_json": json.dumps({"ticket_id": "PROJ-123", "artifact_title": "...", "artifact_body": "..."}),
})
verdict = result["data"]["submitted"]
```

When `KITSOKI_AGENT_SOCK` is set, `kitsoki agent <verb>` also
auto-delegates to the socket, so the same script works in both modes
without code changes.

### JSON-RPC method parameter shape

All five methods follow the same envelope:

```
Request:  {"jsonrpc":"2.0","id":<n>,"method":"agent.<verb>","params":{...}}
Response: {"jsonrpc":"2.0","id":<n>,"result":{...}}
       or {"jsonrpc":"2.0","id":<n>,"error":{"code":-32000,"message":"..."}}
```

Per-verb required params:

| Method | Required params | Common optional params |
|---|---|---|
| `agent.extract` | `schema`, `input` | `resolvers_yaml`, `agent`, `parent_session_id` |
| `agent.decide` | `schema` | `prompt`, `agent`, `args_json`, `validator_cmd`, `parent_session_id` |
| `agent.ask` | `prompt` | `agent`, `schema`, `args_json`, `working_dir`, `parent_session_id` |
| `agent.task` | `agent`, `working_dir`, `acceptance_schema` | `acceptance_cmd`, `context_prompt`, `parent_session_id` |
| `agent.converse` | `message` | `chat_id`, `agent`, `permission_mode`, `background`, `parent_session_id` |

`parent_session_id` threads the call into the parent kitsoki session.
When `KITSOKI_SESSION_ID` is set in the environment, the CLI picks it
up automatically so scripts do not need to pass it explicitly.

---

## Before / after example

`stories/bugfix/scripts/judge_verdict_before.py` shows the old
anti-pattern: `claude -p` with inline regex scraping and a schema copy
that drifts from the canonical `stories/bugfix/schemas/judge_verdict.json`.

`stories/bugfix/scripts/judge_verdict_after.py` shows the rewrite:
`kitsoki agent decide` (CLI or socket path), canonical schema, trace
linked via `KITSOKI_SESSION_ID`. This is the pattern all new validator
scripts must follow.

Key differences:

| Old pattern | New pattern |
|---|---|
| `subprocess.run(["claude", "-p", prompt])` | `subprocess.run(["kitsoki", "agent", "decide", ...])` or RPC |
| Schema reimplemented inline; drifts | `--schema path/to/schema.json` — single source of truth |
| Regex-scrapes first `{...}` block | Structured JSON guaranteed by agent's submit enforcement |
| No session linkage; opaque in trace | `KITSOKI_SESSION_ID` inherited; decision recorded in trace |

---

## Trace continuity

The state machine sets `KITSOKI_SESSION_ID` in the environment before
launching any `host.run` subprocess. Validator scripts inherit it
automatically. Every agent call issued from within the subprocess is
recorded under the parent session so the full decision tree is visible
in `kitsoki session show`.

Scripts that intentionally bypass the agent (e.g. pure shell checks
with no LLM involvement, or demo scripts that simulate operator typing)
must add the sentinel comment one line above the bypassing call:

```python
# kitsoki-ok: intentional out-of-trace use — pure deterministic check
```

Use this sparingly. Every unannotated `claude -p` call in `stories/` or
`tools/` is a bug.

---

## Available agent verbs

| Verb | Use in validators |
|---|---|
| `agent.extract` | Pull typed fields from unstructured output. |
| `agent.decide` | Structured verdict with schema enforcement. Most judges use this. |
| `agent.ask` | Read-only inspection; returns prose + optional JSON. |
| `agent.task` | Agentic mutation with acceptance loop (rarely needed in validators). |
| `agent.converse` | Free-form session; not typical in automated scripts. |

Full CLI reference: [`docs/guide/agents/cli.md`](../../docs/guide/agents/cli.md).
