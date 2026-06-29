#!/usr/bin/env python3
"""Apply local project onboarding files for dev-story."""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path


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


def profile_yaml_from_draft(profile: dict) -> str:
    return yaml_dump(profile).rstrip() + "\n"


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


def app_yaml(data: dict) -> str:
    project_id = data["project_id"]
    title = data["project_title"]
    return f"""app:
  id: {project_id}-dev
  version: 0.1.0
  title: {q(project_id + "-dev - work on " + title + " with Kitsoki")}
  author: "Kitsoki"
  license: "CC0"

routing:
  embedding:
    model: "nomic-embed-text-v1.5"

hosts:
  - host.local_files.ticket
  - host.gh.ticket
  - host.git
  - host.local
  - host.git_worktree
  - host.append_to_file
  - host.inbox.add
  - host.agent.ask
  - host.agent.decide
  - host.agent.task
  - host.agent.search
  - host.agent.converse
  - host.chat.resolve
  - host.artifacts_dir
  - host.ide.open_file
  - host.ide.open_diff
  - host.diff.open
  - host.run
  - host.starlark.run

imports:
  core:
    source: "@kitsoki/dev-story"
    entry: landing
    hosts: declared
    host_bindings:
      ticket:    host.local_files.ticket
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
      design_template_dir:        "{{{{ world.design_template_dir }}}}"
      design_durable_path:        "{{{{ world.design_durable_path }}}}"
      design_doc_filename:        "{{{{ world.design_doc_filename }}}}"
      design_ticket_dir:          "{{{{ world.design_ticket_dir }}}}"
      ticket_repo:                "{{{{ world.ticket_repo }}}}"
      # Project toolchain commands. Empty means dev-story/bugfix keeps its
      # default gates; non-Go projects should set these from their profile.
      build_cmd:                  "{{{{ world.build_cmd }}}}"
      test_cmd:                   "{{{{ world.test_cmd }}}}"

world:
  workdir:                    {{ type: string, default: "." }}
  repo_root:                  {{ type: string, default: "." }}
  ticket_repo:                {{ type: string, default: "" }}
  judge_mode:                 {{ type: string, default: "human" }}
  judge_confidence_threshold: {{ type: float, default: 0.8 }}

  publish_durable_path:       {{ type: string, default: "docs/prd" }}
  prd_doc_filename:           {{ type: string, default: "" }}
  design_template_dir:        {{ type: string, default: "docs/proposals/templates" }}
  design_durable_path:        {{ type: string, default: "docs/proposals" }}
  design_doc_filename:        {{ type: string, default: "" }}
  design_ticket_dir:          {{ type: string, default: "" }}
  build_cmd:                  {{ type: string, default: {q(data.get("build_command", ""))} }}
  test_cmd:                   {{ type: string, default: {q(data.get("test_command", ""))} }}

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
    return "generic"


def enrich_project_shape(data: dict, root: Path) -> None:
    data["has_makefile"] = (root / "Makefile").exists()
    cargo = root / "Cargo.toml"
    data["has_cargo"] = cargo.exists()
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
        managers.append("npm")
    if data.get("has_makefile"):
        managers.append("make")
    return "[" + ", ".join(managers) + "]" if managers else "[]"


def generic_profile_yaml(data: dict) -> str:
    kind = stack_kind(data)
    languages = {
        "rust": "[rust]",
        "go": "[go]",
        "node": "[javascript]",
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
  vcs: git
  default_branch: main
  remote: ""
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
  source: {data.get("conventions", "local defaults")}
  dirs:
    context:   {{ path: ".context",   use: local-runtime }}
    artifacts: {{ path: ".artifacts", use: local-runtime }}
    worktrees: {{ path: ".worktrees", use: local-runtime }}
  gitignore:
    manage: true
    additions:
      - ".kitsoki.local.yaml"
      - ".kitsoki/sessions/"
      - ".artifacts/"
      - ".context/"
      - ".worktrees/"

tracker:
  provider: {q(data.get("tracker", "none"))}

kitsoki:
  story: dev-story
  instance:
    id: {data["project_id"]}-dev
    path: .kitsoki/stories/{data["project_id"]}-dev/app.yaml
    bindings:
      ticket: host.local_files.ticket
      vcs: host.git
      ci: host.local
      workspace: host.git_worktree
      transport: host.append_to_file
  judge_mode: human
  autonomy: supervised

readiness:
  status: not-run
"""


def slidey_profile_yaml(data: dict) -> str:
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
  vcs: git
  default_branch: main
  remote: ""
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

mining:
  job: ""
  sample: none
  intent_count: 0
  themes: []

