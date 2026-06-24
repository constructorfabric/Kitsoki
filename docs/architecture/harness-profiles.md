# Harness Profiles

> **Status:** operator-facing reference for the `harness_profiles:` block and
> the live provider/model switch. A **harness profile** is a named, operator-
> selectable bundle of the agent-selection axes — *which backend CLI is forked*,
> *which endpoint it talks to*, and *which model it defaults to* — that a live
> session can switch between from the TUI (`/provider`, `/model`) or the web
> header picker. The switch takes effect on the **next** turn.

A kitsoki session has four orthogonal agent-selection axes, each documented on
its own page:

- **backend** — which coding-agent CLI is forked
  ([agent-backends.md](./agent-backends.md)): `claude | copilot | codex`.
- **provider** — an env retarget of the forked CLI's subprocess
  ([agent-providers.md](./agent-providers.md)).
- **plugin** — an alternate component that answers
  ([agent-plugin.md](./agent-plugin.md)), e.g. `builtin.local_llm` (llama.cpp).
- **model** — the `--model` passed to the call.

Historically each axis was frozen at startup and reachable only through flags,
env, or per-story YAML — never from a live session. A **harness profile**
collapses these axes behind one operator-facing name so an operator picks a
*profile* (and optionally a *model*) instead of learning the taxonomy.

## Configuration

Profiles are declared in `.kitsoki.yaml` (and its local override — below; the
same file that carries `story_dirs` and the implicit-root `root:` block — see
[`imports.md`](../stories/imports.md) "The blank root that grows"), loaded on
both `kitsoki run` (TUI) and `kitsoki web`.

### Shared file and local override

Config loads from **two layers** — the same dichotomy as Claude Code's
`settings.json` + `settings.local.json`:

| File | Tracked? | Holds |
|---|---|---|
| `.kitsoki.yaml` | **checked in** | the team baseline — profiles that load for *everyone* with zero setup (ambient `claude-native`, no `${VAR}`). |
| `.kitsoki.local.yaml` | **gitignored** | your personal, secret-bearing, machine-specific overrides — synthetic/codex/local-llm profiles whose `${VAR}` env would otherwise hard-fail the shared file for a teammate without the key. Copy `.kitsoki.local.yaml.example`. |

`Load` (`internal/webconfig`; `LocalConfigPath` derives the sibling path by
inserting `.local` before the extension) **deep-merges** the local file on top
of the shared one, **local wins**, *before* validation — so `${VAR}` expansion
and the backend/model/effort/`default_profile` checks all run once on the
effective config:

- `story_dirs`, `default_profile` — a non-empty local value replaces the
  baseline's; `default_profile` may legally name a profile only the local file
  declares.
- `harness_profiles` — merge **by name**: baseline-only profiles survive, local
  profiles are added, and a profile declared in both is replaced **whole** by
  the local one (restate every field you want — you never restate the *other*
  profiles).

A missing local file contributes nothing, so a fresh checkout runs off the
shared baseline alone.

```yaml
# .kitsoki.yaml  —  checked in; the baseline that must load for everyone.
default_profile: claude-native          # the profile new sessions start on
harness_profiles:
  claude-native:                        # your native Anthropic Claude Code subscription
    backend: claude                     # (ambient auth; the default — no secrets)
    model: opus
    models: [opus, sonnet, haiku]       # claude's short model names
    effort: medium
    efforts: [low, medium, high, xhigh, max]   # claude supports --effort
```

```yaml
# .kitsoki.local.yaml  —  gitignored; deep-merged on top, local wins.
# default_profile: synthetic-claude     # (optional) start new sessions here
harness_profiles:
  synthetic-claude:                     # claude-code pointed at synthetic.new
    backend: claude                     # base URL omits /v1 (claude appends /v1/messages)
    model: hf:zai-org/GLM-5.2
    models: [hf:zai-org/GLM-5.2]        # static fallback
    models_endpoint: https://api.synthetic.new/openai/v1/models  # the full always-on list
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"

  synthetic-codex:                      # the codex CLI pointed at synthetic.new
    backend: codex                      # OpenAI base URL includes /v1
    model: hf:zai-org/GLM-5.2
    models: [hf:zai-org/GLM-5.2]
    models_endpoint: https://api.synthetic.new/openai/v1/models
    env:
      OPENAI_BASE_URL: https://api.synthetic.new/openai/v1
      OPENAI_API_KEY: "${SYNTHETIC_API_KEY}"

  codex-native:                         # codex's own config/auth
    backend: codex
    model: gpt-5-codex
    models: [gpt-5-codex, gpt-5]

  llama-local:                          # a local llama.cpp model (plugin path)
    plugin: builtin.local_llm
    model: h200/gpt-oss-120b
```

