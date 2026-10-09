#!/usr/bin/env python3
"""Discover a local project profile for dev-story onboarding."""

from __future__ import annotations

import json
import os
import re
import shlex
import subprocess
import sys
from pathlib import Path

try:
    import tomllib
except ModuleNotFoundError:  # pragma: no cover - exercised on older Python runtimes only.
    try:
        import tomli as tomllib  # type: ignore[no-redef]
    except ModuleNotFoundError:  # pragma: no cover
        tomllib = None  # type: ignore[assignment]

sys.path.insert(0, str(Path(__file__).resolve().parent))
from init_transcripts import transcript_evidence, transcript_slug  # noqa: E402


STARTER_STORY_CATALOG = {
    "setup": {
        "title": "Project setup",
        "source_story": "dev-story:onboarding",
        "summary": "Onboard the checkout, install project-local Kitsoki tooling, and run explicit readiness checks.",
        "expansion": "Keep this enabled as the team's bootstrap and refresh path.",
    },
    "bugfix": {
        "title": "Bug fixing",
        "source_story": "bugfix",
        "summary": "Drive a picked bug through reproduce, fix, test, review, and validation gates.",
        "expansion": "Add narrower project bug policies through profile customizations or prompt overlays.",
    },
    "repo-bakeoff": {
        "title": "Repo-history capsules",
        "source_story": "repo-bakeoff",
        "summary": "Turn historical bug fixes into capsule records, prove RED/GREEN arming, and render live-cell commands.",
        "expansion": "Use once the repo has 3-10 isolated historical bugs with regression tests or PR-proven oracles.",
    },
    "pr-refinement": {
        "title": "PR refinement",
        "source_story": "pr-refinement",
        "summary": "Open or attach to a PR, monitor CI/review feedback, respond, and merge when accepted.",
        "expansion": "Add provider-specific PR policy after the base loop is proven on real pull requests.",
    },
    "git-ops": {
        "title": "Git operations",
        "source_story": "git-ops",
        "summary": "Use the guarded git command hub for status, worktrees, commits, rebases, sync, and integration.",
        "expansion": "Extend with project branch and remote-sync policy once the team's git flow is stable.",
    },
}

STORY_PACKS = {
    "core-engineering": {
        "title": "Core engineering starter",
        "summary": "Focused first-run set for ordinary software repositories: setup, bug fixing, repo-history capsules, PR refinement, and git operations.",
        "stories": ["setup", "bugfix", "repo-bakeoff", "pr-refinement", "git-ops"],
    },
    "core-setup": {
        "title": "Core setup",
        "summary": "Bring a project under project-local Kitsoki tooling with setup and guarded git operations only.",
        "stories": ["setup", "git-ops"],
    },
    "planning-delivery": {
        "title": "Planning and delivery",
        "summary": "Add PRD/design and implementation loops on top of setup and git operations.",
        "stories": ["setup", "git-ops", "prd", "implementation", "pr-refinement"],
    },
    "review-quality": {
        "title": "Review and quality",
        "summary": "Emphasize bug fixing, test repair, code review, docs review, and git operations.",
        "stories": ["setup", "git-ops", "bugfix", "fix-tests", "code-review", "docs-review", "pr-refinement"],
    },
    "full-dev-story": {
        "title": "Full dev-story",
        "summary": "Expose the full day-level dev-story catalog after the team is ready for broader adoption.",
        "stories": [
            "setup",
            "git-ops",
            "bugfix",
            "fix-tests",
            "pr-refinement",
            "prd",
            "implementation",
            "deliver",
            "code-review",
            "docs-review",
            "deploy",
            "observability",
            "incident",
        ],
    },
}

DEFAULT_STORY_PACK_ID = "core-engineering"
DEFAULT_STARTER_STORY_IDS = STORY_PACKS[DEFAULT_STORY_PACK_ID]["stories"]

STARTER_STORY_ALIASES = {
    "bugfixing": "bugfix",
    "bug-fixing": "bugfix",
    "bug-fix": "bugfix",
    "bugfix-oracles": "repo-bakeoff",
    "oracle-capsules": "repo-bakeoff",
    "repo-history-capsules": "repo-bakeoff",
    "capsules": "repo-bakeoff",
    "oracles": "repo-bakeoff",
    "repo-bakeoff": "repo-bakeoff",
    "bakeoff": "repo-bakeoff",
    "bugs": "bugfix",
    "pr": "pr-refinement",
    "prs": "pr-refinement",
    "pull-request": "pr-refinement",
    "pull-requests": "pr-refinement",
    "pr-refine": "pr-refinement",
    "pr-refinement": "pr-refinement",
    "gitops": "git-ops",
    "git": "git-ops",
    "git-ops": "git-ops",
    "onboarding": "setup",
    "init": "setup",
    "setup": "setup",
}

