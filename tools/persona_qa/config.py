"""Persona-QA kit configuration and lightweight validation.

This module is intentionally stdlib-first. PyYAML/jsonschema are useful when
present, but the persona-QA kit has to validate in fresh project checkouts
where those Python packages may not be installed.
"""

from __future__ import annotations

import json
import shlex
from dataclasses import dataclass
from pathlib import Path
from typing import Any

try:  # pragma: no cover - most repo test environments do not have PyYAML.
    import yaml  # type: ignore
except ModuleNotFoundError:  # pragma: no cover - exercised indirectly.
    from tools.arena import _yaml as yaml  # type: ignore


CONFIG_VERSION = 1
SCHEMA_VERSION = "v1"
CANONICAL_DRIVER_CAPABILITIES = {
    "visual.open",
    "visual.observe",
    "visual.act",
    "session.open",
    "session.status",
    "session.submit",
    "session.drive",
    "session.inspect",
    "session.trace",
    "render.tui",
}

DEFAULT_PATHS = {
    ("catalogs", "catalog"): "tools/product-journey/catalog.json",
    ("catalogs", "personas"): "tools/product-journey/personas.json",
    ("catalogs", "scenarios"): "tools/product-journey/scenarios.json",
    ("catalogs", "github_targets"): "tools/product-journey/github-targets.json",
    ("drivers", "dir"): "tools/product-journey/drivers",
    ("artifacts", "root"): ".artifacts/product-journey",
    ("artifacts", "run_log"): ".context/product-journey-runlog.md",
    ("deck", "publish"): "docs/decks/product-journey-eval.slidey.json",
    ("schemas", "root"): "schemas/persona-qa/v1",
}


@dataclass(frozen=True)
class PersonaQAConfig:
    """Resolved persona-QA kit configuration."""

    raw: dict[str, Any]
    base_dir: Path
    repo_root: Path
    config_path: Path | None = None

    @property
    def project_root(self) -> Path:
        project = self.raw.get("project", {}) or {}
        root = project.get("root", ".")
        return _resolve_path(self.base_dir, root)

    @property
    def default_driver(self) -> str:
        drivers = self.raw.get("drivers", {}) or {}
        return str(drivers.get("default") or "kitsoki-mcp")

    def path(self, section: str, key: str) -> Path:
        value = (self.raw.get(section, {}) or {}).get(key)
        default = DEFAULT_PATHS.get((section, key), "")
        raw_value = value if value not in (None, "") else default
        base = self.repo_root if self.config_path is None else self.project_root
        return _resolve_path(base, raw_value)

    def runner_command(self) -> list[str]:
        runner = self.raw.get("runner", {}) or {}
        command = runner.get("command")
        if isinstance(command, list):
            return [str(item) for item in command]
        if isinstance(command, str) and command.strip():
            return shlex.split(command)
        return []

    def summary(self) -> dict[str, str]:
        return {
            "config_path": str(self.config_path or ""),
            "project_root": str(self.project_root),
            "catalog": str(self.path("catalogs", "catalog")),
            "personas": str(self.path("catalogs", "personas")),
            "scenarios": str(self.path("catalogs", "scenarios")),
            "github_targets": str(self.path("catalogs", "github_targets")),
            "drivers_dir": str(self.path("drivers", "dir")),
            "default_driver": self.default_driver,
            "artifact_root": str(self.path("artifacts", "root")),
            "schema_root": str(self.path("schemas", "root")),
        }


def default_config(repo_root: str | Path) -> PersonaQAConfig:
    root = Path(repo_root).resolve()
    raw = {
        "version": CONFIG_VERSION,
        "project": {"id": "kitsoki", "root": "."},
        "catalogs": {
            "catalog": DEFAULT_PATHS[("catalogs", "catalog")],
            "personas": DEFAULT_PATHS[("catalogs", "personas")],
            "scenarios": DEFAULT_PATHS[("catalogs", "scenarios")],
            "github_targets": DEFAULT_PATHS[("catalogs", "github_targets")],
        },
        "drivers": {"default": "kitsoki-mcp", "dir": DEFAULT_PATHS[("drivers", "dir")]},
        "artifacts": {
            "root": DEFAULT_PATHS[("artifacts", "root")],
            "run_log": DEFAULT_PATHS[("artifacts", "run_log")],
        },
        "deck": {"publish": DEFAULT_PATHS[("deck", "publish")]},
        "schemas": {"root": DEFAULT_PATHS[("schemas", "root")]},
    }
    return PersonaQAConfig(raw=raw, base_dir=root, repo_root=root, config_path=None)


