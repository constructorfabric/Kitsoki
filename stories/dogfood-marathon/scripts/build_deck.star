# build_deck.star — write one aggregate slidey deck plus a per-case deck for
# every recorded bug. The decks are derived only from journaled run data.

def _str(v):
    if v == None:
        return ""
    return str(v)

def _dict(v):
    if type(v) == "dict":
        return v
    return {}

def _items(v):
    if type(v) == "list":
        return v
    return []

def _get(obj, key, default):
    v = obj.get(key)
    if v == None:
        return default
    return v

def _truthy(v):
    if v == True:
        return True
    if type(v) == "string":
        s = v.strip().lower()
        return s == "true" or s == "1" or s == "yes"
    return False

def _display_number(v):
    s = _str(v).strip()
    if s == "":
        return "0"
    if s.endswith(".000000"):
        return s[:-7]
    if s.endswith(".0"):
        return s[:-2]
    return s

def _one_line(text):
    s = _str(text).strip()
    if s == "":
        return ""
    out = ""
    for part in s.split("\n"):
        p = part.strip()
        if p != "":
            if out != "":
                out += " "
            out += p
    return out

def _short(text, limit):
    s = _one_line(text)
    if limit <= 0 or len(s) <= limit:
        return s
    return s[:limit - 3] + "..."

def _nonempty(value, fallback):
    s = _one_line(value)
    if s == "":
        return fallback
    return s

def _safe_slug(raw, fallback):
    text = _str(raw).strip().lower()
    if text == "":
        text = fallback
    out = ""
    last_dash = False
    for ch in text.elems():
        ok = (ch >= "a" and ch <= "z") or (ch >= "0" and ch <= "9")
        if ok:
            out += ch
            last_dash = False
        elif not last_dash:
            out += "-"
            last_dash = True
    if out == "":
        return fallback
    return out

def _run_id(ctx):
    supplied = _str(_get(ctx.inputs, "run_id", _get(ctx.world, "run_id", ""))).strip()
    return _safe_slug(supplied, "dogfood-marathon")

def _run_dir(ctx, run_id):
    supplied = _str(_get(ctx.inputs, "run_dir", _get(ctx.world, "run_dir", ""))).strip()
    if supplied != "":
        return supplied
    return ".artifacts/dogfood-marathon/" + run_id

def _out_path(ctx, run_dir):
    supplied = _str(_get(ctx.inputs, "out_path", "")).strip()
    if supplied != "":
        return supplied
    return run_dir + "/decks/aggregate.slidey.json"

def _source(record):
    return record.get("source_url", "") or record.get("source_path", "") or record.get("source_repo", "") or record.get("source_kind", "")

def _source_label(record):
    kind = _str(record.get("source_kind", "")).lower()
    if kind == "local":
        return "📍 local"
    if kind == "github":
        repo = _str(record.get("source_repo", "")).strip()
        if repo != "":
            return "GitHub · " + repo
        return "GitHub"
    return _nonempty(record.get("source_repo", ""), "source")

def _cost(record):
    s = _str(record.get("cost_display", "")).strip()
    if s == "":
        s = _str(record.get("cost_usd", 0)).strip()
    if s == "":
        s = "0"
    if s.startswith("$"):
        return s
    return "$" + s

def _tokens(record):
    return _nonempty(record.get("tokens_display", record.get("tokens", 0)), "0")

def _seconds(record):
    return _nonempty(record.get("wall_s_display", record.get("wall_s", 0)), "0") + "s"

def _verify_status(record):
    return _str(record.get("verify_status", "")).strip().lower()

def _exit_status(record):
    return _str(record.get("exit", "")).strip().lower()

def _verify_badge(record):
    status = _verify_status(record)
    if status == "solved":
        return "✅ solved"
    if status == "partial":
        return "⚠ partial"
    if status == "failed":
        return "❌ failed"
    if status == "skipped":
        return "⏭ skipped"
    if status != "":
        return "• " + status
    return "• pending"

