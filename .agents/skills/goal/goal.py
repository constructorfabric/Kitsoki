#!/usr/bin/env python3
"""goal.py — the deterministic (no-LLM) backbone of the `/goal` goal-seeker.

Everything an LLM should NOT be trusted with lives here so the expensive
evaluator's context stays bounded and its judgment stays honest:

  * fold the append-only log into a ledger (source of truth = the log),
  * build a BOUNDED, fixed-shape preamble (a projection of current state,
    never an accumulation of history) for the next evaluation,
  * lint the decomposition (unique ids, acyclic DAG, non-empty acceptance+gate),
  * select the ready + dependency-satisfied + scope-DISJOINT worker set,
  * append a structured entry to the log,
  * validate an evaluator verdict against the schema.

The exit code of `lint` / `validate-verdict` is the gate — trust it over prose.

Usage:
  goal.py init      --goal-dir <dir> [--work <dir>]      # seed log+ledger from decomposition.yaml
  goal.py ledger    --goal-dir <dir> [--work <dir>]      # (re)fold log -> ledger.json, print table
  goal.py preamble  --goal-dir <dir> [--work <dir>]      # write+print the bounded preamble
  goal.py lint      --goal-dir <dir>                     # structural gate on decomposition.yaml (exit code)
  goal.py ready     --goal-dir <dir> [--work <dir>] [-k N]   # print the disjoint ready worker set (JSON)
  goal.py append    --work <dir> --entry '<json>'        # append one structured log entry
  goal.py validate-verdict --json '<json>' [--goal-dir <dir>]   # gate an evaluator verdict

Layout (defaults):
  <goal-dir> = docs/goals/<slug>/   holds: GOAL.md, plan.md, decomposition.yaml   (committed inputs)
  <work>     = .artifacts/goal/<slug>/   holds: log.jsonl, ledger.json, preamble.md  (generated)
If --work is omitted it is derived: .artifacts/goal/<basename(goal-dir)>/.
"""
from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path

try:
    import yaml  # PyYAML — present in the repo's python env (validators/onboarding use it)
except Exception as e:  # pragma: no cover
    yaml = None
    _YAML_ERR = e

try:
    import jsonschema
except Exception as e:  # pragma: no cover
    jsonschema = None
    _JSONSCHEMA_ERR = e

# ---- state machine -----------------------------------------------------------
# Order matters: a later state never silently regresses to an earlier one in a fold
# unless the log explicitly says so.
STATES = ["blocked", "ready", "assigned", "in_flight", "reviewing", "verified",
          "integrated", "parked"]
TERMINAL_OK = {"integrated"}
PARK_REASONS = {"quota", "conflict", "escalation", "gate_not_red", "drive_failed"}


def _die(msg: str, code: int = 2):
    sys.stderr.write(f"goal.py: {msg}\n")
    sys.exit(code)


def _load_decomp(goal_dir: Path) -> dict:
    if yaml is None:
        _die(f"PyYAML is required to parse decomposition.yaml ({_YAML_ERR})")
    p = goal_dir / "decomposition.yaml"
    if not p.exists():
        _die(f"no decomposition.yaml in {goal_dir}")
    with p.open() as f:
        data = yaml.safe_load(f) or {}
    changes = data.get("changes") or []
    if not changes:
        _die("decomposition.yaml has no `changes`")
    return data


def _changes_by_id(decomp: dict) -> dict:
    return {str(c["id"]): c for c in decomp["changes"]}


def _read_log(work: Path) -> list[dict]:
    p = work / "log.jsonl"
    if not p.exists():
        return []
    out = []
    for i, line in enumerate(p.read_text().splitlines(), 1):
        line = line.strip()
        if not line:
            continue
        try:
            out.append(json.loads(line))
        except json.JSONDecodeError as e:
            _die(f"log.jsonl:{i} is not valid JSON: {e}")
    out.sort(key=lambda e: e.get("seq", 0))
    return out


def _work_for(goal_dir: Path, work: Path | None) -> Path:
    if work is not None:
        return work
    return Path(".artifacts/goal") / goal_dir.name


