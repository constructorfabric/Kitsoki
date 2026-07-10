#!/usr/bin/env python3
"""No-LLM regression tests for the deterministic onboarding discovery script.

Guards two product-journey QA findings:
  * a Go checkout must get real `go build`/`go test` commands instead of
    "(not yet inferred)" — the deterministic profile should never be
    command-less for a stack with canonical commands;
  * `parse_target` must resolve an explicit target path (the `go_init`
    `target` slot vector) so onboarding can point at an external repo
    deterministically, not just the current checkout.

Pure stdlib, runs against throwaway temp dirs — never a live LLM.
"""

from __future__ import annotations

import importlib.util
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("init_discover", HERE / "init_discover.py")
mod = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
SPEC.loader.exec_module(mod)

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def _mkrepo(files: dict[str, str]) -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-discover-"))
    for rel, body in files.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(body, encoding="utf-8")
    return root


def _git(repo: Path, *args: str) -> None:
    subprocess.run(["git", "-C", str(repo), *args], check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)


# 1. Go repo, no Makefile → canonical go commands, no dev server.
go_repo = _mkrepo({"go.mod": "module acme\ngo 1.22\n"})
prof = mod.discover(go_repo)
check("go stack", prof["stack"], "go project")
check("go build", prof["build_command"], "go build ./...")
check("go test", prof["test_command"], "go test ./...")
check("go dev (none)", prof["dev_command"], "")
check("non-git vcs", prof["repo_vcs"], "none")
check("non-git branch", prof["repo_default_branch"], "")
check("non-git remote", prof["repo_remote"], "")
check("go story pack", prof["story_pack"], "core-engineering")
check("go story pack title", prof["story_pack_title"], "Core engineering starter")

# 2. Go repo WITH a Makefile → make targets win over the go defaults.
go_make = _mkrepo({"go.mod": "module acme\ngo 1.22\n", "Makefile": "build:\n\t:\ntest:\n\t:\n"})
prof = mod.discover(go_make)
check("go+make build", prof["build_command"], "make build")
check("go+make test", prof["test_command"], "make test")

# 3. Bare-path target (the go_init `target` slot) resolves to that path.
base = _mkrepo({})
other = _mkrepo({"go.mod": "module other\ngo 1.22\n"})
resolved = mod.parse_target(str(other), "", str(base))
check("target slot path", resolved, other.resolve())

# 4. Empty request falls back to the current checkout (unchanged behavior).
fallback = mod.parse_target("", "", str(base))
check("empty target fallback", fallback, base.resolve())

# 5. Natural-language onboard request still extracts its path.
nl = mod.parse_target(f"onboard {other}", "", str(base))
check("onboard <path>", nl, other.resolve())
focus = mod.parse_story_focus(f"onboard {other} --stories pr-refinement,bugfixing,setup,gitops")
check("story focus explicit", [item["id"] for item in focus], ["pr-refinement", "bugfix", "setup", "git-ops"])
default_focus = mod.parse_story_focus("")
check("story focus default", [item["id"] for item in default_focus], ["setup", "bugfix", "repo-bakeoff", "pr-refinement", "git-ops"])
check("story pack default", mod.parse_story_pack(""), "core-engineering")
check("story pack alias", mod.parse_story_pack("onboard . --pack core"), "core-setup")
check("focused story pack alias", mod.parse_story_pack("onboard . --pack focused-engineering"), "core-engineering")
pack_focus = mod.parse_story_focus("onboard . --pack core")
check("story pack focus", [item["id"] for item in pack_focus], ["setup", "git-ops"])

# 6. Invalid targets are not treated as generic projects.
missing = base / "does-not-exist"
prof = mod.discover(missing)
check("missing target id", prof["project_id"], "")
check("missing target error", prof["error"], "target path does not exist")
check("missing target story pack", prof["story_pack"], "core-engineering")
file_target = base / "README.md"
file_target.write_text("# not a directory\n", encoding="utf-8")
prof = mod.discover(file_target)
check("file target id", prof["project_id"], "")
check("file target error", prof["error"], "target path is not a directory")

# 7. Python repos are first-class normal projects, not generic commandless
# checkouts.
py_repo = _mkrepo({
    "pyproject.toml": '[project]\nname = "acme-py"\ndependencies = ["pytest", "fastapi"]\n',
    "tests/test_smoke.py": "def test_smoke():\n    assert True\n",
})
prof = mod.discover(py_repo)
check("python id", prof["project_id"], "acme-py")
check("python stack", prof["stack"], "python/fastapi project")
check("python test", prof["test_command"], "python -m pytest")
check("python dev", prof["dev_command"], "uvicorn app:app --reload")
check("python build (none)", prof["build_command"], "")

# 8. Node repos honor the selected package manager instead of assuming npm.
pnpm_repo = _mkrepo({
    "package.json": '{"name":"acme-web","packageManager":"pnpm@9.12.0","scripts":{"dev":"vite","test":"vitest","build":"vite build"},"dependencies":{"vite":"latest"}}\n',
    "pnpm-lock.yaml": "lockfileVersion: '9.0'\n",
})
prof = mod.discover(pnpm_repo)
check("pnpm id", prof["project_id"], "acme-web")
check("pnpm manager", prof["node_package_manager"], "pnpm")
check("pnpm dev", prof["dev_command"], "pnpm run dev")
check("pnpm test", prof["test_command"], "pnpm test")
check("pnpm build", prof["build_command"], "pnpm run build")

