Run the session-mining **intent/idea** pipeline over the prepared transcript sources and report the resulting automation brief.

Job: `{{ args.job }}`
Transcript sources: `{{ args.transcript_sources }}`
Project dir (Claude transcripts): `{{ args.project_dir }}` {% if not args.project_dir %}(empty -> resolve the current repo's `~/.claude/projects/<slug>` dir){% endif %}
Codex sessions dir: `{{ args.codex_sessions_dir }}`
Automation goal: {{ args.automation_goal }}
Prepared source plan (JSON): {{ args.source_plan }}
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Follow `tools/session-mining/README.md` and the prepared source plan exactly:

1. For each enabled **Claude** source, use `prep.py` / `outcomes.py`.
2. For each enabled **Codex** source, use `codex_prep.py` / `codex_outcomes.py`.
3. Run the strict-agent pass (`intents.workflow.js`) over each prepared batch set only when the operator has asked for this mining run.
4. Run `ground.py` -> `tag_score.py` -> `emit.py`, then `verify_link.py` and `validate_reports.py`.
5. Merge or compare the Claude/Codex reports without losing the source labels.

{% block spec_project_context %}{% endblock %}

Then **read the emitted `BRIEF.md` and reports** and return the artifact. Every number must come from the reports or be marked unavailable — never estimate.

Report:
- `brief_path`
- `source_counts` keyed by `claude` / `codex`
- `intent_count`
- display strings for `conversation_count_display`, `message_count_display`, `token_count_display`, and `intent_count_display`
- compact per-harness display strings: `claude_breakdown_display`, `codex_breakdown_display`, and `total_reviewed_display`
- determinism split (`deterministic` / `agent_gated` / `irreducible`)
- display strings for `deterministic_display`, `agent_gated_display`, and `irreducible_display`
- compact split display strings: `green_split_display`, `yellow_split_display`, and `red_split_display`, each explaining what the color means
- `source_breakdown_markdown`, a compact table with one row per harness plus a total row: conversations, messages, tokens reviewed, and intents mined
- `tool_gaps` for missing signals, especially Codex lexical satisfaction and costs when unavailable

The display strings are required because transports may render raw numeric JSON as floats. They must be derived from the report values, must not include `.000000`, and must mark unavailable token/cost data honestly instead of estimating.

`summary_markdown` should give the recurring intent-shape clusters, the in-scope action-tag distribution, and the concrete missed opportunities to use Kitsoki stories, rooms, Starlark scripts, hub routes, or skills.