GATE_CLASSES = {"G-FLOW", "G-GOTEST", "G-SMOKE", "G-LINT", "G-LIVE+REPLAY"}
# A cmd that cannot go RED for the right reason / passes trivially.
TRIVIAL_CMD_MARKERS = ["# placeholder", "python3 -c 'import sys'", "python3 -c \"import sys\"", "true #"]


def _goal_gset(goal_dir: Path) -> set:
    """The declared criteria ids (G1..Gn) parsed from GOAL.md. goal_ref must be a subset."""
    out = set()
    for ln in _goal_criteria(goal_dir):
        # lines look like: "- **G1 — Can install it.** ..."
        m = ln.split("**")
        if len(m) > 1 and m[1][:1] == "G":
            gid = m[1].split()[0].strip()
            out.add(gid)
    return out


def _reachable(graph: dict) -> dict:
    """graph[n] = list of ids n depends_on. Returns n -> set of all transitively-reachable deps."""
    memo: dict = {}

    def go(n, stack):
        if n in memo:
            return memo[n]
        acc = set()
        for d in graph.get(n, []):
            if d in stack:  # cycle guard (DAG check reports it separately)
                continue
            acc.add(d)
            acc |= go(d, stack | {n})
        memo[n] = acc
        return acc

    return {n: go(n, set()) for n in graph}


def _load_change_node_schema() -> dict | None:
    """Load the unified change-node.schema.json (v1.0.0) from repo root."""
    schema_path = Path("schemas/change-node.schema.json")
    if not schema_path.exists():
        return None
    try:
        with schema_path.open() as f:
            return json.load(f)
    except Exception:
        return None


def _validate_change_node(change: dict, schema: dict | None) -> list[str]:
    """Validate a single change against the change-node schema. Returns list of errors."""
    if schema is None or jsonschema is None:
        return []  # schema validation optional if schema or jsonschema unavailable

    errors = []
    try:
        # Validate the change against the base schema
        jsonschema.validate(instance=change, schema=schema)
    except jsonschema.ValidationError as e:
        cid = change.get("id", "<unknown>")
        errors.append(f"{cid}: schema validation failed: {e.message}")
    except jsonschema.SchemaError as e:
        errors.append(f"schema validation error: {e.message}")

    return errors


def _validate_runtime(decomp: dict) -> list[str]:
    """Validate the optional top-level `runtime:` block (the goal's declared
    execution context: base_branch + worktree_path). Absent is fine — it only
    means the goal-seeker falls back to convention-locating / an operator seed.
    When present it must be well-formed so bootstrap's gs_resolve.star can trust
    it: both fields non-empty strings, worktree_path repo-root-RELATIVE (an
    absolute path is machine-specific and can't be committed / rejected by the
    inspector's root confinement)."""
    rt = decomp.get("runtime")
    if rt is None:
        return []
    errors: list[str] = []
    if not isinstance(rt, dict):
        return ["`runtime` must be a mapping (base_branch + worktree_path)"]
    base_branch = rt.get("base_branch")
    if not (isinstance(base_branch, str) and base_branch.strip()):
        errors.append("runtime.base_branch must be a non-empty string")
    wt = rt.get("worktree_path")
    if not (isinstance(wt, str) and wt.strip()):
        errors.append("runtime.worktree_path must be a non-empty string")
    elif os.path.isabs(wt):
        errors.append(f"runtime.worktree_path must be repo-root-relative, not absolute: {wt!r}")
    return errors


