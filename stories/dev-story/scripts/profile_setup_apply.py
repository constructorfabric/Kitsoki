#!/usr/bin/env python3
"""Apply a reviewed local harness profile patch."""

from __future__ import annotations

import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from profile_setup_lib import apply_candidate, json_out, resolve_target  # noqa: E402


def main(argv: list[str]) -> int:
    target = argv[1] if len(argv) > 1 else ""
    candidate_raw = argv[2] if len(argv) > 2 else "{}"
    workdir = argv[3] if len(argv) > 3 else ""
    repo_root = argv[4] if len(argv) > 4 else ""
    root = resolve_target(target, workdir, repo_root)
    try:
        candidate = json.loads(candidate_raw or "{}")
        if not isinstance(candidate, dict):
            raise ValueError("candidate must be a JSON object")
        report = apply_candidate(root, candidate)
    except Exception as exc:  # intentionally converts domain failures to JSON
        print(json_out({
            "schema": "kitsoki-profile-setup-apply/v1",
            "status": "failed",
            "target_path": str(root),
            "error": str(exc),
        }))
        return 1
    print(json_out(report))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
