make your worktrees in the project root folder .worktrees

Project skills live at `docs/skills/<name>/SKILL.md` and are exposed to Claude Code by symlinking into `~/.claude/skills/<name>` (Claude Code does not auto-discover skills under `docs/`). When adding a new skill under `docs/skills/`, also create the symlink so it appears in the available-skills list:

```
ln -s "$(pwd)/docs/skills/<name>" ~/.claude/skills/<name>
```

When we do an implementation based on a proposal, the goal is to complete the proposal implementation and move the content to proper narrative docs and delete the proposal - don't leave unfinished work unless specifically instructed, and if so, update the proposal to summarize the completed aspect and focus on the remaining work.

use the `.context` folder for transient markdown files like proposals, summaries, etc... and use the `.artifacts` folder (with subfolders as necessary) for any generated artifact for review that shouldn't be committed.  following these guidelines will help to avoid bot pollution and cruft in the repo.

Automated testing should never use a real LLM or incur costs - mock oracles via cassettes should be used in all cases.  Tests which require real LLM must be gated and only done when specifically requested and required - never automatically or without checking first.