def load_config(path: str | Path | None, *, repo_root: str | Path) -> PersonaQAConfig:
    if not path:
        return default_config(repo_root)
    config_path = Path(path).expanduser()
    if not config_path.is_absolute():
        config_path = (Path.cwd() / config_path).resolve()
    raw = load_data(config_path)
    if raw is None:
        raw = {}
    if not isinstance(raw, dict):
        raise ValueError(f"{config_path} must contain a YAML/JSON object")
    return PersonaQAConfig(
        raw=raw,
        base_dir=config_path.parent,
        repo_root=Path(repo_root).resolve(),
        config_path=config_path,
    )


def load_data(path: str | Path) -> Any:
    p = Path(path)
    text = p.read_text(encoding="utf-8")
    if p.suffix.lower() == ".json":
        return json.loads(text)
    return yaml.safe_load(text)


def dump_yaml(data: Any) -> str:
    return yaml.safe_dump(data, sort_keys=False)


def load_collection(path: str | Path, key: str) -> list[dict[str, Any]]:
    """Load a single JSON/YAML collection file or a directory of item files."""

    p = Path(path)
    if p.is_dir():
        items: list[dict[str, Any]] = []
        for child in sorted(list(p.glob("*.json")) + list(p.glob("*.yaml")) + list(p.glob("*.yml"))):
            data = load_data(child)
            if isinstance(data, dict) and isinstance(data.get(key), list):
                items.extend(_object_items(data[key], child))
            elif isinstance(data, dict):
                items.append(data)
            else:
                raise ValueError(f"{child} must contain an object or {key} array")
        return items
    data = load_data(p)
    if not isinstance(data, dict) or not isinstance(data.get(key), list):
        raise ValueError(f"{p} must contain a {key} array")
    return _object_items(data[key], p)


def validate_config(config: PersonaQAConfig) -> dict[str, Any]:
    issues: list[dict[str, str]] = []
    raw = config.raw
    version = raw.get("version")
    if version != CONFIG_VERSION:
        _issue(issues, "error", "config-version", f"version must be {CONFIG_VERSION}", repr(version))

    project = raw.get("project", {}) or {}
    if not isinstance(project, dict):
        _issue(issues, "error", "project-shape", "project must be an object")
    elif not str(project.get("id", "")).strip():
        _issue(issues, "error", "project-id", "project.id is required")

    counts = {
        "personas": 0,
        "scenarios": 0,
        "github_targets": 0,
        "drivers": 0,
        "schemas": 0,
    }
    _validate_catalog(config.path("catalogs", "catalog"), issues)
    counts["personas"] = _validate_collection(
        config.path("catalogs", "personas"),
        "personas",
        ["id", "label", "description", "surface_preference", "risk_focus"],
        issues,
    )
    counts["scenarios"] = _validate_collection(
        config.path("catalogs", "scenarios"),
        "scenarios",
        ["id", "label", "stage", "task", "primary_story", "required_mcp", "evidence", "success_criteria"],
        issues,
    )
    counts["github_targets"] = _validate_github_targets(config.path("catalogs", "github_targets"), issues)
    counts["drivers"] = _validate_drivers(config.path("drivers", "dir"), config.default_driver, issues)
    counts["schemas"] = _validate_schema_root(config.path("schemas", "root"), issues)

    artifact_root = config.path("artifacts", "root")
    if artifact_root.exists() and not artifact_root.is_dir():
        _issue(issues, "error", "artifact-root", "artifacts.root must be a directory", str(artifact_root))

    errors = sum(1 for issue in issues if issue["severity"] == "error")
    warnings = sum(1 for issue in issues if issue["severity"] == "warning")
    return {
        "status": "valid" if errors == 0 else "invalid",
        "errors": errors,
        "warnings": warnings,
        "issues": issues,
        "counts": counts,
        "config": config.summary(),
    }