# ---- lint (the structural gate) ----------------------------------------------
def cmd_lint(decomp: dict, goal_dir: Path | None = None) -> int:
    changes = decomp["changes"]
    errors: list[str] = _validate_runtime(decomp)
    ids = [str(c.get("id", "")) for c in changes]
    gset = _goal_gset(goal_dir) if goal_dir else set()

    # Load and validate against the unified change-node schema
    schema = _load_change_node_schema()

    for c in changes:
        cid = str(c.get("id", "<missing>"))
        # Validate change against change-node.schema.json
        schema_errors = _validate_change_node(c, schema)
        errors.extend(schema_errors)
        if not c.get("id"):
            errors.append("a change is missing `id`")
        if not (c.get("acceptance") or []):
            errors.append(f"{cid}: empty `acceptance`")
        if not (c.get("scope") or []):
            errors.append(f"{cid}: empty `scope` (write boundary)")
        if not c.get("wall"):
            errors.append(f"{cid}: missing `wall`")

        # goal_ref must reference declared criteria (the first edge of the spine)
        grefs = [str(g) for g in (c.get("goal_ref") or [])]
        if not grefs:
            errors.append(f"{cid}: empty `goal_ref` (untraceable to a criterion)")
        if gset:
            for g in grefs:
                if g not in gset:
                    errors.append(f"{cid}: goal_ref {g} not in GOAL.md criteria {sorted(gset)}")

        # gate: class known; a runnable deterministic check that isn't trivially-passing
        gate = c.get("gate") or {}
        gclass = gate.get("class")
        if not gclass:
            errors.append(f"{cid}: missing `gate.class`")
        elif gclass not in GATE_CLASSES:
            errors.append(f"{cid}: unknown gate.class {gclass}")
        # every class needs a runnable exit-code check. G-LIVE+REPLAY's standing CI
        # gate is `replay_cmd` (no-LLM); the live run is manual acceptance, not the gate.
        check = gate.get("replay_cmd") if gclass == "G-LIVE+REPLAY" else gate.get("cmd")
        if not check:
            need = "replay_cmd" if gclass == "G-LIVE+REPLAY" else "cmd"
            errors.append(f"{cid}: gate has no `{need}` (the standing deterministic/no-LLM check)")
        else:
            low = check.lower()
            if any(m in low for m in TRIVIAL_CMD_MARKERS):
                errors.append(f"{cid}: gate check looks trivially-passing / placeholder: {check!r}")

    # unique ids
    seen = set()
    for i in ids:
        if i in seen:
            errors.append(f"duplicate id: {i}")
        seen.add(i)

    # dangling deps
    idset = set(ids)
    for c in changes:
        for d in (c.get("depends_on") or []):
            if str(d) not in idset:
                errors.append(f"{c.get('id')}: depends_on unknown id {d}")

    # acyclic DAG (Kahn)
    graph = {str(c["id"]): [str(d) for d in (c.get("depends_on") or [])] for c in changes}
    indeg = {n: sum(1 for d in graph[n] if d in graph) for n in graph}
    queue = [n for n, dg in indeg.items() if dg == 0]
    visited = 0
    while queue:
        n = queue.pop()
        visited += 1
        for m in graph:
            if n in graph[m]:
                indeg[m] -= 1
                if indeg[m] == 0:
                    queue.append(m)
    acyclic = visited == len(graph)
    if not acyclic:
        errors.append("dependency graph is not acyclic (cycle detected)")

    # GLOBAL disjoint-scope gate (the claim the docs make). Two changes may overlap
    # ONLY if one transitively depends on the other (never concurrently ready).
    if acyclic:
        reach = _reachable(graph)
        byid = _changes_by_id(decomp)
        for i in range(len(ids)):
            for j in range(i + 1, len(ids)):
                a, b = ids[i], ids[j]
                sequenced = (b in reach.get(a, set())) or (a in reach.get(b, set()))
                if sequenced:
                    continue
                sa = byid[a].get("scope") or []
                sb = byid[b].get("scope") or []
                if _scopes_overlap(sa, sb):
                    errors.append(f"scope overlap between concurrently-ready {a} and {b} "
                                  f"(no depends_on chain between them)")

    if errors:
        for e in errors:
            sys.stderr.write(f"  ✗ {e}\n")
        sys.stderr.write(f"lint FAILED: {len(errors)} problem(s)\n")
        return 1
    print(f"lint OK: {len(changes)} changes, acyclic DAG, disjoint concurrent scopes, "
          f"goal_refs valid, runnable gates")
    return 0


