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

  bench.py verify <manifest.yaml>|--project <name> [--bug <id>] [--repo-dir <prebuilt clone>]
      Prove each fixture: RED at baseline_sha, GREEN after the real fix's source.
      exit 0 ⇔ all checked fixtures armed.

  bench.py preflight --project <name> [--repo-dir <prebuilt clone>] [--candidate K]
      Check manifest, local checkout, oracles, baseline commits, and profile setup.
      Free/no-LLM. exit 0 ⇔ ready to arm/drive.

  bench.py drive-plan --project <name> --bug <id[,id]> --candidate <key[,key]>
      Print exact drive_cell.sh --score commands for the selected matrix.
      Free/no-LLM.

  bench.py completion --project <name> --bug <id[,id]> --candidate <key[,key]>
      Print an explicit repo-history completion verdict from readiness/results:
      no-cost ready, ready to drive live, complete with pending, or live scored.
      Output now includes completion.completed to let KitSoki story flow gate
      deterministic finalization.
      Add --require-result-evidence or --require-live-scored to fail publishing
      gates when the current artifacts are not sufficient. Free/no-LLM.

A project is described entirely by its manifest (see projects/query-string/
manifest.yaml): project.{repo,install,test_cmd,oracle.{target,run}} and
bugs[].{baseline_sha,fix_sha,fix_source,oracle_test,oracle_match}. To add a new
repo, drop in a manifest + the isolated oracle test files — no code changes.
"""
import argparse, json, os, re, shutil, subprocess, sys, tempfile, urllib.parse, urllib.request
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.exit("bench.py needs pyyaml (pip install pyyaml)")

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from tools.completion_state import SCHEMA_VERSION as COMPLETION_STATE_SCHEMA_VERSION
from tools.completion_state import write_completion_state


def load(project):
    # `--project` is normally a project id resolved under projects/<id>/, but a
    # direct path to a manifest.yaml is also accepted (handy for ad-hoc repos and
    # tests) — least surprise: if it points at an existing .yaml, use it as-is.
    cand = Path(project)
    if cand.suffix in (".yaml", ".yml") and cand.exists():
        mpath = cand
    else:
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


def _as_list(value):
    if value is None:
        return []
    if isinstance(value, list):
        return [str(v) for v in value if str(v).strip()]
    if isinstance(value, str):
        return [part.strip() for part in value.replace(",", " ").split() if part.strip()]
    return [str(value)]


def project_repo_envs(proj):
    """Environment variables accepted as local checkout hints for a project.

    A private/local project can now be driven from either its standalone checkout
    (BUGFIX_BAKEOFF_REPO=/path/to/checkout) or a meta checkout env
    (BUGFIX_BAKEOFF_META_REPO=/path/to/meta) when the manifest also declares
    project.repo_subdir.
    """
    envs = _as_list(proj.get("repo_envs"))
    if not envs:
        envs.extend(["BUGFIX_BAKEOFF_REPO", "BUGFIX_BAKEOFF_META_REPO"])
        envs.append(f"{proj['id'].upper().replace('-', '_')}_REPO")
    seen = set()
    out = []
    for env in envs:
        env = env.strip()
        if env and env not in seen:
            out.append(env)
            seen.add(env)
    return out


def is_git_checkout(path):
    return sh(["git", "-C", str(path), "rev-parse", "--is-inside-work-tree"],
              cwd=path if Path(path).exists() else HERE, quiet=True).stdout.strip() == "true"


def manifest_commits(m):
    commits = []
    for bug in selected_bugs(m):
        for key in ("baseline_sha", "fix_sha"):
            value = str(bug.get(key, "") or "").strip()
            if value:
                commits.append(value)
    return commits


def has_manifest_commits(repo, m):
    commits = manifest_commits(m)
    if not commits:
        return True
    for commit in commits:
        r = sh(["git", "-C", str(repo), "cat-file", "-e", f"{commit}^{{commit}}"],
               cwd=repo, quiet=True)
        if r.returncode != 0:
            return False
    return True


def resolve_repo_dir(m, repo_dir=None):
    """Resolve the checkout for a manifest.

    Returns a JSON-friendly record. `path` is the git checkout that contains the
    manifest's baseline/fix commits. When project.repo_subdir is set, callers may
    pass the meta-repo root; the resolver will use <repo-dir>/<repo_subdir>.
    """
    proj = m["project"]
    errors = []
    source = ""
    base = None
    if repo_dir:
        base = Path(repo_dir).expanduser()
        source = "--repo-dir"
    else:
        for env in project_repo_envs(proj):
            value = os.environ.get(env)
            if value:
                base = Path(value).expanduser()
                source = env
                break
        if base is None and bool(proj.get("local_only", False)) and str(proj.get("repo", "")) == ".":
            base = REPO_ROOT
            source = "repo-root"

    subdir = str(proj.get("repo_subdir", "") or "").strip()
    path = None
    if base is not None:
        if subdir:
            candidate = base / subdir
            if candidate.exists() and is_git_checkout(candidate):
                path = candidate
            elif candidate.exists():
                errors.append(f"repo_subdir is not a git checkout: {candidate}")
            elif base.exists() and is_git_checkout(base):
                # The caller passed a standalone checkout instead of the meta
                # repo root. Only accept it when it contains the manifest's
                # historical commits; otherwise this is probably an
                # uninitialized meta-repo submodule.
                if has_manifest_commits(base, m):
                    path = base
                else:
                    errors.append(
                        f"repo_subdir does not exist under {base}: {subdir}; "
                        "base checkout does not contain the manifest commits"
                    )
            else:
                errors.append(f"repo_subdir does not exist under {base}: {subdir}")
        elif base.exists() and is_git_checkout(base):
            path = base
        elif base.exists():
            errors.append(f"repo_dir is not a git checkout: {base}")
        else:
            errors.append(f"repo_dir does not exist: {base}")

    return {
        "ok": path is not None and not errors,
        "project": proj["id"],
        "path": str(path) if path is not None else "",
        "base": str(base) if base is not None else "",
        "source": source,
        "repo_subdir": subdir,
        "repo_envs": project_repo_envs(proj),
        "errors": errors,
    }


def sh(cmd, cwd, env=None, quiet=False):
    r = subprocess.run(cmd, cwd=cwd, shell=isinstance(cmd, str),
                       env={**os.environ, **(env or {})},
                       capture_output=True, text=True)
    if not quiet and r.returncode != 0:
        sys.stderr.write(r.stdout[-2000:] + r.stderr[-2000:])
    return r


_ORACLE_SECRET = re.compile(
    r"(?im)\b(api[_-]?key|access[_-]?token|authorization|password|secret|token)"
    r"(\s*[:=]\s*)([^\s,;]+)"
)


def command_evidence(result, command, limit=4000):
    """Return bounded, redacted post-run evidence for a deterministic command.

    This is written only by the scorer, after the candidate has completed; it
    must never be included in a candidate prompt.  Keeping the last part of
    the output gives an operator enough context to distinguish an oracle
    mismatch from a compile or harness failure after the scratch tree is
    removed, while avoiding an unbounded result artifact.
    """
    output = (result.stdout or "") + (result.stderr or "")
    output = _ORACLE_SECRET.sub(r"\1\2[REDACTED]", output)
    return {
        "command": command,
        "exit_code": result.returncode,
        "output_tail": output[-limit:],
        "truncated": len(output) > limit,
    }


def materialize(tree, dest, node_modules=None):
    """Copy a candidate tree into dest (tracked files if a git repo, else cp),
    then link a prebuilt node_modules (so we never re-install per score)."""
    tree = Path(tree)
    dest = Path(dest)
    files = sh(["git", "ls-files"], cwd=tree, quiet=True)
    if files.returncode == 0 and files.stdout.strip():
        for rel in files.stdout.splitlines():
            src = tree / rel
            dst = dest / rel
            # Preserve tracked symlinks AS symlinks — never copy their target.
            # A tracked symlink to a directory (e.g. docs/skills -> ../.agents/skills)
            # otherwise makes shutil.copy2 raise IsADirectoryError when the link
            # resolves, which is exactly why host scoring diverged from the docker
            # (copytree) path. Recreating the link is deterministic in both.
            if src.is_symlink():
                dst.parent.mkdir(parents=True, exist_ok=True)
                if dst.is_symlink() or dst.exists():
                    dst.unlink()
                os.symlink(os.readlink(src), dst)
                continue
            # Skip dangling entries and directory gitlinks (submodules): nothing
            # to copy as a regular file.
            if not src.exists() or src.is_dir():
                continue
            dst.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dst)
    else:
        # Repo-history exports are plain directories, not Git checkouts. They
        # may contain ignored build caches produced while the baseline command
        # ran (notably `.artifacts/go` in Kitsoki). Those are neither source nor
        # oracle input and copying them into the scorer's second scratch tree
        # can exhaust disk before the deterministic GREEN leg runs.
        shutil.copytree(tree, dest, dirs_exist_ok=True, ignore=shutil.ignore_patterns(
            "node_modules", ".git", ".artifacts", ".cache", ".temp", "__pycache__",
        ))
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


# Host-error signatures that mean "the harness/environment failed", NOT "the
# model produced a wrong fix". Matched case-insensitively against any event's
# error/last_error text. Each maps to a short, actionable label.
INFRA_ERROR_SIGNATURES = [
    ("identity unknown", "git-identity-missing"),
    ("please tell me who you are", "git-identity-missing"),
    ("--dangerously-skip-permissions cannot be used with root", "root-skip-permissions"),
    ("acceptance failed after", "worker-acceptance-failed"),
    ("no such tool", "mcp-tool-missing"),
    ("no handler registered", "host-handler-missing"),
    ("connection reset", "network"),
    ("connection timed out", "network"),
    ("service unavailable", "provider-5xx"),
    ("bad gateway", "provider-5xx"),
    ("gateway timeout", "provider-5xx"),
]

# A drive that reached one of these states ran the pipeline to a real terminal —
# any failure past here is a MODEL result the oracle should judge, not infra.
TERMINAL_STATES = {"bf.done", "finished"}


def classify_cell(trace):
    """Classify a cell run as an INFRA failure vs a real MODEL result by reading
    the trace. The bake-off verdict (`failed`) lies constantly: a worker that
    never ran, a silent stall, or a host/env error all look identical to "the
    model didn't fix it". This separates them so a sweep is readable and the
    model is only scored on runs where it actually got a fair turn.

    Returns {class, reason, evidence{...}}. `class` is one of:
      infra:worker-never-ran   — zero agent.stream / zero completed calls
      infra:stall              — an agent.call.start with no matching complete,
                                 trace ends there (silent hang, e.g. quota/timeout)
      infra:host-error         — a host/env error (git identity, root perms, …)
      model:result             — reached a terminal state; defer to the oracle
      incomplete               — stopped early, no terminal, no clear infra cause
    """
    if not trace or not os.path.exists(trace):
        return {"class": "infra:no-trace", "reason": "trace file absent", "evidence": {"trace": trace}}

    events = []
    for line in open(trace):
        try:
            events.append(json.loads(line))
        except Exception:
            continue

    starts, completes = {}, set()
    streams = 0
    states = set()
    last_infra_error = None
    last_infra_label = None
    stall_agent = None
    for e in events:
        kind = e.get("kind", "")
        p = e.get("payload", {}) or {}
        sp = e.get("state_path")
        if sp:
            states.add(sp)
        if kind == "agent.stream":
            streams += 1
        elif kind == "agent.call.start":
            # call_id is a top-level event field; agent is in the payload.
            starts[e.get("call_id")] = p.get("agent")
        elif kind == "agent.call.complete":
            completes.add(e.get("call_id"))
        # Scan any error-bearing field for an infra signature.
        for field in ("error", "last_error"):
            txt = p.get(field)
            if isinstance(txt, str) and txt:
                low = txt.lower()
                for sig, label in INFRA_ERROR_SIGNATURES:
                    if sig in low:
                        last_infra_error = txt[:300]
                        last_infra_label = label

    open_calls = [(cid, ag) for cid, ag in starts.items() if cid not in completes]
    reached_terminal = bool(states & TERMINAL_STATES)
    evidence = {
        "agent_streams": streams,
        "agent_calls_started": len(starts),
        "agent_calls_completed": len(completes),
        "states_visited": sorted(states),
        "reached_terminal": reached_terminal,
        "open_calls": [{"call_id": c, "agent": a} for c, a in open_calls],
    }

    # 1. Worker never ran at all.
    if streams == 0 and not completes:
        return {"class": "infra:worker-never-ran",
                "reason": "no agent.stream events and no completed agent calls — the worker never made an LLM call",
                "evidence": evidence}

    # 2. A start with no complete, and it's the tail of the trace → silent stall.
    if open_calls and not reached_terminal:
        last = events[-1] if events else {}
        last_is_open_start = last.get("kind") == "agent.call.start" and \
            last.get("call_id") in dict(open_calls)
        # Even if a couple of trailing world.update events follow, a lone open
        # call near the end is a stall (quota deadlock / MCP tool timeout).
        if last_is_open_start or len(open_calls) >= 1:
            agent = open_calls[-1][1]
            evidence["stalled_agent"] = agent
            return {"class": "infra:stall",
                    "reason": f"agent '{agent}' started but never completed and the run did not reach a terminal state — a silent hang (quota deadlock / tool timeout / killed worker)",
                    "evidence": evidence}

    # 3. A recognized host/env error.
    if last_infra_label:
        evidence["error_label"] = last_infra_label
        evidence["error"] = last_infra_error
        return {"class": "infra:host-error",
                "reason": f"host/environment error ({last_infra_label}) — not a model miss",
                "evidence": evidence}

    # 4. Reached a terminal state: a real model result for the oracle to judge.
    if reached_terminal:
        return {"class": "model:result",
                "reason": "pipeline reached a terminal state; the pass/fail is a real model result — score it with the oracle",
                "evidence": evidence}

    # 5. Stopped early without a terminal state or a recognized infra cause.
    return {"class": "incomplete",
            "reason": "pipeline stopped before a terminal state with no recognized infra cause — inspect the trace",
            "evidence": evidence}


# Containment/match calls (Go test / JS-TS test) where a string literal is
# matched against program OUTPUT. A brittle oracle pins the candidate's exact
# ERROR PROSE in one of these instead of asserting observable behavior. We match
# only these call forms — NOT generic "assert"/"expect" — so a t.Fatalf(...)
# failure-MESSAGE string (which legitimately reads like prose) isn't flagged.
_MATCH_CALL_HINTS = ("strings.contains(", "strings.hasprefix(", "strings.hassuffix(",
                     "strings.containsany(", ".contains(", ".includes(",
                     "tocontain", "tomatch", "assertcontains", "containssubstring")
_STR_LITERAL = None  # compiled lazily (avoid a module-level import-time cost)


def _brittle_prose_literals(text):
    """Heuristic: flag natural-language string literals used in assertions.
    A literal of >=3 alphabetic words, >=18 chars, that is mostly letters/spaces
    is almost certainly a human-readable sentence — pinning it asserts one
    implementation's PROSE, not behavior, and wrongly fails equivalent fixes
    (the bug9 oracle pinned "already checked out by session" and failed a fix
    that said "in use by session"). Returns [(lineno, literal)]."""
    import re
    global _STR_LITERAL
    if _STR_LITERAL is None:
        _STR_LITERAL = re.compile(r'"((?:[^"\\]|\\.)*)"')
    findings = []
    for lineno, line in enumerate(text.splitlines(), 1):
        low = line.lower()
        if not any(h in low for h in _MATCH_CALL_HINTS):
            continue
        for mo in _STR_LITERAL.finditer(line):
            lit = mo.group(1)
            if "%" in lit:  # printf-format string → a message, not a match target
                continue
            words = [w for w in lit.replace("\\n", " ").replace("\\t", " ").split() if w]
            alpha_words = [w for w in words if any(c.isalpha() for c in w)]
            if len(alpha_words) < 3 or len(lit) < 18:
                continue
            naturalness = sum(c.isalpha() or c.isspace() for c in lit) / max(1, len(lit))
            if naturalness > 0.7:
                findings.append((lineno, lit))
    return findings


def lint_oracles(m, bug_ids=None, strict=False):
    """Scan each bug's oracle test for brittle exact-prose assertions and report
    them. Advisory by default (exit 0); --strict exits nonzero on any finding.
    Complements `verify` (RED@baseline / GREEN@fix): RED/GREEN against ONE fix
    can't reveal that the oracle would reject an EQUIVALENT correct fix."""
    results = []
    total = 0
    for b in selected_bugs(m, bug_ids):
        if b.get("reference_only"):
            continue
        oracle = m["_dir"] / b.get("oracle_test", "")
        if not oracle.exists():
            results.append({"bug": b.get("id"), "oracle": str(oracle), "error": "missing"})
            continue
        findings = _brittle_prose_literals(oracle.read_text())
        total += len(findings)
        results.append({
            "bug": b.get("id"),
            "oracle": str(oracle),
            "brittle_prose": [{"line": ln, "literal": lit} for ln, lit in findings],
        })
    out = {
        "ok": total == 0,
        "total_brittle": total,
        "results": results,
        "guidance": ("Assert observable BEHAVIOR, not one implementation's exact "
                     "error prose. Accept any equivalent wording (e.g. a set of "
                     "synonyms) plus the load-bearing identifiers. See ORACLE_GUIDE.md."),
    }
    print(json.dumps(out, indent=2))
    return 1 if (strict and total > 0) else 0


