Do implementation work in managed clone-backed capsule workspaces, not git
worktrees. The primary checkout is protected as read-mostly; agents should not
run raw `git worktree`, `git clone`, rebase, merge, or teardown commands for
normal development. Delegate the lifecycle to:

```
scripts/dev-workspace.sh create --id <id> --branch <branch> --bootstrap
scripts/dev-workspace.sh status <id>
scripts/dev-workspace.sh commit <id> --message "<message>"
scripts/dev-workspace.sh merge <id> --gate "<focused validation>" --teardown
```

The script creates workspaces under `.capsules/workspaces`, writes the Kitsoki
capsule/clone sentinels, bases normal development on `staging/local`, runs the
bootstrap target when requested, imports completed local work back into
`staging/local`, and removes the workspace on `--teardown`. If the target branch
advanced, the script rebases the workspace onto the current target; rerun
focused validation before retrying a failed/conflicted merge. Do not manually
chmod the primary checkout except to repair the guard itself.

See `docs/dev-workspaces.md` for the full lifecycle contract, metadata files,
failure modes, recovery rules, and validation commands.

Local developer bug loops should stay local by default. File iterative,
developer-found, or dogfood bugs as local artifact tickets under
`.artifacts/issues/bugs` with their evidence sidecars under `.artifacts`; do not
create GitHub issues for those local stabilization iterations unless the user
explicitly asks. Use the GitHub issue sink only when the issue itself is the
handoff boundary, such as a non-technical user report, an autonomous GitHub
agent job, or a remote collaboration flow where the agent is expected to fix the
issue and open a PR.

Keep the primary checkout's `main` clean, green, and reserved for remote sync or
explicit final promotion. It is acceptable for tests to fail temporarily inside
a managed workspace while implementation is in progress. When the task is
complete, stabilize in that workspace, run focused validation, commit only your
work, and merge to `staging/local` through
`scripts/dev-workspace.sh merge <id> --gate "<focused validation>" --teardown`.
Do not stop at a workspace commit for completed implementation work unless the
user explicitly asks you not to merge. Do not land local iterative work directly
to `main`; final local promotion uses the staging capsule flow below after the
stabilization gate. Open or retarget a GitHub PR only
after the local branch is stabilized; WIP/draft PRs that are not ready for main
CI should use a non-main base prefix such as `agent/*`, `integration/*`, or
`staging/*`.

Final local promotion from staging to `main` goes through the staging capsule,
not an ad hoc branch merge. Keep `.capsules/staging/local` as a managed capsule
checkout on branch `staging/local` (`scripts/dev-workspace.sh create --root
.capsules/staging --id local --branch staging/local --base staging/local
--target main --bootstrap`); run staging commands with `make test-staging`,
`make web-dev-staging`, `make site-dev-staging`, and `make install-staging`.
Before staging validation or final promotion, run
`scripts/refresh-staging-local.sh` from the primary checkout. It checks
`origin/main` first and stops with the required `sync-main-from-remote` steps if
local `main` is stale, then refreshes `.capsules/staging/local` from local
`staging/local`, rebases it onto local `main`, and imports the refreshed
`staging/local` ref back into the primary checkout. Use `--remote upstream` when
that is the authoritative remote; use `--skip-remote` only when remote freshness
is intentionally irrelevant. To promote, run `scripts/merge-to-main.sh` from the
primary checkout. It rebases the staging capsule onto local `main`, runs
`make test` there by default, then fast-forwards protected `main`. Skipping that
gate requires explicit `--force`; use it only when an equivalent gate has
already run.

Prefer GitHub PRs for review and landing, but do not burn CI on every
work-in-progress agent branch. The CI workflow is configured so `pull_request`
runs target `main`; GitHub branch filters on `pull_request` match the PR base
branch, not the source branch. Open draft or staging PRs against a non-main
base branch prefix such as `agent/*`, `integration/*`, or `staging/*` when the
PR is not ready for CI. Retarget or promote the finished PR to `main` only when
it is ready for the required CI gate and GitHub merge.

When local `main` must be reconciled with `origin/main` or `upstream/main`, do
not run `git pull` in the protected primary checkout. From the primary checkout,
run the scripted sync helper:

```
scripts/sync-main-from-remote.sh --remote origin
```

or `--remote upstream`. The sync helper owns its internal git operations. If
conflicts occur, prefer:

```
scripts/sync-main-from-remote.sh --remote origin --auto-resolve
```

with `KITSOKI_SYNC_RESOLVE_CMD` set to the LLM/agent command that resolves and
stages conflicts, and `KITSOKI_SYNC_REVIEW_CMD` set to a separate read/check
agent command that verifies no local or remote work was dropped. For manual
resolution, resolve conflicts inside the helper-created integration workspace,
rerun the helper with `--continue <branch>` and `KITSOKI_SYNC_REVIEW_CMD` set,
validate there, and only then let the helper land the integration branch.

Project skills live in the Codex-standard `.agents/skills/<name>/SKILL.md` location. Claude Code does not auto-discover that directory, so `make setup` links every `.agents/skills/*/SKILL.md` into `.claude/skills/` (relative symlinks; `.claude/` is gitignored). After adding a new skill, re-run:

```
make setup
```

To link a single skill by hand (e.g. without a full setup run):

```
ln -s "../../.agents/skills/<name>" .claude/skills/<name>
```

