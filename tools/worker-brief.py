#!/usr/bin/env python3
"""Deterministic worker context-builder.

Given ONE change id, emit a SLIM, COMPLETE brief a worker agent can act on with
minimal reading and no codebase exploration. Pure/deterministic: reads the
decomposition + the change's in-scope files + any referenced proposal docs, and
formats them. No LLM, no tool-use, no thinking — the builder just projects.

The point (token efficiency): a fresh worker otherwise spends ~10-20k tokens
reading the big decomposition.yaml and grepping to locate files. This hands it
exactly the spec, the gate, the in-scope small files inline, and pointers into
the large ones — so it goes straight to implementing.

Usage:
  tools/worker-brief.py --change 2.1 [--goal-dir docs/goals/generalized-usage]
                        [--worktree <abs path>] [--branch stabilize/gu-c21]
                        [--repo .] [--max-inline-lines 180]
"""
import argparse
import glob
import os
import re
import sys

import yaml


def _load_change(goal_dir, change_id):
    path = os.path.join(goal_dir, "decomposition.yaml")
    doc = yaml.safe_load(open(path))
    changes = doc["changes"] if isinstance(doc, dict) and "changes" in doc else doc
    for c in changes:
        if str(c.get("id")) == change_id:
            return c
    sys.exit(f"worker-brief: change '{change_id}' not found in {path}")


def _resolve_scope(entry, repo):
    """Return (concrete_files, is_glob). A '**'/'*' entry is a glob; else a path."""
    if "*" in entry:
        matches = sorted(glob.glob(os.path.join(repo, entry), recursive=True))
        files = [m for m in matches if os.path.isfile(m)]
        return files, True
    p = os.path.join(repo, entry)
    return ([p] if os.path.isfile(p) else []), False


def _file_block(path, repo, max_inline):
    rel = os.path.relpath(path, repo)
    try:
        text = open(path, encoding="utf-8", errors="replace").read()
    except OSError:
        return f"- `{rel}` — (unreadable)"
    n = text.count("\n") + 1
    if n <= max_inline:
        lang = "go" if rel.endswith(".go") else ("yaml" if rel.endswith((".yaml", ".yml")) else ("python" if rel.endswith(".py") else ""))
        return f"### `{rel}` ({n} lines) — full content:\n```{lang}\n{text}\n```"
    return f"### `{rel}` ({n} lines) — LARGE; read only the slice you need (see the pointers in the Task above)."


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--change", required=True)
    ap.add_argument("--goal-dir", default="docs/goals/generalized-usage")
    ap.add_argument("--worktree", default="")
    ap.add_argument("--branch", default="")
    ap.add_argument("--repo", default=".")
    ap.add_argument("--max-inline-lines", type=int, default=180)
    args = ap.parse_args()

    ch = _load_change(args.goal_dir, args.change)
    gate = ch.get("gate") or {}
    scope = ch.get("scope") or []
    brief = (ch.get("agent_brief") or "").strip()
    accept = ch.get("acceptance") or []
    cmd = gate.get("cmd") or gate.get("replay_cmd") or "(no runnable gate)"

    o = []
    o.append(f"# WORKER BRIEF — change {ch['id']}: {ch.get('title','')}")
    if args.worktree:
        br = args.branch or "stabilize/gu-<id>"
        o.append(
            f"\n**Work ONLY in worktree** `{args.worktree}` (branch `{br}`). All edits + git commits there. "
            f"Do NOT rebase/merge/integrate to the base branch — the orchestrator does that."
        )
    o.append("\n## Task")
    o.append(brief or "(no agent_brief)")
    if accept:
        o.append("\n**Acceptance criteria:**")
        o += [f"- {a}" for a in accept]

    o.append("\n## Gate — must be GREEN *for the right reason*")
    o.append(f"- class: `{gate.get('class')}`")
    o.append(f"- **command:** `{cmd}`")
    if gate.get("red_now"):
        o.append(f"- expected RED-now: {gate['red_now']}")
    o.append(
        "- Verify RED **before** your change and GREEN **after** (RED-first). If the gate is already GREEN "
        "and the behavior already exists, the change is ALREADY-SATISFIED — report that with file:line evidence, "
        "do NOT fabricate work. If the gate is weak (passes without the feature), still deliver the real brief "
        "and self-verify the actual behavior."
    )

    o.append("\n## In-scope files (edit ONLY these)")
    if not scope:
        o.append("(none declared)")
    for entry in scope:
        files, is_glob = _resolve_scope(entry, args.repo)
        if is_glob:
            listing = ", ".join(f"`{os.path.relpath(f, args.repo)}`" for f in files[:20]) or "(none yet — you may create files here)"
            o.append(f"- glob `{entry}` → {listing}")
        elif files:
            o.append(_file_block(files[0], args.repo, args.max_inline_lines))
        else:
            o.append(f"- `{entry}` — does NOT exist yet; you create it.")

    # Inline any referenced proposal/design docs that are small; else pointer.
    refs = sorted(set(re.findall(r"docs/proposals/[\w\-]+\.md", brief)))
    if refs:
        o.append("\n## Referenced design docs")
        for r in refs:
            p = os.path.join(args.repo, r)
            if os.path.isfile(p):
                o.append(_file_block(p, args.repo, args.max_inline_lines))
            else:
                o.append(f"- `{r}` — read it in your worktree.")

    o.append("\n## Protocol")
    o.append(
        "- Implement to satisfy the gate + acceptance. NO real LLM in tests (mock/cassette/flow only). "
        "Stay strictly within the in-scope files.\n"
        "- Commit on your branch (one or a few clean commits). Do NOT integrate.\n"
        "- Report ONLY, concise: `change_id`, `branch`, gate `PASS`/`FAIL`/`ALREADY-SATISFIED` (+ the exact "
        "command you ran), a 1-2 line summary, and any blocker. NO diffs, NO long logs."
    )
    print("\n".join(o))


if __name__ == "__main__":
    main()