def mine_failures(results_dir, traces_dir, markdown=None):
    """Turn a sweep into a prioritized feedback list. Classifies every scored
    cell (via its trace) and aggregates the patterns that should flow BACK into
    the harness and the stories:
      - infra:* patterns (stalled agents, host-error labels) → fix provisioning
        / the harness, NOT the model.
      - model:result + oracle fail → real model misses; the states they reached
        and the bug they missed are the signal for tightening the story
        (room gates, ticket guidance) the model wandered in.
    This is the loop that lets aggressive runs improve the stories instead of
    just scoring models. Output is advisory (always exit 0)."""
    results = Path(results_dir)
    traces = Path(traces_dir)
    cells_dir = results / "cells" if (results / "cells").is_dir() else results
    by_class = {}
    stalled_agents = {}
    host_labels = {}
    model_misses = []
    cells = []
    for cell_json in sorted(cells_dir.glob("*.json")):
        try:
            cell = json.loads(cell_json.read_text())
        except Exception:
            continue
        proj, bug, cand = cell.get("project"), cell.get("bug"), cell.get("candidate")
        if not (proj and bug and cand):
            continue
        trace = traces / f"{proj}-{bug}-{cand}.jsonl"
        health = classify_cell(str(trace))
        cls = health["class"]
        by_class[cls] = by_class.get(cls, 0) + 1
        ev = health.get("evidence", {})
        if cls == "infra:stall" and ev.get("stalled_agent"):
            stalled_agents[ev["stalled_agent"]] = stalled_agents.get(ev["stalled_agent"], 0) + 1
        if cls == "infra:host-error" and ev.get("error_label"):
            host_labels[ev["error_label"]] = host_labels.get(ev["error_label"], 0) + 1
        oracle_pass = (cell.get("outcome", {}) or {}).get("oracle_pass")
        if cls == "model:result" and oracle_pass is False:
            model_misses.append({"bug": bug, "candidate": cand,
                                 "states_reached": ev.get("states_visited", [])})
        cells.append({"bug": bug, "candidate": cand, "class": cls,
                      "oracle_pass": oracle_pass})

    actions = []
    infra_total = sum(v for k, v in by_class.items() if k.startswith("infra"))
    if infra_total:
        actions.append(f"{infra_total} cell(s) failed on INFRASTRUCTURE, not the model — "
                       "these must not be scored against any candidate. Fix the harness/provisioning first.")
    for label, n in sorted(host_labels.items(), key=lambda x: -x[1]):
        actions.append(f"host-error '{label}' x{n} → bake the fix into provision_vm.sh / the harness.")
    for agent, n in sorted(stalled_agents.items(), key=lambda x: -x[1]):
        actions.append(f"agent '{agent}' stalled x{n} → suspect quota/timeout; check tool_timeout_sec + quota window.")
    if model_misses:
        actions.append(f"{len(model_misses)} genuine model miss(es) reached a terminal state — "
                       "review the ticket's layer/behavior guidance and the room gate the model wandered in "
                       "(this is the story-training signal). See ORACLE_GUIDE.md + the bugfix story.")

    report = {
        "ok": True,
        "cells_scanned": len(cells),
        "by_class": by_class,
        "stalled_agents": stalled_agents,
        "host_error_labels": host_labels,
        "model_misses": model_misses,
        "feedback_actions": actions,
        "cells": cells,
    }
    if markdown:
        lines = ["# Bake-off feedback — mined from a sweep", "",
                 f"Scanned **{len(cells)}** scored cells.", "",
                 "## Health breakdown", ""]
        for cls, n in sorted(by_class.items(), key=lambda x: -x[1]):
            lines.append(f"- `{cls}`: {n}")
        lines += ["", "## Feedback actions (highest-leverage first)", ""]
        lines += [f"{i+1}. {a}" for i, a in enumerate(actions)] or ["- (none — all cells healthy)"]
        Path(markdown).write_text("\n".join(lines) + "\n")
    print(json.dumps(report, indent=2))
    return 0


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
        "baseline_sha": bug.get("baseline_sha", ""),
        "fix_sha": bug.get("fix_sha", ""),
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


