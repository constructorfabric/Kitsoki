#!/usr/bin/env python3
"""run_mcp_gate.py — the MCP-surface no-LLM gate harness entry point
`usable_kitsoki_gate.py`'s `drive_command()` already dispatches to
(`MCP_RUNNER`). See `_gate_cli.py` for the shared single-cell contract and
`flow_gate_runner.py`'s module docstring for why the MCP surface today
drives the same flow-replay mechanism as TUI (documented simplification,
not a bug).
"""
from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import _gate_cli  # noqa: E402

if __name__ == "__main__":
    raise SystemExit(_gate_cli.main("mcp"))
