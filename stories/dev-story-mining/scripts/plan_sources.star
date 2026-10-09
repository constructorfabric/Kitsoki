# plan_sources.star - deterministic source and ladder plan for dev-story-mining.
#
# This script does not inspect transcript contents. It turns operator-provided
# source/artifact settings into the concrete mining commands, policy caveats, and
# progressive-determinism ladder the later LLM-producing rooms must honor.

INPUTS = {
    "job": "string - session-mining job id",
    "transcript_sources": "string - comma list: claude,codex",
    "project_dir": "string - Claude project dir, empty means repo slug",
    "codex_sessions_dir": "string - Codex sessions root",
    "stories_dir": "string - target story tree",
    "target_artifacts": "string - comma list of story/script/hub artifacts",
    "automation_goal": "string - operator's improvement goal",
    "determinism_doc": "string - progressive determinism reference",
    "prompt_intercept_doc": "string - prompt interception reference",
    "refine_feedback": "string - feedback from the previous preparation pass",
}

OUTPUTS = {
    "source_plan": "object - sources, commands, artifact classes, policy caveats, ladder",
    "summary_markdown": "string - operator-facing summary",
}


def _split_csv(value):
    items = []
    for raw in value.split(","):
        item = raw.strip().lower()
        if item and item not in items:
            items.append(item)
    return items


def _join(items, sep):
    out = ""
    for item in items:
        if out:
            out += sep
        out += item
    return out


def _source_enabled(sources, name):
    return name in sources or "all" in sources


def _claude_source(job, project_dir):
    root = project_dir if project_dir else "~/.claude/projects/<current-repo-slug>"
    job_dir = ".artifacts/session-mining/%s-claude" % job
    return {
        "id": "claude",
        "kind": "claude-code-jsonl",
        "transcript_root": root,
        "job_dir": job_dir,
        "prep_command": "host.session_mining.run --op prep %s --job %s-claude --sample recency" % (root, job),
        "outcomes_command": "host.session_mining.run --op outcomes --raw %s/raw --out %s/outcomes.json" % (job_dir, job_dir),
        "strengths": [
            "verbatim user turns and lexical correction signals are available",
            "cost extraction is supported where message usage events exist",
        ],
        "limitations": [],
    }


def _codex_source(job, codex_sessions_dir, project_dir):
    cwd_filter = project_dir if project_dir else "<current-repo-dir-name>"
    job_dir = ".artifacts/session-mining/%s-codex" % job
    return {
        "id": "codex",
        "kind": "codex-rollout-jsonl",
        "transcript_root": codex_sessions_dir if codex_sessions_dir else "~/.codex/sessions",
        "job_dir": job_dir,
        "prep_command": "host.session_mining.run --op codex_prep %s --cwd %s --job %s-codex" % (codex_sessions_dir, cwd_filter, job),
        "outcomes_command": "host.session_mining.run --op codex_outcomes --raw %s/raw --out %s/outcomes.json" % (job_dir, job_dir),
        "strengths": [
            "tool outcomes join by call_id, so action grounding is structurally strong",
            "feeds the same ground/tag/emit spine as Claude after adaptation",
        ],
        "limitations": [
            "raw user-turn recovery is weaker until emit.py grows a Codex raw reader",
            "cost sidecars are not wired for Codex token_count events yet",
            "Codex has no pre-model interception hook, so usage enforcement is guidance/launcher/mining based",
        ],
    }


def _artifact_classes(targets):
    classes = []
    if "stories" in targets or "rooms" in targets or "all" in targets:
        classes.append({
            "kind": "ENRICH-STORY",
            "description": "Add a named gate, room, or import to an existing story.",
            "expected_files": ["stories/<story>/rooms/*.yaml", "stories/<story>/flows/*.yaml"],
        })
        classes.append({
            "kind": "NEW-STORY",
            "description": "Create a new story when no existing room owns the workflow.",
            "expected_files": ["stories/<new>/app.yaml", "stories/<new>/rooms/*.yaml", "stories/<new>/flows/*.yaml"],
        })
    if "starlark" in targets or "scripts" in targets or "all" in targets:
        classes.append({
            "kind": "STARLARK-SCRIPT",
            "description": "Promote deterministic glue into scripts/*.star plus a typed sidecar.",
            "expected_files": ["stories/<story>/scripts/*.star", "stories/<story>/scripts/*.star.yaml", "stories/<story>/flows/*.yaml"],
        })
    if "hub-routes" in targets or "hub" in targets or "all" in targets:
        classes.append({
            "kind": "HUB-ROUTE",
            "description": "Route a natural developer request into the right Kitsoki story/hub entry.",
            "expected_files": [".kitsoki/stories/**/app.yaml", "stories/*/rooms/*.yaml"],
        })
    if "skills" in targets or "all" in targets:
        classes.append({
            "kind": "SKILL-ONLY",
            "description": "Keep as a Codex/Claude skill when it is guidance, not a state machine.",
            "expected_files": [".agents/skills/<name>/SKILL.md"],
        })
    if "hooks" in targets or "intercept" in targets or "all" in targets:
        classes.append({
            "kind": "ENFORCEMENT-LIMIT",
            "description": "Record places Kitsoki cannot force usage and install only honest hooks/guidance.",
            "expected_files": ["cmd/kitsoki/hook.go", "docs/architecture/prompt-intercept.md"],
        })
    return classes


