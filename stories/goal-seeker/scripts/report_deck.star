# report_deck.star - deterministic PM report/deck hierarchy for goal-seeker.
#
# Reads the current <work_dir>/ledger.json (written by gs_ledger) and
# <work_dir>/log.jsonl, then emits:
#   * <work_dir>/reports/exec-summary.md
#   * <work_dir>/reports/exec-summary.slidey.json
#   * <work_dir>/reports/changes/<change_id>.md
#   * <work_dir>/reports/changes/<change_id>.slidey.json
#
# This is intentionally a projection over the append-only log + folded ledger:
# no LLM, no shell, no rendering, and no hidden state.


def _str(v):
    if v == None:
        return ""
    return str(v)


def _slug(s):
    out = []
    for ch in _str(s).elems():
        if ch.isalnum() or ch == "-" or ch == "_":
            out.append(ch)
        else:
            out.append("_")
    if len(out) == 0:
        return "change"
    return "".join(out)


def _read_log(ctx, work_dir):
    path = work_dir + "/log.jsonl"
    if not ctx.fs.exists(path):
        return []
    out = []
    for line in ctx.fs.read(path).split("\n"):
        line = line.strip()
        if line == "":
            continue
        out.append(json.decode(line))
    return out


def _read_plan_events(ctx, work_dir):
    path = work_dir + "/plan-evolution.jsonl"
    if not ctx.fs.exists(path):
        return []
    out = []
    for line in ctx.fs.read(path).split("\n"):
        line = line.strip()
        if line == "":
            continue
        out.append(json.decode(line))
    return out


def _latest_log_for(log, change_id):
    latest = None
    latest_seq = -1
    for e in log:
        if _str(e.get("change_id")) != change_id:
            continue
        seq = e.get("seq", 0)
        if seq >= latest_seq:
            latest = e
            latest_seq = seq
    return latest


def _split_md_row(line):
    stripped = line.strip()
    if not stripped.startswith("|") or not stripped.endswith("|"):
        return []
    cells = []
    for cell in stripped.strip("|").split("|"):
        cells.append(cell.strip())
    return cells


def _read_video_catalog(ctx, goal_dir):
    # Authoritative WM.4 catalog. Markdown keeps it reviewable; this parser only
    # consumes the stable table shape used by docs/goals/*/video-catalog.md.
    path = goal_dir.rstrip("/") + "/video-catalog.md"
    if not ctx.fs.exists(path):
        return {}
    rows = {}
    header = []
    for line in ctx.fs.read(path).split("\n"):
        cells = _split_md_row(line)
        if len(cells) == 0:
            continue
        if len(header) == 0:
            header = [c.lower().replace(" ", "_") for c in cells]
            continue
        # Skip Markdown separator rows.
        sep = True
        for c in cells:
            for ch in c.elems():
                if ch != "-" and ch != ":":
                    sep = False
        if sep:
            continue
        rec = {}
        for i in range(min(len(header), len(cells))):
            rec[header[i]] = cells[i]
        cid = _str(rec.get("change"))
        if cid != "":
            rows[cid] = rec
    return rows


def _video_for(ctx, catalog, change_id):
    rec = catalog.get(change_id)
    if rec == None:
        return None
    clip = _str(rec.get("rrweb_clip"))
    if clip == "":
        return None
    if not ctx.fs.exists(clip):
        fail("report_deck: video catalog row for " + change_id + " points at missing clip " + clip)
    if ctx.fs.read(clip).strip() == "":
        fail("report_deck: video catalog row for " + change_id + " points at empty clip " + clip)
    return {
        "clip": clip,
        "title": _str(rec.get("title") or ("Demo for " + change_id)),
        "compendium": _str(rec.get("compendium")).lower() == "yes",
        "shows": _str(rec.get("shows")),
    }


def _goal_title(ctx, goal_dir, fallback):
    path = goal_dir.rstrip("/") + "/GOAL.md"
    if not ctx.fs.exists(path):
        return fallback
    for line in ctx.fs.read(path).split("\n"):
        stripped = line.strip()
        if stripped.startswith("#"):
            return stripped.lstrip("#").strip()
    return fallback


def _status_table(changes):
    lines = []
    lines.append("| Change | State | Gate | SHA | Title |")
    lines.append("|---|---|---|---|---|")
    for c in changes:
        lines.append("| " + _str(c.get("change_id")) + " | " + _str(c.get("state")) + " | " +
                     _str(c.get("gate_status")) + " | " + _str(c.get("sha") or "") + " | " +
                     _str(c.get("title")) + " |")
    return "\n".join(lines)


