#!/usr/bin/env python3
"""bench.py — generic, repo-agnostic bug-fix benchmark grader + fixture verifier.

The external bake-off ("should I use kitsoki for MY project?") generalised to ANY
open-source repo: point it at `projects/<name>/manifest.yaml` and it can
deterministically grade a candidate fix against the regression test the real PR
shipped (the hidden oracle), and verify that every fixture is genuinely armed
(RED at baseline, GREEN at the real fix).

No LLM, no cost. Two subcommands:

  bench.py score  --project <name> --bug <id> --tree <candidate-worktree>
                  [--out cell.json] [--candidate K] [--treatment T]
      Overlay the hidden oracle on the candidate tree and run it.
      exit 0 ⇔ oracle GREEN (good fix); exit 1 ⇔ RED (bug remains).

  bench.py verify --project <name> [--bug <id>] [--repo-dir <prebuilt clone>]
      Prove each fixture: RED at baseline_sha, GREEN after the real fix's source.
      exit 0 ⇔ all checked fixtures armed.

A project is described entirely by its manifest (see projects/query-string/
manifest.yaml): project.{repo,install,test_cmd,oracle.{target,run}} and
bugs[].{baseline_sha,fix_sha,fix_source,oracle_test,oracle_match}. To add a new
repo, drop in a manifest + the isolated oracle test files — no code changes.
"""
import argparse, json, os, shutil, subprocess, sys, tempfile
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("bench.py needs pyyaml (pip install pyyaml)")

HERE = Path(__file__).resolve().parent


def load(project):
    mpath = HERE / "projects" / project / "manifest.yaml"
    if not mpath.exists():
        sys.exit(f"no manifest: {mpath}")
    m = yaml.safe_load(mpath.read_text())
    m["_dir"] = mpath.parent
    return m


def bug_of(m, bug_id):
    for b in m.get("bugs", []):
        if b["id"] == bug_id:
            return b
    sys.exit(f"unknown bug {bug_id} in {m['project']['id']}")


def sh(cmd, cwd, env=None, quiet=False):
    r = subprocess.run(cmd, cwd=cwd, shell=isinstance(cmd, str),
                       env={**os.environ, **(env or {})},
                       capture_output=True, text=True)
    if not quiet and r.returncode != 0:
        sys.stderr.write(r.stdout[-2000:] + r.stderr[-2000:])
    return r


def materialize(tree, dest, node_modules=None):
    """Copy a candidate tree into dest (tracked files if a git repo, else cp),
    then link a prebuilt node_modules (so we never re-install per score)."""
    tree = Path(tree)
    dest = Path(dest)
    files = sh(["git", "ls-files"], cwd=tree, quiet=True)
    if files.returncode == 0 and files.stdout.strip():
        for rel in files.stdout.splitlines():
            src = tree / rel
            if not src.exists():
                continue
            (dest / rel).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest / rel)
    else:
        shutil.copytree(tree, dest, dirs_exist_ok=True,
                        ignore=shutil.ignore_patterns("node_modules", ".git"))
    nm = node_modules or os.environ.get("QS_NODE_MODULES") or (tree / "node_modules")
    nm = Path(nm)
    if nm.exists():
        (dest / "node_modules").unlink(missing_ok=True)
        os.symlink(nm, dest / "node_modules")