STORY_PACK_ALIASES = {
    "engineering": "core-engineering",
    "core-engineering": "core-engineering",
    "focusedengineering": "core-engineering",
    "focused_engineering": "core-engineering",
    "focused": "core-engineering",
    "focused-engineering": "core-engineering",
    "targeted": "core-engineering",
    "targeted-engineering": "core-engineering",
    "core": "core-setup",
    "setup": "core-setup",
    "minimal": "core-setup",
    "essentials": "core-setup",
    "planning": "planning-delivery",
    "delivery": "planning-delivery",
    "planning-delivery": "planning-delivery",
    "prd": "planning-delivery",
    "quality": "review-quality",
    "review": "review-quality",
    "review-quality": "review-quality",
    "full": "full-dev-story",
    "all": "full-dev-story",
    "full-dev": "full-dev-story",
    "full-dev-story": "full-dev-story",
}

PACK_OPTION_KEYS = {"--pack", "--story-pack", "--starter-pack"}
PACK_ASSIGNMENT_KEYS = {"pack", "story_pack", "story-pack", "starter_pack", "starter-pack"}
STORY_OPTION_KEYS = {"--stories", "--starter-stories", "--story-focus", "--focus"}
STORY_ASSIGNMENT_KEYS = {"stories", "starter_stories", "starter-stories", "story_focus", "story-focus", "focus"}


def slug(value: str) -> str:
    value = value.strip().lower()
    value = re.sub(r"^@[^/]+/", "", value)
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value or "project"


def title_from_slug(value: str) -> str:
    if value == "slidey":
        return "Slidey"
    return " ".join(part.capitalize() for part in value.replace("_", "-").split("-") if part)


def normalize_story_id(value: str) -> str:
    text = value.strip().strip("'\"`").lower().replace("_", "-")
    text = re.sub(r"[^a-z0-9-]+", "-", text).strip("-")
    return STARTER_STORY_ALIASES.get(text, text)


def normalize_story_pack_id(value: str) -> str:
    text = value.strip().strip("'\"`").lower().replace("_", "-")
    text = re.sub(r"[^a-z0-9-]+", "-", text).strip("-")
    if text == "custom":
        return "custom"
    return STORY_PACK_ALIASES.get(text, text)


def story_pack(pack_id: str | None) -> dict:
    normalized = normalize_story_pack_id(pack_id or DEFAULT_STORY_PACK_ID)
    if normalized == "custom":
        return {
            "id": "custom",
            "title": "Custom story set",
            "summary": "Operator-specified story list.",
            "stories": [],
        }
    base = STORY_PACKS.get(normalized) or STORY_PACKS[DEFAULT_STORY_PACK_ID]
    return {"id": normalized if normalized in STORY_PACKS else DEFAULT_STORY_PACK_ID, **base}


def story_pack_catalog() -> list[dict]:
    return [story_pack(pack_id) for pack_id in STORY_PACKS]


def story_pack_ids(pack_id: str | None) -> list[str]:
    return [str(item) for item in story_pack(pack_id).get("stories", [])]


def story_pack_title(pack_id: str | None) -> str:
    return str(story_pack(pack_id).get("title") or story_pack(DEFAULT_STORY_PACK_ID)["title"])


def story_pack_summary(pack_id: str | None) -> str:
    return str(story_pack(pack_id).get("summary") or story_pack(DEFAULT_STORY_PACK_ID)["summary"])


def starter_story_entry(story_id: str) -> dict:
    story_id = normalize_story_id(story_id)
    base = STARTER_STORY_CATALOG.get(story_id)
    if base is None:
        base = {
            "title": title_from_slug(story_id),
            "source_story": story_id,
            "summary": "Project-selected Kitsoki story. Confirm it loads and has project-ready fixtures before treating it as a default path.",
            "expansion": "Promote this story after adding project readiness evidence.",
        }
    return {"id": story_id, "status": "enabled", **base}


