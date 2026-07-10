#!/usr/bin/env python3
"""Internal Persona QA compatibility adapter.

The Kitsoki product surface is the scenario-qa story. This module keeps
portable kit configuration, retained-fixture deck generation, and completion
state adapters deterministic and testable while delegating run-bundle mechanics
to tools/product-journey/run.py.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from pathlib import Path
from typing import Sequence

if __package__ in {None, ""}:  # Support `python tools/persona_qa/kit.py ...`.
    sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
    from tools.persona_qa import config as qa_config  # type: ignore
    from tools.persona_qa.completion import load_product_journey_run  # type: ignore
    from tools.persona_qa.deck import PlaybackEvidenceError, build_deck, write_deck  # type: ignore
else:
    from . import config as qa_config
    from .completion import load_product_journey_run
    from .deck import PlaybackEvidenceError, build_deck, write_deck


REPO_ROOT = Path(__file__).resolve().parents[2]


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="persona-qa",
        description="Maintainer/debug adapter for Persona QA kit files. Prefer stories/scenario-qa for operator workflows.",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    init = sub.add_parser("init", help="Create a portable persona-qa/ kit in a project")
    init.add_argument("--root", default=".", help="project root to initialize")
    init.add_argument("--force", action="store_true", help="overwrite existing template files")
    init.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    init.set_defaults(func=cmd_init)

    validate = sub.add_parser("validate", help="Validate persona-qa.yaml and kit catalogs")
    validate.add_argument("--config", default="persona-qa.yaml", help="kit config path")
    validate.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    validate.set_defaults(func=cmd_validate)

    transports = sub.add_parser("transports", help="Preview deterministic scenario x transport legs")
    transports.add_argument("--config", default="persona-qa.yaml", help="kit config path")
    transports.add_argument("--scenario", default="", help="single scenario id; alias for --scenarios")
    transports.add_argument("--scenarios", default="", help="comma-separated scenario ids")
    transports.add_argument("--transport", default="all", help="transport id, comma list, or all")
    transports.add_argument("--driver", default="", help="driver manifest id/path; defaults to config drivers.default")
    transports.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    transports.set_defaults(func=cmd_transports)

    emit = sub.add_parser("emit-run", help="Create a no-LLM run bundle from a kit config")
    add_common_run_flags(emit)
    emit.set_defaults(func=cmd_emit_run)

    smoke = sub.add_parser("replay-smoke", help="Run a deterministic cassette-backed scenario replay smoke")
    smoke.add_argument("--config", default="", help="optional kit config path")
    smoke.add_argument("--project", default="gears-rust", help="project/target id")
    smoke.add_argument("--persona", default="core-maintainer", help="persona id")
    smoke.add_argument("--scenario", default="bugfix", help="scenario id")
    smoke.add_argument("--seed", default="persona-qa-smoke", help="deterministic run seed")
    smoke.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    smoke.set_defaults(func=cmd_replay_smoke)

    drive = sub.add_parser("drive", help="Drive an emitted run bundle")
    drive.add_argument("--config", default="persona-qa.yaml", help="kit config path")
    drive.add_argument("--run-dir", required=True, help="run bundle directory")
    drive.add_argument(
        "--mode",
        default="replay",
        choices=["replay", "record", "live"],
        help="replay is no-LLM/demo evidence; record/live are intentionally explicit future/live modes",
    )
    drive.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    drive.set_defaults(func=cmd_drive)

    review = sub.add_parser("review", help="Review an existing run bundle")
    review.add_argument("--config", default="persona-qa.yaml", help="kit config path")
    review.add_argument("--run-dir", required=True, help="run bundle directory")
    review.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    review.set_defaults(func=cmd_review)

    deck = sub.add_parser("deck", help="Generate a deterministic Slidey deck from an existing run bundle")
    deck.add_argument("--config", default="", help="optional kit config path; used for relative path resolution")
    deck.add_argument("--run-dir", required=True, help="run bundle directory")
    deck.add_argument("--out", default="", help="deck output path; default is <run-dir>/deck.slidey.json")
    deck.add_argument("--title", default="", help="override the generated deck title")
    deck.add_argument(
        "--max-playback-scenes",
        type=int,
        default=6,
        help="maximum standalone playback scenes to include",
    )
    deck.add_argument("--json-output", action="store_true", help="print a machine-readable result")
    deck.set_defaults(func=cmd_deck)

    complete = sub.add_parser("complete", help="Emit the shared completion-state JSON for a run")
    complete.add_argument("--config", default="persona-qa.yaml", help="kit config path; used for path resolution")
    complete.add_argument("--run-dir", required=True, help="run bundle directory")
    complete.add_argument("--out", default="", help="write completion-state JSON here instead of stdout only")
    complete.set_defaults(func=cmd_complete)
    return parser


def add_common_run_flags(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--config", default="persona-qa.yaml", help="kit config path")
    parser.add_argument("--project", default="local-app", help="project/target id")
    parser.add_argument("--persona", default="core-maintainer", help="persona id")
    parser.add_argument("--scenario", default="", help="single scenario id; alias for --scenarios")
    parser.add_argument("--scenarios", default="", help="comma-separated scenario ids")
    parser.add_argument("--transport", default="all", help="transport id, comma list, or all")
    parser.add_argument("--driver", default="", help="driver manifest id/path; defaults to config drivers.default")
    parser.add_argument("--seed", default="persona-qa", help="deterministic run seed")
    parser.add_argument("--live-budget-minutes", type=int, default=20, help="budget written into run contracts")
    parser.add_argument("--preview", action="store_true", help="print the scenario x transport suite without creating a run bundle")
    parser.add_argument("--json-output", action="store_true", help="print a machine-readable result")


def cmd_init(args: argparse.Namespace) -> int:
    root = Path(args.root).expanduser()
    if not root.is_absolute():
        root = (Path.cwd() / root).resolve()
    result = init_kit(root, force=args.force)
    if args.json_output:
        print(json.dumps(result, sort_keys=True))
    else:
        print(f"Persona QA kit initialized: {result['root']}")
        for path in result["files"]:
            print(f"- {path}")
        print("Run: kitsoki run @kitsoki/scenario-qa")
    return 0


def cmd_validate(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    result = qa_config.validate_config(cfg)
    if args.json_output:
        print(json.dumps(result, sort_keys=True))
    else:
        print(f"Persona QA config: {result['status']}")
        print(f"Project root: {result['config']['project_root']}")
        print(f"Personas: {result['counts']['personas']}")
        print(f"Scenarios: {result['counts']['scenarios']}")
        print(f"Drivers: {result['counts']['drivers']}")
        for issue in result["issues"]:
            detail = f" ({issue['detail']})" if issue.get("detail") else ""
            print(f"- {issue['severity']}: {issue['id']}: {issue['message']}{detail}")
    return 0 if result["status"] == "valid" else 1


def cmd_transports(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    scenarios = args.scenarios or args.scenario or "project-onboarding"
    runner_args = [
        "--transport-suite",
        "--scenarios",
        scenarios,
        "--transport",
        args.transport,
    ]
    if args.driver:
        runner_args.extend(["--driver", args.driver])
    elif cfg.default_driver:
        runner_args.extend(["--driver", cfg.default_driver])
    if args.json_output:
        runner_args.append("--json-output")
    return run_runner(cfg, runner_args)


def cmd_emit_run(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    scenarios = args.scenarios or args.scenario or "project-onboarding"
    if args.preview:
        return cmd_transports(args)
    runner_args = [
        "--emit-run",
        "--project",
        args.project,
        "--persona",
        args.persona,
        "--seed",
        args.seed,
        "--scenarios",
        scenarios,
        "--transport",
        args.transport,
        "--live-budget-minutes",
        str(args.live_budget_minutes),
    ]
    if args.driver:
        runner_args.extend(["--driver", args.driver])
    elif cfg.default_driver:
        runner_args.extend(["--driver", cfg.default_driver])
    if args.json_output:
        runner_args.append("--json-output")
    return run_runner(cfg, runner_args)


def cmd_replay_smoke(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config or None, repo_root=REPO_ROOT)
    runner_args = [
        "--gate",
        "driver-replay",
        "--project",
        args.project,
        "--smoke-persona",
        args.persona,
        "--smoke-scenario",
        args.scenario,
        "--seed",
        args.seed,
    ]
    if args.json_output:
        runner_args.append("--json-output")
    return run_runner(cfg, runner_args)


def cmd_drive(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    if args.mode != "replay":
        message = (
            f"drive --mode {args.mode} is a live/recording mode and is not run by default; "
            "use the scenario/product-journey story surfaces for authorized live capture."
        )
        if args.json_output:
            print(json.dumps({"status": "blocked", "mode": args.mode, "summary": message}, sort_keys=True))
        else:
            print(message, file=sys.stderr)
        return 2
    runner_args = ["--seed-demo-evidence", "--run-dir", args.run_dir]
    if args.json_output:
        runner_args.append("--json-output")
    return run_runner(cfg, runner_args)


def cmd_review(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    runner_args = ["--review-run", "--run-dir", args.run_dir]
    if args.json_output:
        runner_args.append("--json-output")
    return run_runner(cfg, runner_args)


def cmd_deck(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config or None, repo_root=REPO_ROOT)
    run_dir = resolve_run_dir(cfg, args.run_dir)
    out = resolve_run_dir(cfg, args.out) if args.out else run_dir / "deck.slidey.json"
    try:
        deck = build_deck(
            run_dir,
            title=args.title,
            max_playback_scenes=max(args.max_playback_scenes, 0),
            source_ref=args.run_dir,
        )
    except PlaybackEvidenceError as exc:
        if args.json_output:
            print(json.dumps({"status": "blocked", "reason": str(exc), "run_dir": str(run_dir)}, sort_keys=True))
        else:
            print(str(exc), file=sys.stderr)
        return 2
    write_deck(deck, out)
    run_id = ""
    run_json = run_dir / "run.json"
    if run_json.exists():
        run_id = str(json.loads(run_json.read_text(encoding="utf-8")).get("run_id", ""))
    result = {
        "status": "deck_built",
        "run_id": run_id,
        "run_dir": str(run_dir),
        "deck_path": str(out),
        "scene_count": len(deck.get("scenes", [])),
        "playback_scene_count": sum(
            1
            for scene in deck.get("scenes", [])
            if isinstance(scene, dict) and scene.get("eyebrow") == "Playback evidence"
        ),
    }
    if args.json_output:
        print(json.dumps(result, sort_keys=True))
    else:
        print(f"Deck: {out}")
        print(f"Scenes: {result['scene_count']}")
        print(f"Playback scenes: {result['playback_scene_count']}")
    return 0


def cmd_complete(args: argparse.Namespace) -> int:
    cfg = qa_config.load_config(args.config, repo_root=REPO_ROOT)
    run_dir = resolve_run_dir(cfg, args.run_dir)
    state = load_product_journey_run(run_dir)
    data = state.to_json()
    if args.out:
        out = resolve_run_dir(cfg, args.out)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(data, encoding="utf-8")
    sys.stdout.write(data)
    return 0


def init_kit(root: Path, *, force: bool = False) -> dict[str, object]:
    files: list[str] = []
    root.mkdir(parents=True, exist_ok=True)
    kit_root = root / "persona-qa"
    for directory in [
        kit_root / "personas",
        kit_root / "scenarios",
        kit_root / "drivers",
        kit_root / "oracles",
        kit_root / "fixtures",
        kit_root / "schemas" / qa_config.SCHEMA_VERSION,
        root / ".context",
        root / ".artifacts",
    ]:
        directory.mkdir(parents=True, exist_ok=True)

    _write(root / "persona-qa.yaml", qa_config.dump_yaml(qa_config.template_config()), force, files)
    _write_json(kit_root / "catalog.json", qa_config.template_catalog(), force, files)
    _write_json(kit_root / "github-targets.json", qa_config.template_github_targets(), force, files)
    _write_json(kit_root / "personas" / "core-maintainer.json", qa_config.template_persona(), force, files)
    _write_json(kit_root / "scenarios" / "project-onboarding.json", qa_config.template_scenario(), force, files)
    _write_json(kit_root / "drivers" / "web-generic.json", qa_config.template_driver(), force, files)
    _write(
        kit_root / "oracles" / "README.md",
        "# Persona QA Oracles\n\nPut deterministic local checks here. CI should use replay/cassette/local proof, not live LLM calls.\n",
        force,
        files,
    )
    _write(
        kit_root / "fixtures" / "README.md",
        "# Persona QA Fixtures\n\nPut replay fixtures, cassette-backed proof, screenshots, and local trace inputs here.\n",
        force,
        files,
    )
    _write(
        kit_root / "README.md",
        (
            "# Persona QA Support Files\n\n"
            "This kit describes persona lenses, scenarios, drivers, local fixtures, and schemas for story-owned product journey QA.\n\n"
            "Start with:\n\n"
            "```sh\n"
            "kitsoki run @kitsoki/scenario-qa\n"
            "```\n\n"
            "Then submit story prompts such as:\n\n"
            "```text\n"
            "preview project-onboarding across all transports\n"
            "check project-onboarding across all transports for core-maintainer on local-app\n"
            "next leg\n"
            "report\n"
            "```\n"
            "\nThe `kitsoki persona-qa` command remains a maintainer/debug adapter for validation, retained fixture decks, and CI checks.\n"
        ),
        force,
        files,
    )
    copy_public_schemas(root, force=force, files=files)
    return {"status": "initialized", "root": str(root), "config": str(root / "persona-qa.yaml"), "files": files}


def copy_public_schemas(root: Path, *, force: bool, files: list[str]) -> None:
    src = REPO_ROOT / "schemas" / "persona-qa" / qa_config.SCHEMA_VERSION
    dst = root / "persona-qa" / "schemas" / qa_config.SCHEMA_VERSION
    if not src.is_dir():
        return
    for schema in sorted(src.glob("*.schema.json")):
        _write(dst / schema.name, schema.read_text(encoding="utf-8"), force, files)


def run_runner(cfg: qa_config.PersonaQAConfig, runner_args: list[str]) -> int:
    configured = cfg.runner_command()
    cmd = configured or [sys.executable, str(REPO_ROOT / "tools" / "product-journey" / "run.py")]
    cmd = [*cmd, "--config", str(cfg.config_path)] if cfg.config_path is not None else [*cmd]
    cmd.extend(runner_args)
    env = os.environ.copy()
    env["PYTHONPATH"] = _prepend_path(env.get("PYTHONPATH", ""), str(REPO_ROOT))
    proc = subprocess.run(cmd, cwd=cfg.project_root, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if proc.stdout:
        sys.stdout.write(proc.stdout)
    if proc.stderr:
        sys.stderr.write(proc.stderr)
    return proc.returncode


def resolve_run_dir(cfg: qa_config.PersonaQAConfig, value: str) -> Path:
    path = Path(value).expanduser()
    if path.is_absolute():
        return path
    return (cfg.project_root / path).resolve()


def _write_json(path: Path, data: dict, force: bool, files: list[str]) -> None:
    _write(path, json.dumps(data, indent=2, sort_keys=True) + "\n", force, files)


def _write(path: Path, text: str, force: bool, files: list[str]) -> None:
    if path.exists() and not force:
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text, encoding="utf-8")
    files.append(str(path))


def _prepend_path(existing: str, value: str) -> str:
    if not existing:
        return value
    parts = existing.split(os.pathsep)
    if value in parts:
        return existing
    return value + os.pathsep + existing


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    return int(args.func(args))


if __name__ == "__main__":
    raise SystemExit(main())