def pending_command(project, bug_id, candidate, reason="<reason>"):
    out = (
        ".artifacts/external-bakeoff/results/cells/"
        f"{project}-{bug_id}-{candidate}-kitsoki.json"
    )
    return (
        "python3 tools/bugfix-bakeoff/external/bench.py pending "
        f"--project {project} --bug {bug_id} --candidate {candidate} "
        f"--reason \"{reason}\" --out {out}"
    )


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
    repo_info = resolve_repo_dir(m, repo_dir) if (repo_dir or local_only or proj.get("repo_subdir")) else {
        "ok": False, "path": "", "base": str(Path(repo_dir).expanduser()) if repo_dir else "",
        "source": "--repo-dir" if repo_dir else "", "repo_subdir": str(proj.get("repo_subdir", "") or ""),
        "repo_envs": project_repo_envs(proj), "errors": [],
    }
    repo_path = Path(repo_info["path"]) if repo_info.get("path") else None
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
            env_names = "/".join(project_repo_envs(proj))
            errors.append(f"local_only project needs --repo-dir <checkout> or one of {env_names}")
    elif repo_dir and not repo_path and not repo_info.get("errors"):
        errors.append(f"repo_dir is not a git checkout: {repo_info.get('base', repo_dir)}")

    errors.extend(repo_info.get("errors", []))

    if repo_path and is_git_checkout(repo_path):
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
        "repo_base_dir": repo_info.get("base", ""),
        "repo_dir_source": repo_info.get("source", ""),
        "repo_subdir": repo_info.get("repo_subdir", ""),
        "repo_envs": repo_info.get("repo_envs", []),
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
    cells_dir = (HERE / results_dir / "cells").resolve()
    cells = []
    for f in sorted(cells_dir.glob("*.json")) if cells_dir.exists() else []:
        try:
            cell = json.loads(f.read_text())
        except Exception:
            continue
        cell["_path"] = str(f)
        cells.append(cell)
    return cells, cells_dir


def result_stale_reason(cell, bug):
    expected = bug.get("baseline_sha", "")
    actual = cell.get("baseline_sha", "")
    if expected and not actual:
        return f"missing baseline_sha; expected {expected}"
    if expected and actual != expected:
        return f"baseline_sha {actual} != expected {expected}"
    return ""


def collect_prepared(results_dir, project, selected):
    prepared_dir = ((HERE / results_dir).parent / "prepared").resolve()
    prepared = []
    stale = []
    for f in sorted(prepared_dir.glob("*.json")) if prepared_dir.exists() else []:
        try:
            item = json.loads(f.read_text())
        except Exception:
            continue
        key = (item.get("bug"), item.get("candidate"))
        if item.get("project") == project and key in selected:
            item["_path"] = str(f)
            closed = item.get("workspace_cleanup") == "closed"
            required_paths = (
                ("prompt", "preflight", "score_result", "workspace_cleanup_receipt")
                if closed else
                ("prompt", "worktree", "preflight")
            )
            missing_paths = [
                field for field in required_paths
                if not item.get(field) or not Path(item[field]).exists()
            ]
            if missing_paths:
                item["missing_paths"] = missing_paths
                stale.append(item)
            else:
                prepared.append(item)
    return prepared, stale, prepared_dir


def _prompt_for_audit(item):
    prompt = item.get("prompt", "")
    if not prompt:
        return "", ["metadata missing prompt path"]
    path = Path(prompt)
    if not path.exists():
        return "", [f"prompt path missing: {prompt}"]
    try:
        return path.read_text(), []
    except Exception as e:
        return "", [f"prompt unreadable: {prompt}: {e}"]


def _audit_capsule_handoff(item):
    """Prove a prepared coding path is owned by the native Capsule manager.

    `worktree` remains in the handoff schema for older report consumers, but it
    may only alias the typed managed workspace. This check is deliberately
    filesystem-only and never starts a model or mutates the checkout.
    """
    errors = []
    required = (
        "workspace", "workspace_provider", "capsule_project",
        "capsule_workspace_id", "capsule_definition_id", "capsule_owner",
        "capsule_generation", "capsule_provider",
    )
    for field in required:
        if item.get(field) in (None, ""):
            errors.append(f"metadata missing {field}")
    if errors:
        return errors

    if item.get("workspace_provider") != "pinned":
        errors.append(
            f"workspace_provider is {item.get('workspace_provider')!r}, want 'pinned'"
        )
    workspace = Path(item["workspace"])
    legacy_path = Path(item.get("worktree", ""))
    capsule_project = Path(item["capsule_project"])
    if workspace != legacy_path:
        errors.append("legacy worktree path does not alias workspace")
    try:
        workspace.resolve().relative_to(
            (capsule_project / ".capsules" / "workspaces").resolve()
        )
    except (OSError, ValueError):
        errors.append("workspace escapes the declared Capsule project workspace root")

    generation = item.get("capsule_generation")
    if isinstance(generation, bool) or not isinstance(generation, int) or generation < 1:
        errors.append(f"capsule_generation is not a positive integer: {generation!r}")

    cleanup = item.get("workspace_cleanup", "prepared")
    if cleanup == "closed":
        if workspace.exists():
            errors.append("closed workspace still exists")
        result_path = Path(item.get("score_result", ""))
        try:
            result = json.loads(result_path.read_text())
        except Exception as exc:
            errors.append(f"closed workspace score result unreadable: {result_path}: {exc}")
        else:
            for field in ("project", "bug", "candidate"):
                if result.get(field) != item.get(field):
                    errors.append(f"score result {field} does not match handoff")
            if (result.get("outcome") or {}).get("quality") not in {"solved", "partial", "failed"}:
                errors.append("closed workspace score result has no completed verdict")
        receipt_path = Path(item.get("workspace_cleanup_receipt", ""))
        try:
            receipt = json.loads(receipt_path.read_text())
        except Exception as exc:
            errors.append(f"workspace close receipt unreadable: {receipt_path}: {exc}")
        else:
            expected_receipt = {
                "schema": "capsule-workspace-close/v1",
                "ok": True,
                "id": item.get("capsule_workspace_id"),
                "definition_id": item.get("capsule_definition_id"),
                "provider": item.get("capsule_provider"),
                "owner": item.get("capsule_owner"),
                "state": "closed",
            }
            for field, expected in expected_receipt.items():
                if receipt.get(field) != expected:
                    errors.append(
                        f"workspace close receipt {field}={receipt.get(field)!r}, want {expected!r}"
                    )
            try:
                receipt_project = Path(receipt.get("project", "")).resolve()
                expected_project = capsule_project.resolve()
            except OSError as exc:
                errors.append(f"workspace close receipt project is invalid: {exc}")
            else:
                if receipt_project != expected_project:
                    errors.append("workspace close receipt project does not match handoff")
            close_generation = receipt.get("generation")
            if isinstance(close_generation, bool) or not isinstance(close_generation, int) or close_generation <= generation:
                errors.append("workspace close receipt generation is not newer than the prepared handle")
            if item.get("capsule_close_generation") != close_generation:
                errors.append("handoff close generation does not match close receipt")
        return errors

    if cleanup == "debt":
        errors.append(
            "workspace cleanup debt: " +
            str(item.get("workspace_cleanup_error") or item.get("workspace_cleanup_error_path") or "close failed")
        )
    if cleanup == "kept-debug" and not (workspace / ".kitsoki-capsule-pin").is_file():
        errors.append("debug-kept workspace is missing its Capsule pin sentinel")

    project_sentinel = capsule_project / ".kitsoki-capsule-project"
    try:
        sentinel = json.loads(project_sentinel.read_text())
    except Exception as exc:
        errors.append(f"Capsule project sentinel unreadable: {project_sentinel}: {exc}")
    else:
        if sentinel.get("schema") != "capsule-project/v1":
            errors.append("Capsule project sentinel has an unsupported schema")
        if sentinel.get("kind") != "external-bakeoff":
            errors.append("Capsule project sentinel is not owned by external-bakeoff")

    manifest_path = workspace / ".kitsoki-capsule"
    if not manifest_path.is_file():
        errors.append(f"workspace missing Capsule sentinel: {manifest_path}")
    capsule_manifest_path = workspace / "capsule-manifest.json"
    try:
        capsule_manifest = json.loads(capsule_manifest_path.read_text())
    except Exception as exc:
        errors.append(f"workspace Capsule manifest unreadable: {capsule_manifest_path}: {exc}")
    else:
        if capsule_manifest.get("capsule_name") != item.get("capsule_definition_id"):
            errors.append("workspace Capsule manifest definition id does not match handoff")
        try:
            manifest_workspace = Path(capsule_manifest.get("workspace", "")).resolve()
            handoff_workspace = workspace.resolve()
        except OSError as exc:
            errors.append(f"workspace Capsule manifest path is invalid: {exc}")
        else:
            if manifest_workspace != handoff_workspace:
                errors.append("workspace Capsule manifest path does not match handoff")
        source = capsule_manifest.get("source") or {}
        if source.get("commit") != item.get("baseline_sha"):
            errors.append("workspace Capsule manifest commit does not match baseline_sha")
        environment = capsule_manifest.get("environment") or {}
        if environment.get("workspace_id") != item.get("capsule_workspace_id"):
            errors.append("workspace Capsule manifest id does not match handoff")
        if environment.get("owner") != item.get("capsule_owner"):
            errors.append("workspace Capsule manifest owner does not match handoff")

    definition_path = (
        capsule_project / ".kitsoki" / "capsules" /
        f"{item.get('capsule_definition_id', '')}.yaml"
    )
    try:
        definition = yaml.safe_load(definition_path.read_text()) or {}
    except Exception as exc:
        errors.append(f"Capsule definition unreadable: {definition_path}: {exc}")
    else:
        if definition.get("id") != item.get("capsule_definition_id"):
            errors.append("Capsule definition id does not match handoff")
        source = definition.get("source") or {}
        if source.get("kind") != "pinned":
            errors.append("Capsule definition source is not pinned")
        if source.get("commit") != item.get("baseline_sha"):
            errors.append("Capsule definition commit does not match baseline_sha")
        commands = ((definition.get("policy") or {}).get("commands") or {})
        if "prepare_base" not in commands or "prepare_branch" not in commands:
            errors.append("Capsule definition is missing declared branch preparation commands")
    return errors


