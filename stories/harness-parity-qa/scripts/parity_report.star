def _str(v):
    if v == None:
        return ""
    return str(v)


def _dict(v):
    if type(v) == "dict":
        return v
    return {}


def _split_csv(value):
    out = []
    for raw in _str(value).split(","):
        item = raw.strip()
        if item:
            out.append(item)
    return out


def _tail(value, n):
    text = _str(value).strip()
    if len(text) <= n:
        return text
    return text[len(text) - n:]


def _check(id, kind, command, contract, result):
    r = _dict(result)
    exit_code = r.get("exit_code", 0)
    ok = r.get("ok", False)
    return {
        "id": id,
        "kind": kind,
        "command": command,
        "contract": contract,
        "run": True,
        "status": "passed" if ok else "failed",
        "exit_code": exit_code,
        "output_tail": _tail(r.get("stdout", ""), 4000),
    }


def _visual_gate(policy):
    return {
        "policy": policy,
        "automated": False,
        "reason": "Vision review is intentionally gated; deterministic tests and screenshots are CI-safe, but LLM visual judging is operator-approved only.",
        "recommended_evidence": [
            "render.tui for the parity story running state",
            "render.web or visual.open for the same live replay session",
            "VS Code Playwright screenshot when extension artifacts are built",
        ],
        "review_standard": "Use kitsoki-ui-qa style evidence review: every pass cites a visible frame; unsupported surface evidence fails.",
    }


def _summary_markdown(surfaces):
    return "Deterministic harness parity gate is defined for " + ", ".join(surfaces) + ". The critical regression check compares Claude and Codex stream normalization so thinking and tool use cannot disappear on one backend while remaining visible on the other."


def _render_markdown(summary):
    lines = [
        "# Harness Parity QA",
        "",
        summary["summary_markdown"],
        "",
        "## Deterministic checks",
        "",
    ]
    for check in summary["checks"]:
        lines.append("- `" + check["id"] + "`")
        lines.append("  - Contract: " + check["contract"])
        lines.append("  - Command: `" + check["command"] + "`")
        lines.append("  - Status: `" + check["status"] + "`")
    lines.extend([
        "",
        "## Visual QA gate",
        "",
        "Policy: `" + summary["visual_gate"]["policy"] + "`",
        "",
        summary["visual_gate"]["reason"],
        "",
        "Recommended evidence:",
    ])
    for item in summary["visual_gate"]["recommended_evidence"]:
        lines.append("- " + item)
    lines.extend(["", summary["visual_gate"]["review_standard"], ""])
    return "\n".join(lines)


def main(ctx):
    surfaces = _split_csv(ctx.inputs.get("surfaces", ""))
    output_root = _str(ctx.inputs.get("output_root", "")).rstrip("/")
    if output_root == "":
        output_root = ".artifacts/harness-parity-qa"
    markdown_path = _str(ctx.inputs.get("markdown_path", ""))
    if markdown_path == "":
        markdown_path = ".context/harness-parity-qa.md"
    visual_policy = _str(ctx.inputs.get("visual_policy", "deterministic"))

    checks = [
        _check(
            "provider-stream-normalization",
            "go-test",
            "go test ./internal/host -run TestAgentStream_HarnessParityThinkingAndToolUse",
            "Claude and Codex stream fixtures normalize to the same ordered thinking/tool activity feed.",
            ctx.inputs.get("provider_result", {}),
        ),
        _check(
            "tui-activity-render",
            "go-test",
            "go test ./internal/tui -run TestMetaStream_FullThoughtReachesScrollback",
            "TUI renders full thinking text and tool breadcrumbs distinctly.",
            ctx.inputs.get("tui_result", {}),
        ),
        _check(
            "web-activity-render",
            "unit-test",
            "pnpm -C tools/runstatus exec vitest run tests/unit/run-store.test.ts",
            "Web chat stores one ordered ActivityFeed with thinking and tool calls interleaved.",
            ctx.inputs.get("web_result", {}),
        ),
        _check(
            "vscode-embedded-surface",
            "unit-test",
            "pnpm -C tools/vscode-kitsoki exec node --test --import tsx tests/spa-visual.unit.test.ts",
            "VS Code embeds the same runstatus SPA surface instead of a forked activity renderer.",
            ctx.inputs.get("vscode_result", {}),
        ),
    ]

    passed = True
    for check in checks:
        if check["status"] != "passed" and check["status"] != "documented":
            passed = False

    summary = {
        "passed": passed,
        "status": "complete",
        "surfaces": surfaces,
        "checks": checks,
        "visual_gate": _visual_gate(visual_policy),
        "output_root": output_root,
        "markdown_path": markdown_path,
        "summary_path": output_root + "/summary.json",
        "summary_markdown": _summary_markdown(surfaces),
    }

    ctx.fs.write(summary["summary_path"], json.encode(summary) + "\n")
    ctx.fs.write(markdown_path, _render_markdown(summary))
    return {"parity_result": summary}