def starter_story_entries(story_ids: list[str] | None = None) -> list[dict]:
    ids = story_ids or DEFAULT_STARTER_STORY_IDS
    out: list[dict] = []
    seen: set[str] = set()
    for raw in ids:
        story_id = normalize_story_id(raw)
        if not story_id or story_id == "all" or story_id in seen:
            continue
        seen.add(story_id)
        out.append(starter_story_entry(story_id))
    if not out:
        return starter_story_entries(DEFAULT_STARTER_STORY_IDS)
    return out


def starter_story_entries_for_pack(pack_id: str | None) -> list[dict]:
    return starter_story_entries(story_pack_ids(pack_id))


def story_ids_from_text(value: str) -> list[str]:
    text = re.sub(r"\band\b", ",", value, flags=re.IGNORECASE)
    text = text.replace("+", ",")
    if any(sep in text for sep in [",", ";"]):
        raw = re.split(r"[,;]+", text)
    else:
        raw = text.split()
    return [normalize_story_id(item) for item in raw if normalize_story_id(item)]


def parse_story_pack(request: str, target: Path | None = None) -> str:
    text = (request or "").strip()
    if not text:
        return DEFAULT_STORY_PACK_ID
    try:
        parts = shlex.split(text)
    except ValueError:
        parts = text.split()
    index = 0
    while index < len(parts):
        token = parts[index]
        lower = token.lower()
        if lower in PACK_OPTION_KEYS and index + 1 < len(parts):
            return normalize_story_pack_id(parts[index + 1])
        if "=" in token:
            key, value = token.split("=", 1)
            key = key.lower().lstrip("-").replace("-", "_")
            if key in {item.replace("-", "_") for item in PACK_ASSIGNMENT_KEYS}:
                return normalize_story_pack_id(value)
        index += 1
    match = re.search(
        r"(?:story[-_ ]pack|starter[-_ ]pack|pack)\s*[:=]\s*([a-zA-Z0-9_.-]+)",
        text,
        flags=re.IGNORECASE,
    )
    if match:
        return normalize_story_pack_id(match.group(1))
    return DEFAULT_STORY_PACK_ID


def parse_story_focus_ids(request: str) -> list[str]:
    text = (request or "").strip()
    if not text:
        return []
    try:
        parts = shlex.split(text)
    except ValueError:
        parts = text.split()
    collected: list[str] = []
    index = 0
    while index < len(parts):
        token = parts[index]
        lower = token.lower()
        if lower in STORY_OPTION_KEYS:
            values: list[str] = []
            index += 1
            while index < len(parts) and not parts[index].startswith("--"):
                values.append(parts[index])
                index += 1
            collected.extend(story_ids_from_text(" ".join(values)))
            continue
        if "=" in token:
            key, value = token.split("=", 1)
            key = key.lower().lstrip("-").replace("-", "_")
            if key in {item.replace("-", "_") for item in STORY_ASSIGNMENT_KEYS}:
                collected.extend(story_ids_from_text(value))
        index += 1
    if not collected:
        match = re.search(
            r"(?:starter[-_ ]stories|story[-_ ]focus|stories|focus)\s*[:=]\s*(.+)$",
            text,
            flags=re.IGNORECASE,
        )
        if match:
            collected.extend(story_ids_from_text(match.group(1)))
    return collected


def parse_story_focus(request: str, pack_id: str | None = None) -> list[dict]:
    collected = parse_story_focus_ids(request)
    if collected:
        return starter_story_entries(collected)
    return starter_story_entries_for_pack(pack_id or parse_story_pack(request))


def is_story_focus_assignment(token: str) -> bool:
    if "=" not in token:
        return False
    key = token.split("=", 1)[0].lower().lstrip("-").replace("-", "_")
    return key in {item.replace("-", "_") for item in STORY_ASSIGNMENT_KEYS | PACK_ASSIGNMENT_KEYS}


def is_onboarding_option(token: str) -> bool:
    return token.lower() in STORY_OPTION_KEYS | PACK_OPTION_KEYS


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
                skip_next = False
                for item in rest:
                    lower = item.lower()
                    if skip_next:
                        skip_next = False
                        continue
                    if is_onboarding_option(lower):
                        skip_next = True
                        continue
                    if lower.startswith("--") or is_story_focus_assignment(item):
                        continue
                    target = item
                    break
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


