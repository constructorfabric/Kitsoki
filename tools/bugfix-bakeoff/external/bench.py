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

  bench.py preflight --project <name> [--repo-dir <prebuilt clone>] [--candidate K]
      Check manifest, local checkout, oracles, baseline commits, and profile setup.
      Free/no-LLM. exit 0 ⇔ ready to arm/drive.

  bench.py drive-plan --project <name> --bug <id[,id]> --candidate <key[,key]>
      Print exact drive_cell.sh --score commands for the selected matrix.
      Free/no-LLM.

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
REPO_ROOT = HERE.parents[2]


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


def split_csv(values):
    if values is None:
        return []
    if isinstance(values, str):
        values = [values]
    return [part.strip() for value in values for part in str(value).split(",")
            if part.strip()]


def selected_bugs(m, bug_ids=None):
    ids = split_csv(bug_ids)
    if not ids:
        return [b for b in m.get("bugs", []) if not b.get("reference_only")]
    wanted = set(ids)
    out = []
    seen = set()
    for bug_id in ids:
        b = bug_of(m, bug_id)
        if b.get("reference_only"):
            sys.exit(f"bug {bug_id} is reference_only in {m['project']['id']}; promote it before arming")
        if bug_id not in seen:
            out.append(b)
            seen.add(bug_id)
    missing = wanted - seen
    if missing:
        sys.exit(f"unknown bug(s) {', '.join(sorted(missing))} in {m['project']['id']}")
    return out


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


def decide_quality(oracle_pass, suite_pass, suite_enabled):
    """The deterministic cell grade.

    oracle GREEN + (suite GREEN or suite not run) ⇒ solved. A suite-disabled
    project (kitsoki/gears-rust: the hidden oracle is the ONLY signal) reaches
    solved on the oracle alone — otherwise "suite not run" is conflated with
    "suite failed" and the escalation ladder could never stop, always climbing to
    the most expensive rung. oracle GREEN + suite RAN-and-RED ⇒ partial (a correct
    fix that didn't update a pre-existing test). oracle RED ⇒ failed.
    """
    if not oracle_pass:
        return "failed"
    return "solved" if (suite_pass or not suite_enabled) else "partial"


def read_trace_metrics(trace):
    """Worker cost/tokens/agent-calls from a live kitsoki trace, or None-filled if
    the trace is absent. Shared by `score` (to enrich a cell) and the `cost` cmd."""
    found = bool(trace) and os.path.exists(trace)
    cost = 0.0
    tin = tout = calls = 0
    if found:
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
    return {
        "found": found,
        "cost_usd": round(cost, 4) if found else None,
        "total_tokens": (tin + tout) if found else None,
        "input_tokens": tin if found else None,
        "output_tokens": tout if found else None,
        "agent_calls": calls if found else None,
        "metered": cost > 0,
    }


def candidate_meta(candidates_path, key):
    """Look up model/effort/provider for a candidate key from candidates.yaml.
    Returns {} if the file or key is absent (back-compat for ad-hoc scoring)."""
    if not candidates_path or not os.path.exists(candidates_path):
        return {}
    try:
        d = yaml.safe_load(Path(candidates_path).read_text()) or {}
    except Exception:
        return {}
    for c in d.get("candidates", []):
        if c.get("key") == key:
            return {k: c.get(k) for k in ("model", "effort", "provider")}
    return {}


def load_candidates(candidates_path):
    if not candidates_path or not os.path.exists(candidates_path):
        return {}
    try:
        return yaml.safe_load(Path(candidates_path).read_text()) or {}
    except Exception:
        return {}


def candidate_by_key(candidates_path, key):
    for c in load_candidates(candidates_path).get("candidates", []):
        if c.get("key") == key:
            return c
    return None


def build_drive_plan(m, bug_ids=None, candidate=None, repo_dir=None):
    bugs = selected_bugs(m, bug_ids)
    candidates = split_csv(candidate)
    if not candidates:
        sys.exit("drive-plan needs at least one --candidate")
    repo_arg = f" --repo-dir {Path(repo_dir).expanduser()}" if repo_dir else ""
    commands = []
    lines = []
    for b in bugs:
        for cand in candidates:
            cmd = (
                f"tools/bugfix-bakeoff/external/drive_cell.sh "
                f"--project {m['project']['id']} --bug {b['id']} "
                f"--candidate {cand}{repo_arg} --score"
            )
            commands.append({"bug": b["id"], "candidate": cand, "command": cmd})
            lines.append(f"- `{b['id']}` x `{cand}`: `{cmd}`")
    markdown = "\n".join(lines)
    return {
        "ok": True,
        "project": m["project"]["id"],
        "repo_dir": str(Path(repo_dir).expanduser()) if repo_dir else "",
        "bugs": [b["id"] for b in bugs],
        "candidates": candidates,
        "commands": commands,
        "markdown": markdown,
    }