# ---- fold: log -> ledger -----------------------------------------------------
def fold_ledger(decomp: dict, log: list[dict]) -> dict:
    byid = _changes_by_id(decomp)
    rows: dict[str, dict] = {}
    for cid, c in byid.items():
        rows[cid] = {
            "change_id": cid,
            "wall": c.get("wall"),
            "goal_ref": c.get("goal_ref") or [],
            "title": c.get("title"),
            "pipeline": c.get("pipeline"),
            "scope_globs": c.get("scope") or [],
            "depends_on": [str(d) for d in (c.get("depends_on") or [])],
            "state": "blocked",
            "assignee": None,
            "branch": None,
            "sha": None,
            "gate_status": "red",   # RED-first: nothing is green until proven
            "gate_class": (c.get("gate") or {}).get("class"),
            "last_summary": None,
            "last_detail": None,
            "park_reason": None,    # {quota, conflict, escalation, gate_not_red, drive_failed} when state==parked
            "model": None,          # last model that worked this change (WM.5 cost-eval)
            "cost_usd": 0.0,        # accumulated across attempts (G4 cost visibility)
        }

    # apply the log in seq order (explicit state/branch/sha/gate/summary/detail)
    for e in log:
        cid = str(e.get("change_id", ""))
        if cid not in rows:
            continue
        r = rows[cid]
        actor = str(e.get("actor", ""))
        if e.get("state") in STATES:
            r["state"] = e["state"]
        if e.get("assignee"):
            r["assignee"] = e["assignee"]
        if e.get("branch"):
            r["branch"] = e["branch"]
        if e.get("sha"):
            r["sha"] = e["sha"]
        g = e.get("gate") or {}
        if g.get("status"):
            # TRUST: a `green` status counts only from an INDEPENDENT verifier
            # (reviewer/integrator) — never a worker's self-report. Any other status
            # (red/…) applies as-is. This makes G3 "green means verified" mechanical.
            if g["status"] == "green":
                if actor.startswith("reviewer") or actor.startswith("integrator"):
                    r["gate_status"] = "green"
                # else: ignored — worker cannot self-certify green
            else:
                r["gate_status"] = g["status"]
        if e.get("park_reason") in PARK_REASONS:
            r["park_reason"] = e["park_reason"]
        if e.get("model"):
            r["model"] = e["model"]
        if e.get("cost_usd") is not None:
            try:
                r["cost_usd"] += float(e["cost_usd"])
            except (TypeError, ValueError):
                pass
        if e.get("summary"):
            r["last_summary"] = e["summary"]
        if e.get("detail"):
            r["last_detail"] = e["detail"]

    # derive blocked<->ready for anything still in the intake states
    def deps_integrated(r):
        return all(rows[d]["state"] in TERMINAL_OK for d in r["depends_on"] if d in rows)

    for r in rows.values():
        if r["state"] in ("blocked", "ready"):
            r["state"] = "ready" if deps_integrated(r) else "blocked"

    return {
        "goal_slug": decomp.get("goal_slug"),
        "counts": _counts(rows),
        "cost_usd_total": round(sum(r["cost_usd"] for r in rows.values()), 4),
        "walls": _wall_gates(rows),
        "changes": [rows[c] for c in byid],  # decomposition order, stable
    }


def _wall_gates(rows: dict) -> dict:
    """Aggregate wall-gate = all member changes integrated AND green. The concrete
    definition the plan's 'merge a wall at a time' + the done-verdict depend on."""
    walls: dict = {}
    for r in rows.values():
        w = r["wall"] or "?"
        wg = walls.setdefault(w, {"total": 0, "integrated": 0, "green": False, "cost_usd": 0.0})
        wg["total"] += 1
        wg["cost_usd"] = round(wg["cost_usd"] + r["cost_usd"], 4)
        if r["state"] in TERMINAL_OK and r["gate_status"] == "green":
            wg["integrated"] += 1
    for w, wg in walls.items():
        wg["green"] = wg["integrated"] == wg["total"]
    return walls


def _counts(rows) -> dict:
    c = {s: 0 for s in STATES}
    for r in rows.values():
        c[r["state"]] = c.get(r["state"], 0) + 1
    return c


def _scopes_overlap(a: list[str], b: list[str]) -> bool:
    # Deterministic, conservative: overlap if any glob string is shared, OR one is a
    # path-prefix of the other (dir ownership). Good enough for the disjointness gate;
    # a shared file is the thing we must never co-assign.
    def norm(g):
        return g.rstrip("/")
    A = [norm(x) for x in a]
    B = [norm(x) for x in b]
    for x in A:
        for y in B:
            if x == y:
                return True
            if x.endswith("/**") or y.endswith("/**"):
                xd = x[:-3].rstrip("/")
                yd = y[:-3].rstrip("/")
                if xd == yd or xd.startswith(yd + "/") or yd.startswith(xd + "/"):
                    return True
            # plain dir-prefix ownership
            if x.startswith(y + "/") or y.startswith(x + "/"):
                return True
    return False


