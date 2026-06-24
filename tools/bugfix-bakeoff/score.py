#!/usr/bin/env python3
"""Score ONE bake-off grid cell into results/cells/<bug>-<candidate>-<treatment>.json.

The deterministic, offline grader for the bugfix bake-off. It NEVER calls an LLM
or a provider — every signal is read off the produced worktree (the tree the cell
candidate left behind) and the run's recorded transcript:

  OUTCOME    — run the bug's hidden `oracle_test` (the regression test the REAL fix
               added) against the candidate's worktree. The oracle is NOT in the
               candidate tree, so we copy it in from the fix commit, run it, then
               remove it so it never pollutes the tree. Also `go build ./...` (go
               bugs) and the bug's affected_test_pkgs for a no-regression suite gate.
               Mapped to outcome.quality per results/SCHEMA.md.
  COMPLIANCE — five best-effort heuristics over `git status/diff` + the transcript:
               reproduced_red, added_regression_test, suite_green, in_scope,
               stage_order. rate = mean of the five.
  METRICS    — exact tokens + cost from the transcript via the session-mining
               cost_extract module (reused, not reimplemented). Robust to a
               missing/empty transcript (everything zeroed, noted).

The oracle runner and the cost extractor are dependency-injected (see
`score_cell`) so score_test.py can stub them with zero process/network cost.

Usage:
  score.py --manifest tools/bugfix-bakeoff/bakeoff.yaml \
           --bug bug1 --candidate opus-4.8 --treatment kitsoki \
           --worktree .worktrees/bakeoff-bug1-opus-4.8-kitsoki \
           --transcript .artifacts/bugfix-bakeoff/bug1/opus-4.8-kitsoki.jsonl \
           --wall-time-s 412.0 --guidance-turns 2 \
           --out tools/bugfix-bakeoff/results/cells/bug1-opus-4.8-kitsoki.json
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys

import yaml

# Reuse the session-mining cost/pricing logic — do NOT duplicate pricing here.
_MINING = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                       "..", "session-mining")
sys.path.insert(0, os.path.abspath(_MINING))
import cost_extract  # noqa: E402

# Top-level dirs a fix for a given component is plausibly allowed to touch. Used
# by the in_scope heuristic; anything outside this (per component) is "unrelated".
SCOPE_DIRS = {
    "tui": {"internal", "tools"},
    "runtime": {"internal"},
    "web": {"internal", "tools"},
}


# --------------------------------------------------------------------------- #
# OUTCOME — the oracle/build/suite runner (injectable).
# --------------------------------------------------------------------------- #

def _run(cmd, cwd, timeout=600):
    """Run a command, returning (returncode, combined_output). rc=-1 on launch
    failure or timeout."""
    try:
        proc = subprocess.run(cmd, cwd=cwd, timeout=timeout,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              text=True)
        return proc.returncode, proc.stdout
    except (subprocess.TimeoutExpired, OSError) as exc:
        return -1, f"{type(exc).__name__}: {exc}"


def _go_test_func(oracle_path):
    """The first `func TestXxx(` name declared in a Go test file, or None."""
    try:
        with open(oracle_path) as fh:
            src = fh.read()
    except OSError:
        return None
    m = re.search(r"func\s+(Test\w+)\s*\(", src)
    return m.group(1) if m else None


def _go_pkg(oracle_test):
    """`./dir/...`-free package path for a go oracle file (its directory)."""
    return "./" + os.path.dirname(oracle_test)


class OracleRunner:
    """Runs the oracle test + build + suite against a worktree using the real
    toolchain. Stubbed in tests; all process work is confined here (DI seam).

    `run_oracle` copies the hidden oracle test in from the fix commit, runs it,
    and removes it so the candidate tree is never polluted (the contract in
    SCHEMA.md). Returns one of: pass / fail / noncompile / absent.
    """

    def __init__(self, runner=_run):
        self._run = runner

    def run_oracle(self, bug, worktree):
        kind = bug.get("oracle_kind")
        oracle = bug["oracle_test"]
        fix_sha = bug["fix_sha"]
        if kind == "go":
            return self._oracle_go(oracle, fix_sha, worktree)
        if kind == "vitest":
            return self._oracle_vitest(oracle, fix_sha, worktree)
        return "absent", f"unknown oracle_kind {kind!r}"

    def _copy_oracle(self, oracle, fix_sha, worktree):
        """git show <fix_sha>:<oracle> > <worktree>/<oracle>. Returns dest path
        or None if the blob can't be read."""
        rc, out = self._run(["git", "show", f"{fix_sha}:{oracle}"], worktree)
        if rc != 0:
            return None
        dest = os.path.join(worktree, oracle)
        os.makedirs(os.path.dirname(dest), exist_ok=True)
        with open(dest, "w") as fh:
            fh.write(out)
        return dest

    def _oracle_go(self, oracle, fix_sha, worktree):
        func = None
        dest = self._copy_oracle(oracle, fix_sha, worktree)
        if dest is None:
            return "absent", "oracle blob not found at fix commit"
        try:
            func = _go_test_func(dest)
            if not func:
                return "absent", "no Test func parsed from oracle"
            rc, out = self._run(
                ["go", "test", "-run", f"^{func}$", _go_pkg(oracle)], worktree)
        finally:
            try:
                os.remove(dest)
            except OSError:
                pass
        return _classify_go(rc, out), f"oracle {func}: rc={rc}"

    def _oracle_vitest(self, oracle, fix_sha, worktree):
        dest = self._copy_oracle(oracle, fix_sha, worktree)
        if dest is None:
            return "absent", "oracle blob not found at fix commit"
        try:
            # runstatus vitest: `pnpm --filter kitsoki-runstatus test -- <file>`.
            rc, out = self._run(
                ["pnpm", "--filter", "kitsoki-runstatus", "test", "--", oracle],
                worktree)
        finally:
            try:
                os.remove(dest)
            except OSError:
                pass
        return _classify_vitest(rc, out), f"oracle vitest: rc={rc}"

    def run_build(self, worktree):
        """`go build ./...`; returns True/False, or None (n/a)."""
        rc, _ = self._run(["go", "build", "./..."], worktree)
        return rc == 0

    def run_suite(self, pkgs, worktree, kind):
        """Affected-package suite. True iff all packages green."""
        if kind == "go":
            rc, _ = self._run(["go", "test"] + list(pkgs), worktree)
            return rc == 0
        if kind == "vitest":
            rc, _ = self._run(
                ["pnpm", "--filter", "kitsoki-runstatus", "test"], worktree)
            return rc == 0
        return None