| Field | Meaning |
|---|---|
| `backend` | `claude \| copilot \| codex`; empty ⇒ `claude`. Ignored when `plugin` is set. |
| `model` | default `--model` for the profile; when the profile is active it supersedes story-local agent model defaults so the selected provider receives a compatible model id. |
| `models` | static catalog the `/model` command and web dropdown list. When set, the model pick must be a member (of the full catalog, incl. fetched — below). |
| `models_endpoint` | OpenAI/Anthropic `/models` URL (e.g. `https://api.synthetic.new/openai/v1/models`); its always-on model ids are **fetched and merged** into the catalog at selection time (auth from this profile's env), so the full live list is offered — not a hand-maintained subset. A fetch failure falls back to `models`. Cached per profile. |
| `effort` | default reasoning effort (`low\|medium\|high\|xhigh\|max`), applied where the backend/model supports it (`claude --effort`). |
| `efforts` | catalog the `/effort` command + web effort dropdown list — declare only on profiles whose backend/model supports effort (codex ignores `--effort` today). Empty ⇒ no effort control. |
| `env` | env overrides merged onto the forked CLI subprocess. `${VAR}`-expanded at **load time** (an unset var is a hard error, mirroring `providers:`). **Never recorded in traces.** |
| `plugin` | routes through an agent plugin (e.g. `builtin.local_llm`) instead of forking a backend CLI. |
| `default_profile` (top-level) | the profile new sessions start on; must name a declared profile. Omitted ⇒ the flag-derived static default (today's `--agent`/`--model`). |

**Secrets** never live in the file: `env` values use `${VAR}` interpolation
against the process environment. With no `harness_profiles:` block the static
flag/env path is preserved byte-for-byte.

### Why `env` works for codex/openai, not just claude

A provider/profile's `env` is merged onto **every** backend CLI's subprocess
environment (`internal/host/agent_runner.go`, `envWithProvider`), not only the
`claude` one. So a `claude`-backed profile sets `ANTHROPIC_BASE_URL`/
`ANTHROPIC_AUTH_TOKEN` (which the `claude` CLI reads) and a `codex`-backed
profile sets `OPENAI_BASE_URL`/`OPENAI_API_KEY` (which the `codex` CLI reads).
That is what makes "synthetic.new on codex" real with no engine change — the
merge was already backend-agnostic; only the *variable names* are backend-
specific.

## Selecting a profile

| Surface | How |
|---|---|
| **TUI** | `/provider` lists the profiles (active one marked) and `/provider <name\|n>` switches; `/model` lists the active profile's catalog and `/model <id\|n>` switches the model; `/effort` does the same for the reasoning effort (where the profile declares `efforts:`). See [docs/tui/README.md](../tui/README.md). |
| **Web** | A provider dropdown, a dependent model dropdown, and (where the profile declares `efforts:`) an effort dropdown in **both** the Observe and Drive headers; switching fires the `runstatus.session.set_selection` RPC. See [docs/web/README.md](../web/README.md). |

Both drive the same orchestrator API — `Profiles()`, `Selection()`,
`SetSelection(profile, model, effort)` — exposed to the web via the optional
`HarnessController` driver interface.

### Resolution & precedence

The selection is held per session behind a mutex and **resolved once per
dispatch**. Precedence (highest first):

> per-effect `with: { provider }` / named story provider › **active profile model/env** › agent model default › flag-derived static default

So an operator-selected profile like `synthetic-claude` can replace story-pinned
Claude model names with the profile's compatible `hf:` model id. A call that
explicitly names a story `provider:` still selects that provider instead of the
session profile (`applyProvider` in `internal/host/agents.go`).

### Next-turn semantics

A switch rebuilds the selection lazily: **every interpretive call from the switch
on uses the new selection; the one already in flight finishes on the old one.**
There is no mid-flight cancellation. A single snapshot per dispatch means a
concurrent switch can never tear one call.

## Trace

Every `agent.call.start` already stamps the model; a session that selected a
profile also stamps `profile` (`AgentCalledPayload.Profile`) and, when set,
`effort`, so a transcript line reads `agent.decide · profile=claude-native ·
model=opus · effort=high`. The web trace renders these as a chip on each agent
row, so the trace provenance matches the picker. Only the profile name, backend,
model, and effort are recorded — **never the `env` secrets**.

## Real-provider runbook

The three providers the feature targets, end to end:

1. **Put your synthetic.new key in the environment** the kitsoki process inherits
   — it is referenced as `${SYNTHETIC_API_KEY}` in `.kitsoki.yaml`, never inlined:

   ```bash
   export SYNTHETIC_API_KEY=sk-...        # in your shell / profile / .envrc
   ```

   (synthetic.new's Anthropic-compatible base is `https://api.synthetic.new/anthropic`
   — claude-code appends `/v1/messages` — and the OpenAI-compatible base is
   `https://api.synthetic.new/openai/v1`. Use explicit `hf:` model ids
   (for example `hf:zai-org/GLM-5.2`) to avoid rejected alias names.)

2. **Native Anthropic Claude Code** — `claude-native`: ambient auth, nothing to
   set. Verified live: a real `kitsoki turn` routes free text → intent with the
   `claude` backend (no cassette).

   > **Auth-precedence gotchas (verified end-to-end):**
   > - `claude` honors `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` **even with an
   >   active subscription**, so `synthetic-claude` works with env alone.
   > - A model will misidentify itself ("I am Claude Sonnet 4" from GLM-5.1) — that
   >   is hallucination, not a routing bug. **Confirm the route by the billed model
   >   / the server's `model` field, never by the model's self-description.**
   > - `codex` **ignores** `OPENAI_BASE_URL`/`OPENAI_API_KEY` when logged into a
   >   ChatGPT account (it rejects the non-OpenAI model). To run `synthetic-codex`
   >   you must declare a `[model_providers.synthetic]` block in
   >   `~/.codex/config.toml` and select it; `codex-native` (the subscription)
   >   needs none of this.

3. **Launch with the profiles and pick live:**

   ```bash
   kitsoki web --stories-dir stories            # or: kitsoki run <story>/app.yaml
   ```

   In the web header, pick `synthetic-claude` (and a model from its catalog), or
   `synthetic-codex`, then submit a turn. The trace row shows `profile=…` and the
   chosen model's real answer. In the TUI, `/provider synthetic-codex` then drive.

Automated tests never exercise these — real-LLM verification is operator-driven
and gated, per CLAUDE.md.

## Backward compatibility

Fully additive, default-off. Existing flags/env/`providers:`/`agent_plugins:`
keep working unchanged; a profile is a *named* bundle of the same overrides
applied through the same merge points. With no profiles declared, `/provider`
and `/model` report the single flag-derived default and the web picker hides.