def validate_schema_subset(instance: Any, schema: dict[str, Any], *, path: str = "$") -> list[str]:
    """Small JSON-Schema subset validator used when jsonschema is unavailable."""

    errors: list[str] = []
    expected_type = schema.get("type")
    if expected_type and not _matches_type(instance, expected_type):
        errors.append(f"{path}: expected {expected_type}, got {type(instance).__name__}")
        return errors
    if "enum" in schema and instance not in schema["enum"]:
        errors.append(f"{path}: expected one of {schema['enum']}, got {instance!r}")
    if isinstance(instance, dict):
        for field in schema.get("required", []):
            if field not in instance:
                errors.append(f"{path}: missing required field {field}")
        properties = schema.get("properties", {}) or {}
        additional = schema.get("additionalProperties", True)
        for key, value in instance.items():
            if key in properties:
                errors.extend(validate_schema_subset(value, properties[key], path=f"{path}.{key}"))
            elif additional is False:
                errors.append(f"{path}: unexpected field {key}")
    if isinstance(instance, list):
        min_items = schema.get("minItems")
        if isinstance(min_items, int) and len(instance) < min_items:
            errors.append(f"{path}: expected at least {min_items} item(s)")
        item_schema = schema.get("items")
        if isinstance(item_schema, dict):
            for index, item in enumerate(instance):
                errors.extend(validate_schema_subset(item, item_schema, path=f"{path}[{index}]"))
    return errors


def template_config() -> dict[str, Any]:
    return {
        "version": CONFIG_VERSION,
        "project": {
            "id": "local-app",
            "root": ".",
        },
        "catalogs": {
            "catalog": "persona-qa/catalog.json",
            "personas": "persona-qa/personas",
            "scenarios": "persona-qa/scenarios",
            "github_targets": "persona-qa/github-targets.json",
        },
        "drivers": {
            "default": "web-generic",
            "dir": "persona-qa/drivers",
        },
        "artifacts": {
            "root": ".artifacts/persona-qa",
            "run_log": ".context/persona-qa-runlog.md",
        },
        "deck": {
            "publish": "docs/decks/persona-qa-latest.slidey.json",
        },
        "schemas": {
            "root": "persona-qa/schemas/v1",
        },
    }


def template_catalog() -> dict[str, Any]:
    return {
        "program": "Scenario QA",
        "targets": [
            {
                "id": "local-app",
                "label": "Local App",
                "stack": "web",
                "run_mode": "local-fixture",
                "status": "candidate",
                "notes": "Replace this with your product, repo, or app target.",
            }
        ],
    }


def template_github_targets() -> dict[str, Any]:
    return {
        "version": 1,
        "selection_contract": {
            "host": "github.com",
            "license": "open-source",
            "popularity": "project-specific",
            "open_bug_floor": 0,
            "refresh_note": "Populate targets before running a GitHub matrix sweep.",
        },
        "targets": [],
    }


def template_persona() -> dict[str, Any]:
    return {
        "id": "core-maintainer",
        "label": "Core maintainer",
        "description": "Maintains the project and is skeptical of broad, hard-to-review automation.",
        "surface_preference": "terminal-first",
        "risk_focus": ["reviewability", "setup clarity", "deterministic proof"],
        "persona_lens": {
            "starting_surface": "terminal-first; use visible app state before reading logs",
            "first_question": "Can I reach the promised workflow and see proof without tribal context?",
            "evidence_emphasis": "screenshots or TUI frames, replayable traces, deterministic checks, and concise findings",
            "escalation_trigger": "missing setup steps, hidden prerequisites, or unsupported product claims",
            "finding_bias": "Prefer findings that make the workflow easier to trust and review.",
        },
    }


