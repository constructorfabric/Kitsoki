Run the session-mining **intent** pipeline over recent Claude Code transcripts and report the resulting brief.

Job: `{{ args.job }}`
Project dir (transcripts): `{{ args.project_dir }}` {% if not args.project_dir %}(empty → resolve the current repo's `~/.claude/projects/<slug>` dir){% endif %}
{% if args.refine_feedback %}Operator feedback from the last pass: {{ args.refine_feedback }}{% endif %}

Follow `tools/session-mining/README.md` § "Intent mining":
1. `prep.py "<project_dir>" --job {{ args.job }} --sample recency` — distill into `.artifacts/session-mining/{{ args.job }}/`.
2. Run the strict-agent pass (`intents.workflow.js`) over the batches.
3. `ground.py` → `tag_score.py` → `emit.py`, then `verify_link.py` and `validate_reports.py`.

{% block spec_project_context %}{% endblock %}

Then **read the emitted `BRIEF.md` and reports** and return the artifact. Every number must come from the reports — never estimate. Report `brief_path`, `intent_count`, and the determinism split (`deterministic` / `agent_gated` / `irreducible`). `summary_markdown` should give the recurring intent-shape clusters and the in-scope action-tag distribution.