def _exit_badge(record):
    status = _exit_status(record)
    if status == "shipped":
        return "🚢 shipped"
    if status == "needs-human":
        return "⚠ needs-human"
    if status == "not-reproducible":
        return "⏭ not reproducible"
    if status == "abandoned":
        return "❌ abandoned"
    if status == "skipped":
        return "⏭ skipped"
    if status != "":
        return "• " + status
    return "• pending"

def _status_style(record):
    status = _verify_status(record)
    if status == "solved":
        return "primary"
    if status == "partial" or _exit_status(record) == "needs-human":
        return "secondary"
    if status == "failed":
        return "secondary"
    return "default"

def _objective_status(record):
    status = _verify_status(record)
    if status == "solved":
        return "done"
    if status == "partial":
        return "issue"
    if status == "failed":
        return "issue"
    if status == "skipped":
        return "skipped"
    if _exit_status(record) == "needs-human":
        return "issue"
    return "pending"

def _exit_objective_status(record):
    status = _exit_status(record)
    if status == "shipped":
        return "done"
    if status == "needs-human":
        return "issue"
    if status == "abandoned":
        return "blocked"
    if status == "skipped" or status == "not-reproducible":
        return "skipped"
    return "pending"

def _before_lines(record):
    lines = []
    source = _source(record)
    if source != "":
        lines.append(_source_label(record))
    baseline = _one_line(record.get("baseline", ""))
    policy = _one_line(record.get("baseline_policy", ""))
    if baseline != "" or policy != "":
        lines.append("baseline: " + _nonempty(baseline, policy))
    repro = _one_line(record.get("repro_command", ""))
    if repro != "":
        lines.append("RED repro: " + repro)
    triage = _one_line(record.get("triage", ""))
    evidence = _one_line(record.get("triage_evidence", ""))
    if triage != "" or evidence != "":
        lines.append("triage: " + _nonempty(triage, "(pending)") + (" — " + evidence if evidence != "" else ""))
    if len(lines) == 0:
        lines.append("No source/baseline detail was recorded.")
    return lines

def _after_lines(record):
    lines = []
    lines.append(_exit_badge(record))
    lines.append(_verify_badge(record))
    summary = _one_line(record.get("summary", ""))
    if summary != "":
        lines.append("changed: " + summary)
    fix_ref = _one_line(record.get("fix_ref", record.get("branch", "")))
    if fix_ref != "":
        lines.append("fix ref: " + fix_ref)
    verify_how = _one_line(record.get("verify_how", ""))
    if verify_how != "":
        lines.append("verify: " + verify_how)
    question = _one_line(record.get("operator_question", ""))
    if question != "":
        lines.append("operator question: " + question)
    return lines

def _before_cell(record):
    lines = _before_lines(record)
    if len(lines) == 0:
        return "(none)"
    return _short(lines[0], 86)

def _after_cell(record):
    summary = _one_line(record.get("summary", ""))
    if summary != "":
        return _short(summary, 96)
    return _short(_exit_badge(record) + " · " + _verify_badge(record), 96)

def _outcome_bug_cell(record):
    issue = _one_line(record.get("issue_label", ""))
    if issue != "":
        return _short(issue, 26)
    return _short(_str(record.get("case_id", "")), 26)

def _outcome_before_cell(record):
    repro = _one_line(record.get("repro_command", ""))
    if repro != "":
        return "RED: " + _short(repro, 34)
    triage = _one_line(record.get("triage", ""))
    if triage != "":
        return "Triage: " + _short(triage, 30)
    baseline = _one_line(record.get("baseline", ""))
    if baseline != "":
        return "Base: " + _short(baseline, 34)
    return _short(_source_label(record), 38)