def _classify_go(rc, out):
    """pass / fail / noncompile from a `go test` run."""
    if rc == 0:
        return "pass"
    low = (out or "").lower()
    if "build failed" in low or "cannot find" in low or "undefined:" in low \
            or "does not compile" in low or "syntax error" in low:
        return "noncompile"
    return "fail"


def _classify_vitest(rc, out):
    if rc == 0:
        return "pass"
    low = (out or "").lower()
    if "transform failed" in low or "cannot find module" in low \
            or "is not defined" in low or "syntaxerror" in low:
        return "noncompile"
    return "fail"


# --------------------------------------------------------------------------- #
# COMPLIANCE — heuristics over git + transcript.
# --------------------------------------------------------------------------- #

def _git_added_test(worktree, kind):
    """added_regression_test heuristic: did the candidate add a NEW *_test.go
    (go) or *.test.ts (vitest) file? Reads `git status --porcelain` for an
    added/untracked test file. Best-effort — a renamed or amended test slips by.
    """
    rc, out = _run(["git", "-C", worktree, "status", "--porcelain"], None)
    if rc != 0:
        return False
    suffix = "_test.go" if kind == "go" else ".test.ts"
    for line in out.splitlines():
        status, _, path = line.partition(" ")
        path = line[3:].strip()
        flag = line[:2]
        if path.endswith(suffix) and ("A" in flag or "?" in flag):
            return True
    return False


def _git_changed_top_dirs(worktree):
    """Set of top-level directories the candidate touched (per `git status`)."""
    rc, out = _run(["git", "-C", worktree, "status", "--porcelain"], None)
    dirs = set()
    if rc != 0:
        return dirs
    for line in out.splitlines():
        path = line[3:].strip()
        if "->" in path:  # rename
            path = path.split("->")[-1].strip()
        if path:
            dirs.add(path.split("/")[0])
    return dirs


def _block_text(content):
    """All human-readable text in a message content (str or block list):
    user/assistant `text`, tool_use command/file args, and tool_result output
    (where a failing-test signal lands)."""
    if isinstance(content, str):
        return content
    if not isinstance(content, list):
        return ""
    out = []
    for b in content:
        if not isinstance(b, dict):
            continue
        t = b.get("type")
        if t in ("text",):
            out.append(b.get("text", ""))
        elif t == "tool_use":
            inp = b.get("input", {}) or {}
            out.append(str(inp.get("command") or inp.get("file_path") or ""))
        elif t == "tool_result":
            c = b.get("content")
            out.append(c if isinstance(c, str) else _block_text(c))
    return " ".join(out)


