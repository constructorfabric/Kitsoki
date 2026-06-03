#!/usr/bin/env python3
"""Stop hook: ask the current Claude instance to update .context/<session>.md.

Thresholds (edit to taste):
  MIN_TOKENS  — minimum total context tokens before summarising
  MIN_TURNS   — minimum user turns before summarising
Either condition triggers; set to 0 to disable.
"""
import json
import subprocess
import sys
from pathlib import Path

MIN_TOKENS = 75_000
MIN_TURNS  = 20

data = json.load(sys.stdin)
session_id = data.get("session_id", "")
if not session_id:
    sys.exit(0)

result = subprocess.run(
    ["git", "rev-parse", "--show-toplevel"],
    capture_output=True, text=True
)
if result.returncode != 0:
    sys.exit(0)
repo_root = Path(result.stdout.strip())
context_dir = repo_root / ".context"
context_dir.mkdir(exist_ok=True)

projects_dir = Path.home() / ".claude" / "projects"
matches = list(projects_dir.glob(f"**/{session_id}.jsonl"))
if not matches:
    sys.exit(0)
transcript_lines = matches[0].read_text().splitlines()

# Parse all records once
records = []
for raw in transcript_lines:
    try:
        records.append(json.loads(raw))
    except json.JSONDecodeError:
        continue

# Count user turns
user_turns = sum(
    1 for r in records
    if r.get("message", {}).get("role") == "user"
)

# Total context tokens from the last assistant record that has usage
total_tokens = 0
for r in reversed(records):
    usage = r.get("message", {}).get("usage")
    if usage:
        total_tokens = (
            usage.get("input_tokens", 0)
            + usage.get("cache_read_input_tokens", 0)
            + usage.get("cache_creation_input_tokens", 0)
        )
        break

if total_tokens < MIN_TOKENS and user_turns < MIN_TURNS:
    sys.exit(0)

summary_file = context_dir / f"session-{session_id[:8]}.md"
existing = summary_file.read_text() if summary_file.exists() else ""

cursor_file = context_dir / f"session-{session_id[:8]}.cursor"
cursor = int(cursor_file.read_text().strip()) if cursor_file.exists() else 0

new_turns = []
new_user_turns = 0
# Recursion guard: only count user messages authored by the *operator*.
# Two ways a transcript "user" record can be system-injected:
#   1. isMeta == True  — the harness wraps any hook's stderr (or any
#      other system-side text) as a meta user turn. This catches the
#      Stop-hook-feedback rewrite loop generically, regardless of
#      which hook wrote the stderr.
#   2. content starts with a known wrapper tag — <task-notification>
#      and <system-reminder> arrive as non-meta user turns but are
#      still system-authored.
# Anything else is real operator input.
SYSTEM_USER_PREFIXES = ("<task-notification>", "<system-reminder>")
for rec in records[cursor:]:
    role = rec.get("message", {}).get("role")
    if role not in ("user", "assistant"):
        continue
    content = rec.get("message", {}).get("content", "")
    if isinstance(content, list):
        content = " ".join(
            block.get("text", "") for block in content if block.get("type") == "text"
        )
    text = content.strip()
    if not text:
        continue
    new_turns.append(f"{role}: {text}")
    if role != "user":
        continue
    if rec.get("isMeta") is True:
        continue
    if text.startswith(SYSTEM_USER_PREFIXES):
        continue
    new_user_turns += 1

# Only fire if a real user has said something new — prevents recursion on
# hook-generated turns (task-notifications, system-reminders, assistant responses).
if not new_turns or new_user_turns == 0:
    sys.exit(0)

cursor_file.write_text(str(len(records)))

# Write instructions to stderr — asyncRewake delivers stderr to the model on exit 2
print(
    f"Please update the session summary file at `{summary_file}`.\n\n"
    + (f"Current summary:\n{existing}\n\n" if existing else "")
    + "New turns to incorporate:\n" + "\n".join(new_turns)[-8000:] + "\n\n"
    + "Rewrite the file with an updated 3-5 bullet summary of what's been accomplished, "
    "decided, or left in-progress. Keep it concise.",
    file=sys.stderr,
)
sys.exit(2)