def _outcome_after_cell(record):
    summary = _one_line(record.get("summary", ""))
    if summary != "":
        return _short(summary, 44)
    verify_how = _one_line(record.get("verify_how", ""))
    if verify_how != "":
        return _short(verify_how, 44)
    return _short(_exit_badge(record), 44)

def _evidence_cell(record):
    trace_label = _one_line(record.get("trace_label", ""))
    trace = _one_line(record.get("trace", ""))
    if trace_label != "":
        return trace_label
    if trace != "":
        return _short(trace, 56)
    fix_ref = _one_line(record.get("fix_ref", record.get("branch", "")))
    if fix_ref != "":
        return _short(fix_ref, 56)
    return "(none)"

def _metric_card(icon, label, sub, lines, style):
    return {
        "icon": icon,
        "label": _str(label),
        "sub": _str(sub),
        "lines": lines,
        "style": style,
    }

def _join(items, empty):
    vals = []
    for item in _items(items):
        text = _str(item).strip()
        if text != "":
            vals.append(text)
    if len(vals) == 0:
        return empty
    return "\n".join(vals)

def _case_objectives(record):
    return [
        {
            "label": "Triage",
            "status": "done" if _one_line(record.get("triage", "")) != "" else "pending",
            "detail": _short(_nonempty(record.get("triage", ""), "(pending)") + " — " + _nonempty(record.get("triage_evidence", ""), "no evidence detail"), 132),
        },
        {
            "label": "Driver exit",
            "status": _exit_objective_status(record),
            "detail": _exit_badge(record) + " · " + _nonempty(record.get("fix_ref", record.get("branch", "")), "no fix ref"),
        },
        {
            "label": "Independent verify",
            "status": _objective_status(record),
            "detail": _verify_badge(record) + " · " + _short(_nonempty(record.get("verify_how", ""), "no verifier detail"), 122),
        },
        {
            "label": "Traceability",
            "status": "done" if _one_line(record.get("trace", "")) != "" else "issue",
            "detail": _short(_nonempty(record.get("trace", ""), "missing trace path"), 132),
        },
        {
            "label": "Cost / time",
            "status": "done",
            "detail": _cost(record) + " · " + _tokens(record) + " tokens · " + _seconds(record),
        },
    ]

def _case_evidence_items(record):
    items = []
    source = _source(record)
    if source != "":
        items.append({
            "label": "Source issue",
            "status": "validated",
            "detail": _short(_nonempty(record.get("issue_label", record.get("case_id", "")), record.get("case_id", "")), 96),
            "refType": "doc",
            "ref": source,
        })
    trace = _one_line(record.get("trace", ""))
    if trace != "":
        items.append({
            "label": "Trace",
            "status": "validated",
            "detail": _nonempty(record.get("trace_label", ""), "case trace"),
            "refType": "log",
            "ref": trace,
        })
    fix_ref = _one_line(record.get("fix_ref", record.get("branch", "")))
    worktree = _one_line(record.get("worktree", ""))
    if worktree != "":
        detail = "deliverable present" if _truthy(record.get("deliverable_present", False)) else "deliverable not confirmed"
        if fix_ref != "":
            detail += " · fix ref: " + fix_ref
        items.append({
            "label": "Worktree",
            "status": "implemented" if _truthy(record.get("deliverable_present", False)) else "issue",
            "detail": _short(detail, 132),
            "refType": "path",
            "ref": worktree,
        })
    if fix_ref != "" and worktree == "":
        items.append({
            "label": "Fix ref",
            "status": "implemented",
            "detail": _exit_badge(record),
            "refType": "artifact",
            "ref": fix_ref,
        })
    question = _one_line(record.get("operator_question", ""))
    if question != "":
        items.append({
            "label": "Operator question",
            "status": "issue",
            "detail": _short(question, 132),
            "refType": "doc",
            "ref": _source(record),
        })
    if len(items) == 0:
        items.append({
            "label": "Evidence",
            "status": "issue",
            "detail": "No trace, source, worktree, or fix ref was recorded.",
            "refType": "log",
            "ref": "(missing)",
        })
    return items[:6]

