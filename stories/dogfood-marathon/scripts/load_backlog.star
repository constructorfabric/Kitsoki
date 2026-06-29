# load_backlog.star — load the backlog of cases for the marathon.
#
# A real drive lists the backlog source (a dir of tickets/bugs) and normalizes
# each into {id, title, baseline, repro_command}. ctx.fs is used to enumerate the
# source. To keep this deterministic and dependency-light at load/validate time,
# when the source cannot be enumerated we return an EMPTY backlog (the intake room
# routes an empty backlog straight to aggregation) — never a fabricated case.
#
# Under flow tests this whole call is stubbed by id, so the backlog is seeded by
# the fixture and this body does not run.
#
# Interface (authoritative in load_backlog.star.yaml):
#   inputs:  source (string), limit (int?), baseline_policy (string?)
#   world:   backlog (object)
#   outputs: backlog (object {items:[...]})

def main(ctx):
    # Idempotent: if a backlog was pre-seeded (a flow's initial_world, or an
    # operator who assembled the cases by hand), pass it through unchanged rather
    # than re-enumerating the source. This lets deterministic flow tests seed the
    # cases without stubbing host.starlark.run (the script runs for real).
    seeded = ctx.world.get("backlog") or {}
    if type(seeded) == "dict" and len(seeded.get("items", [])) > 0:
        return {"backlog": seeded}

    items = []
    source = ctx.inputs.get("source", "")
    limit = ctx.inputs.get("limit", 10)

    # Enumerate the source if the host exposes a filesystem; otherwise emit an
    # empty backlog (honest: no cases rather than invented ones).
    fs = getattr(ctx, "fs", None)
    if fs and source:
        listed = fs.glob(source + "/*.md") if hasattr(fs, "glob") else []
        for path in listed:
            if len(items) >= limit:
                break
            name = path.split("/")[-1]
            items.append({
                "id": name[:-3] if name.endswith(".md") else name,
                "title": name,
                "baseline": "",          # pinned per baseline_policy by the operator at drive time
                "repro_command": "",
                "source_path": path,
            })

    return {"backlog": {"items": items}}
