#!/usr/bin/env python3
"""Run a command with a wall-clock timeout and kill its process tree on expiry."""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import time


def positive_int(raw: str) -> int:
    try:
        value = int(raw)
    except ValueError as exc:
        raise argparse.ArgumentTypeError(f"not an integer: {raw!r}") from exc
    if value <= 0:
        raise argparse.ArgumentTypeError("must be > 0")
    return value


def terminate_group(proc: subprocess.Popen[bytes], grace_seconds: int) -> None:
    try:
        os.killpg(proc.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    deadline = time.monotonic() + grace_seconds
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            return
        time.sleep(0.1)
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except ProcessLookupError:
        return


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timeout", type=positive_int, required=True, help="wall-clock timeout in seconds")
    parser.add_argument("--grace", type=positive_int, default=5, help="SIGTERM grace period before SIGKILL")
    parser.add_argument("--label", default="command", help="human-readable command label")
    parser.add_argument("command", nargs=argparse.REMAINDER, help="command to run after --")
    args = parser.parse_args(argv)

    command = args.command
    if command and command[0] == "--":
        command = command[1:]
    if not command:
        parser.error("missing command after --")

    try:
        proc = subprocess.Popen(command, start_new_session=True)
    except FileNotFoundError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 127

    try:
        return proc.wait(timeout=args.timeout)
    except subprocess.TimeoutExpired:
        print(
            f"\nTIMEOUT: {args.label} exceeded {args.timeout}s; terminating process group",
            file=sys.stderr,
        )
        terminate_group(proc, args.grace)
        return 124


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
