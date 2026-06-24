# verify_delta.star — compare a fresh audit against the group's fingerprints.
#
# Reads world.verify_raw.findings and world.current.member_ids directly.
#
# Interface (authoritative in verify_delta.star.yaml):
#   inputs:  (none required — reads from world)
#   world:   verify_raw.findings, current.member_ids
#   outputs: cleared (bool), remaining (int), regressions (list), after_frames (list)

def _fingerprint(f):
    return f.get("source", "") + "|" + f.get("check", "") + "|" + f.get("selector", "")

def main(ctx):
    verify_raw = ctx.world.get("verify_raw") or {}
    fresh = verify_raw.get("findings", [])
    if type(fresh) != "list":
        fresh = []

    current = ctx.world.get("current") or {}
    members = current.get("member_ids", [])
    if type(members) != "list":
        members = []

    fresh_fps = {_fingerprint(f): f for f in fresh}
    member_set = {fp: True for fp in members}

    still_present = [fp for fp in members if fp in fresh_fps]
    cleared = len(still_present) == 0

    regressions = [
        fp for fp, f in fresh_fps.items()
        if fp not in member_set and f.get("severity", "info") in ("error", "warn")
    ]

    return {
        "result": {
            "cleared":      cleared,
            "remaining":    len(still_present),
            "regressions":  regressions,
            "after_frames": [],
        },
    }