def audit_prepared_handoff(m, item):
    """Audit a no-drive handoff without running cargo/npm or calling a model.

    The handoff is what an operator reviews before spending on a live MCP drive.
    It must be actionable enough to review and leak-safe: task context is allowed,
    but hidden oracle paths/content and real-fix hints are not.
    """
    errors = []
    warnings = []
    bug_id = item.get("bug")
    candidate = item.get("candidate")
    bugs = {b.get("id"): b for b in m.get("bugs", [])}
    bug = bugs.get(bug_id)
    if not bug:
        errors.append(f"unknown bug in prepared metadata: {bug_id}")
        bug = {}

    prompt, prompt_errors = _prompt_for_audit(item)
    errors.extend(prompt_errors)
    errors.extend(_audit_capsule_handoff(item))
    for field in ("project", "bug", "candidate", "profile", "worktree", "branch",
                  "baseline_sha", "trace", "thread", "preflight", "score_result"):
        if not item.get(field):
            errors.append(f"metadata missing {field}")
    for field in (("preflight",) if item.get("workspace_cleanup") == "closed" else ("worktree", "preflight")):
        if item.get(field) and not Path(item[field]).exists():
            errors.append(f"{field} path missing: {item[field]}")

    if prompt:
        required_fragments = [
            "Drive ONE kitsoki bug-fix pipeline cell",
            f'profile: "{item.get("profile", "")}"',
            f'ticket_id: "{bug_id}"',
            'workspace_id: ""',
            f'workdir: "{item.get("worktree", "")}"',
            "Do not use shell",
        ]
        for fragment in required_fragments:
            if fragment not in prompt:
                errors.append(f"prompt missing required fragment: {fragment}")
        title = bug.get("title", "")
        if title and title not in prompt:
            warnings.append("prompt does not include bug title")
        ticket = (bug.get("ticket") or "").strip()
        if ticket:
            first_ticket_line = next((line.strip() for line in ticket.splitlines() if line.strip()), "")
            if first_ticket_line and first_ticket_line not in prompt:
                warnings.append("prompt does not include ticket detail")

        leak_terms = []
        for key in ("oracle_test", "fix_sha", "fix_source"):
            value = str(bug.get(key, "") or "")
            if value:
                leak_terms.append((key, value))
        oracle_cfg = bug.get("oracle", {})
        for key in ("target", "run", "match"):
            value = str(oracle_cfg.get(key, "") or "")
            if value:
                leak_terms.append((f"oracle.{key}", value))
        for key, value in leak_terms:
            if value and value in prompt:
                errors.append(f"prompt leaks hidden {key}: {value}")

        oracle_path = m.get("_dir", Path(".")) / bug.get("oracle_test", "")
        if bug.get("oracle_test") and oracle_path.exists():
            oracle_text = oracle_path.read_text()
            snippets = [
                line.strip()
                for line in oracle_text.splitlines()
                if len(line.strip()) >= 40 and not line.strip().startswith("//")
            ][:8]
            for snippet in snippets:
                if snippet in prompt:
                    errors.append("prompt leaks hidden oracle content")
                    break

    return {
        "project": item.get("project"),
        "bug": bug_id,
        "candidate": candidate,
        "ok": not errors,
        "errors": errors,
        "warnings": warnings,
        "metadata": item.get("_path", ""),
        "prompt": item.get("prompt", ""),
        "worktree": item.get("worktree", ""),
    }


def audit_handoffs(m, results_dir="../../../.artifacts/external-bakeoff/results",
                   candidate=None, bug_ids=None, markdown=None):
    plan = build_drive_plan(m, bug_ids=bug_ids, candidate=candidate)
    selected = {(cmd["bug"], cmd["candidate"]) for cmd in plan["commands"]}
    prepared, stale, prepared_dir = collect_prepared(results_dir, m["project"]["id"], selected)
    audits = [audit_prepared_handoff(m, item) for item in prepared]
    prepared_keys = {(p.get("bug"), p.get("candidate")) for p in prepared}
    stale_keys = {(p.get("bug"), p.get("candidate")) for p in stale}
    missing = [
        {"bug": bug, "candidate": cand}
        for bug, cand in sorted(selected - prepared_keys - stale_keys)
    ]
    errors = []
    for item in stale:
        errors.append(f"{item.get('bug')} x {item.get('candidate')}: stale prepared metadata")
    for item in missing:
        errors.append(f"{item['bug']} x {item['candidate']}: no prepared metadata")
    for audit in audits:
        errors.extend(
            f"{audit['bug']} x {audit['candidate']}: {e}"
            for e in audit.get("errors", [])
        )
    out = {
        "ok": not errors,
        "project": m["project"]["id"],
        "selected_cells": len(selected),
        "prepared_cells": len(prepared),
        "stale_prepared_cells": len(stale),
        "missing_prepared_cells": len(missing),
        "prepared_dir": str(prepared_dir),
        "audits": audits,
        "stale": stale,
        "missing": missing,
        "errors": errors,
    }
    if markdown:
        write_handoff_audit_markdown(out, Path(markdown))
        out["markdown"] = markdown
    print(json.dumps(out, indent=2))
    return 0 if out["ok"] else 1


def write_handoff_audit_markdown(report, markdown):
    markdown.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        f"# {report['project']} prepared handoff audit",
        "",
        f"Status: {'ready' if report.get('ok') else 'blocked'}",
        f"Selected cells: {report.get('selected_cells', 0)}",
        f"Prepared cells: {report.get('prepared_cells', 0)}",
        f"Stale prepared cells: {report.get('stale_prepared_cells', 0)}",
        f"Missing prepared cells: {report.get('missing_prepared_cells', 0)}",
        "",
        "## What This Checks",
        "",
        "- Prepared metadata points at existing prompt, worktree, and preflight files.",
        "- The MCP prompt includes the bug id, user-facing bug context, worktree, profile, and no-shell drive instructions.",
        "- The MCP prompt does not include hidden oracle paths/content or real-fix commit/source hints.",
        "",
        "## Cells",
        "",
    ]
    for audit in report.get("audits", []):
        lines.append(
            f"- `{audit.get('bug')}` x `{audit.get('candidate')}`: "
            f"{'ready' if audit.get('ok') else 'blocked'}; "
            f"metadata `{audit.get('metadata')}`, prompt `{audit.get('prompt')}`"
        )
        for warning in audit.get("warnings", []):
            lines.append(f"  warning: {warning}")
        for error in audit.get("errors", []):
            lines.append(f"  error: {error}")
    if report.get("stale"):
        lines.extend(["", "## Stale Metadata", ""])
        for item in report["stale"]:
            lines.append(f"- `{item.get('bug')}` x `{item.get('candidate')}`: `{item.get('_path')}`")
    if report.get("missing"):
        lines.extend(["", "## Missing Metadata", ""])
        for item in report["missing"]:
            lines.append(f"- `{item.get('bug')}` x `{item.get('candidate')}`")
    if report.get("errors"):
        lines.extend(["", "## Errors", ""])
        lines.extend(f"- {e}" for e in report["errors"])
    markdown.write_text("\n".join(lines) + "\n")


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


