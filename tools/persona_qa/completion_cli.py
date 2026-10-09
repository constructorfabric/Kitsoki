"""CLI entrypoint for visual-QA/persona-QA completion-state artifacts."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Sequence

from .completion import CompletionState, load_product_journey_run
from .ui_verdict import load_ui_qa_verdict, load_ui_review_verdict

KIND_HELP = {
    "ui-qa": "kitsoki-ui-qa verdict.json (journey-verdict)",
    "ui-review": "kitsoki-ui-review verdict.json (ux-heuristic)",
    "product-journey-run": "product-journey run directory",
}


def load_completion(kind: str, input_path: str | Path) -> CompletionState:
    """Load one supported persona-QA artifact as a CompletionState."""

    path = Path(input_path)
    if kind == "ui-qa":
        return load_ui_qa_verdict(path)
    if kind == "ui-review":
        return load_ui_review_verdict(path)
    if kind == "product-journey-run":
        return load_product_journey_run(path)
    raise ValueError(f"unknown completion artifact kind {kind!r}")


def write_completion(state: CompletionState, out_path: str | Path | None) -> str:
    """Write `state` to `out_path`, or return the JSON for stdout."""

    data = state.to_json()
    if out_path:
        out = Path(out_path)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(data, encoding="utf-8")
    return data


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="python3 -m tools.persona_qa",
        description=(
            "Convert an already-produced persona/visual-QA artifact into the "
            "shared completion-state JSON contract. Pure adapter: no LLM, no "
            "browser, no recording."
        ),
    )
    parser.add_argument(
        "--kind",
        required=True,
        choices=sorted(KIND_HELP),
        help="input artifact kind: " + "; ".join(f"{k}={v}" for k, v in sorted(KIND_HELP.items())),
    )
    parser.add_argument(
        "--input",
        required=True,
        help="verdict.json path for ui-qa/ui-review, or run directory for product-journey-run",
    )
    parser.add_argument(
        "--out",
        default="",
        help="write completion-state JSON here; stdout is used when omitted",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    state = load_completion(args.kind, args.input)
    data = write_completion(state, args.out or None)
    if not args.out:
        sys.stdout.write(data)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
