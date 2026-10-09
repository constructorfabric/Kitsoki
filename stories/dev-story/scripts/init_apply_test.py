#!/usr/bin/env python3
"""No-LLM regression tests for deterministic onboarding apply.

The story flow fixtures stub host.run, so they cannot prove the Python apply
script validates generated profiles before writing files. These tests run the
script against temp repos and fake `kitsoki project-profile validate` binaries.
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "init_apply.py"

failures: list[str] = []


def check(label: str, condition: bool, detail: str = "") -> None:
    if not condition:
        failures.append(f"{label}{': ' + detail if detail else ''}")


def mkrepo() -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-apply-"))
    (root / "go.mod").write_text("module acme\ngo 1.22\n", encoding="utf-8")
    (root / "AGENTS.md").write_text("# Agent rules\n", encoding="utf-8")
    return root


def mkgitrepo() -> Path:
    root = mkrepo()
    subprocess.run(["git", "-C", str(root), "init", "--quiet", "--initial-branch=trunk"], check=True)
    subprocess.run(["git", "-C", str(root), "remote", "add", "origin", "https://github.com/example/acme.git"], check=True)
    return root


def mkmeta_child_repo() -> Path:
    parent = Path(tempfile.mkdtemp(prefix="kitsoki-meta-"))
    profile_dir = parent / ".kitsoki"
    provider_dir = profile_dir / "providers"
    provider_dir.mkdir(parents=True)
    (provider_dir / "meta_jira_ticket.star").write_text("def main(ctx):\n    return {'tickets': []}\n", encoding="utf-8")
    (provider_dir / "meta_jira_ticket.star.yaml").write_text(
        "inputs:\n  op: { type: string, required: false }\noutputs:\n  tickets: { type: list }\n",
        encoding="utf-8",
    )
    (profile_dir / "project-profile.yaml").write_text(
        """schema: project-profile/v1
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
        encoding="utf-8",
    )
    child = parent / "src" / "platform-presentation"
    child.mkdir(parents=True)
    (child / "go.mod").write_text("module platform-presentation\ngo 1.22\n", encoding="utf-8")
    (child / "AGENTS.md").write_text("# Agent rules\n", encoding="utf-8")
    return child


def mkpyrepo() -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-apply-py-"))
    (root / "pyproject.toml").write_text('[project]\nname = "acme-py"\n', encoding="utf-8")
    (root / "tests").mkdir()
    (root / "tests" / "test_smoke.py").write_text("def test_smoke():\n    assert True\n", encoding="utf-8")
    return root


def mknoderepo() -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-apply-node-"))
    (root / "package.json").write_text(
        '{"name":"acme-web","packageManager":"pnpm@9.12.0","scripts":{"dev":"vite","test":"vitest","build":"vite build"}}\n',
        encoding="utf-8",
    )
    (root / "pnpm-lock.yaml").write_text("lockfileVersion: '9.0'\n", encoding="utf-8")
    return root


def fake_kitsoki(ok: bool) -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-bin-"))
    path = root / "kitsoki"
    payload = {"ok": ok}
    if not ok:
        payload["schema"] = ["forced invalid profile"]
    validation_guard = ""
    if ok:
        validation_guard = (
            "    last=\n"
            "    for arg do last=$arg; done\n"
            "    if [ -n \"$last\" ] && grep -q '^[[:space:]]*capability:' \"$last\"; then\n"
            "      printf '%s\\n' '{\"ok\":false,\"schema\":[\"forbidden onboarding capability\"],\"semantic\":[],\"warnings\":[]}'\n"
            "      exit 1\n"
            "    fi\n"
        )
    path.write_text(
        "#!/bin/sh\n"
        "case \"$*\" in\n"
        "  *'project-profile validate'*)\n"
        f"{validation_guard}"
        f"    printf '%s\\n' {json.dumps(json.dumps(payload))}\n"
        f"    exit {0 if ok else 1}\n"
        "    ;;\n"
        "  *) echo unexpected command: \"$*\" >&2; exit 2 ;;\n"
        "esac\n",
        encoding="utf-8",
    )
    path.chmod(0o755)
    return path


