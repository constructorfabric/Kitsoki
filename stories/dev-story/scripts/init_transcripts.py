#!/usr/bin/env python3
"""Transcript evidence helpers for project onboarding.

This module is deterministic and local-only. It detects whether a target repo
has associated Claude Code or Codex history so onboarding can propose a bounded
session-mining seed without starting the mining pipeline or spending LLM budget.
"""

from __future__ import annotations

import os
from pathlib import Path


def transcript_slug(path: Path) -> str:
    """Match the Claude Code project slug convention used by mining.Resolver."""
    return str(path).replace("/", "-").replace(".", "-")


def transcript_mtime(path: Path) -> int:
    try:
        return int(path.stat().st_mtime)
    except OSError:
        return 0


def count_jsonl(path: Path) -> tuple[int, int]:
    if not path.is_dir():
        return 0, 0
    count = 0
    latest = 0
    try:
        files = list(path.glob("*.jsonl"))
    except OSError:
        return 0, 0
    for item in files:
        count += 1
        latest = max(latest, transcript_mtime(item))
    return count, latest


def codex_session_refs(home: Path, repo: Path, limit: int = 200) -> tuple[int, int]:
    """Find recent Codex sessions that mention this repo path.

    Codex stores sessions by date, not in a repo-slug directory, so discovery
    uses a bounded recent-file scan. This is evidence for first-run setup, not
    the full mining pass.
    """
    root = home / ".codex" / "sessions"
    if not root.is_dir():
        return 0, 0
    target = str(repo)
    try:
        files = sorted(root.rglob("*.jsonl"), key=transcript_mtime, reverse=True)[:limit]
    except OSError:
        return 0, 0
    count = 0
    latest = 0
    for item in files:
        try:
            text = item.read_text(encoding="utf-8", errors="ignore")
        except OSError:
            continue
        if target in text:
            count += 1
            latest = max(latest, transcript_mtime(item))
    return count, latest


def home_dir() -> Path:
    home_env = os.environ.get("KITSOKI_INIT_HOME", "")
    if home_env:
        return Path(home_env).expanduser()
    try:
        return Path.home()
    except RuntimeError:
        return Path("")


def transcript_evidence(path: Path) -> dict:
    home = home_dir()
    slug_value = transcript_slug(path)
    claude_dir = home / ".claude" / "projects" / slug_value
    claude_count, claude_latest = count_jsonl(claude_dir)
    codex_count, codex_latest = codex_session_refs(home, path)
    sources = []
    if claude_count:
        sources.append({
            "backend": "claude-code",
            "dir": str(claude_dir),
            "sessions": claude_count,
            "latest_mtime": claude_latest,
            "include": "human",
        })
    if codex_count:
        sources.append({
            "backend": "codex",
            "dir": str(home / ".codex" / "sessions"),
            "sessions": codex_count,
            "latest_mtime": codex_latest,
            "include": "human",
        })
    total = sum(int(src.get("sessions", 0)) for src in sources)
    recommendation = {
        "status": "transcripts-found" if total else "no-transcripts-found",
        "sample": "recency",
        "first_pass_sample": min(12, total) if total else 0,
        "transcript_count": total,
        "sources": sources,
        "note": (
            "Seed project customization from recent associated transcripts after operator consent."
            if total
            else "No associated Claude/Codex transcripts were found during deterministic discovery."
        ),
    }
    return {
        "slug": slug_value,
        "sources": sources,
        "count": total,
        "latest_mtime": max([0] + [int(src.get("latest_mtime", 0)) for src in sources]),
        "mining": recommendation,
    }


def mining_recommendation(path: Path) -> dict:
    return transcript_evidence(path)["mining"]
