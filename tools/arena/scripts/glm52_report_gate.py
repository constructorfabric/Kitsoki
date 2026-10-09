#!/usr/bin/env python3
"""Validate the GLM-5.2 + BugSwarm report's research claims.

The report generator is allowed to publish partial evidence, but it must not
turn missing cells into apparent wins, token ratios, or BugSwarm readiness. This
gate is offline and deterministic: it reads the JSON report and checks the
claim-level invariants that make the Markdown safe to cite.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path
from typing import Any

QUALITY_ATTEMPTED = {"solved", "partial", "failed"}
REQUIRED_COMPARISON_SCOPES = {"overall", "oss-oracle", "bugswarm"}
REQUIRED_CORPUS_TREATMENTS = {
    "oss-oracle|kitsoki",
    "oss-oracle|raw-prompt",
    "bugswarm|kitsoki",
    "bugswarm|raw-prompt",
}
REQUIRED_COMPLETION_REQUIREMENTS = {
    "report-artifact",
    "oss-source",
    "bugswarm-source",
    "bugswarm-execute-verification",
    "oss-kitsoki-glm52",
    "oss-raw-glm52",
    "bugswarm-kitsoki-glm52",
    "bugswarm-raw-glm52",
}
REPO_ROOT = Path(__file__).resolve().parents[3]


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report-json", required=True)
    parser.add_argument(
        "--require-publishable",
        action="store_true",
        help="fail unless the report has all evidence required for publishable headline claims",
    )
    args = parser.parse_args(argv)

    report = json.loads(Path(args.report_json).read_text(encoding="utf-8"))
    problems = validate_report(report)
    if args.require_publishable:
        problems.extend(require_publishable(report))
    if problems:
        print("FAIL: GLM-5.2 report gate")
        for problem in problems:
            print(f"  - {problem}")
        return 1
    print("PASS: GLM-5.2 report gate")
    return 0


def validate_report(report: dict[str, Any]) -> list[str]:
    problems: list[str] = []
    problems.extend(require_top_level(report))
    rows = [row for row in report.get("required_glm52_matrix", []) if isinstance(row, dict)]
    problems.extend(require_matrix(rows))
    problems.extend(require_rollups(report, rows))
    problems.extend(require_source_mix(report))
    problems.extend(require_comparisons(report))
    problems.extend(require_claim_ledger(report))
    problems.extend(require_threats_to_validity(report))
    problems.extend(require_closure(report, rows))
    problems.extend(require_completion_audit(report, rows))
    problems.extend(require_study_protocol(report, rows))
    problems.extend(require_reproducibility(report))
    problems.extend(require_references(report))
    return problems


def require_publishable(report: dict[str, Any]) -> list[str]:
    """Validate that the report is complete enough for headline publication.

    The normal gate checks that partial reports are honest. This stricter mode
    is the completion gate: it must stay red until the missing GLM-5.2 raw and
    BugSwarm cells are committed and folded into the report.
    """
    problems: list[str] = []
    ledger = report.get("claim_ledger") if isinstance(report.get("claim_ledger"), dict) else {}
    audit = report.get("completion_audit") if isinstance(report.get("completion_audit"), dict) else {}
    protocol = report.get("study_protocol") if isinstance(report.get("study_protocol"), dict) else {}
    closure = report.get("evidence_closure") if isinstance(report.get("evidence_closure"), dict) else {}
    comparisons = report.get("comparisons") if isinstance(report.get("comparisons"), dict) else {}
    if ledger.get("status") != "publishable":
        problems.append("publishable gate requires claim_ledger.status == 'publishable'")
    if audit.get("status") != "complete":
        problems.append("publishable gate requires completion_audit.status == 'complete'")
    if protocol.get("status") != "complete":
        problems.append("publishable gate requires study_protocol.status == 'complete'")
    if int(closure.get("pending_cell_count") or 0) != 0:
        problems.append("publishable gate requires zero pending GLM-5.2 headline cells")
    for scope in REQUIRED_COMPARISON_SCOPES:
        comparison = comparisons.get(scope) if isinstance(comparisons.get(scope), dict) else {}
        if comparison.get("status") != "complete":
            problems.append(f"publishable gate requires comparison {scope} to be complete")
        if comparison.get("success_rate_delta") is None:
            problems.append(f"publishable gate requires comparison {scope} success_rate_delta")
        if comparison.get("token_ratio_kitsoki_to_raw") is None:
            problems.append(f"publishable gate requires comparison {scope} token_ratio_kitsoki_to_raw")
    return problems


def require_top_level(report: dict[str, Any]) -> list[str]:
    problems: list[str] = []
    if report.get("kind") != "glm52_bugswarm_bugfix_report":
        problems.append(f"unexpected report kind: {report.get('kind')!r}")
    for key in ("corpora", "source_mix", "rollups", "comparisons", "claim_ledger", "threats_to_validity", "completion_audit", "study_protocol", "evidence_closure", "reproducibility", "references"):
        if not isinstance(report.get(key), dict):
            problems.append(f"missing mapping: {key}")
    return problems


def require_matrix(rows: list[dict[str, Any]]) -> list[str]:
    problems: list[str] = []
    seen = {f"{row.get('corpus')}|{row.get('treatment')}" for row in rows}
    missing = sorted(REQUIRED_CORPUS_TREATMENTS - seen)
    if missing:
        problems.append("required GLM matrix missing corpus/treatment bucket(s): " + ", ".join(missing))
    for row in rows:
        if row.get("candidate") != "glm-5.2":
            problems.append(f"matrix row {row.get('corpus')}:{row.get('task')}:{row.get('treatment')} is not glm-5.2")
        quality = row.get("quality")
        tokens = row.get("total_tokens")
        if quality in QUALITY_ATTEMPTED and not isinstance(tokens, int):
            problems.append(f"attempted row lacks token evidence: {row.get('corpus')} {row.get('task')} {row.get('treatment')}")
        if quality == "pending" and tokens is not None:
            problems.append(f"pending row must not carry token evidence: {row.get('corpus')} {row.get('task')} {row.get('treatment')}")
    return problems


def require_rollups(report: dict[str, Any], rows: list[dict[str, Any]]) -> list[str]:
    problems: list[str] = []
    rollups = report.get("rollups") if isinstance(report.get("rollups"), dict) else {}
    by_corpus = rollups.get("glm52_by_corpus_treatment")
    overall = rollups.get("glm52_by_treatment_overall")
    if not isinstance(by_corpus, dict):
        problems.append("missing glm52_by_corpus_treatment rollup")
    elif set(by_corpus) != REQUIRED_CORPUS_TREATMENTS:
        problems.append("glm52_by_corpus_treatment keys do not match required matrix buckets")
    if not isinstance(overall, dict):
        problems.append("missing glm52_by_treatment_overall rollup")
    else:
        for treatment in ("kitsoki", "raw-prompt"):
            if treatment not in overall:
                problems.append(f"overall rollup missing treatment {treatment}")
        pending_rows = sum(1 for row in rows if row.get("quality") == "pending")
        pending_rollup = sum(int(bucket.get("pending") or 0) for bucket in overall.values() if isinstance(bucket, dict))
        if pending_rows != pending_rollup:
            problems.append(f"overall pending count {pending_rollup} does not match matrix pending count {pending_rows}")
    return problems


def require_comparisons(report: dict[str, Any]) -> list[str]:
    comparisons = report.get("comparisons") if isinstance(report.get("comparisons"), dict) else {}
    problems: list[str] = []
    missing = sorted(REQUIRED_COMPARISON_SCOPES - set(comparisons))
    if missing:
        problems.append("missing comparison scope(s): " + ", ".join(missing))
    for scope, comparison in comparisons.items():
        if not isinstance(comparison, dict):
            problems.append(f"comparison {scope!r} is not a mapping")
            continue
        kitsoki = comparison.get("kitsoki") if isinstance(comparison.get("kitsoki"), dict) else {}
        raw = comparison.get("raw_prompt") if isinstance(comparison.get("raw_prompt"), dict) else {}
        complete = kitsoki.get("attempted", 0) > 0 and raw.get("attempted", 0) > 0
        if complete:
            if comparison.get("status") != "complete":
                problems.append(f"comparison {scope} should be complete when both arms have attempted cells")
            if comparison.get("success_rate_delta") is None:
                problems.append(f"comparison {scope} is complete but lacks success_rate_delta")
            if kitsoki.get("total_tokens") is not None and raw.get("total_tokens") is not None and comparison.get("token_ratio_kitsoki_to_raw") is None:
                problems.append(f"comparison {scope} is complete but lacks token_ratio_kitsoki_to_raw")
        else:
            if comparison.get("status") != "pending":
                problems.append(f"comparison {scope} must remain pending until both arms have attempted cells")
            if comparison.get("success_rate_delta") is not None:
                problems.append(f"comparison {scope} must not publish success delta while pending")
            if comparison.get("token_ratio_kitsoki_to_raw") is not None:
                problems.append(f"comparison {scope} must not publish token ratio while pending")
    return problems


def require_source_mix(report: dict[str, Any]) -> list[str]:
    mix = report.get("source_mix") if isinstance(report.get("source_mix"), dict) else {}
    problems: list[str] = []
    if not mix:
        return ["missing source_mix"]
    oss = mix.get("oss_oracle") if isinstance(mix.get("oss_oracle"), dict) else {}
    components = oss.get("components") if isinstance(oss.get("components"), list) else []
    by_id = {component.get("id"): component for component in components if isinstance(component, dict)}
    public = by_id.get("pre_registered_oss_targets", {})
    fixtures = by_id.get("armed_bugfix_fixtures", {})
    if public.get("repo_count") != 10 or public.get("task_count") != 20:
        problems.append("source mix must preserve the 10 public OSS target / 20 task component")
    if fixtures.get("task_count") != 6:
        problems.append("source mix must preserve the 6 armed bugfix fixture component")
    if "github_content" not in set(public.get("oracle_kinds") or []):
        problems.append("source mix public OSS component must use github_content oracles")
    if "external_bakeoff" not in set(fixtures.get("oracle_kinds") or []):
        problems.append("source mix fixture component must use external_bakeoff oracles")
    bugswarm = mix.get("bugswarm") if isinstance(mix.get("bugswarm"), dict) else {}
    if bugswarm.get("component") != "containerized_fail_pass_ci_artifacts":
        problems.append("source mix must describe BugSwarm as containerized fail/pass CI artifacts")
    if "execute RED/GREEN" not in str(bugswarm.get("verification_gate") or ""):
        problems.append("source mix BugSwarm component must keep execute RED/GREEN verification gate")
    policies = "\n".join(str(item) for item in (mix.get("blend_policy") or []))
    for required in ("separate source families", "total tokens", "dry-run BugSwarm"):
        if required not in policies:
            problems.append(f"source mix blend_policy missing {required!r}")
    return problems


def require_claim_ledger(report: dict[str, Any]) -> list[str]:
    ledger = report.get("claim_ledger") if isinstance(report.get("claim_ledger"), dict) else {}
    problems: list[str] = []
    if not ledger:
        return ["missing claim_ledger"]
    claims = ledger.get("claims") if isinstance(ledger.get("claims"), list) else []
    by_id = {claim.get("id"): claim for claim in claims if isinstance(claim, dict)}
    required = {
        "overall-token-usage",
        "overall-success-rate",
        "bugswarm-success-rate",
        "bugswarm-reusable-source",
        "oss-source-mix",
        "observed-oss-kitsoki-glm52-cell",
    }
    missing = sorted(required - set(by_id))
    if missing:
        problems.append("claim ledger missing claim(s): " + ", ".join(missing))
    if ledger.get("supported_count") != sum(1 for claim in claims if isinstance(claim, dict) and claim.get("status") == "supported"):
        problems.append("claim ledger supported_count does not match claims")
    if ledger.get("pending_count") != sum(1 for claim in claims if isinstance(claim, dict) and claim.get("status") == "pending"):
        problems.append("claim ledger pending_count does not match claims")
    comparisons = report.get("comparisons") if isinstance(report.get("comparisons"), dict) else {}
    if (comparisons.get("overall") or {}).get("status") == "pending":
        for claim_id in ("overall-token-usage", "overall-success-rate"):
            claim = by_id.get(claim_id, {})
            if claim.get("status") != "pending":
                problems.append(f"claim ledger {claim_id} must remain pending while overall comparison is pending")
            if not claim.get("missing_evidence"):
                problems.append(f"claim ledger {claim_id} must name missing evidence")
    if (comparisons.get("bugswarm") or {}).get("status") == "pending":
        claim = by_id.get("bugswarm-success-rate", {})
        if claim.get("status") != "pending":
            problems.append("claim ledger bugswarm-success-rate must remain pending while BugSwarm comparison is pending")
    for claim_id in ("bugswarm-reusable-source", "oss-source-mix", "observed-oss-kitsoki-glm52-cell"):
        claim = by_id.get(claim_id, {})
        if claim.get("status") != "supported":
            problems.append(f"claim ledger {claim_id} should be supported by current committed evidence")
        if not claim.get("evidence"):
            problems.append(f"claim ledger {claim_id} lacks evidence")
    return problems


def require_threats_to_validity(report: dict[str, Any]) -> list[str]:
    ledger = report.get("threats_to_validity") if isinstance(report.get("threats_to_validity"), dict) else {}
    problems: list[str] = []
    if not ledger:
        return ["missing threats_to_validity"]
    threats = ledger.get("threats") if isinstance(ledger.get("threats"), list) else []
    by_id = {threat.get("id"): threat for threat in threats if isinstance(threat, dict)}
    required = {
        "missing-raw-glm52-arm",
        "bugswarm-unverified-artifact",
        "single-observed-glm52-cell",
        "partial-is-not-solved",
        "supporting-round-not-glm52",
    }
    missing = sorted(required - set(by_id))
    if missing:
        problems.append("threats_to_validity missing threat(s): " + ", ".join(missing))
    active = [threat for threat in threats if isinstance(threat, dict) and threat.get("status") == "active"]
    high = [threat for threat in active if threat.get("severity") == "high"]
    if ledger.get("active_count") != len(active):
        problems.append("threats_to_validity active_count does not match threats")
    if ledger.get("high_count") != len(high):
        problems.append("threats_to_validity high_count does not match threats")
    comparisons = report.get("comparisons") if isinstance(report.get("comparisons"), dict) else {}
    if (comparisons.get("overall") or {}).get("status") == "pending":
        threat = by_id.get("missing-raw-glm52-arm", {})
        if threat.get("status") != "active" or threat.get("severity") != "high":
            problems.append("missing raw GLM-5.2 arm must be disclosed as an active high-severity threat")
    bugswarm = report.get("corpora", {}).get("bugswarm", {}) if isinstance(report.get("corpora"), dict) else {}
    if bugswarm.get("imported_task_count", 0) > 0 and bugswarm.get("verified_task_count", 0) == 0:
        threat = by_id.get("bugswarm-unverified-artifact", {})
        if threat.get("status") != "active" or threat.get("severity") != "high":
            problems.append("unverified BugSwarm artifact must be disclosed as an active high-severity threat")
    for threat in threats:
        if not isinstance(threat, dict):
            problems.append("threats_to_validity contains a non-mapping threat")
            continue
        if not str(threat.get("mitigation") or "").strip():
            problems.append(f"threat {threat.get('id')} lacks mitigation")
    return problems


def require_closure(report: dict[str, Any], rows: list[dict[str, Any]]) -> list[str]:
    closure = report.get("evidence_closure") if isinstance(report.get("evidence_closure"), dict) else {}
    actions = closure.get("actions") if isinstance(closure.get("actions"), list) else []
    action_by_corpus = {action.get("corpus"): action for action in actions if isinstance(action, dict)}
    problems: list[str] = []
    pending_rows = [row for row in rows if row.get("quality") == "pending"]
    if closure.get("pending_cell_count") != len(pending_rows):
        problems.append("evidence closure pending_cell_count does not match matrix")
    for corpus in ("oss-oracle", "bugswarm"):
        if corpus not in action_by_corpus:
            problems.append(f"evidence closure missing action for {corpus}")
    bugswarm = report.get("corpora", {}).get("bugswarm", {}) if isinstance(report.get("corpora"), dict) else {}
    bugswarm_action = action_by_corpus.get("bugswarm", {})
    if bugswarm.get("imported_task_count", 0) > 0 and bugswarm.get("verified_task_count", 0) == 0:
        if bugswarm_action.get("status") != "needs-execute-verification":
            problems.append("BugSwarm imported-but-unverified source must be gated as needs-execute-verification")
    return problems


def require_completion_audit(report: dict[str, Any], rows: list[dict[str, Any]]) -> list[str]:
    audit = report.get("completion_audit") if isinstance(report.get("completion_audit"), dict) else {}
    problems: list[str] = []
    if not audit:
        return ["missing completion_audit"]
    status = audit.get("status")
    if status not in {"complete", "incomplete"}:
        problems.append(f"completion audit has invalid status {status!r}")
    requirements = audit.get("requirements") if isinstance(audit.get("requirements"), list) else []
    by_id = {item.get("id"): item for item in requirements if isinstance(item, dict)}
    missing_ids = sorted(REQUIRED_COMPLETION_REQUIREMENTS - set(by_id))
    extra_ids = sorted(set(by_id) - REQUIRED_COMPLETION_REQUIREMENTS)
    if missing_ids:
        problems.append("completion audit missing requirement(s): " + ", ".join(missing_ids))
    if extra_ids:
        problems.append("completion audit has unexpected requirement(s): " + ", ".join(extra_ids))
    if audit.get("requirement_count") != len(requirements):
        problems.append("completion audit requirement_count does not match requirements")
    proven_count = sum(1 for item in requirements if isinstance(item, dict) and item.get("status") == "proven")
    if audit.get("proven_count") != proven_count:
        problems.append("completion audit proven_count does not match requirements")
    pending_rows = [row for row in rows if row.get("quality") == "pending"]
    pending_comparisons = pending_comparison_scopes(report)
    if (pending_rows or pending_comparisons) and status == "complete":
        problems.append("completion audit must remain incomplete while matrix/comparisons have pending evidence")
    if status == "complete":
        for item in requirements:
            if isinstance(item, dict) and item.get("status") != "proven":
                problems.append(f"completion audit complete but requirement {item.get('id')} is {item.get('status')}")
    for item in requirements:
        if not isinstance(item, dict):
            problems.append("completion audit contains a non-mapping requirement")
            continue
        req_status = item.get("status")
        if req_status not in {"proven", "missing"}:
            problems.append(f"completion audit requirement {item.get('id')} has invalid status {req_status!r}")
        if req_status == "missing" and not str(item.get("next") or "").strip():
            problems.append(f"completion audit missing requirement {item.get('id')} lacks next step")
        if req_status == "proven" and not item.get("evidence"):
            problems.append(f"completion audit proven requirement {item.get('id')} lacks evidence")
    return problems


def pending_comparison_scopes(report: dict[str, Any]) -> list[str]:
    comparisons = report.get("comparisons") if isinstance(report.get("comparisons"), dict) else {}
    return [
        str(scope)
        for scope, comparison in comparisons.items()
        if isinstance(comparison, dict) and comparison.get("status") == "pending"
    ]


def require_study_protocol(report: dict[str, Any], rows: list[dict[str, Any]]) -> list[str]:
    protocol = report.get("study_protocol") if isinstance(report.get("study_protocol"), dict) else {}
    problems: list[str] = []
    if not protocol:
        return ["missing study_protocol"]
    pending_rows = [row for row in rows if row.get("quality") == "pending"]
    if protocol.get("candidate") != "glm-5.2":
        problems.append("study protocol candidate must be glm-5.2")
    if protocol.get("primary_cost_metric") != "total_tokens":
        problems.append("study protocol primary_cost_metric must be total_tokens")
    if protocol.get("pending_cell_count") != len(pending_rows):
        problems.append("study protocol pending_cell_count does not match matrix")
    cells = protocol.get("pending_cells") if isinstance(protocol.get("pending_cells"), list) else []
    expected_cells = {
        (str(row.get("corpus")), str(row.get("task")), str(row.get("treatment")))
        for row in pending_rows
    }
    actual_cells = {
        (str(cell.get("corpus")), str(cell.get("task")), str(cell.get("treatment")))
        for cell in cells
        if isinstance(cell, dict)
    }
    if actual_cells != expected_cells:
        problems.append("study protocol pending_cells do not match matrix pending rows")
    steps = protocol.get("execution_steps") if isinstance(protocol.get("execution_steps"), list) else []
    step_by_id = {step.get("id"): step for step in steps if isinstance(step, dict)}
    if any(row.get("corpus") == "oss-oracle" and row.get("treatment") == "raw-prompt" for row in pending_rows):
        oss_step = step_by_id.get("oss-raw-glm52", {})
        if oss_step.get("status") != "ready":
            problems.append("study protocol must mark missing OSS raw GLM-5.2 cells ready to plan")
        if not command_contains(oss_step, "oss_to_arena_spec.py"):
            problems.append("study protocol OSS raw step must include oss_to_arena_spec.py")
        if not command_contains(oss_step, "--live"):
            problems.append("study protocol OSS raw step must include explicit live command")
    bugswarm = report.get("corpora", {}).get("bugswarm", {}) if isinstance(report.get("corpora"), dict) else {}
    bugswarm_pending = any(row.get("corpus") == "bugswarm" for row in pending_rows)
    if bugswarm_pending and bugswarm.get("imported_task_count", 0) > 0 and bugswarm.get("verified_task_count", 0) == 0:
        verify_step = step_by_id.get("bugswarm-execute-verification", {})
        if verify_step.get("status") != "required-before-live":
            problems.append("study protocol must require BugSwarm execute verification before live cells")
        if not command_contains(verify_step, "bugswarm_verify_source.py") or not command_contains(verify_step, "--execute"):
            problems.append("study protocol BugSwarm verification step must include --execute verifier command")
        if command_contains(verify_step, "arena.py run") and command_contains(verify_step, "--live"):
            problems.append("study protocol must not put live model command in BugSwarm verification step")
        if "bugswarm-glm52-cells" in step_by_id:
            problems.append("study protocol must not schedule BugSwarm live cells before execute verification")
    controls = protocol.get("live_controls") if isinstance(protocol.get("live_controls"), list) else []
    joined_controls = "\n".join(str(item) for item in controls)
    for required in ("ARENA_PAIRED_TASK_ENABLE_CODEX=1", "backend=claude", "execute-mode RED/GREEN"):
        if required not in joined_controls:
            problems.append(f"study protocol live controls missing {required}")
    return problems


def command_contains(step: dict[str, Any], needle: str) -> bool:
    commands = step.get("commands") if isinstance(step.get("commands"), list) else []
    return any(needle in str(command) for command in commands)


def require_reproducibility(report: dict[str, Any]) -> list[str]:
    ledger = report.get("reproducibility") if isinstance(report.get("reproducibility"), dict) else {}
    problems: list[str] = []
    if not ledger:
        return ["missing reproducibility ledger"]
    generator = ledger.get("generator") if isinstance(ledger.get("generator"), dict) else {}
    if generator.get("path") != "tools/arena/scripts/glm52_bugswarm_report.py":
        problems.append("reproducibility generator path must be tools/arena/scripts/glm52_bugswarm_report.py")
    problems.extend(require_hashed_file(generator, "reproducibility generator"))

    regenerate = ledger.get("regenerate_command") if isinstance(ledger.get("regenerate_command"), list) else []
    regenerate_text = " ".join(str(part) for part in regenerate)
    for required in ("--generated-at", "--json-out", "--markdown-out"):
        if required not in regenerate_text:
            problems.append(f"reproducibility regenerate command missing {required}")

    validation_commands = [str(command) for command in ledger.get("validation_commands", []) if isinstance(command, str)]
    joined_validation = "\n".join(validation_commands)
    if "glm52_report_gate.py" not in joined_validation:
        problems.append("reproducibility validation commands missing normal report gate")
    if "--require-publishable" not in joined_validation:
        problems.append("reproducibility validation commands missing publishable gate command")

    artifacts = ledger.get("artifacts") if isinstance(ledger.get("artifacts"), list) else []
    paths = {
        artifact.get("path")
        for artifact in artifacts
        if isinstance(artifact, dict) and artifact.get("kind") == "file"
    }
    for required in (
        "tools/arena/corpus/cost-bench.manifest.yaml",
        "tools/arena/corpus/sources.yaml",
        "tools/arena/corpus/bugswarm.seed.yaml",
        "tools/arena/results/round-1/rollup.json",
    ):
        if required not in paths:
            problems.append(f"reproducibility artifacts missing required input {required}")

    has_glm52_cell = False
    for artifact in artifacts:
        if not isinstance(artifact, dict):
            problems.append("reproducibility artifacts contains a non-mapping item")
            continue
        kind = artifact.get("kind")
        if kind == "file":
            if artifact.get("exists"):
                problems.extend(require_hashed_file(artifact, f"reproducibility artifact {artifact.get('path')}"))
        elif kind == "directory-glob":
            if artifact.get("path") != "tools/bugfix-bakeoff/results/cells" or artifact.get("pattern") != "*glm-5.2*.json":
                problems.append("reproducibility directory-glob must cover GLM-5.2 bakeoff cells")
            files = artifact.get("files") if isinstance(artifact.get("files"), list) else []
            if artifact.get("match_count") != len(files):
                problems.append("reproducibility directory-glob match_count does not match files")
            for item in files:
                if not isinstance(item, dict):
                    problems.append("reproducibility directory-glob contains a non-mapping file")
                    continue
                if str(item.get("path") or "").endswith("glm-5.2-kitsoki.json") or "*glm-5.2*" in str(artifact.get("pattern") or ""):
                    has_glm52_cell = True
                problems.extend(require_hashed_file(item, f"reproducibility artifact {item.get('path')}"))
        elif kind != "missing-optional":
            problems.append(f"reproducibility artifact has unexpected kind {kind!r}")
    if not has_glm52_cell:
        problems.append("reproducibility ledger must include at least one GLM-5.2 bakeoff cell hash")
    return problems


def require_hashed_file(artifact: dict[str, Any], label: str) -> list[str]:
    problems: list[str] = []
    path = str(artifact.get("path") or "")
    sha = str(artifact.get("sha256") or "")
    size = artifact.get("bytes")
    if len(sha) != 64 or any(char not in "0123456789abcdef" for char in sha):
        problems.append(f"{label} lacks a 64-character sha256")
    if not isinstance(size, int):
        problems.append(f"{label} lacks byte size")
    if path:
        disk_path = (REPO_ROOT / path).resolve()
        try:
            disk_path.relative_to(REPO_ROOT)
        except ValueError:
            problems.append(f"{label} path escapes repository root")
            return problems
        if not disk_path.exists():
            problems.append(f"{label} path does not exist on disk: {path}")
            return problems
        data = disk_path.read_bytes()
        actual_sha = hashlib.sha256(data).hexdigest()
        if sha and sha != actual_sha:
            problems.append(f"reproducibility hash mismatch for {path}")
        if isinstance(size, int) and size != len(data):
            problems.append(f"reproducibility byte size mismatch for {path}")
    return problems


def require_references(report: dict[str, Any]) -> list[str]:
    refs = report.get("references") if isinstance(report.get("references"), dict) else {}
    problems: list[str] = []
    local_paths = {ref.get("path") for ref in refs.get("local_evidence", []) if isinstance(ref, dict)}
    for required in ("tools/bugfix-bakeoff/results/cells", "tools/arena/corpus/cost-bench.manifest.yaml", "tools/arena/corpus/sources.yaml"):
        if required not in local_paths:
            problems.append(f"references missing local evidence path {required}")
    upstream_urls = {ref.get("url") for ref in refs.get("upstream", []) if isinstance(ref, dict)}
    for required in ("https://www.bugswarm.org/", "https://github.com/BugSwarm/client", "https://www.bugswarm.org/docs/toolset/bugswarm-rest-api/"):
        if required not in upstream_urls:
            problems.append(f"references missing upstream URL {required}")
    if not refs.get("bugswarm_seed"):
        problems.append("references missing BugSwarm seed provenance")
    return problems


if __name__ == "__main__":
    raise SystemExit(main())