def build_readiness_report(m, repo_dir=None, candidate=None, candidates_path=None,
                           bug_ids=None,
                           results_dir="../../../.artifacts/external-bakeoff/results",
                           armed=False):
    pre = build_preflight(m, repo_dir=repo_dir, candidate=candidate,
                          candidates_path=candidates_path, bug_ids=bug_ids)
    plan = build_drive_plan(m, bug_ids=bug_ids, candidate=candidate, repo_dir=repo_dir)
    cells, cells_dir = collect_cells(results_dir)
    selected = {(cmd["bug"], cmd["candidate"]) for cmd in plan["commands"]}
    matching = []
    stale_results = []
    bugs_by_id = {b.get("id"): b for b in m.get("bugs", [])}
    for cell in cells:
        key = (cell.get("bug"), cell.get("candidate"))
        if key in selected and cell.get("project") == m["project"]["id"]:
            reason = result_stale_reason(cell, bugs_by_id.get(cell.get("bug"), {}))
            if reason:
                cell["stale_reason"] = reason
                stale_results.append(cell)
            else:
                matching.append(cell)
    completed = {(c.get("bug"), c.get("candidate")) for c in matching}
    prepared, stale_prepared, prepared_dir = collect_prepared(results_dir, m["project"]["id"], selected)
    prepared_keys = {(p.get("bug"), p.get("candidate")) for p in prepared}
    command_by_key = {
        (cmd["bug"], cmd["candidate"]): cmd["command"]
        for cmd in plan["commands"]
    }
    for item in stale_prepared:
        key = (item.get("bug"), item.get("candidate"))
        if key in command_by_key:
            item["command"] = command_by_key[key].replace(" --score", " --no-drive")
    missing = [
        {
            "bug": cmd["bug"],
            "candidate": cmd["candidate"],
            "command": cmd["command"],
            "pending_command": pending_command(m["project"]["id"], cmd["bug"], cmd["candidate"]),
        }
        for cmd in plan["commands"]
        if (cmd["bug"], cmd["candidate"]) not in completed
    ]
    unprepared = [
        {
            "bug": cmd["bug"],
            "candidate": cmd["candidate"],
            "command": cmd["command"].replace(" --score", " --no-drive"),
        }
        for cmd in plan["commands"]
        if (cmd["bug"], cmd["candidate"]) not in prepared_keys
    ]
    result_summary = {
        "cells_dir": str(cells_dir),
        "selected_cells": len(plan["commands"]),
        "scored_cells": len(matching),
        "missing_cells": len(missing),
        "stale_result_cells": len(stale_results),
        "prepared_cells": len(prepared),
        "stale_prepared_cells": len(stale_prepared),
        "unprepared_cells": len(unprepared),
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
    if unprepared:
        next_actions.append("optional: run the listed drive_cell.sh --no-drive commands to prepare missing handoff metadata before live spend")
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
        "prepared": {
            "dir": str(prepared_dir),
            "cells": prepared,
            "stale": stale_prepared,
        },
        "unprepared": unprepared,
        "missing": missing,
        "stale_results": stale_results,
        "next_actions": next_actions,
    }
    return out


def readiness(m, repo_dir=None, candidate=None, candidates_path=None, bug_ids=None,
              results_dir="../../../.artifacts/external-bakeoff/results", markdown=None,
              armed=False):
    """Free operator-facing audit for the selected repo-history matrix.

    This command does not run cargo/npm or call a model. It composes preflight,
    drive-plan, and existing result artifacts into one report so an operator can
    see whether setup is ready, which cells are still missing, and what command
    to run next.
    """
    out = build_readiness_report(m, repo_dir=repo_dir, candidate=candidate,
                                 candidates_path=candidates_path, bug_ids=bug_ids,
                                 results_dir=results_dir, armed=armed)
    if markdown:
        write_readiness_markdown(out, Path(markdown))
        out["markdown"] = markdown
    print(json.dumps(out, indent=2))
    return 0 if out["preflight"]["ok"] else 1


def write_readiness_markdown(report, markdown):
    pre = report["preflight"]
    plan = report["drive_plan"]
    results = report["results"]
    arming = report.get("arming", {})
    missing_cells = results["missing_cells"]
    scored_cells = results["scored_cells"]
    selected_cells = results["selected_cells"]
    def noun(n, singular, plural=None):
        return singular if n == 1 else (plural or f"{singular}s")
    markdown.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        f"# {report['project']} repo-history readiness",
        "",
        f"Preflight: {'ready' if pre.get('ok') else 'blocked'}",
        f"Arming: {'verified' if arming.get('verified') else 'not captured'}",
        f"Selected cells: {selected_cells}",
        f"Scored cells: {scored_cells}",
        f"Missing cells: {missing_cells}",
        f"Stale result cells: {results.get('stale_result_cells', 0)}",
        f"Prepared cells: {results.get('prepared_cells', 0)}",
        f"Stale prepared cells: {results.get('stale_prepared_cells', 0)}",
        f"Unprepared cells: {results.get('unprepared_cells', 0)}",
        "",
        "## What This Proves",
        "",
    ]
    if pre.get("ok"):
        lines.append("- The project manifest, selected bugs, local checkout, and selected candidates passed preflight.")
    else:
        lines.append("- Preflight is blocked; fix setup errors before arming fixtures or driving live cells.")
    if arming.get("verified"):
        lines.append("- The selected fixtures were verified RED at the historical baseline and GREEN at the real fix.")
    else:
        lines.append("- RED/GREEN arming was not captured in this readiness command; run the verify command before live cells.")
    if missing_cells:
        selected_label = noun(selected_cells, "selected cell")
        result_label = noun(missing_cells, "result artifact")
        verb = "has" if missing_cells == 1 else "have"
        lines.append(
            f"- {missing_cells} of {selected_cells} {selected_label} {verb} no {result_label} yet; "
            "that means not attempted or not recorded, not failed."
        )
    elif selected_cells:
        lines.append("- Every selected cell has a scored or pending result artifact.")
    lines.extend([
        "",
        "## Setup",
        "",
    ])
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
        "## Prepared Cells",
        "",
    ])
    prepared = report.get("prepared", {}).get("cells", [])
    if prepared:
        for p in prepared:
            lines.append(
                f"- `{p.get('bug')}` x `{p.get('candidate')}`: "
                f"metadata `{p.get('_path')}`, prompt `{p.get('prompt')}`, "
                f"worktree `{p.get('worktree')}`, trace `{p.get('trace')}`"
            )
    else:
        lines.append("No prepared-cell metadata found yet. Run `drive_cell.sh --no-drive` or `make history-smoke` with first-cell preparation enabled.")
    stale = report.get("prepared", {}).get("stale", [])
    if stale:
        lines.extend(["", "## Stale Prepared Metadata", ""])
        lines.append("These prepared-cell records point at missing prompt/worktree/preflight paths. Re-run the listed `--no-drive` commands:")
        lines.append("")
        for p in stale:
            missing = ", ".join(p.get("missing_paths", []))
            line = f"- `{p.get('bug')}` x `{p.get('candidate')}`: metadata `{p.get('_path')}` missing {missing}"
            if p.get("command"):
                line += f"; refresh with `{p.get('command')}`"
            lines.append(line)
    if report.get("unprepared"):
        lines.extend(["", "## Unprepared Cells", ""])
        lines.append("These selected cells do not have prepared handoff metadata yet. This is optional before `--score`, but useful for review:")
        lines.append("")
        for item in report["unprepared"]:
            lines.append(f"- `{item['bug']}` x `{item['candidate']}`: `{item['command']}`")
    lines.extend([
        "",
        "## Missing Cells",
        "",
    ])
    if report.get("missing"):
        for m in report["missing"]:
            lines.append(f"- `{m['bug']}` x `{m['candidate']}`: `{m['command']}`")
    else:
        lines.append("All selected cells have scored or pending results.")
    if report.get("missing"):
        lines.extend(["", "## Pending Alternatives", ""])
        lines.append("Use these only when a provider/profile/infrastructure blocker prevents a real model attempt:")
        lines.append("")
        for m in report["missing"]:
            lines.append(f"- `{m['bug']}` x `{m['candidate']}`: `{m['pending_command']}`")
    if report.get("stale_results"):
        lines.extend(["", "## Stale Result Artifacts", ""])
        lines.append("These selected result artifacts do not match the current manifest baseline and are not counted as scored:")
        lines.append("")
        for cell in report["stale_results"]:
            lines.append(
                f"- `{cell.get('bug')}` x `{cell.get('candidate')}`: "
                f"`{cell.get('_path')}` ({cell.get('stale_reason')})"
            )
    lines.extend(["", "## Next Actions", ""])
    lines.extend(f"- {a}" for a in report.get("next_actions", []))
    markdown.write_text("\n".join(lines) + "\n")


