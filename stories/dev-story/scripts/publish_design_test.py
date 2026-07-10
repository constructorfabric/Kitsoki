#!/usr/bin/env python3
"""No-LLM regression tests for design publishing helpers."""

from __future__ import annotations

import importlib.util
import json
import os
import sys
import tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
SCRIPT = HERE / "publish_design.py"

failures: list[str] = []


def check(label: str, condition: bool, detail: str = "") -> None:
    if not condition:
        failures.append(f"{label}{': ' + detail if detail else ''}")


def load_publish_design():
    spec = importlib.util.spec_from_file_location("publish_design", SCRIPT)
    if spec is None or spec.loader is None:
        raise RuntimeError("could not load publish_design.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def fake_kitsoki(record: Path) -> Path:
    root = Path(tempfile.mkdtemp(prefix="kitsoki-publish-design-"))
    path = root / "kitsoki"
    path.write_text(
        "#!/usr/bin/env python3\n"
        "import json, os, sys\n"
        "with open(os.environ['KITSOKI_ARG_RECORD'], 'w') as f:\n"
        "    json.dump(sys.argv[1:], f)\n"
        "print('https://github.com/o/r/issues/456')\n",
        encoding="utf-8",
    )
    path.chmod(0o755)
    return path


mod = load_publish_design()
record = Path(tempfile.mkdtemp(prefix="kitsoki-publish-design-record-")) / "args.json"
old_bin = os.environ.get("KITSOKI_BIN")
old_record = os.environ.get("KITSOKI_ARG_RECORD")
try:
    os.environ["KITSOKI_BIN"] = str(fake_kitsoki(record))
    os.environ["KITSOKI_ARG_RECORD"] = str(record)
    number, url = mod.file_feature_issue_github(
        "new-widget",
        "New Widget",
        "Make the widget configurable.",
        "docs/proposals/new-widget.md",
        "o/r",
    )
finally:
    if old_bin is None:
        os.environ.pop("KITSOKI_BIN", None)
    else:
        os.environ["KITSOKI_BIN"] = old_bin
    if old_record is None:
        os.environ.pop("KITSOKI_ARG_RECORD", None)
    else:
        os.environ["KITSOKI_ARG_RECORD"] = old_record

args = json.loads(record.read_text(encoding="utf-8"))
check("returned issue number", number == "456", number)
check("returned issue url", url == "https://github.com/o/r/issues/456", url)
check("uses kitsoki bug create", args[:2] == ["bug", "create"], str(args))
check("passes github repo", "--github" in args and args[args.index("--github") + 1] == "o/r", str(args))
check("does not invoke gh", "gh" not in args[:1], str(args))

if failures:
    for failure in failures:
        print("FAIL:", failure)
    sys.exit(1)
print("PASS")