def _case_table(record):
    before = _short(_nonempty(record.get("repro_command", ""), record.get("baseline", "")), 82)
    triage = _short(_nonempty(record.get("triage", ""), "(pending)") + " — " + _nonempty(record.get("triage_evidence", ""), "no evidence detail"), 82)
    return [
        {"cells": ["Source", _source_label(record), _nonempty(record.get("issue_label", record.get("case_id", "")), record.get("case_id", "")), "source link"]},
        {"cells": ["Before", before, triage, "RED + triage"]},
        {"cells": ["After", _exit_badge(record), _short(_nonempty(record.get("summary", ""), "(no summary)"), 82), _verify_badge(record)], "highlight": 3},
    ]

def _case_deck(record, run_id):
    case_id = _str(record.get("case_id", "case"))
    title = _str(record.get("title", case_id))
    source = _source(record)
    body = _str(record.get("summary", "")).strip()
    if body == "":
        body = _str(record.get("verify_how", "")).strip()
    if body == "":
        body = "No case summary was recorded."
    before = {
        "label": "🔴 Before",
        "sub": _short(title, 82),
        "lines": _before_lines(record),
        "style": "secondary",
    }
    after = {
        "label": "🟢 After" if _verify_status(record) == "solved" else "🟠 After",
        "sub": _verify_badge(record) + " · " + _exit_badge(record),
        "lines": _after_lines(record),
        "style": _status_style(record),
    }
    return {
        "meta": {
            "title": "Dogfood bug " + case_id,
            "resolution": {"width": 1920, "height": 1080},
            "theme": "rose-pine-moon",
            "narration": {"voice": "en-AU-NatashaNeural", "rate": "+0%"},
        },
        "scenes": [
            {
                "type": "title",
                "eyebrow": "Marathon · " + run_id,
                "title": case_id,
                "subtitle": _verify_badge(record) + " · " + title,
                "narration": "Bug " + case_id + " ended " + _verify_badge(record) + ".",
            },
            {
                "type": "cards",
                "variant": "before-after",
                "title": "Before → after",
                "left": before,
                "right": after,
                "caption": _verify_badge(record) + " · " + _exit_badge(record) + " · " + _cost(record) + " · " + _tokens(record) + " tokens · " + _seconds(record),
                "narration": "Before and after for " + case_id + ": source, repro, fix, and verification.",
            },
            {
                "type": "objectives",
                "title": "Result signals",
                "items": _case_objectives(record),
                "caption": "Large status glyphs separate solved, partial, failed, skipped, and missing-evidence states.",
                "narration": "The case status is split into triage, driver exit, independent verification, traceability, and cost.",
            },
            {
                "type": "table",
                "variant": "comparison",
                "title": "Source → result",
                "columns": ["Surface", "Before", "After", "Signal"],
                "rows": _case_table(record),
                "winner": 3,
                "caption": "A compact scan table. Full source, trace, worktree, fix ref, and cost live on the evidence slide.",
                "narration": "A compact before, after, and result table for " + case_id + ".",
            },
            {
                "type": "evidence",
                "title": "Inspection handles",
                "items": _case_evidence_items(record),
                "caption": "Every row points to a concrete source, trace, path, or fix reference when the run recorded one.",
                "narration": "Concrete evidence handles for " + case_id + ".",
            },
            {
                "type": "narrative",
                "eyebrow": "Recorded summary",
                "lede": _verify_badge(record) + " · " + _nonempty(source, "source not recorded"),
                "body": body,
                "narration": "Recorded summary for " + case_id + ".",
            },
        ],
    }

