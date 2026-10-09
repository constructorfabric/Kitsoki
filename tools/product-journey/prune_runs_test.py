#!/usr/bin/env python3
"""Test for the --prune-runs retention policy (run.prune_runs).

Run directly:  python3 tools/product-journey/prune_runs_test.py

Verifies that every non-run-bundle sibling subtree under ARTIFACT_ROOT
(matrices/, dogfood/, target-proofs/, eval/, marathon-smokes/, preflights/)
is protected from the timestamped-run-dir sweep, alongside the existing
keep-count and "-final" retention behavior. Local temp dirs only; no live
LLM or GitHub calls.
"""

import importlib.util
import sys
import tempfile
from pathlib import Path

_spec = importlib.util.spec_from_file_location(
    "pj_run", str(Path(__file__).with_name("run.py"))
)
run = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(run)


def _check(name, cond):
    if not cond:
        print(f"FAIL: {name}")
        sys.exit(1)
    print(f"ok: {name}")


def main():
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp) / "product-journey"
        root.mkdir(parents=True)

        protected_dirs = [
            "matrices",
            "dogfood",
            "target-proofs",
            "eval",
            "marathon-smokes",
            "preflights",
        ]
        for name in protected_dirs:
            child = root / name
            child.mkdir()
            (child / f"{name}-marker.json").write_text("{}", encoding="utf-8")

        run_dir_names = [
            "20260701T000000Z-run-one",
            "20260702T000000Z-run-two",
            "20260703T000000Z-run-three",
            "20260704T000000Z-run-four-final",
        ]
        for name in run_dir_names:
            (root / name).mkdir()

        original_root = run.ARTIFACT_ROOT
        run.ARTIFACT_ROOT = root
        try:
            result = run.prune_runs(keep=1, dry_run=False)
        finally:
            run.ARTIFACT_ROOT = original_root

        remaining = {p.name for p in root.iterdir() if p.is_dir()}

        for name in protected_dirs:
            _check(f"protected subtree survives: {name}", (root / name).is_dir())
            _check(
                f"protected subtree contents untouched: {name}",
                (root / name / f"{name}-marker.json").exists(),
            )

        _check(
            "newest run dir kept",
            "20260703T000000Z-run-three" in remaining,
        )
        _check(
            "-final run dir kept regardless of age",
            "20260704T000000Z-run-four-final" in remaining,
        )
        _check(
            "older non-final run dirs pruned",
            "20260701T000000Z-run-one" not in remaining
            and "20260702T000000Z-run-two" not in remaining,
        )
        _check(
            "report kept list only covers run bundles, not protected subtrees",
            set(result["kept"]) == {
                "20260703T000000Z-run-three",
                "20260704T000000Z-run-four-final",
            },
        )
        _check(
            "report lists exactly the pruned run dirs",
            set(result["removed"]) == {"20260701T000000Z-run-one", "20260702T000000Z-run-two"},
        )

    print("PASS")


if __name__ == "__main__":
    main()
