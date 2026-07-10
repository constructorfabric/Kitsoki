#!/usr/bin/env python3
"""Discover local harness profile setup without exposing secrets."""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from profile_setup_lib import discover, json_out, resolve_target  # noqa: E402


def main(argv: list[str]) -> int:
    target = argv[1] if len(argv) > 1 else ""
    workdir = argv[2] if len(argv) > 2 else ""
    repo_root = argv[3] if len(argv) > 3 else ""
    root = resolve_target(target, workdir, repo_root)
    if not root.exists() or not root.is_dir():
        print(json_out({
            "schema": "kitsoki-profile-setup-discovery/v1",
            "status": "error",
            "target_path": str(root),
            "error": f"target path does not exist or is not a directory: {root}",
        }))
        return 0
    print(json_out(discover(root)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