def _rollup_objectives(counts, totals, case_decks):
    solved = counts.get("solved", 0)
    partial = counts.get("partial", 0)
    failed = counts.get("failed", 0)
    exceptions = counts.get("exceptions", 0)
    processed = counts.get("processed", 0)
    return [
        {
            "label": "Solved",
            "status": "done" if solved > 0 else "pending",
            "detail": "✅ " + _display_number(solved) + " independently verified solved out of " + _display_number(processed) + " processed.",
        },
        {
            "label": "Needs review",
            "status": "issue" if partial > 0 or exceptions > 0 else "done",
            "detail": "⚠ " + _display_number(partial) + " partial; " + _display_number(exceptions) + " serious exception(s).",
        },
        {
            "label": "Failed",
            "status": "issue" if failed > 0 else "done",
            "detail": "❌ " + _display_number(failed) + " failed independent verification.",
        },
        {
            "label": "Evidence decked",
            "status": "done" if len(case_decks) == processed else "issue",
            "detail": str(len(case_decks)) + " per-bug deck(s) generated for " + _display_number(processed) + " recorded case(s).",
        },
        {
            "label": "Cost / time",
            "status": "done",
            "detail": "$" + _display_number(totals.get("cost_usd", 0)) + " · " + _display_number(totals.get("tokens", 0)) + " tokens · " + _display_number(totals.get("wall_s", 0)) + "s.",
        },
    ]

def _rollup_cards(counts, totals, case_decks):
    return [
        _metric_card("✅", "Solved", _display_number(counts.get("solved", 0)), ["independently verified", _display_number(counts.get("shipped", 0)) + " shipped"], "primary"),
        _metric_card("⚠", "Needs review", _display_number(counts.get("partial", 0)), [_display_number(counts.get("needs_human", 0)) + " needs-human", _display_number(counts.get("exceptions", 0)) + " exception(s)"], "secondary" if counts.get("partial", 0) > 0 or counts.get("exceptions", 0) > 0 else "default"),
        _metric_card("❌", "Failed", _display_number(counts.get("failed", 0)), ["independent verify failures", _display_number(counts.get("abandoned", 0)) + " abandoned"], "secondary" if counts.get("failed", 0) > 0 else "default"),
        _metric_card("🧾", "Per-bug decks", str(len(case_decks)), ["one focused deck per case", "plus aggregate deck"], "default"),
        _metric_card("💰", "Cost", "$" + _display_number(totals.get("cost_usd", 0)), [_display_number(totals.get("tokens", 0)) + " tokens", _display_number(totals.get("wall_s", 0)) + "s"], "default"),
        _metric_card("🧭", "Source mix", _display_number(counts.get("github", 0)) + " GitHub", [_display_number(counts.get("local", 0)) + " local", _display_number(counts.get("processed", 0)) + " processed"], "default"),
    ]

def _outcome_row(record, idx):
    return {
        "cells": [
            str(idx),
            _outcome_bug_cell(record),
            _outcome_before_cell(record),
            _outcome_after_cell(record),
            _short(_verify_badge(record) + " · " + _exit_badge(record), 32),
        ],
        "highlight": 4,
    }

def _outcome_rows(records, start, limit):
    rows = []
    i = 0
    for r in _items(records):
        item = _dict(r)
        if i >= start and i < start + limit:
            rows.append(_outcome_row(item, i + 1))
        i += 1
    return rows

def _outcome_table_scene(records, start, limit):
    end = start + len(_outcome_rows(records, start, limit))
    return {
        "type": "table",
        "variant": "data",
        "title": "Bug outcomes " + str(start + 1) + "–" + str(end),
        "columns": ["#", "Bug", "Before", "After", "Result"],
        "rows": _outcome_rows(records, start, limit),
        "caption": "Concise scan view. Full source, trace, worktree, and fix evidence lives in each per-bug deck.",
        "hold": 420,
        "narration": "A scan table of bug outcomes " + str(start + 1) + " through " + str(end) + ".",
    }

