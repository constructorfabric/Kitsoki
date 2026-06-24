#!/usr/bin/env python3
"""verify_link.py — check the cross-link contract between the two intent reports.

Every intents.json record's analysis_ref must be "analysis.json#<instance_id>"
and that instance_id must exist in analysis.json (and vice-versa: no orphan
instances). Exits non-zero and lists violations on failure. Deterministic, no LLM.

    python3 verify_link.py <job-dir>
"""
import os
import sys

import intent_common as ic


def verify(job_dir):
    intents = ic.load_json(os.path.join(job_dir, "intents.json"))
    analysis = ic.load_json(os.path.join(job_dir, "analysis.json"))

    inst_ids = {i["instance_id"] for i in analysis.get("instances", [])}
    problems = []
    seen = set()
    for it in intents.get("intents", []):
        iid = it.get("instance_id")
        seen.add(iid)
        ref = it.get("analysis_ref", "")
        expected = "analysis.json#" + str(iid)
        if ref != expected:
            problems.append("intent %s: analysis_ref %r != %r" % (iid, ref, expected))
        if iid not in inst_ids:
            problems.append("intent %s: no matching instance in analysis.json" % iid)
    for iid in inst_ids:
        if iid not in seen:
            problems.append("instance %s: orphan (no intent points to it)" % iid)
    return problems


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    if len(argv) != 1:
        print("usage: verify_link.py <job-dir>", file=sys.stderr)
        return 2
    problems = verify(argv[0])
    if problems:
        for p in problems:
            print("LINK FAIL:", p, file=sys.stderr)
        return 1
    print("cross-link OK", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
