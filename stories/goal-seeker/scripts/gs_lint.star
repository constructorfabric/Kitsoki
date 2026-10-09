# gs_lint.star — the structural gate on the decomposition (goal.py:cmd_lint).
#
# Deterministic, no-LLM. Rejects a decomposition that is not safe to drive:
#   * a change missing id / empty acceptance / missing gate.class / missing gate.cmd
#     (except G-LIVE+REPLAY) / empty scope (the write boundary),
#   * duplicate ids, dangling depends_on,
#   * a dependency cycle (Kahn topological sort),
#   * two ready-at-intake changes whose scope globs OVERLAP (no two changes may own
#     the same file at ready-time — the disjoint-scope claim).
# Returns route=ok / route=fail + a joined error string. The bootstrap room routes a
# fail to needs_human, so a bad decomposition never reaches the loop.

GREEN_ACTORS = ["reviewer", "integrator"]


def _text(v):
    if v == None:
        return ""
    return str(v).strip()


def _scopes_overlap(a, b):
    aa = [x.rstrip("/") for x in a]
    bb = [y.rstrip("/") for y in b]
    for x in aa:
        for y in bb:
            if x == y:
                return True
            if x.endswith("/**") or y.endswith("/**"):
                xd = x[:-3].rstrip("/")
                yd = y[:-3].rstrip("/")
                if xd == yd or xd.startswith(yd + "/") or yd.startswith(xd + "/"):
                    return True
            if x.startswith(y + "/") or y.startswith(x + "/"):
                return True
    return False


def _has_cycle(ids_order, adj):
    remaining = {}
    for bid in ids_order:
        remaining[bid] = adj[bid]
    for _ in range(len(ids_order)):
        progressed = False
        next_remaining = {}
        for bid in ids_order:
            if bid not in remaining:
                continue
            blocked = False
            for dep in remaining[bid]:
                if dep in remaining:
                    blocked = True
            if blocked:
                next_remaining[bid] = remaining[bid]
            else:
                progressed = True
        remaining = next_remaining
        if len(remaining) == 0:
            return ""
        if not progressed:
            break
    for bid in ids_order:
        if bid in remaining:
            return bid
    return ""


def main(ctx):
    goal_dir = ctx.inputs["goal_dir"].rstrip("/")
    decomp_path = goal_dir + "/decomposition.yaml"
    if not ctx.fs.exists(decomp_path):
        return {"route": "fail", "error": "no decomposition.yaml at " + decomp_path}
    doc = yaml.decode(ctx.fs.read(decomp_path))
    if type(doc) != "dict":
        return {"route": "fail", "error": "decomposition.yaml must be a mapping"}
    changes = doc.get("changes") or []
    if not changes:
        return {"route": "fail", "error": "decomposition.yaml has no `changes`"}

    errors = []

    # Optional top-level `runtime:` block (the goal's declared execution context;
    # bootstrap's gs_resolve.star reads it). Absent is fine; present must be well-
    # formed — parity with goal.py:_validate_runtime.
    runtime = doc.get("runtime")
    if runtime != None:
        if type(runtime) != "dict":
            errors.append("runtime must be a mapping (base_branch + worktree_path)")
        else:
            if _text(runtime.get("base_branch")) == "":
                errors.append("runtime.base_branch must be a non-empty string")
            wt = _text(runtime.get("worktree_path"))
            if wt == "":
                errors.append("runtime.worktree_path must be a non-empty string")
            elif wt.startswith("/"):
                errors.append("runtime.worktree_path must be repo-root-relative, not absolute: " + wt)
    ids = {}
    ids_order = []
    for i, c in enumerate(changes):
        if type(c) != "dict":
            errors.append("change[%d] is not a mapping" % i)
            continue
        cid = _text(c.get("id"))
        if cid == "":
            errors.append("change[%d] missing id" % i)
            continue
        if cid in ids:
            errors.append("duplicate id: " + cid)
        ids[cid] = i
        ids_order.append(cid)

    idset = {}
    for cid in ids_order:
        idset[cid] = True

    adj = {}
    for c in changes:
        if type(c) != "dict":
            continue
        cid = _text(c.get("id"))
        if cid == "":
            continue
        if not (c.get("acceptance") or []):
            errors.append(cid + ": empty acceptance")
        gate = c.get("gate") or {}
        gclass = _text(gate.get("class"))
        if gclass == "":
            errors.append(cid + ": missing gate.class")
        if _text(gate.get("cmd")) == "" and gclass != "G-LIVE+REPLAY":
            errors.append(cid + ": gate has no cmd (the deterministic check)")
        if not (c.get("scope") or []):
            errors.append(cid + ": empty scope (write boundary)")
        deps = [str(d) for d in (c.get("depends_on") or [])]
        for d in deps:
            if d not in idset:
                errors.append(cid + ": depends_on unknown id " + d)
        adj[cid] = deps

    cycle = _has_cycle(ids_order, adj)
    if cycle != "":
        errors.append("dependency cycle detected involving " + cycle)

    # scope-disjointness among changes that are READY at intake (no deps) — no two
    # such changes may own the same file. (goal.py enforces this at ready-time.)
    ready0 = []
    for c in changes:
        if type(c) != "dict":
            continue
        cid = _text(c.get("id"))
        if cid == "":
            continue
        if len(adj.get(cid, [])) == 0:
            ready0.append((cid, [str(g) for g in (c.get("scope") or [])]))
    for i in range(len(ready0)):
        for j in range(i + 1, len(ready0)):
            if _scopes_overlap(ready0[i][1], ready0[j][1]):
                errors.append("scope overlap between ready changes " + ready0[i][0] + " and " + ready0[j][0])

    if errors:
        return {"route": "fail", "error": "decomposition lint failed: " + "; ".join(errors)}
    return {"route": "ok", "error": ""}