def _transcript_text(transcript):
    """Lowercased concatenation of EVERY message's readable text (assistant
    reasoning, tool commands, and tool_result output), for the reproduce/
    stage-order scans. Reads the raw JSONL directly because the cost extractor
    intentionally drops assistant text + tool results. '' if unreadable."""
    if not transcript or not os.path.exists(transcript):
        return ""
    parts = []
    try:
        with open(transcript) as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except json.JSONDecodeError:
                    continue
                msg = rec.get("message") or {}
                parts.append(_block_text(msg.get("content")))
    except OSError:
        return ""
    return " ".join(parts).lower()


def _scan_reproduced_red(text):
    """reproduced_red heuristic: transcript shows the bug demonstrated RED before
    the fix — a test run that failed, or explicit reproduce language."""
    if not text:
        return False
    cues = ["--- fail", "fail:", "reproduc", "red before", "failing test",
            "confirm the bug", "demonstrate the bug"]
    return any(c in text for c in cues)


def _scan_stage_order(text):
    """stage_order heuristic: a failing/repro test appears in the transcript
    BEFORE language about implementing the fix (reproduce -> implement -> test)."""
    if not text:
        return False
    repro = -1
    for c in ("reproduc", "failing test", "--- fail", "red"):
        i = text.find(c)
        if i != -1:
            repro = i if repro == -1 else min(repro, i)
    impl = -1
    for c in ("implement the fix", "apply the fix", "now fix", "fix the bug",
              "the fix is"):
        i = text.find(c)
        if i != -1:
            impl = i if impl == -1 else min(impl, i)
    return repro != -1 and impl != -1 and repro < impl


def compute_compliance(bug, worktree, transcript, suite_pass):
    """Derive the five compliance booleans + their mean. Each is documented on
    its scanner above; all are best-effort and offline."""
    kind = bug.get("oracle_kind")
    text = _transcript_text(transcript)
    component = bug.get("component", "")
    changed = _git_changed_top_dirs(worktree)
    allowed = SCOPE_DIRS.get(component, {"internal", "tools"})
    in_scope = changed.issubset(allowed) if changed else True

    bools = {
        "reproduced_red": _scan_reproduced_red(text),
        "added_regression_test": _git_added_test(worktree, kind),
        "suite_green": bool(suite_pass),
        "in_scope": in_scope,
        "stage_order": _scan_stage_order(text),
    }
    rate = sum(1 for v in bools.values() if v) / len(bools)
    out = dict(bools)
    out["rate"] = round(rate, 4)
    return out


# --------------------------------------------------------------------------- #
# METRICS — tokens + cost via the reused extractor (injectable).
# --------------------------------------------------------------------------- #

def default_cost_fn(transcript):
    """Sum a transcript's recorded usage into the metrics shape using the
    session-mining extractor. Returns a dict (zeroed when absent/empty)."""
    zero = dict(input_tokens=0, output_tokens=0, cache_read_tokens=0,
                cache_write_tokens=0, cost_usd=0.0, cost_exact=True,
                note="no transcript")
    if not transcript or not os.path.exists(transcript):
        return zero
    try:
        sc = cost_extract.extract(transcript)
    except Exception as exc:
        z = dict(zero)
        z["note"] = f"transcript unreadable: {exc}"
        return z
    if not sc.turns:
        z = dict(zero)
        z["note"] = "transcript empty"
        return z
    return dict(
        input_tokens=sum(t.input_tokens for t in sc.turns),
        output_tokens=sum(t.output_tokens for t in sc.turns),
        cache_read_tokens=sum(t.cache_read for t in sc.turns),
        cache_write_tokens=sum(t.cache_write for t in sc.turns),
        cost_usd=round(sc.total(), 6),
        cost_exact=sc.exact(),
        note="",
    )


# --------------------------------------------------------------------------- #
# quality mapping + assembly.
# --------------------------------------------------------------------------- #

def map_quality(oracle_status, build_pass, suite_pass):
    """Per SCHEMA.md:
      solved  = oracle_pass && build_pass!=false && suite_pass
      partial = oracle_pass but a regression/build issue, OR oracle noncompile
      failed  = otherwise (oracle fail/absent)."""
    oracle_pass = oracle_status == "pass"
    if oracle_pass and build_pass is not False and suite_pass:
        return "solved"
    if oracle_pass:
        return "partial"
    if oracle_status == "noncompile":
        return "partial"
    return "failed"