def ready_set(ledger: dict, k: int) -> list[dict]:
    """Greedy: pick ready changes, dependency order preserved, refusing any whose
    scope overlaps an already-picked one. Guarantees disjoint concurrent assignments."""
    picked: list[dict] = []
    for r in ledger["changes"]:
        if r["state"] != "ready":
            continue
        if any(_scopes_overlap(r["scope_globs"], p["scope_globs"]) for p in picked):
            continue
        picked.append(r)
        if len(picked) >= k:
            break
    return picked


# ---- preamble (the bounded, deterministic projection) ------------------------
def _goal_criteria(goal_dir: Path) -> list[str]:
    p = goal_dir / "GOAL.md"
    if not p.exists():
        return []
    return [ln.strip() for ln in p.read_text().splitlines()
            if ln.strip().startswith("- **G")]


def build_preamble(goal_dir: Path, decomp: dict, ledger: dict, log: list[dict]) -> str:
    crit = _goal_criteria(goal_dir)
    counts = ledger["counts"]
    lines = []
    lines.append(f"# /goal preamble — {ledger.get('goal_slug')} (BOUNDED projection; do not treat as history)")
    lines.append("")
    lines.append("## Goal criteria (the target)")
    lines.extend(crit or ["(GOAL.md criteria not found)"])
    lines.append("")
    lines.append("## Ledger (current state — one line per change)")
    lines.append("| id | wall | state | gate | deps | branch@sha |")
    lines.append("|----|------|-------|------|------|------------|")
    for r in ledger["changes"]:
        bs = (f"{r['branch']}@{(r['sha'] or '')[:7]}" if r["branch"] else "—")
        deps = ",".join(r["depends_on"]) or "—"
        lines.append(f"| {r['change_id']} | {r['wall']} | {r['state']} | "
                     f"{r['gate_class']}:{r['gate_status']} | {deps} | {bs} |")
    lines.append("")
    lines.append(f"Counts: " + ", ".join(f"{s}={counts.get(s,0)}" for s in STATES if counts.get(s)))
    lines.append(f"Cost so far: ${ledger.get('cost_usd_total', 0.0)} (G4 — spend visible)")
    lines.append("")
    lines.append("## Wall gates (aggregate = all member changes integrated AND green)")
    for w, wg in sorted(ledger.get("walls", {}).items()):
        mark = "GREEN" if wg["green"] else f"{wg['integrated']}/{wg['total']}"
        lines.append(f"- {w}: {mark}  (${wg['cost_usd']})")
    lines.append("")
    # Frontier detail — ONLY the open changes, ONLY their latest summary + pointers.
    frontier = [r for r in ledger["changes"]
                if r["state"] in ("assigned", "in_flight", "reviewing", "verified", "parked")]
    lines.append("## Frontier (open changes only — the last summary each; fetch detail ONLY IF NECESSARY)")
    if not frontier:
        lines.append("(no open changes — dispatch from the ready set)")
    for r in frontier:
        park = f" [parked: {r['park_reason']}]" if r["state"] == "parked" and r.get("park_reason") else ""
        lines.append(f"- **{r['change_id']}** ({r['state']}){park}: {r['last_summary'] or '(no summary yet)'}")
        if r["last_detail"]:
            ptrs = "; ".join(f"{kk}={vv}" for kk, vv in r["last_detail"].items())
            lines.append(f"    detail → {ptrs}")
    lines.append("")
    lines.append("## Ready to dispatch now (disjoint scopes, deps satisfied)")
    for r in ready_set(ledger, k=99):
        lines.append(f"- {r['change_id']} — {r['title']} (scope: {', '.join(r['scope_globs'])})")
    lines.append("")
    lines.append("## Your job")
    lines.append("Emit ONE JSON: {verdict, summary, next_instruction}. NOTE: `verdict=done` is "
                 "DETERMINISTICALLY re-checked against the ledger (every change integrated AND "
                 "every wall-gate green) — a false `done` is rejected by validate-verdict, so don't "
                 "claim it early. Otherwise pick the next change(s) from the ready set. Open a "
                 "`detail →` pointer with a tool ONLY IF the summary+gate leave you genuinely unsure. "
                 "Do not restate this preamble back.")
    return "\n".join(lines) + "\n"


