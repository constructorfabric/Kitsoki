# Getting started — install Kitsoki and onboard your project

This guide is for a developer who already has a repository and wants to try
Kitsoki there. You do not need a Kitsoki source checkout: install the binary,
run it from your project root, review what it discovers, and commit the small
setup it writes.

To build or contribute to Kitsoki itself, use
[contributor-setup.md](contributor-setup.md).

## Fast path

From your project root, with the `kitsoki` binary on `PATH`:

```sh
kitsoki run
# type: onboard .
```

Kitsoki profiles the repo, shows you what it found, and waits for confirmation
before writing anything. After apply, run the readiness checks it records and
commit the `.kitsoki/` setup if it matches your project.

## 1. Install the binary

Download a prebuilt binary from
[GitHub Releases](https://github.com/bsacrobatix/Kitsoki/releases/latest) or the
[Download Kitsoki](https://bsacrobatix.github.io/Kitsoki/download.html) page.
Extract it, put `kitsoki` on your `PATH`, then check it:

```sh
kitsoki version
```

Kitsoki is a single binary. It embeds the base stories, the web UI, and the
skill/agent toolkit installed during onboarding.

## 2. Choose how agent calls run

Kitsoki can replay deterministic flows with no LLM, but normal interactive use
needs an agent backend for the steps that require model judgment. By default it
uses the first available option:

| Available | Harness | Use |
|---|---|---|
| `claude` CLI | `claude` | Reuse your Claude Code login. |
| `ANTHROPIC_API_KEY` | `live` | Call Anthropic directly. |
| Neither | `replay` | Replay fixtures only; not fresh work. |

You can force a harness when launching:

```sh
kitsoki run --harness claude
kitsoki run --harness live
```

Provider/model profiles are covered in
[harness profiles](guide/agents/harness-profiles.md).

If the default provider is not usable yet, run the guided local profile setup
from the same `kitsoki run` session:

```text
provider
```

That path checks installed/logged-in backend CLIs and credential env/file
sources, shows a patch preview, and writes only the gitignored
`.kitsoki.local.yaml` after you confirm. It records env-var references such as
`${OPENAI_API_KEY}`, never raw keys.

## 3. Onboard your repo

Run Kitsoki from the repository you want to onboard:

```sh
cd ~/code/my-project
kitsoki run
# > onboard .
# > continue
# > continue
```

The first `continue` lets you review the discovered profile. The final confirm
applies the setup: a project profile, a local dev-story instance, MCP
registration, and the skills/agents toolkit.

Discovery is read-only. If the toolkit or MCP install fails, onboarding stops
with a loud `init_tools_failed` state rather than reporting success. You can
retry from there or finish later with:

```sh
kitsoki init
```

## 4. Optional GitHub auth

For local issue/PR work, the shortest path is GitHub CLI auth:

```sh
kitsoki gh-agent login
source ~/.config/kitsoki/github.env
```

That reuses `gh auth login --web`, writes a local `0600` env file, and exposes
`GH_TOKEN`/`GITHUB_TOKEN` only to flows that request GitHub actions.

For repo-limited permissions, use the GitHub App path:

```sh
kitsoki gh-agent setup app \
  --name <app-name> \
  --local-only
kitsoki gh-agent setup attach --repo owner/name
kitsoki gh-agent token
source ~/.config/kitsoki/github.env
```

Hosted `@kitsoki` agents and webhook setup are covered in
[GitHub App setup](guide/integrations/github-app-setup.md).

## 5. What onboarding writes

Onboarding writes a small, auditable set of files:

| Path | Purpose |
|---|---|
| `.kitsoki.yaml` | Points Kitsoki at the project profile and story dirs. |
| `.kitsoki/project-profile.yaml` | Records stack, commands, conventions, and selected starter stories. |
| `.kitsoki/check-readiness.py` | Lists and runs the build/test/story-load checks discovered for this repo. |
| `.kitsoki/stories/<id>-dev/app.yaml` | A local dev-story instance that can be edited and extended. |
| `.mcp.json` | Registers the Kitsoki studio MCP server for MCP-capable clients. |
| `.agents/` and `.claude/` entries | Installs the Kitsoki skill/agent toolkit for Codex and Claude Code. |

None of this is hidden in a runtime cache. Review it like any other project
setup before committing.

## 6. First useful checks

Start the onboarded story from the project root:

```sh
kitsoki run
```

Then inspect the readiness checks onboarding recorded:

```sh
python3 .kitsoki/check-readiness.py --list
python3 .kitsoki/check-readiness.py --json
```

The report is written to `.artifacts/kitsoki-readiness.json`. Red checks are
project facts, not runtime crashes; fix or record them before relying on the
story for real work.

With `.mcp.json` registered, an MCP client can drive Kitsoki through the studio
tools. The installed `kitsoki-mcp-driver` agent is the intended Claude Code
driver:

```sh
claude --agent kitsoki-mcp-driver
```

Codex users can launch the same driver through Kitsoki, with the studio MCP
attached and shell access disabled:

```sh
kitsoki agent launch --agent kitsoki-mcp-driver --backend codex
```

The full runbook is the
[Studio MCP dogfood recipe](recipes/studio-mcp-dogfood.md#run-a-pure-kitsoki-driver).

## Read next

- [Evaluate Kitsoki](evaluate-kitsoki.md) explains when the structure is worth
  adopting.
- [Concept](architecture/concept.md) describes the control-inversion model.
- [Story architecture](stories/architecture.md) explains rooms, intents, and
  transitions.
- [Flow testing](tracing/testing.md) shows how to test without live LLM spend.