def read_pyproject(path: Path) -> dict:
    pyproject = path / "pyproject.toml"
    if not pyproject.exists():
        return {}
    try:
        text = pyproject.read_text(encoding="utf-8")
    except OSError:
        return {}
    name = ""
    in_project = False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("[") and stripped.endswith("]"):
            in_project = stripped == "[project]"
            continue
        if in_project:
            match = re.match(r"name\s*=\s*[\"']([^\"']+)[\"']", stripped)
            if match:
                name = match.group(1)
                break
    return {"name": name, "text": text}


def command_from_scripts(package: dict, name: str) -> str:
    return command_from_scripts_with_manager(package, name, "npm")


def command_from_scripts_with_manager(package: dict, name: str, manager: str) -> str:
    scripts = package.get("scripts") if isinstance(package, dict) else {}
    if isinstance(scripts, dict) and scripts.get(name):
        if manager == "npm":
            return f"npm run {name}" if name != "test" else "npm test"
        if manager == "pnpm":
            return f"pnpm run {name}" if name != "test" else "pnpm test"
        if manager == "yarn":
            return f"yarn {name}"
        if manager == "bun":
            return f"bun run {name}"
        return f"{manager} run {name}"
    return ""


def node_package_manager(path: Path, package: dict) -> str:
    package_manager = package.get("packageManager") if isinstance(package.get("packageManager"), str) else ""
    if package_manager:
        name = package_manager.split("@", 1)[0].strip().lower()
        if name in {"npm", "pnpm", "yarn", "bun"}:
            return name
    if (path / "pnpm-lock.yaml").exists():
        return "pnpm"
    if (path / "yarn.lock").exists():
        return "yarn"
    if (path / "bun.lock").exists() or (path / "bun.lockb").exists():
        return "bun"
    return "npm"


def make_targets(path: Path) -> set[str]:
    makefile = path / "Makefile"
    if not makefile.exists():
        return set()
    targets: set[str] = set()
    try:
        text = makefile.read_text(encoding="utf-8")
    except OSError:
        return targets
    for line in text.splitlines():
        match = re.match(r"^([A-Za-z0-9_.-]+)\s*:", line)
        if match:
            targets.add(match.group(1))
    return targets


def parse_simple_project_toml(text: str) -> dict:
    """Parse the small subset of project.toml needed when tomllib is absent."""
    doc: dict = {}
    repos: list[dict] = []
    current: dict | None = doc
    current_repo: dict | None = None
    in_multiline = False
    for raw in text.splitlines():
        stripped = raw.strip()
        if in_multiline:
            if '"""' in stripped or "'''" in stripped:
                in_multiline = False
            continue
        if not stripped or stripped.startswith("#"):
            continue
        if stripped == "[[repos]]":
            current_repo = {}
            repos.append(current_repo)
            current = current_repo
            continue
        if stripped == "[repos.local_run]" and current_repo is not None:
            local_run: dict = {}
            current_repo["local_run"] = local_run
            current = local_run
            continue
        if stripped.startswith("["):
            current = None
            continue
        if "=" not in stripped or current is None:
            continue
        key, value = stripped.split("=", 1)
        key = key.strip()
        value = value.strip()
        if value.startswith('"""') or value.startswith("'''"):
            if not (len(value) > 3 and (value.endswith('"""') or value.endswith("'''"))):
                in_multiline = True
            current[key] = value.strip('"').strip("'")
            continue
        if value.startswith('"') and value.endswith('"'):
            current[key] = value[1:-1]
        elif value.startswith("'") and value.endswith("'"):
            current[key] = value[1:-1]
        else:
            current[key] = value
    if repos:
        doc["repos"] = repos
    return doc


def load_toml(path: Path) -> dict:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError:
        return {}
    if tomllib is None:
        return parse_simple_project_toml(text)
    try:
        return tomllib.loads(text)
    except tomllib.TOMLDecodeError:
        return parse_simple_project_toml(text)


def normalized_rel(path: str) -> str:
    return path.strip().strip("/").replace("\\", "/")