def _evidence_items_for_run(findings, exceptions):
    rows = []
    for ex in _items(exceptions.get("items", [])):
        item = _dict(ex)
        rows.append({
            "label": "⚠ " + _short(_nonempty(item.get("case_id", ""), "exception"), 48),
            "status": "issue" if _str(item.get("status", "")).lower() != "parked" else "skipped",
            "detail": _short(_nonempty(item.get("question", item.get("summary", "")), "serious exception raised"), 132),
            "refType": "doc",
            "ref": _nonempty(item.get("issue_url", item.get("trace", "")), "(no ref)"),
        })
        if len(rows) >= 6:
            return rows
    for finding in _items(findings.get("items", [])):
        item = _dict(finding)
        rows.append({
            "label": "Finding · " + _short(_nonempty(item.get("title", ""), item.get("severity", "issue")), 48),
            "status": "issue",
            "detail": _short(_nonempty(item.get("detail", item.get("summary", "")), "finding recorded"), 132),
            "refType": "log",
            "ref": _nonempty(item.get("path", ""), "journal"),
        })
        if len(rows) >= 6:
            return rows
    if len(rows) == 0:
        rows.append({
            "label": "No serious findings",
            "status": "done",
            "detail": "The run recorded no serious exceptions or consequential findings.",
            "refType": "log",
            "ref": "journal",
        })
    return rows

def _deck_index_rows(case_decks, start, limit):
    rows = []
    i = 0
    for deck in _items(case_decks):
        item = _dict(deck)
        if i >= start and i < start + limit:
            rows.append({
                "cells": [
                    str(i + 1),
                    _short(_str(item.get("case_id", "")), 40),
                    _short(_str(item.get("title", "")), 72),
                    _short(_str(item.get("path", "")), 90),
                ],
            })
        i += 1
    return rows

def _aggregate_deck(run_id, rollup, results, findings, exceptions, case_decks):
    counts = _dict(rollup.get("counts", {}))
    totals = _dict(rollup.get("totals", {}))
    records = _items(results.get("items", []))
    scenes = [
        {
            "type": "title",
            "eyebrow": "Dogfood marathon",
            "title": run_id,
            "subtitle": _str(rollup.get("headline", "")),
            "narration": "A queue marathon over configured cases, with clear before-after and result affordances.",
        },
        {
            "type": "objectives",
            "title": "Run status at a glance",
            "items": _rollup_objectives(counts, totals, case_decks),
            "caption": "Green check = independently verified. Orange warning = needs human review. Red exclamation = failed verification.",
            "narration": "The run status uses large glyphs and color so solved, partial, failed, and evidence states are instantly legible.",
        },
        {
            "type": "cards",
            "variant": "icon-row",
            "columns": 3,
            "title": "Outcome scoreboard",
            "narration": "Counts and totals rolled up across every processed case.",
            "cards": _rollup_cards(counts, totals, case_decks),
            "caption": "Tokens are primary cost accounting; dollars are shown when the run recorded them.",
        },
        {
            "type": "narrative",
            "eyebrow": "How to read the next slides",
            "lede": "Before → after → result → evidence.",
            "body": "Each bug row uses the same order: source and RED starting point, what changed or why it was parked, independent verification status, then the trace/fix evidence. ✅ means solved; ⚠ means partial or operator-scoped; ❌ means failed verification; ⏭ means intentionally skipped.",
            "narration": "The outcome tables use one visual language: before, after, result, evidence.",
        },
    ]

    record_count = len(records)
    if record_count > 0:
        scenes.append(_outcome_table_scene(records, 0, 5))
    if record_count > 5:
        scenes.append(_outcome_table_scene(records, 5, 5))
    if record_count > 10:
        scenes.append(_outcome_table_scene(records, 10, 5))
    if record_count > 15:
        scenes.append(_outcome_table_scene(records, 15, 5))

    scenes.extend([
        {
            "type": "narrative",
            "eyebrow": "What worked",
            "lede": "Evidence that held up.",
            "body": _join(rollup.get("worked", []), "No positive patterns recorded."),
            "narration": "What worked across this run.",
        },
        {
            "type": "evidence",
            "title": "Issues, findings, and serious questions",
            "items": _evidence_items_for_run(findings, exceptions),
            "caption": "Warnings are not buried in prose; they get status glyphs and concrete references.",
            "narration": "Friction and serious questions captured for follow-up.",
        },
        {
            "type": "narrative",
            "eyebrow": "What did not",
            "lede": "Friction becomes findings.",
            "body": _join(rollup.get("didnt", []), "No failures or serious exceptions recorded."),
            "narration": "Friction and serious exceptions captured for follow-up.",
        },
        {
            "type": "table",
            "variant": "data",
            "title": "Per-bug deck index 1–8",
            "columns": ["#", "Bug", "Title", "Deck path"],
            "rows": _deck_index_rows(case_decks, 0, 8),
            "caption": "Each bug has its own focused deck with before/after, status, and evidence scenes.",
            "hold": 360,
            "narration": "The first per-bug deck index.",
        },
    ])
    if len(case_decks) > 8:
        scenes.append({
            "type": "table",
            "variant": "data",
            "title": "Per-bug deck index 9–15",
            "columns": ["#", "Bug", "Title", "Deck path"],
            "rows": _deck_index_rows(case_decks, 8, 8),
            "caption": "The remaining per-bug decks.",
            "hold": 360,
            "narration": "The second per-bug deck index.",
        })

    return {
        "meta": {
            "title": "Dogfood marathon " + run_id,
            "resolution": {"width": 1920, "height": 1080},
            "theme": "rose-pine-moon",
            "narration": {"voice": "en-AU-NatashaNeural", "rate": "+0%"},
        },
        "scenes": scenes,
    }