def template_scenario() -> dict[str, Any]:
    return {
        "id": "project-onboarding",
        "label": "Project onboarding",
        "stage": "onboard_project",
        "task": (
            "Start from a fresh checkout or product entrypoint and decide whether a new contributor "
            "can reach a concrete first workflow with proof-backed next steps."
        ),
        "primary_story": "local-app",
        "required_mcp": ["visual.open", "visual.observe"],
        "evidence": ["browser_screenshot", "navigation_trace", "key_interaction_video"],
        "success_criteria": [
            "The first useful action is visible without private project context.",
            "Required setup or credentials are explicit before the user is blocked.",
            "Any broken or confusing behavior is captured as a finding with evidence.",
        ],
        "natural_utterances": [
            {
                "text": "I am new here. What should I try first, and how do I know it worked?",
                "source": "persona-qa-template",
                "source_ref": "first-run",
            }
        ],
        "case_variants": [
            {
                "id": "fresh-checkout",
                "utterance": "I cloned the repo and want the shortest trustworthy first run.",
                "setup": "Start with no warmed local state beyond documented prerequisites.",
                "success_focus": "The product leads to one concrete next step and a proof artifact.",
            }
        ],
        "transports": {
            "allowed": ["web"],
            "required": ["web"],
            "overrides": {
                "web": {
                    "required_mcp": ["visual.open", "visual.observe"],
                    "evidence": ["browser_screenshot", "navigation_trace", "key_interaction_video"],
                }
            },
        },
    }


def template_driver() -> dict[str, Any]:
    capabilities = {
        "visual.open": "visual.open",
        "visual.observe": "visual.observe",
        "visual.act": "visual.act",
        "session.open": "session.open",
        "session.status": "session.status",
        "session.submit": "session.submit",
        "session.drive": "session.drive",
        "session.inspect": "session.inspect",
        "session.trace": "session.trace",
        "render.tui": "render.tui",
    }
    return {
        "id": "web-generic",
        "label": "Generic web app driver",
        "app_kind": "web",
        "capabilities": capabilities,
        "launch": {
            "command": "npm run dev",
            "cwd": ".",
            "ready": {"http": "http://127.0.0.1:5173", "timeout_s": 90},
        },
        "affordances": {
            "primary_action": "button[type=submit], [data-testid=primary-action]",
            "navigation": "nav, [role=navigation]",
        },
        "evidence_contract": {
            "proof_sources": ["local", "cassette", "retained", "external"],
            "default_capture": "browser_screenshot",
        },
        "oracles": [],
        "notes": [
            "Replace launch.command and affordances with your app's real local development contract.",
            "Replay/CI runs must use cassettes or local fixtures, not live LLM calls.",
        ],
    }


def _validate_catalog(path: Path, issues: list[dict[str, str]]) -> int:
    try:
        data = load_data(path)
    except Exception as exc:
        _issue(issues, "error", "catalog-load", "catalog could not be loaded", f"{path}: {exc}")
        return 0
    if not isinstance(data, dict) or not isinstance(data.get("targets"), list):
        _issue(issues, "error", "catalog-shape", "catalog must contain a targets array", str(path))
        return 0
    if not data["targets"]:
        _issue(issues, "warning", "catalog-empty", "catalog has no targets", str(path))
    for item in data["targets"]:
        if not isinstance(item, dict) or not item.get("id") or not item.get("label"):
            _issue(issues, "error", "catalog-target-shape", "each target requires id and label", str(path))
    return len(data["targets"])


def _validate_collection(
    path: Path,
    key: str,
    required: list[str],
    issues: list[dict[str, str]],
) -> int:
    try:
        items = load_collection(path, key)
    except Exception as exc:
        _issue(issues, "error", f"{key}-load", f"{key} could not be loaded", f"{path}: {exc}")
        return 0
    if not items:
        _issue(issues, "error", f"{key}-empty", f"{key} must contain at least one item", str(path))
    seen: set[str] = set()
    for item in items:
        missing = [field for field in required if field not in item or item[field] in (None, "")]
        if missing:
            _issue(issues, "error", f"{key}-required", f"{item.get('id', '<unknown>')} missing required fields", ", ".join(missing))
        item_id = str(item.get("id", ""))
        if item_id in seen:
            _issue(issues, "error", f"{key}-duplicate-id", "duplicate id", item_id)
        seen.add(item_id)
    return len(items)