def run_apply(repo: Path, validator: Path) -> subprocess.CompletedProcess[str]:
    return run_apply_with(repo, validator, "acme", "Acme", "go project", "", "go test ./...", "go build ./...")


def run_apply_with(
    repo: Path,
    validator: Path,
    project_id: str,
    title: str,
    stack: str,
    dev: str,
    test: str,
    build: str,
    draft: dict | None = None,
    mining: dict | None = None,
    tracker: str = "none",
    starter: list | None = None,
) -> subprocess.CompletedProcess[str]:
    env = os.environ.copy()
    env["KITSOKI_BIN"] = str(validator)
    args = [
        sys.executable,
        str(SCRIPT),
        str(repo),
        project_id,
        title,
        stack,
        dev,
        test,
        build,
        "local defaults",
        tracker,
    ]
    if draft is not None:
        args.append(json.dumps(draft))
    if mining is not None:
        if draft is None:
            args.append("")
        args.append(json.dumps(mining))
    if starter is not None:
        if draft is None:
            args.append("")
        if mining is None:
            args.append("")
        args.append(json.dumps(starter))
    return subprocess.run(
        args,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )


# 1. Validation failure is loud and happens before onboarding files are written.
repo = mkrepo()
proc = run_apply(repo, fake_kitsoki(False))
check("invalid exit", proc.returncode != 0, proc.stdout + proc.stderr)
check("invalid no config write", not (repo / ".kitsoki.yaml").exists())
check("invalid no profile write", not (repo / ".kitsoki" / "project-profile.yaml").exists())
try:
    invalid_report = json.loads(proc.stdout)
except json.JSONDecodeError as err:
    failures.append(f"invalid json: {err}: {proc.stdout!r}")
else:
    check("invalid status", invalid_report.get("status") == "profile-validation-failed", str(invalid_report))
    check("invalid carries diagnostics", invalid_report.get("profile_validation", {}).get("schema") == ["forced invalid profile"])

# 2. Invalid targets are refused before validation and are not created by apply.
missing_parent = Path(tempfile.mkdtemp(prefix="kitsoki-apply-missing-parent-"))
missing_repo = missing_parent / "missing-project"
proc = run_apply(missing_repo, fake_kitsoki(True))
check("missing target exit", proc.returncode != 0, proc.stdout + proc.stderr)
check("missing target not created", not missing_repo.exists())
try:
    missing_report = json.loads(proc.stdout)
except json.JSONDecodeError as err:
    failures.append(f"missing target json: {err}: {proc.stdout!r}")
else:
    check("missing target status", missing_report.get("status") == "target-invalid", str(missing_report))

# 3. Validation success writes files and carries the validation report.
repo = mkrepo()
proc = run_apply(repo, fake_kitsoki(True))
check("valid exit", proc.returncode == 0, proc.stdout + proc.stderr)
check("valid config write", (repo / ".kitsoki.yaml").exists())
config_text = (repo / ".kitsoki.yaml").read_text(encoding="utf-8") if (repo / ".kitsoki.yaml").exists() else ""
check("valid config project profile", "project_profile: .kitsoki/project-profile.yaml" in config_text)
check("valid config root import", "root:\n  import: dev-story" in config_text)
check("valid config no stale default story", "default_story:" not in config_text)
check("valid config no mining without transcripts", "\nmining:" not in config_text)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
check("valid profile write", profile_path.exists())
try:
    valid_report = json.loads(proc.stdout)
except json.JSONDecodeError as err:
    failures.append(f"valid json: {err}: {proc.stdout!r}")
