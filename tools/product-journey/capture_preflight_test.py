#!/usr/bin/env python3
"""Runner-level test for --capture-preflight.

Run directly:  python3 tools/product-journey/capture_preflight_test.py

The real preflight uses kitsoki web-shot. This test injects tiny local commands
so it never launches Chromium, a live server, or an LLM.
"""

import importlib.util
import json
import shlex
import sys
import tempfile
import datetime
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
        tmp = Path(tmp)
        run.ARTIFACT_ROOT = tmp / "product-journey"
        run.PREFLIGHT_ROOT = run.ARTIFACT_ROOT / "preflights"

        pass_script = tmp / "fake_webshot_pass.py"
        pass_script.write_text(
            "import os\n"
            "from pathlib import Path\n"
            "Path(os.environ['KITSOKI_CAPTURE_PREFLIGHT_OUT']).write_bytes(b'fake-png')\n"
            "print('captured')\n",
            encoding="utf-8",
        )
        studio_pass = tmp / "fake_studio_pass.py"
        studio_pass.write_text("print('studio ok')\n", encoding="utf-8")
        quota_state = tmp / "quota.json"
        quota_state.write_text(
            json.dumps({"schema": "kitsoki/provider-quota/v1", "profiles": {}}),
            encoding="utf-8",
        )
        result = run.capture_preflight(
            "pass",
            f"{shlex.quote(sys.executable)} {shlex.quote(str(pass_script))}",
            10,
            f"{shlex.quote(sys.executable)} {shlex.quote(str(studio_pass))}",
            str(quota_state),
        )
        _check("pass status", result["status"] == "passed")
        _check("pass counts", result["passed"] == 6 and result["failed"] == 0)
        _check("records studio ping check", any(check["id"] == "studio-ping" and check["status"] == "passed" for check in result["checks"]))
        _check("records quota window check", any(check["id"] == "quota-window" and check["status"] == "passed" for check in result["checks"]))
        _check("writes preflight json", Path(result["preflight_path"]).exists())
        _check("writes preflight markdown", Path(result["markdown_path"]).exists())
        _check("writes webshot output", Path(result["webshot_output"]).exists())

        fail_script = tmp / "fake_webshot_fail.py"
        fail_script.write_text(
            "import sys\n"
            "print('capture failed', file=sys.stderr)\n"
            "sys.exit(7)\n",
            encoding="utf-8",
        )
        failed = run.capture_preflight(
            "fail",
            f"{shlex.quote(sys.executable)} {shlex.quote(str(fail_script))}",
            10,
            f"{shlex.quote(sys.executable)} {shlex.quote(str(studio_pass))}",
            str(quota_state),
        )
        _check("fail status", failed["status"] == "failed")
        _check("fail records command exit", failed["exit_code"] == 7)
        _check("fail records missing output", any(check["id"] == "webshot-output" and check["status"] == "failed" for check in failed["checks"]))
        quota_blocked = tmp / "quota-blocked.json"
        quota_blocked.write_text(
            json.dumps({
                "schema": "kitsoki/provider-quota/v1",
                "profiles": {
                    "default|provider|model|ambient": {
                        "backoff_until": (
                            datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(hours=1)
                        ).isoformat()
                    }
                },
            }),
            encoding="utf-8",
        )
        blocked = run.capture_preflight(
            "quota",
            f"{shlex.quote(sys.executable)} {shlex.quote(str(pass_script))}",
            10,
            f"{shlex.quote(sys.executable)} {shlex.quote(str(studio_pass))}",
            str(quota_blocked),
        )
        _check("quota cooldown fails preflight", blocked["status"] == "failed")
        _check("quota cooldown is recorded", any(check["id"] == "quota-window" and check["status"] == "failed" for check in blocked["checks"]))

    print("PASS")


if __name__ == "__main__":
    main()
