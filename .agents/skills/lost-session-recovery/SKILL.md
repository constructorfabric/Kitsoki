---
name: lost-session-recovery
description: Find a coding-agent session (Codex CLI or Claude Code) that looks "lost" or "killed" — hung indefinitely, orphaned from its terminal, or working in a capsule workspace with no recent progress — diagnose whether it's actually stuck vs. genuinely OS-killed, and write a resume brief so the work isn't re-derived from scratch. Use when the user says a session was "killed", "lost", "died", "hung", "stuck", or asks "what was I working on that got interrupted" without naming the specific session.
---

# Lost Session Recovery

A coding-agent process (Codex CLI, Claude Code) can go quiet in three very
different ways, and the fix depends on which one happened:

1. **The agent process itself is gone** (OS killed it, terminal closed, user
   `kill`ed it). The transcript file just stops mid-turn.
2. **The agent process is alive but wedged** — it's polling a child process
   (a background shell/PTY session) that the OS silently killed out from
   under it, so it retries forever against a dead handle with zero output.
   `ps` shows the agent as alive and "running", which is misleading.
3. **The agent finished normally** (`task_complete` / a final assistant
   message) but the user just doesn't remember which session it was, or
   thinks it died because it went quiet after finishing.

Don't guess which one applies — the transcript and process table together
tell you deterministically. Never write a resume brief for a session you
haven't confirmed is actually dead/stuck; a wrong brief sends the next agent
down the wrong branch/workspace and wastes as much time as no brief at all.

## Where the evidence lives