def drive_plan(m, bug_ids=None, candidate=None, repo_dir=None):
    print(json.dumps(build_drive_plan(m, bug_ids=bug_ids, candidate=candidate, repo_dir=repo_dir)))
    return 0


def pending_cell(m, bug_id, candidate, reason, out, candidates_path=None, treatment="kitsoki"):
    """Write an honest pending cell for provider/profile/infrastructure blockers.

    Pending is not a model capability result: the oracle never ran, so summaries
    count it separately from solved/partial/failed and the report keeps the note.
    """
    bug = bug_of(m, bug_id)
    cm = candidate_meta(candidates_path, candidate)
    cell = {
        "project": m["project"]["id"],
        "bug": bug["id"],
        "candidate": candidate,
        "treatment": treatment,
        "model": cm.get("model", ""),
        "effort": cm.get("effort", ""),
        "provider": cm.get("provider", ""),
        "outcome": {
            "oracle_pass": None,
            "oracle_status": "absent",
            "build_pass": None,
            "suite_pass": None,
            "quality": "pending",
            "adjudicated": False,
            "adjudication_note": "",
        },
        "compliance": {"rate": None, "note": "pending cell; compliance not measured"},
        "metrics": {
            "cost_usd": None,
            "total_tokens": None,
            "wall_time_s": None,
            "guidance_turns": 0,
            "agent_calls": None,
        },
        "trace_found": False,
        "notes": reason,
        "pending_reason": reason,
    }
    out_path = Path(out) if out else HERE / "results" / "cells" / f"{m['project']['id']}-{bug['id']}-{candidate}-{treatment}.json"
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(json.dumps(cell, indent=2) + "\n")
    print(json.dumps({"ok": True, "out": str(out_path), "cell": cell}))
    return 0


def configured_profiles(root=None):
    """Return harness profile names from the checked-in and local kitsoki config.

    This intentionally parses YAML instead of grepping indentation so preflight
    and shell drivers share one less brittle definition of "configured".
    """
    root = Path(root) if root is not None else REPO_ROOT
    profiles = set()
    for name in (".kitsoki.yaml", ".kitsoki.local.yaml"):
        path = Path(root) / name
        if not path.exists():
            continue
        try:
            data = yaml.safe_load(path.read_text()) or {}
        except Exception:
            continue
        harness_profiles = data.get("harness_profiles") or {}
        if isinstance(harness_profiles, dict):
            profiles.update(str(k) for k in harness_profiles.keys())
    return profiles


def git_commit_exists(repo, sha):
    return sh(["git", "-C", str(repo), "cat-file", "-e", f"{sha}^{{commit}}"],
              cwd=repo, quiet=True).returncode == 0


def git_tracked_dirty(repo):
    r = sh(["git", "-C", str(repo), "status", "--porcelain", "--untracked-files=no"],
           cwd=repo, quiet=True)
    if r.returncode != 0:
        return None
    return bool(r.stdout.strip())