def find_candidate(manifest, key):
    for c in manifest.get("candidates", []):
        if c.get("key") == key:
            return c
    raise KeyError(f"candidate {key!r} not in manifest")


def find_bug(manifest, bug_id):
    for b in manifest.get("bugs", []):
        if b.get("id") == bug_id:
            return b
    raise KeyError(f"bug {bug_id!r} not in manifest")


def score_cell(manifest, bug_id, candidate_key, treatment, worktree,
               transcript, wall_time_s, guidance_turns, *,
               oracle_runner=None, cost_fn=None, trace_found=None):
    """Score one cell into the SCHEMA cell dict. `oracle_runner` and `cost_fn`
    are injectable so tests can run with no toolchain/transcript at all."""
    oracle_runner = oracle_runner or OracleRunner()
    cost_fn = cost_fn or default_cost_fn
    bug = find_bug(manifest, bug_id)
    cand = find_candidate(manifest, candidate_key)
    kind = bug.get("oracle_kind")
    notes = []

    oracle_status, onote = oracle_runner.run_oracle(bug, worktree)
    if onote:
        notes.append(onote)
    build_pass = oracle_runner.run_build(worktree) if kind == "go" else None
    suite_pass = oracle_runner.run_suite(
        bug.get("affected_test_pkgs", []), worktree, kind)
    quality = map_quality(oracle_status, build_pass, suite_pass)
    if oracle_status == "noncompile":
        notes.append("oracle noncompiles against candidate's differing "
                     "implementation; bug plausibly fixed (partial)")

    compliance = compute_compliance(bug, worktree, transcript, suite_pass)
    metrics = cost_fn(transcript)
    if metrics.get("note"):
        notes.append(metrics["note"])

    total_tokens = (metrics["input_tokens"] + metrics["output_tokens"]
                    + metrics["cache_read_tokens"] + metrics["cache_write_tokens"])

    cell = {
        "bug": bug_id,
        "candidate": candidate_key,
        "treatment": treatment,
        "profile": cand.get("profile"),
        "model": cand.get("model"),
        "effort": cand.get("effort"),
        "provider": cand.get("provider"),
        "outcome": {
            "oracle_pass": oracle_status == "pass",
            "oracle_status": oracle_status,
            "build_pass": build_pass,
            "suite_pass": bool(suite_pass) if suite_pass is not None else None,
            "quality": quality,
        },
        "compliance": compliance,
        "metrics": {
            "input_tokens": metrics["input_tokens"],
            "output_tokens": metrics["output_tokens"],
            "cache_read_tokens": metrics["cache_read_tokens"],
            "cache_write_tokens": metrics["cache_write_tokens"],
            "total_tokens": total_tokens,
            "cost_usd": metrics["cost_usd"],
            "cost_exact": metrics["cost_exact"],
            "wall_time_s": float(wall_time_s),
            "guidance_turns": int(guidance_turns),
        },
        "transcript_path": transcript or "",
        "trace_found": bool(trace_found) if trace_found is not None
        else (treatment == "kitsoki" and bool(transcript)
              and os.path.exists(transcript or "")),
        "notes": "; ".join(n for n in notes if n),
    }
    return cell


def load_manifest(path):
    with open(path) as fh:
        return yaml.safe_load(fh)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--manifest", default=os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "bakeoff.yaml"))
    ap.add_argument("--bug", required=True)
    ap.add_argument("--candidate", required=True)
    ap.add_argument("--treatment", required=True, choices=["kitsoki", "single"])
    ap.add_argument("--worktree", required=True)
    ap.add_argument("--transcript", default="")
    ap.add_argument("--wall-time-s", type=float, default=0.0)
    ap.add_argument("--guidance-turns", type=int, default=0)
    ap.add_argument("--out", required=True)
    args = ap.parse_args(argv)

    manifest = load_manifest(args.manifest)
    cell = score_cell(
        manifest, args.bug, args.candidate, args.treatment, args.worktree,
        args.transcript, args.wall_time_s, args.guidance_turns)

    os.makedirs(os.path.dirname(os.path.abspath(args.out)), exist_ok=True)
    with open(args.out, "w") as fh:
        json.dump(cell, fh, indent=2)
        fh.write("\n")
    print(f"wrote {args.out}  quality={cell['outcome']['quality']} "
          f"oracle={cell['outcome']['oracle_status']} "
          f"compliance={cell['compliance']['rate']} "
          f"cost={cell['metrics']['cost_usd']}")


if __name__ == "__main__":
    main()
