# Getting Started — running kitsoki locally

A from-zero walkthrough: install the toolchain, build and install the
`kitsoki` binary, verify it works, and (optionally) wire up a local
LLM via llama.cpp so cheap decisions run offline instead of hitting
Anthropic.

If you just want the 30-second version, see the **Quickstart** in the
[README](../README.md). This guide is the slower, fresh-machine path.

---

## 1. Prerequisites — `make setup`

`make setup` installs everything `make install` needs on a fresh
machine. It is idempotent — anything already present at a new-enough
version is skipped — and covers macOS (Homebrew), Debian/Ubuntu
(`apt`), and RockyLinux/RHEL (`dnf`).

```sh
make setup
```

It installs (and verifies versions of):

| Dependency | Why |
|---|---|
| Go ≥ 1.25 | compiles the binary |
| Node ≥ 20 | builds the runstatus SPA that gets embedded |
| pnpm ≥ 11 | SPA package manager (via corepack) |
| git, bash, curl | required; kitsoki also shells out to git + bash at runtime |
| jq, ffmpeg, gh | optional; needed by some `make` targets (`fix-tests`, `demo-tour`, GitHub integration) |

Project skills live under `.agents/skills/`, where Codex discovers
them directly. Setup also symlinks them into `.claude/skills/` so
Claude Code uses the same skill definitions. Re-run `make setup` any
time you add a skill.

> **Heads-up on PATH:** on Linux a fresh Go tarball install adds
> `/usr/local/go/bin` to your PATH via `/etc/profile.d/go.sh`. Open a
> new shell (or `source` it) before continuing so the new tools are
> visible.

---

## 2. Build and install — `make install`

`make install` runs `check-deps` (clear "run make setup" hint if a
tool is missing), builds the embedded web SPA, then `go install`s the
binary.

```sh
make install
```

The binary lands in your install dir and is named `kitsoki`:

- **macOS** → `~/.local/bin` (already on PATH for typical setups; macOS
  doesn't put `~/bin` on PATH).
- **Linux** → `~/bin`.
- Override with `make install INSTALLDIR=/somewhere/on/your/path`.

If the install dir isn't on your `PATH`, `make install` warns and tells
you how to fix it. To build a throwaway binary in the repo without
installing, use plain `make build` (produces `./kitsoki`) — or the
README's `go build -o kitsoki ./cmd/kitsoki`.

To remove it later: `make uninstall`.

---

## 3. Verify it works

A few cheap, no-LLM checks, fastest first.

**a. The binary runs:**

```sh
kitsoki version
```

**b. Deterministic flow tests pass (zero LLM cost).** This is the
best single signal that the runtime is healthy — flow tests drive
example apps through recorded cassettes, no network, no API key:

```sh
kitsoki test flows testdata/apps/cloak/app.yaml
```

Or run the whole Go suite (finishes in under ~10s):

```sh
make test
```

**c. Launch an example app.** `kitsoki run` opens the TUI — transcript
pane, action menu, inbox:

```sh
kitsoki run testdata/apps/cloak/app.yaml
```

Type free text or pick an action; sessions persist in
`$XDG_DATA_HOME/kitsoki/sessions.db`. `kitsoki run` auto-selects a
harness: the `claude` CLI if it's on your PATH (uses your existing
Claude Code login), else `live` if `ANTHROPIC_API_KEY` is set, else
deterministic `replay`. Force one with `--harness claude|live|replay`.

**d. (Optional) the web UI:**

```sh
make web-dev      # dev server with hot reload; logs via `make web-dev-logs`
```

If anything here fails, the `kitsoki-web-debug` and
`kitsoki-debugging` skills cover the common failure modes.

---

## 4. Start using it from your coding agent — Studio MCP

The easiest way to start using Kitsoki for real work is to attach the
**studio MCP** to your coding agent. From Claude Code, Codex, Cursor, or
another MCP client, the agent gets one control plane for Kitsoki:
author stories, validate and flow-test them, drive live sessions, inspect
traces, and render the TUI/web result.

Claude Code can use the repo-local `.mcp.json`:

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

Codex uses its MCP config instead:

```sh
codex mcp add kitsoki -- kitsoki mcp --stories-dir stories
codex mcp list
```

For story development and testing, start with the
[Studio MCP dogfood recipe](recipes/studio-mcp-dogfood.md). It gives a
practical loop for running a constrained driver agent that can only use
`mcp__kitsoki__...` tools, then verifying the result with traces,
`story.validate`, `story.test`, and no-LLM flow fixtures. This is the
best first pattern when you want to improve stories through the same
surface external users and non-Claude models will use.

---

## 5. Optional — a local LLM via llama.cpp