# 9. Git metadata is inferred from the local checkout without network access.
git_repo = _mkrepo({"go.mod": "module branchy\ngo 1.22\n"})
subprocess.run(["git", "-C", str(git_repo), "init", "--quiet", "--initial-branch=trunk"], check=True)
_git(git_repo, "remote", "add", "origin", "git@github.com:example/branchy.git")
prof = mod.discover(git_repo)
check("git vcs", prof["repo_vcs"], "git")
check("git branch", prof["repo_default_branch"], "trunk")
check("git remote", prof["repo_remote"], "git@github.com:example/branchy.git")
# GitHub remote ⇒ the tracker classifies as github and the owner/repo slug
# rides discovery so apply can pin host.gh.ticket (external ticket-repo
# passthrough — WS-A A2).
check("git tracker", prof["tracker"], "github")
check("git ticket_repo", prof["ticket_repo"], "example/branchy")

# 9b. github_repo_slug covers the common transports; non-GitHub remotes stay "".
for remote, want in [
    ("https://github.com/acme/gears-rust.git", "acme/gears-rust"),
    ("https://github.com/acme/gears-rust", "acme/gears-rust"),
    ("git@github.com:acme/gears-rust.git", "acme/gears-rust"),
    ("ssh://git@github.com/acme/gears-rust", "acme/gears-rust"),
    ("https://gitlab.com/acme/gears-rust.git", ""),
    ("https://github.com.evil.example/acme/gears-rust.git", ""),
    ("", ""),
]:
    check(f"slug {remote!r}", mod.github_repo_slug(remote), want)

# 9c. A non-GitHub origin keeps tracker=none / ticket_repo="" (local tickets).
gl_repo = _mkrepo({"go.mod": "module gl\ngo 1.22\n"})
subprocess.run(["git", "-C", str(gl_repo), "init", "--quiet", "--initial-branch=main"], check=True)
_git(gl_repo, "remote", "add", "origin", "https://gitlab.com/example/gl.git")
prof = mod.discover(gl_repo)
check("gitlab tracker", prof["tracker"], "none")
check("gitlab ticket_repo", prof["ticket_repo"], "")

# 9d. A child project under a parent meta-repo inherits the parent's private
# ticket provider metadata without treating it as GitHub ticket_repo state.
meta_repo = _mkrepo({
    ".kitsoki/project-profile.yaml": """schema: project-profile/v1
tracker:
  provider: meta-jira
  repo: PLATFORM
  setup_command: ./scripts/dev-env jira
  readiness_command: ./scripts/dev-env jira --check
  required_env: [JIRA_URL, JIRA_USERNAME, JIRA_API_TOKEN]
kitsoki:
  instance:
    bindings:
      ticket: .kitsoki/providers/meta_jira_ticket.star
""",
    ".kitsoki/providers/meta_jira_ticket.star": "def main(ctx):\n    return {'tickets': []}\n",
    "src/platform-presentation/go.mod": "module platform-presentation\ngo 1.22\n",
})
child_project = meta_repo / "src" / "platform-presentation"
prof = mod.discover(child_project)
check("meta tracker", prof["tracker"], "meta-jira")
check("meta ticket_repo stays empty", prof["ticket_repo"], "")
check("meta inherited flag", prof["ticket_provider_inherited"], True)
check("meta parent profile", prof["ticket_provider_parent_profile"], "../../.kitsoki/project-profile.yaml")
check("meta parent root", prof["ticket_provider_parent_root"], "../..")
check("meta binding", prof["ticket_binding"], "../../.kitsoki/providers/meta_jira_ticket.star")
check("meta setup metadata", prof["ticket_provider_metadata"]["setup_command"], "./scripts/dev-env jira")
check("meta required env", prof["ticket_provider_metadata"]["required_env"], ["JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN"])

# 10. Associated Claude/Codex transcript history is detected without running
# the mining pipeline or touching the real home directory.
home = _mkrepo({})
repo_with_history = _mkrepo({"go.mod": "module hist\ngo 1.22\n"})
old_home = os.environ.get("KITSOKI_INIT_HOME")
os.environ["KITSOKI_INIT_HOME"] = str(home)
try:
    slug = mod.transcript_slug(repo_with_history.resolve())
    claude_dir = home / ".claude" / "projects" / slug
    claude_dir.mkdir(parents=True)
    (claude_dir / "claude-session.jsonl").write_text('{"type":"user","entrypoint":"cli"}\n', encoding="utf-8")
    codex_dir = home / ".codex" / "sessions" / "2026" / "07" / "03"
    codex_dir.mkdir(parents=True)
    (codex_dir / "codex-session.jsonl").write_text(
        '{"cwd":"' + str(repo_with_history.resolve()) + '","type":"turn_context"}\n',
        encoding="utf-8",
    )
    prof = mod.discover(repo_with_history.resolve())
finally:
    if old_home is None:
        os.environ.pop("KITSOKI_INIT_HOME", None)
    else:
        os.environ["KITSOKI_INIT_HOME"] = old_home

check("transcript count", prof["transcript_count"], 2)
check("mining status", prof["mining_recommendation"]["status"], "transcripts-found")
check("mining sample", prof["mining_recommendation"]["sample"], "recency")
check("mining first pass", prof["mining_recommendation"]["first_pass_sample"], 2)
check("transcript source backends", [s["backend"] for s in prof["transcript_sources"]], ["claude-code", "codex"])

if failures:
    print("FAIL: init_discover regression")
    for f in failures:
        print("  -", f)
    sys.exit(1)
print("PASS: init_discover regression (go defaults + target slot)")
