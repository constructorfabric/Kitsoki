# dedupe_findings.star — collapse verdict.json findings to a deduped set.
#
# Reads world.verdict_raw.findings directly (avoids template→string→list issues).
# Takes severity_floor as a string input.
#
# Interface (authoritative in dedupe_findings.star.yaml):
#   inputs:  severity_floor (string)
#   world:   verdict_raw.findings (read via ctx.world)
#   outputs: items (list), total_before (int), total_after (int)

SEVERITY_RANK = {"error": 3, "warn": 2, "info": 1}

def _meets_floor(sev, floor):
    return SEVERITY_RANK.get(sev, 0) >= SEVERITY_RANK.get(floor, 1)

def _fingerprint(f):
    return f.get("source", "") + "|" + f.get("check", "") + "|" + f.get("selector", "")

def main(ctx):
    floor = ctx.inputs["severity_floor"]

    verdict_raw = ctx.world.get("verdict_raw")
    if verdict_raw == None:
        return {"items": [], "total_before": 0, "total_after": 0}

    findings = verdict_raw.get("findings", [])
    if type(findings) != "list":
        findings = []

    total_before = len(findings)

    passing = [f for f in findings if _meets_floor(f.get("severity", "info"), floor)]

    vision = [f for f in passing if f.get("source", "") == "vision"]
    structural = [f for f in passing if f.get("source", "") != "vision"]

    seen = {}
    for f in structural:
        fp = _fingerprint(f)
        if fp not in seen:
            seen[fp] = {
                "id":             fp,
                "source":         f.get("source", ""),
                "check":          f.get("check", ""),
                "severity":       f.get("severity", "info"),
                "selector":       f.get("selector", ""),
                "detail":         f.get("detail", ""),
                "recommendation": f.get("recommendation", ""),
                "surfaces":       [],
                "viewports":      [],
                "frames":         [],
                "count":          0,
            }
        entry = seen[fp]
        srf = f.get("surface", f.get("step", ""))
        if srf and srf not in entry["surfaces"]:
            entry["surfaces"].append(srf)
        vp = f.get("viewport", "")
        if vp and vp not in entry["viewports"]:
            entry["viewports"].append(vp)
        fr = f.get("frame", "")
        if fr and fr not in entry["frames"]:
            entry["frames"].append(fr)
        entry["count"] = entry["count"] + f.get("count", 1)
        if SEVERITY_RANK.get(f.get("severity", "info"), 0) > SEVERITY_RANK.get(entry["severity"], 0):
            entry["severity"] = f.get("severity", "info")

    deduped_structural = [seen[k] for k in seen]

    vision_out = []
    for f in vision:
        entry = {
            "id":             f.get("source", "vision") + "|" + f.get("check", "") + "|" + f.get("surface", f.get("step", "")) + "|" + f.get("viewport", ""),
            "source":         f.get("source", "vision"),
            "check":          f.get("check", ""),
            "severity":       f.get("severity", "info"),
            "selector":       "",
            "detail":         f.get("detail", ""),
            "recommendation": f.get("recommendation", ""),
            "surfaces":       [f.get("surface", f.get("step", ""))],
            "viewports":      [f.get("viewport", "")],
            "frames":         f.get("frames", [f.get("frame", "")]),
            "count":          f.get("count", 1),
        }
        vision_out.append(entry)

    items = deduped_structural + vision_out
    return {
        "findings": {
            "items":        items,
            "total_before": total_before,
            "total_after":  len(items),
        },
    }