def _plan_event_label(event):
    added = event.get("added") or []
    added_s = ", ".join([_str(a) for a in added])
    parts = []
    trigger = _str(event.get("trigger"))
    provenance = _str(event.get("provenance"))
    if trigger != "":
        parts.append("trigger `" + trigger + "`")
    if provenance != "":
        parts.append("provenance `" + provenance + "`")
    if added_s != "":
        parts.append("added `" + added_s + "`")
    version_path = _str(event.get("version_path"))
    if version_path != "":
        parts.append("version `" + version_path + "`")
    if len(parts) == 0:
        return _str(event)
    return "; ".join(parts)


def _change_markdown(goal_title, change, log_entry, deck_path):
    cid = _str(change.get("change_id"))
    lines = []
    lines.append("# Change " + cid + " - " + _str(change.get("title")))
    lines.append("")
    lines.append("- Goal: " + goal_title)
    lines.append("- State: `" + _str(change.get("state")) + "`")
    lines.append("- Gate: `" + _str(change.get("gate_status")) + "`")
    lines.append("- SHA: `" + _str(change.get("sha") or "") + "`")
    lines.append("- Deck: `" + deck_path + "`")
    lines.append("")
    lines.append("## Scope")
    scopes = change.get("scope_globs") or []
    if len(scopes) == 0:
        lines.append("(none)")
    for s in scopes:
        lines.append("- `" + _str(s) + "`")
    lines.append("")
    lines.append("## Evidence")
    if log_entry == None:
        lines.append("- No log entry was found for this integrated change.")
    else:
        lines.append("- Log seq: `" + _str(log_entry.get("seq")) + "`")
        lines.append("- Actor: `" + _str(log_entry.get("actor")) + "`")
        lines.append("- Summary: " + _str(log_entry.get("summary")))
    return "\n".join(lines) + "\n"


def _summary_markdown(goal_title, ledger, integrated, change_paths, videos, plan_events):
    counts = ledger.get("counts") or {}
    lines = []
    lines.append("# " + goal_title + " - execution summary")
    lines.append("")
    lines.append("- Goal slug: `" + _str(ledger.get("goal_slug")) + "`")
    lines.append("- Remaining: `" + _str(ledger.get("remaining")) + "`")
    lines.append("- Integrated: `" + _str(counts.get("integrated", 0)) + "`")
    lines.append("- Ready: `" + _str(counts.get("ready", 0)) + "`")
    lines.append("- Blocked: `" + _str(counts.get("blocked", 0)) + "`")
    lines.append("- Parked: `" + _str(counts.get("parked", 0)) + "`")
    lines.append("")
    lines.append("## Status")
    lines.append(_status_table(ledger.get("changes") or []))
    lines.append("")
    lines.append("## Integrated change decks")
    if len(integrated) == 0:
        lines.append("(none yet)")
    for c in integrated:
        cid = _str(c.get("change_id"))
        p = change_paths.get(cid) or {}
        lines.append("- " + cid + ": `" + _str(p.get("deck")) + "` (`" + _str(p.get("markdown")) + "`)")
    lines.append("")
    lines.append("## Demo videos")
    any_video = False
    for c in integrated:
        cid = _str(c.get("change_id"))
        v = videos.get(cid)
        if v == None:
            continue
        any_video = True
        lines.append("- " + cid + ": `" + _str(v.get("clip")) + "` - " + _str(v.get("shows")))
    if not any_video:
        lines.append("(none linked)")
    lines.append("")
    lines.append("## Plan evolution")
    if len(plan_events) == 0:
        lines.append("(none recorded)")
    for event in plan_events:
        lines.append("- " + _plan_event_label(event))
    return "\n".join(lines) + "\n"


