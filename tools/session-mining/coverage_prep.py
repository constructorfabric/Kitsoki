#!/usr/bin/env python3
"""coverage_prep.py — mechanical data-prep for STORY COVERAGE MINING (no verdicts).

Takes a job dir (emit.py output: intents.json + analysis.json) and a story
*mining profile* (e.g. stories/git-ops/mining.profile.yaml), and produces the
inputs a human/LLM needs to fill the coverage worksheet:

  intents.git.json  scope-filtered intents (profile.scope.action_tags), each
                    joined to its analysis recipe — actions, the Phase-1
                    `outcome` of each action, and the `satisfaction` flag.
  coverage.md       a worksheet SKELETON: a per-intent table (with a BLANK
                    Verdict column the human/LLM fills) plus an arg-aware,
                    frequency-ranked command-shape table for prioritisation.

What it deliberately does NOT do — assign verdicts. CONFORMS / DIVERGES /
FIXTURE-GAP / COVERAGE-GAP / OUT-OF-SCOPE require reading the room bash against
the recovered outcome, which is the irreducibly human/LLM map step (see
docs/stories/story-coverage-mining.md). This tool only PREPARES data:

  * scope-filter to the profile's action tags,
  * arg-aware command-shape dedup (the signatures are arg-genericized, so this
    fixes tag_score.py's tool-sequence-only clusters where every git intent
    collapses into one `Bash>Bash` bucket),
  * frequency ranking,
  * candidate-room join (profile.owns) — a STARTING POINT, not an assertion,
  * Phase-1 outcome + satisfaction inlining,
  * a best-effort non_goal HINT from scanning the verbatim user_text.

Stdlib only. Deterministic. No LLM.
"""
import argparse
import json
import os
import sys


# ---- minimal YAML-subset reader for the profile (stdlib only) ---------------
# Handles exactly the profile shape: indent-0/2 nested maps, scalar values, and
# flow-style lists `[a, "b c", ...]`. Inline `#` comments on scalar values are
# stripped. No block lists, no multi-line — the profile is authored to this subset.

def _unquote(s):
    s = s.strip()
    if len(s) >= 2 and s[0] in "\"'" and s[-1] == s[0]:
        return s[1:-1]
    return s


def _split_flow(inner):
    """Split a flow-list body on top-level commas, honoring single/double quotes
    (so `"git merge, squash"` stays one element). No nesting/escapes — the profile
    subset doesn't need them."""
    parts, buf, quote = [], [], None
    for ch in inner:
        if quote:
            buf.append(ch)
            if ch == quote:
                quote = None
        elif ch in "\"'":
            quote = ch
            buf.append(ch)
        elif ch == ",":
            parts.append("".join(buf))
            buf = []
        else:
            buf.append(ch)
    parts.append("".join(buf))
    return [p for p in parts if p.strip()]


def _parse_value(val):
    val = val.strip()
    if val.startswith("["):
        inner = val[1:val.rindex("]")] if "]" in val else val[1:]
        return [_unquote(p) for p in _split_flow(inner)]
    # scalar: strip an inline comment only when the value is not quoted
    if not (val.startswith('"') or val.startswith("'")):
        val = val.split("#", 1)[0].strip()
    return _unquote(val)


def load_profile(path):
    """Parse the mining.profile.yaml subset: indent-nested maps, scalar values,
    and flow-style lists `[a, "b c", ...]`. This is a SUBSET, not full YAML — it
    fails loud on the shapes it can't represent (block lists, tab indentation)
    rather than silently producing garbage."""
    root = {}
    stack = [(-1, root)]  # (indent, container-dict)
    with open(path) as fh:
        for n, raw in enumerate(fh, 1):
            line = raw.rstrip("\n")
            if not line.strip() or line.lstrip().startswith("#"):
                continue
            ws = line[:len(line) - len(line.lstrip())]
            if "\t" in ws:
                raise ValueError("%s:%d uses tab indentation; the profile parser "
                                 "requires spaces (YAML forbids tabs)" % (path, n))
            if line.lstrip().startswith("- "):
                raise ValueError("%s:%d uses a block-style list (`- item`); this "
                                 "profile parser only supports flow lists "
                                 "(`key: [a, b, c]`)" % (path, n))
            indent = len(ws)
            while len(stack) > 1 and indent <= stack[-1][0]:
                stack.pop()
            parent = stack[-1][1]
            key, _, val = line.strip().partition(":")
            key = key.strip()
            if val.strip() == "":
                d = {}
                parent[key] = d
                stack.append((indent, d))
            else:
                parent[key] = _parse_value(val)
    return root


