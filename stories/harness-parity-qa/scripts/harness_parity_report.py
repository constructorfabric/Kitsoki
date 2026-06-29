#!/usr/bin/env python3
"""Emit deterministic harness parity QA artifacts.

The heavy regression guard lives in Go tests, where the real Claude/Codex
stream parsers and TUI render paths are available. This script is the story
entrypoint: it defines the operator-facing contract, writes review artifacts,
and returns a host.run stdout_json envelope.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--surfaces", default="tui,web,vscode")
    parser.add_argument("--output-root", default=".artifacts/harness-parity-qa")
    parser.add_argument("--markdown", default=".context/harness-parity-qa.md")
    parser.add_argument("--visual-policy", default="deterministic")
    args = parser.parse_args()

    surfaces = [s.strip() for s in args.surfaces.split(",") if s.strip()]
    output_root = Path(args.output_root)
    markdown_path = Path(args.markdown)
    output_root.mkdir(parents=True, exist_ok=True)
    markdown_path.parent.mkdir(parents=True, exist_ok=True)

    checks = [
        {
            "id": "provider-stream-normalization",
            "kind": "go-test",
            "command": "go test ./internal/host -run TestAgentStream_HarnessParityThinkingAndToolUse",
            "contract": "Claude and Codex stream fixtures normalize to the same ordered thinking/tool activity feed.",
            "run": True,
        },
        {
            "id": "tui-activity-render",
            "kind": "go-test",
            "command": "go test ./internal/tui -run TestMetaStream_FullThoughtReachesScrollback",
            "contract": "TUI renders full thinking text and tool breadcrumbs distinctly.",
            "run": True,
        },
        {
            "id": "web-activity-render",
            "kind": "unit-test",
            "command": "pnpm -C tools/runstatus exec vitest run tests/unit/run-store.test.ts",
            "contract": "Web chat stores one ordered ActivityFeed with thinking and tool calls interleaved.",
            "run": True,
        },
        {
            "id": "vscode-embedded-surface",
            "kind": "unit-test",
            "command": "pnpm -C tools/vscode-kitsoki exec node --test --import tsx tests/spa-visual.unit.test.ts",
            "contract": "VS Code embeds the same runstatus SPA surface instead of a forked activity renderer.",
            "run": True,
        },
    ]

    for check in checks:
        if check.get("run"):
            check.update(run_check(check["command"]))

    visual_gate = {
        "policy": args.visual_policy,
        "automated": False,
        "reason": "Vision review is intentionally gated; deterministic tests and screenshots are CI-safe, but LLM visual judging is operator-approved only.",
        "recommended_evidence": [
            "render.tui for the parity story running state",
            "render.web or visual.open for the same live replay session",
            "VS Code Playwright screenshot when extension artifacts are built",
        ],
        "review_standard": "Use kitsoki-ui-qa style evidence review: every pass cites a visible frame; unsupported surface evidence fails.",
    }

    passed = all(c.get("status") in ("passed", "documented") for c in checks)
    summary = {
        "passed": passed,
        "status": "complete",
        "surfaces": surfaces,
        "checks": checks,
        "visual_gate": visual_gate,
        "output_root": str(output_root),
        "markdown_path": str(markdown_path),
        "summary_path": str(output_root / "summary.json"),
        "summary_markdown": (
            "Deterministic harness parity gate is defined for "
            + ", ".join(surfaces)
            + ". The critical regression check compares Claude and Codex stream normalization so thinking and tool use cannot disappear on one backend while remaining visible on the other."
        ),
    }

    (output_root / "summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    markdown_path.write_text(render_markdown(summary), encoding="utf-8")
    print(json.dumps(summary, sort_keys=True))
    return 0


def run_check(command: str) -> dict:
    env = os.environ.copy()
    env.setdefault("GOCACHE", str(Path(".cache/go-build").resolve()))
    proc = subprocess.run(command.split(), text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=120, env=env)
    output = proc.stdout.strip()
    return {
        "status": "passed" if proc.returncode == 0 else "failed",
        "exit_code": proc.returncode,
        "output_tail": output[-4000:],
    }


def render_markdown(summary: dict) -> str:
    lines = [
        "# Harness Parity QA",
        "",
        summary["summary_markdown"],
        "",
        "## Deterministic checks",
        "",
    ]
    for check in summary["checks"]:
        lines.extend(
            [
                f"- `{check['id']}`",
                f"  - Contract: {check['contract']}",
                f"  - Command: `{check['command']}`",
                f"  - Status: `{check.get('status', 'unknown')}`",
            ]
        )
        if check.get("reason"):
            lines.append(f"  - Reason: {check['reason']}")
    lines.extend(
        [
            "",
            "## Visual QA gate",
            "",
            f"Policy: `{summary['visual_gate']['policy']}`",
            "",
            summary["visual_gate"]["reason"],
            "",
            "Recommended evidence:",
        ]
    )
    for item in summary["visual_gate"]["recommended_evidence"]:
        lines.append(f"- {item}")
    lines.extend(["", summary["visual_gate"]["review_standard"], ""])
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