def score(m, bug, tree, out, candidate, treatment):
    proj = m["project"]
    # Per-bug `oracle:` overrides the project default (target/run/match/inject).
    # A heterogeneous repo (gears-rust: per-bug crate + cargo features +
    # standalone test file) sets these per bug; a uniform repo (query-string)
    # uses the project default. Back-compat: no per-bug block ⇒ project default.
    oracle_cfg = {**proj.get("oracle", {}), **bug.get("oracle", {})}
    oracle_file = m["_dir"] / bug["oracle_test"]
    if not oracle_file.exists():
        sys.exit(f"oracle missing: {oracle_file}")

    scratch = Path(tempfile.mkdtemp(prefix="bench-"))
    try:
        materialize(tree, scratch)
        # Inject the hidden oracle. `inject: append` (default) appends into an
        # EXISTING shared test file (query-string's test/parse.js); `inject:
        # write` CREATES a standalone test file at `target` (a Rust
        # tests/oracle_<bug>.rs calling public API). Both keep the oracle out of
        # the candidate tree until scoring.
        target = scratch / oracle_cfg["target"]
        if oracle_cfg.get("inject", "append") == "write":
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(oracle_file.read_text())
        else:
            with open(target, "a") as f:
                f.write(f"\n// ---- injected hidden oracle ({bug['id']}) ----\n")
                f.write(oracle_file.read_text())

        run_cmd = oracle_cfg["run"].replace("{target}", oracle_cfg["target"]) \
                                   .replace("{match}", bug.get("oracle_match", ""))
        oracle_r = sh(run_cmd, cwd=scratch, quiet=True)
        oracle_pass = oracle_r.returncode == 0

        suite_pass = None
        # `suite: false` skips the heavy secondary full-suite signal (e.g. a
        # whole-workspace `cargo test`); the hidden oracle stays primary.
        if proj.get("test_cmd") and proj.get("suite", True):
            suite_r = sh(proj["test_cmd"], cwd=scratch, quiet=True)
            suite_pass = suite_r.returncode == 0

        if oracle_pass:
            quality = "solved" if suite_pass else "partial"
        else:
            quality = "failed"

        sys.stderr.write(
            f"[bench] {m['project']['id']}/{bug['id']} oracle="
            f"{'pass' if oracle_pass else 'fail'} suite={suite_pass} -> {quality}\n")

        if out:
            Path(out).parent.mkdir(parents=True, exist_ok=True)
            Path(out).write_text(json.dumps({
                "project": m["project"]["id"],
                "bug": bug["id"],
                "candidate": candidate,
                "treatment": treatment,
                "outcome": {
                    "oracle_pass": oracle_pass,
                    "oracle_status": "pass" if oracle_pass else "fail",
                    "build_pass": None,
                    "suite_pass": suite_pass,
                    "quality": quality,
                    "adjudicated": False,
                    "adjudication_note": "",
                },
                "notes": "external oracle; suite_pass is the SECONDARY signal "
                         "(a correct fix may legitimately update one pre-existing test)",
            }, indent=2))
            sys.stderr.write(f"[bench] wrote {out}\n")
        return 0 if oracle_pass else 1
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def verify(m, only_bug, repo_dir):
    """Clone (or reuse), and for each fixture assert RED@baseline, GREEN@fix."""
    proj = m["project"]
    tmp = None
    if repo_dir:
        repo = Path(repo_dir)
    else:
        tmp = Path(tempfile.mkdtemp(prefix="bench-repo-"))
        repo = tmp / proj["id"]
        sh(["git", "init", "-q", str(repo)], cwd=tmp)
        sh(["git", "remote", "add", "origin", proj["repo"]], cwd=repo)

    ok = True
    try:
        bugs = [b for b in m["bugs"] if (not only_bug or b["id"] == only_bug)]
        # fetch all needed commits + install once
        if not repo_dir:
            for b in bugs:
                sh(["git", "fetch", "-q", "--depth", "1", "origin", b["baseline_sha"]], cwd=repo)
                sh(["git", "fetch", "-q", "--depth", "1", "origin", b["fix_sha"]], cwd=repo)
            sh(["git", "checkout", "-q", bugs[0]["baseline_sha"]], cwd=repo)
            sys.stderr.write("[bench] npm install (once)...\n")
            sh(proj["install"], cwd=repo)
        os.environ["QS_NODE_MODULES"] = str(repo / "node_modules")

        work = Path(tempfile.mkdtemp(prefix="bench-verify-"))
        for b in bugs:
            # Isolate the compiled-artifact cache PER FIXTURE. A shared
            # CARGO_TARGET_DIR cross-contaminates: two bugs pin DIFFERENT baselines
            # of the same workspace, so a dep (e.g. cf-modkit-canonical-errors)
            # compiled for bug1's newer baseline would leak its rlib into bug4's
            # older baseline and falsely turn its RED oracle GREEN. A per-bug dir
            # keeps RED+GREEN of the SAME fixture fast (shared deps) while staying
            # correct across fixtures. (cargo-only; harmless for other runners.)
            os.environ["CARGO_TARGET_DIR"] = str(work / f"{b['id']}-target")
            red = work / f"{b['id']}-red"
            export(repo, b["baseline_sha"], red)
            red_green = score(m, b, red, None, "real-fix", "oracle") == 0
            green = work / f"{b['id']}-green"
            export(repo, b["baseline_sha"], green)
            sh(["git", "--work-tree=" + str(green), "checkout", b["fix_sha"], "--",
                b.get("fix_source", ".")], cwd=repo)
            fix_green = score(m, b, green, None, "real-fix", "oracle") == 0
            armed = (not red_green) and fix_green
            ok = ok and armed
            print(f"{'OK ' if armed else 'BAD'} {b['id']}: "
                  f"baseline={'GREEN' if red_green else 'RED'} (want RED), "
                  f"real-fix={'GREEN' if fix_green else 'RED'} (want GREEN)")
        shutil.rmtree(work, ignore_errors=True)
    finally:
        if tmp:
            shutil.rmtree(tmp, ignore_errors=True)
    return 0 if ok else 1