else:
    check("valid status", valid_report.get("status") == "applied", str(valid_report))
    check("valid profile validation", valid_report.get("profile_validation", {}).get("ok") is True)
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("valid setup plan present", "setup_plan:" in profile_text)
    check("valid non-git vcs", "vcs: none" in profile_text)
    check("valid non-git branch empty", "default_branch: \"\"" in profile_text)
    check("valid doc profile present", "dev_story_profile:" in profile_text)
    check("valid prd local path", "publish_durable_path: \".context/prd\"" in profile_text)
    check("valid design no template", "design_template_dir: \"\"" in profile_text)
    check("valid design local path", "design_durable_path: \".context/designs\"" in profile_text)
    check("valid setup writes instance", ".kitsoki/stories/acme-dev/app.yaml" in profile_text)
    check("valid setup writes readiness verifier", ".kitsoki/check-readiness.py" in profile_text)
    check("valid setup writes mining promotion helper", ".kitsoki/promote-session-mining.py" in profile_text)
    check("valid setup creates prd dir", "- \".context/prd\"" in profile_text)
    check("valid setup creates design dir", "- \".context/designs\"" in profile_text)
    check("valid setup gates build", "command: \"go build ./...\"" in profile_text)
    check("valid setup gates tests", "command: \"go test ./...\"" in profile_text)
    check("valid onboarding base story", "base_story: \"dev-story\"" in profile_text)
    check("valid story pack", "story_pack: \"core-engineering\"" in profile_text)
    check("valid story pack title", "story_pack_title: \"Core engineering starter\"" in profile_text)
    check("valid enabled stories present", "enabled_stories:" in profile_text)
    check("valid enabled setup", "- \"setup\"" in profile_text)
    check("valid enabled bugfix", "- \"bugfix\"" in profile_text)
    check("valid enabled repo-bakeoff", "- \"repo-bakeoff\"" in profile_text)
    check("valid enabled pr-refinement", "- \"pr-refinement\"" in profile_text)
    check("valid enabled git-ops", "- \"git-ops\"" in profile_text)
    check("valid onboarding starter stories", "starter_stories:" in profile_text)
    check("valid onboarding expansion policy", "expansion_policy:" in profile_text)
    check("valid onboarding repo patterns", "repo_patterns:" in profile_text)
    check("valid onboarding customizations", "story_customizations:" in profile_text)
    check("valid onboarding toolchain customization", "id: \"toolchain-gates\"" in profile_text)
    check("valid onboarding omits invalid capability", "capability:" not in profile_text)
    check("valid local ticket customization", "id: \"local-ticket-intake\"" in profile_text)
    check("valid ticket intake customization", "id: \"ticket-intake\"" in profile_text)
    check("valid starter focus customization", "id: \"starter-story-focus\"" in profile_text)
    check("valid rules files", "rules_files:\n    - \"AGENTS.md\"" in profile_text)
    check("valid repo rules evidence", "id: \"repo-rules\"" in profile_text)
app_path = repo / ".kitsoki" / "stories" / "acme-dev" / "app.yaml"
if app_path.exists():
    app_text = app_path.read_text(encoding="utf-8")
    check("valid app fallback routing", "free_form_fallback:\n    state: core.landing\n    intent: core__landing_capture" in app_text)
    check("valid app composite host declared", "- host.local_github.ticket" in app_text)
    check("valid app codeact host declared", "- host.agent.codeact" in app_text)
    check("valid app prd local path", 'publish_durable_path:       { type: string, default: ".context/prd" }' in app_text)
    check("valid app design no template", 'design_template_dir:        { type: string, default: "" }' in app_text)
    check("valid app design local path", 'design_durable_path:        { type: string, default: ".context/designs" }' in app_text)
    check("valid app github source empty", 'ticket_github_repo:         { type: string, default: "" }' in app_text)
    check("valid app slidey render host declared", "- host.slidey.render" in app_text)
readme_path = repo / ".kitsoki" / "stories" / "acme-dev" / "README.md"
if readme_path.exists():
    readme_text = readme_path.read_text(encoding="utf-8")
    check("valid readme no arg run", "kitsoki run\n```" in readme_text)
    check("valid readme explicit wrapper optional", "Use the materialized wrapper explicitly only after editing it" in readme_text)
    check("valid readme starter pack", "Starter story pack: `core-engineering`" in readme_text)
    check("valid readme local ticket provider", "- source: local files" in readme_text)
    check("valid readme story pack command", "kitsoki project-profile story-packs add <pack>" in readme_text)
    check("valid readme git-ops", "`git-ops`" in readme_text)
    check("valid readme readiness command", "python3 .kitsoki/check-readiness.py --json" in readme_text)
    check("valid readme mining promote command", "python3 .kitsoki/promote-session-mining.py --dry-run" in readme_text)
