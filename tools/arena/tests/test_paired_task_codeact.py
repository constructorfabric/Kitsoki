#!/usr/bin/env python3
"""No-LLM tests for paired-task CodeAct treatments."""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
ARENA_ROOT = HERE.parent
REPO_ROOT = ARENA_ROOT.parent.parent
os.environ.setdefault("KITSOKI_ROOT", str(REPO_ROOT))
sys.path.insert(0, str(ARENA_ROOT))

from arena.model import JobSpec  # noqa: E402
from arena.plugins import base as plugins  # noqa: E402

runner_spec = importlib.util.spec_from_file_location(
    "paired_task_runner", ARENA_ROOT / "lib" / "paired_task_runner.py"
)
if runner_spec is None or runner_spec.loader is None:
    raise SystemExit("could not load paired_task_runner.py")
runner = importlib.util.module_from_spec(runner_spec)
sys.modules[runner_spec.name] = runner
runner_spec.loader.exec_module(runner)

failures: list[str] = []


def check(label: str, got, want) -> None:
    if got != want:
        failures.append(f"{label}: got {got!r}, want {want!r}")


def require(label: str, condition: bool) -> None:
    if not condition:
        failures.append(label)


check("native runner root is source checkout", runner.default_kitsoki_root({}), REPO_ROOT)


check("registry raw alias", runner.resolve_treatment_driver("single-briefed").name, "raw-codex")
check("registry kitsoki alias", runner.resolve_treatment_driver("kitsoki").name, "kitsoki-mcp")
check("registry codeact", runner.resolve_treatment_driver("codex-codeact").name, "codex-codeact")
check("registry unknown", runner.resolve_treatment_driver("not-a-treatment"), None)

drive_source = (REPO_ROOT / "tools" / "mcp-drive" / "drive.sh").read_text(encoding="utf-8")
runner_source = (ARENA_ROOT / "lib" / "paired_task_runner.py").read_text(encoding="utf-8")
require("MCP driver skips unset forwarded environment", 'if [[ -n "${!_fwd-}" ]]; then' in drive_source)
require("MCP driver does not override CODEX_HOME with empty value", 'mcp_servers.kitsoki.env.${_fwd}=${!_fwd-}' not in drive_source)

testing_prompt = (REPO_ROOT / "stories" / "bugfix" / "prompts" / "testing_executing.md").read_text(encoding="utf-8")
require("strict testing recognizes expected test-runner failures", "[expected fail]" in testing_prompt)
require("strict testing records expected failures as caveats", "baseline caveat" in testing_prompt)

args = argparse.Namespace(capability_presets_json="", capability_preset="")
cap_json, cap_hash = runner.capability_preset_json(args, "repo_patch")
check("canonical capability json", cap_json, '{"fs":{"max_bytes":1048576,"read":["**"],"write":["**"]},"vcs":"read"}')
require("capability hash has sha256 prefix", cap_hash.startswith("sha256:"))

bad_args = argparse.Namespace(capability_presets_json='{"narrow":{"vcs":"read"}}', capability_preset="")
try:
    runner.capability_preset_json(bad_args, "missing")
except ValueError as exc:
    require("unknown preset names known presets", "unknown capability preset" in str(exc))
else:
    failures.append("unknown preset did not raise")

