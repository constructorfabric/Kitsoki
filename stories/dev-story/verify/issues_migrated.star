# issues_migrated.star — the verify gate behind an ad-hoc plan's `apply`.
#
# Proves that a repo-local issue-migration step actually landed: GitHub now
# lists at least `expected_min` issues for the resolved repo. It does it the
# read-only way the inspection capability is meant to be used — a single
# allow-listed native GitHub probe (`gh.issue.list`) plus deterministic reshaping
# — no shell, no filesystem write, no clock, no randomness, so a recorded run replays
# byte-for-byte from an inspect cassette and a flow test injects a ReplayInspector
# (no live GitHub call, honoring the no-LLM / no-cost test rule).
#
# ── Interface (authoritative copy lives in issues_migrated.star.yaml) ─────────
# These INPUTS/OUTPUTS dicts are documentation for the reader; the engine
# validates against the sidecar.
INPUTS = {
    "expected_min": "int — the minimum number of GitHub issues that must exist for the gate to pass",
    "repo": "string — owner/repo to probe (resolved from the plan's issue_source: origin | upstream | combined)",
}
OUTPUTS = {
    "ok": "bool — True iff GitHub lists >= expected_min issues for the repo",
    "reason": "string — human-readable pass/fail rationale",
}

def main(ctx):
    expected_min = ctx.inputs.get("expected_min", 1)
    repo = ctx.inputs["repo"]

    # ONLY an allow-listed read-only native GitHub probe — never a shell.
    res = ctx.probe("gh.issue.list", [repo])
    out = res["out"]

    # A non-zero exit (e.g. gh not authed, repo missing) is a result, not an
    # exception — fail the gate loudly with the probe's exit code in the reason.
    if res["exit"] != 0:
        return {
            "ok": False,
            "reason": "GitHub issue list for %s failed (exit %d) — cannot confirm migration." % (repo, res["exit"]),
        }

    issues = json.decode(out) if out else []
    n = len(issues)
    ok = n >= expected_min
    if ok:
        reason = "Migration verified: GitHub lists %d issue(s) for %s (>= %d expected)." % (n, repo, expected_min)
    else:
        reason = "Migration incomplete: GitHub lists %d issue(s) for %s (< %d expected)." % (n, repo, expected_min)
    return {"ok": ok, "reason": reason}
