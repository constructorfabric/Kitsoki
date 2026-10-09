#!/usr/bin/env python3
"""Deterministic redactor for session-mining.

Jobs:
  (default)   read text on stdin, write redacted text on stdout, print a
              per-category redaction tally to stderr (a safety signal — you can
              see what was scrubbed). Used on the distilled TRACE (keeps path
              tails so the extractor can still see structure).
  --report    read a report JSON on stdin, GENERICIZE its free-text fields
              (example_signatures + decision_points) and write clean JSON on
              stdout. This is the share-boundary scrub: it does NOT trust the
              extractor to have genericized — it collapses path tails to <path>,
              filenames to <file>, and (with --names) known project words to
              <name>. Run this before --scan.
  --scenario  read a `kind: conversation` scenario IR document (schema/
              scenario_ir.schema.json) on stdin, redact + genericize its
              free-text fields (goal, turns[].text, turns[].corrective_ops[],
              turns[].followup_text_head, expected_effects[]) and write clean
              JSON to stdout. THIS IS A GATE, not just a scrub: it re-runs the
              HIGH-RISK --scan check over the fully-redacted document and
              EXITS 1 (emitting nothing on stdout) if any secret-shaped content
              survives — so a caller can safely treat "exit 0" as "safe to
              write to a committable path" and must route "exit 1" to a
              gitignored local dir instead. See docs/proposals/
              scenario-foundry.md task 2.3 (the mined-IR redaction gate).
  --scan      read text on stdin, exit 1 (and list hits on stderr) if any
              HIGH-RISK pattern survives. The share-gate: run on the final report.

Design stance: redaction of free-form prose is never fully reliable, so the
shareable report should carry *genericized signatures*, not verbatim quotes.
Trace redaction keeps useful path tails (private tier); the --report scrub strips
them (shareable tier), so usefulness and safety don't fight.

Residual risk: secrets with no recognizable shape, and proprietary identifiers
that look like ordinary words AND aren't in --names. The --report path collapse
removes most structural leakage; --names is the last-mile control for bare
product/codenames (e.g. an app name used as a CLI subcommand).
"""
import json
import re
import sys

