"""Direct Codex CodeAct treatment."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
from pathlib import Path
from typing import Any

from .base import DriverResult, DriverServices, TreatmentDriver
from .capabilities import (
    CODEACT_CAPABILITY_PRESET,
    DEFAULT_CODEACT_AGENT,
    assert_codeact_launch_plan,
    capability_preset_json,
)


class CodexCodeactDriver(TreatmentDriver):
    name = "codex-codeact"
    action_surface = "kitsoki-codeact-mcp"

    def run(
        self,
        args: argparse.Namespace,
        task: dict[str, Any],
        tree: Path,
        trace_ref: str,
        services: DriverServices,
    ) -> DriverResult:
        if args.backend != "codex":
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes=f"codex-codeact live dispatch expects backend 'codex', got {args.backend!r}",
                metrics=services.zero_metrics(action_surface=self.action_surface),
            )
        agent = args.agent or DEFAULT_CODEACT_AGENT
        preset_name = args.capability_preset or CODEACT_CAPABILITY_PRESET
        try:
            cap_json, cap_hash = capability_preset_json(args, preset_name)
            task_file = services.write_task_file(args, task, trace_ref)
            bin_dir = services.ensure_kitsoki_launcher()
        except (Exception, SystemExit) as exc:  # noqa: BLE001 - report as harness failure.
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes=f"codeact prepare failed: {exc}",
                metrics=services.zero_metrics(action_surface=self.action_surface),
            )

        cmd = [
            "kitsoki",
            "agent",
            "launch",
            "--agent",
            agent,
            "--mode",
            "codeact",
            "--backend",
            "codex",
            "--working-dir",
            str(tree),
            "--codeact-capabilities-json",
            cap_json,
            "--task-file",
            str(task_file),
        ]
        if args.model:
            cmd.extend(["--model", args.model])
        if args.effort:
            cmd.extend(["--effort", args.effort])

        env = dict(os.environ)
        env["PATH"] = f"{bin_dir}:{env.get('PATH', '')}"
        launch_plan_ref = str(Path(trace_ref).with_suffix(".launch-plan.json"))
        dry = subprocess.run(cmd, cwd=services.kitsoki_root, text=True, capture_output=True, env=env)
        Path(launch_plan_ref).write_text(
            dry.stdout if dry.stdout.strip() else json.dumps({
                "stderr": dry.stderr[-4000:],
                "returncode": dry.returncode,
            }, indent=2),
            encoding="utf-8",
        )
        if dry.returncode != 0:
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes=f"codeact launch dry-run failed exit={dry.returncode}: {services.first_line(dry.stdout + chr(10) + dry.stderr)}",
                trace_ref=services.container_path(trace_ref),
                launch_plan_ref=services.container_path(launch_plan_ref),
                metrics=services.zero_metrics(
                    action_surface=self.action_surface,
                    capability_hash=cap_hash,
                    launch_plan_ref=services.container_path(launch_plan_ref),
                ),
            )
        try:
            plan = json.loads(dry.stdout)
        except json.JSONDecodeError as exc:
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes=f"codeact launch dry-run did not return JSON: {exc}",
                trace_ref=services.container_path(trace_ref),
                launch_plan_ref=services.container_path(launch_plan_ref),
                metrics=services.zero_metrics(
                    action_surface=self.action_surface,
                    capability_hash=cap_hash,
                    launch_plan_ref=services.container_path(launch_plan_ref),
                ),
            )
        assertions = assert_codeact_launch_plan(
            plan,
            tree=tree,
            agent=agent,
            backend="codex",
            capability_json=cap_json,
            capability_hash=cap_hash,
        )
        if not assertions["passed"]:
            Path(trace_ref).write_text(json.dumps({
                "launch_plan_ref": services.container_path(launch_plan_ref),
                "permission_assertions": assertions,
            }, indent=2, sort_keys=True), encoding="utf-8")
            return DriverResult(
                blocked=True,
                infra_class="infra:harness",
                notes="codeact launch plan assertion failed: " + "; ".join(assertions["failures"]),
                trace_ref=services.container_path(trace_ref),
                launch_plan_ref=services.container_path(launch_plan_ref),
                metrics=services.zero_metrics(
                    action_surface=self.action_surface,
                    capability_hash=cap_hash,
                    launch_plan_ref=services.container_path(launch_plan_ref),
                    permission_assertions=assertions,
                ),
            )

        exec_cmd = cmd + ["--exec"]
        timeout = int(os.environ.get("ARENA_CODEX_TIMEOUT_S", "900"))
        try:
            proc = subprocess.run(
                exec_cmd,
                cwd=services.kitsoki_root,
                text=True,
                capture_output=True,
                env=env,
                timeout=timeout,
            )
            timed_out = False
        except subprocess.TimeoutExpired as exc:
            timed_out = True
            proc = subprocess.CompletedProcess(exec_cmd, 124, stdout=(exc.stdout or ""), stderr=(exc.stderr or ""))
        trace_blob = {
            "cmd": services.redact_cmd(exec_cmd),
            "launch_plan_ref": services.container_path(launch_plan_ref),
            "permission_assertions": assertions,
            "capability_hash": cap_hash,
            "timeout_s": timeout,
            "timed_out": timed_out,
            "returncode": proc.returncode,
            "stdout_tail": proc.stdout[-4000:],
            "stderr_tail": proc.stderr[-4000:],
        }
        Path(trace_ref).write_text(json.dumps(trace_blob, indent=2, sort_keys=True), encoding="utf-8")
        metrics = services.codex_output_metrics(proc.stdout + "\n" + proc.stderr, args.model or "gpt-5.5")
        metrics.update({
            "action_surface": self.action_surface,
            "capability_preset": preset_name,
            "capability_hash": cap_hash,
            "launch_plan_ref": services.container_path(launch_plan_ref),
            "permission_assertions": assertions,
        })
        metrics.update(services.codeact_text_metrics(proc.stdout + "\n" + proc.stderr))
        note = f"codeact codex exit={proc.returncode}: {services.first_line(proc.stdout + chr(10) + proc.stderr)}"
        if timed_out:
            note = f"codeact codex timed out at {timeout}s; " + services.first_line(proc.stdout + chr(10) + proc.stderr)
        if metrics.get("cost_note"):
            note += f"; {metrics['cost_note']}"
        return DriverResult(
            blocked=proc.returncode != 0 or timed_out,
            infra_class="infra:harness" if proc.returncode != 0 or timed_out else "",
            notes=note,
            trace_ref=services.container_path(trace_ref),
            transcript_ref=services.container_path(trace_ref),
            launch_plan_ref=services.container_path(launch_plan_ref),
            metrics=metrics,
        )