def build_completion_report(m, repo_dir=None, candidate=None, candidates_path=None,
                            bug_ids=None,
                            results_dir="../../../.artifacts/external-bakeoff/results",
                            armed=False):
    readiness_report = build_readiness_report(
        m,
        repo_dir=repo_dir,
        candidate=candidate,
        candidates_path=candidates_path,
        bug_ids=bug_ids,
        results_dir=results_dir,
        armed=armed,
    )
    results = readiness_report["results"]
    preflight_ok = bool(readiness_report["preflight"].get("ok"))
    arming_verified = bool(readiness_report["arming"].get("verified"))
    selected = results.get("selected_cells", 0)
    missing = results.get("missing_cells", 0)
    stale_results = results.get("stale_result_cells", 0)
    stale_prepared = results.get("stale_prepared_cells", 0)
    unprepared = results.get("unprepared_cells", 0)
    prepared = results.get("prepared_cells", 0)
    no_cost_ready = preflight_ok and arming_verified and selected > 0
    ready_to_drive = no_cost_ready and stale_prepared == 0 and unprepared == 0
    result_evidence_complete = no_cost_ready and missing == 0 and stale_results == 0
    rollup = (results.get("rollup", {}) or {}).get("by_candidate", {})
    pending = sum(v.get("pending", 0) for v in rollup.values())
    attempted = sum(v.get("attempted", 0) for v in rollup.values())
    solved = sum(v.get("solved", 0) for v in rollup.values())
    partial = sum(v.get("partial", 0) for v in rollup.values())
    failed = sum(v.get("failed", 0) for v in rollup.values())
    live_scored = result_evidence_complete and pending == 0 and attempted == selected
    blockers = []
    if not preflight_ok:
        blockers.append("preflight is blocked")
    if not arming_verified:
        blockers.append("selected fixtures have not been verified RED@baseline/GREEN@fix in this report")
    if selected == 0:
        blockers.append("no selected cells")
    if stale_prepared:
        blockers.append(f"{stale_prepared} prepared handoff(s) are stale")
    if unprepared:
        blockers.append(f"{unprepared} selected cell(s) have no prepared handoff metadata")
    if stale_results:
        blockers.append(f"{stale_results} selected result artifact(s) are stale")
    if missing:
        blockers.append(f"{missing} selected cell(s) still need a scored or pending result")
    if result_evidence_complete and pending:
        blockers.append(f"{pending} selected cell(s) are pending, so this is not a live scored capability result")

    status = "complete"
    if not result_evidence_complete:
        status = "ready-to-drive" if ready_to_drive else "blocked"
    elif pending:
        status = "complete-with-pending"
    completed = status in {"complete", "complete-with-pending"}
    needs_manual_drive = (missing > 0 or stale_results > 0)
    can_auto_repair = (result_evidence_complete is False) and (
        (stale_prepared > 0 or unprepared > 0) and
        not needs_manual_drive and
        preflight_ok and
        arming_verified
    )

    out = {
        "ok": result_evidence_complete,
        "project": m["project"]["id"],
        "status": status,
        "completed": completed,
        "requires_drive": needs_manual_drive,
        "repairable": can_auto_repair,
        "checks": {
            "preflight_ok": preflight_ok,
            "arming_verified": arming_verified,
            "no_cost_ready": no_cost_ready,
            "ready_to_drive": ready_to_drive,
            "result_evidence_complete": result_evidence_complete,
            "live_scored": live_scored,
        },
        "results": {
            "selected_cells": selected,
            "prepared_cells": prepared,
            "unprepared_cells": unprepared,
            "stale_prepared_cells": stale_prepared,
            "result_cells": results.get("scored_cells", 0),
            "missing_cells": missing,
            "stale_result_cells": stale_results,
            "attempted_cells": attempted,
            "pending_cells": pending,
            "solved_cells": solved,
            "partial_cells": partial,
            "failed_cells": failed,
        },
        "blockers": blockers,
        "drive_commands": readiness_report.get("missing", []),
        "pending_commands": [
            {
                "bug": item["bug"],
                "candidate": item["candidate"],
                "command": item["pending_command"],
            }
            for item in readiness_report.get("missing", [])
        ],
        "readiness": readiness_report,
    }
    return out


def completion(m, repo_dir=None, candidate=None, candidates_path=None, bug_ids=None,
               results_dir="../../../.artifacts/external-bakeoff/results",
               markdown=None, armed=False, require_result_evidence=False,
               require_live_scored=False):
    report = build_completion_report(
        m,
        repo_dir=repo_dir,
        candidate=candidate,
        candidates_path=candidates_path,
        bug_ids=bug_ids,
        results_dir=results_dir,
        armed=armed,
    )
    if markdown:
        write_completion_markdown(report, Path(markdown))
        report["markdown"] = markdown
    print(json.dumps(report, indent=2))
    checks = report["checks"]
    if not checks["preflight_ok"]:
        return 1
    if require_result_evidence and not checks["result_evidence_complete"]:
        return 1
    if require_live_scored and not checks["live_scored"]:
        return 1
    return 0


def write_completion_markdown(report, markdown):
    markdown.parent.mkdir(parents=True, exist_ok=True)
    checks = report["checks"]
    results = report["results"]
    lines = [
        f"# {report['project']} repo-history completion",
        "",
        f"Status: {report['status']}",
        f"Completed: {'yes' if report['completed'] else 'no'}",
        f"No-cost ready: {'yes' if checks['no_cost_ready'] else 'no'}",
        f"Ready to drive live: {'yes' if checks['ready_to_drive'] else 'no'}",
        f"Result evidence complete: {'yes' if checks['result_evidence_complete'] else 'no'}",
        f"Live scored capability result: {'yes' if checks['live_scored'] else 'no'}",
        f"Requires additional live drive: {'yes' if report['requires_drive'] else 'no'}",
        f"Auto-repairable prepared-handshake issues: {'yes' if report['repairable'] else 'no'}",
        "",
        "## Cell Counts",
        "",
        f"- Selected cells: {results['selected_cells']}",
        f"- Prepared cells: {results['prepared_cells']}",
        f"- Unprepared cells: {results['unprepared_cells']}",
        f"- Stale prepared cells: {results['stale_prepared_cells']}",
        f"- Result cells: {results['result_cells']}",
        f"- Missing cells: {results['missing_cells']}",
        f"- Stale result cells: {results['stale_result_cells']}",
        f"- Attempted cells: {results['attempted_cells']}",
        f"- Pending cells: {results['pending_cells']}",
        f"- Solved cells: {results['solved_cells']}",
        f"- Partial cells: {results['partial_cells']}",
        f"- Failed cells: {results['failed_cells']}",
        "",
        "## Blockers",
        "",
    ]
    if report.get("blockers"):
        lines.extend(f"- {item}" for item in report["blockers"])
    else:
        lines.append("None.")
    if report.get("drive_commands"):
        lines.extend(["", "## Drive Commands", ""])
        for item in report["drive_commands"]:
            lines.append(f"- `{item['bug']}` x `{item['candidate']}`: `{item['command']}`")
    if report.get("pending_commands"):
        lines.extend(["", "## Pending Commands", ""])
        lines.append("Use these only for a pre-attempt provider/profile/infrastructure blocker:")
        lines.append("")
        for item in report["pending_commands"]:
            lines.append(f"- `{item['bug']}` x `{item['candidate']}`: `{item['command']}`")
    lines.extend([
        "",
        "## Interpretation",
        "",
        "- `No-cost ready` means preflight passed, selected fixtures were armed, and the matrix is non-empty.",
        "- `Ready to drive live` additionally means every selected cell has fresh prepared handoff metadata.",
        "- `Result evidence complete` means every selected cell has a current scored or pending result artifact.",
        "- `Live scored capability result` requires current non-pending result artifacts for every selected cell.",
    ])
    markdown.write_text("\n".join(lines) + "\n")


def score(m, bug, tree, out, candidate, treatment, trace=None, candidates_path=None,
          completion_state=None):
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
                "baseline_sha": bug.get("baseline_sha", ""),
                "fix_sha": bug.get("fix_sha", ""),
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
                # The oracle is intentionally hidden from the candidate, but a
                # finished calibration needs enough deterministic evidence to
                # diagnose a fail after its private scratch tree is deleted.
                # This result is a post-run operator artifact, never prompt
                # material. Keep it bounded and redact common credential forms.
                "evidence": {"oracle": command_evidence(oracle_r, run_cmd)},
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

        if completion_state:
            tm = tm if out else read_trace_metrics(trace)
            write_completion_state(
                completion_state,
                verdict=quality,
                health="model:result",
                metrics={
                    "cost_usd": tm["cost_usd"],
                    "total_tokens": tm["total_tokens"],
                    "agent_calls": tm["agent_calls"],
                },
                evidence_refs=[trace] if trace else [],
                cell_id=f"{m['project']['id']}-{bug['id']}-{candidate}",
                job_type="bugfix",
                target_id=m["project"]["id"],
                variant_id=candidate,
                axis={"bug": bug["id"]},
                trace_ref=trace or "",
                notes="external oracle; suite_pass is the SECONDARY signal",
            )
            sys.stderr.write(f"[bench] wrote completion-state {completion_state}\n")
        return 0 if oracle_pass else 1
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def verify(m, only_bug, repo_dir, completion_state=None):
    """Clone (or reuse), and for each fixture assert RED@baseline, GREEN@fix."""
    if m.get("kind") == "arena_cost_corpus":
        return verify_arena_corpus(m)

    proj = m["project"]
    bugs = selected_bugs(m, only_bug)
    repo_info = resolve_repo_dir(m, repo_dir) if (repo_dir or proj.get("local_only") or proj.get("repo_subdir")) else {
        "path": "", "errors": [],
    }
    if proj.get("local_only") and not repo_info.get("path"):
        sys.stderr.write("\n".join(repo_info.get("errors") or [
            "local_only project needs --repo-dir or a configured repo env",
        ]) + "\n")
        if completion_state:
            write_completion_state(
                completion_state, verdict="blocked", health="infra:repo-dir-missing",
                job_type="bugfix", target_id=proj["id"],
                notes="local_only project needs a resolvable checkout",
            )
        return 1
    tmp = None
    if repo_info.get("path"):
        # Never operate directly on a caller's checkout. The GREEN proof checks
        # out the real fix's source paths through git; doing that against the
        # source checkout can dirty its index/worktree. A local mirror is cheap
        # and gives the verifier a private git directory to mutate.
        src = Path(repo_info["path"])
        tmp = Path(tempfile.mkdtemp(prefix="bench-repo-"))
        repo = tmp / f"{proj['id']}-mirror"
        # --no-hardlinks: repo_dir may be a read-only bind mount (e.g. inside the
        # paired-task arena container) on a different device than tmp, so
        # --local's default hardlinking fails with "Invalid cross-device link".
        r = sh(["git", "clone", "--local", "--no-hardlinks", "--no-checkout", "-q", str(src), str(repo)], cwd=tmp)
        if r.returncode != 0:
            sys.stderr.write(r.stdout[-2000:] + r.stderr[-2000:])
            if completion_state:
                write_completion_state(
                    completion_state, verdict="blocked", health="infra:clone-failed",
                    job_type="bugfix", target_id=proj["id"],
                    notes=(r.stdout[-500:] + r.stderr[-500:]).strip(),
                )
            return 1
    else:
        tmp = Path(tempfile.mkdtemp(prefix="bench-repo-"))
        repo = tmp / proj["id"]
        sh(["git", "init", "-q", str(repo)], cwd=tmp)
        sh(["git", "remote", "add", "origin", proj["repo"]], cwd=repo)

    ok = True
    try:
        # fetch all needed commits + install once
        if not repo_info.get("path"):
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
    if completion_state:
        write_completion_state(
            completion_state,
            verdict="armed" if ok else "failed",
            health="model:result",
            job_type="bugfix",
            target_id=proj["id"],
            axis={"bug": ",".join(b["id"] for b in bugs)},
            notes="RED@baseline/GREEN@fix arming proof" if ok else
                  "one or more fixtures failed to arm (see stdout for per-bug OK/BAD)",
        )
    return 0 if ok else 1