def build_preflight(m, repo_dir=None, candidate=None, candidates_path=None, bug_ids=None):
    """No-cost readiness check for a repo-history bake-off cell/sweep."""
    proj = m["project"]
    errors = []
    warnings = []
    local_only = bool(proj.get("local_only", False))
    repo_path = Path(repo_dir).expanduser() if repo_dir else None
    candidates = split_csv(candidate)
    candidate_infos = []
    bugs = selected_bugs(m, bug_ids)

    if not bugs:
        errors.append("manifest has no bugs")

    for b in bugs:
        oracle = m["_dir"] / b.get("oracle_test", "")
        if b.get("reference_only"):
            continue
        if not oracle.exists():
            errors.append(f"{b.get('id', '?')}: oracle missing: {oracle}")
        if not b.get("baseline_sha"):
            errors.append(f"{b.get('id', '?')}: missing baseline_sha")
        if not b.get("fix_sha"):
            warnings.append(f"{b.get('id', '?')}: missing fix_sha; verify cannot prove GREEN")

    for cand in candidates:
        candidate_info = candidate_by_key(candidates_path, cand)
        if not candidate_info:
            errors.append(f"unknown candidate '{cand}' in {candidates_path}")
            continue
        candidate_infos.append(candidate_info)
        profile = candidate_info.get("profile", "")
        profiles = configured_profiles()
        if not profile:
            errors.append(f"candidate '{cand}' has no profile")
        elif profile not in profiles:
            errors.append(
                f"profile '{profile}' for candidate '{cand}' is not configured "
                "in .kitsoki.yaml/.kitsoki.local.yaml"
            )

    candidate_arg = " ".join(f"--candidate {c}" for c in candidates)
    if not candidate_arg:
        if len(candidate_infos) == 1:
            candidate_arg = f"--candidate {candidate_infos[0].get('key', '<candidate>')}"
        else:
            candidate_arg = "--candidate <candidate>"

    if local_only:
        if not repo_path:
            env_name = f"{proj['id'].upper().replace('-', '_')}_REPO"
            errors.append(f"local_only project needs --repo-dir <checkout> or {env_name}")
        elif not (repo_path / ".git").exists():
            errors.append(f"repo_dir is not a git checkout: {repo_path}")
    elif repo_path and not (repo_path / ".git").exists():
        errors.append(f"repo_dir is not a git checkout: {repo_path}")

    if repo_path and (repo_path / ".git").exists():
        dirty = git_tracked_dirty(repo_path)
        if dirty is None:
            warnings.append(f"could not inspect git status for {repo_path}")
        elif dirty:
            warnings.append(f"tracked changes present in {repo_path}; source checkout is not mutated, but verify from a clean tree is easier to audit")
        for b in bugs:
            if not b.get("baseline_sha"):
                continue
            if not git_commit_exists(repo_path, b["baseline_sha"]):
                errors.append(f"{b['id']}: baseline {b['baseline_sha']} not present in {repo_path}")
            fix_sha = b.get("fix_sha")
            if fix_sha and not git_commit_exists(repo_path, fix_sha):
                errors.append(f"{b['id']}: fix {fix_sha} not present in {repo_path}")

    repo_arg = f" --repo-dir {repo_path}" if repo_path else ""
    bug_arg = f" --bug {','.join(b['id'] for b in bugs)}" if bugs else ""
    cell_candidate_arg = f" --candidate {candidates[0]}" if len(candidates) == 1 else " --candidate <candidate>"
    commands = {
        "verify": f"python3 bench.py verify --project {proj['id']}{bug_arg}{repo_arg}",
        "preflight": f"python3 bench.py preflight --project {proj['id']}{bug_arg}{repo_arg} {candidate_arg}".rstrip(),
        "dry_run_cell": f"tools/bugfix-bakeoff/external/drive_cell.sh --project {proj['id']} --bug <bug>{cell_candidate_arg}{repo_arg} --no-drive",
        "drive_cell": f"tools/bugfix-bakeoff/external/drive_cell.sh --project {proj['id']} --bug <bug>{cell_candidate_arg}{repo_arg} --score",
        "summarize": f"python3 bench.py summarize --project {proj['id']} --results ../../../.artifacts/external-bakeoff/results --deck ../../../.artifacts/external-bakeoff/report/deck.slidey.json --markdown ../../../.artifacts/external-bakeoff/report/report.md",
    }
    out = {
        "ok": not errors,
        "project": proj["id"],
        "local_only": local_only,
        "repo_dir": str(repo_path) if repo_path else "",
        "bugs": [b.get("id") for b in bugs],
        "reference_only": [b.get("id") for b in m.get("bugs", []) if b.get("reference_only")],
        "candidates": candidate_infos,
        "candidate": candidate_infos[0] if len(candidate_infos) == 1 else {},
        "errors": errors,
        "warnings": warnings,
        "commands": commands,
    }
    return out


def preflight(m, repo_dir=None, candidate=None, candidates_path=None, bug_ids=None):
    out = build_preflight(m, repo_dir=repo_dir, candidate=candidate,
                          candidates_path=candidates_path, bug_ids=bug_ids)
    print(json.dumps(out, indent=2))
    return 0 if out["ok"] else 1


