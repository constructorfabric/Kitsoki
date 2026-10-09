#!/usr/bin/env python3
"""Shared deterministic helpers for local harness profile setup."""

from __future__ import annotations

import json
import os
import re
import shutil
import subprocess
from pathlib import Path
from typing import Any


BACKENDS = {
    "claude": {
        "binary": "claude",
        "override_env": "KITSOKI_AGENT_CLAUDE_BIN",
        "auth_envs": ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"],
        "auth_files": ["~/.claude/settings.json", "~/.claude.json"],
    },
    "codex": {
        "binary": "codex",
        "override_env": "KITSOKI_AGENT_CODEX_BIN",
        "auth_envs": ["OPENAI_API_KEY", "SYNTHETIC_API_KEY"],
        "auth_files": ["~/.codex/auth.json", "~/.codex/config.toml"],
    },
    "copilot": {
        "binary": "copilot",
        "override_env": "KITSOKI_AGENT_COPILOT_BIN",
        "auth_envs": ["GH_TOKEN", "GITHUB_TOKEN"],
        "auth_files": ["~/.config/github-copilot/hosts.json"],
    },
    "agy": {
        "binary": "agy",
        "override_env": "KITSOKI_AGENT_AGY_BIN",
        "auth_envs": [],
        "auth_files": ["~/.config/agy/auth.json"],
    },
}

COMMON_ENV = [
    "ANTHROPIC_API_KEY",
    "ANTHROPIC_AUTH_TOKEN",
    "OPENAI_API_KEY",
    "OPENAI_BASE_URL",
    "SYNTHETIC_API_KEY",
    "GH_TOKEN",
    "GITHUB_TOKEN",
]

SAFE_ENV_RE = re.compile(r"^[A-Z_][A-Z0-9_]*$")
PROFILE_NAME_RE = re.compile(r"^[A-Za-z0-9_.-]+$")
CREDENTIAL_KEY_RE = re.compile(r"(token|api[_-]?key|secret|credential)", re.IGNORECASE)
CLAUDE_AUTH_STATUS_COMMAND = "claude auth status --json"
AUTH_PROBE_TIMEOUT_SECONDS = 3


def json_out(data: dict[str, Any]) -> str:
    return json.dumps(data, sort_keys=True, separators=(",", ":"))


def local_config_path(root: Path) -> Path:
    return root / ".kitsoki.local.yaml"


def base_config_path(root: Path) -> Path:
    return root / ".kitsoki.yaml"


def resolve_target(raw: str, workdir: str = "", repo_root: str = "") -> Path:
    raw = (raw or "").strip()
    if not raw:
        raw = repo_root or workdir or "."
    path = Path(os.path.expanduser(raw))
    if not path.is_absolute():
        path = Path.cwd() / path
    return path.resolve()


def _strip_comment(value: str) -> str:
    if " #" in value:
        return value.split(" #", 1)[0].rstrip()
    return value.strip()


def _unquote(value: str) -> str:
    value = _strip_comment(value).strip()
    if not value:
        return ""
    if value[0] in "'\"" and value[-1:] == value[0]:
        try:
            return json.loads(value) if value[0] == '"' else value[1:-1]
        except json.JSONDecodeError:
            return value[1:-1]
    return value


def _parse_list(value: str) -> list[str]:
    value = _strip_comment(value).strip()
    if not value:
        return []
    if value.startswith("[") and value.endswith("]"):
        inner = value[1:-1].strip()
        if not inner:
            return []
        return [_unquote(item.strip()) for item in inner.split(",") if item.strip()]
    return [_unquote(value)]


def _top_level_blocks(lines: list[str]) -> list[tuple[str, int, int, str]]:
    starts: list[tuple[str, int, str]] = []
    for idx, line in enumerate(lines):
        if not line or line.startswith((" ", "\t", "#")):
            continue
        match = re.match(r"^([A-Za-z0-9_][A-Za-z0-9_-]*):(.*)$", line)
        if match:
            starts.append((match.group(1), idx, match.group(2)))
    out: list[tuple[str, int, int, str]] = []
    for pos, (key, start, rest) in enumerate(starts):
        end = starts[pos + 1][1] if pos + 1 < len(starts) else len(lines)
        out.append((key, start, end, rest))
    return out