def project_metadata_for_target(path: Path) -> dict:
    """Find meta-repo style projects/<id>/project.toml metadata for a target."""
    for root in [path, *path.parents]:
        projects_dir = root / "projects"
        if not projects_dir.is_dir():
            continue
        try:
            target_rel = normalized_rel(path.relative_to(root).as_posix())
        except ValueError:
            continue
        for project_file in sorted(projects_dir.glob("*/project.toml")):
            doc = load_toml(project_file)
            repos = doc.get("repos") if isinstance(doc, dict) else None
            if not isinstance(repos, list):
                continue
            for repo in repos:
                if not isinstance(repo, dict):
                    continue
                submodule = repo.get("submodule")
                if isinstance(submodule, str) and normalized_rel(submodule) == target_rel:
                    return {
                        "root": str(root),
                        "path": str(project_file),
                        "project_id": project_file.parent.name,
                        "doc": doc,
                        "repo": repo,
                    }
    return {}


def text_field(value) -> str:
    return value if isinstance(value, str) else ""


def apply_project_metadata(
    metadata: dict,
    project_id: str,
    stack: str,
    dev_command: str,
    test_command: str,
    build_command: str,
) -> tuple[str, str, str, str, str, str]:
    if not metadata:
        return project_id, title_from_slug(project_id), stack, dev_command, test_command, build_command

    doc = metadata.get("doc") if isinstance(metadata.get("doc"), dict) else {}
    repo = metadata.get("repo") if isinstance(metadata.get("repo"), dict) else {}
    local_run = repo.get("local_run") if isinstance(repo.get("local_run"), dict) else {}

    meta_id = slug(text_field(metadata.get("project_id")) or project_id)
    title = text_field(doc.get("title")) or title_from_slug(meta_id)
    description = " ".join(
        item
        for item in [
            text_field(doc.get("description")),
            text_field(repo.get("description")),
        ]
        if item
    ).lower()

    if stack == "local project":
        if re.search(r"\bgo\b|golang", description):
            stack = "go project"
        elif re.search(r"\bnode\b|typescript|javascript|vite|react|vue", description):
            stack = "node project"
        elif re.search(r"\bpython\b", description):
            stack = "python project"
        elif re.search(r"\brust\b", description):
            stack = "rust project"

    meta_test = text_field(repo.get("test_command"))
    meta_build = text_field(local_run.get("build"))
    meta_dev = text_field(local_run.get("start"))
    return (
        meta_id,
        title,
        stack,
        meta_dev or dev_command,
        meta_test or test_command,
        meta_build or build_command,
    )


def git_output(path: Path, *args: str) -> str:
    try:
        proc = subprocess.run(
            ["git", "-C", str(path), *args],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
        )
    except (OSError, ValueError):
        return ""
    if proc.returncode != 0:
        return ""
    return proc.stdout.strip()


def github_repo_slug(remote: str) -> str:
    """Parse an `owner/repo` slug from a github.com remote URL.

    Handles the common transports (`https://github.com/owner/repo.git`,
    `git@github.com:owner/repo.git`, `ssh://git@github.com/owner/repo`).
    Non-GitHub remotes return "" — the ticket passthrough is GitHub-only
    (the `host.gh.ticket` adapter); other forges keep local-file tickets.
    """
    remote = (remote or "").strip()
    if not remote:
        return ""
    match = re.search(r"(?:^|[/@])github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?$", remote)
    if not match:
        return ""
    owner, repo = match.group(1), match.group(2)
    if not owner or not repo:
        return ""
    return f"{owner}/{repo}"


def _strip_yaml_comment(value: str) -> str:
    in_single = False
    in_double = False
    escaped = False
    for index, ch in enumerate(value):
        if escaped:
            escaped = False
            continue
        if ch == "\\" and in_double:
            escaped = True
            continue
        if ch == "'" and not in_double:
            in_single = not in_single
            continue
        if ch == '"' and not in_single:
            in_double = not in_double
            continue
        if ch == "#" and not in_single and not in_double and (index == 0 or value[index - 1].isspace()):
            return value[:index].rstrip()
    return value.rstrip()


def _split_inline_list(value: str) -> list[str]:
    body = value.strip()[1:-1].strip()
    if not body:
        return []
    out: list[str] = []
    current = ""
    in_single = False
    in_double = False
    escaped = False
    for ch in body:
        if escaped:
            current += ch
            escaped = False
            continue
        if ch == "\\" and in_double:
            current += ch
            escaped = True
            continue
        if ch == "'" and not in_double:
            in_single = not in_single
            current += ch
            continue
        if ch == '"' and not in_single:
            in_double = not in_double
            current += ch
            continue
        if ch == "," and not in_single and not in_double:
            out.append(str(_parse_yaml_scalar(current)))
            current = ""
            continue
        current += ch
    if current.strip():
        out.append(str(_parse_yaml_scalar(current)))
    return out