readiness_path = repo / ".kitsoki" / "check-readiness.py"
check("valid readiness verifier write", readiness_path.exists())
if readiness_path.exists():
    check("valid readiness executable", os.access(readiness_path, os.X_OK))
    list_proc = subprocess.run(
        [sys.executable, str(readiness_path), "--list"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid readiness list exit", list_proc.returncode == 0, list_proc.stdout + list_proc.stderr)
    check("valid readiness list has story", '"id": "story-load"' in list_proc.stdout)
    check("valid readiness list has tests", '"command": "go test ./..."' in list_proc.stdout)
    update_proc = subprocess.run(
        [sys.executable, str(readiness_path), "--json", "--update-profile", "--timeout", "5"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid readiness update exits red on failed required check", update_proc.returncode != 0)
    updated_profile = profile_path.read_text(encoding="utf-8") if profile_path.exists() else ""
    check("valid readiness profile status updated", "readiness:\n  status: \"fail\"" in updated_profile)
    check("valid readiness profile check recorded", 'id: "story-load"' in updated_profile)
    check("valid readiness profile detail recorded", "detail:" in updated_profile)
promote_path = repo / ".kitsoki" / "promote-session-mining.py"
check("valid mining promotion helper write", promote_path.exists())
if promote_path.exists():
    check("valid mining promotion executable", os.access(promote_path, os.X_OK))
    analysis_dir = repo / ".artifacts" / "mining" / "jobs" / "seed"
    analysis_dir.mkdir(parents=True)
    (analysis_dir / "analysis.json").write_text(json.dumps({
        "instances": [{
            "instance_id": "session-1#003",
            "determinism": "deterministic",
            "tags": {"action": ["run tests", "open diff"]},
            "grounding": {"quarantined": False},
        }]
    }), encoding="utf-8")
    promote_proc = subprocess.run(
        [sys.executable, str(promote_path), "--json"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid mining promotion exit", promote_proc.returncode == 0, promote_proc.stdout + promote_proc.stderr)
    promoted_profile = profile_path.read_text(encoding="utf-8") if profile_path.exists() else ""
    check("valid mining promotion pending id", 'id: "mined-session-1-003"' in promoted_profile)
    check("valid mining promotion pending status", 'status: "pending"' in promoted_profile)
    check("valid mining promotion evidence", "analysis.json#session-1#003" in promoted_profile)
    accept_proc = subprocess.run(
        [sys.executable, str(promote_path), "--accept-pending", "--json"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid mining acceptance exit", accept_proc.returncode == 0, accept_proc.stdout + accept_proc.stderr)
    accepted_profile = profile_path.read_text(encoding="utf-8") if profile_path.exists() else ""
    check("valid mining acceptance status", 'status: "accepted"' in accepted_profile)
    check("valid mining acceptance json count", '"accepted":' in accept_proc.stdout)
    # Add a second generated pending entry to prove the refinement path records operator feedback.
    (analysis_dir / "analysis.json").write_text(json.dumps({
        "instances": [{
            "instance_id": "session-1#004",
            "determinism": "deterministic",
            "tags": {"action": ["add focused tests"]},
            "grounding": {"quarantined": False},
        }]
    }), encoding="utf-8")
    promote_proc = subprocess.run(
        [sys.executable, str(promote_path), "--json"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid mining duplicate promote exit", promote_proc.returncode == 0, promote_proc.stdout + promote_proc.stderr)
    refine_proc = subprocess.run(
        [sys.executable, str(promote_path), "--refine-pending", "needs a project-specific test command", "--json"],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=repo,
    )
    check("valid mining refinement exit", refine_proc.returncode == 0, refine_proc.stdout + refine_proc.stderr)
    refined_profile = profile_path.read_text(encoding="utf-8") if profile_path.exists() else ""
    check("valid mining refinement status", 'status: "needs-refinement"' in refined_profile)
    check("valid mining refinement feedback", 'review_feedback: "needs a project-specific test command"' in refined_profile)

# 4. Git metadata is preserved instead of assuming main/no remote.
repo = mkgitrepo()
proc = run_apply(repo, fake_kitsoki(True))
check("git metadata exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("git metadata vcs", "vcs: git" in profile_text)
    check("git metadata branch", "default_branch: \"trunk\"" in profile_text)
    check("git metadata remote", "remote: \"https://github.com/example/acme.git\"" in profile_text)

# 5. Python projects keep Python stack metadata and pytest verification.
repo = mkpyrepo()
proc = run_apply_with(repo, fake_kitsoki(True), "acme-py", "Acme Py", "python/fastapi project", "uvicorn app:app --reload", "python -m pytest", "")
check("python valid exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("python stack kind", "kind: \"python\"" in profile_text)
    check("python language", "languages: [python]" in profile_text)
    check("python package manager", "package_managers: [pyproject]" in profile_text)
    check("python setup gates tests", "command: \"python -m pytest\"" in profile_text)
    check("python setup gates dev advisory", "command: \"uvicorn app:app --reload\"" in profile_text)

# 6. Node projects keep their selected package manager instead of defaulting to npm.
repo = mknoderepo()
proc = run_apply_with(repo, fake_kitsoki(True), "acme-web", "Acme Web", "node/vite project", "pnpm run dev", "pnpm test", "pnpm run build")
check("node valid exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("node stack kind", "kind: \"node\"" in profile_text)
    check("node package manager", "package_managers: [pnpm]" in profile_text)
    check("node setup gates tests", "command: \"pnpm test\"" in profile_text)
    check("node setup gates build", "command: \"pnpm run build\"" in profile_text)

# 7. Associated transcripts create a durable seed-mining handoff without running
# the mining pipeline.
repo = mkrepo()
mining = {
    "status": "transcripts-found",
    "sample": "recency",
    "first_pass_sample": 2,
    "transcript_count": 2,
    "sources": [
        {"backend": "claude-code", "dir": "/home/u/.claude/projects/acme", "sessions": 1, "include": "human"},
        {"backend": "codex", "dir": "/home/u/.codex/sessions", "sessions": 1, "include": "human"},
    ],
    "note": "Seed project customization from recent associated transcripts after operator consent.",
}
proc = run_apply_with(repo, fake_kitsoki(True), "acme", "Acme", "go project", "", "go test ./...", "go build ./...", mining=mining)
check("mining seed exit", proc.returncode == 0, proc.stdout + proc.stderr)
try:
    mining_report = json.loads(proc.stdout)
except json.JSONDecodeError as err:
    failures.append(f"mining json: {err}: {proc.stdout!r}")
else:
    check("mining seed path reported", mining_report.get("mining_seed_path", "").endswith(".context/kitsoki-session-mining-seed.md"))
seed_path = repo / ".context" / "kitsoki-session-mining-seed.md"
check("mining seed note written", seed_path.exists())
config_path = repo / ".kitsoki.yaml"
if config_path.exists():
    config_text = config_path.read_text(encoding="utf-8")
    check("mining config stays disabled", "enabled: false" in config_text)
    check("mining config cadence", "cadence: \"30s\"" in config_text)
    check("mining config sample", "first_pass_sample: 2" in config_text)
    check("mining config claude scope", "- \"/home/u/.claude/projects/acme\"" in config_text)
    check("mining config codex scope", "- \"/home/u/.codex/sessions\"" in config_text)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("mining profile seed job", "job: \"seed-pending-operator-review\"" in profile_text)
    check("mining profile review path", "Seed review note: `.context/kitsoki-session-mining-seed.md`" in profile_text)
    check("mining setup write", "path: \".context/kitsoki-session-mining-seed.md\"" in profile_text)
if seed_path.exists():
    seed_text = seed_path.read_text(encoding="utf-8")
    check("mining seed mentions no cost", "no LLM cost" in seed_text)
    check("mining seed lists codex", "codex: 1 sessions" in seed_text)
    check("mining seed mentions disabled runtime config", "mining.enabled: false" in seed_text)
    check("mining seed mentions opt in", "/mine resume" in seed_text)

# 8. Sparse LLM-drafted profiles get the generated instance defaults injected
# before validation and write.
repo = mkrepo()
draft = {
    "schema": "project-profile/v1",
    "id": "acme",
    "title": "Acme",
    "repo": {"root": ".", "vcs": "git"},
    "stack": {"kind": "go", "languages": ["go"]},
    "commands": {"test": "go test ./...", "build": "go build ./..."},
    "kitsoki": {
        "story": "dev-story",
        "instance": {
            "id": "acme-dev",
            "path": ".kitsoki/stories/acme-dev/app.yaml",
            "bindings": {
                "ticket": "host.local_files.ticket",
                "vcs": "host.git",
                "ci": "host.local",
                "workspace": "host.git_worktree",
                "transport": "host.append_to_file",
            },
        },
    },
}
proc = run_apply_with(repo, fake_kitsoki(True), "acme", "Acme", "go project", "", "go test ./...", "go build ./...", draft=draft)
check("draft defaults exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("draft doc defaults injected", "dev_story_profile:" in profile_text)
    check("draft prd local path", "publish_durable_path: \".context/prd\"" in profile_text)
    check("draft design local path", "design_durable_path: \".context/designs\"" in profile_text)
    check("draft bugfix build", "build_cmd: \"go build ./...\"" in profile_text)
    check("draft bugfix test", "test_cmd: \"go test ./...\"" in profile_text)
    check("draft onboarding defaults injected", "base_story: \"dev-story\"" in profile_text)
    check("draft enabled stories injected", "enabled_stories:" in profile_text)
    check("draft starter stories injected", "starter_stories:" in profile_text)
    check("draft onboarding customizations injected", "story_customizations:" in profile_text)

# 8b. Explicit starter-story focus is preserved and normalized by apply.
repo = mkrepo()
proc = run_apply_with(
    repo,
    fake_kitsoki(True),
    "acme",
    "Acme",
    "go project",
    "",
    "go test ./...",
    "go build ./...",
    starter=[{"id": "bugfixing"}, {"id": "gitops"}],
)
check("custom starter exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("custom starter pack", "story_pack: \"custom\"" in profile_text)
    check("custom starter bugfix", "- \"bugfix\"" in profile_text)
    check("custom starter gitops", "- \"git-ops\"" in profile_text)
    check("custom starter omits setup", "- \"setup\"" not in profile_text)

# 9. GitHub tracker ⇒ combined local+GitHub ticket intake: the generated
# instance binds iface.ticket → host.local_github.ticket, searches the slug
# derived from the origin remote, and keeps active ticket_repo empty until a
# GitHub row is picked.
repo = mkgitrepo()
proc = run_apply_with(repo, fake_kitsoki(True), "acme", "Acme", "go project", "", "go test ./...", "go build ./...", tracker="github")
check("gh tracker exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("gh tracker provider", "provider: \"github\"" in profile_text)
    check("gh tracker repo slug", "repo: \"example/acme\"" in profile_text)
    check("gh tracker binding", "ticket: host.local_github.ticket" in profile_text)
    check("gh tracker docs ticket_repo opt-in empty", "ticket_repo: \"\"" in profile_text)
    check("gh tracker ticket intake", "id: \"ticket-intake\"" in profile_text)
    check("gh tracker customization", "id: \"github-ticket-source\"" in profile_text)
    check("gh tracker omits invalid capability", "capability:" not in profile_text)
app_path = repo / ".kitsoki" / "stories" / "acme-dev" / "app.yaml"
check("gh tracker instance write", app_path.exists())
if app_path.exists():
    app_text = app_path.read_text(encoding="utf-8")
    check("gh tracker instance binding", "ticket:    host.local_github.ticket" in app_text)
    check("gh tracker active repo empty", 'ticket_repo:                { type: string, default: "" }' in app_text)
    check("gh tracker github source pin", 'ticket_github_repo:         { type: string, default: "example/acme" }' in app_text)
    check("gh tracker instance hosts", "- host.gh.ticket.transition" in app_text)
    check("gh tracker instance local github host", "- host.local_github.ticket" in app_text)
    check("gh tracker slidey render host declared", "- host.slidey.render" in app_text)
    check("gh tracker current host surface", "- host.agent.codeact" in app_text and "- host.chat.transcript" in app_text and "- host.ide.get_diagnostics" in app_text)
readme_path = repo / ".kitsoki" / "stories" / "acme-dev" / "README.md"
if readme_path.exists():
    readme_text = readme_path.read_text(encoding="utf-8")
    check("gh tracker readme provider", "- source: GitHub Issues `example/acme`" in readme_text)

# 9b. GitHub tracker WITHOUT a parseable GitHub remote degrades honestly to
# local-file tickets (no guessed slug, no gh binding).
repo = mkrepo()
proc = run_apply_with(repo, fake_kitsoki(True), "acme", "Acme", "go project", "", "go test ./...", "go build ./...", tracker="github")
check("gh no-remote exit", proc.returncode == 0, proc.stdout + proc.stderr)
app_path = repo / ".kitsoki" / "stories" / "acme-dev" / "app.yaml"
if app_path.exists():
    app_text = app_path.read_text(encoding="utf-8")
    check("gh no-remote local binding", "ticket:    host.local_files.ticket" in app_text)
    check("gh no-remote empty active repo", 'ticket_repo:                { type: string, default: "" }' in app_text)
    check("gh no-remote empty github source", 'ticket_github_repo:         { type: string, default: "" }' in app_text)

# 9c. tracker=none keeps the local binding even when the remote IS GitHub
# (the operator's explicit choice wins over remote evidence).
repo = mkgitrepo()
proc = run_apply(repo, fake_kitsoki(True))
check("none tracker exit", proc.returncode == 0, proc.stdout + proc.stderr)
app_path = repo / ".kitsoki" / "stories" / "acme-dev" / "app.yaml"
if app_path.exists():
    app_text = app_path.read_text(encoding="utf-8")
    check("none tracker local binding", "ticket:    host.local_files.ticket" in app_text)

# 9d. A projects-folder child inherits a parent meta-repo ticket provider. The
# generated child profile carries generic tracker metadata, leaves ticket_repo
# empty, and rewrites the parent-owned script path for the materialized app.
repo = mkmeta_child_repo()
proc = run_apply_with(repo, fake_kitsoki(True), "platform-presentation", "Platform Presentation", "go project", "", "go test ./...", "go build ./...", tracker="meta-jira")
check("meta tracker exit", proc.returncode == 0, proc.stdout + proc.stderr)
profile_path = repo / ".kitsoki" / "project-profile.yaml"
if profile_path.exists():
    profile_text = profile_path.read_text(encoding="utf-8")
    check("meta provider", "provider: \"meta-jira\"" in profile_text)
    check("meta repo", "repo: \"PLATFORM\"" in profile_text)
    check("meta inherited profile", "inherited_from: \"../../.kitsoki/project-profile.yaml\"" in profile_text)
    check("meta inherited root", "inherited_root: \"../..\"" in profile_text)
    check("meta binding profile", "ticket: ../../.kitsoki/providers/meta_jira_ticket.star" in profile_text)
    check("meta docs ticket_repo empty", "ticket_repo: \"\"" in profile_text)
    check("meta customization", "id: \"inherited-ticket-source\"" in profile_text)
    check("meta readiness command", "command: \"cd ../.. && ./scripts/dev-env jira --check\"" in profile_text)
app_path = repo / ".kitsoki" / "stories" / "platform-presentation-dev" / "app.yaml"
check("meta app write", app_path.exists())
if app_path.exists():
    app_text = app_path.read_text(encoding="utf-8")
    check("meta app binding", "ticket:    ../../../../../.kitsoki/providers/meta_jira_ticket.star" in app_text)
    check("meta app empty ticket repo", 'ticket_repo:                { type: string, default: "" }' in app_text)
readiness_path = repo / ".kitsoki" / "check-readiness.py"
if readiness_path.exists():
    readiness_text = readiness_path.read_text(encoding="utf-8")
    check("meta readiness check scripted", '"id": "ticket-provider"' in readiness_text)
    check("meta readiness check cwd", '"command": "cd ../.. && ./scripts/dev-env jira --check"' in readiness_text)

if failures:
    print("FAIL: init_apply regression")
    for failure in failures:
        print("  -", failure)
    sys.exit(1)
print("PASS: init_apply regression (profile validation gates writes)")