def collect_cells(results_dir):
    cells_dir = HERE / results_dir / "cells"
    cells = []
    for f in sorted(cells_dir.glob("*.json")) if cells_dir.exists() else []:
        try:
            cell = json.loads(f.read_text())
        except Exception:
            continue
        cell["_path"] = str(f)
        cells.append(cell)
    return cells, cells_dir


def rollup_cells(cells):
    by = {}
    for c in cells:
        cand = c.get("candidate", "?")
        q = (c.get("outcome", {}) or {}).get("quality", "?")
        b = by.setdefault(cand, {"n": 0, "solved": 0, "partial": 0, "failed": 0, "pending": 0})
        b["n"] += 1
        if q in b:
            b[q] += 1
    for _, b in by.items():
        attempted = b["n"] - b.get("pending", 0)
        b["attempted"] = attempted
        b["solve_rate"] = round(b["solved"] / attempted, 3) if attempted else 0.0
    return by


def readiness(m, repo_dir=None, candidate=None, candidates_path=None, bug_ids=None,
              results_dir="../../../.artifacts/external-bakeoff/results", markdown=None,
              armed=False):
    """Free operator-facing audit for the selected repo-history matrix.

    This command does not run cargo/npm or call a model. It composes preflight,
    drive-plan, and existing result artifacts into one report so an operator can
    see whether setup is ready, which cells are still missing, and what command
    to run next.
    """
    pre = build_preflight(m, repo_dir=repo_dir, candidate=candidate,
                          candidates_path=candidates_path, bug_ids=bug_ids)
    plan = build_drive_plan(m, bug_ids=bug_ids, candidate=candidate, repo_dir=repo_dir)
    cells, cells_dir = collect_cells(results_dir)
    selected = {(cmd["bug"], cmd["candidate"]) for cmd in plan["commands"]}
    matching = []
    for cell in cells:
        key = (cell.get("bug"), cell.get("candidate"))
        if key in selected and cell.get("project") == m["project"]["id"]:
            matching.append(cell)
    completed = {(c.get("bug"), c.get("candidate")) for c in matching}
    missing = [
        {"bug": cmd["bug"], "candidate": cmd["candidate"], "command": cmd["command"]}
        for cmd in plan["commands"]
        if (cmd["bug"], cmd["candidate"]) not in completed
    ]
    result_summary = {
        "cells_dir": str(cells_dir),
        "selected_cells": len(plan["commands"]),
        "scored_cells": len(matching),
        "missing_cells": len(missing),
        "rollup": {"by_candidate": rollup_cells(matching)},
    }
    next_actions = []
    if not pre["ok"]:
        next_actions.append("fix preflight errors before arming or driving live cells")
    elif not armed:
        next_actions.append("run history-smoke or bench.py verify for RED/GREEN arming if not already captured")
    if missing:
        next_actions.append("run the listed drive_cell.sh --score commands, or mark blocked providers with bench.py pending")
    else:
        next_actions.append("run bench.py summarize or advance repo-bakeoff scoring to generate the report/deck")
    out = {
        "ok": pre["ok"],
        "project": m["project"]["id"],
        "preflight": pre,
        "arming": {
            "verified": bool(armed),
            "note": "selected fixtures verified RED@baseline/GREEN@fix" if armed else "not checked by this readiness command",
        },
        "drive_plan": plan,
        "results": result_summary,
        "missing": missing,
        "next_actions": next_actions,
    }
    if markdown:
        write_readiness_markdown(out, Path(markdown))
        out["markdown"] = markdown
    print(json.dumps(out, indent=2))
    return 0 if pre["ok"] else 1


