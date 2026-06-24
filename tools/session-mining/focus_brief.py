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
import sys

PRIORITY_ORDER = {"now": 0, "soon": 1, "later": 2}
PRIORITY_LABEL = {
    "now": "NOW — acute / highest leverage",
    "soon": "SOON — strong signal, build next",
    "later": "LATER — real but lower urgency",
}


def load(src):
    raw = sys.stdin.read() if src == "-" else open(src).read()
    obj = json.loads(raw)
    if isinstance(obj, dict) and "result" in obj and isinstance(obj["result"], dict):
        obj = obj["result"]
    return obj


def clean(s):
    # synthesis agents sometimes emit HTML-escaped <, >, & in prose
    return html.unescape(str(s)) if s is not None else ""


def main():
    ap = argparse.ArgumentParser(
        description="Render a focused idea-mining synthesis JSON into a brief.")
    ap.add_argument("input", nargs="?", default="-",
                    help="synthesis JSON file (default: stdin)")
    ap.add_argument("--title", default="Ideas mined from chat history",
                    help="brief H1 title")
    ap.add_argument("--subtitle", default="",
                    help="optional one-line provenance note under the title")
    args = ap.parse_args()

    obj = load(args.input)
    themes = obj.get("themes", []) if isinstance(obj, dict) else []
    headline = clean(obj.get("headline", "")) if isinstance(obj, dict) else ""

    out = []
    w = out.append
    w("# %s\n" % args.title)
    if args.subtitle:
        w("> %s\n" % args.subtitle)
    if headline:
        w("## Headline\n")
        w(headline + "\n")

    themes = sorted(
        themes,
        key=lambda t: (PRIORITY_ORDER.get(t.get("priority", "later"), 2),
                       -int(t.get("session_count", 0) or 0)),
    )

    n = 0
    cur = None
    for t in themes:
        pri = t.get("priority", "later")
        if pri != cur:
            cur = pri
            w("\n---\n")
            w("\n## %s\n" % PRIORITY_LABEL.get(pri, pri.upper()))
        n += 1
        cats = "/".join(t.get("categories", []) or [])
        sc = int(t.get("session_count", 0) or 0)
        w("### %d. %s" % (n, clean(t.get("theme", "(untitled)"))))
        meta_bits = [b for b in (clean(t.get("target", "")), cats,
                                 "%d session%s" % (sc, "" if sc == 1 else "s")) if b]
        w("*%s*\n" % " · ".join(meta_bits))
        if t.get("summary"):
            w(clean(t["summary"]) + "\n")
        if t.get("rationale"):
            w("**Why this rank:** %s\n" % clean(t["rationale"]))
        ideas = t.get("supporting_ideas", []) or []
        if ideas:
            w("**Distinct sub-points:**")
            for s in ideas:
                w("- %s" % clean(s))
            w("")
        sess = t.get("sessions", []) or []
        if sess:
            w("<sub>sessions: %s</sub>\n" %
              ", ".join("`%s`" % str(x)[:8] for x in sess))

    sys.stdout.write("\n".join(out) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
