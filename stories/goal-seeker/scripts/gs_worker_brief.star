# gs_worker_brief.star — deterministic worker context-builder (WM.1).
#
# Starlark port of tools/worker-brief.py: given ONE change id, project a SLIM,
# COMPLETE brief a dispatched worker can act on without reading the whole
# decomposition.yaml or exploring the tree. Reads only the resolved change +
# its in-scope files + any docs/proposals/*.md the agent_brief references, and
# formats them. Pure/deterministic: no LLM, no network, no shell — the builder
# just projects what's already on disk.
#
# The point (token efficiency): a fresh worker otherwise spends ~10-20k tokens
# reading the big decomposition.yaml and grepping to locate files. This hands
# it exactly the spec, the gate, the in-scope small files inline, and pointers
# into the large ones — so it goes straight to implementing.
#
# gs_manifest.star DUPLICATES the `worker_brief_sections` projection below
# (and its small helpers) rather than `load`ing this file: host.starlark.run's
# sandbox does not wire a Starlark loader (see internal/host/starlark/run.go —
# ExecFileOptions is called with no Load callback / module map beyond
# json/math/yaml), so cross-.star imports are not available in this sandbox.
# Keep the two copies in sync; this file is the canonical reference. This file
# also has no `while` (Starlark's while requires FileOptions.While, which the
# host does not enable) — every scan below is a bounded `for` over a fixed
# range/list instead.


def _str(v):
    if v == None:
        return ""
    return str(v)


def _join(repo, entry):
    # Mirrors tools/worker-brief.py's os.path.join(repo, entry): repo is an
    # optional subdirectory prefix under ctx.fs's already-rooted tree (ctx.fs
    # cannot address a second filesystem root), so "." / "" is a no-op prefix.
    r = _str(repo).rstrip("/")
    if r == "" or r == ".":
        return entry
    return r + "/" + entry


def _lang_for(rel):
    if rel.endswith(".go"):
        return "go"
    if rel.endswith(".yaml") or rel.endswith(".yml"):
        return "yaml"
    if rel.endswith(".py"):
        return "python"
    if rel.endswith(".star"):
        return "python"
    return ""


def _file_block(ctx, path, max_inline):
    # Caller guarantees ctx.fs.exists(path) already; ctx.fs.read on a missing
    # path is a Starlark error, not a value to branch on.
    text = ctx.fs.read(path)
    n = text.count("\n") + 1
    if n <= max_inline:
        return ("### `" + path + "` (" + str(n) + " lines) — full content:\n```" +
                _lang_for(path) + "\n" + text + "\n```")
    return ("### `" + path + "` (" + str(n) +
            " lines) — LARGE; read only the slice you need (see the pointers in the Task above).")


def _is_pathchar(c):
    return c.isalnum() or c == "-" or c == "_" or c == "."


def _find_proposal_refs(text):
    # No regex module in this sandbox (host.starlark.run's predeclared set is
    # only json/math/yaml — see internal/host/starlark/run.go) — hand-scan for
    # "docs/proposals/<name>.md" substrings via split() instead of re.findall,
    # bounded by a `for i in range(len(part))` scan (no while).
    marker = "docs/proposals/"
    parts = text.split(marker)
    refs = []
    if len(parts) <= 1:
        return refs
    for part in parts[1:]:
        end = len(part)
        for i in range(len(part)):
            if not _is_pathchar(part[i]):
                end = i
                break
        candidate = marker + part[:end]
        if end > 0 and candidate.endswith(".md") and candidate not in refs:
            refs.append(candidate)
    return refs


def worker_brief_sections(ctx, change, repo, max_inline_lines):
    # Returns the Task / Gate / In-scope-files / Referenced-design-docs body as
    # a list of markdown lines. Split out from main() so gs_manifest.star's
    # duplicate can fold it straight into a dispatch prompt without the
    # standalone brief's header/Protocol wrapper.
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
        joined = _join(repo, entry)
        if "**" in joined:
            # ctx.fs.glob is a thin filepath.Glob wrapper with no recursive-**
            # semantics (unlike Python's glob.glob(recursive=True)) — expanding
            # it here would silently under-report nested matches, so list the
            # pattern instead of pretending to enumerate it.
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
            o.append(_file_block(ctx, joined, max_inline_lines))
        else:
            o.append("- `" + entry + "` — does NOT exist yet; you create it.")

    refs = _find_proposal_refs(brief)
    if len(refs) > 0:
        o.append("")
        o.append("## Referenced design docs")
        for r in refs:
            if ctx.fs.exists(r):
                o.append(_file_block(ctx, r, max_inline_lines))
            else:
                o.append("- `" + r + "` — read it in your worktree.")

    return o


def main(ctx):
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    change_id = ctx.inputs["change_id"].strip()
    if change_id == "":
        fail("gs_worker_brief: change_id is required")

    repo = _str(ctx.inputs.get("repo") or ".")
    worktree = _str(ctx.inputs.get("worktree") or "")
    branch = _str(ctx.inputs.get("branch") or "")
    max_inline_lines = ctx.inputs.get("max_inline_lines")
    if max_inline_lines == None:
        max_inline_lines = 180

    decomp_path = goal_dir + "/decomposition.yaml"
    if not ctx.fs.exists(decomp_path):
        fail("gs_worker_brief: no decomposition.yaml at " + decomp_path)
    doc = yaml.decode(ctx.fs.read(decomp_path))
    changes = doc.get("changes") or []

    change = None
    for c in changes:
        if str(c["id"]) == change_id:
            change = c
    if change == None:
        fail("gs_worker_brief: change_id '" + change_id + "' not in decomposition")

    o = []
    o.append("# WORKER BRIEF — change " + change_id + ": " + _str(change.get("title")))
    if worktree != "":
        br = branch if branch != "" else "stabilize/gu-<id>"
        o.append("")
        o.append(
            "**Work ONLY in worktree** `" + worktree + "` (branch `" + br + "`). All edits + git commits there. " +
            "Do NOT rebase/merge/integrate to the base branch — the orchestrator does that."
        )
    o.append("")
    o += worker_brief_sections(ctx, change, repo, max_inline_lines)

    o.append("")
    o.append("## Protocol")
    o.append(
        "- Implement to satisfy the gate + acceptance. NO real LLM in tests (mock/cassette/flow only). " +
        "Stay strictly within the in-scope files.\n" +
        "- Commit on your branch (one or a few clean commits). Do NOT integrate.\n" +
        "- Report ONLY, concise: `change_id`, `branch`, gate `PASS`/`FAIL`/`ALREADY-SATISFIED` (+ the exact " +
        "command you ran), a 1-2 line summary, and any blocker. NO diffs, NO long logs."
    )

    return {"brief": "\n".join(o) + "\n"}
