#!/usr/bin/env python3
"""Apply local project onboarding files for dev-story."""

from __future__ import annotations

import json
import os
import re
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
from init_discover import (  # noqa: E402
    github_repo_slug,
    inherited_ticket_provider,
    is_script_binding,
    normalize_story_pack_id,
    rebase_script_binding,
    starter_story_entries,
    story_pack,
)
from init_transcripts import mining_recommendation  # noqa: E402


def q(value: str) -> str:
    return json.dumps(value or "")


def slug(value: str) -> str:
    value = value.strip().lower()
    value = re.sub(r"[^a-z0-9]+", "-", value).strip("-")
    return value or "project"


def write_text(path: Path, content: str, writes: list[str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    old = path.read_text(encoding="utf-8") if path.exists() else None
    if old != content:
        path.write_text(content, encoding="utf-8")
        writes.append(str(path))


def yaml_scalar(value) -> str:
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value)
    return json.dumps(value)


def yaml_dump(value, indent: int = 0) -> str:
    pad = " " * indent
    if isinstance(value, dict):
        lines = []
        for key in sorted(value):
            item = value[key]
            if isinstance(item, (dict, list)):
                lines.append(f"{pad}{key}:")
                lines.append(yaml_dump(item, indent + 2))
            else:
                lines.append(f"{pad}{key}: {yaml_scalar(item)}")
        return "\n".join(lines)
    if isinstance(value, list):
        if not value:
            return pad + "[]"
        lines = []
        for item in value:
            if isinstance(item, (dict, list)):
                lines.append(f"{pad}-")
                lines.append(yaml_dump(item, indent + 2))
            else:
                lines.append(f"{pad}- {yaml_scalar(item)}")
        return "\n".join(lines)
    return pad + yaml_scalar(value)


def binding_yaml(value: str) -> str:
    value = value or ""
    if re.match(r"^[A-Za-z0-9_./-]+$", value):
        return value
    return q(value)


def ticket_binding_for_profile(data: dict) -> str:
    if data.get("ticket_binding"):
        return str(data["ticket_binding"])
    return ticket_binding(data)


def ticket_binding_for_app(data: dict) -> str:
    binding = ticket_binding_for_profile(data)
    if not is_script_binding(binding):
        return binding
    root = Path(data["target_path"])
    app_dir = root / ".kitsoki" / "stories" / f"{data['project_id']}-dev"
    return rebase_script_binding(binding, root, app_dir)


def tracker_profile(data: dict) -> dict:
    meta = data.get("tracker_metadata") if isinstance(data.get("tracker_metadata"), dict) else {}
    tracker = dict(meta)
    tracker["provider"] = data.get("tracker", "none")
    tracker["repo"] = data.get("ticket_repo") or str(tracker.get("repo") or "")
    if data.get("ticket_provider_inherited"):
        tracker["inherited_from"] = data.get("ticket_provider_parent_profile", "")
        tracker["inherited_root"] = data.get("ticket_provider_parent_root", "")
        tracker["binding"] = ticket_binding_for_profile(data)
        if tracker.get("setup_command") and data.get("ticket_provider_parent_root"):
            tracker.setdefault("setup_cwd", data["ticket_provider_parent_root"])
        if tracker.get("readiness_command") and data.get("ticket_provider_parent_root"):
            tracker.setdefault("readiness_cwd", data["ticket_provider_parent_root"])
    return tracker


def tracker_profile_yaml(data: dict, indent: int = 0) -> str:
    return yaml_dump(tracker_profile(data), indent)


def profile_yaml_from_draft(profile: dict) -> str:
    return yaml_dump(profile).rstrip() + "\n"


def normalize_starter_stories(value) -> list[dict]:
    if not isinstance(value, list):
        return starter_story_entries()
    ids: list[str] = []
    for item in value:
        if isinstance(item, dict):
            raw = item.get("id") or item.get("source_story") or item.get("story")
        else:
            raw = item
        if raw is None:
            continue
        ids.append(str(raw))
    return starter_story_entries(ids)


def starter_story_ids(data: dict) -> list[str]:
    stories = normalize_starter_stories(data.get("starter_stories"))
    return [str(item["id"]) for item in stories]


def starter_story_focus(data: dict) -> list[dict]:
    return normalize_starter_stories(data.get("starter_stories"))


def selected_story_pack(data: dict) -> dict:
    pack_id = normalize_story_pack_id(str(data.get("story_pack") or "core-engineering"))
    known = story_pack(pack_id)
    if pack_id == "custom" or known["id"] == pack_id:
        return known
    story_ids = starter_story_ids(data)
    for candidate in ["core-engineering", "core-setup", "planning-delivery", "review-quality", "full-dev-story"]:
        if story_ids == [str(item) for item in story_pack(candidate).get("stories", [])]:
            return story_pack(candidate)
    return story_pack("custom")


def expansion_policy(data: dict) -> str:
    ids = ", ".join(starter_story_ids(data))
    pack = selected_story_pack(data)
    return (
        f"Start new teams on the `{pack['id']}` story pack: {ids}. Treat it as an adoption scope, "
        "not a runtime fence: dev-story remains available. Use "
        "`kitsoki project-profile story-packs list` to see packs, "
        "`kitsoki project-profile story-packs set <pack>` to switch the starter scope, or "
        "`kitsoki project-profile story-packs add <pack>` to expand it after adding readiness checks/flows."
    )


def ensure_draft_profile_defaults(profile: dict, data: dict) -> None:
    tracker = profile.setdefault("tracker", {})
    if not isinstance(tracker, dict):
        tracker = {}
        profile["tracker"] = tracker
    for key, value in tracker_profile(data).items():
        tracker.setdefault(key, value)

    kitsoki = profile.setdefault("kitsoki", {})
    if not isinstance(kitsoki, dict):
        kitsoki = {}
        profile["kitsoki"] = kitsoki
    kitsoki.setdefault("story_pack", selected_story_pack(data)["id"])
    kitsoki.setdefault("enabled_stories", starter_story_ids(data))
    instance = kitsoki.setdefault("instance", {})
    if not isinstance(instance, dict):
        instance = {}
        kitsoki["instance"] = instance
    bindings = instance.setdefault("bindings", {})
    if not isinstance(bindings, dict):
        bindings = {}
        instance["bindings"] = bindings
    default_bindings = {
        "ticket": ticket_binding_for_profile(data),
        "vcs": "host.git",
        "ci": "host.local",
        "workspace": "host.git_worktree",
        "transport": "host.append_to_file",
    }
    for key, value in default_bindings.items():
        if key == "ticket" and (data.get("ticket_repo") or data.get("ticket_provider_inherited")):
            bindings[key] = value
        else:
            bindings.setdefault(key, value)

    dev_story_profile = profile.setdefault("dev_story_profile", {})
    if not isinstance(dev_story_profile, dict):
        dev_story_profile = {}
        profile["dev_story_profile"] = dev_story_profile
    docs = dev_story_profile.setdefault("docs", {})
    if not isinstance(docs, dict):
        docs = {}
        dev_story_profile["docs"] = docs
    for key, value in dev_story_docs_profile(data).items():
        docs.setdefault(key, value)

    bugfix = dev_story_profile.setdefault("bugfix", {})
    if not isinstance(bugfix, dict):
        bugfix = {}
        dev_story_profile["bugfix"] = bugfix
    if data.get("build_command"):
        bugfix.setdefault("build_cmd", data["build_command"])
    if data.get("test_command"):
        bugfix.setdefault("test_cmd", data["test_command"])

    onboarding = profile.setdefault("onboarding", {})
    if not isinstance(onboarding, dict):
        onboarding = {}
        profile["onboarding"] = onboarding
    defaults = onboarding_profile(data)
    for key, value in defaults.items():
        onboarding.setdefault(key, value)


def validate_profile_yaml(content: str, root: Path) -> dict:
    kitsoki_bin = os.environ.get("KITSOKI_BIN", "kitsoki")
    with tempfile.TemporaryDirectory(prefix="kitsoki-profile-") as tmp:
        path = Path(tmp) / "project-profile.yaml"
        path.write_text(content, encoding="utf-8")
        proc = subprocess.run(
            [kitsoki_bin, "project-profile", "validate", "--json", "--repo-root", str(root), str(path)],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    report = {}
    if proc.stdout.strip():
        try:
            report = json.loads(proc.stdout)
        except json.JSONDecodeError:
            report = {"ok": False, "schema": ["validator returned non-json stdout"]}
    ok = bool(report.get("ok")) and proc.returncode == 0
    return {
        "ok": ok,
        "schema": report.get("schema", []),
        "semantic": report.get("semantic", []),
        "warnings": report.get("warnings", []),
        "validator_stdout": proc.stdout,
        "validator_stderr": proc.stderr,
        "validator_exit_code": proc.returncode,
    }


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


def append_gitignore(path: Path, writes: list[str]) -> None:
    additions = [
        ".kitsoki.local.yaml",
        ".kitsoki/sessions/",
        ".artifacts/",
        ".context/",
        ".worktrees/",
    ]
    current = path.read_text(encoding="utf-8") if path.exists() else ""
    existing = {line.strip() for line in current.splitlines()}
    existing_normalized = {line.rstrip("/") for line in existing}
    missing = [entry for entry in additions if entry.rstrip("/") not in existing_normalized]
    if not missing:
        return
    block = "\n# Kitsoki local runtime\n" + "\n".join(missing) + "\n"
    path.write_text(current.rstrip() + block, encoding="utf-8")
    writes.append(str(path))


def dev_story_docs_profile(data: dict) -> dict:
    if data["project_id"] == "slidey":
        defaults = {
            "publish_durable_path": "docs/prd",
            "prd_doc_filename": "",
            "design_template_dir": "docs/proposals/templates",
            "design_durable_path": "docs/proposals",
            "design_doc_filename": "",
            "design_ticket_dir": "",
            "ticket_repo": "",
        }
    else:
        defaults = {
            "publish_durable_path": ".context/prd",
            "prd_doc_filename": "",
            "design_template_dir": "",
            "design_durable_path": ".context/designs",
            "design_doc_filename": "",
            "design_ticket_dir": "",
            # Bug intake and design-ticket creation are separate knobs.
            # Onboarding wires GitHub issue search through ticket_github_repo in
            # the instance wrapper; feature/proposal publishing only files a
            # GitHub issue when a project explicitly opts into this docs field.
            "ticket_repo": "",
        }
    override = data.get("dev_story_docs_profile")
    if isinstance(override, dict):
        for key in defaults:
            if key in override and override[key] is not None:
                defaults[key] = str(override[key])
    return defaults


DEV_STORY_INSTANCE_HOSTS = [
    "host.local_files.ticket",
    "host.local_github.ticket",
    "host.gh.ticket",
    "host.gh.ticket.get",
    "host.gh.ticket.comment",
    "host.gh.ticket.transition",
    "host.git",
    "host.local",
    "host.git_worktree",
    "host.append_to_file",
    "host.inbox.add",
    "host.agent.ask",
    "host.agent.decide",
    "host.agent.task",
    "host.agent.codeact",
    "host.agent.search",
    "host.agent.converse",
    "host.chat.resolve",
    "host.chat.transcript",
    "host.artifacts_dir",
    "host.slidey.render",
    "host.fs.writable_dir",
    "host.ide.get_diagnostics",
    "host.ide.open_file",
    "host.ide.open_diff",
    "host.diff.open",
    "host.run",
    "host.starlark.run",
]


def dev_story_instance_hosts_yaml(indent: int = 2) -> str:
    pad = " " * indent
    return "\n".join(f"{pad}- {host}" for host in DEV_STORY_INSTANCE_HOSTS)


def ticket_binding(data: dict) -> str:
    if data.get("ticket_repo"):
        return "host.local_github.ticket"
    return "host.local_files.ticket"


def ticket_repo_default(data: dict) -> str:
    # Leave the active ticket repo empty at rest. ticket_search fills it only
    # after a GitHub row is picked, so local/pasted bugs do not accidentally
    # close or comment on a remote issue.
    return ""


def ticket_github_repo_default(data: dict) -> str:
    # The configured GitHub source shown beside local artifact tickets.
    return data.get("ticket_repo") or ""


def ticket_provider_summary(data: dict) -> str:
    if data.get("ticket_provider_inherited"):
        return f"inherited `{data.get('tracker')}` provider from `{data.get('ticket_provider_parent_profile')}`"
    repo = data.get("ticket_repo") or ""
    if repo:
        return f"GitHub issues from `{repo}` plus local markdown / pasted reports"
    return "local markdown / pasted reports"


def app_yaml(data: dict) -> str:
    project_id = data["project_id"]
    title = data["project_title"]
    docs = dev_story_docs_profile(data)
    # Ticket source passthrough: GitHub binds to the built-in gh adapter pinned
    # on world.ticket_repo. Parent meta-repo providers bind through their
    # inherited handler/script while keeping ticket_repo empty so GitHub-only
    # rooms do not treat them as GitHub Issues.
    binding = ticket_binding_for_app(data)
    return f"""app:
  id: {project_id}-dev
  version: 0.1.0
  title: {q(project_id + "-dev - work on " + title + " with Kitsoki")}
  author: "Kitsoki"
  license: "CC0"

routing:
  embedding:
    model: "nomic-embed-text-v1.5"
  free_form_fallback:
    state: core.landing
    intent: core__landing_capture

hosts:
{dev_story_instance_hosts_yaml()}

imports:
  core:
    source: "@kitsoki/dev-story"
    entry: landing
    hosts: declared
    host_bindings:
      ticket:    {binding_yaml(binding)}
      vcs:       host.git
      ci:        host.local
      workspace: host.git_worktree
      transport: host.append_to_file
    world_in:
      workdir:                    "{{{{ world.workdir }}}}"
      repo_root:                  "{{{{ world.repo_root }}}}"
      judge_mode:                 "{{{{ world.judge_mode }}}}"
      judge_confidence_threshold: "{{{{ world.judge_confidence_threshold }}}}"
      publish_durable_path:       "{{{{ world.publish_durable_path }}}}"
      prd_doc_filename:           "{{{{ world.prd_doc_filename }}}}"
      prd_mockup_path:            "{{{{ world.prd_mockup_path }}}}"
      design_template_dir:        "{{{{ world.design_template_dir }}}}"
      design_durable_path:        "{{{{ world.design_durable_path }}}}"
      design_doc_filename:        "{{{{ world.design_doc_filename }}}}"
      design_ticket_dir:          "{{{{ world.design_ticket_dir }}}}"
      ticket_repo:                "{{{{ world.ticket_repo }}}}"
      ticket_github_repo:         "{{{{ world.ticket_github_repo }}}}"
      bugfix_destination:         "{{{{ world.bugfix_destination }}}}"
      # Project toolchain commands. Empty means dev-story/bugfix keeps its
      # default gates; non-Go projects should set these from their profile.
      build_cmd:                  "{{{{ world.build_cmd }}}}"
      test_cmd:                   "{{{{ world.test_cmd }}}}"
      quick_test_cmd:             "{{{{ world.quick_test_cmd }}}}"
      quick_test_timeout_seconds: "{{{{ world.quick_test_timeout_seconds }}}}"
      full_test_timeout_seconds:  "{{{{ world.full_test_timeout_seconds }}}}"

world:
  workdir:                    {{ type: string, default: "." }}
  repo_root:                  {{ type: string, default: "." }}
  # ticket_repo is the active selected GitHub issue repo. It stays empty at rest
  # so local artifact and pasted-report bugfixes close out locally. A picked
  # GitHub row fills it before entering child stories.
  ticket_repo:                {{ type: string, default: {q(ticket_repo_default(data))} }}
  # ticket_github_repo is the configured remote source displayed beside local
  # artifacts when the ticket binding is host.local_github.ticket.
  ticket_github_repo:         {{ type: string, default: {q(ticket_github_repo_default(data))} }}
  bugfix_destination:         {{ type: string, default: "open-pr" }}
  judge_mode:                 {{ type: string, default: "human" }}
  judge_confidence_threshold: {{ type: float, default: 0.8 }}

  publish_durable_path:       {{ type: string, default: {q(docs["publish_durable_path"])} }}
  prd_doc_filename:           {{ type: string, default: {q(docs["prd_doc_filename"])} }}
  prd_mockup_path:            {{ type: string, default: {q(data.get("prd_mockup_path", ""))} }}
  design_template_dir:        {{ type: string, default: {q(docs["design_template_dir"])} }}
  design_durable_path:        {{ type: string, default: {q(docs["design_durable_path"])} }}
  design_doc_filename:        {{ type: string, default: {q(docs["design_doc_filename"])} }}
  design_ticket_dir:          {{ type: string, default: {q(docs["design_ticket_dir"])} }}
  build_cmd:                  {{ type: string, default: {q(data.get("build_command", ""))} }}
  test_cmd:                   {{ type: string, default: {q(data.get("test_command", ""))} }}
  quick_test_cmd:             {{ type: string, default: "" }}
  quick_test_timeout_seconds: {{ type: int,    default: 180 }}
  full_test_timeout_seconds:  {{ type: int,    default: 1200 }}

root: core
"""


def profile_yaml(data: dict) -> str:
    if data["project_id"] == "slidey":
        return slidey_profile_yaml(data)
    return generic_profile_yaml(data)


def stack_kind(data: dict) -> str:
    stack = (data.get("stack") or "").lower()
    if "rust" in stack:
        return "rust"
    if "go project" in stack:
        return "go"
    if "node" in stack:
        return "node"
    if "python" in stack:
        return "python"
    return "generic"


def enrich_project_shape(data: dict, root: Path) -> None:
    repo = git_info(root)
    data["repo_vcs"] = repo["vcs"]
    data["repo_default_branch"] = repo["default_branch"]
    data["repo_remote"] = repo["remote"]
    # External ticket-repo passthrough: when the selected tracker is GitHub, the
    # generated instance pins `iface.ticket -> host.gh.ticket` on the selected
    # slug. Discovery can prefill that slug from origin, but an explicit
    # onboarding choice wins. Any other tracker, or an unparseable remote,
    # can inherit a parent meta-repo provider or degrade honestly to
    # local-file/copy-paste tickets.
    selected_ticket_repo = str(data.get("ticket_repo") or github_repo_slug(repo["remote"]) or "")
    data["ticket_repo"] = selected_ticket_repo if data.get("tracker") == "github" else ""
    data["ticket_binding"] = ""
    data["ticket_provider_inherited"] = False
    data["ticket_provider_parent_profile"] = ""
    data["ticket_provider_parent_root"] = ""
    data["tracker_metadata"] = {}
    if not data["ticket_repo"]:
        inherited = inherited_ticket_provider(root)
        if inherited:
            data["tracker"] = inherited["provider"]
            data["ticket_binding"] = inherited["ticket_binding"]
            data["ticket_provider_inherited"] = True
            data["ticket_provider_parent_profile"] = inherited.get("profile_relpath", "")
            data["ticket_provider_parent_root"] = inherited.get("profile_root_relpath", "")
            data["tracker_metadata"] = inherited.get("tracker") if isinstance(inherited.get("tracker"), dict) else {}
    data["has_makefile"] = (root / "Makefile").exists()
    cargo = root / "Cargo.toml"
    data["has_cargo"] = cargo.exists()
    package_json = root / "package.json"
    data["has_package_json"] = package_json.exists()
    data["node_package_manager"] = "npm"
    if package_json.exists():
        try:
            package = json.loads(package_json.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            package = {}
        package_manager = package.get("packageManager") if isinstance(package.get("packageManager"), str) else ""
        if package_manager:
            name = package_manager.split("@", 1)[0].strip().lower()
            if name in {"npm", "pnpm", "yarn", "bun"}:
                data["node_package_manager"] = name
    if (root / "pnpm-lock.yaml").exists():
        data["node_package_manager"] = "pnpm"
    elif (root / "yarn.lock").exists():
        data["node_package_manager"] = "yarn"
    elif (root / "bun.lock").exists() or (root / "bun.lockb").exists():
        data["node_package_manager"] = "bun"
    data["has_pyproject"] = (root / "pyproject.toml").exists()
    data["has_requirements"] = (root / "requirements.txt").exists()
    data["has_uv_lock"] = (root / "uv.lock").exists()
    data["has_poetry_lock"] = (root / "poetry.lock").exists()
    data["rules_files"] = [
        name
        for name in ("AGENTS.md", "CLAUDE.md", ".cursorrules", ".windsurfrules")
        if (root / name).exists()
    ]
    data["is_monorepo"] = False
    if cargo.exists():
        try:
            text = cargo.read_text(encoding="utf-8")
            data["is_monorepo"] = "[workspace]" in text or "\n[workspace." in text
        except OSError:
            data["is_monorepo"] = False


def package_managers(data: dict, kind: str) -> str:
    managers: list[str] = []
    if kind == "rust" or data.get("has_cargo"):
        managers.append("cargo")
    elif kind == "go":
        managers.append("go")
    elif kind == "node":
        managers.append(data.get("node_package_manager") or "npm")
    elif kind == "python":
        if data.get("has_uv_lock"):
            managers.append("uv")
        if data.get("has_poetry_lock"):
            managers.append("poetry")
        if data.get("has_pyproject"):
            managers.append("pyproject")
        if data.get("has_requirements") or not managers:
            managers.append("pip")
    if data.get("has_makefile"):
        managers.append("make")
    return "[" + ", ".join(managers) + "]" if managers else "[]"


def convention_source(value: str) -> str:
    normalized = (value or "").strip().lower()
    if normalized in {"kitsoki", "project", "hybrid"}:
        return normalized
    return "project"


def mining_seed_enabled(data: dict) -> bool:
    mining = data.get("mining_recommendation")
    if not isinstance(mining, dict):
        return False
    return int(mining.get("transcript_count") or 0) > 0 or mining.get("status") == "transcripts-found"


def mining_seed_path() -> str:
    return ".context/kitsoki-session-mining-seed.md"


def mining_transcript_dirs(data: dict) -> list[str]:
    mining = data.get("mining_recommendation") if isinstance(data.get("mining_recommendation"), dict) else {}
    sources = mining.get("sources") if isinstance(mining, dict) else []
    if not isinstance(sources, list):
        return []
    out: list[str] = []
    seen: set[str] = set()
    for source in sources:
        if not isinstance(source, dict):
            continue
        path = str(source.get("dir") or "").strip()
        if not path or path in seen:
            continue
        seen.add(path)
        out.append(path)
    return out


def mining_config(data: dict) -> dict:
    mining = data.get("mining_recommendation") if isinstance(data.get("mining_recommendation"), dict) else {}
    first_pass = int(mining.get("first_pass_sample") or 0) if isinstance(mining, dict) else 0
    return {
        "enabled": False,
        "cadence": "30s",
        "first_pass_sample": first_pass or 12,
        "transcript_dirs": mining_transcript_dirs(data),
    }


def mining_config_yaml(data: dict) -> str:
    if not mining_seed_enabled(data):
        return ""
    return "\nmining:\n" + yaml_dump(mining_config(data), 2) + "\n"


def mining_setup_writes(data: dict) -> list[dict]:
    if not mining_seed_enabled(data):
        return []
    return [{
        "path": mining_seed_path(),
        "action": "create",
        "summary": "Operator review note for seeding project customization from associated Claude/Codex transcripts.",
    }]


def mining_profile_yaml(data: dict) -> str:
    mining = data.get("mining_recommendation") or mining_recommendation(Path(data["target_path"]))
    if mining_seed_enabled({"mining_recommendation": mining}):
        mining = dict(mining)
        mining["job"] = mining.get("job") or "seed-pending-operator-review"
        note = mining.get("note") or ""
        review = (
            f"Seed review note: `{mining_seed_path()}`. "
            "Run the first mining pass in propose-only mode after operator review."
        )
        mining["note"] = f"{note}\n\n{review}".strip()
    return yaml_dump(mining, 2)


def onboarding_profile(data: dict) -> dict:
    kind = stack_kind(data)
    story_ids = starter_story_ids(data)
    pack = selected_story_pack(data)
    patterns = [
        {
            "id": "selected-starter",
            "source": "deterministic-init",
            "evidence": f"Selected dev-story for a {data.get('stack') or 'local project'} checkout.",
            "recommendation": "Use dev-story as the shared root while keeping project-specific changes in `.kitsoki/project-profile.yaml` or the generated `.kitsoki/stories/<id>-dev/` wrapper.",
        },
        {
            "id": "starter-story-focus",
            "source": "operator-or-default-scope",
            "evidence": f"Selected story pack `{pack['id']}`: " + ", ".join(f"`{item}`" for item in story_ids) + ".",
            "recommendation": "Start team adoption on this pack, then expand with `kitsoki project-profile story-packs add <pack>` after adding readiness evidence.",
        }
    ]
    managers = package_managers(data, kind)
    if managers != "[]":
        patterns.append({
            "id": "toolchain",
            "source": "repo-files",
            "evidence": f"Stack kind `{kind}` with package/tool managers {managers}.",
            "recommendation": "Project command gates are projected into dev-story from the profile rather than hardcoded in the shared story.",
        })
    if data.get("repo_vcs") != "none" or data.get("repo_default_branch") or data.get("repo_remote"):
        patterns.append({
            "id": "repo-metadata",
            "source": "local-git",
            "evidence": f"VCS `{data.get('repo_vcs', 'none')}`, default branch `{data.get('repo_default_branch', '')}`, remote `{data.get('repo_remote', '')}`.",
            "recommendation": "Use local VCS metadata for worktree and handoff defaults; no network lookup is required during onboarding.",
        })
    if data.get("ticket_provider_inherited"):
        patterns.append({
            "id": "parent-ticket-provider",
            "source": "parent-project-profile",
            "evidence": f"Inherited `{data.get('tracker')}` ticket provider from `{data.get('ticket_provider_parent_profile')}`.",
            "recommendation": "Keep private tracker setup/auth scripts in the parent meta-repo profile; child projects inherit only the generic binding metadata.",
        })
    rules_files = data.get("rules_files") if isinstance(data.get("rules_files"), list) else []
    if rules_files:
        patterns.append({
            "id": "repo-rules",
            "source": "rules-files",
            "evidence": "Found project rule files: " + ", ".join(f"`{path}`" for path in rules_files) + ".",
            "recommendation": "Treat these as the first customization source before mining older transcripts.",
        })
    mining = data.get("mining_recommendation") if isinstance(data.get("mining_recommendation"), dict) else {}
    if int(mining.get("transcript_count") or 0) > 0:
        patterns.append({
            "id": "associated-transcripts",
            "source": "local-transcripts",
            "evidence": f"Found {mining.get('transcript_count')} associated Claude/Codex sessions.",
            "recommendation": f"Review `{mining_seed_path()}` and run session mining in propose-only mode to evolve this profile.",
        })

    customizations = [
        {
            "id": "project-local-instance",
            "status": "applied",
            "summary": "Generate a project-owned dev-story wrapper under `.kitsoki/stories/<id>-dev/` that imports `@kitsoki/dev-story` and carries the standard provider/routing bindings.",
            "evidence": f".kitsoki/stories/{data['project_id']}-dev/app.yaml",
        },
        {
            "id": "project-doc-defaults",
            "status": "applied",
            "summary": "Retarget generated PRD/design outputs to project-local runtime docs paths.",
            "evidence": f"{dev_story_docs_profile(data)['publish_durable_path']}, {dev_story_docs_profile(data)['design_durable_path']}",
        },
        {
            "id": "ticket-intake",
            "status": "applied",
            "summary": f"Ticket intake starts with {ticket_provider_summary(data)}.",
            "evidence": f"binding={ticket_binding_for_profile(data)}; github_source={ticket_github_repo_default(data)}",
        },
    ]
    if data.get("build_command") or data.get("test_command"):
        customizations.append({
            "id": "toolchain-gates",
            "status": "applied",
            "summary": "Project build/test commands are projected into dev-story bugfix gates.",
            "evidence": f"build={data.get('build_command', '')}; test={data.get('test_command', '')}",
        })
    if data.get("ticket_repo"):
        customizations.append({
            "id": "github-ticket-source",
            "status": "applied",
            "summary": f"Ticket search includes GitHub issues from `{data['ticket_repo']}` through the composite local+GitHub provider; selected GitHub rows carry the remote repo into child stories.",
            "evidence": f"remote={data.get('repo_remote', '')}",
        })
    elif data.get("ticket_provider_inherited"):
        customizations.append({
            "id": "inherited-ticket-source",
            "status": "applied",
            "summary": f"Tickets bind to inherited `{data.get('tracker')}` through `iface.ticket` using parent profile metadata.",
            "evidence": ticket_binding_for_profile(data),
        })
    else:
        customizations.append({
            "id": "local-ticket-intake",
            "status": "applied",
            "summary": "Tickets use local markdown/copy-paste intake. This is the good no-provider mode; configure a ticket provider later for remote search, body fetch, comments, and status transitions.",
            "evidence": "ticket provider not selected during onboarding",
        })
    customizations.append({
        "id": "starter-story-focus",
        "status": "applied",
        "summary": f"Initial project adoption is scoped to the `{pack['id']}` pack unless the team expands it.",
        "evidence": ", ".join(story_ids),
    })
    if mining_seed_enabled(data):
        customizations.append({
            "id": "session-mining-seed",
            "status": "pending",
            "summary": "Associated transcript history is captured as an operator-reviewed seed for future project customization.",
            "evidence": mining_seed_path(),
        })

    return {
        "base_story": "dev-story",
        "base_story_title": "Dev-story project workflow",
        "base_story_reason": "Default starter for normal software repositories. This profile intentionally narrows first adoption to the enabled story list, then lets the team expand from the same dev-story root.",
        "story_pack": pack["id"],
        "story_pack_title": pack["title"],
        "story_pack_summary": pack["summary"],
        "starter_stories": starter_story_focus(data),
        "expansion_policy": expansion_policy(data),
        "repo_patterns": patterns,
        "story_customizations": customizations,
        "recording_policy": "no-llm-only",
    }


def onboarding_profile_yaml(data: dict, indent: int = 2) -> str:
    return yaml_dump(onboarding_profile(data), indent)


def ticket_provider_readiness_check(data: dict) -> dict | None:
    meta = data.get("tracker_metadata") if isinstance(data.get("tracker_metadata"), dict) else {}
    command = str(meta.get("readiness_command") or "").strip()
    if not command:
        return None
    cwd = str(data.get("ticket_provider_parent_root") or "").strip()
    if cwd and cwd != ".":
        command = f"cd {shlex.quote(cwd)} && {command}"
    return {
        "id": "ticket-provider",
        "kind": "readiness",
        "command": command,
        "gate": "recommended",
    }


def generic_verifications(data: dict) -> list[dict]:
    project_id = data["project_id"]
    verifications = [
        {
            "id": "story-load",
            "kind": "story",
            "command": f"kitsoki validate .kitsoki/stories/{project_id}-dev/app.yaml",
            "gate": "required",
        },
    ]
    ticket_check = ticket_provider_readiness_check(data)
    if ticket_check is not None:
        verifications.append(ticket_check)
    if data.get("build_command"):
        verifications.append({
            "id": "build",
            "kind": "build",
            "command": data["build_command"],
            "gate": "required",
        })
    if data.get("test_command"):
        verifications.append({
            "id": "unit-tests",
            "kind": "tests",
            "command": data["test_command"],
            "gate": "required",
        })
    if data.get("check_command"):
        verifications.append({
            "id": "check",
            "kind": "tests",
            "command": data["check_command"],
            "gate": "recommended",
        })
    if data.get("dev_command"):
        verifications.append({
            "id": "dev-command",
            "kind": "dev-server",
            "command": data["dev_command"],
            "gate": "advisory",
        })
    return verifications


def slidey_verifications(data: dict) -> list[dict]:
    return [
        {
            "id": "story-load",
            "kind": "story",
            "command": "kitsoki validate .kitsoki/stories/slidey-dev/app.yaml",
            "gate": "required",
        },
        {
            "id": "unit-tests",
            "kind": "tests",
            "command": data.get("test_command", ""),
            "gate": "required",
        },
        {
            "id": "build",
            "kind": "build",
            "command": data.get("build_command", ""),
            "gate": "required",
        },
        {
            "id": "cli-validate",
            "kind": "golden-path",
            "command": "node src/index.js examples/hello.slidey.json --validate",
            "gate": "required",
        },
        {
            "id": "html-bundle",
            "kind": "artifact",
            "command": "node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html",
            "gate": "recommended",
        },
        {
            "id": "web-player",
            "kind": "ui",
            "command": data.get("dev_command", ""),
            "gate": "recommended",
        },
    ]


def setup_verifications(data: dict) -> list[dict]:
    checks = slidey_verifications(data) if data["project_id"] == "slidey" else generic_verifications(data)
    return [check for check in checks if str(check.get("command") or "").strip()]


def generic_setup_plan_yaml(data: dict) -> str:
    project_id = data["project_id"]
    docs = dev_story_docs_profile(data)
    return yaml_dump({
        "writes": [
            {
                "path": ".kitsoki/project-profile.yaml",
                "action": "create",
                "summary": "Declarative onboarding profile for this project.",
            },
            {
                "path": f".kitsoki/stories/{project_id}-dev/app.yaml",
                "action": "create",
                "summary": "Thin project-owned dev-story instance importing @kitsoki/dev-story.",
            },
            {
                "path": f".kitsoki/stories/{project_id}-dev/README.md",
                "action": "create",
                "summary": "Local operator handoff for running and extending the generated instance.",
            },
            {
                "path": ".kitsoki/check-readiness.py",
                "action": "create",
                "summary": "Explicit post-apply verifier for the profile-declared checks.",
            },
            {
                "path": ".kitsoki/promote-session-mining.py",
                "action": "create",
                "summary": "Promote emitted session-mining recipes into pending profile customizations.",
            },
            {
                "path": ".kitsoki.yaml",
                "action": "create",
                "summary": "Discover project-local Kitsoki stories and select the generated instance.",
            },
            {
                "path": ".gitignore",
                "action": "merge",
                "summary": "Ignore local Kitsoki runtime, session, artifact, and worktree files.",
            },
        ] + mining_setup_writes(data),
        "dirs_create": [
            ".kitsoki",
            ".kitsoki/stories",
            f".kitsoki/stories/{project_id}-dev",
            ".context",
            docs["publish_durable_path"],
            docs["design_durable_path"],
            ".artifacts",
            ".worktrees",
        ],
        "gitignore_additions": [
            ".kitsoki.local.yaml",
            ".kitsoki/sessions/",
            ".artifacts/",
            ".context/",
            ".worktrees/",
        ],
        "verifications": setup_verifications(data),
    }, 2)


def generic_profile_yaml(data: dict) -> str:
    kind = stack_kind(data)
    docs = dev_story_docs_profile(data)
    languages = {
        "rust": "[rust]",
        "go": "[go]",
        "node": "[javascript]",
        "python": "[python]",
    }.get(kind, "[]")
    return f"""schema: project-profile/v1
id: {data["project_id"]}
title: {data["project_title"]}
summary: |
  Project-local Kitsoki dev-story binding for {data["project_title"]}. The
  generated instance imports `@kitsoki/dev-story`; this repository owns only the
  profile values, tool commands, and local setup files.

commands:
  dev: {q(data.get("dev_command", ""))}
  test: {q(data.get("test_command", ""))}
  build: {q(data.get("build_command", ""))}
  check: {q(data.get("check_command", ""))}

repo:
  root: "."
  vcs: {data.get("repo_vcs", "git")}
  default_branch: {q(data.get("repo_default_branch", "main"))}
  remote: {q(data.get("repo_remote", ""))}
  monorepo: {str(bool(data.get("is_monorepo"))).lower()}

stack:
  kind: {q(kind)}
  languages: {languages}
  package_managers: {package_managers(data, kind)}

testing:
  mechanisms:
    - kind: unit
      runner: command
      command: {q(data.get("test_command", ""))}
    - kind: build
      runner: command
      command: {q(data.get("build_command", ""))}

conventions:
  source: {convention_source(data.get("conventions", "project"))}
  dirs:
    context:   {{ path: ".context",   use: local-runtime }}
    artifacts: {{ path: ".artifacts", use: local-runtime }}
    worktrees: {{ path: ".worktrees", use: local-runtime }}
  rules_files:
{yaml_dump(data.get("rules_files") or [], 4)}
  gitignore:
    manage: true
    additions:
      - ".kitsoki.local.yaml"
      - ".kitsoki/sessions/"
      - ".artifacts/"
      - ".context/"
      - ".worktrees/"

tracker:
{tracker_profile_yaml(data, 2)}

kitsoki:
  story: dev-story
  story_pack: {q(selected_story_pack(data)["id"])}
  enabled_stories:
{yaml_dump(starter_story_ids(data), 4)}
  instance:
    id: {data["project_id"]}-dev
    path: .kitsoki/stories/{data["project_id"]}-dev/app.yaml
    bindings:
      ticket: {binding_yaml(ticket_binding_for_profile(data))}
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
  judge_mode: human
  autonomy: supervised

dev_story_profile:
  docs:
    publish_durable_path: {q(docs["publish_durable_path"])}
    prd_doc_filename: {q(docs["prd_doc_filename"])}
    design_template_dir: {q(docs["design_template_dir"])}
    design_durable_path: {q(docs["design_durable_path"])}
    design_doc_filename: {q(docs["design_doc_filename"])}
    design_ticket_dir: {q(docs["design_ticket_dir"])}
    ticket_repo: {q(docs["ticket_repo"])}
  bugfix:
    build_cmd: {q(data.get("build_command", ""))}
    test_cmd: {q(data.get("test_command", ""))}

onboarding:
{onboarding_profile_yaml(data)}

mining:
{mining_profile_yaml(data)}

setup_plan:
{generic_setup_plan_yaml(data)}

readiness:
  status: not-run
"""


def slidey_profile_yaml(data: dict) -> str:
    docs = dev_story_docs_profile(data)
    return f"""schema: project-profile/v1
id: slidey
title: Slidey
summary: |
  Deterministic, spec-driven declarative deck engine. Slidey renders JSON scene
  specs through the same Vue components as an interactive web player, a
  self-contained HTML deck, PDF, and optional deterministic MP4 video export.

generated:
  by: "kitsoki dev-story project onboarding"
  at: "2026-06-23"

repo:
  root: "."
  vcs: {data.get("repo_vcs", "git")}
  default_branch: {q(data.get("repo_default_branch", "main"))}
  remote: {q(data.get("repo_remote", ""))}
  monorepo: false

stack:
  kind: fullstack
  languages: [javascript]
  frameworks: [vue, vite, puppeteer]
  package_managers: [npm]

dev_server:
  components:
    - name: viewer
      role: frontend
      command: "npm run dev"
      cwd: "."
      url: "http://127.0.0.1:5173"
      ready:
        probe: http
        target: "http://127.0.0.1:5173/"
        expect: "200"
        timeout_ms: 30000
        interval_ms: 500
    - name: cli-viewer
      role: backend
      command: {q(data.get("dev_command", ""))}
      cwd: "."
      url: "http://127.0.0.1:5000"
      ready:
        probe: http
        target: "http://127.0.0.1:5000/"
        expect: "200"
        timeout_ms: 30000
        interval_ms: 500

environments:
  - name: local
    kind: local
    url: "http://127.0.0.1:5000"
    config_ref: ".kitsoki.yaml"
    notes: "Use the CLI viewer for workspace behavior; plain Vite is useful for component work."
  - name: ci
    kind: test
    config_ref: "package.json"
    notes: "npm test exercises the Node test suite."

commands:
  install: "npm install"
  build: {q(data.get("build_command", ""))}
  dev: {q(data.get("dev_command", ""))}
  viewer: {q(data.get("dev_command", ""))}
  html_bundle: "node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html"
  validate_deck: "node src/index.js examples/hello.slidey.json --validate"
  render_pdf: "node src/index.js examples/hello.slidey.json .artifacts/hello.pdf"
  render_mp4: "node src/index.js examples/hello.slidey.json .artifacts/hello.mp4"
  test: {q(data.get("test_command", ""))}
  e2e: "npm run test:vscode"
  lint: ""
  typecheck: ""

output_workflows:
  primary_review:
    format: web-player
    command: {q(data.get("dev_command", ""))}
    url: "http://127.0.0.1:5000/"
    use_when: "Inspecting, editing, navigating, or reviewing a deck interactively."
    notes: "This is the default human review path; it serves the real Vue viewer and workspace assets."
  shareable_review:
    format: single-file-html
    command: "node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html"
    output: ".artifacts/hello.html"
    use_when: "Sending or archiving a deck review artifact that should open without a local server."
    notes: "The HTML bundle inlines the viewer, CSS, spec, and local image/gif assets."
  export:
    format: mp4
    command: "node src/index.js examples/hello.slidey.json .artifacts/hello.mp4"
    output: ".artifacts/hello.mp4"
    use_when: "Producing fixed video evidence, narrated playback, or a video scene source for another deck."
    notes: "MP4 is not the primary review surface; use it only when a baked video artifact is needed."

testing:
  frameworks: [node-test]
  mechanisms:
    - kind: unit
      runner: node-test
      command: {q(data.get("test_command", ""))}
    - kind: build
      runner: npm
      command: {q(data.get("build_command", ""))}
    - kind: e2e
      runner: node-test
      command: "npm run test:vscode"
  ci:
    provider: none
    config_ref: "package.json"

golden_path:
  applicable: true
  kind: ui
  name: "Open a Slidey spec in the web player"
  description: |
    Start the Slidey CLI workspace server on a known example deck and confirm
    the interactive web player responds without using any LLM or
    network-dependent narration. Use single-file HTML for portable review and
    MP4 only for baked video evidence or narration output.
  steps:
    - "Run {data.get("dev_command", "")}"
    - "Open http://127.0.0.1:5000/"
    - "Confirm the selected example deck renders."
    - "For shareable deck review, run node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html"
  verify:
    harness: command
    spec: "node src/index.js examples/hello.slidey.json --validate"
    url: "http://127.0.0.1:5000/"

conventions:
  source: hybrid
  dirs:
    context:   {{ path: ".context",   use: existing }}
    artifacts: {{ path: ".artifacts", use: existing }}
    worktrees: {{ path: ".worktrees", use: existing }}
  gitignore:
    manage: true
    additions:
      - ".kitsoki.local.yaml"
      - ".kitsoki/sessions/"
      - ".artifacts/"
      - ".context"
      - ".worktrees"
  rules_files: []

rules:
  - id: web-player-first
    scope: testing
    source: operator
    ref: "README.md#install-as-a-cli--open-a-folderfile"
    text: "Use the Slidey web player as the primary deck review surface; reserve MP4 for fixed video evidence or narration export."
  - id: html-for-shareable-review
    scope: artifacts
    source: operator
    ref: "README.md#install-as-a-cli--open-a-folderfile"
    text: "When a reviewable deck artifact is needed, prefer a single-file HTML bundle over an embedded MP4 unless motion/narration is the goal."
  - id: no-real-llm-in-tests
    scope: tests
    source: kitsoki
    ref: "Kitsoki AGENTS.md"
    text: "Automated Kitsoki story tests use mocked interactions and never require a real LLM."

kitsoki:
  story: dev-story
  story_pack: {q(selected_story_pack(data)["id"])}
  enabled_stories:
{yaml_dump(starter_story_ids(data), 4)}
  instance:
    id: slidey-dev
    path: ".kitsoki/stories/slidey-dev/app.yaml"
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
  harness_profile: ""
  judge_mode: human
  autonomy: supervised

dev_story_profile:
  docs:
    publish_durable_path: {q(docs["publish_durable_path"])}
    prd_doc_filename: {q(docs["prd_doc_filename"])}
    design_template_dir: {q(docs["design_template_dir"])}
    design_durable_path: {q(docs["design_durable_path"])}
    design_doc_filename: {q(docs["design_doc_filename"])}
    design_ticket_dir: {q(docs["design_ticket_dir"])}
    ticket_repo: {q(docs["ticket_repo"])}
  bugfix:
    build_cmd: {q(data.get("build_command", ""))}
    test_cmd: {q(data.get("test_command", ""))}

onboarding:
{onboarding_profile_yaml(data)}

mining:
{mining_profile_yaml(data)}

setup_plan:
  writes:
    - path: ".kitsoki/project-profile.yaml"
      action: create
      summary: "Declarative onboarding profile for Slidey."
    - path: ".kitsoki/stories/slidey-dev/app.yaml"
      action: create
      summary: "Materialized dev-story instance for Slidey."
    - path: ".kitsoki/check-readiness.py"
      action: create
      summary: "Explicit post-apply verifier for the profile-declared checks."
    - path: ".kitsoki/promote-session-mining.py"
      action: create
      summary: "Promote emitted session-mining recipes into pending profile customizations."
    - path: ".kitsoki.yaml"
      action: create
      summary: "Discover project-local stories under ./.kitsoki/stories."
    - path: ".gitignore"
      action: merge
      summary: "Ignore local Kitsoki runtime/session artifacts."
{yaml_dump(mining_setup_writes(data), 4) if mining_seed_enabled(data) else ""}
  dirs_create: [".context", ".artifacts", ".worktrees", ".kitsoki"]
  gitignore_additions:
    - ".kitsoki.local.yaml"
    - ".kitsoki/sessions/"
    - ".artifacts/"
    - ".context"
    - ".worktrees"
  verifications:
{yaml_dump(setup_verifications(data), 4)}

readiness:
  status: not-run
"""


def config_yaml(data: dict) -> str:
    return f"""story_dirs:
  - ./.kitsoki/stories

project_profile: .kitsoki/project-profile.yaml

root:
  import: dev-story
{mining_config_yaml(data)}"""


def readiness_script(data: dict) -> str:
    checks_json = json.dumps(setup_verifications(data), indent=2, sort_keys=True)
    return f'''#!/usr/bin/env python3
"""Run Kitsoki onboarding readiness checks for this checkout.

Generated by `kitsoki` project onboarding. It is intentionally explicit:
onboarding writes this file but does not run it automatically.
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path


CHECKS = {checks_json}


def run_check(check: dict, root: Path, timeout: int) -> dict:
    command = str(check.get("command") or "").strip()
    started = time.time()
    result = {{
        "id": check.get("id", ""),
        "kind": check.get("kind", ""),
        "gate": check.get("gate", ""),
        "command": command,
        "ok": False,
        "exit_code": None,
        "duration_ms": 0,
        "stdout": "",
        "stderr": "",
    }}
    if not command:
        result["stderr"] = "empty command"
        return result
    try:
        proc = subprocess.run(
            command,
            cwd=root,
            shell=True,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired as err:
        result["duration_ms"] = int((time.time() - started) * 1000)
        result["stderr"] = f"timed out after {{timeout}}s"
        if err.stdout:
            result["stdout"] = err.stdout if isinstance(err.stdout, str) else err.stdout.decode("utf-8", "replace")
        if err.stderr:
            result["stderr"] += "\\n" + (err.stderr if isinstance(err.stderr, str) else err.stderr.decode("utf-8", "replace"))
        return result
    result["duration_ms"] = int((time.time() - started) * 1000)
    result["exit_code"] = proc.returncode
    result["ok"] = proc.returncode == 0
    result["stdout"] = proc.stdout
    result["stderr"] = proc.stderr
    return result


def detail_for(item: dict) -> str:
    parts = []
    if item.get("exit_code") is not None:
        parts.append(f"exit={{item.get('exit_code')}}")
    if item.get("stderr"):
        parts.append("stderr: " + str(item.get("stderr", "")).strip().splitlines()[0][:240])
    elif item.get("stdout"):
        parts.append("stdout: " + str(item.get("stdout", "")).strip().splitlines()[0][:240])
    return "; ".join(parts) or "completed"


def yaml_scalar(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    return json.dumps(str(value))


def readiness_yaml(report: dict) -> str:
    lines = ["readiness:", f"  status: {{yaml_scalar(report.get('status', 'not-run'))}}"]
    checks = report.get("checks") if isinstance(report.get("checks"), list) else []
    if checks:
        lines.append("  checks:")
        for item in checks:
            lines.append(f"    - id: {{yaml_scalar(item.get('id', ''))}}")
            lines.append(f"      kind: {{yaml_scalar(item.get('kind', ''))}}")
            lines.append(f"      ok: {{yaml_scalar(bool(item.get('ok')))}}")
            detail = detail_for(item)
            if detail:
                lines.append(f"      detail: {{yaml_scalar(detail)}}")
    return "\\n".join(lines) + "\\n"


def replace_top_level_block(text: str, key: str, replacement: str) -> str:
    lines = text.splitlines()
    start = -1
    for index, line in enumerate(lines):
        if line == key + ":":
            start = index
            break
    if start == -1:
        return text.rstrip() + "\\n\\n" + replacement
    end = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line and not line.startswith((" ", "\\t")):
            end = index
            break
    return "\\n".join(lines[:start] + replacement.rstrip().splitlines() + lines[end:]) + "\\n"


def update_profile(root: Path, report: dict) -> Path:
    profile = root / ".kitsoki" / "project-profile.yaml"
    text = profile.read_text(encoding="utf-8")
    profile.write_text(replace_top_level_block(text, "readiness", readiness_yaml(report)), encoding="utf-8")
    return profile


def main() -> int:
    parser = argparse.ArgumentParser(description="Run project onboarding readiness checks.")
    parser.add_argument("--list", action="store_true", help="print the checks without running them")
    parser.add_argument("--json", action="store_true", help="print the report JSON to stdout")
    parser.add_argument("--update-profile", action="store_true", help="persist a readiness summary into .kitsoki/project-profile.yaml")
    parser.add_argument("--timeout", type=int, default=120, help="per-check timeout in seconds")
    parser.add_argument("--out", default=".artifacts/kitsoki-readiness.json", help="report path")
    args = parser.parse_args()

    root = Path(__file__).resolve().parents[1]
    if args.list:
        print(json.dumps({{"checks": CHECKS}}, indent=2, sort_keys=True))
        return 0

    results = [run_check(check, root, args.timeout) for check in CHECKS]
    required_failed = [
        item for item in results
        if item.get("gate") == "required" and not item.get("ok")
    ]
    any_failed = any(not item.get("ok") for item in results)
    status = "pass"
    if required_failed:
        status = "fail"
    elif any_failed:
        status = "partial"
    report = {{
        "schema": "kitsoki-readiness/v1",
        "status": status,
        "checks": results,
    }}
    out = root / args.out
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(report, indent=2, sort_keys=True) + "\\n", encoding="utf-8")
    profile = None
    if args.update_profile:
        profile = update_profile(root, report)
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True))
    else:
        print(f"readiness {{status}}: {{out}}")
        if profile is not None:
            print(f"profile updated: {{profile}}")
        for item in results:
            mark = "PASS" if item.get("ok") else "FAIL"
            print(f"- {{mark}} {{item.get('id')}}: {{item.get('command')}}")
    return 1 if required_failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
'''


def mining_promotion_script() -> str:
    return r'''#!/usr/bin/env python3
"""Promote session-mining recipe reports into pending profile customizations.

This is deterministic and local-only. It does not run mining, call an LLM, or
edit the shared dev-story. It reads already-emitted analysis.json files and
adds pending entries under onboarding.story_customizations for operator review.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path


def slug(value: str) -> str:
    value = re.sub(r"[^a-z0-9]+", "-", value.lower()).strip("-")
    return value or "recipe"


def find_analysis(root: Path, explicit: list[str]) -> list[Path]:
    if explicit:
        return [Path(item).expanduser().resolve() for item in explicit]
    jobs = root / ".artifacts" / "mining" / "jobs"
    if not jobs.exists():
        return []
    return sorted(jobs.glob("*/analysis.json"))


def load_recipes(path: Path) -> list[dict]:
    try:
        doc = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as err:
        return [{"id": slug(str(path)), "status": "error", "summary": f"Could not read {path}: {err}", "evidence": str(path)}]
    out: list[dict] = []
    for inst in doc.get("instances", []):
        if not isinstance(inst, dict):
            continue
        grounding = inst.get("grounding") if isinstance(inst.get("grounding"), dict) else {}
        if grounding.get("quarantined"):
            continue
        instance_id = str(inst.get("instance_id") or "")
        if not instance_id:
            continue
        determinism = str(inst.get("determinism") or "unknown")
        tags = inst.get("tags") if isinstance(inst.get("tags"), dict) else {}
        actions = tags.get("action") if isinstance(tags.get("action"), list) else []
        action_text = ", ".join(str(item) for item in actions[:4]) or "session-mined workflow"
        out.append({
            "id": "mined-" + slug(instance_id)[-48:],
            "status": "pending",
            "summary": f"Review session-mined {determinism} recipe: {action_text}.",
            "evidence": f"{path}#{instance_id}",
        })
    return out


def yaml_quote(value: str) -> str:
    return json.dumps(str(value))


def entry_yaml(entry: dict) -> list[str]:
    return [
        f"    - id: {yaml_quote(entry['id'])}",
        f"      status: {yaml_quote(entry['status'])}",
        f"      summary: {yaml_quote(entry['summary'])}",
        f"      evidence: {yaml_quote(entry['evidence'])}",
    ]


def existing_ids(text: str) -> set[str]:
    return set(re.findall(r'^\s+id:\s+"([^"]+)"\s*$', text, flags=re.MULTILINE))


def parse_scalar(raw: str) -> str:
    raw = raw.strip()
    if not raw:
        return ""
    try:
        value = json.loads(raw)
    except json.JSONDecodeError:
        return raw
    return str(value)


def customization_ranges(lines: list[str]) -> list[tuple[int, int, dict]]:
    start = -1
    for index, line in enumerate(lines):
        if line == "  story_customizations:":
            start = index
            break
    if start == -1:
        return []
    end = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line.startswith("  ") and not line.startswith("    ") and line.strip():
            end = index
            break
        if line and not line.startswith((" ", "\t")):
            end = index
            break
    starts: list[int] = []
    for index in range(start + 1, end):
        if re.match(r"^\s*-\s*(?:\w+:.*)?$", lines[index]):
            starts.append(index)
    ranges: list[tuple[int, int, dict]] = []
    for pos, item_start in enumerate(starts):
        item_end = starts[pos + 1] if pos + 1 < len(starts) else end
        entry: dict[str, str] = {}
        for line in lines[item_start:item_end]:
            match = re.match(r"^\s*(?:-\s*)?([A-Za-z_][\w-]*):\s*(.*)$", line)
            if match:
                entry[match.group(1)] = parse_scalar(match.group(2))
        ranges.append((item_start, item_end, entry))
    return ranges


def list_customizations(text: str) -> list[dict]:
    lines = text.splitlines()
    return [entry for _, _, entry in customization_ranges(lines) if entry.get("id")]


def status_counts(entries: list[dict]) -> dict:
    counts: dict[str, int] = {}
    for entry in entries:
        status = str(entry.get("status") or "unknown")
        counts[status] = counts.get(status, 0) + 1
        alias = status.replace("-", "_")
        if alias != status:
            counts[alias] = counts.get(alias, 0) + 1
    return counts


def replace_or_insert_key(block: list[str], key: str, value: str) -> list[str]:
    rendered = f"      {key}: {yaml_quote(value)}"
    pattern = re.compile(rf"^(\s*(?:-\s*)?{re.escape(key)}:\s*).*$")
    out = list(block)
    for index, line in enumerate(out):
        if pattern.match(line):
            prefix = pattern.match(line).group(1)
            out[index] = f"{prefix}{yaml_quote(value)}"
            return out
    insert_at = len(out)
    for index, line in enumerate(out):
        if re.match(r"^\s*(?:-\s*)?evidence:\s*", line):
            insert_at = index + 1
            break
    out.insert(insert_at, rendered)
    return out


def update_pending_customizations(text: str, status: str, feedback: str = "") -> tuple[str, list[dict]]:
    lines = text.splitlines()
    ranges = customization_ranges(lines)
    if not ranges:
        return text, []
    out: list[str] = []
    cursor = 0
    changed: list[dict] = []
    for start, end, entry in ranges:
        out.extend(lines[cursor:start])
        block = lines[start:end]
        if entry.get("status") == "pending":
            block = replace_or_insert_key(block, "status", status)
            if feedback:
                block = replace_or_insert_key(block, "review_feedback", feedback)
            updated = dict(entry)
            updated["status"] = status
            if feedback:
                updated["review_feedback"] = feedback
            changed.append(updated)
        out.extend(block)
        cursor = end
    out.extend(lines[cursor:])
    return "\n".join(out) + "\n", changed


def insert_customizations(text: str, entries: list[dict]) -> str:
    if not entries:
        return text
    ids = existing_ids(text)
    new_entries = [entry for entry in entries if entry["id"] not in ids]
    if not new_entries:
        return text
    lines = text.splitlines()
    start = -1
    for index, line in enumerate(lines):
        if line == "  story_customizations:":
            start = index
            break
    block: list[str] = []
    for entry in new_entries:
        block.extend(entry_yaml(entry))
    if start == -1:
        onboarding = -1
        for index, line in enumerate(lines):
            if line == "onboarding:":
                onboarding = index
                break
        if onboarding == -1:
            return text.rstrip() + "\n\nonboarding:\n  story_customizations:\n" + "\n".join(block) + "\n"
        insert_at = len(lines)
        for index in range(onboarding + 1, len(lines)):
            if lines[index] and not lines[index].startswith((" ", "\t")):
                insert_at = index
                break
        return "\n".join(lines[:insert_at] + ["  story_customizations:"] + block + lines[insert_at:]) + "\n"
    insert_at = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line.startswith("  ") and not line.startswith("    ") and line.strip():
            insert_at = index
            break
        if line and not line.startswith((" ", "\t")):
            insert_at = index
            break
    return "\n".join(lines[:insert_at] + block + lines[insert_at:]) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description="Promote session-mining analysis into pending Kitsoki customizations.")
    parser.add_argument("analysis", nargs="*", help="analysis.json path(s); defaults to .artifacts/mining/jobs/*/analysis.json")
    parser.add_argument("--profile", default=".kitsoki/project-profile.yaml", help="project profile to update")
    parser.add_argument("--dry-run", action="store_true", help="print proposed entries without writing")
    parser.add_argument("--json", action="store_true", help="print machine-readable summary")
    parser.add_argument("--list", action="store_true", help="list current profile customizations")
    parser.add_argument("--accept-pending", action="store_true", help="mark pending profile customizations accepted")
    parser.add_argument("--refine-pending", metavar="FEEDBACK", help="mark pending profile customizations as needing refinement")
    args = parser.parse_args()

    root = Path.cwd()
    profile = root / args.profile
    if args.list or args.accept_pending or args.refine_pending is not None:
        if not profile.exists():
            print(f"profile not found: {profile}", file=sys.stderr)
            return 2
        text = profile.read_text(encoding="utf-8")
        updated = text
        changed: list[dict] = []
        action = "list"
        if args.accept_pending:
            action = "accept"
            updated, changed = update_pending_customizations(text, "accepted")
        elif args.refine_pending is not None:
            action = "refine"
            updated, changed = update_pending_customizations(text, "needs-refinement", args.refine_pending)
        if updated != text and not args.dry_run:
            profile.write_text(updated, encoding="utf-8")
        summary = {
            "action": action,
            "entries": list_customizations(updated if not args.dry_run else text),
            "changed": changed,
            "counts": status_counts(list_customizations(updated if not args.dry_run else text)),
            "updated": updated != text and not args.dry_run,
        }
        print(json.dumps(summary, indent=2, sort_keys=True) if args.json or args.dry_run else f"{action} {len(changed)} pending customization(s); updated={summary['updated']}")
        return 0

    analyses = find_analysis(root, args.analysis)
    entries: list[dict] = []
    for path in analyses:
        entries.extend(load_recipes(path))
    entries = [entry for entry in entries if entry.get("status") == "pending"]
    summary = {"action": "promote", "analysis_files": [str(path) for path in analyses], "entries": entries, "updated": False}
    if args.dry_run:
        print(json.dumps(summary, indent=2, sort_keys=True))
        return 0
    if not profile.exists():
        print(f"profile not found: {profile}", file=sys.stderr)
        return 2
    text = profile.read_text(encoding="utf-8")
    updated = insert_customizations(text, entries)
    if updated != text:
        profile.write_text(updated, encoding="utf-8")
        summary["updated"] = True
    summary["entries"] = list_customizations(updated)
    summary["counts"] = status_counts(summary["entries"])
    if args.json:
        print(json.dumps(summary, indent=2, sort_keys=True))
    else:
        print(f"promoted {len(entries)} pending customization(s); updated={summary['updated']}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
'''


def mining_seed_markdown(data: dict) -> str:
    mining = data.get("mining_recommendation") if isinstance(data.get("mining_recommendation"), dict) else {}
    sources = mining.get("sources") if isinstance(mining, dict) else []
    if not isinstance(sources, list):
        sources = []
    source_lines = []
    for source in sources:
        if not isinstance(source, dict):
            continue
        backend = source.get("backend", "unknown")
        sessions = source.get("sessions", 0)
        path = source.get("dir", "")
        source_lines.append(f"- {backend}: {sessions} sessions at `{path}`")
    if not source_lines:
        source_lines.append("- No transcript source details were recorded.")
    first_pass = mining.get("first_pass_sample", 0) if isinstance(mining, dict) else 0
    sample = mining.get("sample", "recency") if isinstance(mining, dict) else "recency"
    note = mining.get("note", "") if isinstance(mining, dict) else ""
    return f"""# Kitsoki Session Mining Seed

Kitsoki found existing Claude/Codex transcript history associated with this
checkout during first-run onboarding. No mining pass has run yet and no LLM cost
was incurred by onboarding.

Review goal:

- Seed project-local customization from prior real usage.
- Prefer proposed `.kitsoki/` profile or root-instance changes over changes to
  the shared `@kitsoki/dev-story`.
- Keep generated proposals under `.artifacts/session-mining/` until an operator
  accepts them.

Discovered sources:

{chr(10).join(source_lines)}

Seed pass defaults:

- trigger: `seed`
- sample: `{sample}`
- first pass sample: `{first_pass}`
- target: `root-instance`
- apply mode: `propose-only`
- runtime config: `.kitsoki.yaml` contains `mining.enabled: false` plus the
  discovered `transcript_dirs`; use `/mine resume` or `/mine now` only after
  reviewing this note.

Discovery note:

{note or "(none)"}
"""


def readme(data: dict, profile_path: str) -> str:
    title = data["project_title"]
    story_id = f"{data['project_id']}-dev"
    docs = dev_story_docs_profile(data)
    pack = selected_story_pack(data)
    starter_lines = "\n".join(
        f"- `{item['id']}` ({item['source_story']}): {item['summary']}"
        for item in starter_story_focus(data)
    )
    commands = []
    if data.get("dev_command"):
        commands.append(("dev", data["dev_command"]))
    if data.get("test_command"):
        commands.append(("test", data["test_command"]))
    if data.get("build_command"):
        commands.append(("build", data["build_command"]))
    command_block = "\n".join(cmd for _, cmd in commands) or "# No project commands were inferred during onboarding."
    command_notes = "\n".join(f"- `{name}`: `{cmd}`" for name, cmd in commands) or "- No project commands were inferred; update `.kitsoki/project-profile.yaml` and this README after choosing them."
    if data.get("ticket_provider_inherited"):
        meta = data.get("tracker_metadata") if isinstance(data.get("tracker_metadata"), dict) else {}
        setup = str(meta.get("setup_command") or "").strip()
        readiness = str(meta.get("readiness_command") or "").strip()
        ticket_note = f"""Ticket provider:

- source: inherited `{data.get("tracker")}` from `{data.get("ticket_provider_parent_profile")}`
- binding: `{ticket_binding_for_profile(data)}`
- setup command: `{setup or "(not declared)"}`
- readiness command: `{readiness or "(not declared)"}`
"""
    elif data.get("ticket_repo"):
        ticket_note = f"""Ticket provider:

- source: GitHub Issues `{data["ticket_repo"]}`
- binding: `host.gh.ticket`
"""
    else:
        ticket_note = """Ticket provider:

- source: local files
- binding: `host.local_files.ticket`
"""
    flow_note = (
        "No deterministic flow fixtures are generated for this project instance yet. "
        "Use the imported dev-story fixtures in the Kitsoki checkout for hub coverage, "
        "and add project-local flows when this repo needs its own story-specific assertions."
    )
    if data.get("ticket_provider_inherited"):
        provider_detail = (
            "The parent meta-repo owns tracker setup/auth; this child project "
            "inherits only the generic binding metadata and readiness command."
        )
    elif data.get("ticket_repo"):
        provider_detail = (
            "Full ticket capability is enabled: GitHub issues appear beside local "
            "markdown tickets and pasted reports. Local/pasted reports stay local; "
            "a selected GitHub row carries the remote repo into bugfix close-out."
        )
    else:
        provider_detail = (
            "Good local capability is enabled without remote setup: paste a "
            "bug report into the bugfix path or keep markdown tickets under "
            "`issues/bugs/`. Configure a provider later when you want remote search, "
            "issue-body fetch, comments, and transitions."
        )
    mining_note = ""
    if mining_seed_enabled(data):
        mining_note = f"""
Session mining seed:

Kitsoki found associated Claude/Codex transcript history during onboarding and
wrote `{mining_seed_path()}` as an operator review note. Treat it as a proposed
seed pass for project-local customization; no mining pass or LLM call ran during
onboarding. `.kitsoki.yaml` has `mining.enabled: false` with the discovered
transcript scope prefilled; use `/mine resume` or `/mine now` after review to
opt in.
"""
    return f"""# {story_id}

Kitsoki dev-story instance for the {title} checkout.

Run from the {title} repo root:

```sh
kitsoki run
```

`kitsoki run` starts the profile-driven implicit dev-story root for this
checkout. Use the materialized wrapper explicitly only after editing it:

```sh
kitsoki run .kitsoki/stories/{story_id}/app.yaml
```

Start the browser UI:

```sh
kitsoki web
```

This instance imports `@kitsoki/dev-story` from the Kitsoki binary. The shared
dev-story hub defines the general workflow; this repository owns the local
profile, command defaults, and any project-specific extensions.

Project profile: `{Path(profile_path).relative_to(Path(data["target_path"]))}`
Readiness verifier: `.kitsoki/check-readiness.py`
Session mining promotion helper: `.kitsoki/promote-session-mining.py`

{ticket_note}
{provider_detail}

Starter story pack: `{pack["id"]}` - {pack["title"]}

{starter_lines}

This focus is recorded in `kitsoki.enabled_stories` and
`onboarding.starter_stories` inside `.kitsoki/project-profile.yaml`. It is an
adoption scope, not a runtime fence: the shared dev-story root remains
available. Expand it after adding matching readiness checks or project-local
flow coverage:

```sh
kitsoki project-profile story-packs list
kitsoki project-profile story-packs add <pack>
```

Generated PRDs publish under `{docs["publish_durable_path"]}` and design drafts
publish under `{docs["design_durable_path"]}`. Update
`.kitsoki/project-profile.yaml` and `.kitsoki/stories/{story_id}/app.yaml`
together if this project later adopts a different documentation layout.

Inferred project commands:

```sh
{command_block}
```

Command map:

{command_notes}

Testing:

{flow_note}

Post-apply readiness:

Onboarding does not run project commands automatically. Review the checks, then
run them explicitly when you are ready:

```sh
python3 .kitsoki/check-readiness.py --list
python3 .kitsoki/check-readiness.py --json
python3 .kitsoki/check-readiness.py --json --update-profile
```

The report is written to `.artifacts/kitsoki-readiness.json`. Add
`--update-profile` when you want the summarized pass/fail result persisted into
`.kitsoki/project-profile.yaml`.

Session-mining customization:

When a mining pass emits `.artifacts/mining/jobs/<job>/analysis.json`, review the
candidate profile customizations and promote the pending ones explicitly:

```sh
python3 .kitsoki/promote-session-mining.py --dry-run
python3 .kitsoki/promote-session-mining.py --json
```

The helper updates `onboarding.story_customizations` only; it does not edit the
shared `@kitsoki/dev-story` or run an LLM.
{mining_note}
"""


def main() -> int:
    if len(sys.argv) < 9:
        raise SystemExit("usage: init_apply.py target_path project_id project_title stack dev test build conventions tracker")
    data = {
        "target_path": str(Path(sys.argv[1]).expanduser().resolve()),
        "project_id": slug(sys.argv[2]),
        "project_title": sys.argv[3] or sys.argv[2],
        "stack": sys.argv[4],
        "dev_command": sys.argv[5],
        "test_command": sys.argv[6],
        "build_command": sys.argv[7],
        "conventions": sys.argv[8],
        "tracker": sys.argv[9] if len(sys.argv) > 9 else "none",
        "starter_stories": starter_story_entries(),
        "story_pack": "core-engineering",
        "ticket_repo": "",
    }
    # Validate + enrich the target BEFORE draft-profile defaulting so the
    # detected repo shape (remote → ticket_repo, toolchain files) seeds the
    # draft's dev_story_profile defaults too.
    root = Path(data["target_path"])
    if not root.exists() or not root.is_dir():
        print(json.dumps({
            "status": "target-invalid",
            "target_path": str(root),
            "error": "target path does not exist" if not root.exists() else "target path is not a directory",
        }, sort_keys=True))
        return 1
    enrich_project_shape(data, root)
    draft_profile = None
    if len(sys.argv) > 10 and sys.argv[10].strip():
        draft_profile = json.loads(sys.argv[10])
        commands = draft_profile.get("commands") if isinstance(draft_profile, dict) else {}
        if isinstance(commands, dict):
            data["dev_command"] = commands.get("dev") or data["dev_command"]
            data["test_command"] = commands.get("test") or data["test_command"]
            data["build_command"] = commands.get("build") or data["build_command"]
        if isinstance(draft_profile.get("title"), str):
            data["project_title"] = draft_profile["title"]
    if len(sys.argv) > 11 and sys.argv[11].strip():
        data["mining_recommendation"] = json.loads(sys.argv[11])
    if len(sys.argv) > 12 and sys.argv[12].strip():
        data["starter_stories"] = normalize_starter_stories(json.loads(sys.argv[12]))
        data["story_pack"] = "custom"
    if len(sys.argv) > 13 and sys.argv[13].strip():
        data["story_pack"] = normalize_story_pack_id(sys.argv[13])
    if len(sys.argv) > 14 and sys.argv[14].strip() and data.get("tracker") == "github":
        data["ticket_repo"] = sys.argv[14].strip()
    if draft_profile is not None and "mining" not in draft_profile:
        draft_profile["mining"] = data.get("mining_recommendation") or mining_recommendation(Path(data["target_path"]))
    if isinstance(draft_profile, dict):
        kitsoki = draft_profile.get("kitsoki")
        if isinstance(kitsoki, dict) and isinstance(kitsoki.get("story_pack"), str):
            data["story_pack"] = normalize_story_pack_id(kitsoki["story_pack"])
        if isinstance(kitsoki, dict) and isinstance(kitsoki.get("enabled_stories"), list):
            data["starter_stories"] = normalize_starter_stories(kitsoki["enabled_stories"])
        onboarding = draft_profile.get("onboarding")
        if isinstance(onboarding, dict) and isinstance(onboarding.get("story_pack"), str):
            data["story_pack"] = normalize_story_pack_id(onboarding["story_pack"])
        if isinstance(onboarding, dict) and isinstance(onboarding.get("starter_stories"), list):
            data["starter_stories"] = normalize_starter_stories(onboarding["starter_stories"])
        dev_story_profile = draft_profile.get("dev_story_profile")
        docs_profile = dev_story_profile.get("docs") if isinstance(dev_story_profile, dict) else None
        if isinstance(docs_profile, dict):
            data["dev_story_docs_profile"] = docs_profile
        ensure_draft_profile_defaults(draft_profile, data)
    makefile = root / "Makefile"
    if makefile.exists() and not data.get("check_command"):
        try:
            if re.search(r"^check\s*:", makefile.read_text(encoding="utf-8"), re.MULTILINE):
                data["check_command"] = "make check"
        except OSError:
            data["check_command"] = ""
    profile_content = profile_yaml_from_draft(draft_profile) if draft_profile is not None else profile_yaml(data)
    profile_validation = validate_profile_yaml(profile_content, root)
    if not profile_validation["ok"]:
        print(json.dumps({
            "status": "profile-validation-failed",
            "target_path": str(root),
            "profile_validation": profile_validation,
        }, sort_keys=True))
        return 1
    writes: list[str] = []
    docs = dev_story_docs_profile(data)
    dirs = [
        ".kitsoki",
        ".kitsoki/stories",
        ".context",
        docs["publish_durable_path"],
        docs["design_durable_path"],
        ".artifacts",
        ".worktrees",
        f".kitsoki/stories/{data['project_id']}-dev",
    ]
    for rel in dirs:
        (root / rel).mkdir(parents=True, exist_ok=True)

    config_path = root / ".kitsoki.yaml"
    profile_path = root / ".kitsoki" / "project-profile.yaml"
    instance_path = root / ".kitsoki" / "stories" / f"{data['project_id']}-dev" / "app.yaml"
    readme_path = root / ".kitsoki" / "stories" / f"{data['project_id']}-dev" / "README.md"
    readiness_path = root / ".kitsoki" / "check-readiness.py"
    mining_promote_path = root / ".kitsoki" / "promote-session-mining.py"
    gitignore_path = root / ".gitignore"

    write_text(config_path, config_yaml(data), writes)
    write_text(profile_path, profile_content, writes)
    write_text(instance_path, app_yaml(data), writes)
    write_text(readme_path, readme(data, str(profile_path)), writes)
    write_text(readiness_path, readiness_script(data), writes)
    write_text(mining_promote_path, mining_promotion_script(), writes)
    try:
        readiness_path.chmod(0o755)
        mining_promote_path.chmod(0o755)
    except OSError:
        pass
    mining_seed_note = ""
    if mining_seed_enabled(data):
        mining_seed_note = str(root / mining_seed_path())
        write_text(root / mining_seed_path(), mining_seed_markdown(data), writes)
    append_gitignore(gitignore_path, writes)

    print(json.dumps({
        "status": "applied",
        "config_path": str(config_path),
        "profile_path": str(profile_path),
        "instance_path": str(instance_path),
        "readiness_path": str(readiness_path),
        "mining_promote_path": str(mining_promote_path),
        "gitignore_path": str(gitignore_path),
        "mining_seed_path": mining_seed_note,
        "dirs_created": [str(root / rel) for rel in dirs],
        "profile_validation": profile_validation,
        "writes": writes,
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
