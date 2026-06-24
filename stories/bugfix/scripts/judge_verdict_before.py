#!/usr/bin/env python3
"""judge_verdict_before.py — BEFORE agent-split.

Invokes the judge by shelling out to `claude -p` directly.
Problems:
  - No structured output guarantee; parses freeform text with regex.
  - Not connected to the kitsoki session trace (KITSOKI_TRACE_FILE is
    not threaded through; decisions appear as opaque subprocess calls).
  - Verdict schema is reimplemented in client code and drifts from
    stories/bugfix/schemas/judge_verdict.json.
  - JSON-extraction is brittle: the first {...} block is assumed to be
    the verdict.

Usage:
    python judge_verdict_before.py <ticket_id> <artifact_title> <artifact_body>
"""

import json
import re
import subprocess
import sys
import textwrap


VERDICT_SCHEMA_INLINE = {
    "verdict":    ["pass", "fail", "uncertain"],
    "intent":     ["accept", "refine", "restart_from", "quit", "uncertain"],
    "reason":     "string",
    "confidence": "float 0-1",
}

PROMPT_TEMPLATE = textwrap.dedent("""\
    You are the LLM-judge for the validation artifact at the
    validating_awaiting_reply checkpoint of bug-fix run {ticket_id}.

    Artifact title: {artifact_title}

    Artifact body:
    {artifact_body}

    Decision criteria:
    - accept — outcome is pass; fix confirmed in full environment.
    - refine — outcome is fail_short; small follow-up needed.
    - restart_from — outcome is fail; proposal needs redraft.
    - quit — outcome is infra_error; unrecoverable.
    - uncertain — yield to a human.

    Respond with a JSON object only:
    {{"verdict": "...", "intent": "...", "reason": "...", "confidence": 0.0}}
""")


def call_claude(prompt: str) -> str:
    # kitsoki-ok: intentional out-of-trace use — this is the "before" demo file
    # showing the old anti-pattern. See judge_verdict_after.py for the rewrite.
    result = subprocess.run(
        ["claude", "-p", prompt],
        capture_output=True,
        text=True,
        check=False,
    )
    if result.returncode != 0:
        raise RuntimeError(f"claude exited {result.returncode}: {result.stderr.strip()}")
    return result.stdout


def extract_json(text: str) -> dict:
    # Naive: grab the first {...} block in the output.
    m = re.search(r"\{.*?\}", text, re.DOTALL)
    if not m:
        raise ValueError(f"no JSON found in claude output:\n{text}")
    return json.loads(m.group(0))


def validate_verdict(data: dict) -> None:
    required = {"verdict", "intent", "reason", "confidence"}
    missing = required - data.keys()
    if missing:
        raise ValueError(f"missing fields: {missing}")
    # Drifted enum — does not match judge_verdict.json exactly.
    valid_verdicts = {"pass", "fail", "uncertain", "pass_with_warnings"}
    if data["verdict"] not in valid_verdicts:
        raise ValueError(f"unknown verdict: {data['verdict']!r}")


def main() -> None:
    if len(sys.argv) < 4:
        print(f"usage: {sys.argv[0]} <ticket_id> <artifact_title> <artifact_body>", file=sys.stderr)
        sys.exit(1)

    ticket_id = sys.argv[1]
    artifact_title = sys.argv[2]
    artifact_body = sys.argv[3]

    prompt = PROMPT_TEMPLATE.format(
        ticket_id=ticket_id,
        artifact_title=artifact_title,
        artifact_body=artifact_body,
    )

    raw = call_claude(prompt)
    verdict = extract_json(raw)
    validate_verdict(verdict)

    print(json.dumps(verdict, indent=2))


if __name__ == "__main__":
    main()