# ---- joining + shaping ------------------------------------------------------

def short_sig(sig):
    """Compact a signature for a table cell: drop a leading 'git '."""
    sig = (sig or "").strip()
    return sig[4:] if sig.startswith("git ") else sig


def intent_shape(actions):
    """Arg-aware dedup key: the ordered genericized signatures. Unlike
    tag_score.py's tool-only cluster key (Bash>Bash for every git intent), the
    signatures carry the command, so `rebase <branch>` and `commit -m <msg>`
    land in different buckets."""
    return " → ".join(short_sig(a.get("signature", "")) for a in actions) or "(no actions)"


def outcome_cell(actions):
    """Per-action outcome summary for the worksheet. ✓ ok, ✗ error (+head)."""
    parts = []
    for a in actions:
        sig = short_sig(a.get("signature", ""))
        oc = a.get("outcome")
        if not oc:
            parts.append(sig + " ?")
        elif oc.get("is_error"):
            head = (oc.get("stderr_head") or oc.get("stdout_head") or "").strip().splitlines()
            head = head[0][:32] if head else ""
            parts.append("%s ✗ %s" % (sig, head))
        else:
            parts.append(sig + " ✓")
    return " · ".join(parts) or "—"


def satisfaction_cell(sat):
    if not sat or not sat.get("corrected"):
        return "—"
    ops = ", ".join(short_sig(o) for o in (sat.get("corrective_ops") or []))
    return "⚠ corrected: %s" % (ops or "(yes)")


def candidate_rooms(action_tags, owns):
    seen, rooms = set(), []
    for t in action_tags:
        for r in owns.get(t, []):
            if r not in seen:
                seen.add(r)
                rooms.append(r)
    return rooms


def non_goal_hint(user_text, markers):
    lt = (user_text or "").lower()
    hits = [m for m in markers if m.lower() in lt]
    return hits


def md_escape(s):
    return (s or "").replace("|", "\\|").replace("\n", " ").strip()


