make your worktrees in the project root folder .worktrees

Project skills live in the Codex-standard `.agents/skills/<name>/SKILL.md` location. Claude Code does not auto-discover that directory, so `make setup` links every `.agents/skills/*/SKILL.md` into `.claude/skills/` (relative symlinks; `.claude/` is gitignored). After adding a new skill, re-run:

```
make setup
```

To link a single skill by hand (e.g. without a full setup run):

```
ln -s "../../.agents/skills/<name>" .claude/skills/<name>
```

When we do an implementation based on a proposal, the goal is to complete the proposal implementation and move the content to proper narrative docs and delete the proposal - don't leave unfinished work unless specifically instructed, and if so, update the proposal to summarize the completed aspect and focus on the remaining work.

use the `.context` folder for transient markdown files like proposals, summaries, etc... and use the `.artifacts` folder (with subfolders as necessary) for any generated artifact for review that shouldn't be committed.  following these guidelines will help to avoid bot pollution and cruft in the repo.

Automated testing should never use a real LLM or incur costs - mock agents via cassettes should be used in all cases.  Tests which require real LLM must be gated and only done when specifically requested and required - never automatically or without checking first.

use dependency injection patterns wherever relevant.

principle of least surprise.

`AskUserQuestion` is hard-denied in every dispatched `claude -p` agent (it auto-resolves with empty answers when headless — a silent landmine). When a live operator surface is attached, agent questions are instead forwarded into kitsoki via the operator-ask bridge (the `mcp__operator__ask` tool) and surfaced on web + TUI; when no operator is attached (cassettes/flows/headless) no replacement tool is added and the agent proceeds on its own. See `docs/architecture/operator-ask.md`.

when in doubt always save a markdown into .context for review later - much easier to check/review than staying in the conversation and requiring an extra turn.

commit when you're done with your work and commit only your work - this helps to avoid a mess in the repo.  There may be parallel agents working.  Keep a minimum number of commits and amend as you go where there's no value in separate commits.  Separate key decisions or aspects in clean commits to enable bisect and reverts to work well and not create a mess.

avoid generating a binary of kitsoki for testing - just use go run unless there's some very specific reason that won't work (I think there's issues related to file embedding... not sure)

the kitsoki architecture provides extensive flow testing and mocking capabilities - both synthetic and recorded - to enable thorough testing and demonstration without LLM usage.  make sure to de-risk all cases with flow tests and mocked interactions - and when doing live integration tested if mocks/flows change ensure the tests accurately reflect it.