def _exec_deck(goal_title, ledger, summary_path, integrated, change_paths, videos, plan_events):
    counts = ledger.get("counts") or {}
    rows = []
    for c in ledger.get("changes") or []:
        rows.append({
            "Change": _str(c.get("change_id")),
            "State": _str(c.get("state")),
            "Gate": _str(c.get("gate_status")),
            "SHA": _str(c.get("sha") or ""),
        })
    links = []
    video_cards = []
    for c in integrated:
        cid = _str(c.get("change_id"))
        p = change_paths.get(cid) or {}
        links.append(cid + ": " + _str(p.get("deck")))
        v = videos.get(cid)
        if v != None:
            video_cards.append({
                "title": cid + " - " + _str(v.get("title")),
                "body": _str(v.get("shows")),
                "rrweb": _str(v.get("clip")),
            })
    scenes = [
        {
            "type": "title",
            "title": goal_title,
            "subtitle": "Goal-seeker execution summary",
        },
        {
            "type": "cards",
            "title": "Current ledger",
            "cards": [
                {"title": "Integrated", "body": _str(counts.get("integrated", 0))},
                {"title": "Remaining", "body": _str(ledger.get("remaining"))},
                {"title": "Ready", "body": _str(counts.get("ready", 0))},
                {"title": "Parked", "body": _str(counts.get("parked", 0))},
            ],
        },
    ]
    if len(video_cards) > 0:
        scenes.append({
            "type": "cards",
            "title": "Demo compendium",
            "cards": video_cards,
        })
    if len(plan_events) > 0:
        event_rows = []
        for event in plan_events:
            event_rows.append({
                "Trigger": _str(event.get("trigger")),
                "Provenance": _str(event.get("provenance")),
                "Added": ", ".join([_str(a) for a in (event.get("added") or [])]),
                "Version": _str(event.get("version_path")),
            })
        scenes.append({
            "type": "table",
            "title": "Plan evolution",
            "rows": event_rows,
        })
    scenes.extend([
        {
            "type": "table",
            "title": "Change status",
            "rows": rows,
        },
        {
            "type": "narrative",
            "eyebrow": "Artifacts",
            "body": "Summary: " + summary_path,
            "lede": "\n".join(links),
        },
    ])
    return {
        "meta": {
            "title": goal_title + " execution summary",
            "mode": "report",
            "source": "goal-seeker",
        },
        "scenes": scenes,
    }


def _change_deck(goal_title, change, markdown_path, log_entry, video):
    cid = _str(change.get("change_id"))
    evidence = "No log entry found."
    if log_entry != None:
        evidence = "seq " + _str(log_entry.get("seq")) + " by " + _str(log_entry.get("actor")) + ": " + _str(log_entry.get("summary"))
    scenes = [
        {
            "type": "title",
            "title": "Change " + cid,
            "subtitle": _str(change.get("title")),
        },
        {
            "type": "narrative",
            "eyebrow": goal_title,
            "body": "State " + _str(change.get("state")) + " with gate " + _str(change.get("gate_status")),
            "lede": evidence,
        },
    ]
    if video != None:
        scenes.append({
            "type": "video",
            "title": _str(video.get("title")),
            "body": _str(video.get("shows")),
            "rrweb": _str(video.get("clip")),
        })
    scenes.append({
        "type": "narrative",
        "eyebrow": "Artifact",
        "body": markdown_path,
    })
    return {
        "meta": {
            "title": "Change " + cid + ": " + _str(change.get("title")),
            "mode": "report",
            "source": "goal-seeker",
        },
        "scenes": scenes,
    }


def main(ctx):
    goal_slug = _str(ctx.inputs["goal_slug"]).strip()
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    work_dir = ctx.inputs["work_dir"].rstrip("/")
    ledger_path = work_dir + "/ledger.json"
    if not ctx.fs.exists(ledger_path):
        fail("report_deck: no ledger.json at " + ledger_path + " (run gs_ledger first)")

    ledger = json.decode(ctx.fs.read(ledger_path))
    goal_title = _goal_title(ctx, goal_dir, goal_slug if goal_slug != "" else "goal")
    catalog = _read_video_catalog(ctx, goal_dir)
    report_dir = work_dir + "/reports"
    changes_dir = report_dir + "/changes"
    summary_path = report_dir + "/exec-summary.md"
    deck_path = report_dir + "/exec-summary.slidey.json"

    log = _read_log(ctx, work_dir)
    plan_events = _read_plan_events(ctx, work_dir)
    integrated = []
    for c in ledger.get("changes") or []:
        if c.get("state") == "integrated":
            integrated.append(c)

    change_paths = {}
    videos = {}
    for c in integrated:
        cid = _str(c.get("change_id"))
        slug = _slug(cid)
        md_path = changes_dir + "/" + slug + ".md"
        change_deck_path = changes_dir + "/" + slug + ".slidey.json"
        entry = _latest_log_for(log, cid)
        video = _video_for(ctx, catalog, cid)
        if video != None:
            videos[cid] = video
        change_paths[cid] = {"markdown": md_path, "deck": change_deck_path}
        ctx.fs.write(md_path, _change_markdown(goal_title, c, entry, change_deck_path))
        ctx.fs.write(change_deck_path, json.encode(_change_deck(goal_title, c, md_path, entry, video)) + "\n")

    ctx.fs.write(summary_path, _summary_markdown(goal_title, ledger, integrated, change_paths, videos, plan_events))
    ctx.fs.write(deck_path, json.encode(_exec_deck(goal_title, ledger, summary_path, integrated, change_paths, videos, plan_events)) + "\n")

    return {
        "status": "written",
        "summary_path": summary_path,
        "deck_path": deck_path,
        "change_count": len(integrated),
        "video_count": len(videos),
        "plan_event_count": len(plan_events),
        "change_paths": change_paths,
    }