def write_readiness_markdown(report, markdown):
    pre = report["preflight"]
    plan = report["drive_plan"]
    results = report["results"]
    markdown.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        f"# {report['project']} repo-history readiness",
        "",
        f"Preflight: {'ready' if pre.get('ok') else 'blocked'}",
        f"Arming: {'verified' if report.get('arming', {}).get('verified') else 'not captured'}",
        f"Selected cells: {results['selected_cells']}",
        f"Scored cells: {results['scored_cells']}",
        f"Missing cells: {results['missing_cells']}",
        "",
        "## Setup",
        "",
    ]
    if pre.get("errors"):
        lines.extend(["Errors:", ""])
        lines.extend(f"- {e}" for e in pre.get("errors", []))
        lines.append("")
    if pre.get("warnings"):
        lines.extend(["Warnings:", ""])
        lines.extend(f"- {w}" for w in pre.get("warnings", []))
        lines.append("")
    if not pre.get("errors") and not pre.get("warnings"):
        lines.extend(["No preflight errors or warnings.", ""])
    lines.extend([
        "## Drive Commands",
        "",
        plan.get("markdown", "(none)"),
        "",
        "## Missing Cells",
        "",
    ])
    if report.get("missing"):
        for m in report["missing"]:
            lines.append(f"- `{m['bug']}` x `{m['candidate']}`: `{m['command']}`")
    else:
        lines.append("All selected cells have scored or pending results.")
    lines.extend(["", "## Next Actions", ""])
    lines.extend(f"- {a}" for a in report.get("next_actions", []))
    markdown.write_text("\n".join(lines) + "\n")