def _validate_github_targets(path: Path, issues: list[dict[str, str]]) -> int:
    try:
        data = load_data(path)
    except Exception as exc:
        _issue(issues, "error", "github-targets-load", "github target catalog could not be loaded", f"{path}: {exc}")
        return 0
    if not isinstance(data, dict) or not isinstance(data.get("targets"), list):
        _issue(issues, "error", "github-targets-shape", "github target catalog must contain a targets array", str(path))
        return 0
    if not isinstance(data.get("selection_contract", {}), dict):
        _issue(issues, "error", "github-selection-contract", "selection_contract must be an object", str(path))
    return len(data["targets"])


def _validate_drivers(path: Path, default_driver: str, issues: list[dict[str, str]]) -> int:
    if not path.is_dir():
        _issue(issues, "error", "drivers-dir", "drivers.dir must be a directory", str(path))
        return 0
    manifests = sorted(path.glob("*.json"))
    default_path = path / f"{default_driver}.json"
    if not default_path.exists():
        _issue(issues, "error", "default-driver", "drivers.default manifest does not exist", str(default_path))
    for manifest_path in manifests:
        try:
            manifest = load_data(manifest_path)
        except Exception as exc:
            _issue(issues, "error", "driver-load", "driver manifest could not be loaded", f"{manifest_path}: {exc}")
            continue
        if not isinstance(manifest, dict):
            _issue(issues, "error", "driver-shape", "driver manifest must be an object", str(manifest_path))
            continue
        for field in ["id", "label", "app_kind", "capabilities"]:
            if not manifest.get(field):
                _issue(issues, "error", "driver-required", f"{manifest_path.name} missing {field}", str(manifest_path))
        capabilities = manifest.get("capabilities", {}) or {}
        if isinstance(capabilities, dict):
            missing = sorted(CANONICAL_DRIVER_CAPABILITIES - set(capabilities))
            if missing:
                _issue(issues, "error", "driver-capabilities", f"{manifest_path.name} missing capabilities", ", ".join(missing))
    return len(manifests)


def _validate_schema_root(path: Path, issues: list[dict[str, str]]) -> int:
    expected = [
        "config.schema.json",
        "persona.schema.json",
        "scenario.schema.json",
        "driver-manifest.schema.json",
        "run-bundle.schema.json",
        "leg-result.schema.json",
        "transport-suite.schema.json",
        "review.schema.json",
    ]
    if not path.is_dir():
        _issue(issues, "error", "schema-root", "schemas.root must be a directory", str(path))
        return 0
    count = 0
    for name in expected:
        schema_path = path / name
        if not schema_path.exists():
            _issue(issues, "error", "schema-missing", "public schema file is missing", str(schema_path))
            continue
        try:
            schema = json.loads(schema_path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            _issue(issues, "error", "schema-json", "schema file is not valid JSON", f"{schema_path}: {exc}")
            continue
        if not schema.get("$schema") or not schema.get("$id"):
            _issue(issues, "error", "schema-contract", "schema needs $schema and $id", str(schema_path))
        count += 1
    return count


def _object_items(values: list[Any], path: Path) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for index, item in enumerate(values):
        if not isinstance(item, dict):
            raise ValueError(f"{path}[{index}] must be an object")
        out.append(item)
    return out


def _matches_type(value: Any, expected: Any) -> bool:
    if isinstance(expected, list):
        return any(_matches_type(value, item) for item in expected)
    return {
        "object": isinstance(value, dict),
        "array": isinstance(value, list),
        "string": isinstance(value, str),
        "integer": isinstance(value, int) and not isinstance(value, bool),
        "number": isinstance(value, (int, float)) and not isinstance(value, bool),
        "boolean": isinstance(value, bool),
        "null": value is None,
    }.get(str(expected), True)


def _resolve_path(base: Path, value: Any) -> Path:
    p = Path(str(value)).expanduser()
    if p.is_absolute():
        return p
    return (base / p).resolve()


def _issue(
    issues: list[dict[str, str]],
    severity: str,
    issue_id: str,
    message: str,
    detail: str = "",
) -> None:
    issues.append({
        "severity": severity,
        "id": issue_id,
        "message": message,
        "detail": detail,
    })