def main(ctx):
    sources = _split_csv(ctx.inputs["transcript_sources"])
    targets = _split_csv(ctx.inputs["target_artifacts"])
    warnings = []
    planned_sources = []

    if _source_enabled(sources, "claude"):
        planned_sources.append(_claude_source(ctx.inputs["job"], ctx.inputs["project_dir"]))
    if _source_enabled(sources, "codex"):
        planned_sources.append(_codex_source(ctx.inputs["job"], ctx.inputs["codex_sessions_dir"], ctx.inputs["project_dir"]))

    for source in sources:
        if source not in ["claude", "codex", "all"]:
            warnings.append("unknown transcript source %r ignored" % source)
    if not planned_sources:
        warnings.append("no known transcript sources enabled; defaulting the plan to Claude + Codex")
        planned_sources.append(_claude_source(ctx.inputs["job"], ctx.inputs["project_dir"]))
        planned_sources.append(_codex_source(ctx.inputs["job"], ctx.inputs["codex_sessions_dir"], ctx.inputs["project_dir"]))

    classes = _artifact_classes(targets)
    if not classes:
        warnings.append("no actionable artifact classes selected; defaulting to stories, Starlark, hub routes, skills, and hook limits")
        classes = _artifact_classes(["all"])

    ladder = [
        {"rung": "L0", "steps": "human + model, freehand", "decisions": "from scratch every time"},
        {"rung": "L1", "steps": "checklist or skill", "decisions": "model guided by prose"},
        {"rung": "L2", "steps": "deterministic story/script skeleton", "decisions": "human/LLM at named recorded gates"},
        {"rung": "L3", "steps": "deterministic skeleton", "decisions": "default rule for the common case; model/human on low confidence"},
        {"rung": "L4", "steps": "deterministic skeleton", "decisions": "rules only; no model"},
    ]

    enforcement = [
        {
            "agent": "claude",
            "status": "pre-model hook available",
            "route": "kitsoki hook install --agent claude --write",
        },
        {
            "agent": "codex",
            "status": "no pre-model interception hook",
            "route": "use Kitsoki launchers/workflows, MCP dispatch, guidance, and transcript mining; do not claim hard interception",
        },
    ]

    lines = []
    lines.append("## Prepared source plan")
    lines.append("")
    lines.append("Goal: %s" % ctx.inputs["automation_goal"])
    lines.append("")
    for src in planned_sources:
        lines.append("### %s" % src["id"])
        lines.append("")
        lines.append("- Root: `%s`" % src["transcript_root"])
        lines.append("- Job dir: `%s`" % src["job_dir"])
        lines.append("- Prep: `%s`" % src["prep_command"])
        lines.append("- Outcomes: `%s`" % src["outcomes_command"])
        if src["limitations"]:
            lines.append("- Missing signals: %s" % _join(src["limitations"], "; "))
        else:
            lines.append("- Missing signals: none known for the local mining path")
        lines.append("")
    lines.append("## Kitsoki targets")
    lines.append("")
    for cls in classes:
        lines.append("- **%s**: %s" % (cls["kind"], cls["description"]))
    lines.append("")
    lines.append("## Progressive determinism")
    lines.append("")
    lines.append("- Start new automation at L2 when a deterministic story/script skeleton plus named recorded gates is possible.")
    lines.append("- Move toward L3 only when recorded decisions show a strong default rule.")
    lines.append("- Move to L4 only when the rule no longer needs a model or human gate.")
    lines.append("")
    lines.append("## Enforcement boundary")
    lines.append("")
    lines.append("- Claude Code: pre-model hook available through `kitsoki hook install --agent claude --write`.")
    lines.append("- Codex: no hard pre-model interception hook today; use Kitsoki launchers, Studio MCP workflows, guidance, and transcript-mining feedback.")
    if ctx.inputs["refine_feedback"]:
        lines.append("")
        lines.append("Refine feedback: %s" % ctx.inputs["refine_feedback"])
    if warnings:
        lines.append("")
        lines.append("Warnings:")
        for warning in warnings:
            lines.append("- %s" % warning)

    return {
        "source_plan": {
            "schema": "dev-story-mining-source-plan/v1",
            "job": ctx.inputs["job"],
            "sources": planned_sources,
            "artifact_classes": classes,
            "ladder": ladder,
            "enforcement": enforcement,
            "warnings": warnings,
            "references": {
                "determinism": ctx.inputs["determinism_doc"],
                "prompt_intercept": ctx.inputs["prompt_intercept_doc"],
            },
        },
        "summary_markdown": "\n".join(lines),
    }
