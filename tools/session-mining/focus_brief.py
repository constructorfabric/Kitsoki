#!/usr/bin/env python3
"""focus_brief.py — render a focused idea-mining synthesis into a ranked brief.

Input (stdin or a file path): the JSON produced by the synthesis step of the
`session-idea-mining` workflow. Accepts either shape:

  * the raw synthesis object: {"headline": "...", "themes": [...]}
  * a wrapped workflow/task result: {"result": {"headline": ..., "themes": [...]}}

Each theme is expected to carry: theme, priority (now|soon|later), rationale,
categories[], target, summary, supporting_ideas[], session_count, sessions[].
Missing fields degrade gracefully.

Output: a Markdown brief grouped by priority (now -> soon -> later), themes within
a tier sorted by session_count desc. This is the deterministic render step — the
clustering/ranking judgment happens in the workflow's synthesis agent; this just
formats it so two runs of the same synthesis produce byte-identical briefs.

stdlib only; python3.9+.
"""
import argparse
import html
import json
import os
import subprocess
import sys
from pathlib import Path

PRIORITY_ORDER = {"now": 0, "soon": 1, "later": 2}
PRIORITY_LABEL = {
    "now": "NOW — acute / highest leverage",
    "soon": "SOON — strong signal, build next",
    "later": "LATER — real but lower urgency",
}
ROOT = Path(__file__).resolve().parents[2]


def load(src):
    raw = sys.stdin.read() if src == "-" else open(src).read()
    obj = json.loads(raw)
    if isinstance(obj, dict) and "result" in obj and isinstance(obj["result"], dict):
        obj = obj["result"]
    return obj


def clean(s):
    # synthesis agents sometimes emit HTML-escaped <, >, & in prose
    return html.unescape(str(s)) if s is not None else ""


def sorted_themes(obj):
    themes = obj.get("themes", []) if isinstance(obj, dict) else []
    return sorted(
        themes,
        key=lambda t: (PRIORITY_ORDER.get(t.get("priority", "later"), 2),
                       -int(t.get("session_count", 0) or 0)),
    )


def summarize(obj, title, subtitle="", source="", markdown_path="", summary_path=""):
    themes = []
    for t in sorted_themes(obj):
        themes.append({
            "theme": clean(t.get("theme", "(untitled)")),
            "priority": clean(t.get("priority", "later")).lower() or "later",
            "rationale": clean(t.get("rationale", "")),
            "categories": [clean(c) for c in (t.get("categories", []) or [])],
            "target": clean(t.get("target", "")),
            "summary": clean(t.get("summary", "")),
            "supporting_ideas": [clean(s) for s in (t.get("supporting_ideas", []) or [])],
            "session_count": int(t.get("session_count", 0) or 0),
            "sessions": [str(s) for s in (t.get("sessions", []) or [])],
        })
    return {
        "_source": source,
        "title": title,
        "subtitle": subtitle,
        "headline": clean(obj.get("headline", "")) if isinstance(obj, dict) else "",
        "markdown_path": markdown_path,
        "summary_path": summary_path,
        "themes": themes,
    }


def render_summary(summary):
    out = []
    w = out.append
    w("# %s\n" % summary.get("title", "Ideas mined from chat history"))
    if summary.get("subtitle"):
        w("> %s\n" % summary["subtitle"])
    if summary.get("headline"):
        w("## Headline\n")
        w(summary["headline"] + "\n")

    n = 0
    cur = None
    for t in summary.get("themes", []):
        pri = t.get("priority", "later")
        if pri != cur:
            cur = pri
            w("\n---\n")
            w("\n## %s\n" % PRIORITY_LABEL.get(pri, pri.upper()))
        n += 1
        cats = "/".join(t.get("categories", []) or [])
        sc = int(t.get("session_count", 0) or 0)
        w("### %d. %s" % (n, t.get("theme", "(untitled)")))
        meta_bits = [b for b in (t.get("target", ""), cats,
                                 "%d session%s" % (sc, "" if sc == 1 else "s")) if b]
        w("*%s*\n" % " · ".join(meta_bits))
        if t.get("summary"):
            w(t["summary"] + "\n")
        if t.get("rationale"):
            w("**Why this rank:** %s\n" % t["rationale"])
        ideas = t.get("supporting_ideas", []) or []
        if ideas:
            w("**Distinct sub-points:**")
            for s in ideas:
                w("- %s" % s)
            w("")
        sess = t.get("sessions", []) or []
        if sess:
            w("<sub>sessions: %s</sub>\n" %
              ", ".join("`%s`" % str(x)[:8] for x in sess))

    return "\n".join(out) + "\n"


def write(path, content):
    directory = os.path.dirname(os.path.abspath(path))
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(content)


def write_slidey_spec(path, summary):
    builder = ROOT / "tools" / "report-deck" / "deterministic_deck.py"
    subprocess.run(
        [
            sys.executable,
            str(builder),
            "--kind",
            "session-idea-mining",
            "--input-json",
            json.dumps(summary, sort_keys=True),
            "--out",
            path,
        ],
        cwd=ROOT,
        check=True,
        stdout=subprocess.DEVNULL,
    )


def main():
    ap = argparse.ArgumentParser(
        description="Render a focused idea-mining synthesis JSON into a brief.")
    ap.add_argument("input", nargs="?", default="-",
                    help="synthesis JSON file (default: stdin)")
    ap.add_argument("--title", default="Ideas mined from chat history",
                    help="brief H1 title")
    ap.add_argument("--subtitle", default="",
                    help="optional one-line provenance note under the title")
    ap.add_argument("--markdown", help="write Markdown brief here instead of stdout")
    ap.add_argument("--summary", help="write machine-readable summary JSON")
    ap.add_argument("--slidey-spec", help="write deterministic Slidey JSON deck spec")
    args = ap.parse_args()

    obj = load(args.input)
    summary = summarize(
        obj,
        args.title,
        subtitle=args.subtitle,
        source="" if args.input == "-" else args.input,
        markdown_path=args.markdown or "",
        summary_path=args.summary or "",
    )
    md = render_summary(summary)
    if args.markdown:
        write(args.markdown, md)
    else:
        sys.stdout.write(md)
    if args.summary:
        write(args.summary, json.dumps(summary, indent=2, sort_keys=True) + "\n")
    if args.slidey_spec:
        write_slidey_spec(args.slidey_spec, summary)
    return 0


if __name__ == "__main__":
    sys.exit(main())
