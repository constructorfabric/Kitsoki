# Starlark Experience

Starlark is Kitsoki's deterministic scripting language. It is the place to put
procedural logic that is too large for guards/templates and too small or
story-specific for a Go host handler.

Kitsoki uses the same Starlark runtime in three places:

| Surface | Author writes | Who chooses the code | Use when |
|---|---|---|---|
| `host.starlark.run` | `scripts/*.star` plus a `.star.yaml` sidecar | The story author | The logic is known and should run deterministically in every flow, cassette, and live session. |
| `host.agent.codeact` | A goal plus a capability grant | A bounded agent loop emits Starlark snippets, then `done(payload)` | The logic is exploratory, but every action should still be constrained to the Starlark sandbox instead of an open tool loop. |
| `kitsoki mcp-codeact` | An MCP server capability config | The attached external agent calls `codeact_eval` with snippets | A Claude/Codex/harness session should get code-as-action without receiving Bash, Python, Node, or direct filesystem tools. |

All three surfaces use `def main(ctx): ...`, the same `ctx` capability model, the
same pure standard library, the same Starlark dialect restrictions, and the
same no-LLM testing discipline around recorded, promoted, or in-process runs.

## The Mental Model

Kitsoki keeps the state machine deterministic and pushes uncertainty to narrow
edges. Starlark is one of those edges, but it is still deliberately small:

- Checked-in scripts receive effect inputs through `ctx.inputs`; CodeAct
  snippets normally read session state through a read-only `ctx.world.get(key)`
  snapshot; MCP CodeAct snippets receive `inputs` and `world` from the tool call.
- Outputs leave through `main(ctx)`'s returned dict and then ordinary `bind:`.
- External surfaces are absent until `with.capabilities` grants them.
- No script gets environment variables, clocks, randomness, imports, shell, or
  arbitrary process execution.
- Runtime errors are domain errors: they fire the effect's `on_error:` path
  instead of becoming hidden mutations.

That means a story can branch, shape JSON/YAML, call a plain HTTP API, inspect a
small part of the working tree, or write a bounded artifact without turning the
step into an opaque shell script or a free agent task.

## `host.starlark.run`

Use `host.starlark.run` when the code is part of the story contract.

```yaml
hosts:
  - host.starlark.run

states:
  enrich:
    on_enter:
      - invoke: host.starlark.run
        id: derive_widget
        with:
          script: scripts/derive_widget.star
          capabilities:
            http:
              methods: [GET]
              hosts: ["api.example.com"]
              cassette_required: true
          inputs:
            widget_id: "{{ world.widget_id }}"
        bind:
          widget_name: name
        on_error: lookup_failed
        once: true
```

Every script travels with a sidecar:

```yaml
# scripts/derive_widget.star.yaml
inputs:
  widget_id: { type: string, required: true }
outputs:
  name: { type: string }
```

The sidecar is the enforced interface. A missing required input, an undeclared
returned key, a forgotten declared output, or a type mismatch is a domain error.
In-script `INPUTS`/`OUTPUTS` dicts are comments only.

## `host.agent.codeact`

Use `host.agent.codeact` when the step needs an agent to decide what Starlark to
run, but you still want a bounded, capability-scoped execution surface.

```yaml
hosts:
  - host.agent.codeact

agents:
  triager:
    system_prompt: "Inspect with scoped Starlark snippets and submit a verdict."

states:
  triaging:
    on_enter:
      - invoke: host.agent.codeact
        once: true
        with:
          agent: triager
          goal: >-
            Triage {{ world.ticket_id }} against the current tree and return
            whether the bug is still live. Cite concrete code evidence.
          budget: 6
          capabilities:
            world: read
            vcs: read
          schema: schemas/triage_verdict.json
        bind:
          verdict: payload
          codeact_steps: steps
        on_error: triage_failed
```

Each step asks the named agent for one of two actions:

- `snippet`: Starlark source defining `def main(ctx): ...`.
- `done`: a final payload, validated by `schema:` when one is declared.

The snippet runs through the same Starlark evaluator as `host.starlark.run`.
If the snippet references `ctx.fs` without an `fs` grant, `ctx.http` without an
`http` grant, or an unlisted `ctx.host.call` verb, the step fails before any
production adapter is reached. The structured error is fed back to the next
agent step so the loop can self-correct, until `done` succeeds or `budget`
steps are consumed.

CodeAct is not `host.agent.task` with a smaller prompt. It does not receive an
open Claude Code tool loop, and it does not use `with.sandbox:`. Its sandbox is
the bounded Starlark loop plus the explicit capability map.