def find_top_level_block(lines: list[str], key: str) -> tuple[int, int, str]:
    for found, start, end, rest in _top_level_blocks(lines):
        if found == key:
            return start, end, rest
    return -1, -1, ""


def _profile_blocks(lines: list[str], start: int, end: int) -> list[tuple[str, int, int]]:
    starts: list[tuple[str, int]] = []
    for idx in range(start + 1, end):
        match = re.match(r"^  ([A-Za-z0-9_.-]+):\s*(?:#.*)?$", lines[idx])
        if match:
            starts.append((match.group(1), idx))
    out: list[tuple[str, int, int]] = []
    for pos, (name, pstart) in enumerate(starts):
        pend = starts[pos + 1][1] if pos + 1 < len(starts) else end
        out.append((name, pstart, pend))
    return out


def parse_config_text(text: str, source: str) -> dict[str, Any]:
    lines = text.splitlines()
    result: dict[str, Any] = {
        "source": source,
        "default_profile": "",
        "profiles": {},
    }
    for key, start, _end, rest in _top_level_blocks(lines):
        if key == "default_profile":
            result["default_profile"] = _unquote(rest)
            break

    hp_start, hp_end, hp_rest = find_top_level_block(lines, "harness_profiles")
    if hp_start < 0 or _strip_comment(hp_rest).strip() == "{}":
        return result

    profiles: dict[str, Any] = {}
    for name, pstart, pend in _profile_blocks(lines, hp_start, hp_end):
        profile: dict[str, Any] = {"name": name, "source": source, "env_keys": [], "env_refs": []}
        idx = pstart + 1
        while idx < pend:
            line = lines[idx]
            field = re.match(r"^    ([A-Za-z0-9_][A-Za-z0-9_-]*):(.*)$", line)
            if not field:
                idx += 1
                continue
            key = field.group(1)
            rest = field.group(2)
            if key == "env":
                env_keys: list[str] = []
                env_refs: list[str] = []
                idx += 1
                while idx < pend:
                    env_line = lines[idx]
                    env_match = re.match(r"^      ([A-Za-z_][A-Za-z0-9_]*):(.*)$", env_line)
                    if not env_match:
                        break
                    env_key = env_match.group(1)
                    env_value = _unquote(env_match.group(2))
                    env_keys.append(env_key)
                    for ref in re.findall(r"\$\{([A-Za-z_][A-Za-z0-9_]*)\}", env_value):
                        if ref not in env_refs:
                            env_refs.append(ref)
                    idx += 1
                profile["env_keys"] = env_keys
                profile["env_refs"] = env_refs
                continue
            if key in {"models", "efforts"}:
                profile[key] = _parse_list(rest)
            elif key in {"backend", "plugin", "model", "models_endpoint", "effort"}:
                profile[key] = _unquote(rest)
            idx += 1
        profiles[name] = profile
    result["profiles"] = profiles
    return result


def read_config(path: Path, source: str) -> dict[str, Any]:
    if not path.exists():
        return {"source": source, "default_profile": "", "profiles": {}, "exists": False}
    try:
        data = parse_config_text(path.read_text(encoding="utf-8"), source)
    except OSError as exc:
        return {"source": source, "default_profile": "", "profiles": {}, "exists": True, "error": str(exc)}
    data["exists"] = True
    return data


def merge_configs(base: dict[str, Any], local: dict[str, Any]) -> dict[str, Any]:
    profiles: dict[str, Any] = {}
    for name, profile in (base.get("profiles") or {}).items():
        profiles[name] = dict(profile)
    for name, profile in (local.get("profiles") or {}).items():
        profiles[name] = dict(profile)
    default_profile = str(local.get("default_profile") or base.get("default_profile") or "")
    return {"default_profile": default_profile, "profiles": profiles}


def env_sources() -> list[dict[str, Any]]:
    return [{"name": name, "present": bool(os.environ.get(name)), "source": "process-env" if os.environ.get(name) else ""} for name in COMMON_ENV]


