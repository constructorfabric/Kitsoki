#!/usr/bin/env python3
"""Deterministic tests for the Persona QA compatibility adapter.

Run directly:
  python3 tools/persona_qa/tests/test_kit_cli.py

No test calls a live LLM. The end-to-end path emits and reviews a dry run
bundle from a temp external kit config while the story remains the operator
surface.
"""

from __future__ import annotations

import contextlib
import io
import json
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT))

from tools.persona_qa import config as qa_config  # noqa: E402
from tools.persona_qa import kit  # noqa: E402

failures: list[str] = []


def check(label: str, cond: bool, detail: object = "") -> None:
    if not cond:
        failures.append(f"{label}: expected true{f' ({detail})' if detail else ''}")


def check_eq(label: str, got: object, want: object) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def run_cli(argv: list[str]) -> tuple[int, str, str]:
    stdout = io.StringIO()
    stderr = io.StringIO()
    with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
        code = kit.main(argv)
    return code, stdout.getvalue(), stderr.getvalue()


def json_stdout(argv: list[str]) -> dict:
    code, stdout, stderr = run_cli(argv)
    check_eq("CLI exit code for " + " ".join(argv), code, 0)
    if stderr:
        failures.append(f"unexpected stderr for {' '.join(argv)}: {stderr}")
    return json.loads(stdout)


with tempfile.TemporaryDirectory(prefix="persona-qa-kit-") as td:
    project = Path(td)
    init_result = json_stdout(["init", "--root", str(project), "--json-output"])
    check_eq("init status", init_result["status"], "initialized")
    check("config file written", (project / "persona-qa.yaml").is_file())
    check("persona directory written", (project / "persona-qa" / "personas").is_dir())
    check("schema copied", (project / "persona-qa" / "schemas" / "v1" / "config.schema.json").is_file())

    config_path = project / "persona-qa.yaml"
    cfg = qa_config.load_config(config_path, repo_root=ROOT)
    validation = qa_config.validate_config(cfg)
    check_eq("validation status", validation["status"], "valid")
    check_eq("validation personas", validation["counts"]["personas"], 1)
    check_eq("validation scenarios", validation["counts"]["scenarios"], 1)

    schema_root = project / "persona-qa" / "schemas" / "v1"
    schema_cases = [
        ("config.schema.json", qa_config.template_config()),
        ("persona.schema.json", qa_config.template_persona()),
        ("scenario.schema.json", qa_config.template_scenario()),
        ("driver-manifest.schema.json", qa_config.template_driver()),
    ]
    for schema_name, instance in schema_cases:
        schema = json.loads((schema_root / schema_name).read_text(encoding="utf-8"))
        errors = qa_config.validate_schema_subset(instance, schema)
        check_eq(f"{schema_name} validates template", errors, [])

    validate_result = json_stdout(["validate", "--config", str(config_path), "--json-output"])
    check_eq("validate CLI status", validate_result["status"], "valid")

    transport_preview = json_stdout([
        "transports",
        "--config",
        str(config_path),
        "--scenario",
        "project-onboarding",
        "--transport",
        "all",
        "--json-output",
    ])
    check_eq("transport preview schema", transport_preview["schema"], "kitsoki/persona-qa-transport-suite/v1")
    check_eq("transport preview status", transport_preview["status"], "ready")
    check_eq("transport preview uses external web-only scenario", transport_preview["summary"]["leg_count"], 1)
    check_eq("transport preview applicable transport", transport_preview["legs"][0]["transport"], "web")
    check_eq("transport preview proof level", transport_preview["legs"][0]["evidence_contract"]["level"], "frame-level")
    check("transport preview has stable entrypoints", "visual.observe" in transport_preview["legs"][0]["entrypoints"]["observe"]["capabilities"])
    transport_schema = json.loads((schema_root / "transport-suite.schema.json").read_text(encoding="utf-8"))
    check_eq(
        "transport preview validates against public schema",
        qa_config.validate_schema_subset(transport_preview, transport_schema),
        [],
    )

    emit_preview = json_stdout([
        "emit-run",
        "--config",
        str(config_path),
        "--scenario",
        "project-onboarding",
        "--transport",
        "all",
        "--preview",
        "--json-output",
    ])
    check_eq("emit-run preview reuses transport suite", emit_preview["schema"], "kitsoki/persona-qa-transport-suite/v1")
    check_eq("emit-run preview does not create a run bundle", "run_dir" in emit_preview, False)

    emit_result = json_stdout([
        "emit-run",
        "--config",
        str(config_path),
        "--project",
        "local-app",
        "--persona",
        "core-maintainer",
        "--scenario",
        "project-onboarding",
        "--transport",
        "all",
        "--seed",
        "kit-test",
        "--json-output",
    ])
    run_dir = Path(emit_result["run_dir"])
    expected_artifact_root = (project / ".artifacts" / "persona-qa").resolve()
    check("emit-run uses external artifact root", str(run_dir).startswith(str(expected_artifact_root)), run_dir)
    run_json = json.loads((run_dir / "run.json").read_text(encoding="utf-8"))
    check_eq("run project from external config", run_json["project"]["id"], "local-app")
    check_eq("run scenario from external config", run_json["scenarios"][0]["id"], "project-onboarding")
    driver_plan = json.loads((run_dir / "driver-plan.json").read_text(encoding="utf-8"))
    check_eq("driver plan has web leg only", [item.get("transport") for item in driver_plan["scenarios"]], ["web"])

    review_result = json_stdout(["review", "--config", str(config_path), "--run-dir", str(run_dir), "--json-output"])
    check("review writes review path", Path(review_result["review_path"]).is_file(), review_result)

    complete = json_stdout(["complete", "--config", str(config_path), "--run-dir", str(run_dir)])
    check_eq("completion schema present", complete["schema_version"], "1.0.0")
    check("completion has metrics", isinstance(complete["metrics"], dict))

if failures:
    print("FAIL: persona-qa kit CLI")
    for failure in failures:
        print(f"  - {failure}")
    sys.exit(1)
print("PASS: persona-qa kit CLI")