def verify_arena_corpus(m):
    """Verify the frozen arena corpus manifest.

    The corpus can mix heavyweight external-bakeoff bug fixtures with cheap
    repo-history content oracles. Keep this in bench.py because WB.1's replay
    gate calls this verifier directly.
    """
    tasks = m.get("tasks") or []
    ok = True
    for task in tasks:
        task_id = task.get("id", "<missing-id>")
        oracle = task.get("oracle") or {}
        kind = oracle.get("kind")
        if kind == "github_content":
            armed = verify_github_content_task(task, oracle)
        elif kind == "external_bakeoff":
            project = oracle.get("project")
            bug = oracle.get("bug")
            if not project or not bug:
                print(f"BAD {task_id}: external_bakeoff oracle needs project and bug")
                armed = False
            else:
                pm = load(project)
                armed = verify(pm, bug, None) == 0
        else:
            print(f"BAD {task_id}: unknown oracle kind {kind!r}")
            armed = False
        ok = ok and armed
    return 0 if ok else 1


def verify_github_content_task(task, oracle):
    task_id = task.get("id", "<missing-id>")
    repo = oracle.get("repo")
    file_path = oracle.get("file")
    required = oracle.get("required_text")
    baseline_sha = task.get("baseline_sha")
    fix_sha = task.get("fix_sha")
    if not all([repo, file_path, required, baseline_sha, fix_sha]):
        print(f"BAD {task_id}: github_content oracle needs repo, file, required_text, baseline_sha, fix_sha")
        return False
    baseline = fetch_github_raw(repo, baseline_sha, file_path)
    fixed = fetch_github_raw(repo, fix_sha, file_path)
    red = baseline is None or required not in baseline
    green = fixed is not None and required in fixed
    armed = red and green
    print(f"{'OK ' if armed else 'BAD'} {task_id}: "
          f"baseline={'RED' if red else 'GREEN'} (want RED), "
          f"real-fix={'GREEN' if green else 'RED'} (want GREEN)")
    return armed