def main(argv=None):
    ap = argparse.ArgumentParser(description="Mechanical data-prep for story coverage mining (no verdicts).")
    ap.add_argument("--job-dir", required=True, help="emit.py output dir (intents.json + analysis.json)")
    ap.add_argument("--profile", required=True, help="story mining.profile.yaml")
    ap.add_argument("--out-dir", default=None, help="where to write intents.git.json + coverage.md (default: --job-dir)")
    args = ap.parse_args(argv)

    out_dir = args.out_dir or args.job_dir
    prof = load_profile(args.profile)
    scope = prof.get("scope", {})
    action_tags = set(scope.get("action_tags", []))
    owns = prof.get("owns", {})
    markers = prof.get("non_goal_markers", [])
    non_goals = prof.get("non_goals", [])  # the declared v1 non-goal set (surfaced for the map)
    story = prof.get("story", "?")

    intents = json.load(open(os.path.join(args.job_dir, "intents.json"))).get("intents", [])
    analysis = json.load(open(os.path.join(args.job_dir, "analysis.json"))).get("instances", [])
    by_id = {i["instance_id"]: i for i in analysis}

    rows = []
    out_of_scope = 0
    for it in intents:
        tags = it.get("tags", {})
        a_tags = tags.get("action", [])
        if not (set(a_tags) & action_tags):
            out_of_scope += 1
            continue
        inst = by_id.get(it["instance_id"], {})
        actions = inst.get("actions", [])
        rows.append({
            "instance_id": it["instance_id"],
            "user_text": it.get("user_text", ""),
            "session": it.get("session"),
            "span": it.get("span"),
            "tags": tags,
            "action_tags": a_tags,
            "determinism": inst.get("determinism"),
            "shape": intent_shape(actions),
            "actions": [{"signature": a.get("signature"), "tool": a.get("tool"),
                          "parameters": a.get("parameters"), "outcome": a.get("outcome")}
                         for a in actions],
            "satisfaction": inst.get("satisfaction"),
            "candidate_rooms": candidate_rooms(a_tags, owns),
            "non_goal_hint": non_goal_hint(it.get("user_text", ""), markers),
        })

    # arg-aware frequency groups (dedup by action-tagset + shape)
    groups = {}
    for r in rows:
        key = (",".join(sorted(r["action_tags"])), r["shape"])
        groups.setdefault(key, []).append(r["instance_id"])
    group_list = sorted(
        ({"action": k[0], "shape": k[1], "freq": len(v), "instances": v}
         for k, v in groups.items()),
        key=lambda g: (-g["freq"], g["shape"]))

    # --- intents.git.json ---
    git_report = {
        "schema_version": "1.0",
        "story": story,
        "job": os.path.basename(os.path.normpath(args.job_dir)),
        "non_goals": non_goals,
        "total_in_scope": len(rows),
        "total_out_of_scope_by_tag": out_of_scope,
        "deduped_shapes": len(group_list),
        "groups": group_list,
        "intents": rows,
    }
    git_path = os.path.join(out_dir, "intents.git.json")
    with open(git_path, "w") as fh:
        json.dump(git_report, fh, indent=2)
        fh.write("\n")

    # --- coverage.md skeleton ---
    L = []
    L.append("<!-- GENERATED SKELETON by coverage_prep.py — fill the VERDICT + NOTE columns by hand. -->")
    L.append("<!-- This is the mechanical data-prep tier. Verdicts require reading room bash vs the")
    L.append("     recovered outcome — the human/LLM map step. See docs/stories/story-coverage-mining.md. -->")
    L.append("")
    L.append("# Coverage worksheet — `%s`" % story)
    L.append("")
    L.append("_%d in-scope intents · %d deduped command-shapes · %d out-of-scope by tag._"
             % (len(rows), len(group_list), out_of_scope))
    L.append("")
    L.append("Verdicts (fill the **Verdict** column): **CONFORMS** · **DIVERGES** · "
             "**FIXTURE-GAP** · **COVERAGE-GAP** · **OUT-OF-SCOPE**. The non_goal hint and the "
             "`satisfaction` flag route attention; they are not verdicts.")
    L.append("")
    L.append("| # | intent (user_text) | command shape | real outcome (Phase 1) | satisfaction | candidate rooms | non_goal? | Verdict | Note |")
    L.append("|--:|---|---|---|---|---|---|---|---|")
    for n, r in enumerate(rows, 1):
        L.append("| %d | %s | `%s` | %s | %s | %s | %s |  |  |" % (
            n,
            md_escape(r["user_text"][:70]),
            md_escape(r["shape"]),
            md_escape(outcome_cell(r["actions"])),
            md_escape(satisfaction_cell(r["satisfaction"])),
            ", ".join(r["candidate_rooms"]) or "—",
            ("⚑ " + ", ".join(r["non_goal_hint"])) if r["non_goal_hint"] else "—",
        ))
    L.append("")
    L.append("Summary line (fill after verdicts): "
             "`N deduped · X CONFORMS · D DIVERGES · C corrected · Y FIXTURE-GAP · Z COVERAGE-GAP · O out-of-scope`.")
    L.append("")
    L.append("## Command-shape frequency (arg-aware dedup — rank gaps by this)")
    L.append("")
    L.append("| freq | action tag(s) | command shape | instances |")
    L.append("|--:|---|---|---|")
    for g in group_list:
        L.append("| %d | %s | `%s` | %s |" % (
            g["freq"], md_escape(g["action"]), md_escape(g["shape"]),
            ", ".join(g["instances"])))
    L.append("")
    cov_path = os.path.join(out_dir, "coverage.md")
    with open(cov_path, "w") as fh:
        fh.write("\n".join(L) + "\n")

    print("coverage_prep: %d in-scope, %d deduped shapes, %d out-of-scope -> %s, %s"
          % (len(rows), len(group_list), out_of_scope, git_path, cov_path), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
