# Oracle Providers

> **Status:** operator-facing reference for the `providers:` block. A
> **provider** retargets the `claude` subprocess behind an oracle invocation —
> pointing it at an alternate Anthropic-compatible backend — without changing
> which oracle verb runs or how its output is validated. It is orthogonal to
> the [oracle plugin](./oracle-plugin.md) mechanism: plugins choose *which
> component answers*; providers choose *which backend the claude component
> talks to*.

Kitsoki's default oracle verbs (`host.oracle.{decide,ask,task,extract,converse,ask_with_mcp}`)
fork the local `claude` binary, which talks to whatever backend the ambient
environment configures (normally your Anthropic auth). A provider lets some
invocations keep that ambient backend while others point claude at a different
one — e.g. an internal LiteLLM proxy exposing OpenAI/Anthropic-compatible
models — by overriding a few environment variables for that subprocess only.

The canonical motivating case: route cheap/bulk decisions to an internal local
LLM endpoint while keeping high-stakes reasoning on real Anthropic models.

---

## 1. `providers:` block YAML reference

Providers are declared under the top-level `providers:` key (a map of provider
name → declaration):

```yaml
providers:
  local_llm:
    model: h200/gpt-oss-120b              # default --model for this provider
    env:                                  # merged onto the claude subprocess env
      ANTHROPIC_BASE_URL: https://local-llm.adc.corp.acronis.com
      ANTHROPIC_AUTH_TOKEN: "${LOCAL_LLM_TOKEN}"   # ${VAR} substituted at load
      NODE_EXTRA_CA_CERTS: /home/me/itca-r2.pem
```

A declaration must set `model:`, `effort:`, and/or `env:` — an empty provider is
rejected at load time (it would have no effect). Fields:

| Field    | Meaning |
|----------|---------|
| `model`  | The `--model` value used for an invocation that selects this provider **and** whose agent (and effect) declare no explicit model. An explicit agent/effect model always wins. Optional. |
| `effort` | The `--effort` default (`low\|medium\|high\|xhigh\|max`) used for an invocation that selects this provider **and** whose agent (and effect) declare no explicit effort. An explicit agent/effect effort always wins. Optional. |
| `env`    | Environment-variable overrides merged onto the `claude` subprocess (overriding any ambient value of the same key). Typical keys: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `NODE_EXTRA_CA_CERTS`. Values support `${VAR}` interpolation, resolved at load time; an unset `${VAR}` is a hard load error. Optional. |

> **Secrets:** put the token in your environment and reference it as
> `${LOCAL_LLM_TOKEN}` — never inline a literal token in the story file. This
> mirrors the `oracle_plugins:` env contract.

---

## 2. Selecting a provider

A provider is selected per invocation, in this precedence order (highest first):

1. **Effect arg** — `with: { provider: <name> }` on a `host.oracle.*` effect.
2. **Agent default** — `provider: <name>` on the agent the invocation resolves to.
3. **Unset** — the ambient environment (today's behavior; no override).

```yaml
agents:
  cheap_helper:
    system_prompt: prompts/helper.md
    provider: local_llm          # every call through this agent → local_llm

states:
  triage:
    on_enter:
      # Uses cheap_helper's provider (local_llm):
      - invoke: host.oracle.decide
        with: { agent: cheap_helper, prompt: prompts/triage.md, schema: schemas/triage.json }

      # Overrides back to ambient Anthropic for one high-stakes call:
      - invoke: host.oracle.decide
        with: { agent: cheap_helper, provider: anthropic, prompt: ... }
    terminal: true
```

(The override example assumes you also declared an `anthropic` provider; if you
want "ambient" as the override, simply omit `provider:` on that effect — there
is no need to declare a no-op provider.)

The precedence mirrors `system_prompt` and `tools` (per-effect wins over the
agent default), so authors carry one mental model.

---

## 3. Validation

The loader fails fast on:

- a provider that sets none of `model:`, `effort:`, or `env:`,
- an `effort:` (on a provider or agent) outside `low\|medium\|high\|xhigh\|max`,
- an unset `${VAR}` referenced in any provider `env:` value,
- an `agents.<name>.provider` naming an undeclared provider,
- an effect `with.provider` naming an undeclared provider, or used on a
  non-`host.oracle.*` invocation.

Templated provider names (`{{ … }}`) are resolved at runtime and skipped by the
static checks.

---

## 4. Semantics & guarantees

- **Subprocess-scoped.** Provider env is merged onto the specific `claude`
  invocation's environment only — never `os.Setenv`. Concurrent invocations on
  different providers do not race.
- **Override, not replace.** A provider `env` entry overrides any ambient value
  of the same key; unrelated env (PATH, the kitsoki-bin-on-PATH shim,
  `KITSOKI_SESSION_ID`, the IDE-link scrub) is preserved.
- **Default-stable.** An invocation that selects no provider produces a
  byte-identical environment to before this feature existed.
- **Orthogonal to plugins.** Providers affect the built-in claude path
  (`oracle.claude` and any invocation that forks claude). They do not change
  `subprocess` / `mcp_http` / `builtin.local_llm` oracle plugins, which carry
  their own transport config under `oracle_plugins:`.

---

## 5. Worked example: internal LiteLLM proxy

```yaml
providers:
  local_llm:
    model: h200/gpt-oss-120b
    env:
      ANTHROPIC_BASE_URL: https://local-llm.adc.corp.acronis.com
      ANTHROPIC_AUTH_TOKEN: "${LOCAL_LLM_TOKEN}"
      NODE_EXTRA_CA_CERTS: "${HOME}/itca-r2.pem"

agents:
  bulk_classifier:
    system_prompt: prompts/classify.md
    provider: local_llm

  lead_reviewer:
    system_prompt: prompts/review.md
    model: claude-opus-4-8        # no provider → ambient Anthropic auth
```

`bulk_classifier` calls hit the proxy with `h200/gpt-oss-120b`; `lead_reviewer`
calls hit your normal Anthropic backend with Opus. Both run the same oracle
verbs with the same schema validation and tracing.
