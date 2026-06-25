#!/usr/bin/env python3
"""Deterministic marathon status table.

Reads cases.yaml + attempts.jsonl (append-only) and regenerates STATUS.md.
No LLM calls, no network. Re-runnable: same inputs -> byte-identical output.

attempts.jsonl record shape (one JSON object per line):
  {"bug","candidate","treatment","baseline_red","drive_exit","verify",
   "tokens","cost_usd","wall_s","trace","notes"}
  verify  : "PASS" | "FAIL" | "PENDING"
The LATEST record per (bug,candidate,treatment) wins; ordering is by file order.
"""
import json, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))

def load_cases():
    # tiny hand parser (no yaml dep): collect id/title/package/fix_sha/baseline_sha
    cases, cur = [], None
    for line in open(os.path.join(HERE, "cases.yaml")):
        s = line.strip()
        if s.startswith("- id:"):
            if cur: cases.append(cur)
            cur = {"id": s.split("- id:")[1].strip()}
        elif cur is not None and ":" in s and not s.startswith("#"):
            k = s.split(":", 1)[0].strip()
            v = s.split(":", 1)[1].strip().strip('"')
            if k in ("title", "package", "fix_sha", "baseline_sha", "confirmed_red"):
                cur[k] = v
    if cur: cases.append(cur)
    return cases

def load_attempts():
    p = os.path.join(HERE, "attempts.jsonl")
    latest = {}
    if os.path.exists(p):
        for line in open(p):
            line = line.strip()
            if not line:
                continue
            r = json.loads(line)
            latest[(r["bug"], r.get("candidate", ""), r.get("treatment", "kitsoki"))] = r
    return latest

def main():
    cases = load_cases()
    latest = load_attempts()
    shipped = sum(1 for r in latest.values()
                  if r.get("treatment") == "kitsoki" and r.get("verify") == "PASS")
    rows = []
    for c in cases:
        bug = c["id"]
        r = latest.get((bug, "", "kitsoki")) or next(
            (v for k, v in latest.items() if k[0] == bug and k[2] == "kitsoki"), None)
        red = c.get("confirmed_red", "?")
        if r:
            verify = r.get("verify", "PENDING")
            cand = r.get("candidate", "")
            tok = r.get("tokens", "")
            cost = r.get("cost_usd", "")
            wall = r.get("wall_s", "")
            exitr = r.get("drive_exit", "")
            notes = r.get("notes", "")
        else:
            verify = cand = tok = cost = wall = exitr = notes = ""
        rows.append((bug, c.get("title", "")[:54], c.get("fix_sha", ""), red,
                     cand, exitr, verify, tok, cost, wall, notes[:40]))
    out = []
    out.append("# gears-rust bugfix marathon — status\n")
    out.append(f"**Shipped (independent-verify PASS): {shipped} / 10**\n")
    out.append("Generated deterministically by `gen_table.py` from `cases.yaml` + `attempts.jsonl`.\n")
    hdr = ["bug", "title", "fix_sha", "RED?", "cand", "exit", "verify",
           "tokens", "cost$", "wall_s", "notes"]
    out.append("| " + " | ".join(hdr) + " |")
    out.append("|" + "|".join(["---"] * len(hdr)) + "|")
    for row in rows:
        out.append("| " + " | ".join(str(x) for x in row) + " |")
    open(os.path.join(HERE, "STATUS.md"), "w").write("\n".join(out) + "\n")
    print("\n".join(out))

if __name__ == "__main__":
    main()