setup_plan:
  writes:
    - path: ".kitsoki/project-profile.yaml"
      action: create
      summary: "Declarative onboarding profile for Slidey."
    - path: ".kitsoki/stories/slidey-dev/app.yaml"
      action: create
      summary: "Materialized dev-story instance for Slidey."
    - path: ".kitsoki.yaml"
      action: create
      summary: "Discover project-local stories under ./.kitsoki/stories."
    - path: ".gitignore"
      action: merge
      summary: "Ignore local Kitsoki runtime/session artifacts."
  dirs_create: [".context", ".artifacts", ".worktrees", ".kitsoki"]
  gitignore_additions:
    - ".kitsoki.local.yaml"
    - ".kitsoki/sessions/"
    - ".artifacts/"
    - ".context"
    - ".worktrees"
  verifications:
    - id: story-load
      kind: story
      command: "kitsoki run .kitsoki/stories/slidey-dev/app.yaml"
      gate: required
    - id: unit-tests
      kind: tests
      command: {q(data.get("test_command", ""))}
      gate: required
    - id: build
      kind: build
      command: {q(data.get("build_command", ""))}
      gate: required
    - id: cli-validate
      kind: golden-path
      command: "node src/index.js examples/hello.slidey.json --validate"
      gate: required
    - id: html-bundle
      kind: artifact
      command: "node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html"
      gate: recommended
    - id: web-player
      kind: ui
      command: {q(data.get("dev_command", ""))}
      gate: recommended

readiness:
  status: not-run
"""


def config_yaml(project_id: str) -> str:
    return f"""story_dirs:
  - ./.kitsoki/stories

default_story: .kitsoki/stories/{project_id}-dev/app.yaml
project_profile: .kitsoki/project-profile.yaml
"""


def readme(data: dict, profile_path: str) -> str:
    title = data["project_title"]
    story_id = f"{data['project_id']}-dev"
    commands = []
    if data.get("dev_command"):
        commands.append(("dev", data["dev_command"]))
    if data.get("test_command"):
        commands.append(("test", data["test_command"]))
    if data.get("build_command"):
        commands.append(("build", data["build_command"]))
    command_block = "\n".join(cmd for _, cmd in commands) or "# No project commands were inferred during onboarding."
    command_notes = "\n".join(f"- `{name}`: `{cmd}`" for name, cmd in commands) or "- No project commands were inferred; update `.kitsoki/project-profile.yaml` and this README after choosing them."
    flow_note = (
        "No deterministic flow fixtures are generated for this project instance yet. "
        "Use the imported dev-story fixtures in the Kitsoki checkout for hub coverage, "
        "and add project-local flows when this repo needs its own story-specific assertions."
    )
    return f"""# {story_id}

Kitsoki dev-story instance for the {title} checkout.

Run from the {title} repo root:

```sh
kitsoki run .kitsoki/stories/{story_id}/app.yaml
```

Or start the browser UI:

```sh
kitsoki web
```

This instance imports `@kitsoki/dev-story` from the Kitsoki binary. The shared
dev-story hub defines the general workflow; this repository owns the local
profile, command defaults, and any project-specific extensions.

Project profile: `{Path(profile_path).relative_to(Path(data["target_path"]))}`

Inferred project commands:

```sh
{command_block}
```

Command map:

{command_notes}

Testing:

{flow_note}
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
    }
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
    root = Path(data["target_path"])
    enrich_project_shape(data, root)
    makefile = root / "Makefile"
    if makefile.exists() and not data.get("check_command"):
        try:
            if re.search(r"^check\s*:", makefile.read_text(encoding="utf-8"), re.MULTILINE):
                data["check_command"] = "make check"
        except OSError:
            data["check_command"] = ""
    writes: list[str] = []
    dirs = [".kitsoki", ".kitsoki/stories", ".context", ".artifacts", ".worktrees", f".kitsoki/stories/{data['project_id']}-dev"]
    for rel in dirs:
        (root / rel).mkdir(parents=True, exist_ok=True)

    config_path = root / ".kitsoki.yaml"
    profile_path = root / ".kitsoki" / "project-profile.yaml"
    instance_path = root / ".kitsoki" / "stories" / f"{data['project_id']}-dev" / "app.yaml"
    readme_path = root / ".kitsoki" / "stories" / f"{data['project_id']}-dev" / "README.md"
    gitignore_path = root / ".gitignore"

    write_text(config_path, config_yaml(data["project_id"]), writes)
    if draft_profile is not None:
        write_text(profile_path, profile_yaml_from_draft(draft_profile), writes)
    else:
        write_text(profile_path, profile_yaml(data), writes)
    write_text(instance_path, app_yaml(data), writes)
    write_text(readme_path, readme(data, str(profile_path)), writes)
    append_gitignore(gitignore_path, writes)

    print(json.dumps({
        "status": "applied",
        "config_path": str(config_path),
        "profile_path": str(profile_path),
        "instance_path": str(instance_path),
        "gitignore_path": str(gitignore_path),
        "dirs_created": [str(root / rel) for rel in dirs],
        "writes": writes,
    }, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
