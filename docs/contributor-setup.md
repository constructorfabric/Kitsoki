# Contributor Setup — build Kitsoki from source

This guide is for people changing Kitsoki itself. If you want to use Kitsoki in
your own project, start with [getting-started.md](getting-started.md).

---

## 1. Install prerequisites

`make setup` installs everything `make install` needs on a fresh machine. It is
idempotent and covers macOS (Homebrew), Debian/Ubuntu (`apt`), and
RockyLinux/RHEL (`dnf`).

```sh
make setup
```

It installs or verifies:

| Dependency | Why |
|---|---|
| Go >= 1.25 | Compiles the binary. |
| Node >= 20 | Builds the runstatus SPA that gets embedded. |
| pnpm >= 11 | SPA package manager, via corepack. |
| Chromium for Playwright | Browser payload for runstatus and TUI visual QA. |
| git, bash, curl | Required runtime and development tools. |
| jq, ffmpeg, gh | Optional tools used by demo, release, and GitHub-integration targets. |
| python3 >= 3.9 | Runtime: the agent toolkit shells out to Python — the goal-seeker/punch-list scripts and the decomposition/docs lints under `tools/*.py`. |

Project skills live under `.agents/skills/`, where Codex discovers them
directly. Setup also symlinks them into `.claude/skills/` so Claude Code uses
the same definitions. Re-run `make setup` after adding a skill.

## 2. Build and install

```sh
make install
```

`make install` runs dependency checks, builds the embedded web SPA, then installs
the `kitsoki` binary.

Default install locations:

| Platform | Install dir |
|---|---|
| macOS | `~/.local/bin` |
| Linux | `~/bin` |

Override with:

```sh
make install INSTALLDIR=/somewhere/on/your/path
```

To build a throwaway binary in the repo:

```sh
make build
```

To remove an installed binary:

```sh
make uninstall
```

## 3. Verify the checkout

Run the cheap binary check:

```sh
kitsoki version
```

Run the deterministic fixture smoke:

```sh
kitsoki test flows testdata/apps/cloak/app.yaml
```

Run the full local gate:

```sh
make test
```

Flow tests use recorded fixtures and do not call a real LLM.

## 4. Launch the local UI

The TUI:

```sh
kitsoki run
```

The web UI:

```sh
make web-dev
```

Logs for the web dev server:

```sh
make web-dev-logs
```

If the web UI fails to load, use the `kitsoki-web-debug` skill. If a story
session routes unexpectedly or bounces to idle, use the `kitsoki-debugging`
skill.

## 5. Studio MCP for story work

This repo ships a `.mcp.json` for the Kitsoki studio MCP. It gives coding
agents one control plane for story validation, flow tests, session driving,
trace inspection, and TUI/web rendering.

Claude Code can adopt the checked-in driver agent:

```sh
claude --agent kitsoki-mcp-driver
```

Codex can launch the mirrored `.codex/agents/kitsoki-mcp-driver.toml` driver
through Kitsoki, which attaches the studio MCP and disables Codex shell access:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex
```

For headless delegation, use the wrapper:

```sh
tools/mcp-drive/drive.sh
```

Full runbook:
[Studio MCP dogfood recipe](recipes/studio-mcp-dogfood.md#run-a-pure-kitsoki-driver).

## 6. Optional local LLM

Kitsoki can route small, high-frequency decisions to a local
OpenAI-compatible llama.cpp server through the `builtin.local_llm` backend. See
[agent-plugin.md](architecture/agent-plugin.md#9-local-model-backend) for the
current managed and endpoint modes.

## 7. Next docs

| Doc | What |
|---|---|
| [architecture/overview.md](architecture/overview.md) | Runtime layers, packages, data flow, persistence. |
| [guide/development/developer-guide.md](guide/development/developer-guide.md) | Contributor-oriented implementation guide. |
| [stories/authoring.md](stories/authoring.md) | Authoring vocabulary and story structure. |
| [tracing/testing.md](tracing/testing.md) | Flow tests, traces, replay, and debugging. |