## `kitsoki mcp-codeact`

Use `mcp-codeact` when the worker itself is outside the story engine but should
still act only through Kitsoki's Starlark sandbox. The server exposes one MCP
tool, `codeact_eval`, whose input is a single snippet plus optional `inputs` and
`world` objects:

```json
{
  "snippet": "def main(ctx):\n    return {\"ok\": ctx.fs.exists(\"go.mod\")}\n",
  "inputs": {},
  "world": {"ticket_id": "KIT-7"}
}
```

The capability map is fixed when the MCP server starts. Tool callers cannot
broaden it:

```sh
kitsoki mcp-codeact --working-dir . \
  --capabilities-json '{"fs":{"read":["**"]},"vcs":"read"}'
```

For a launched Codex agent, attach it through the agent TOML:

```toml
[mcp_servers.codeact]
command = "kitsoki"
args = [
  "mcp-codeact",
  "--working-dir", ".",
  "--capabilities-json", '{"fs":{"read":["**"]},"vcs":"read"}',
]
```

The standalone MCP server supports `ctx.world`, `ctx.fs`, `ctx.probe`/`vcs`, and
`ctx.http` when granted. It deliberately rejects `ctx.host` grants for now: an
external stdio server has no safe session-local host registry to call.

## Benchmark Usage

The arena paired-task harness uses CodeAct in two different ways:

- `codex-codeact` launches `kitsoki-codeact-driver` through
  `kitsoki agent launch --mode codeact`. The dry-run launch plan is saved before
  the live run, and the harness asserts that Codex shell/apps are disabled and
  only `mcp__kitsoki-codeact__codeact_eval` is exposed.
- `kitsoki-mcp-codeact` keeps `kitsoki-mcp-driver` on the Studio MCP surface for
  orchestration, then seeds `implementation_mode: codeact` into the
  `bench-bugfix` story. The imported bugfix implementing room swaps only the
  mutating implementer step from `host.agent.task` to `host.agent.codeact`; the
  rest of the pipeline remains the same.

That separation matters when reading benchmark results: Studio MCP evidence
shows how the workflow was driven, while CodeAct evidence shows the write
surface used by the model applying the fix. Arena cell JSON records the action
surface, launch-plan reference, capability hash, and permission assertion
outcomes when those are available.

## `ctx` Surface

`ctx` is the whole Starlark world:

| Surface | Default | Grant | Shape |
|---|---|---|---|
| `ctx.inputs` | yes | n/a | For `host.starlark.run`, a dict of effect inputs type-checked by the sidecar. For MCP CodeAct, the tool call's `inputs` object. Story CodeAct snippets do not have per-snippet sidecars and should use `ctx.world` for session state. |
| `ctx.world` | yes | `world: read` or default | Read-only `ctx.world.get(key)` snapshot. Set `world: none` to remove it. |
| `ctx.http` | no | `http:` | `get` / `post`, method and host filtered, cassette-aware. With auth-aware runners such as `ticket_provider/v1`, optional `auth=` names host-side policies; the transport injects credentials without exposing secret values to Starlark. |
| `ctx.fs` | no | `fs.read` / `fs.write` | Rooted read/exists/glob/write with path patterns and byte caps. |
| `ctx.probe` | no | `probe`, `vcs`, or `github` | Fixed read-only probes such as `git.status`, `git.ls_files`, `gh.issue.list`. |
| `ctx.host` | no | `host.verbs` | Exact allow-listed engine host calls. |

Example capability map:

```yaml
capabilities:
  stdlib: [json, math, yaml]       # default; can be narrowed
  world: read                      # default; use none/false to hide ctx.world
  http:
    methods: [GET, POST]
    hosts: ["api.example.com", "*.trusted.example"]
    cassette_required: true
  fs:
    read: ["docs/**", "stories/my-story/**"]
    write: [".artifacts/my-story/**"]
    max_bytes: 1048576
  vcs: read                        # git.status, git.ls_files
  github:
    issues: read                   # gh.issue.list
  host:
    verbs: ["host.graph.load"]
```

Unknown capability keys fail app load. Ungranted runtime access fails as a
Starlark domain error with a clear missing-attribute or not-granted message.

## Standard Library

Kitsoki exposes only deterministic helpers:

| Module | Functions / constants | Notes |
|---|---|---|
| `json` | `decode`, `encode`, `encode_indent`, `indent` | From `go.starlark.net/lib/json`. Dict keys are emitted sorted by the module. |
| `math` | `ceil`, `floor`, `round`, `sqrt`, `pow`, trig/hyperbolic/log functions, `pi`, `e` | From `go.starlark.net/lib/math`; functions accept Starlark `int` and `float`. |
| `yaml` | `decode` | Kitsoki helper. Decode-only; returns Starlark dict/list/scalar values. |

There is no `time`, `random`, filesystem import, module import, or package
loader. If a script needs outside data, declare it as an input or grant a
specific `ctx` capability.

```python
def main(ctx):
    doc = yaml.decode(ctx.inputs["manifest_yaml"])
    payload = json.decode(ctx.inputs["payload_json"], default={})
    score = int(math.round(payload.get("score", 0)))
    return {"name": doc["app"]["id"], "score": score}
```

## Sandboxing Layers

Kitsoki has more than one sandbox-like boundary. They are intentionally
separate:

| Mechanism | Applies to | Enforces |
|---|---|---|
| Starlark capability map | `host.starlark.run`, story CodeAct snippets, and MCP CodeAct snippets | Which `ctx` attributes exist, plus HTTP method/host policy, fs path/byte policy, probe names, and host verbs where supported. |
| Starlark interpreter limits | `host.starlark.run`, story CodeAct snippets, and MCP CodeAct snippets | Strict dialect, no global reassignment/recursion, no nondeterministic stdlib, execution-step cap. |
| Agent runtime `sandbox:` | `host.agent.task` and write-capable `host.agent.converse` | External coding-agent process supervision, timeout/cancel cleanup, env allowlisting, and diff capture. Not used by CodeAct. |
| Agent launch policy | All external agent launches | Whether an agent may start in a protected checkout/branch or outside an opened capsule. |

Do not copy a `sandbox:` block onto a CodeAct effect. The loader rejects it
because the CodeAct boundary is not an external subprocess filesystem sandbox;
it is the Starlark capability sandbox.

## Validation And Tests

Start with static validation:

```sh
.agents/skills/starlark/tools/validate.sh stories/<name>/scripts/<script>.star -kitsoki
```

`-kitsoki` parses and resolves without executing. It requires `def main(ctx)`,
uses the strict dialect, and pins the available globals to the Kitsoki stdlib.

Then load and run the story through deterministic flow tests:

```sh
kitsoki test flows stories/<name>/app.yaml --flows flows/<case>.yaml --v
```

For `host.starlark.run`, do not stub the whole handler when you need coverage of
the script. Let the real script execute and replay only its external boundary:

- `starlark_http_cassette:` replays `ctx.http`.
- `starlark_inspect_cassette:` replays `ctx.fs` / `ctx.probe`.
- Flow assertions should check the bound outputs, error arcs, and trace-summary
  events that matter.

Capability tests should prove the opt-in boundary, not just the happy path:

- A script/snippet that references `ctx.fs`, `ctx.http`, `ctx.probe`, or
  `ctx.host` without a matching grant should fail before any mock, cassette, or
  production adapter is consulted. In practice, ungranted attributes are absent
  from `ctx`, so the failure is an early Starlark domain error.
- Unknown or wrongly shaped `with.capabilities` maps should fail app load.
- Method/host/path/probe/verb restrictions should be covered by negative tests
  that inject a harmless mock/replay adapter and still assert the policy wrapper
  rejects the call first.
- Flow fixtures that stub the whole host call prove room wiring only. They do
  not prove the script used the granted surface correctly.

For CodeAct, use a `host_cassette` or flow stub to replay the recorded
`host.agent.codeact` result with zero LLM. When a trajectory stabilizes into a
deterministic transform, promote the final Starlark into `host.starlark.run`
with a sidecar and replace the CodeAct call. The promoted flow should prove the
same payload with no `host.agent.codeact` dispatch.

For MCP CodeAct, unit-test the server through the SDK in-memory transport and
assert both the happy path and denied capability paths. The attached LLM/harness
does not need to run in tests.

## Where To Look

- Full field-level host reference: [`hosts.md#hoststarlarkrun`](hosts.md#hoststarlarkrun)
  and [`hosts.md#hostagentcodeact`](hosts.md#hostagentcodeact).
- Story-authoring guidance: [`../stories/authoring.md`](../stories/authoring.md).
- Starlark skill reference for authors:
  [`../../.agents/skills/starlark/reference/kitsoki.md`](../../.agents/skills/starlark/reference/kitsoki.md).
- Runtime packages: `internal/host/starlark/`, `internal/host/codeact/`, and
  `internal/mcp/codeact.go`.
