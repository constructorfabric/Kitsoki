#!/usr/bin/env python3
"""Regression test for the cassette:// unbacked-proof hole.

Run directly:  python3 tools/product-journey/cassette_proof_test.py

A cassette:// evidence URI is a LOCAL recorded artifact, not a remote URL, so
it must resolve to a real backing file before it counts as proof or as
"resolving" for the review gate. Before the fix, both artifact_ref_exists and
is_proof_evidence waved through `cassette://…/nothing.diff`, letting the review
gate read `ready` with nothing on disk.
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
        rd = Path(tmp)

        unbacked = "cassette://product-journey/run-x/diffs/nothing.diff"
        backed_rel = "diffs/real.diff"
        backed_uri = "cassette://product-journey/run-x/diffs/real.diff"
        (rd / "diffs").mkdir(parents=True)
        (rd / backed_rel).write_text("diff --git a/x b/x\n", encoding="utf-8")

        # 1. Unbacked cassette ref does NOT resolve.
        _check("unbacked cassette does not resolve",
               run.artifact_ref_exists(rd, unbacked) is False)
        # 2. Backed cassette ref DOES resolve.
        _check("backed cassette resolves",
               run.artifact_ref_exists(rd, backed_uri) is True)
        # 3. Remote schemes stay unverifiable-but-present.
        _check("https stays present", run.artifact_ref_exists(rd, "https://x/y.png") is True)
        _check("retained stays present", run.artifact_ref_exists(rd, "retained://abc") is True)

        unbacked_item = {"status": "captured", "source": "cassette", "path": unbacked}
        backed_item = {"status": "captured", "source": "cassette", "path": backed_uri}
        external_item = {"status": "captured", "source": "external", "path": "https://x/y.png"}

        # 4. With a run_dir (gating call sites), unbacked cassette is NOT proof.
        _check("unbacked cassette is not proof (gated)",
               run.is_proof_evidence(unbacked_item, rd) is False)
        # 5. Backed cassette IS proof.
        _check("backed cassette is proof (gated)",
               run.is_proof_evidence(backed_item, rd) is True)
        # 6. Remote external is proof on source alone (can't be stat'd).
        _check("external is proof (gated)",
               run.is_proof_evidence(external_item, rd) is True)
        # 7. Without run_dir (reporting call sites), legacy source-only behavior.
        _check("unbacked cassette source-only stays proof (no run_dir)",
               run.is_proof_evidence(unbacked_item) is True)
        # 8. A dangling local path is not proof when gated.
        _check("dangling local is not proof (gated)",
               run.is_proof_evidence(
                   {"status": "captured", "source": "local", "path": "evidence/missing.png"}, rd) is False)

    print("\nPASS: cassette proof regression")


if __name__ == "__main__":
    main()