# (label, compiled regex, replacement). Order matters: most specific first.
# HIGH-RISK entries (is_secret=True) also fail the --scan gate.
RULES = [
    # ---- credentials / secrets (HIGH RISK) ----
    ("PRIVATE_KEY", re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----", re.S), "<PRIVATE_KEY>", True),
    ("AWS_KEY",     re.compile(r"\bAKIA[0-9A-Z]{16}\b"), "<AWS_KEY>", True),
    ("GH_TOKEN",    re.compile(r"\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b"), "<GH_TOKEN>", True),
    ("SLACK_TOKEN", re.compile(r"\bxox[baprs]-[A-Za-z0-9-]{8,}\b"), "<SLACK_TOKEN>", True),
    ("JWT",         re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b"), "<JWT>", True),
    ("SECRET_KV",   re.compile(r"(?i)\b(api[_-]?key|secret|token|password|passwd|pwd|access[_-]?key|client[_-]?secret|authorization|bearer)\b(\s*[:=]\s*|\s+)[\"']?[A-Za-z0-9._\-/+]{6,}[\"']?"), r"\1=<REDACTED>", True),
    ("LONG_HEX",    re.compile(r"\b[0-9a-fA-F]{40,}\b"), "<HEX>", True),
    ("LONG_B64",    re.compile(r"\b[A-Za-z0-9+/]{60,}={0,2}\b"), "<B64>", True),
    # ---- PII / identifying (not secret, but de-anonymizing) ----
    ("EMAIL",       re.compile(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b"), "<EMAIL>", False),
    ("URL",         re.compile(r"\bhttps?://[^\s)'\"]+"), "<URL>", False),
    ("IP",          re.compile(r"\b\d{1,3}(?:\.\d{1,3}){3}\b"), "<IP>", False),
    ("TICKET",      re.compile(r"\b[A-Z]{2,10}-\d{2,}\b"), "<TICKET>", False),
    # home dir -> ~  (strips the OS username)
    ("HOME",        re.compile(r"/(?:home|Users)/[^/\s:'\"]+"), "~", False),
    ("WINHOME",     re.compile(r"[A-Za-z]:\\\\Users\\\\[^\\\s]+"), "~", False),
    # repo name right after a common workspace root -> <repo>, keep the useful tail.
    # `dev` is a common repo parent (~/dev/myrepo) but also the system /dev tree; the
    # lookahead keeps shell device idioms (/dev/null, 2>/dev/stderr, /dev/fd/…) intact.
    ("REPO",        re.compile(r"(~?/(?:code|src|git|repos|projects|dev|work)/)(?!(?:null|zero|random|urandom|full|tty|std(?:in|out|err)|fd)(?![A-Za-z0-9_-]))[^/\s:'\"]+"), r"\1<repo>", False),
]


def redact(text):
    counts = {}
    for label, rx, repl, _ in RULES:
        text, n = rx.subn(repl, text)
        if n:
            counts[label] = counts.get(label, 0) + n
    return text, counts


# --- report-only genericization (the share-boundary scrub) -------------------
# A path rooted at ~, a redacted <repo>, or a temp dir keeps real sub-dirs/
# codenames in its tail (kept on purpose in the trace; temp paths routinely embed
# project names like /tmp/<proj>-trace.log). On the SHARE boundary, collapse the
# whole rooted path — including any <repo> segment — to a single <path>.
_PATH_TAIL = re.compile(r"(?:~|<repo>|/tmp|/var/tmp|/var/folders|/private/tmp)(?:/(?:<repo>|<[\w-]+>|[\w.+-]+))+")
# A bare filename leaks the real module/file name; placeholders like <file> have
# no extension char run before the dot, so they're untouched.
_FILENAME = re.compile(r"\b[\w-]+\.(?:js|ts|tsx|jsx|go|py|rb|java|json|ya?ml|md|html|css|scss|sh|bash|txt|toml|cfg|ini|sql|rs|c|cpp|h)\b")


def genericize(text, names_rx=None):
    """Strip structural identifiers from a shareable free-text string while
    keeping the tool-call SHAPE. Order: names denylist, filenames, path tails."""
    if names_rx is not None:
        text = names_rx.sub("<name>", text)
    text = _FILENAME.sub("<file>", text)
    text = _PATH_TAIL.sub("<path>", text)
    return text


def build_names_rx(path):
    if not path:
        return None
    words = [w.strip() for w in open(path).read().split() if w.strip()]
    if not words:
        return None
    # whole-word, case-insensitive, longest-first so substrings don't pre-empt
    pat = "|".join(re.escape(w) for w in sorted(set(words), key=len, reverse=True))
    return re.compile(rf"(?i)\b(?:{pat})\b")


def clean_report(obj, names_rx=None):
    """Genericize the free-text fields of a report (or aggregate) in place.
    Returns the count of strings changed."""
    changed = 0

    def scrub_list(lst):
        nonlocal changed
        for i, s in enumerate(lst):
            if isinstance(s, str):
                # secrets/home/url first, then structural genericization
                red, _ = redact(s)
                new = genericize(red, names_rx)
                if new != s:
                    lst[i] = new
                    changed += 1

    def walk(node):
        if isinstance(node, dict):
            for k, v in node.items():
                if k in ("example_signatures", "decision_points") and isinstance(v, list):
                    scrub_list(v)
                else:
                    walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)

    walk(obj)
    return changed


_SCENARIO_TEXT_FIELDS = ("goal",)
_SCENARIO_TURN_TEXT_FIELDS = ("text", "followup_text_head")


def clean_scenario(obj, names_rx=None):
    """Genericize the free-text fields of a `kind: conversation` scenario IR
    document (schema/scenario_ir.schema.json) IN PLACE. Same two-pass scrub as
    clean_report (secrets/home/url first, then structural genericization), but
    with an allowlist scoped to the IR's actual free-text shape instead of
    reusing clean_report's report-only field names.

    Returns the count of strings changed. Does NOT itself gate on --scan
    (main() does that) — callers that want the redact.py fixture without the
    subprocess CLI can call this directly and run scan() themselves.
    """
    changed = 0

    def scrub_str(s):
        nonlocal changed
        red, _ = redact(s)
        new = genericize(red, names_rx)
        if new != s:
            changed += 1
        return new

    def scrub_list(lst):
        for i, s in enumerate(lst):
            if isinstance(s, str):
                lst[i] = scrub_str(s)

    for field in _SCENARIO_TEXT_FIELDS:
        if isinstance(obj.get(field), str):
            obj[field] = scrub_str(obj[field])

    for turn in obj.get("turns", []) or []:
        if not isinstance(turn, dict):
            continue
        for field in _SCENARIO_TURN_TEXT_FIELDS:
            if isinstance(turn.get(field), str):
                turn[field] = scrub_str(turn[field])
        if isinstance(turn.get("corrective_ops"), list):
            scrub_list(turn["corrective_ops"])

    if isinstance(obj.get("expected_effects"), list):
        scrub_list(obj["expected_effects"])

    return changed


def scan(text):
    hits = {}
    for label, rx, _, is_secret in RULES:
        if not is_secret:
            continue
        found = rx.findall(text)
        if found:
            hits[label] = len(found)
    return hits


def main():
    argv = sys.argv[1:]
    if "--report" in argv:
        names_rx = None
        if "--names" in argv:
            names_rx = build_names_rx(argv[argv.index("--names") + 1])
        obj = json.load(sys.stdin)
        changed = clean_report(obj, names_rx)
        json.dump(obj, sys.stdout, indent=2)
        sys.stdout.write("\n")
        sys.stderr.write(f"report scrub: genericized {changed} free-text field(s)\n")
        return
    if "--scenario" in argv:
        names_rx = None
        if "--names" in argv:
            names_rx = build_names_rx(argv[argv.index("--names") + 1])
        obj = json.load(sys.stdin)
        changed = clean_scenario(obj, names_rx)
        serialized = json.dumps(obj, indent=2)
        hits = scan(serialized)
        if hits:
            sys.stderr.write(
                "SCENARIO REDACTION GATE FAIL — secret-shaped content survives "
                "genericization (this document must NOT be written to a "
                "committable path; keep it in the gitignored local dir):\n")
            for k, v in sorted(hits.items()):
                sys.stderr.write(f"  {k}: {v}\n")
            sys.exit(1)
        sys.stdout.write(serialized)
        sys.stdout.write("\n")
        sys.stderr.write(
            f"scenario redaction gate PASS: genericized {changed} free-text "
            f"field(s), no secret-shaped content survives\n")
        return
    data = sys.stdin.read()
    if "--scan" in argv:
        hits = scan(data)
        if hits:
            sys.stderr.write("SHARE-GATE FAIL — secret-shaped content survives:\n")
            for k, v in sorted(hits.items()):
                sys.stderr.write(f"  {k}: {v}\n")
            sys.exit(1)
        sys.stderr.write("share-gate OK — no secret-shaped content found\n")
        sys.exit(0)
    out, counts = redact(data)
    sys.stdout.write(out)
    if counts:
        sys.stderr.write("redactions: " + ", ".join(f"{k}={v}" for k, v in sorted(counts.items())) + "\n")


if __name__ == "__main__":
    main()
