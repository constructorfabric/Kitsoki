#!/usr/bin/env python3
"""Export real Kitsoki JSONL traces into a routing benchmark corpus.

The exporter reads ~/.kitsoki/sessions/<app>/*.jsonl style EventSink traces and
pairs each turn.input with its machine.transition for the same file+turn. The
result is intentionally plain JSON/JSONL so benchmark runners can consume it
without replaying the original sessions.
"""

from __future__ import annotations

import argparse
import json
import os
import re
from collections import Counter
from pathlib import Path
from typing import Any


CONTEXT_DEPENDENT_INPUTS = {
    "do it",
    "go ahead",
    "ok",
    "ok do it",
    "ok go ahead",
    "okay",
    "okay do it",
    "okay go ahead",
    "yes",
    "yep",
}


def default_sessions_root() -> Path:
    return Path.home() / ".kitsoki" / "sessions"


def iter_trace_files(root: Path, app: str | None) -> list[Path]:
    if app:
        app_dir = root / app
        if not app_dir.exists():
            return []
        return sorted(p for p in app_dir.glob("*.jsonl") if p.is_file())
    return sorted(p for p in root.glob("*/*.jsonl") if p.is_file())


def read_jsonl(path: Path) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as f:
        for line_no, line in enumerate(f, 1):
            line = line.strip()
            if not line:
                continue
            try:
                item = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path}:{line_no}: invalid JSON: {exc}") from exc
            if isinstance(item, dict):
                out.append(item)
    return out


def app_for_path(root: Path, path: Path) -> str:
    try:
        return path.relative_to(root).parts[0]
    except Exception:
        return path.parent.name


def normalized_input(value: Any) -> str:
    return " ".join(str(value or "").strip().lower().split())


def is_context_dependent_input(value: Any) -> bool:
    return normalized_input(value) in CONTEXT_DEPENDENT_INPUTS


def build_samples(root: Path, trace_files: list[Path], include_empty: bool) -> list[dict[str, Any]]:
    samples: list[dict[str, Any]] = []
    for path in trace_files:
        events = read_jsonl(path)
        inputs: dict[int, dict[str, Any]] = {}
        transitions: dict[int, dict[str, Any]] = {}
        for event in events:
            turn = event.get("turn")
            if not isinstance(turn, int):
                continue
            payload = event.get("payload")
            if not isinstance(payload, dict):
                continue
            kind = event.get("kind")
            if kind == "turn.input":
                inputs[turn] = event
            elif kind == "machine.transition":
                transitions[turn] = event

        app = app_for_path(root, path)
        for turn in sorted(inputs):
            input_event = inputs[turn]
            transition_event = transitions.get(turn)
            payload = input_event.get("payload") or {}
            if not isinstance(payload, dict):
                continue
            user_input = payload.get("input") or ""
            if not include_empty and not str(user_input).strip():
                continue
            transition_payload = {}
            if transition_event is not None and isinstance(transition_event.get("payload"), dict):
                transition_payload = transition_event["payload"]
            sample_id = f"{app}:{path.stem}:turn-{turn}"
            samples.append(
                {
                    "sample_id": sample_id,
                    "source": "kitsoki_event_trace",
                    "app": app,
                    "trace_path": str(path),
                    "turn": turn,
                    "state": input_event.get("state_path") or transition_payload.get("from") or "",
                    "input": user_input,
                    "turn_input_intent": payload.get("intent") or "",
                    "expected": {
                        "intent": transition_payload.get("intent") or payload.get("intent") or "",
                        "slots": transition_payload.get("slots") or {},
                        "to": transition_payload.get("to") or "",
                    },
                    "route_labeled": bool(transition_payload.get("intent") or payload.get("intent")),
                    "has_transition": transition_event is not None,
                    "context_dependent": is_context_dependent_input(user_input),
                }
            )
    return samples


def iter_transcript_files(root: Path, app: str | None) -> list[Path]:
    if app:
        return sorted((root / app / "transcripts").glob("*.jsonl"))
    return sorted(root.glob("*/transcripts/*.jsonl"))


def build_transcript_prompts(root: Path, transcript_files: list[Path]) -> list[dict[str, Any]]:
    prompts: list[dict[str, Any]] = []
    for path in transcript_files:
        app = app_for_path(root, path)
        ordinal = 0
        for item in read_jsonl(path):
            if item.get("type") != "user":
                continue
            message = item.get("message")
            if not isinstance(message, dict):
                continue
            content = message.get("content")
            if not isinstance(content, list):
                continue
            for block in content:
                if not isinstance(block, dict) or block.get("type") != "text":
                    continue
                text = str(block.get("text") or "").strip()
                if not text:
                    continue
                ordinal += 1
                prompts.append(
                    {
                        "sample_id": f"{app}:{path.stem}:transcript-user-{ordinal}",
                        "source": "kitsoki_embedded_agent_transcript",
                        "app": app,
                        "transcript_path": str(path),
                        "session_id": item.get("session_id") or "",
                        "ordinal": ordinal,
                        "input": text,
                        "route_labeled": False,
                        "note": "Real kitsoki-dev agent transcript prompt; not a Kitsoki router gold label.",
                    }
                )
    return prompts


