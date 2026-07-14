## Kitsoki Session Launcher

**Status:** Draft v1. Nothing implemented yet.

**Title** — A story-driven CLI session launcher that spins up tailored Claude Code instances per task

---

### Problem

Claude Code sessions accumulate tools, skills, MCP servers, and agent definitions over time. For any given task — a focused code review, a wave fan-out job, a summarization pass — most of that context is noise and some of it actively interferes. There's no lightweight way to say "start a Claude Code session with exactly these tools and this context, for this task" without either manually configuring a new profile or accepting a cluttered general-purpose session. Agents can be scoped appropriately, but they must be invoked manually and lack a first-class launcher experience.

---

### Proposed Solution

Build a kitsoki story that acts as a **session launcher**: a TUI front-end that lets users describe the Claude Code session they want in natural language, then launches it in a tmux pane with the right tool set, pre-populated context, and prompt scaffolding.

Key capabilities:

- **Session definition via free-form description** — user describes what they want ("a code review agent with only file-read tools and the last 3 commits as context") and the story resolves that into a concrete agent config + JSONL stub.
- **Reusable conversation stubs** — for summarization and wave fan-out jobs, the launcher generates an `agent + stub conversation JSONL` that encodes context population. These stubs can be replayed for batch runs or pre-loaded into interactive sessions (context + prompt instructions pre-filled; user adds their message, LLM responds).
- **tmux-based session lifecycle** — launches the configured Claude Code instance in a tmux window/pane. When the user exits, control returns to the launcher so the next session can be queued.
- **Session extension** — user can reference a prior session ("extend the code review from this morning") and the launcher hydrates from its stub.

---

### Success Criteria

1. User describes a session in plain text; launcher generates and launches a tmux-hosted Claude Code instance with a narrowed tool set and pre-populated context within a few seconds.
2. A summarization or fan-out job produces a JSONL stub that can be replayed identically — same context, same prompt scaffold — in both batch and interactive modes.
3. Exiting a session returns the user to the launcher without leaving orphaned tmux sessions.
4. An existing session stub can be extended (context appended) and re-launched without manual editing.

---

### Scope / Non-goals

**In scope:**
- Claude Code as the first (and initially only) target CLI
- tmux as the session host
- JSONL stub generation for summarization and wave fan-out patterns
- Interactive and batch replay of stubs

**Out of scope:**
- Supporting non-Claude CLIs (future work; architecture should leave the door open)
- A persistent session registry or history UI (stub files on disk are sufficient for now)
- Auto-detecting which tools a task needs (user describes it; the story resolves it — no inference magic yet)
- Session sharing or multi-user workflows⁣⁢⁢⁣