def summarize(m, results_dir, deck=None, markdown=None):
    """Roll up every results/cells/<bug>-<cand>-*.json into a by-candidate summary
    (solved/partial/failed counts + solve_rate). Free, deterministic — consumed by
    the repo-bakeoff story's scoring room and the report/deck builder."""
    cells_dir = HERE / results_dir / "cells"
    cells = []
    for f in sorted(cells_dir.glob("*.json")) if cells_dir.exists() else []:
        try:
            cells.append(json.loads(f.read_text()))
        except Exception:
            continue
    by = {}
    for c in cells:
        cand = c.get("candidate", "?")
        q = (c.get("outcome", {}) or {}).get("quality", "?")
        b = by.setdefault(cand, {"n": 0, "solved": 0, "partial": 0, "failed": 0})
        b["n"] += 1
        if q in b:
            b[q] += 1
    for cand, b in by.items():
        b["solve_rate"] = round(b["solved"] / b["n"], 3) if b["n"] else 0.0
    out = {"project": m["project"]["id"], "cells": cells, "rollup": {"by_candidate": by},
           "summary_path": str((HERE / results_dir / "summary.json"))}
    summary_path = HERE / results_dir / "summary.json"
    summary_path.write_text(json.dumps(out, indent=2))
    if deck:
        root = HERE.parents[2]
        cmd = [
            sys.executable,
            str(root / "tools" / "report-deck" / "deterministic_deck.py"),
            "--kind", "external-summary",
            "--input", str(summary_path),
            "--out", deck,
        ]
        if markdown:
            cmd += ["--markdown", markdown]
        subprocess.run(cmd, check=True, stdout=subprocess.PIPE, text=True)
    print(json.dumps(out))
    return 0


def trace_cost(trace):
    """Sum the worker cost + tokens from a live kitsoki trace.
    Metered providers carry payload.meta.cost_usd; subscription auth carries
    none, so we always also report token usage + agent-call count."""
    if not os.path.exists(trace):
        print(json.dumps({"error": "no trace", "trace": trace}))
        return 1
    cost = 0.0
    tin = tout = calls = 0
    for line in open(trace):
        try:
            o = json.loads(line)
        except Exception:
            continue
        p = o.get("payload", {}) or {}
        meta = p.get("meta", {}) if isinstance(p.get("meta"), dict) else {}
        c = meta.get("cost_usd")
        if isinstance(c, (int, float)):
            cost += c
        u = meta.get("usage", {}) if isinstance(meta.get("usage"), dict) else {}
        tin += u.get("input_tokens", 0) or 0
        tout += u.get("output_tokens", 0) or 0
        if o.get("kind") == "agent.call.complete":
            calls += 1
    print(json.dumps({"trace": trace, "cost_usd": round(cost, 4),
                      "input_tokens": tin, "output_tokens": tout,
                      "agent_calls": calls, "metered": cost > 0}))
    return 0


def export(repo, sha, dest):
    Path(dest).mkdir(parents=True, exist_ok=True)
    p1 = subprocess.Popen(["git", "-C", str(repo), "archive", sha], stdout=subprocess.PIPE)
    p2 = subprocess.Popen(["tar", "-x", "-C", str(dest)], stdin=p1.stdout)
    p1.stdout.close()
    p2.communicate()
    if p2.returncode != 0:
        sys.exit(f"export {sha} failed")


def main():
    ap = argparse.ArgumentParser(description="generic repo-agnostic bug-fix benchmark grader")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("score")
    s.add_argument("--project", required=True)
    s.add_argument("--bug", required=True)
    s.add_argument("--tree", required=True)
    s.add_argument("--out")
    s.add_argument("--candidate", default="candidate")
    s.add_argument("--treatment", default="kitsoki")
    v = sub.add_parser("verify")
    v.add_argument("--project", required=True)
    v.add_argument("--bug")
    v.add_argument("--repo-dir", help="prebuilt clone with node_modules to reuse")
    mt = sub.add_parser("meta")  # machine-readable project facts (for the Go runner)
    mt.add_argument("--project", required=True)
    mt.add_argument("--bug")     # optional: emit one bug's drive facts
    c = sub.add_parser("cost")   # worker cost/tokens from a live trace
    c.add_argument("--trace", required=True)
    sm = sub.add_parser("summarize")  # roll up results/cells/*.json by candidate
    sm.add_argument("--project", required=True)
    sm.add_argument("--results", default="results", help="results dir (cells/ under it)")
    sm.add_argument("--deck", help="optional Slidey JSON report spec")
    sm.add_argument("--markdown", help="optional Markdown review index")
    a = ap.parse_args()

    if a.cmd == "cost":
        sys.exit(trace_cost(a.trace))
    if a.cmd == "summarize":
        sys.exit(summarize(load(a.project), a.results, a.deck, a.markdown))

    m = load(a.project)
    if a.cmd == "score":
        sys.exit(score(m, bug_of(m, a.bug), a.tree, a.out, a.candidate, a.treatment))
    elif a.cmd == "meta":
        p = m["project"]
        if a.bug:
            b = bug_of(m, a.bug)
            print(json.dumps({
                "id": p["id"], "repo": p["repo"], "install": p["install"],
                "test_cmd": p.get("test_cmd", ""),
                "bug": b["id"], "baseline_sha": b["baseline_sha"], "fix_sha": b["fix_sha"],
                "title": b["title"], "ticket": b.get("ticket", b["title"]),
            }))
        else:
            print(json.dumps({
                "id": p["id"], "repo": p["repo"],
                "onboard_app": p.get("onboard_app", "@kitsoki/dev-story"),
                "local_only": bool(p.get("local_only", False)),
                "baselines": [b["baseline_sha"] for b in m["bugs"]],
                "bugs": [b["id"] for b in m["bugs"]],
            }))
        sys.exit(0)
    else:
        sys.exit(verify(m, a.bug, a.repo_dir))


if __name__ == "__main__":
    main()