def _parse_yaml_scalar(raw: str):
    value = _strip_yaml_comment(raw).strip()
    if value in {"", "null", "Null", "NULL", "~"}:
        return ""
    if value.startswith("[") and value.endswith("]"):
        return _split_inline_list(value)
    if value in {"true", "True", "TRUE"}:
        return True
    if value in {"false", "False", "FALSE"}:
        return False
    if (value.startswith('"') and value.endswith('"')) or (value.startswith("'") and value.endswith("'")):
        if value.startswith('"'):
            try:
                return json.loads(value)
            except json.JSONDecodeError:
                return value[1:-1]
        return value[1:-1].replace("''", "'")
    return value


def _profile_root(profile_path: Path) -> Path:
    # .kitsoki/project-profile.yaml -> project root. If a caller supplies a
    # differently named file, fall back to its parent.
    return profile_path.parent.parent if profile_path.parent.name == ".kitsoki" else profile_path.parent


def find_parent_project_profile(path: Path) -> Path | None:
    current = path.resolve()
    for parent in current.parents:
        profile = parent / ".kitsoki" / "project-profile.yaml"
        if profile.exists():
            return profile
    return None


def read_project_profile_ticket_subset(profile_path: Path) -> dict:
    try:
        lines = profile_path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return {}
    stack: list[tuple[int, str]] = []
    tracker: dict = {}
    ticket_binding = ""
    docs_ticket_repo = ""
    for line in lines:
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        stripped = line.strip()
        while stack and indent <= stack[-1][0]:
            stack.pop()
        if stripped.startswith("- "):
            path_tuple = tuple(item[1] for item in stack)
            if len(path_tuple) == 2 and path_tuple[0] == "tracker":
                tracker.setdefault(path_tuple[1], []).append(_parse_yaml_scalar(stripped[2:]))
            continue
        if ":" not in stripped:
            continue
        key, raw = stripped.split(":", 1)
        key = key.strip()
        path_tuple = tuple([item[1] for item in stack] + [key])
        raw = raw.strip()
        if raw == "":
            stack.append((indent, key))
            continue
        value = _parse_yaml_scalar(raw)
        if path_tuple == ("kitsoki", "instance", "bindings", "ticket"):
            ticket_binding = str(value)
        elif len(path_tuple) == 2 and path_tuple[0] == "tracker":
            tracker[path_tuple[1]] = value
        elif path_tuple == ("dev_story_profile", "docs", "ticket_repo"):
            docs_ticket_repo = str(value)
    return {
        "profile_path": profile_path,
        "root": _profile_root(profile_path),
        "tracker": tracker,
        "ticket_binding": ticket_binding,
        "ticket_repo": docs_ticket_repo,
    }


def is_script_binding(value: str) -> bool:
    return (value or "").strip().endswith(".star")


def rebase_script_binding(value: str, source_root: Path, target_root: Path) -> str:
    binding = (value or "").strip()
    if not is_script_binding(binding):
        return binding
    path = Path(binding).expanduser()
    if not path.is_absolute():
        path = source_root / path
    try:
        return os.path.relpath(path.resolve(), target_root.resolve())
    except OSError:
        return os.path.relpath(path, target_root)


def inherited_ticket_provider(path: Path) -> dict:
    child_root = path.resolve()
    parent_profile = find_parent_project_profile(child_root)
    if parent_profile is None:
        return {}
    subset = read_project_profile_ticket_subset(parent_profile)
    tracker = subset.get("tracker") if isinstance(subset.get("tracker"), dict) else {}
    provider = str(tracker.get("provider") or "").strip()
    ticket_binding = str(subset.get("ticket_binding") or "").strip()
    if not provider or provider in {"none", "github"} or not ticket_binding:
        return {}
    root = subset["root"]
    return {
        "provider": provider,
        "repo": str(tracker.get("repo") or subset.get("ticket_repo") or ""),
        "profile_path": str(parent_profile),
        "profile_root": str(root),
        "profile_root_relpath": os.path.relpath(root, child_root),
        "profile_relpath": os.path.relpath(parent_profile, child_root),
        "ticket_binding": rebase_script_binding(ticket_binding, root, child_root),
        "tracker": dict(tracker),
    }