# ---- verdict validation ------------------------------------------------------
VERDICT_REQUIRED = {"verdict", "summary", "next_instruction"}
VERDICT_ENUM = {"done", "not_done", "blocked"}


def validate_verdict(obj: dict, ledger: dict | None) -> tuple[bool, list[str]]:
    errs = []
    missing = VERDICT_REQUIRED - set(obj)
    if missing:
        errs.append(f"missing keys: {sorted(missing)}")
    if obj.get("verdict") not in VERDICT_ENUM:
        errs.append(f"verdict must be one of {sorted(VERDICT_ENUM)}")
    ni = obj.get("next_instruction")
    if obj.get("verdict") == "done":
        if ni not in (None, [], {}):
            errs.append("verdict=done requires next_instruction to be null/empty")
        # DETERMINISTIC done-check: the evaluator cannot declare done while the ledger
        # disagrees. done ⇔ every change integrated AND every wall-gate green. This is
        # the anti-false-done gate — computed, not trusted to LLM judgment.
        if ledger is not None:
            not_done = [r["change_id"] for r in ledger["changes"]
                        if r["state"] not in TERMINAL_OK or r["gate_status"] != "green"]
            if not_done:
                shown = ", ".join(not_done[:8]) + ("…" if len(not_done) > 8 else "")
                errs.append(f"verdict=done contradicts ledger: {len(not_done)} change(s) "
                            f"not integrated+green: {shown}")
            open_walls = [w for w, wg in ledger.get("walls", {}).items() if not wg["green"]]
            if open_walls:
                errs.append(f"verdict=done but wall-gates not green: {sorted(open_walls)}")
    else:
        items = ni if isinstance(ni, list) else [ni]
        if not items or items == [None]:
            errs.append("verdict!=done requires at least one next_instruction")
        for it in items:
            if not isinstance(it, dict):
                errs.append("next_instruction item is not an object")
                continue
            for req in ("change_id", "action", "gate"):
                if req not in it:
                    errs.append(f"next_instruction missing `{req}`")
            if ledger and "change_id" in it:
                known = {r["change_id"] for r in ledger["changes"]}
                if str(it["change_id"]) not in known:
                    errs.append(f"next_instruction.change_id {it['change_id']} not in ledger")
    return (not errs), errs


# ---- verify (actually RUN a change's gate — closes RED-first / GREEN checks) --
def cmd_verify(decomp: dict, change_id: str, expect: str) -> int:
    """Run a change's deterministic gate check and enforce the expected polarity.
    expect=red  → the check MUST fail now (RED-first; refuse to start otherwise).
    expect=green→ the check MUST pass (integration gate).
    expect=any  → just report the exit code.
    The exit code of the gate — not a human's read — is the truth."""
    byid = _changes_by_id(decomp)
    c = byid.get(str(change_id))
    if not c:
        _die(f"no change {change_id}")
    gate = c.get("gate") or {}
    check = gate.get("replay_cmd") if gate.get("class") == "G-LIVE+REPLAY" else gate.get("cmd")
    if not check:
        _die(f"{change_id}: gate has no runnable check")
    print(f"[{change_id}] running gate: {check}", file=sys.stderr)
    rc = subprocess.run(check, shell=True).returncode
    passed = rc == 0
    if expect == "red":
        ok = not passed
        print(f"[{change_id}] RED-first check: gate {'FAILS (RED) ✓' if not passed else 'PASSES — NOT RED ✗'} (exit {rc})")
        return 0 if ok else 1
    if expect == "green":
        print(f"[{change_id}] GREEN check: gate {'PASSES ✓' if passed else 'FAILS ✗'} (exit {rc})")
        return 0 if passed else 1
    print(f"[{change_id}] gate exit {rc}")
    return rc


# ---- append ------------------------------------------------------------------
def append_entry(work: Path, entry: dict) -> int:
    work.mkdir(parents=True, exist_ok=True)
    log = work / "log.jsonl"
    existing = _read_log(work)
    entry.setdefault("seq", (existing[-1]["seq"] + 1) if existing else 1)
    if "kind" not in entry:
        _die("log entry needs a `kind`")
    with log.open("a") as f:
        f.write(json.dumps(entry, sort_keys=True) + "\n")
    print(f"appended seq={entry['seq']} kind={entry['kind']} change={entry.get('change_id','-')}")
    return 0


