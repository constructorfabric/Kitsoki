#!/usr/bin/env python3
"""No-LLM, no-docker test of arena's product-journey corpus loading.

Proves JobSpec can load targets from tools/product-journey/github-targets.json
and a persona axis from tools/product-journey/personas.json (unify-corpora),
without duplicating either corpus's vocabulary by hand, while hand-inlined
targets/variants/axes keep working exactly as before (backward compatibility
is asserted here, not just implied by test_arena_skeleton.py staying green).

Pure file parsing — no docker, no LLM.
"""

from __future__ import annotations

import json
import sys
import tempfile
from pathlib import Path

# Import the arena package from the tools/arena dir (sibling of tests/).
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent))

from arena.model import (  # noqa: E402
    JobSpec,
    Target,
    load_persona_axis_values,
    load_target_proof_checks,
    load_targets_from_corpus,
)

REPO_ROOT = HERE.parent.parent.parent
GITHUB_TARGETS = REPO_ROOT / "tools/product-journey/github-targets.json"
PERSONAS = REPO_ROOT / "tools/product-journey/personas.json"

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


# ---- 1. Read-only load of the real product-journey corpora -----------------

assert GITHUB_TARGETS.exists(), f"missing fixture corpus: {GITHUB_TARGETS}"
assert PERSONAS.exists(), f"missing fixture corpus: {PERSONAS}"

real_targets = load_targets_from_corpus(GITHUB_TARGETS)
check("github-targets.json loads at least one target", len(real_targets) > 0, True)
check("vscode target present", any(t.id == "vscode" for t in real_targets), True)
vscode = next(t for t in real_targets if t.id == "vscode")
check("target repo carried through", vscode.repo, "https://github.com/microsoft/vscode")
check("target stack carried through", vscode.stack, "typescript")
check("extra github-targets fields land in meta", vscode.meta.get("license_spdx"), "MIT")

real_personas = load_persona_axis_values(PERSONAS)
check("personas.json loads persona ids", "core-maintainer" in real_personas, True)
check("persona ids are strings", all(isinstance(p, str) for p in real_personas), True)

# ---- 2. targets_from: JobSpec.from_dict materializes Targets from the file --

spec = JobSpec.from_dict({
    "job_type": "persona-qa",
    "targets_from": "tools/product-journey/github-targets.json",
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
})
check("targets_from resolves relative to repo root", len(spec.targets), len(real_targets))
check("targets_from provenance recorded on spec", spec.targets_from, "tools/product-journey/github-targets.json")
check("targets_from cells enumerate", len(spec.cells()), len(real_targets))

# targets_from also accepts an absolute path (still resolved, not re-rooted).
abs_spec = JobSpec.from_dict({
    "job_type": "persona-qa",
    "targets_from": str(GITHUB_TARGETS),
    "variants": [{"id": "v1"}],
})
check("absolute targets_from path works", len(abs_spec.targets), len(real_targets))

# ---- 3. persona axis: loaded from personas.json, usable as a variant/axis --

persona_spec = JobSpec.from_dict({
    "job_type": "persona-qa",
    "targets": [{"id": "t1", "label": "t1"}],
    "variants": [{"id": "v1"}],
    "persona_axis_from": "tools/product-journey/personas.json",
})
check("persona axis materialized", persona_spec.axes.get("persona"), real_personas)
persona_cells = persona_spec.cells()
check("persona axis drives cell enumeration", len(persona_cells), len(real_personas))
check("cell axis carries persona id", {c.axis["persona"] for c in persona_cells}, set(real_personas))

# Hand-inlined axes.persona wins over persona_axis_from — no silent override.
override_spec = JobSpec.from_dict({
    "job_type": "persona-qa",
    "targets": [{"id": "t1"}],
    "variants": [{"id": "v1"}],
    "axes": {"persona": ["only-one"]},
    "persona_axis_from": "tools/product-journey/personas.json",
})
check("hand-inlined persona axis is not overridden", override_spec.axes["persona"], ["only-one"])

# ---- 4. target-proof metadata is respected when present --------------------

with tempfile.TemporaryDirectory() as tmp:
    proof_path = Path(tmp) / "target-proof.json"
    proof_path.write_text(json.dumps({
        "proof_id": "test-proof",
        "checks": [
            {"target": "vscode", "status": "pass", "open_bug_count": 250, "stargazers_count": 999999},
            {"target": "unknown-id", "status": "fail"},
        ],
    }), encoding="utf-8")

    checks = load_target_proof_checks(proof_path)
    check("proof checks indexed by target id", checks["vscode"]["status"], "pass")

    proof_spec = JobSpec.from_dict({
        "job_type": "persona-qa",
        "targets_from": "tools/product-journey/github-targets.json",
        "target_proof_from": str(proof_path),
        "variants": [{"id": "v1"}],
    })
    vscode_with_proof = next(t for t in proof_spec.targets if t.id == "vscode")
    check("target_proof merged into meta", vscode_with_proof.meta.get("target_proof", {}).get("status"), "pass")
    check("target_proof open_bug_count merged", vscode_with_proof.meta.get("target_proof", {}).get("open_bug_count"), 250)
    kubernetes_without_proof = next(t for t in proof_spec.targets if t.id == "kubernetes")
    check("targets absent from proof are untouched", "target_proof" in kubernetes_without_proof.meta, False)

    # Directory form (proof dir convention): pass the containing dir.
    dir_spec = JobSpec.from_dict({
        "job_type": "persona-qa",
        "targets_from": "tools/product-journey/github-targets.json",
        "target_proof_from": str(Path(tmp)),
        "variants": [{"id": "v1"}],
    })
    vscode_dir = next(t for t in dir_spec.targets if t.id == "vscode")
    check("target_proof_from accepts a proof directory", vscode_dir.meta.get("target_proof", {}).get("status"), "pass")

# Missing proof file is tolerated (proof is optional metadata, never required).
no_proof_checks = load_target_proof_checks("tools/product-journey/target-proofs-that-do-not-exist")
check("missing proof file returns empty, not an error", no_proof_checks, {})

# ---- 5. Backward compatibility: hand-inlined targets/variants/axes ---------

inline_spec = JobSpec.from_dict({
    "job_type": "bugfix",
    "targets": [{"id": "query-string", "label": "qs", "stack": "javascript"}],
    "variants": [{"id": "kitsoki-gpt-5.5", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {"bug": ["qs1", "qs2"]},
})
check("inline targets unaffected by targets_from support", len(inline_spec.targets), 1)
check("inline target id preserved", inline_spec.targets[0].id, "query-string")
check("inline targets_from stays empty", inline_spec.targets_from, "")
check("inline axes unaffected by persona axis support", inline_spec.axes, {"bug": ["qs1", "qs2"]})
check("inline cells still enumerate (1 target x 1 variant x 2 bugs)", len(inline_spec.cells()), 2)

# Inline targets take precedence even if targets_from is ALSO present.
both_spec = JobSpec.from_dict({
    "job_type": "bugfix",
    "targets": [{"id": "hand-picked"}],
    "targets_from": "tools/product-journey/github-targets.json",
    "variants": [{"id": "v1"}],
})
check("hand-inlined targets take precedence over targets_from", [t.id for t in both_spec.targets], ["hand-picked"])

if failures:
    print("FAIL: arena corpus loading")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: arena corpus loading (github-targets.json + personas.json, no LLM)")