By default the LLM decision points fork the `claude` CLI
(`agent.claude`). You can instead route the **small, high-frequency**
decisions — semantic routing's LLM tier and schema-bounded `decide`
gates — to a local [llama.cpp](https://github.com/ggml-org/llama.cpp)
`llama-server` over OpenAI-compatible HTTP. This is the
`builtin.local_llm` agent backend. It's opt-in and additive: a story
only uses it if it *both* declares the plugin *and* routes a call to
it.

Full reference: [`docs/architecture/agent-plugin.md` §9 Local model
backend](architecture/agent-plugin.md#9-local-model-backend).

### Two modes

- **Managed mode** (`model:`): zero-touch. On the first `agent.local`
  call kitsoki fetches a pinned `llama-server` release + the model's
  GGUF weights into the user cache dir (`~/Library/Caches/kitsoki` on
  macOS, `~/.cache/kitsoki` on Linux; override with
  `KITSOKI_CACHE_DIR`), sha256-verifies them, spawns the sidecar on
  `127.0.0.1`, and health-gates before use.
- **Endpoint mode** (`endpoint:`): attach to a `llama-server` you run
  yourself; kitsoki never fetches, spawns, or owns its lifecycle.

> **Apple Silicon (darwin/arm64) works zero-touch.** Managed mode is
> filled and proven on both `linux/amd64` and `darwin/arm64` (llama.cpp
> release b9444 + Qwen2.5-1.5B). The macOS archive ships Metal-enabled
> dylibs that resolve via `@loader_path`, so no extra setup is needed —
> just set `model:` and the first call provisions everything. Endpoint
> mode (below) is still available on every platform if you'd rather run
> the server yourself.

### Managed mode (recommended — zero-touch on macOS and Linux/amd64)

Let kitsoki fetch and run everything. Just set `model:` instead of
`endpoint:`:

```yaml
agent_plugins:
  agent.local:
    plugin: builtin.local_llm
    model: qwen2.5-1.5b-instruct
    grammar: true                      # best-effort schema → grammar constraint
    # port: 8080                       # optional sidecar bind port
    # server_bin: /path/to/llama-server # optional; skip the binary fetch
```

The first `agent.local` call downloads the pinned `llama-server`
(~10 MB on macOS) and the GGUF weights (~1.1 GB), sha256-verifies both,
spawns the sidecar, and health-gates it. On Apple Silicon a warm
grammar-constrained `decide` then returns in well under a second
(Metal-accelerated). To pre-warm the cache so no turn pays the download
cost:

```sh
make fetch-llama-server          # fetch the llama-server binary
make fetch-models                # fetch the default model's weights
make fetch-models MODEL=<id>     # fetch a specific model
```

### Endpoint mode (works on every platform)

Run the server yourself and attach to it. On macOS:

1. Install and run a llama.cpp server:

   ```sh
   brew install llama.cpp
   # download a small instruct GGUF, e.g. Qwen2.5-1.5B-Instruct Q4_K_M,
   # then serve it on an OpenAI-compatible endpoint:
   llama-server -m ./qwen2.5-1.5b-instruct-q4_k_m.gguf \
       --host 127.0.0.1 --port 8080 --parallel 4
   ```

   `--parallel N` matters: routing fires on nearly every turn, and the
   default single decode slot is the usual source of stacked latency.

2. Declare the plugin in your story's `app.yaml`:

   ```yaml
   agent_plugins:
     agent.local:
       plugin: builtin.local_llm
       endpoint: http://127.0.0.1:8080
       grammar: true        # best-effort schema → grammar constraint
   ```

### Routing a call to the local agent (either mode)

Route a call to it (e.g. on an agent or a `host.agent.decide` effect)
with `agent: agent.local`. `agent.claude` stays the default for
everything that doesn't name it.

### Good to know

- **`grammar:` is a bias, not a guarantee.** llama.cpp constrains JSON
  *shape* (within its supported grammar subset), not numeric *range*.
  `ValidateSubmission` — which kitsoki runs on every agent answer — is
  the actual structural guarantee. On a validation reject the call
  **falls back to `agent.claude` once** (same `call_id`), so local
  models degrade gracefully rather than failing the turn.
- Spell out field scales in your `decide` prompt; a small model may
  emit `confidence: 95` for a `0..1` field (caught + fixed by the
  fallback).
- Nothing binary is committed or bundled; the cache dir is gitignored.

---

## 6. Choosing the model & provider — harness profiles

A session can switch which LLM **backend/provider** and **model** answer it,
live, without restarting. You declare named **harness profiles** in
`.kitsoki.yaml` and pick one from the TUI (`/provider`, `/model`) or the web
header dropdowns; the switch takes effect on the next turn. Full reference:
[harness profiles](architecture/harness-profiles.md).

The checked-in `.kitsoki.yaml` ships only the ambient `claude-native` profile,
so it works out of the box. Secret-bearing profiles (synthetic, codex, a local
llama.cpp) live in a gitignored **`.kitsoki.local.yaml`** that's deep-merged on
top (local wins) — copy the template and set your key:

```sh
cp .kitsoki.local.yaml.example .kitsoki.local.yaml   # synthetic/codex/llama profiles
export SYNTHETIC_API_KEY=syn_...            # your shell / profile / .envrc
kitsoki web --stories-dir stories           # pick the provider in the header
#   …or in the TUI:  /provider synthetic-claude  ·  /model 1
```

The key is referenced as `${SYNTHETIC_API_KEY}` in the YAML — never inlined,
never recorded in a trace. Each agent call's trace row shows `profile=… ·
model=… · effort=…` so you can see exactly which backend/model answered.

Beyond the provider, a profile can offer a **model** picker (e.g. claude's
`opus`/`sonnet`/`haiku`, or `models_endpoint:` to pull a provider's full
always-on model list live) and, where the model supports it, an **effort**
picker (`low`…`max`, applied as claude's `--effort`). See
[harness profiles](architecture/harness-profiles.md).

### Verified providers (and the auth gotchas that matter)

These three were driven end-to-end through kitsoki — each genuinely answered a
real `host.agent.ask`:

| Profile | Backend | Result |
|---|---|---|
| `claude-native` | claude | Your native Anthropic Claude Code subscription — works out of the box. |
| `synthetic-claude` | claude + `ANTHROPIC_BASE_URL` | claude-code pointed at **synthetic.new** (GLM-5.1 via `syn:large:text`) — **works**. |
| `codex-native` | codex | Your codex **ChatGPT** subscription (GPT-5) — works out of the box. |

Two non-obvious things, learned the hard way — read these before you debug a
"wrong" answer:

- **A model will happily misidentify itself.** Asked "which model are you?",
  synthetic.new's GLM-5.1 (through claude-code) replied *"I am Claude Sonnet 4."*
  That is the **model hallucinating**, not a routing bug — the call really went to
  synthetic (real Anthropic has no `syn:large:text` model, so the request could
  only have been served by synthetic). **Verify the route by the billed model /
  the server's `model` field, never by what the model says it is.**

- **`claude` honors `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` even with an
  active subscription** — so `synthetic-claude` works with just env. **`codex`
  does not**: when you are logged into a ChatGPT account, codex **ignores**
  `OPENAI_BASE_URL`/`OPENAI_API_KEY` and rejects a non-OpenAI model
  (`"the 'syn:large:text' model is not supported when using Codex with a ChatGPT
  account"`). To run **codex against synthetic.new** you must declare a provider
  in `~/.codex/config.toml` (a `[model_providers.synthetic]` block with
  `base_url`/`env_key`) and select it — env alone is not enough. `codex-native`
  (the subscription) needs none of this.

The example config also ships `synthetic-codex` (the codex-on-synthetic profile,
which needs the `config.toml` step above) and `llama-local`
([§5](#5-optional--a-local-llm-via-llamacpp)).

---

## 7. Using kitsoki in another repo

Once `kitsoki` is installed (step 2) it's a single binary on your
PATH — it isn't tied to this checkout. A *story* is just an `app.yaml`
state machine; the binary runs it against whatever repo it's pointed
at. Host calls (`host.bash`, agent work, git operations) run in the
**process working directory** unless a story field sets an explicit
`cwd:`. So the simplest way to drive another repo is to **run kitsoki
from inside it**, passing the story by absolute path.

> **Setting up a project for keeps?** The ad-hoc "run from inside it"
> approach below is great for a one-off demo. To install a committed,
> working kitsoki environment *into* your repo — a runnable dev-story
> instance, the studio MCP registered for your coding agent, and the
> skill/agent toolkit — use **project onboarding** instead:
> [`project-onboarding.md`](project-onboarding.md).

### Example — a demo against `~/code/cyberware-rust`

```sh
cd ~/code/cyberware-rust
kitsoki run /Users/brad/code/Kitsoki/stories/<story>/app.yaml
```

Everything the story shells out to — repo searches, builds, git — now
operates on `cyberware-rust`, while the story definition still lives in
the Kitsoki checkout. The same works for `kitsoki web` and `kitsoki
test flows`.

### Pointing engine/dogfood tooling at the target repo

Some commands resolve a target repo explicitly rather than from cwd:

- `kitsoki bug` writes bug files under `--target-dir` or `$KITSOKI_REPO`
  (engine target) — see `kitsoki bug --help`.
- Stories that need an absolute base path can reference
  `${KITSOKI_APP_DIR}` (the story's own directory) and read a target
  repo from an env var you set, e.g. `KITSOKI_REPO=~/code/cyberware-rust`.

A clean convention for a recurring demo is to set the target once in
the shell you launch from:

```sh
export KITSOKI_REPO="$HOME/code/cyberware-rust"
cd "$KITSOKI_REPO"
kitsoki run /Users/brad/code/Kitsoki/stories/<story>/app.yaml
```

> If your demo story declares a `builtin.local_llm` agent (§5), the
> same endpoint/managed rules apply regardless of which repo you run
> from — the cache and sidecar are per-user, not per-repo.

---

## Where to go next

| Doc | What |
|---|---|
| [`docs/architecture/concept.md`](architecture/concept.md) | The thesis — start here for the *why*. |
| [`docs/architecture/overview.md`](architecture/overview.md) | Layers, packages, data flow, persistence. |
| [`docs/stories/authoring.md`](stories/authoring.md) | How to write your own `app.yaml`. |
| [`docs/architecture/developer-guide.md`](architecture/developer-guide.md) | Build, test, debug, add features. |
| [`.kitsoki/stories/kitsoki-dev/README.md`](../.kitsoki/stories/kitsoki-dev/README.md) | Dogfood mode — kitsoki fixing kitsoki. |