def score(m, bug, tree, out, candidate, treatment, trace=None, candidates_path=None):
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

        # Optional per-bug/project `oracle.setup`: a command run in the scratch
        # tree BEFORE the oracle (e.g. a nested `pnpm install` for a polyglot repo
        # whose JS package isn't at the root). Keeps the harness general across
        # go/node/cargo without a root-only node_modules assumption.
        setup_cmd = oracle_cfg.get("setup")
        if setup_cmd:
            setup_r = sh(setup_cmd, cwd=scratch, quiet=True)
            if setup_r.returncode != 0:
                sys.stderr.write(setup_r.stdout[-2000:] + setup_r.stderr[-2000:])

        run_cmd = oracle_cfg["run"].replace("{target}", oracle_cfg["target"]) \
                                   .replace("{match}", bug.get("oracle_match", ""))
        oracle_r = sh(run_cmd, cwd=scratch, quiet=True)
        oracle_pass = oracle_r.returncode == 0

        suite_pass = None
        # `suite: false` (or no test_cmd) skips the heavy secondary full-suite
        # signal (e.g. a whole-workspace `cargo test`); the hidden oracle stays
        # primary. Track whether the suite actually RAN, so "not run" is never
        # conflated with "ran and failed".
        suite_enabled = bool(proj.get("test_cmd") and proj.get("suite", True))
        if suite_enabled:
            suite_r = sh(proj["test_cmd"], cwd=scratch, quiet=True)
            suite_pass = suite_r.returncode == 0

        quality = decide_quality(oracle_pass, suite_pass, suite_enabled)

        sys.stderr.write(
            f"[bench] {m['project']['id']}/{bug['id']} oracle="
            f"{'pass' if oracle_pass else 'fail'} suite={suite_pass} -> {quality}\n")

        if out:
            # Full cell shape so the deck aggregator (aggregate.py) consumes it
            # directly: metrics (worker cost/tokens from the trace) + compliance
            # + the model/effort/provider axis. compliance.rate is None — the
            # external grader does not measure contract conformance; the headline
            # signals are outcome.quality + metrics.cost_usd.
            tm = read_trace_metrics(trace)
            cm = candidate_meta(candidates_path, candidate)
            Path(out).parent.mkdir(parents=True, exist_ok=True)
            Path(out).write_text(json.dumps({
                "project": m["project"]["id"],
                "bug": bug["id"],
                "candidate": candidate,
                "treatment": treatment,
                "model": cm.get("model", ""),
                "effort": cm.get("effort", ""),
                "provider": cm.get("provider", ""),
                "outcome": {
                    "oracle_pass": oracle_pass,
                    "oracle_status": "pass" if oracle_pass else "fail",
                    "build_pass": None,
                    "suite_pass": suite_pass,
                    "quality": quality,
                    "adjudicated": False,
                    "adjudication_note": "",
                },
                "compliance": {"rate": None, "note": "not measured by the external grader"},
                "metrics": {
                    "cost_usd": tm["cost_usd"],
                    "total_tokens": tm["total_tokens"],
                    "wall_time_s": None,
                    "guidance_turns": 0,
                    "agent_calls": tm["agent_calls"],
                },
                "trace_found": tm["found"],
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
    bugs = selected_bugs(m, only_bug)
    tmp = None
    if repo_dir:
        # Never operate directly on a caller's checkout. The GREEN proof checks
        # out the real fix's source paths through git; doing that against the
        # source checkout can dirty its index/worktree. A local mirror is cheap
        # and gives the verifier a private git directory to mutate.
        src = Path(repo_dir)
        tmp = Path(tempfile.mkdtemp(prefix="bench-repo-"))
        repo = tmp / f"{proj['id']}-mirror"
        r = sh(["git", "clone", "--local", "--no-checkout", "-q", str(src), str(repo)], cwd=tmp)
        if r.returncode != 0:
            sys.stderr.write(r.stdout[-2000:] + r.stderr[-2000:])
            return 1
    else:
        tmp = Path(tempfile.mkdtemp(prefix="bench-repo-"))
        repo = tmp / proj["id"]
        sh(["git", "init", "-q", str(repo)], cwd=tmp)
        sh(["git", "remote", "add", "origin", proj["repo"]], cwd=repo)

    ok = True
    try:
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


def summarize(m, results_dir, deck=None, markdown=None, allow_empty=False):
    """Roll up every results/cells/<bug>-<cand>-*.json into a by-candidate summary
    (solved/partial/failed counts + solve_rate). Free, deterministic — consumed by
    the repo-bakeoff story's scoring room and the report/deck builder."""
    cells, cells_dir = collect_cells(results_dir)
    for c in cells:
        c.pop("_path", None)
    if not cells and not allow_empty:
        print(json.dumps({
            "ok": False,
            "project": m["project"]["id"],
            "error": "no scored cells",
            "cells_dir": str(cells_dir),
            "hint": "run drive_cell.sh --score for at least one matrix cell before scoring",
        }))
        return 1
    by = rollup_cells(cells)
    out = {"project": m["project"]["id"], "cells": cells, "rollup": {"by_candidate": by},
           "summary_path": str((HERE / results_dir / "summary.json"))}
    summary_path = HERE / results_dir / "summary.json"
    summary_path.parent.mkdir(parents=True, exist_ok=True)
    summary_path.write_text(json.dumps(out, indent=2))
    if deck:
        write_external_deck(out, Path(deck), markdown=Path(markdown) if markdown else None)
        out["deck"] = {
            "spec_path": deck,
            "summary": external_headline(out),
        }
    elif markdown:
        write_external_markdown(out, Path(markdown))
    if markdown:
        out["markdown"] = markdown
    print(json.dumps(out))
    return 0


def external_headline(summary):
    by = summary.get("rollup", {}).get("by_candidate", {})
    total = sum(v.get("attempted", v.get("n", 0)) for v in by.values())
    solved = sum(v.get("solved", 0) for v in by.values())
    pending = sum(v.get("pending", 0) for v in by.values())
    suffix = f"; {pending} pending" if pending else ""
    return f"{summary.get('project', 'project')} bake-off: {solved}/{total} attempted solved{suffix}"


def write_external_deck(summary, deck_path, markdown=None):
    """Write a small deterministic Slidey report directly from the external
    summary. Kept local to this harness so repo-bakeoff does not depend on an
    optional shared deck builder being installed in a consumer checkout."""
    project = summary.get("project", "project")
    cells = summary.get("cells", [])
    by = summary.get("rollup", {}).get("by_candidate", {})
    headline = external_headline(summary)
    rows = []
    for cand, bucket in sorted(by.items()):
        n = bucket.get("n", 0)
        solved = bucket.get("solved", 0)
        partial = bucket.get("partial", 0)
        failed = bucket.get("failed", 0)
        pending = bucket.get("pending", 0)
        rate = bucket.get("solve_rate", 0)
        rows.append({"cells": [cand, str(n), str(solved), str(partial), str(failed), str(pending), f"{rate:.0%}"]})
    bug_rows = []
    for c in sorted(cells, key=lambda item: (item.get("bug", ""), item.get("candidate", ""))):
        outcome = c.get("outcome", {}) or {}
        metrics = c.get("metrics", {}) or {}
        cost = metrics.get("cost_usd")
        cost_text = "subscription/unknown" if cost is None else f"${cost:.4f}"
        oracle_status = outcome.get("oracle_status", "")
        if outcome.get("quality") == "pending":
            oracle_status = "pending"
        bug_rows.append({
            "cells": [
                c.get("bug", ""),
                c.get("candidate", ""),
                outcome.get("quality", ""),
                oracle_status if oracle_status else ("pass" if outcome.get("oracle_pass") else "fail"),
                cost_text,
            ]
        })
    deck = {
        "meta": {
            "title": f"{project} repo-history bake-off",
            "resolution": {"width": 1920, "height": 1080},
            "theme": "rose-pine-moon",
        },
        "scenes": [
            {
                "type": "title",
                "eyebrow": "Kitsoki repo-history training",
                "title": f"{project} bug-fix bake-off",
                "subtitle": headline,
                "narration": headline,
            },
            {
                "type": "narrative",
                "eyebrow": "Method",
                "lede": "History becomes deterministic training material.",
                "body": (
                    "Each case pins a historical baseline and grades candidate fixes "
                    "with the real regression oracle. Live model cells are driven "
                    "through Kitsoki; this report is generated offline from scored JSON."
                ),
            },
            {
                "type": "table",
                "title": "Candidate rollup",
                "variant": "data",
                "columns": ["Candidate", "Cells", "Solved", "Partial", "Failed", "Pending", "Solve rate"],
                "rows": rows,
            },
            {
                "type": "table",
                "title": "Cell verdicts",
                "variant": "data",
                "columns": ["Bug", "Candidate", "Quality", "Oracle", "Cost"],
                "rows": bug_rows,
            },
        ],
    }
    deck_path.parent.mkdir(parents=True, exist_ok=True)
    deck_path.write_text(json.dumps(deck, indent=2) + "\n")
    if markdown:
        write_external_markdown(summary, markdown)


def write_external_markdown(summary, markdown):
    project = summary.get("project", "project")
    cells = summary.get("cells", [])
    by = summary.get("rollup", {}).get("by_candidate", {})
    headline = external_headline(summary)
    markdown.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        f"# {project} repo-history bake-off",
        "",
        headline,
        "",
        "## Candidate rollup",
        "",
        "| Candidate | Cells | Solved | Partial | Failed | Pending | Solve rate |",
        "|---|---:|---:|---:|---:|---:|---:|",
    ]
    for cand, bucket in sorted(by.items()):
        lines.append(
            f"| {cand} | {bucket.get('n', 0)} | {bucket.get('solved', 0)} | "
            f"{bucket.get('partial', 0)} | {bucket.get('failed', 0)} | "
            f"{bucket.get('pending', 0)} | "
            f"{bucket.get('solve_rate', 0):.0%} |"
        )
    lines.extend(["", "## Cell verdicts", "", "| Bug | Candidate | Quality | Oracle |", "|---|---|---|---|"])
    for c in sorted(cells, key=lambda item: (item.get("bug", ""), item.get("candidate", ""))):
        outcome = c.get("outcome", {}) or {}
        oracle = outcome.get("oracle_status") or ("pass" if outcome.get("oracle_pass") else "fail")
        if outcome.get("quality") == "pending":
            oracle = "pending"
        lines.append(f"| {c.get('bug', '')} | {c.get('candidate', '')} | {outcome.get('quality', '')} | {oracle} |")
    markdown.write_text("\n".join(lines) + "\n")


def trace_cost(trace):
    """Print the worker cost + tokens from a live kitsoki trace (the `cost` cmd).
    Metered providers carry payload.meta.cost_usd; subscription auth carries
    none, so we always also report token usage + agent-call count."""
    if not os.path.exists(trace):
        print(json.dumps({"error": "no trace", "trace": trace}))
        return 1
    tm = read_trace_metrics(trace)
    print(json.dumps({"trace": trace, "cost_usd": tm["cost_usd"],
                      "input_tokens": tm["input_tokens"], "output_tokens": tm["output_tokens"],
                      "agent_calls": tm["agent_calls"], "metered": tm["metered"]}))
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
    s.add_argument("--trace", help="live trace to read worker cost/tokens from (for the cell metrics)")
    s.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                   help="candidates.yaml for model/effort/provider lookup by --candidate")
    v = sub.add_parser("verify")
    v.add_argument("--project", required=True)
    v.add_argument("--bug", action="append",
                   help="bug id to verify; repeat or pass comma-separated ids to scope the matrix")
    v.add_argument("--repo-dir", help="prebuilt clone with node_modules to reuse")
    pf = sub.add_parser("preflight")
    pf.add_argument("--project", required=True)
    pf.add_argument("--bug", action="append",
                    help="bug id to preflight; repeat or pass comma-separated ids to scope the matrix")
    pf.add_argument("--repo-dir", help="local checkout for private/local_only projects")
    pf.add_argument("--candidate", action="append",
                    help="candidate key from candidates.yaml to check profile readiness; repeat to check a matrix")
    pf.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                    help="candidates.yaml for candidate/profile lookup")
    dp = sub.add_parser("drive-plan")
    dp.add_argument("--project", required=True)
    dp.add_argument("--bug", action="append", required=True,
                    help="bug id to drive; repeat or pass comma-separated ids")
    dp.add_argument("--candidate", action="append", required=True,
                    help="candidate key to drive; repeat or pass comma-separated keys")
    dp.add_argument("--repo-dir", help="local checkout for private/local_only projects")
    rd = sub.add_parser("readiness")
    rd.add_argument("--project", required=True)
    rd.add_argument("--bug", action="append", required=True,
                    help="bug id to audit; repeat or pass comma-separated ids")
    rd.add_argument("--candidate", action="append", required=True,
                    help="candidate key to audit; repeat or pass comma-separated keys")
    rd.add_argument("--repo-dir", help="local checkout for private/local_only projects")
    rd.add_argument("--results", default="../../../.artifacts/external-bakeoff/results",
                    help="results dir to inspect for existing cells")
    rd.add_argument("--markdown", help="optional Markdown readiness report")
    rd.add_argument("--armed", action="store_true",
                    help="mark selected fixtures as already verified RED@baseline/GREEN@fix")
    rd.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                    help="candidates.yaml for candidate/profile lookup")
    pc = sub.add_parser("pending")
    pc.add_argument("--project", required=True)
    pc.add_argument("--bug", required=True)
    pc.add_argument("--candidate", required=True)
    pc.add_argument("--reason", required=True)
    pc.add_argument("--out", help="cell JSON path to write; defaults under results/cells/")
    pc.add_argument("--treatment", default="kitsoki")
    pc.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                    help="candidates.yaml for model/effort/provider lookup by --candidate")
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
    sm.add_argument("--allow-empty", action="store_true",
                    help="write an empty 0-cell summary instead of failing when no cell JSON exists")
    a = ap.parse_args()

    if a.cmd == "cost":
        sys.exit(trace_cost(a.trace))
    if a.cmd == "summarize":
        sys.exit(summarize(load(a.project), a.results, a.deck, a.markdown,
                           allow_empty=a.allow_empty))

    m = load(a.project)
    if a.cmd == "preflight":
        sys.exit(preflight(m, repo_dir=a.repo_dir, candidate=a.candidate,
                           candidates_path=a.candidates, bug_ids=a.bug))
    if a.cmd == "drive-plan":
        sys.exit(drive_plan(m, bug_ids=a.bug, candidate=a.candidate,
                            repo_dir=a.repo_dir))
    if a.cmd == "readiness":
        sys.exit(readiness(m, repo_dir=a.repo_dir, candidate=a.candidate,
                           candidates_path=a.candidates, bug_ids=a.bug,
                           results_dir=a.results, markdown=a.markdown,
                           armed=a.armed))
    if a.cmd == "pending":
        sys.exit(pending_cell(m, a.bug, a.candidate, a.reason, a.out,
                              candidates_path=a.candidates, treatment=a.treatment))
    if a.cmd == "score":
        sys.exit(score(m, bug_of(m, a.bug), a.tree, a.out, a.candidate, a.treatment,
                       trace=a.trace, candidates_path=a.candidates))
    elif a.cmd == "meta":
        p = m["project"]
        if a.bug:
            b = bug_of(m, a.bug)
            print(json.dumps({
                "id": p["id"], "repo": p.get("repo", "."), "install": p.get("install", ""),
                "test_cmd": p.get("test_cmd", ""), "local_only": bool(p.get("local_only", False)),
                "bug": b["id"], "baseline_sha": b["baseline_sha"], "fix_sha": b.get("fix_sha", ""),
                "title": b.get("title", b["id"]), "ticket": b.get("ticket", b.get("title", b["id"])),
            }))
        else:
            print(json.dumps({
                "id": p["id"], "repo": p.get("repo", "."),
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
