# gs_manifest.star — write a SINGLE-ITEM punch-list/v1 manifest for one change.
#
# Given a change_id + the decomposition, look up the change and emit a one-item
# punch-list manifest to <work_dir>/manifests/<change_id>.json (JSON is valid YAML,
# which punch-list's loader decodes). The change's pipeline picks the story punch-list
# drives; its gate.cmd becomes the deterministic verify. Returns the manifest path +
# the change object (bound into world.current_change for the integrate projection).
#
# The manifest item also carries structured target-story inputs (`ticket_id`,
# `ticket_title`, and a generic `world_in` block) so punch-list's driver can seed
# the target session's `initial_world` instead of relying on a free-text prompt
# alone — both stories/implementation and stories/bugfix self-provision (workspace/
# branch/workdir) from `world.ticket_id` in their `idle` rooms, but only when a
# caller actually hands one over. See stories/punch-list/prompts/drive_item.md.
#
# LIVE PROOF (twice) showed the free-text maker driving the target session does
# NOT reliably pass `initial_world` to `session.new` even when told to — it opens
# the session with an empty world and drives off the prompt text alone, so
# `world.ticket_id` stays empty and the target story can't self-provision. What
# the maker DOES reliably do is drive the prompt text verbatim as its first turn.
# So the prompt below LEADS with an explicit, verbatim-quotable ticket directive
# ("Work ticket <id> titled \"<title>\". <brief>") that both stories/implementation
# and stories/bugfix's `idle` rooms can extract via a semantic-router template
# (`work_ticket` intent, `{ticket_id}`/`{ticket_title}` captures — see their
# rooms/idle.yaml) even when initial_world never arrives. world_in stays as the
# belt-and-suspenders structured path for a future maker that DOES honor it.
#
# "titled" is a deliberate literal anchor: the router's `{ticket_id}` capture is
# a trailing run absorbing every token up to the next literal, so without an
# anchor right after the id it would swallow the rest of the message (title +
# brief) into ticket_id itself. "titled" bounds it cleanly. The `{ticket_title}`
# capture after it is best-effort only (it trails to end-of-input, so it also
# picks up the brief) — both target stories treat a noisy ticket_title as
# harmless: implementation's review_task_executing immediately re-fetches the
# authoritative title via iface.ticket.get, and bugfix only ever uses it as
# human-facing display text.
#
# stories/implementation additionally calls `iface.ticket.get {id}` (review_task
# room), which — unlike the append-only transport — does NOT self-bootstrap: the
# file-backed default handler (host.local_files.ticket) requires a REAL
# `<root>/issues/<kind>/<id>.md` record to already exist. So this script also
# writes a synthetic ticket file, scoped under the goal's own work_dir (never the
# project's real, now-frozen `issues/` archive — see issues/DEPRECATED.md) and
# points `world_in.tickets_root` at it so the target session's ticket.get resolves
# there via its `root` arg instead of falling back to cwd.
#
# WM.1 (bounded-context worker brief): the dispatch prompt below is the
# router-anchor directive ("Work ticket <id> titled ...") FOLLOWED by the SLIM
# worker-brief projection (Task/Gate/in-scope-files/referenced-docs) from
# scripts/gs_worker_brief.star's `worker_brief_sections` — so a fresh worker
# gets exactly the spec + gate + small in-scope files inline + pointers into
# large ones, instead of the bare agent_brief that used to send it exploring
# the whole decomposition. worker_brief_sections (and its small helpers) are
# DUPLICATED verbatim from gs_worker_brief.star rather than `load`ed: this
# sandbox's host.starlark.run wires no Starlark loader (see
# internal/host/starlark/run.go), so cross-.star imports aren't available.
# gs_worker_brief.star is the canonical copy — keep this one in sync with it.

PIPELINE_STORY = {
    "bugfix": "stories/bugfix/app.yaml",
    "implementation": "stories/implementation/app.yaml",
    "docs": "stories/dev-story/app.yaml",
}

MAKER_LADDER = {
    "models": [
        {"backend": "codex", "provider": "synthetic-codex", "model": "hf:zai-org/GLM-5.2"},
        {"backend": "codex", "provider": "codex-native", "model": "gpt-5.5"},
        {"backend": "codex", "provider": "codex-spark", "model": "gpt-5.3-codex-spark"},
        {"backend": "claude", "provider": "claude-sonnet", "model": "sonnet"},
        {"backend": "claude", "provider": "claude-native", "model": "opus"},
    ],
    "efforts": ["low", "medium", "high", "xhigh", "max"],
    "max_attempts": 12,
    "backoff": "5m",
}


def _yaml_dq(s):
    # Minimal YAML double-quoted-scalar escaping — enough for a ticket title;
    # not a general YAML encoder.
    s = _str(s).replace("\\", "\\\\").replace("\"", "\\\"")
    return "\"" + s + "\""


def _str(v):
    if v == None:
        return ""
    return str(v)