def main(ctx):
    run_id = _run_id(ctx)
    run_dir = _run_dir(ctx, run_id)
    out_path = _out_path(ctx, run_dir)
    results = _dict(ctx.inputs.get("results", {}))
    rollup = _dict(ctx.inputs.get("rollup", {}))
    findings = _dict(ctx.inputs.get("findings", {}))
    exceptions = _dict(ctx.inputs.get("exceptions", {}))

    case_decks = []
    for record in _items(results.get("items", [])):
        item = _dict(record)
        slug = _safe_slug(item.get("case_id", ""), "case-" + str(len(case_decks) + 1))
        path = run_dir + "/decks/" + slug + ".slidey.json"
        spec = _case_deck(item, run_id)
        ctx.fs.write(path, json.encode(spec) + "\n")
        case_decks.append({
            "case_id": item.get("case_id", ""),
            "title": item.get("title", ""),
            "path": path,
            "source": _source(item),
            "result": _verify_badge(item),
        })

    aggregate = _aggregate_deck(run_id, rollup, results, findings, exceptions, case_decks)
    ctx.fs.write(out_path, json.encode(aggregate) + "\n")

    counts = _dict(rollup.get("counts", {}))
    processed = counts.get("processed", len(_items(results.get("items", []))))
    solved = counts.get("solved", 0)
    partial = counts.get("partial", 0)
    failed = counts.get("failed", 0)
    summary = "Dogfood marathon " + run_id + ": ✅ " + _display_number(solved) + " solved, ⚠ " + _display_number(partial) + " partial, ❌ " + _display_number(failed) + " failed across " + _display_number(processed) + " case(s); " + str(len(case_decks)) + " per-bug deck(s)."

    return {
        "deck_spec": {
            "spec_path": out_path,
            "summary": summary,
            "case_decks": case_decks,
            "scenes_preview": [
                "title: " + run_id,
                "status: objectives with solved/partial/failed glyphs",
                "scoreboard: color-coded cards",
                "outcomes: before/after/result tables",
                "per-bug decks: before-after + objectives + evidence",
            ],
        },
    }