| Tool | Session transcripts | Format |
|---|---|---|
| Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | one JSON object per line: `session_meta`, `response_item` (`reasoning`/`function_call`/`function_call_output`/`message`), `event_msg` (`token_count`, `task_complete`) |
| Claude Code | `~/.claude/projects/<project-slug>/*.jsonl` (see `tools/session-mining/` for the repo's own mining scripts) | similar per-line JSON, different schema |

This repo also has `.agents/skills/session-idea-mining` and
`.agents/skills/session-recap` for *reading* transcripts generally — this
skill is specifically about triaging a **stuck/dead** session and producing a
**resume brief**, not summarizing history.

## Step 1 — find candidate sessions

Narrow by recency and (if the user gave a topic/keyword) content:

```sh
# recently-touched session files, newest first
find ~/.codex/sessions -type f -newermt "2h ago" | xargs ls -la

# topical filter, if the user named a feature/area
grep -l "<keyword>" ~/.codex/sessions/YYYY/MM/DD/*.jsonl
```

Don't stop at the first content match — grep hits are often just bundled
docs/memory-bank text (AGENTS.md, prior rollout summaries) quoted into the
prompt, not evidence the session is actually about that topic. Confirm via
the session's own user-turn messages (Step 2).

## Step 2 — read out the real task, not the boilerplate

The first "user" message in a Codex rollout is almost always the injected
`AGENTS.md` instructions block — skip it. Pull every user-role message that
isn't that boilerplate to get the actual ask:

```python
import json
for l in open(path):
    d = json.loads(l)
    p = d.get('payload', {})
    if p.get('type') == 'message' and p.get('role') == 'user':
        for c in p.get('content', []):
            t = c.get('text', '')
            if t and not t.startswith('# AGENTS.md') and '<environment_context>' not in t:
                print(t)
```

## Step 3 — determine which of the three states applies

**Check if the process is still alive:**

```sh
ps aux | grep -i codex        # or claude
lsof 2>/dev/null | grep "<rollout-file-path>"   # is a real agent pid still holding the file open (not mds/spotlight)?
```

`lsof` will show a `com.apple.*` (Spotlight/mds) reader on almost every
recent file — ignore those. Only a `codex`/`node` process holding the file
**for writing** (`w` in the fd column) means the session is still live.

**If the process is gone** (state 1): confirm the last few lines end
mid-`function_call`/`function_call_output` with no `task_complete` /
`turn_aborted` / final assistant message after it. That's a clean kill
signature. Go to Step 4.

**If the process is alive** (`ps` shows it): check whether it's actually
making progress or wedged. Look at the tail of the file for a repeating
pattern of the same tool call (e.g. `write_stdin` against the same
`session_id`) returning empty output over and over across many minutes:

```sh
tail -c 2000 <rollout-file>          # eyeball the repeating call
grep -c '"session_id":<N>' <rollout-file>   # how many times has it polled the same dead handle?
```

Find where that background session was started (search backward for the
`exec_command`/`shell` call whose output first mentioned that `session_id`)
to see **what it launched** and **what workdir**. Then check whether that
child process is still in the process table:

```sh
ps aux | grep -i "<distinctive part of the command>"
```

If the command is gone from `ps` but the agent is still politely asking it
for output, that's state 2 (OS-killed child, wedged parent) — confirmed. The
agent process itself isn't the problem; treat it as dead for planning
purposes and go to Step 4. Flag to the user that the visibly-running agent
process should be killed (don't kill it yourself without asking — it's still
attached to the user's terminal).

**If it ended with `task_complete`** (state 3): it finished; there's nothing
to resume. Say so plainly instead of forcing a brief.

## Step 4 — locate any managed workspace and its real progress state

Kitsoki work happens in `.capsules/workspaces/<id>/` (see
`docs/dev-workspaces.md`). Cross-reference the session against a workspace
two ways:

1. **Grep the rollout for `--id <name>`** passed to
   `scripts/dev-workspace.sh create` — the session usually names its own
   workspace early on.
2. **Confirm the workspace directory exists** and check *real* activity
   time, not blanket directory mtimes (a `find`/backup pass can touch every
   workspace's mtime identically and lie to you):

```sh
stat -f "%m" .capsules/workspaces/<id>/.git/logs/HEAD   # last real commit/checkout activity
git -C .capsules/workspaces/<id> status --short          # uncommitted work — this is what's at risk
git -C .capsules/workspaces/<id> log --oneline -5
git -C .capsules/workspaces/<id> diff --stat
```

A workspace whose `.git/logs/HEAD` hasn't moved in ~30+ minutes while its
owning session is confirmed stuck/dead is the one to write up. If several
workspaces exist, cross-reference each live/dead session's `--id` calls
against them rather than assuming — a stale workspace and a stuck session are
only the same incident if the transcript actually names that workspace.

## Step 5 — pull the design context, not just the diff

Diff stat alone doesn't tell the next agent *why* the code looks the way it
does. Pull the assistant's own narration — Codex "reasoning"/summary text
often documents the design decision right before the point of death:

```python
for l in open(path):
    d = json.loads(l)
    p = d.get('payload', {})
    if p.get('type') == 'message' and p.get('role') == 'assistant':
        for c in p.get('content', []):
            if c.get('text', '').strip():
                print(c['text'])
```

The last 5-10 of these usually explain the approach taken, any dead ends hit
(patch-apply failures, permission surprises, wrong-file edits) that don't
need repeating, and what was left unverified.

## Step 6 — write the resume brief

Save to `.context/YYYY-MM-DD-<short-slug>-resume-brief.md` (per this repo's
"when in doubt, save a markdown into `.context`" convention). Structure:

- **What happened** — which session/pid, which workspace, what got killed,
  when, and your evidence for state 1/2/3 (don't assert "killed by OS"
  without the process-table check backing it up).
- **Original task**, verbatim, every real user turn.
- **Design direction already converged on** — pull straight from Step 5, not
  paraphrased into something vaguer.
- **Current diff state** — `git diff --stat` output, branch name, base,
  fork-point commit. Note explicitly if the work is uncommitted (it usually
  is) so the resuming agent commits before doing anything else.
- **Root-cause hint for the hang/kill**, if you found one (e.g. a spawned
  binary losing its ad-hoc codesign — see `cp-binary-invalidates-codesign` in
  repo memory/notes — or an OOM/jetsam pattern). Don't guess wildly; say what
  you actually observed (process gone, zero output, X minutes) and name it as
  a hypothesis if you can't confirm it from logs.
- **Next steps** — concrete: kill the stuck process (name the pid) if still
  live, re-run whatever was hung to see if it's reproducible or was an
  environment fluke, what to verify before trusting anything the dead session
  believed had passed, and the correct merge path
  (`scripts/dev-workspace.sh merge <id> --gate "..." --teardown`).

Don't kill a live process yourself without asking first if the terminal is
still visibly attached to the user (they may be mid-interaction with it in
another tab) — surface the pid and ask, unless they've already told you it's
abandoned.