def _ticket_body_markdown(change, gate_cmd):
    lines = []
    brief = _str(change.get("agent_brief")).strip()
    if brief != "":
        lines.append(brief)
        lines.append("")
    scope = change.get("scope") or []
    if len(scope) > 0:
        lines.append("## Scope")
        for s in scope:
            lines.append("- " + _str(s))
        lines.append("")
    acceptance = change.get("acceptance") or []
    if len(acceptance) > 0:
        lines.append("## Acceptance")
        for a in acceptance:
            lines.append("- " + _str(a))
        lines.append("")
    if gate_cmd != "":
        lines.append("## Gate")
        lines.append("`" + gate_cmd + "`")
        lines.append("")
    return "\n".join(lines)


# ─── worker-brief projection (duplicated from gs_worker_brief.star; see the
# module docstring for why) ──────────────────────────────────────────────────


def _wb_join(repo, entry):
    r = _str(repo).rstrip("/")
    if r == "" or r == ".":
        return entry
    return r + "/" + entry


def _wb_lang_for(rel):
    if rel.endswith(".go"):
        return "go"
    if rel.endswith(".yaml") or rel.endswith(".yml"):
        return "yaml"
    if rel.endswith(".py"):
        return "python"
    if rel.endswith(".star"):
        return "python"
    return ""


def _wb_file_block(ctx, path, max_inline):
    text = ctx.fs.read(path)
    n = text.count("\n") + 1
    if n <= max_inline:
        return ("### `" + path + "` (" + str(n) + " lines) — full content:\n```" +
                _wb_lang_for(path) + "\n" + text + "\n```")
    return ("### `" + path + "` (" + str(n) +
            " lines) — LARGE; read only the slice you need (see the pointers in the Task above).")


def _wb_is_pathchar(c):
    return c.isalnum() or c == "-" or c == "_" or c == "."


def _wb_find_proposal_refs(text):
    marker = "docs/proposals/"
    parts = text.split(marker)
    refs = []
    if len(parts) <= 1:
        return refs
    for part in parts[1:]:
        end = len(part)
        for i in range(len(part)):
            if not _wb_is_pathchar(part[i]):
                end = i
                break
        candidate = marker + part[:end]
        if end > 0 and candidate.endswith(".md") and candidate not in refs:
            refs.append(candidate)
    return refs


def worker_brief_sections(ctx, change, repo, max_inline_lines):
    gate = change.get("gate") or {}
    scope = change.get("scope") or []
    brief = _str(change.get("agent_brief")).strip()
    accept = change.get("acceptance") or []
    cmd = _str(gate.get("cmd") or gate.get("replay_cmd") or "(no runnable gate)")

    o = []
    o.append("## Task")
    o.append(brief if brief != "" else "(no agent_brief)")
    if len(accept) > 0:
        o.append("")
        o.append("**Acceptance criteria:**")
        for a in accept:
            o.append("- " + _str(a))

    o.append("")
    o.append("## Gate — must be GREEN *for the right reason*")
    o.append("- class: `" + _str(gate.get("class")) + "`")
    o.append("- **command:** `" + cmd + "`")
    if gate.get("red_now"):
        o.append("- expected RED-now: " + _str(gate.get("red_now")))
    o.append(
        "- Verify RED **before** your change and GREEN **after** (RED-first). If the gate is already GREEN " +
        "and the behavior already exists, the change is ALREADY-SATISFIED — report that with file:line evidence, " +
        "do NOT fabricate work. If the gate is weak (passes without the feature), still deliver the real brief " +
        "and self-verify the actual behavior."
    )

    o.append("")
    o.append("## In-scope files (edit ONLY these)")
    if len(scope) == 0:
        o.append("(none declared)")
    for entry in scope:
        joined = _wb_join(repo, entry)
        if "**" in joined:
            o.append("- glob `" + entry + "` — recursive scope; see/create files here (not expanded).")
        elif "*" in joined:
            matches = ctx.fs.glob(joined)
            if len(matches) > 0:
                shown = matches[:20]
                listing = ", ".join(["`" + m + "`" for m in shown])
            else:
                listing = "(none yet — you may create files here)"
            o.append("- glob `" + entry + "` → " + listing)
        elif ctx.fs.exists(joined):
            o.append(_wb_file_block(ctx, joined, max_inline_lines))
        else:
            o.append("- `" + entry + "` — does NOT exist yet; you create it.")

    refs = _wb_find_proposal_refs(brief)
    if len(refs) > 0:
        o.append("")
        o.append("## Referenced design docs")
        for r in refs:
            if ctx.fs.exists(r):
                o.append(_wb_file_block(ctx, r, max_inline_lines))
            else:
                o.append("- `" + r + "` — read it in your worktree.")

    return o