When a task is broad, naturally parallel, or asks for Claude-Code-like worker
fan-out from Codex, prefer the repo skill `.agents/skills/kitsoki-dynamic-workflows/SKILL.md`.
Use Kitsoki's Studio MCP `workflow.*` tools to create, validate, launch, status,
and export the workflow, then drive the launched session with `session.*` and
verify with deterministic `story.*` / render tools. Keep small single-file or
clearly linear edits in normal Codex flow; use dynamic workflows when Kitsoki
should own the worker dispatch, trace, receipt, and reusable story artifacts.
The supervising Codex session must independently inspect the receipt, trace,
diff, untracked files, and validation gates instead of trusting worker claims.

After creating a new managed workspace, run `make bootstrap-workspace` from
inside it before running `go run ./cmd/kitsoki` or any Playwright spec. Passing
`--bootstrap` to `scripts/dev-workspace.sh create` does this automatically; it
stages the embed-only stories/SPA dirs, installs `tools/runstatus` node_modules,
and warms the Go build cache, all of which are otherwise empty/cold in a fresh
clone.

When we do an implementation based on a proposal, the goal is to complete the proposal implementation and move the content to proper narrative docs and delete the proposal - don't leave unfinished work unless specifically instructed, and if so, update the proposal to summarize the completed aspect and focus on the remaining work.

use the `.context` folder for transient markdown files like proposals, summaries, etc... and use the `.artifacts` folder (with subfolders as necessary) for any generated artifact for review that shouldn't be committed.  following these guidelines will help to avoid bot pollution and cruft in the repo.

Slidey is a sister project to Kitsoki. Use Slidey decks wherever possible and
reasonable for narrative, visual, temporal, comparative, or evidence-rich
outputs: demos, QA/readiness reports, bug evidence, workflow walkthroughs,
comparison studies, and product review artifacts. JSON stays the canonical
machine artifact and markdown stays useful for quick audit/review, but the
preferred human presentation surface is a Slidey deck when it adds clarity
instead of ceremony. Provide the `.slidey.json` deck link for review. Do not
render Slidey decks to MP4, and do not bundle them to HTML, unless the user
explicitly asks for that derived artifact; the VS Code Slidey previewer is the
preferred review surface. Put generated deck renders and throwaway review
artifacts under `.artifacts`; commit source decks/bundles only when they are
intended to live under `docs/decks/` per `docs/media/README.md`.

Any demo, QA proof, deck, or video that claims coverage of a product-journey
scenario or transport must start from the universal scenario mechanism:
`tools/product-journey/scenarios.json`, `tools/product-journey/run.py --emit-run
--transport ...`, and/or `stories/scenario-qa/app.yaml`. Bespoke Playwright,
xterm.js, VS Code, rrweb, or shell recorders are capture adapters only: they must
consume the generated run bundle/driver-plan capture routes, avoid private
hard-coded case lists, attach evidence back with `--attach-evidence`, and record
driver events in the same run. Before adding or refreshing such a recorder, read
and apply `.agents/skills/scenario-qa/SKILL.md`,
`.agents/skills/kitsoki-ui-demo/SKILL.md`,
`.agents/skills/kitsoki-ui-qa/SKILL.md`, and, when the story surface changes,
`.agents/skills/kitsoki-story-authoring/SKILL.md`; update any skill that would
have prevented a missed scenario ownership rule.

Automated testing should never use a real LLM or incur costs - mock agents via cassettes should be used in all cases.  Tests which require real LLM must be gated and only done when specifically requested and required - never automatically or without checking first.

Use hermetic capsules for reusable repository/workspace fixtures. Prefer `capsules/<name>/capsule.yaml` plus `internal/capsuletest.Open(t, "<name>")` over ad hoc `git init` setup in tests, story fixtures, and agent validation. Only keep bespoke temp repos when the behavior under test is the repository creation/bootstrap process itself or the exact git command sequence. When adding or migrating these fixtures, use the `.agents/skills/capsules/SKILL.md` project skill.

use dependency injection patterns wherever relevant.

principle of least surprise.

`AskUserQuestion` is hard-denied in every dispatched `claude -p` agent (it auto-resolves with empty answers when headless — a silent landmine). When a live operator surface is attached, agent questions are instead forwarded into kitsoki via the operator-ask bridge (the `mcp__operator__ask` tool) and surfaced on web + TUI; when no operator is attached (cassettes/flows/headless) no replacement tool is added and the agent proceeds on its own. See `docs/architecture/operator-ask.md`.

when in doubt always save a markdown into .context for review later - much easier to check/review than staying in the conversation and requiring an extra turn.

commit when you're done with your work and commit only your work - this helps to avoid a mess in the repo.  There may be parallel agents working.  Keep a minimum number of commits and amend as you go where there's no value in separate commits.  Separate key decisions or aspects in clean commits to enable bisect and reverts to work well and not create a mess.

avoid generating a binary of kitsoki for testing - just use go run unless there's some very specific reason that won't work (I think there's issues related to file embedding... not sure)

the kitsoki architecture provides extensive flow testing and mocking capabilities - both synthetic and recorded - to enable thorough testing and demonstration without LLM usage.  make sure to de-risk all cases with flow tests and mocked interactions - and when doing live integration tested if mocks/flows change ensure the tests accurately reflect it.