def git_info(path: Path) -> dict:
    inside = git_output(path, "rev-parse", "--is-inside-work-tree")
    if inside != "true":
        return {"vcs": "none", "default_branch": "", "remote": ""}
    remote = git_output(path, "config", "--get", "remote.origin.url")
    default_branch = git_output(path, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
    if default_branch.startswith("origin/"):
        default_branch = default_branch.split("/", 1)[1]
    if not default_branch:
        default_branch = git_output(path, "symbolic-ref", "--quiet", "--short", "HEAD")
    if not default_branch:
        for candidate in ("main", "master"):
            if git_output(path, "show-ref", "--verify", f"refs/heads/{candidate}"):
                default_branch = candidate
                break
    return {"vcs": "git", "default_branch": default_branch or "main", "remote": remote}


def invalid_discovery(
    path: Path,
    error: str,
    starter_stories: list[dict] | None = None,
    pack_id: str | None = None,
) -> dict:
    transcripts = transcript_evidence(path)
    pack = story_pack(pack_id)
    return {
        "target_path": str(path),
        "project_id": "",
        "project_title": "",
        "stack": "",
        "dev_command": "",
        "test_command": "",
        "build_command": "",
        "node_package_manager": "",
        "repo_vcs": "none",
        "repo_default_branch": "",
        "repo_remote": "",
        "conventions": "local defaults",
        "tracker": "none",
        "ticket_repo": "",
        "starter_stories": starter_stories or starter_story_entries(),
        "story_pack": pack["id"],
        "story_pack_title": pack["title"],
        "story_pack_summary": pack["summary"],
        "story_packs": story_pack_catalog(),
        "ticket_binding": "",
        "ticket_provider_inherited": False,
        "ticket_provider_parent_profile": "",
        "ticket_provider_parent_root": "",
        "ticket_provider_metadata": {},
        "transcript_slug": transcripts["slug"],
        "transcript_count": transcripts["count"],
        "transcript_sources": transcripts["sources"],
        "mining_recommendation": transcripts["mining"],
        "error": error,
    }


def discover(path: Path, starter_stories: list[dict] | None = None, pack_id: str | None = None) -> dict:
    pack = story_pack(pack_id)
    starter_stories = starter_stories or starter_story_entries_for_pack(pack["id"])
    if not path.exists():
        return invalid_discovery(path, "target path does not exist", starter_stories, pack["id"])
    if not path.is_dir():
        return invalid_discovery(path, "target path is not a directory", starter_stories, pack["id"])

    package = read_package(path)
    package_name = package.get("name") if isinstance(package.get("name"), str) else ""
    pyproject = read_pyproject(path)
    pyproject_name = pyproject.get("name") if isinstance(pyproject.get("name"), str) else ""
    project_id = slug(package_name or pyproject_name or path.name)
    node_manager = node_package_manager(path, package) if package else ""
    targets = make_targets(path)
    project_metadata = project_metadata_for_target(path)
    deps = {}
    for key in ("dependencies", "devDependencies"):
        value = package.get(key)
        if isinstance(value, dict):
            deps.update(value)
    py_text = (pyproject.get("text") or "").lower()
    requirements = path / "requirements.txt"
    if requirements.exists():
        try:
            py_text += "\n" + requirements.read_text(encoding="utf-8").lower()
        except OSError:
            pass
    python_project = any(
        (path / marker).exists()
        for marker in ("pyproject.toml", "requirements.txt", "setup.py", "setup.cfg", "Pipfile", "poetry.lock", "uv.lock")
    )

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
    elif python_project:
        stack_bits = ["python"]
        for name in ("django", "fastapi", "flask"):
            if name in py_text:
                stack_bits.append(name)
        stack = "/".join(stack_bits) + " project"
    else:
        stack = "local project"

    dev_command = command_from_scripts_with_manager(package, "dev", node_manager or "npm")
    test_command = command_from_scripts_with_manager(package, "test", node_manager or "npm")
    build_command = command_from_scripts_with_manager(package, "build", node_manager or "npm")
    if project_id == "slidey" and (path / "src" / "index.js").exists():
        examples = list((path / "examples").glob("*.slidey.json")) if (path / "examples").exists() else []
        example = "examples/hello.slidey.json" if (path / "examples" / "hello.slidey.json").exists() else ""
        if not example and examples:
            example = str(examples[0].relative_to(path))
        if example:
            dev_command = f"node src/index.js {example} --port 5000 --no-open"
    elif (path / "Cargo.toml").exists():
        build_command = "make build" if "build" in targets else "cargo build"
        test_command = "make test" if "test" in targets else "cargo test"
        if "dev" in targets:
            dev_command = "make dev"
        elif "example" in targets:
            dev_command = "make example"
    elif (path / "go.mod").exists():
        # Go has canonical build/test commands, so the deterministic profile
        # should never leave them "(not yet inferred)" — a Makefile target
        # still wins where one exists. (Go has no universal "dev server", so
        # dev_command stays empty unless a make dev/run target is present.)
        build_command = "make build" if "build" in targets else "go build ./..."
        test_command = "make test" if "test" in targets else "go test ./..."
        if "dev" in targets:
            dev_command = "make dev"
        elif "run" in targets:
            dev_command = "make run"
    elif python_project:
        build_command = "make build" if "build" in targets else ""
        if "test" in targets:
            test_command = "make test"
        elif (path / "tox.ini").exists():
            test_command = "tox"
        elif (path / "pytest.ini").exists() or (path / "tests").exists() or "pytest" in py_text:
            test_command = "python -m pytest"
        if "dev" in targets:
            dev_command = "make dev"
        elif "run" in targets:
            dev_command = "make run"
        elif "fastapi" in py_text or "uvicorn" in py_text:
            dev_command = "uvicorn app:app --reload"
        elif "flask" in py_text:
            dev_command = "flask run"

    project_id, project_title, stack, dev_command, test_command, build_command = apply_project_metadata(
        project_metadata,
        project_id,
        stack,
        dev_command,
        test_command,
        build_command,
    )

    transcripts = transcript_evidence(path)
    repo = git_info(path)
    # External ticket-repo passthrough: a github.com origin is the repo's
    # ticket source of record (GitHub Issues), so discovery classifies the
    # tracker and carries the `owner/repo` slug. Apply binds
    # `iface.ticket → host.gh.ticket` pinned to that slug; the operator (or
    # the profile-draft step) can override tracker back to "none" to keep
    # local-file tickets.
    ticket_repo = github_repo_slug(repo["remote"])
    inherited_ticket = inherited_ticket_provider(path) if not ticket_repo else {}
    tracker = "github" if ticket_repo else str(inherited_ticket.get("provider") or "none")
    tracker_metadata = inherited_ticket.get("tracker") if isinstance(inherited_ticket.get("tracker"), dict) else {}
    return {
        "target_path": str(path),
        "project_id": project_id,
        "project_title": project_title,
        "stack": stack,
        "dev_command": dev_command,
        "test_command": test_command,
        "build_command": build_command,
        "node_package_manager": node_manager,
        "repo_vcs": repo["vcs"],
        "repo_default_branch": repo["default_branch"],
        "repo_remote": repo["remote"],
        "conventions": "project" if project_metadata else ("hybrid" if project_id == "slidey" or (path / "AGENTS.md").exists() or (path / "CLAUDE.md").exists() else "local defaults"),
        "tracker": tracker,
        "ticket_repo": ticket_repo,
        "starter_stories": starter_stories,
        "story_pack": pack["id"],
        "story_pack_title": pack["title"],
        "story_pack_summary": pack["summary"],
        "story_packs": story_pack_catalog(),
        "ticket_binding": inherited_ticket.get("ticket_binding", ""),
        "ticket_provider_inherited": bool(inherited_ticket),
        "ticket_provider_parent_profile": inherited_ticket.get("profile_relpath", ""),
        "ticket_provider_parent_root": inherited_ticket.get("profile_root_relpath", ""),
        "ticket_provider_metadata": tracker_metadata,
        "transcript_slug": transcripts["slug"],
        "transcript_count": transcripts["count"],
        "transcript_sources": transcripts["sources"],
        "mining_recommendation": transcripts["mining"],
    }


def main() -> int:
    request = sys.argv[1] if len(sys.argv) > 1 else ""
    workdir = sys.argv[2] if len(sys.argv) > 2 else ""
    repo_root = sys.argv[3] if len(sys.argv) > 3 else ""
    target = parse_target(request, workdir, repo_root)
    explicit_ids = parse_story_focus_ids(request)
    pack_id = "custom" if explicit_ids else parse_story_pack(request, target)
    starter_stories = starter_story_entries(explicit_ids) if explicit_ids else starter_story_entries_for_pack(pack_id)
    print(json.dumps(discover(target, starter_stories, pack_id), sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
