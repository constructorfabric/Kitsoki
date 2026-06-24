#!/usr/bin/env python3
"""Discover a local project profile for dev-story onboarding."""

from __future__ import annotations

import json
import os
import re
import shlex
import sys
from pathlib import Path


def slug(value: str) -> str:
    value = value.strip().lower()
    value = re.sub(r"^@[^/]+/", "", value)
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value or "project"


def title_from_slug(value: str) -> str:
    if value == "slidey":
        return "Slidey"
    return " ".join(part.capitalize() for part in value.replace("_", "-").split("-") if part)


def parse_target(request: str, workdir: str, repo_root: str) -> Path:
    base = Path(repo_root or workdir or os.getcwd()).expanduser()
    text = (request or "").strip()
    target = ""
    if text:
        try:
            parts = shlex.split(text)
        except ValueError:
            parts = text.split()
        lowered = [p.lower() for p in parts]
        prefixes = [
            ["onboard"],
            ["project", "onboarding"],
            ["init", "project"],
            ["set", "up", "kitsoki", "for"],
        ]
        for prefix in prefixes:
            if lowered[: len(prefix)] == prefix:
                rest = parts[len(prefix) :]
                if rest and rest[0].lower() in {"this", "repo", "project"}:
                    rest = rest[1:]
                if rest:
                    target = rest[0]
                break
        if not target and len(parts) == 1:
            target = parts[0]
    if not target:
        return base.resolve()
    path = Path(target).expanduser()
    if not path.is_absolute():
        path = base / path
    return path.resolve()


def read_package(path: Path) -> dict:
    pkg = path / "package.json"
    if not pkg.exists():
        return {}
    try:
        return json.loads(pkg.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}


def command_from_scripts(package: dict, name: str) -> str:
    scripts = package.get("scripts") if isinstance(package, dict) else {}
    if isinstance(scripts, dict) and scripts.get(name):
        return f"npm run {name}" if name != "test" else "npm test"
    return ""


def discover(path: Path) -> dict:
    package = read_package(path)
    package_name = package.get("name") if isinstance(package.get("name"), str) else ""
    project_id = slug(package_name or path.name)
    deps = {}
    for key in ("dependencies", "devDependencies"):
        value = package.get(key)
        if isinstance(value, dict):
            deps.update(value)

    if project_id == "slidey":
        stack = "node/vue/vite/puppeteer declarative deck engine with web, html, pdf, and mp4 outputs"
    elif package:
        stack_bits = ["node"]
        if "vue" in deps:
            stack_bits.append("vue")
        if "puppeteer" in deps:
            stack_bits.append("puppeteer")
        if "vite" in deps:
            stack_bits.append("vite")
        stack = "/".join(stack_bits) + " project"
    elif (path / "go.mod").exists():
        stack = "go project"
    elif (path / "Cargo.toml").exists():
        stack = "rust project"
    else:
        stack = "local project"

    dev_command = command_from_scripts(package, "dev")
    if project_id == "slidey" and (path / "src" / "index.js").exists():
        examples = list((path / "examples").glob("*.slidey.json")) if (path / "examples").exists() else []
        example = "examples/hello.slidey.json" if (path / "examples" / "hello.slidey.json").exists() else ""
        if not example and examples:
            example = str(examples[0].relative_to(path))
        if example:
            dev_command = f"node src/index.js {example} --port 5000 --no-open"

    return {
        "target_path": str(path),
        "project_id": project_id,
        "project_title": title_from_slug(project_id),
        "stack": stack,
        "dev_command": dev_command,
        "test_command": command_from_scripts(package, "test"),
        "build_command": command_from_scripts(package, "build"),
        "conventions": "hybrid" if project_id == "slidey" or (path / "AGENTS.md").exists() or (path / "CLAUDE.md").exists() else "local defaults",
        "tracker": "none",
    }


def main() -> int:
    request = sys.argv[1] if len(sys.argv) > 1 else ""
    workdir = sys.argv[2] if len(sys.argv) > 2 else ""
    repo_root = sys.argv[3] if len(sys.argv) > 3 else ""
    print(json.dumps(discover(parse_target(request, workdir, repo_root)), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
