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

The review is an outcome contract, not just a file preview. It must identify
the canonical test command, deterministic dev-server lifecycle when applicable,
branch and ticket-ID convention, the ordered ticket sources, and PR
destination/base/template policy. When repository evidence cannot answer one
of those questions, Kitsoki shows the default it used and the exact field in
`.kitsoki/project-profile.yaml` to update.

Every new profile includes local markdown intake under `.artifacts` and may add
any number of independently labelled remote sources. GitHub remotes are
discovered by repository identity, so an origin fork and an upstream repository
remain distinct even when their issue numbers overlap. The generated wrapper
binds the source list through `host.ticket_federation`; edit
`tracker.sources` to add or remove sources later.

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
| `.kitsoki/project-profile.yaml` | Records goals/postconditions, stack, commands, delivery policy, per-field resolution provenance, and selected starter stories. |
| `.artifacts/kitsoki-readiness.json` | Native readiness report written after the onboarding story's explicit readiness action. |
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

Then choose the explicit `readiness` action in the onboarding result to run the
native `host.dev.onboarding` readiness operation:

```sh
kitsoki run
```

The report is written to `.artifacts/kitsoki-readiness.json`. Red checks are
project facts, not runtime crashes; fix or record them before relying on the
story for real work. A green report proves the `onboarding` goal: the wrapper
loads, project gates run, and branch/ticket/PR policy is explicit. It does not
claim the later reference-corpus optimization goal is complete.

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

## 7. Rerun and update safely

Onboarding and readiness are idempotent. An unchanged onboarding rerun performs
no writes; readiness replaces its current report. For an existing project,
refresh deterministically discovered/default profile fields with a dry run:

```sh
kitsoki project-profile refresh --target /path/to/project
kitsoki project-profile refresh --target /path/to/project --json
```

After reviewing the candidate, apply it explicitly:

```sh
kitsoki project-profile refresh --target /path/to/project --apply
```

Fields recorded as `source: operator` are preserved. A manual value that differs
from its last managed resolution is promoted to operator-owned on refresh. The
command validates before writing. Generator-owned wrappers carry a
`kitsoki-managed-wrapper` content checksum and refresh their wiring when
profile-owned ticket sources change; an untouched legacy generated wrapper is
migrated once by a frozen exact fingerprint. A customized wrapper invalidates
that checksum and is never overwritten: refresh
returns `instance_update_required: true` and a validation warning until its
ticket binding, source projection, defaults, and host allow-list match.

The wrapper still imports `@kitsoki/dev-story`, so installing a new binary
updates the embedded base story without copying story content into the wrapper.
During source
development, select the staging story explicitly:

```sh
kitsoki --kitsoki-repo /Users/brad/code/Kitsoki/.capsules/staging/local run
```

The repo-wide flag affects every `@kitsoki/*` import. To override only
`dev-story`, use either a process-scoped environment variable or a persisted
kit-dev mapping:

```sh
KITSOKI_KIT_DEV_DEV_STORY=/absolute/path/to/Kitsoki/stories/dev-story kitsoki run

kitsoki kit dev dev-story --path /absolute/path/to/Kitsoki/stories/dev-story
kitsoki kit dev dev-story --clear
```

The scoped path must be the story directory containing `app.yaml`. Per-story
overrides win over `--kitsoki-repo` / `KITSOKI_REPO`, which win over the binary's
embedded story; clear old overrides when verifying a newly installed binary.

After onboarding, the validation goal is to turn independently RED/GREEN-proved
**repo-history capsules** into a Corpus Forge `corpus-receipt.v1`, optimize only
against its calibration cases, reserve a separate non-overlapping heldout
receipt for promotion, and prove a real bug-to-PR path. Today those are explicit
`repo-bakeoff`, Corpus Forge, `dogfood-marathon`, and `goal-seeker` surfaces—not
one shipped corpus-to-green optimizer command—and the repo-bakeoff harness still
requires a Kitsoki source checkout. See
[dev-story onboarding](stories/dev-story-onboarding.md#validation-after-onboarding)
for the exact boundary and prerequisites.

## Read next

- [Evaluate Kitsoki](evaluate-kitsoki.md) explains when the structure is worth
  adopting.
- [Concept](architecture/concept.md) describes the control-inversion model.
- [Story architecture](stories/architecture.md) explains rooms, intents, and
  transitions.
- [Dev-story onboarding](stories/dev-story-onboarding.md) defines the goal,
  resolution, rerun, base-story update, and validation contracts.
- [Flow testing](tracing/testing.md) shows how to test without live LLM spend.