# ---- CLI ---------------------------------------------------------------------
def main(argv=None):
    ap = argparse.ArgumentParser(prog="goal.py")
    sub = ap.add_subparsers(dest="cmd", required=True)
    for name in ("init", "ledger", "preamble", "lint", "ready", "walls"):
        sp = sub.add_parser(name)
        sp.add_argument("--goal-dir", required=True)
        sp.add_argument("--work", default=None)
        if name == "ready":
            sp.add_argument("-k", type=int, default=1)
    sp = sub.add_parser("append")
    sp.add_argument("--work", required=True)
    sp.add_argument("--entry", required=True)
    sp = sub.add_parser("verify")
    sp.add_argument("--goal-dir", required=True)
    sp.add_argument("--change", required=True)
    sp.add_argument("--expect", choices=["red", "green", "any"], default="any")
    sp = sub.add_parser("validate-verdict")
    sp.add_argument("--json", required=True)
    sp.add_argument("--goal-dir", default=None)
    sp.add_argument("--work", default=None)
    args = ap.parse_args(argv)

    if args.cmd == "append":
        return append_entry(Path(args.work), json.loads(args.entry))

    if args.cmd == "validate-verdict":
        obj = json.loads(args.json)
        ledger = None
        if args.goal_dir:
            gd = Path(args.goal_dir)
            wk = _work_for(gd, Path(args.work) if args.work else None)
            ledger = fold_ledger(_load_decomp(gd), _read_log(wk))
        ok, errs = validate_verdict(obj, ledger)
        if ok:
            print("verdict OK")
            return 0
        for e in errs:
            sys.stderr.write(f"  ✗ {e}\n")
        return 1

    goal_dir = Path(args.goal_dir)
    decomp = _load_decomp(goal_dir)

    if args.cmd == "verify":
        return cmd_verify(decomp, args.change, args.expect)

    if args.cmd == "lint":
        return cmd_lint(decomp, goal_dir)

    work = _work_for(goal_dir, Path(args.work) if args.work else None)

    if args.cmd == "init":
        rc = cmd_lint(decomp, goal_dir)
        if rc != 0:
            return rc
        work.mkdir(parents=True, exist_ok=True)
        (work / "log.jsonl").touch()
        ledger = fold_ledger(decomp, [])
        (work / "ledger.json").write_text(json.dumps(ledger, indent=2))
        print(f"initialized {work} — {len(decomp['changes'])} changes, log empty")
        return 0

    log = _read_log(work)
    ledger = fold_ledger(decomp, log)

    if args.cmd == "ledger":
        (work).mkdir(parents=True, exist_ok=True)
        (work / "ledger.json").write_text(json.dumps(ledger, indent=2))
        print(json.dumps(ledger["counts"]))
        for r in ledger["changes"]:
            print(f"  {r['change_id']:<6} {r['wall']:<3} {r['state']:<11} "
                  f"{r['gate_class']}:{r['gate_status']:<6} {r['title']}")
        return 0

    if args.cmd == "ready":
        picked = ready_set(ledger, k=args.k)
        print(json.dumps([{"change_id": r["change_id"], "title": r["title"],
                           "pipeline": r["pipeline"], "scope": r["scope_globs"],
                           "worktree": f".worktrees/gu-{r['change_id']}"} for r in picked], indent=2))
        return 0

    if args.cmd == "preamble":
        text = build_preamble(goal_dir, decomp, ledger, log)
        work.mkdir(parents=True, exist_ok=True)
        (work / "preamble.md").write_text(text)
        sys.stdout.write(text)
        return 0

    if args.cmd == "walls":
        print(json.dumps(ledger["walls"], indent=2))
        allgreen = all(wg["green"] for wg in ledger["walls"].values())
        print(f"GOAL {'DONE' if allgreen else 'not done'} — "
              f"total cost ${ledger['cost_usd_total']}")
        return 0 if allgreen else 1

    _die(f"unknown command {args.cmd}")


if __name__ == "__main__":
    sys.exit(main())
