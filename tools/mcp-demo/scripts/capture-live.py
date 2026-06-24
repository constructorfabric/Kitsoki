#!/usr/bin/env python3
"""capture-live.py — the ONE gated live capture for "record once, replay forever".

Spawns a command (a real `claude` session driving the kitsoki MCP server) under a
pty and records its terminal output as an asciicast-v2 `.cast` file (the open
asciinema format — a header line then `[time, "o", data]` event lines). No external
deps: python3 stdlib only (`pty`, `os`, `select`, `tty`).

This is the SOLE step in the MCP demo that involves a real LLM, so it is gated:
run it deliberately, with explicit go-ahead, never in CI / the default suite
(AGENTS.md: tests never use a real LLM; live is opt-in only). The captured `.cast`
is then segmented into a `termcast` (see scripts/segment-cast.mjs + casts/types.ts)
and committed; from then on the demo replays that cassette for free, forever.

Usage:
  python3 scripts/capture-live.py --out casts/claude-code-live.cast -- \
      claude --mcp-config .mcp.json \
             --allowedTools 'mcp__kitsoki__*' \
             "Build a tiny barista story over the kitsoki MCP server: author it, \
              validate the graph, run its flows, then drive a session and show the TUI."

Interactive runs work too (drop the trailing prompt and drive `claude` by hand);
the recorder is transparent — your terminal behaves normally and Ctrl-D / exit ends
the capture.
"""
import argparse
import json
import os
import pty
import select
import shutil
import sys
import termios
import time
import tty


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, help="output .cast path (asciicast v2)")
    ap.add_argument("cmd", nargs=argparse.REMAINDER,
                    help="-- then the command to record (e.g. -- claude ...)")
    args = ap.parse_args()
    cmd = args.cmd[1:] if args.cmd and args.cmd[0] == "--" else args.cmd
    if not cmd:
        ap.error("provide a command after `--`, e.g. -- claude --mcp-config .mcp.json ...")

    cols, rows = shutil.get_terminal_size((116, 32))
    header = {"version": 2, "width": cols, "height": rows,
              "timestamp": int(time.time()), "env": {"TERM": os.environ.get("TERM", "xterm-256color")}}

    pid, fd = pty.fork()
    if pid == 0:  # child: exec the recorded command
        os.execvp(cmd[0], cmd)
        os._exit(127)

    # parent: relay stdin↔pty, tee pty output to the .cast with timestamps.
    t0 = time.time()
    events: list[list] = []
    old = None
    try:
        old = termios.tcgetattr(sys.stdin)
        tty.setraw(sys.stdin)
    except Exception:
        old = None  # not a tty (piped) — fine, just no raw mode
    try:
        while True:
            try:
                r, _, _ = select.select([fd, sys.stdin], [], [])
            except (InterruptedError, OSError):
                break
            if fd in r:
                try:
                    data = os.read(fd, 65536)
                except OSError:
                    break
                if not data:
                    break
                os.write(sys.stdout.fileno(), data)
                events.append([round(time.time() - t0, 4), "o",
                               data.decode("utf-8", "replace")])
            if sys.stdin in r:
                try:
                    inp = os.read(sys.stdin.fileno(), 65536)
                except OSError:
                    inp = b""
                if inp:
                    os.write(fd, inp)
    finally:
        if old is not None:
            termios.tcsetattr(sys.stdin, termios.TCSADRAIN, old)
        try:
            os.waitpid(pid, 0)
        except OSError:
            pass

    os.makedirs(os.path.dirname(os.path.abspath(args.out)) or ".", exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as f:
        f.write(json.dumps(header) + "\n")
        for ev in events:
            f.write(json.dumps(ev) + "\n")
    sys.stderr.write(f"\n[capture-live] wrote {args.out} ({len(events)} events, "
                     f"{round(time.time()-t0,1)}s)\n")
    sys.stderr.write("[capture-live] next: segment into a termcast — "
                     "node scripts/segment-cast.mjs " + args.out + " > casts/claude-code-live.json\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