def write_jsonl(path: Path, rows: list[dict[str, Any]]) -> None:
    with path.open("w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row, sort_keys=True, ensure_ascii=False) + "\n")


def write_summary(path: Path, root: Path, args: argparse.Namespace, samples: list[dict[str, Any]], prompts: list[dict[str, Any]]) -> None:
    by_app = Counter(row["app"] for row in samples)
    non_empty_by_app = Counter(row["app"] for row in samples if str(row.get("input") or "").strip())
    by_intent = Counter(row["expected"]["intent"] for row in samples if row.get("expected"))
    summary = {
        "root": str(root),
        "app_filter": args.app,
        "include_empty": args.include_empty,
        "routing_samples": len(samples),
        "routing_samples_non_empty": sum(1 for row in samples if str(row.get("input") or "").strip()),
        "routing_samples_context_dependent": sum(1 for row in samples if row.get("context_dependent")),
        "routing_samples_by_app": dict(sorted(by_app.items())),
        "routing_non_empty_by_app": dict(sorted(non_empty_by_app.items())),
        "top_intents": by_intent.most_common(30),
        "embedded_transcript_prompts": len(prompts),
        "embedded_transcript_prompts_by_app": dict(sorted(Counter(row["app"] for row in prompts).items())),
        "limits": [
            "routing_samples are gold-labeled from Kitsoki turn.input + machine.transition pairs",
            "intent fixtures exclude context-dependent confirmation inputs by default because their gold route depends on prior room/world context",
            "embedded transcript prompts are real model-decision prompts but are not router labels",
        ],
    }
    path.write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def fixture_slug(value: str) -> str:
    value = re.sub(r"[^a-zA-Z0-9_.-]+", "-", value).strip("-")
    return value or "sample"


def yaml_scalar(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def write_intent_fixtures(out_dir: Path, samples: list[dict[str, Any]], include_contextual: bool = False) -> int:
    grouped: dict[tuple[str, str], list[dict[str, Any]]] = {}
    for sample in samples:
        if sample.get("context_dependent") and not include_contextual:
            continue
        expected = sample.get("expected")
        if not isinstance(expected, dict) or not expected.get("intent"):
            continue
        app = str(sample.get("app") or "unknown")
        state = str(sample.get("state") or "")
        if not state:
            continue
        grouped.setdefault((app, state), []).append(sample)

    fixtures_dir = out_dir / "intent-fixtures"
    count = 0
    for (app, state), rows in sorted(grouped.items()):
        app_dir = fixtures_dir / fixture_slug(app)
        app_dir.mkdir(parents=True, exist_ok=True)
        path = app_dir / f"{fixture_slug(state)}.yaml"
        lines = [
            "# Generated from real Kitsoki EventSink traces by tools/session-mining/kitsoki_trace_corpus.py.",
            "# Review before committing; generated fixtures intentionally preserve observed routing outputs.",
            "# Context-dependent confirmations are excluded by default; rerun with --include-contextual-fixtures to include them.",
            "test_kind: intents",
            f"app: {yaml_scalar(app)}",
            f"state: {yaml_scalar(state)}",
            "defaults:",
            "  runs: 1",
            "  min_pass_rate: 1.0",
            "  temperature: 0.0",
            "fixtures:",
        ]
        for row in rows:
            expected = row["expected"]
            fixture_id = fixture_slug(row["sample_id"])
            slots = expected.get("slots") or {}
            lines.extend(
                [
                    f"  - id: {yaml_scalar(fixture_id)}",
                    "    intent:",
                    f"      name: {yaml_scalar(str(expected.get('intent') or ''))}",
                    f"      slots: {json.dumps(slots, sort_keys=True, ensure_ascii=False)}",
                    "    inputs:",
                    f"      - {yaml_scalar(str(row.get('input') or ''))}",
                    f"    # source: {row.get('trace_path')} turn {row.get('turn')}",
                ]
            )
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")
        count += 1
    return count


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=default_sessions_root())
    parser.add_argument("--app", help="limit to one app directory, e.g. kitsoki-dev")
    parser.add_argument("--include-empty", action="store_true", help="include empty/menu-driven turn.input samples")
    parser.add_argument("--include-transcripts", action="store_true", help="also export embedded Claude transcript user prompts")
    parser.add_argument("--emit-intent-fixtures", action="store_true", help="write kitsoki test intents YAML fixtures grouped by app and state")
    parser.add_argument("--include-contextual-fixtures", action="store_true", help="include context-dependent confirmations in generated intent fixtures")
    parser.add_argument("--out-dir", type=Path, required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = args.root.expanduser().resolve()
    out_dir = args.out_dir.resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    trace_files = iter_trace_files(root, args.app)
    samples = build_samples(root, trace_files, include_empty=args.include_empty)
    prompts: list[dict[str, Any]] = []
    if args.include_transcripts:
        prompts = build_transcript_prompts(root, iter_transcript_files(root, args.app))

    write_jsonl(out_dir / "routing-samples.jsonl", samples)
    if args.include_transcripts:
        write_jsonl(out_dir / "embedded-transcript-prompts.jsonl", prompts)
    fixture_files = 0
    if args.emit_intent_fixtures:
        fixture_files = write_intent_fixtures(out_dir, samples, include_contextual=args.include_contextual_fixtures)
    write_summary(out_dir / "summary.json", root, args, samples, prompts)

    print(f"routing_samples={len(samples)}")
    print(f"routing_samples_non_empty={sum(1 for row in samples if str(row.get('input') or '').strip())}")
    print(f"embedded_transcript_prompts={len(prompts)}")
    print(f"intent_fixture_files={fixture_files}")
    print(f"out_dir={out_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