with tempfile.TemporaryDirectory(prefix="paired-codeact-") as td:
    tree = Path(td).resolve()
    command = [
        "codex",
        "exec",
        "--dangerously-bypass-approvals-and-sandbox",
        "--disable=shell_tool",
        "--disable=apps",
        "-c",
        'mcp_servers.kitsoki-codeact.command="kitsoki"',
        "-c",
        "mcp_servers.kitsoki-codeact.enabled=true",
        "-c",
        f'mcp_servers.kitsoki-codeact.args=["mcp-codeact","--capabilities-json","{cap_json}"]',
    ]
    plan = {
        "mode": "codeact",
        "agent": "kitsoki-codeact-driver",
        "orchestrator_model": "gpt-5.4",
        "backend": "codex",
        "working_dir": str(tree),
        "tools": ["mcp__kitsoki-codeact__codeact_eval"],
        "command": command,
    }
    assertions = runner.assert_codeact_launch_plan(
        plan,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("valid plan passes", assertions["passed"], True)
    escaped = dict(plan, command=command[:-1] + [
        f'mcp_servers.kitsoki-codeact.args=["mcp-codeact","--capabilities-json","{cap_json.replace(chr(34), chr(92) + chr(34))}"]',
    ])
    assertions = runner.assert_codeact_launch_plan(
        escaped,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("escaped capability plan passes", assertions["passed"], True)
    doubly_escaped = cap_json.replace(chr(34), chr(92) + chr(92) + chr(92) + chr(34))
    double_escaped = dict(plan, command=command[:-1] + [
        f'mcp_servers.kitsoki-codeact.args=["mcp-codeact","--capabilities-json","{doubly_escaped}"]',
    ])
    assertions = runner.assert_codeact_launch_plan(
        double_escaped,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("double-escaped capability plan passes", assertions["passed"], True)
    wrong_payload = cap_json.replace("1048576", "1")
    wrong_payload_plan = dict(plan, command=command[:-1] + [
        f'mcp_servers.kitsoki-codeact.args=["mcp-codeact","--capabilities-json","{wrong_payload}"]',
    ])
    assertions = runner.assert_codeact_launch_plan(
        wrong_payload_plan,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("different capability payload fails", assertions["passed"], False)
    bad = dict(plan, tools=["mcp__kitsoki-codeact__codeact_eval", "Bash"])
    assertions = runner.assert_codeact_launch_plan(
        bad,
        tree=tree,
        agent="kitsoki-codeact-driver",
        backend="codex",
        capability_json=cap_json,
        capability_hash=cap_hash,
    )
    check("extra tool fails", assertions["passed"], False)

missing_trace = runner.real_trace_metrics(str(tree / "absent.jsonl"), "gpt-5.4")
check("missing studio trace is incomplete", missing_trace.get("measurement_status"), "incomplete")

public_ticket_task = {
    "id": "public-ticket-fixture",
    "archetype": "bugfix_test_repair",
    "ticket": "The post-command validator loses its declared working directory.",
    "ticket_body": "The public contract requires Cwd and a separate infrastructure-error channel.",
    "acceptance_contract": [{"id": "cwd", "description": "Preserve the declared working directory."}],
}
prompt_args = argparse.Namespace(treatment="raw-codex", implementation_mode="agent_task", story="")
raw_prompt = runner.build_prompt(prompt_args, public_ticket_task)
require("raw prompt contains complete public ticket", public_ticket_task["ticket_body"] in raw_prompt)
kitsoki_prompt = runner.build_kitsoki_prompt(
    prompt_args,
    public_ticket_task,
    Path("/tmp/public-ticket-tree"),
    "/tmp/public-ticket-trace.jsonl",
    Path("/tmp/public-ticket-thread.md"),
    "codex-gpt54",
    "paired-task-public-ticket",
    "go test ./internal/host -count=1",
    "codex",
)
require("MCP prompt contains complete public ticket", json.dumps(public_ticket_task["ticket_body"]) in kitsoki_prompt)
require("MCP prompt retains public acceptance contract", "acceptance_contract:" in kitsoki_prompt)

prompt_args = argparse.Namespace(implementation_mode="agent_task", story="")
prompt = runner.build_kitsoki_prompt(
    prompt_args,
    {
        "id": "public-contract-fixture",
        "archetype": "bugfix",
        "baseline_sha": "0123456789abcdef",
        "ticket": "A visible error message must reach the operator.",
        "acceptance_contract": [{"id": "visible-message", "description": "operator sees error"}],
    },
    Path("/tmp/fixture"),
    "/tmp/fixture.jsonl",
    Path("/tmp/thread.md"),
    "codex-gpt54",
    "paired-task-fixture",
    "pnpm vitest run",
    "codex",
)
require("public acceptance contract forwarded", 'acceptance_contract: [{"description": "operator sees error", "id": "visible-message"}]' in prompt)
require("public acceptance baseline forwarded", 'acceptance_base_sha: "0123456789abcdef"' in prompt)
require("MCP prompt has one explicit lifecycle entry", "bf_autostart_attempted:" not in prompt)
require("MCP prompt submits full pipeline explicitly", "Submit `full_pipeline` ONCE with **session.submit**" in prompt)
check("GPT-5.4 maps to a dedicated Kitsoki profile", runner.MODEL_TO_PROFILE.get("gpt-5.4"), "codex-gpt54")
check("Spark maps to its dedicated Kitsoki profile", runner.MODEL_TO_PROFILE.get("gpt-5.3-codex-spark"), "codex-spark")

# Spark uses the same Codex backend in both arms.  The strict CodeAct
# treatment must therefore accept its worker profile rather than rejecting the
# cell at the generic backend guard before a provider is ever considered.
spark_strict_args = argparse.Namespace(
    treatment="kitsoki-mcp-codeact",
    backend="codex",
    agent="",
    worker_profile="codex-spark",
    implementation_mode="codeact",
    capability_preset="repo_patch",
    capability_presets_json="",
)
check("Spark strict CodeAct backend validation", runner.validate_driver_args(spark_strict_args), "")

launcher_dir = runner.ensure_kitsoki_launcher()
launcher = launcher_dir / "kitsoki"
require("Kitsoki source launcher exists", launcher.exists())
require("Kitsoki source launcher uses go run", "go run" in launcher.read_text(encoding="utf-8"))
require("Kitsoki source launcher does not compile a binary", "go build" not in launcher.read_text(encoding="utf-8"))

subscription_raw = runner.codex_output_metrics('{"usage":{"input_tokens":10,"output_tokens":2}}', "gpt-5.4")
check("raw subscription USD is unavailable", subscription_raw.get("cost_usd"), None)
check("raw subscription cost basis is explicit", subscription_raw.get("cost_basis"), "subscription-unmetered")

direct_codeact_fixture = (HERE / "fixtures" / "direct-codeact-turn-completed.jsonl").read_text(encoding="utf-8")
direct_codeact_usage = runner.codex_output_metrics(direct_codeact_fixture, "gpt-5.4")
check("direct CodeAct turn.completed sums distinct requests", direct_codeact_usage.get("tokens"), 385)
check("direct CodeAct turn.completed deduplicates retry event", direct_codeact_usage.get("input_tokens"), 210)
check("direct CodeAct preserves cache-read usage", direct_codeact_usage.get("cached_input_tokens"), 140)
check("direct CodeAct preserves output usage", direct_codeact_usage.get("output_tokens"), 26)
check("direct CodeAct preserves reasoning usage", direct_codeact_usage.get("reasoning_output_tokens"), 9)

direct_codeact_log_fixture = (HERE / "fixtures" / "direct-codeact-turn-completed.log").read_text(encoding="utf-8")
direct_codeact_log_usage = runner.codex_output_metrics(direct_codeact_log_fixture, "gpt-5.4")
check("direct CodeAct structured turn.completed retains input", direct_codeact_log_usage.get("input_tokens"), 1008249)
check("direct CodeAct structured turn.completed retains output", direct_codeact_log_usage.get("output_tokens"), 12703)
check("direct CodeAct structured turn.completed totals tokens", direct_codeact_log_usage.get("tokens"), 1020952)
check("direct CodeAct structured log duplicate is counted once", direct_codeact_log_usage.get("tokens"), 1020952)

with tempfile.TemporaryDirectory(prefix="paired-result-target-") as td:
    target = Path(td) / "nested" / "cell.json"
    exit_code = runner.emit(
        verdict="armed",
        notes="fixture",
        target=str(target),
    )
    check("result target exit code", exit_code, 0)
    persisted = json.loads(target.read_text(encoding="utf-8"))
    check("result target persists verdict", persisted.get("verdict"), "armed")
    check("result target persists notes", persisted.get("notes"), "fixture")

with tempfile.TemporaryDirectory(prefix="paired-subscription-trace-") as td:
    trace = Path(td) / "trace.jsonl"
    trace.write_text(json.dumps({
        "kind": "agent.call.complete",
        "payload": {"meta": {"usage": {"input_tokens": 10, "cached_input_tokens": 6, "output_tokens": 2}}},
    }) + "\n", encoding="utf-8")
    subscription_trace = runner.real_trace_metrics(str(trace), "gpt-5.4")
    check("Kitsoki subscription USD is unavailable", subscription_trace.get("cost_usd"), None)
    check("Kitsoki subscription cost basis is explicit", subscription_trace.get("cost_basis"), "subscription-unmetered")

spec = JobSpec.from_dict({
    "job_type": "paired-task",
    "targets": [{"id": "fixture"}],
    "variants": [{
        "id": "codex-codeact-gpt55",
        "treatment": "codex-codeact",
        "backend": "codex",
        "model": "gpt-5.5",
        "effort": "medium",
        "agent": "kitsoki-codeact-driver",
        "orchestrator_model": "gpt-5.4",
        "capability_preset": "repo_patch",
    }],
    "axes": {"task": ["api-routing"]},
    "options": {
        "live_gate_env": "ARENA_PAIRED_TASK_ENABLE_CODEX",
        "capability_presets": {
            "repo_patch": {
                "fs": {"read": ["**"], "write": ["**"], "max_bytes": 1048576},
                "vcs": "read",
            }
        },
    },
})
cell = spec.cells()[0]
plugin = plugins.get("paired-task")
argv = plugin.drive_command(cell, live=True)
for flag, value in {
    "--agent": "kitsoki-codeact-driver",
    "--orchestrator-model": "gpt-5.4",
    "--capability-preset": "repo_patch",
    "--live-gate-env": "ARENA_PAIRED_TASK_ENABLE_CODEX",
}.items():
    require(f"{flag} threaded", flag in argv and argv[argv.index(flag) + 1] == value)
require("capability presets threaded", "--capability-presets-json" in argv)
json.loads(argv[argv.index("--capability-presets-json") + 1])
require("no target/variant story meta -> no --story flag threaded (default preserved)", "--story" not in argv)

story_spec = JobSpec.from_dict({
    "job_type": "paired-task",
    "targets": [{"id": "fixture", "story": "stories/bugfix/app.yaml"}],
    "variants": [{"id": "kitsoki-gpt55", "treatment": "kitsoki", "backend": "codex", "model": "gpt-5.5"}],
    "axes": {"task": ["api-routing"]},
})
story_cell = story_spec.cells()[0]
story_argv = plugin.drive_command(story_cell, live=True)
require("--story threaded from target.meta", "--story" in story_argv and story_argv[story_argv.index("--story") + 1] == "stories/bugfix/app.yaml")

with tempfile.TemporaryDirectory(prefix="paired-raw-effort-") as td:
    trace = Path(td) / "raw.jsonl"
    captured: list[str] = []
    captured_env: dict[str, str] = {}
    original_run = runner.subprocess.run

    def fake_run(cmd, **kwargs):
        captured.extend(cmd)
        captured_env.update(kwargs.get("env") or {})
        return runner.subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

    runner.subprocess.run = fake_run
    try:
        runner.dispatch_single_prompt_codex(
            argparse.Namespace(model="gpt-5.4", effort="medium", treatment="raw-codex"),
            {"id": "fixture", "ticket": "fix", "archetype": "bugfix", "oracle": {}},
            Path(td),
            str(trace),
        )
    finally:
        runner.subprocess.run = original_run
    require("raw Codex effort is explicit", 'model_reasoning_effort="medium"' in captured)
    require("raw Codex uses a private HOME", captured_env.get("HOME") not in {"", os.environ.get("HOME", "")})
    require("raw Codex uses an auth-only CODEX_HOME", captured_env.get("CODEX_HOME", "").endswith("/.codex"))

with tempfile.TemporaryDirectory(prefix="paired-codex-home-") as td:
    source = Path(td) / "source" / "auth.json"
    source.parent.mkdir(parents=True)
    source.write_text('{"token":"test-only"}', encoding="utf-8")
    env, cleanup = runner.isolated_codex_env(source_auth=source, base_env={"PATH": "/bin"})
    private_home = Path(env["HOME"])
    private_codex_home = Path(env["CODEX_HOME"])
    check("isolated home is not source parent", private_home == source.parent, False)
    check("isolated CODEX_HOME holds auth only", sorted(p.name for p in private_codex_home.iterdir()), ["auth.json"])
    check("isolated env preserves PATH", env["PATH"], "/bin")
    check("isolation metadata never forwards operator home", runner.codex_isolation_metadata()["operator_codex_home_forwarded"], False)
    cleanup()
    check("isolated home is removed", private_home.exists(), False)

require("raw Codex ignores user config", '"--ignore-user-config",' in runner_source)
require("raw Codex uses an absolute checkout", "str(tree.resolve())," in runner_source)
require("MCP driver uses ephemeral outer orchestration", "--ephemeral" in drive_source)
require("MCP dispatch uses auth-only environment", "env, cleanup_auth_home = isolated_codex_env()" in runner_source)
require("raw prompt carries isolation policy", "BENCHMARK ISOLATION:" in runner.build_prompt(argparse.Namespace(treatment="raw"), {"id": "fixture", "archetype": "bugfix", "ticket": "fix"}))
check("paired task explicit public test command wins", runner.test_cmd_for({"test_cmd": "pnpm vitest run", "oracle": {}}), "pnpm vitest run")
check("paired task without a suite skips strict suite gate", runner.test_cmd_for({"oracle": {}}), "")

with tempfile.TemporaryDirectory(prefix="paired-trace-reset-") as td:
    trace = Path(td) / "cell.jsonl"
    sidecars = [trace, trace.with_suffix(".prompt.md"), trace.with_suffix(".thread.md"), trace.with_suffix(".drive-log.json")]
    for path in sidecars:
        path.write_text("stale", encoding="utf-8")
    runner.reset_cell_trace(trace)
    check("retained cell rerun clears stale trace sidecars", any(path.exists() for path in sidecars), False)

with tempfile.TemporaryDirectory(prefix="paired-cleanup-") as td:
    tree = Path(td) / "node_modules" / "nested"
    tree.mkdir(parents=True)
    locked = tree / "package.json"
    locked.write_text("{}", encoding="utf-8")
    locked.chmod(0o400)
    runner.cleanup_cell_workdir(Path(td) / "node_modules")
    check("cell cleanup removes read-only nested tree", (Path(td) / "node_modules").exists(), False)

with tempfile.TemporaryDirectory(prefix="paired-prewarm-") as td:
    captured: list[list[str]] = []
    original_run = runner.run
    original_subprocess_run = runner.subprocess.run

    def fake_runner_run(cmd, **_kwargs):
        return runner.subprocess.CompletedProcess(cmd, 0, stdout=json.dumps({"install": "npm install"}), stderr="")

    def fake_prewarm_run(cmd, **_kwargs):
        captured.append(cmd)
        return runner.subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

    runner.run = fake_runner_run
    runner.subprocess.run = fake_prewarm_run
    try:
        prewarm = runner.prepare_live_tree(
            {"oracle": {"kind": "external_bakeoff", "project": "fixture", "bug": "case"}}, Path(td),
        )
    finally:
        runner.run = original_run
        runner.subprocess.run = original_subprocess_run
    check("external corpus prewarm succeeds", prewarm["ok"], True)
    check("external corpus prewarm configures disposable Git identity then installs", captured, [
        ["git", "config", "user.name", "Kitsoki Arena"],
        ["git", "config", "user.email", "arena@kitsoki.local"],
        ["sh", "-lc", "npm install"],
    ])

with tempfile.TemporaryDirectory(prefix="paired-shallow-baseline-") as td:
    captured: list[list[str]] = []
    original_run = runner.run

    def fake_materialize_run(cmd, **_kwargs):
        captured.append(cmd)
        if cmd[:3] == ["python3", str(runner.BENCH), "meta"]:
            return runner.subprocess.CompletedProcess(
                cmd, 0,
                stdout=json.dumps({"repo": "https://example.test/repo.git", "baseline_sha": "baseline-sha"}),
                stderr="",
            )
        return runner.subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

    runner.run = fake_materialize_run
    try:
        runner.materialize_baseline(
            {"oracle": {"kind": "external_bakeoff", "project": "fixture", "bug": "case"}},
            Path(td) / "tree",
        )
    finally:
        runner.run = original_run
    check("external baseline starts from empty repository", captured[1], ["git", "init", "-q", str(Path(td) / "tree")])
    check("external baseline fetches exactly one frozen commit", captured[3], ["git", "fetch", "-q", "--depth", "1", "origin", "baseline-sha"])
    require("external baseline does not full-clone future history", not any(cmd[:2] == ["git", "clone"] for cmd in captured))

with tempfile.TemporaryDirectory(prefix="paired-local-shallow-baseline-") as td:
    captured = []
    original_run = runner.run

    def fake_local_materialize_run(cmd, **_kwargs):
        captured.append(cmd)
        if cmd[:3] == ["python3", str(runner.BENCH), "meta"]:
            return runner.subprocess.CompletedProcess(
                cmd, 0,
                stdout=json.dumps({"repo": ".", "baseline_sha": "short-baseline"}),
                stderr="",
            )
        if cmd[:2] == ["git", "rev-parse"]:
            return runner.subprocess.CompletedProcess(cmd, 0, stdout="full-baseline\n", stderr="")
        return runner.subprocess.CompletedProcess(cmd, 0, stdout="", stderr="")

    runner.run = fake_local_materialize_run
    try:
        runner.materialize_baseline(
            {"oracle": {"kind": "external_bakeoff", "project": "fixture", "bug": "case"}},
            Path(td) / "tree",
        )
    finally:
        runner.run = original_run
    require("local short SHA resolves in harness", ["git", "rev-parse", "short-baseline^{commit}"] in captured)
    require("local shallow fetch receives resolved full SHA", ["git", "fetch", "-q", "--depth", "1", "origin", "full-baseline"] in captured)

prompt_args = argparse.Namespace(implementation_mode="agent_task")
prompt = runner.build_kitsoki_prompt(
    prompt_args,
    {"id": "fixture", "archetype": "bugfix", "ticket": "fix"},
    Path("/tmp/tree"),
    "/tmp/trace.jsonl",
    Path("/tmp/thread.md"),
    "codex-gpt54",
    "fixture-branch",
    "true",
    "codex",
)
require("Kitsoki prompt uses direct menu submission", "session.submit" in prompt)
require("Kitsoki prompt with no --story defaults to bench-bugfix", 'story_path: "' + str(runner.BENCH_BUGFIX_STORY) in prompt)
require("build_kitsoki_prompt tolerates a Namespace with no story attr set", True)  # the call above didn't raise

story_override_prompt = runner.build_kitsoki_prompt(
    argparse.Namespace(implementation_mode="agent_task", story="stories/bugfix/app.yaml"),
    {"id": "fixture", "archetype": "bugfix", "ticket": "fix"},
    Path("/tmp/tree"),
    "/tmp/trace.jsonl",
    Path("/tmp/thread.md"),
    "codex-gpt54",
    "fixture-branch",
    "true",
    "codex",
)
require("--story overrides the drive target", 'story_path: "stories/bugfix/app.yaml"' in story_override_prompt)
require("Kitsoki prompt gives post-worker continue example", 'intent: \"continue\"' in prompt)
require("Kitsoki prompt has the settled-turn control loop", "Then run this exact control loop" in prompt)
require("Kitsoki prompt waits through the supervised worker bound", "async_after_ms: 900000" in prompt)
require("Kitsoki prompt forbids rapid bare-status polling", "Never spin on bare status" in prompt)
require("Kitsoki prompt has a bounded trace-aware stall rule", "three spaced (15s) checks" in prompt)
require("Kitsoki prompt defers to an active supervised runtime", "Do NOT apply the three-check stall" in prompt)
require("Kitsoki prompt marks the cell workspace as prepared", "workspace_prepared: true" in prompt)

with tempfile.TemporaryDirectory(prefix="paired-runtime-trace-") as td:
    trace = Path(td) / "trace.jsonl"
    trace.write_text(json.dumps({"kind": "agent.runtime.start", "call_id": "live"}) + "\n", encoding="utf-8")
    check("unclosed supervised runtime blocks a cell", runner.trace_has_unclosed_runtime(str(trace)), True)
    with trace.open("a", encoding="utf-8") as f:
        f.write(json.dumps({"kind": "agent.runtime.end", "call_id": "live"}) + "\n")
    check("closed supervised runtime is terminal", runner.trace_has_unclosed_runtime(str(trace)), False)

with tempfile.TemporaryDirectory(prefix="paired-sequence-trace-") as td:
    trace = Path(td) / "trace.jsonl"
    trace.write_text(
        json.dumps({"kind": "session.header", "schema_version": 1}) + "\n" +
        json.dumps({"turn": 1, "seq": 0, "kind": "turn.start", "payload": {}}) + "\n" +
        json.dumps({"turn": 1, "seq": 2, "kind": "turn.end", "payload": {}}) + "\n",
        encoding="utf-8",
    )
    check("sequence gap blocks a cell", runner.trace_has_invalid_sequence(str(trace)), True)
    trace.write_text(
        json.dumps({"kind": "session.header", "schema_version": 1}) + "\n" +
        json.dumps({"turn": 1, "seq": 0, "kind": "turn.start", "payload": {}}) + "\n" +
        json.dumps({"turn": 1, "seq": 1, "kind": "turn.end", "payload": {}}) + "\n",
        encoding="utf-8",
    )
    check("contiguous sequence is accepted", runner.trace_has_invalid_sequence(str(trace)), False)

require("blocked dispatch skips external scoring", "external oracle skipped because live dispatch did not reach a terminal trace" in runner_source)

bench_story = (REPO_ROOT / "stories" / "bench-bugfix" / "app.yaml").read_text(encoding="utf-8")
require("bench forwards prepared workspace ownership into bugfix", "workspace_prepared: \"{{ world.workspace_prepared }}\"" in bench_story)

missing_agent = argparse.Namespace(
    treatment="codex-codeact",
    backend="codex",
    agent="",
    capability_preset="repo_patch",
    capability_presets_json="",
)
require("missing agent validation", "requires variant.agent" in runner.validate_driver_args(missing_agent))

wrong_agent = argparse.Namespace(
    treatment="codex-codeact",
    backend="codex",
    agent="kitsoki-mcp-driver",
    capability_preset="repo_patch",
    capability_presets_json="",
)
require("wrong agent validation", "kitsoki-codeact-driver" in runner.validate_driver_args(wrong_agent))

if failures:
    print("FAIL: paired-task CodeAct")
    for f in failures:
        print(f"  - {f}")
    raise SystemExit(1)
print("PASS: paired-task CodeAct treatments (registry, assertions, argv; no LLM)")