def _write_ticket(ctx, tickets_root, change_id, title, change, gate_cmd):
    # issues/<kind>/<id>.md is the on-disk contract host.local_files.ticket
    # scans (findTicketPath); "features" is as good a bucket as any for a
    # generic dispatched change (ticket.get's `type` field is not branched on
    # by either target story).
    slug = change_id.replace("/", "_")
    ticket_path = tickets_root + "/issues/features/" + slug + ".md"
    doc = "---\n"
    doc += "title: " + _yaml_dq(title) + "\n"
    doc += "status: open\n"
    doc += "---\n\n"
    doc += "# " + title + "\n\n"
    doc += _ticket_body_markdown(change, gate_cmd)
    ctx.fs.write(ticket_path, doc)

    # The NL-extraction fallback (idle.yaml's `work_ticket` intent — see the
    # module docstring) captures ticket_id through the semantic router's
    # `{slot}` template mechanism, which lowercases captured string values
    # (internal/lex.Tokenize normalises + lowercases BEFORE the matcher ever
    # sees the text — see docs/architecture/semantic-routing.md §1.3's case-
    # folding caveat). change_id is frequently mixed-case (e.g. "WM.9"), so a
    # live NL-driven ticket_id would arrive lowercased ("wm.9") while this
    # exact-case file is "WM.9.md" — findTicketPath's os.Stat lookup is
    # case-sensitive on Linux (masked by APFS's case-insensitive default on
    # macOS dev boxes, which is why this wouldn't show up locally). Mirror
    # the ticket at the lowercased slug too so ticket.get resolves regardless
    # of which path (structured world_in, exact case; or NL extraction,
    # lowercased) produced world.ticket_id.
    lower_slug = slug.lower()
    if lower_slug != slug:
        ctx.fs.write(tickets_root + "/issues/features/" + lower_slug + ".md", doc)

    return ticket_path


def main(ctx):
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    work_dir = ctx.inputs["work_dir"].rstrip("/")
    change_id = ctx.inputs["change_id"].strip()
    if change_id == "":
        fail("gs_manifest: change_id is required")

    decomp_path = goal_dir + "/decomposition.yaml"
    if not ctx.fs.exists(decomp_path):
        fail("gs_manifest: no decomposition.yaml at " + decomp_path)
    doc = yaml.decode(ctx.fs.read(decomp_path))
    changes = doc.get("changes") or []

    change = None
    for c in changes:
        if str(c["id"]) == change_id:
            change = c
    if change == None:
        fail("gs_manifest: change_id '" + change_id + "' not in decomposition")

    gate = change.get("gate") or {}
    gate_cmd = str(gate.get("cmd") or "true")
    pipeline = str(change.get("pipeline") or "implementation")
    story = PIPELINE_STORY.get(pipeline, "stories/implementation/app.yaml")
    title = str(change.get("title") or change_id)
    agent_brief = str(change.get("agent_brief") or title or change_id)
    # Lead with the verbatim-parseable ticket directive (see the module
    # docstring): "titled" anchors the {ticket_id} template capture in the
    # target story's idle room so it doesn't swallow the rest of this prompt.
    # Then fold in the WM.1 slim worker-brief projection (Task/Gate/in-scope
    # files/referenced docs) so the dispatched worker gets a bounded, complete
    # context instead of just the bare agent_brief.
    ticket_directive = "Work ticket " + change_id + " titled \"" + title + "\". " + agent_brief
    brief_sections = worker_brief_sections(ctx, change, ".", 180)
    action = ticket_directive + "\n\n" + "\n".join(brief_sections)

    tickets_root = work_dir + "/tickets"
    ticket_path = _write_ticket(ctx, tickets_root, change_id, title, change, gate_cmd)

    manifest = {
        "version": "punch-list/v1",
        "defaults": {
            "harness": "ladder",
            "profile": "synthetic-codex",
            "model": "hf:zai-org/GLM-5.2",
            "harness_ladder": MAKER_LADDER,
            "sandbox": {
                "min_strength": "supervised",
                "repo": "read_only",
                "rw": [work_dir, ".worktrees"],
                "hidden": [".env", ".git/config", ".git-credentials"],
                "network": "inherit",
                "degrade": "warn",
                "resources": {"timeout": "8m"},
            },
            "trace_root": work_dir + "/traces",
            "require_trace_model": False,
        },
        "items": [{
            "id": change_id,
            "title": title,
            "priority": 1,
            "story": story,
            "mode": "drive",
            "prompt": action,
            "gate_command": gate_cmd,
            "verify": [{"kind": "command", "cmd": gate_cmd}],
            # Structured target-story inputs the driver forwards as `initial_world`
            # when it opens the target session (see stories/punch-list's
            # prompts/drive_item.md) — the seam that lets a change actually LAND
            # instead of being driven blind off free text alone. punch-list itself
            # never reads these keys; it only carries the item through.
            "ticket_id": change_id,
            "ticket_title": title,
            "world_in": {
                "ticket_id": change_id,
                "ticket_title": title,
                "tickets_root": tickets_root,
            },
        }],
    }

    slug = change_id.replace("/", "_")
    manifest_path = work_dir + "/manifests/" + slug + ".json"
    ctx.fs.write(manifest_path, json.encode(manifest) + "\n")
    return {"manifest_path": manifest_path, "change": change, "gate_cmd": gate_cmd, "ticket_path": ticket_path}