def fetch_github_raw(repo, sha, file_path):
    quoted = urllib.parse.quote(str(file_path))
    url = f"https://raw.githubusercontent.com/{repo}/{sha}/{quoted}"
    req = urllib.request.Request(url, headers={"User-Agent": "kitsoki-bench-corpus"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.read().decode("utf-8", errors="replace")
    except Exception:
        return None


def load_project_manifests(project_ids=None):
    ids = split_csv(project_ids)
    if ids:
        return [load(project_id) for project_id in ids]
    manifests = sorted((HERE / "projects").glob("*/manifest.yaml"))
    return [load(str(path)) for path in manifests]


def repo_history_capsule_record(m, bug):
    proj = m["project"]
    oracle_cfg = {**proj.get("oracle", {}), **bug.get("oracle", {})}
    local_only = bool(proj.get("local_only", False))
    repo_subdir = str(proj.get("repo_subdir", "") or "")
    capsule_ref = f"repo-history/{proj['id']}/{bug['id']}"
    verify_cmd = f"python3 tools/bugfix-bakeoff/external/bench.py verify --project {proj['id']} --bug {bug['id']}"
    readiness_cmd = (
        "python3 tools/bugfix-bakeoff/external/bench.py readiness "
        f"--project {proj['id']} --bug {bug['id']} --candidate <candidate> --armed"
    )
    if local_only:
        repo_hint = f"${project_repo_envs(proj)[0]}" if project_repo_envs(proj) else "<repo>"
        verify_cmd += f" --repo-dir {repo_hint}"
        readiness_cmd += f" --repo-dir {repo_hint}"
    repo_modes = ["single-repo"]
    if repo_subdir:
        repo_modes.append("meta-repo")
    environment = repo_history_environment_contract(proj, bug, oracle_cfg)
    return {
        "id": f"{proj['id']}/{bug['id']}",
        "capsule_ref": capsule_ref,
        "kind": "repo-history",
        "materializer": "bugfix-bakeoff",
        "project": proj["id"],
        "bug": bug["id"],
        "title": bug.get("title", bug["id"]),
        "ticket": bug.get("issue_url") or bug.get("pr_url") or bug.get("ticket_ref") or "",
        "baseline_sha": bug.get("baseline_sha", ""),
        "fix_sha": bug.get("fix_sha", ""),
        "fix_source": bug.get("fix_source", ""),
        "oracle_test": bug.get("oracle_test", ""),
        "oracle_target": oracle_cfg.get("target", ""),
        "oracle_run": oracle_cfg.get("run", ""),
        "oracle_match": bug.get("oracle_match", ""),
        "suite_enabled": bool(proj.get("test_cmd") and proj.get("suite", True)),
        "local_only": local_only,
        "repo": proj.get("repo", "."),
        "repo_envs": project_repo_envs(proj),
        "repo_subdir": repo_subdir,
        "repo_modes": repo_modes,
        "environment": environment,
        "executor_contract": {
            "schema": "capsule-executor-contract/v1",
            "provider": "container",
            "materializer": "bugfix-bakeoff",
            "completion_state": "completion-state/v1",
            "verify_command": verify_cmd,
            "readiness_command": readiness_cmd,
        },
        "source_manifest": str(Path(m.get("_dir", "")) / "manifest.yaml") if m.get("_dir") else "",
        "verify_command": verify_cmd,
        "readiness_command": readiness_cmd,
        "green_red_green": [
            "GREEN: project checkout and ordinary suite/deps are ready",
            "RED: overlay hidden oracle at baseline_sha and require failure",
            "GREEN: overlay the real fix source from fix_sha and require pass",
            "GREEN: score a candidate worktree with the same hidden oracle",
        ],
    }


def repo_history_environment_contract(proj, bug, oracle_cfg):
    """Project the repo-history harness onto Capsule CI's env/executor vocabulary.

    Repo-history materialization still lives in this Python harness because it
    owns pinned upstream repos and hidden oracle overlays. The returned shape is
    intentionally the same checked-in environment contract used by Capsule CI so
    the catalog can be reasoned about, locked, and eventually moved behind the
    common executor provider without changing every manifest.
    """
    language = str(proj.get("language", "") or "").lower()
    caches = []
    lockfiles = []
    image = "ubuntu:24.04"
    if language == "javascript":
        node_min = str(proj.get("node_min", "") or "22")
        image = f"node:{node_min}-bookworm"
        lockfiles.extend(["package-lock.json", "npm-shrinkwrap.json", "pnpm-lock.yaml", "yarn.lock"])
        caches.append({"id": "node-modules", "scope": "project", "mode": "read_write"})
    elif language == "rust":
        image = "rust:1-bookworm"
        lockfiles.append("Cargo.lock")
        caches.append({"id": "cargo", "scope": "project", "mode": "read_write"})
    elif language == "go":
        image = "golang:1-bookworm"
        lockfiles.append("go.sum")
        caches.append({"id": "go-build", "scope": "project", "mode": "read_write"})
    return {
        "schema": "capsule-environment/v1",
        "id": f"repo-history-{proj['id']}",
        "source": {"image": image},
        "lockfiles": lockfiles,
        "bootstrap": {"command": str(proj.get("install", "") or "").strip()},
        "network": "replay",
        "caches": caches,
        "sandbox": "container",
        "verifier_overlay": {
            "visibility": "verifier",
            "oracle_target": oracle_cfg.get("target", ""),
            "oracle_run": oracle_cfg.get("run", ""),
            "oracle_test": bug.get("oracle_test", ""),
        },
    }


def repo_history_capsules(project_ids=None, markdown=False):
    manifests = load_project_manifests(project_ids)
    capsules = []
    projects = {}
    for m in manifests:
        proj = m["project"]
        project_capsules = [
            repo_history_capsule_record(m, bug)
            for bug in selected_bugs(m)
        ]
        capsules.extend(project_capsules)
        projects[proj["id"]] = {
            "id": proj["id"],
            "title": proj.get("title", ""),
            "local_only": bool(proj.get("local_only", False)),
            "repo": proj.get("repo", "."),
            "repo_envs": project_repo_envs(proj),
            "repo_subdir": str(proj.get("repo_subdir", "") or ""),
            "capsules": len(project_capsules),
        }
    out = {
        "ok": True,
        "count": len(capsules),
        "projects": projects,
        "capsules": capsules,
        "commands": {
            "list": "python3 tools/bugfix-bakeoff/external/bench.py capsules",
            "verify_project": "python3 tools/bugfix-bakeoff/external/bench.py verify --project <project> [--repo-dir <checkout-or-meta-root>]",
            "readiness": "make history-smoke HISTORY_PROJECT=<project> HISTORY_REPO_DIR=<checkout-or-meta-root> HISTORY_BUGS=<ids> HISTORY_CANDIDATES=<candidate>",
        },
    }
    if markdown:
        lines = [
            "# Repo History Capsules",
            "",
            f"Promoted capsules: **{len(capsules)}**",
            "",
            "| Capsule | Ticket / PR | Baseline | Real fix | Executor | Repo modes |",
            "|---|---|---|---|---|---|",
        ]
        for c in capsules:
            ticket = c["ticket"] or "(manifest)"
            modes = ", ".join(c["repo_modes"])
            lines.append(
                f"| `{c['capsule_ref']}` | {ticket} | `{c['baseline_sha'][:12]}` | "
                f"`{c['fix_sha'][:12]}` | {c['materializer']} | {modes} |"
            )
        lines.extend([
            "",
            "These are capsules in the shared Kitsoki sense: named, reusable, "
            "deterministically verified repository states. They currently use "
            "the repo-history harness materializer because core `kitsoki capsule "
            "open` is still limited to local synthetic fixtures.",
            "",
            "Every repo-history capsule uses the same GREEN/RED/GREEN discipline: "
            "ordinary repo setup is ready, the hidden oracle fails at the bug "
            "baseline, the real maintainer fix passes it, and candidate fixes are "
            "scored with that same oracle.",
        ])
        print("\n".join(lines))
    else:
        print(json.dumps(out, indent=2))
    return 0


# Compatibility for older harness callers. New code should use
# repo_history_capsule_record/repo_history_capsules and the `capsules` command.
oracle_capsule_record = repo_history_capsule_record
oracle_capsules = repo_history_capsules


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
    s.add_argument("--completion-state",
                   help="write a schema-conformant completion-state JSON here "
                        "(schemas/completion-state.schema.json) for arena's bugfix plugin to read")
    v = sub.add_parser("verify")
    v.add_argument("manifest", nargs="?",
                   help="direct project or arena corpus manifest path")
    v.add_argument("--project",
                   help="project id under projects/<id>/, or direct manifest path")
    v.add_argument("--bug", action="append",
                   help="bug id to verify; repeat or pass comma-separated ids to scope the matrix")
    v.add_argument("--repo-dir", help="prebuilt clone with node_modules to reuse")
    v.add_argument("--completion-state",
                   help="write a schema-conformant completion-state JSON here "
                        "(schemas/completion-state.schema.json) for arena's bugfix plugin to read")
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
    cp = sub.add_parser("completion")
    cp.add_argument("--project", required=True)
    cp.add_argument("--bug", action="append", required=True,
                    help="bug id to audit; repeat or pass comma-separated ids")
    cp.add_argument("--candidate", action="append", required=True,
                    help="candidate key to audit; repeat or pass comma-separated keys")
    cp.add_argument("--repo-dir", help="local checkout for private/local_only projects")
    cp.add_argument("--results", default="../../../.artifacts/external-bakeoff/results",
                    help="results dir to inspect for existing cells")
    cp.add_argument("--markdown", help="optional Markdown completion report")
    cp.add_argument("--armed", action="store_true",
                    help="mark selected fixtures as already verified RED@baseline/GREEN@fix")
    cp.add_argument("--require-result-evidence", action="store_true",
                    help="exit nonzero unless every selected cell has a current scored or pending result")
    cp.add_argument("--require-live-scored", action="store_true",
                    help="exit nonzero unless every selected cell has a current non-pending scored result")
    cp.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                    help="candidates.yaml for candidate/profile lookup")
    ah = sub.add_parser("audit-handoffs")
    ah.add_argument("--project", required=True)
    ah.add_argument("--bug", action="append", required=True,
                    help="bug id to audit; repeat or pass comma-separated ids")
    ah.add_argument("--candidate", action="append", required=True,
                    help="candidate key to audit; repeat or pass comma-separated keys")
    ah.add_argument("--results", default="../../../.artifacts/external-bakeoff/results",
                    help="results dir whose sibling prepared/ directory holds handoff metadata")
    ah.add_argument("--markdown", help="optional Markdown handoff audit report")
    pc = sub.add_parser("pending")
    pc.add_argument("--project", required=True)
    pc.add_argument("--bug", required=True)
    pc.add_argument("--candidate", required=True)
    pc.add_argument("--reason", required=True)
    pc.add_argument("--out", help="cell JSON path to write; defaults under results/cells/")
    pc.add_argument("--treatment", default="kitsoki")
    pc.add_argument("--candidates", default=str(HERE / "candidates.yaml"),
                    help="candidates.yaml for model/effort/provider lookup by --candidate")
    mf = sub.add_parser("mine-failures")  # classify a sweep + emit feedback actions
    mf.add_argument("--results", default="../../../.artifacts/external-bakeoff/results",
                    help="results dir (cells/ under it)")
    mf.add_argument("--traces", default="../../../.artifacts/external-bakeoff/traces",
                    help="traces dir holding <project>-<bug>-<candidate>.jsonl")
    mf.add_argument("--markdown", help="optional Markdown feedback report")
    cap = sub.add_parser("capsules")
    cap.add_argument("--project", action="append",
                     help="project id to list; repeat or comma-separate; default all promoted projects")
    cap.add_argument("--markdown", action="store_true",
                     help="render a Markdown catalog instead of JSON")
    oc = sub.add_parser("oracle-capsules")
    oc.add_argument("--project", action="append",
                    help="deprecated alias for capsules; repeat or comma-separate project ids")
    oc.add_argument("--markdown", action="store_true",
                    help="render a Markdown catalog instead of JSON")
    rp = sub.add_parser("repo-path")
    rp.add_argument("--project", required=True)
    rp.add_argument("--repo-dir", help="standalone checkout or meta-repo root")
    lo = sub.add_parser("lint-oracles")  # flag brittle exact-prose oracle assertions
    lo.add_argument("--project", required=True)
    lo.add_argument("--bug", action="append", help="bug id to lint; repeat or comma-separate; default all")
    lo.add_argument("--strict", action="store_true", help="exit nonzero on any brittle-prose finding")
    mt = sub.add_parser("meta")  # machine-readable project facts (for the Go runner)
    mt.add_argument("--project", required=True)
    mt.add_argument("--bug")     # optional: emit one bug's drive facts
    c = sub.add_parser("cost")   # worker cost/tokens from a live trace
    c.add_argument("--trace", required=True)
    cl = sub.add_parser("classify")  # INFRA-fail vs MODEL-result from a live trace
    cl.add_argument("--trace", required=True)
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
    if a.cmd == "classify":
        print(json.dumps(classify_cell(a.trace)))
        sys.exit(0)
    if a.cmd == "mine-failures":
        sys.exit(mine_failures(a.results, a.traces, markdown=a.markdown))
    if a.cmd in {"capsules", "oracle-capsules"}:
        sys.exit(repo_history_capsules(project_ids=a.project, markdown=a.markdown))
    if a.cmd == "summarize":
        sys.exit(summarize(load(a.project), a.results, a.deck, a.markdown,
                           allow_empty=a.allow_empty))

    project_ref = getattr(a, "project", None) or getattr(a, "manifest", None)
    if not project_ref:
        sys.exit(f"{a.cmd} needs --project or a manifest path")
    m = load(project_ref)
    if a.cmd == "repo-path":
        out = resolve_repo_dir(m, a.repo_dir)
        print(json.dumps(out, indent=2))
        sys.exit(0 if out["ok"] else 1)
    if a.cmd == "lint-oracles":
        sys.exit(lint_oracles(m, bug_ids=a.bug, strict=a.strict))
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
    if a.cmd == "completion":
        sys.exit(completion(m, repo_dir=a.repo_dir, candidate=a.candidate,
                            candidates_path=a.candidates, bug_ids=a.bug,
                            results_dir=a.results, markdown=a.markdown,
                            armed=a.armed,
                            require_result_evidence=a.require_result_evidence,
                            require_live_scored=a.require_live_scored))
    if a.cmd == "audit-handoffs":
        sys.exit(audit_handoffs(m, results_dir=a.results, candidate=a.candidate,
                                bug_ids=a.bug, markdown=a.markdown))
    if a.cmd == "pending":
        sys.exit(pending_cell(m, a.bug, a.candidate, a.reason, a.out,
                              candidates_path=a.candidates, treatment=a.treatment))
    if a.cmd == "score":
        # A completed grade exits 0 regardless of the oracle verdict — the
        # pass/fail outcome is DATA in the result JSON, not an execution status.
        # `score()` only RETURNS after scoring ran to completion (genuine
        # execution failures — oracle-missing, etc. — already sys.exit(<msg>)
        # earlier, and exceptions propagate as a nonzero traceback). Propagating
        # the verdict as the exit code made a legitimate `failed` cell look like
        # a transient error to drive_cell.sh's run_with_retry, which then burned
        # the entire docker+host backoff ladder (~hours) on an already-successful
        # score. The verdict still lives in score()'s return for in-process
        # callers like verify().
        score(m, bug_of(m, a.bug), a.tree, a.out, a.candidate, a.treatment,
              trace=a.trace, candidates_path=a.candidates,
              completion_state=a.completion_state)
        sys.exit(0)
    elif a.cmd == "meta":
        p = m["project"]
        if a.bug:
            b = bug_of(m, a.bug)
            print(json.dumps({
                "id": p["id"], "repo": p.get("repo", "."), "install": p.get("install", ""),
                "test_cmd": p.get("test_cmd", ""), "local_only": bool(p.get("local_only", False)),
                "repo_envs": project_repo_envs(p), "repo_subdir": str(p.get("repo_subdir", "") or ""),
                "bug": b["id"], "baseline_sha": b["baseline_sha"], "fix_sha": b.get("fix_sha", ""),
                "title": b.get("title", b["id"]), "ticket": b.get("ticket", b.get("title", b["id"])),
            }))
        else:
            print(json.dumps({
                "id": p["id"], "repo": p.get("repo", "."),
                "onboard_app": p.get("onboard_app", "@kitsoki/dev-story"),
                "local_only": bool(p.get("local_only", False)),
                "repo_envs": project_repo_envs(p),
                "repo_subdir": str(p.get("repo_subdir", "") or ""),
                "baselines": [b["baseline_sha"] for b in m["bugs"]],
                "bugs": [b["id"] for b in m["bugs"]],
            }))
        sys.exit(0)
    else:
        sys.exit(verify(m, a.bug, a.repo_dir, completion_state=a.completion_state))


if __name__ == "__main__":
    main()