def _json_has_credential_marker(value: Any) -> bool:
    if isinstance(value, dict):
        for key, item in value.items():
            if CREDENTIAL_KEY_RE.search(str(key)) and item not in ("", None, [], {}):
                return True
            if _json_has_credential_marker(item):
                return True
    elif isinstance(value, list):
        return any(_json_has_credential_marker(item) for item in value)
    return False


def file_has_credential_marker(path: Path) -> bool:
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError, UnicodeDecodeError):
        return False
    return _json_has_credential_marker(data)


def claude_auth_status_probe(path: str) -> dict[str, Any]:
    if not path:
        return {}
    probe: dict[str, Any] = {"command": CLAUDE_AUTH_STATUS_COMMAND}
    try:
        proc = subprocess.run(
            [path, "auth", "status", "--json"],
            check=False,
            text=True,
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=AUTH_PROBE_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return {**probe, "status": "timeout"}
    except OSError as exc:
        return {**probe, "status": "error", "error": exc.__class__.__name__}

    try:
        data = json.loads((proc.stdout or "").strip())
    except json.JSONDecodeError:
        return {**probe, "status": "unsupported", "exit_code": proc.returncode}
    if not isinstance(data, dict) or not isinstance(data.get("loggedIn"), bool):
        return {**probe, "status": "unsupported", "exit_code": proc.returncode}
    return {
        **probe,
        "status": "ok",
        "exit_code": proc.returncode,
        "logged_in": bool(data["loggedIn"]),
        "auth_method": str(data.get("authMethod") or ""),
        "api_provider": str(data.get("apiProvider") or ""),
    }


def auth_summary(auth_sources: list[dict[str, Any]], auth_probe: dict[str, Any] | None = None) -> str:
    parts: list[str] = []
    for source in auth_sources:
        kind = source.get("type")
        name = str(source.get("name") or "")
        if kind == "env":
            parts.append(f"env:{name}")
        elif source.get("credential"):
            parts.append(f"file:{name}")
        else:
            parts.append(f"file-present:{name}")
    if auth_probe and auth_probe.get("status") == "ok":
        parts.append("probe:logged-in" if auth_probe.get("logged_in") else "probe:not-logged-in")
    return ", ".join(parts)


def binary_status(name: str, spec: dict[str, Any]) -> dict[str, Any]:
    override = str(os.environ.get(spec["override_env"]) or "")
    path = override or shutil.which(spec["binary"]) or ""
    auth_sources: list[dict[str, Any]] = []
    for env_name in spec.get("auth_envs", []):
        if os.environ.get(env_name):
            auth_sources.append({"type": "env", "name": env_name, "credential": True})
    for raw_path in spec.get("auth_files", []):
        path_obj = Path(os.path.expanduser(raw_path))
        if path_obj.exists():
            auth_sources.append({
                "type": "file",
                "name": raw_path,
                "credential": file_has_credential_marker(path_obj),
            })
    installed = bool(path)
    has_env_credential = any(source.get("type") == "env" and source.get("credential") for source in auth_sources)
    has_file_credential = any(source.get("type") == "file" and source.get("credential") for source in auth_sources)
    auth_probe = claude_auth_status_probe(path) if name == "claude" and installed and not has_env_credential else {}
    if has_env_credential:
        logged_in = "yes"
    elif auth_probe.get("status") == "ok":
        logged_in = "yes" if auth_probe.get("logged_in") else "no"
    elif has_file_credential:
        logged_in = "yes"
    else:
        logged_in = "unknown" if installed else "no"
    result = {
        "backend": name,
        "binary": spec["binary"],
        "override_env": spec["override_env"],
        "installed": installed,
        "path_source": "env" if override else ("PATH" if path else ""),
        "path": path,
        "auth_sources": auth_sources,
        "auth_summary": auth_summary(auth_sources, auth_probe) or "none",
        "logged_in": logged_in,
    }
    if auth_probe:
        result["auth_probe"] = auth_probe
    return result


def backend_sources() -> list[dict[str, Any]]:
    return [binary_status(name, spec) for name, spec in BACKENDS.items()]


def _backend_ready(profile: dict[str, Any], statuses: dict[str, dict[str, Any]]) -> str:
    if profile.get("plugin"):
        return "plugin"
    if profile.get("env_refs"):
        present = all(bool(os.environ.get(ref)) for ref in profile.get("env_refs", []))
        if not present:
            return "env-missing"
    backend = str(profile.get("backend") or "claude")
    status = statuses.get(backend, {})
    if status.get("installed") and status.get("logged_in") == "yes":
        return "env-present" if profile.get("env_refs") else "installed"
    if status.get("installed") and status.get("logged_in") == "no":
        return "installed-auth-missing"
    if status.get("installed") and status.get("logged_in") == "unknown":
        return "installed-auth-unknown"
    return "missing"


def profile_list(effective: dict[str, Any], base: dict[str, Any], local: dict[str, Any], statuses: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for name in sorted((effective.get("profiles") or {}).keys()):
        profile = dict(effective["profiles"][name])
        if name in (local.get("profiles") or {}):
            source = "local"
        elif name in (base.get("profiles") or {}):
            source = "baseline"
        else:
            source = str(profile.get("source") or "effective")
        profile["source"] = source
        profile["readiness"] = _backend_ready(profile, statuses)
        out.append(profile)
    return out


def quote_yaml(value: str) -> str:
    if PROFILE_NAME_RE.match(value):
        return value
    return json.dumps(value)


def _yaml_list(items: list[str]) -> str:
    return "[" + ", ".join(quote_yaml(str(item)) for item in items) + "]"


def render_profile_block(candidate: dict[str, Any]) -> str:
    name = str(candidate.get("name") or "").strip()
    if not PROFILE_NAME_RE.match(name):
        raise ValueError(f"profile name {name!r} is invalid")
    action = str(candidate.get("action") or "")
    lines = [f"  {name}:"]
    if action == "upsert_local_llm" or candidate.get("plugin"):
        lines.append("    plugin: builtin.local_llm")
        model = str(candidate.get("model") or "").strip()
        if not model:
            raise ValueError("local LLM profile requires model")
        lines.append(f"    model: {quote_yaml(model)}")
        return "\n".join(lines) + "\n"

    backend = str(candidate.get("backend") or "codex").strip()
    if backend not in BACKENDS:
        raise ValueError(f"backend {backend!r} is invalid")
    model = str(candidate.get("model") or "").strip()
    if not model:
        raise ValueError("profile requires model")
    lines.append(f"    backend: {backend}")
    lines.append(f"    model: {quote_yaml(model)}")
    models = candidate.get("models")
    if isinstance(models, list) and models:
        lines.append(f"    models: {_yaml_list([str(item) for item in models])}")
    else:
        lines.append(f"    models: [{quote_yaml(model)}]")
    models_endpoint = str(candidate.get("models_endpoint") or "").strip()
    if models_endpoint:
        lines.append(f"    models_endpoint: {models_endpoint}")
    env_entries: list[tuple[str, str]] = []
    base_url = str(candidate.get("base_url") or "").strip()
    env_var = str(candidate.get("env_var") or "").strip()
    if base_url:
        env_entries.append(("OPENAI_BASE_URL", base_url))
    if env_var:
        if not SAFE_ENV_RE.match(env_var):
            raise ValueError(f"env var {env_var!r} is invalid; provide a variable name, not a raw key")
        env_entries.append(("OPENAI_API_KEY", "${" + env_var + "}"))
    if env_entries:
        lines.append("    env:")
        for key, value in env_entries:
            lines.append(f"      {key}: {quote_yaml(value)}")
    return "\n".join(lines) + "\n"


def preview_for_candidate(candidate: dict[str, Any]) -> str:
    action = str(candidate.get("action") or "")
    name = str(candidate.get("name") or "").strip()
    if not action or not name:
        return ""
    if not PROFILE_NAME_RE.match(name):
        raise ValueError(f"profile name {name!r} is invalid")
    lines = [f"default_profile: {quote_yaml(name)}"]
    if action in {"upsert_openai", "upsert_local_llm", "upsert_backend"}:
        lines.append("harness_profiles:")
        lines.append(render_profile_block(candidate).rstrip())
    return "\n".join(lines).rstrip() + "\n"


def recommended_candidate(profiles: list[dict[str, Any]], default_profile: str, statuses: dict[str, dict[str, Any]]) -> dict[str, Any]:
    ready_profiles = [p for p in profiles if p.get("readiness") in {"installed", "plugin", "env-present"}]
    if default_profile and any(p.get("name") == default_profile and p.get("readiness") in {"installed", "plugin", "env-present"} for p in ready_profiles):
        return {}
    if ready_profiles:
        return {"action": "set_default", "name": str(ready_profiles[0]["name"])}
    codex = statuses.get("codex", {})
    if codex.get("installed") and codex.get("logged_in") == "yes":
        return {
            "action": "upsert_backend",
            "name": "codex-native",
            "backend": "codex",
            "model": "gpt-5.5",
            "models": ["gpt-5-codex", "gpt-5", "gpt-5.5"],
        }
    if os.environ.get("OPENAI_API_KEY") and codex.get("installed"):
        return {
            "action": "upsert_openai",
            "name": "openai-codex",
            "backend": "codex",
            "model": "gpt-5.5",
            "models": ["gpt-5-codex", "gpt-5", "gpt-5.5"],
            "env_var": "OPENAI_API_KEY",
            "base_url": os.environ.get("OPENAI_BASE_URL", ""),
        }
    claude = statuses.get("claude", {})
    if claude.get("installed") and claude.get("logged_in") == "yes":
        return {
            "action": "upsert_backend",
            "name": "claude-native",
            "backend": "claude",
            "model": "opus",
            "models": ["opus", "sonnet", "haiku"],
        }
    return {}


def discover(root: Path) -> dict[str, Any]:
    warnings: list[str] = []
    base_path = base_config_path(root)
    local_path = local_config_path(root)
    base = read_config(base_path, "baseline")
    local = read_config(local_path, "local")
    if base.get("error"):
        warnings.append(f"could not read {base_path}: {base['error']}")
    if local.get("error"):
        warnings.append(f"could not read {local_path}: {local['error']}")
    effective = merge_configs(base, local)
    statuses_list = backend_sources()
    statuses = {str(item["backend"]): item for item in statuses_list}
    for status in statuses_list:
        probe = status.get("auth_probe") if isinstance(status.get("auth_probe"), dict) else {}
        if probe.get("status") == "ok" and status.get("logged_in") == "no":
            warnings.append(f"{status['backend']} auth probe reports not logged in")
        elif status.get("installed") and status.get("logged_in") == "unknown" and status.get("auth_sources"):
            warnings.append(
                f"{status['backend']} has config/auth files but no credential marker; login status is unknown"
            )
    profiles = profile_list(effective, base, local, statuses)
    candidate = recommended_candidate(profiles, str(effective.get("default_profile") or ""), statuses)
    try:
        preview = preview_for_candidate(candidate) if candidate else ""
    except ValueError as exc:
        warnings.append(str(exc))
        preview = ""
        candidate = {}
    if not base_path.exists():
        warnings.append(".kitsoki.yaml is missing; this setup writes only .kitsoki.local.yaml")
    if not local_path.exists():
        warnings.append(".kitsoki.local.yaml does not exist yet; apply will create it")
    return {
        "schema": "kitsoki-profile-setup-discovery/v1",
        "status": "ok",
        "target_path": str(root),
        "config_path": str(base_path),
        "local_config_path": str(local_path),
        "baseline_exists": bool(base.get("exists")),
        "local_exists": bool(local.get("exists")),
        "baseline_default_profile": str(base.get("default_profile") or ""),
        "local_default_profile": str(local.get("default_profile") or ""),
        "default_profile": str(effective.get("default_profile") or ""),
        "profiles": profiles,
        "profile_sources": {
            "baseline": sorted((base.get("profiles") or {}).keys()),
            "local": sorted((local.get("profiles") or {}).keys()),
        },
        "backend_sources": statuses_list,
        "env_sources": env_sources(),
        "candidate_profile": candidate,
        "candidate_action": str(candidate.get("action") or ""),
        "patch_preview": preview,
        "warnings": warnings,
    }


def _is_tracked(root: Path, path: Path) -> bool:
    try:
        inside = subprocess.run(
            ["git", "-C", str(root), "rev-parse", "--is-inside-work-tree"],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
        if inside.returncode != 0 or inside.stdout.strip() != "true":
            return False
        rel = os.path.relpath(path, root)
        proc = subprocess.run(
            ["git", "-C", str(root), "ls-files", "--error-unmatch", "--", rel],
            check=False,
            text=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        return proc.returncode == 0
    except (OSError, ValueError):
        return False


def _insert_index_after_header(lines: list[str]) -> int:
    idx = 0
    while idx < len(lines) and (lines[idx].strip() == "" or lines[idx].lstrip().startswith("#")):
        idx += 1
    return idx


def set_top_level_scalar(lines: list[str], key: str, value: str) -> list[str]:
    line = f"{key}: {quote_yaml(value)}"
    start, _end, _rest = find_top_level_block(lines, key)
    if start >= 0:
        lines[start] = line
        return lines
    idx = _insert_index_after_header(lines)
    lines[idx:idx] = [line]
    return lines


def upsert_profile_block(lines: list[str], profile_name: str, block: str) -> list[str]:
    block_lines = block.rstrip("\n").splitlines()
    hp_start, hp_end, hp_rest = find_top_level_block(lines, "harness_profiles")
    if hp_start < 0:
        if lines and lines[-1].strip():
            lines.append("")
        lines.append("harness_profiles:")
        lines.extend(block_lines)
        return lines
    if _strip_comment(hp_rest).strip() == "{}":
        lines[hp_start] = "harness_profiles:"
        hp_end = hp_start + 1
    for name, pstart, pend in _profile_blocks(lines, hp_start, hp_end):
        if name == profile_name:
            lines[pstart:pend] = block_lines
            return lines
    lines[hp_end:hp_end] = block_lines
    return lines


def apply_candidate(root: Path, candidate: dict[str, Any]) -> dict[str, Any]:
    action = str(candidate.get("action") or "").strip()
    name = str(candidate.get("name") or "").strip()
    if not action:
        raise ValueError("no candidate action selected")
    if not PROFILE_NAME_RE.match(name):
        raise ValueError(f"profile name {name!r} is invalid")
    env_var = str(candidate.get("env_var") or "").strip()
    if env_var and not SAFE_ENV_RE.match(env_var):
        raise ValueError(f"env var {env_var!r} is invalid; provide a variable name, not a raw key")
    local_path = local_config_path(root)
    if local_path.exists() and _is_tracked(root, local_path):
        raise ValueError(f"{local_path} is tracked by git; refusing to write secret-bearing local config")

    old = local_path.read_text(encoding="utf-8") if local_path.exists() else (
        "# Kitsoki local harness profile overrides.\n"
        "# Generated by dev-story local harness profile setup. Keep this file gitignored.\n"
    )
    lines = old.splitlines()
    lines = set_top_level_scalar(lines, "default_profile", name)
    if action in {"upsert_openai", "upsert_local_llm", "upsert_backend"}:
        lines = upsert_profile_block(lines, name, render_profile_block(candidate))
    new = "\n".join(lines).rstrip() + "\n"
    writes: list[str] = []
    if new != old:
        local_path.write_text(new, encoding="utf-8")
        writes.append(str(local_path))
    report = discover(root)
    return {
        "schema": "kitsoki-profile-setup-apply/v1",
        "status": "applied",
        "target_path": str(root),
        "local_config_path": str(local_path),
        "writes": writes,
        "default_profile": report.get("default_profile", ""),
        "profiles": report.get("profiles", []),
        "candidate_profile": candidate,
        "patch_preview": preview_for_candidate(candidate),
        "warnings": report.get("warnings", []),
    }
