#!/usr/bin/env python3
"""codex_adapter.py — turn a codex CLI/TUI rollout (`~/.codex/sessions/**/*.jsonl`)
into the SAME two intermediate shapes the Claude Code pipeline already consumes,
so nothing downstream (ground.py, tag_score.py, emit.py, redact.py) has to know
or care which agent produced the transcript:

  1. a distilled trace: one line per event, `USER: ...` / `AI: ...` /
     `  > Tool: arg` — byte-for-byte the same LINE GRAMMAR distill.jq emits for
     Claude Code, so ground.py's citation parser (`intent_common.parse_tool_line`)
     and emit.py's `tool_ordinal` join work unmodified.
  2. an outcomes join: `{is_error, stdout_head, stderr_head, interrupted}` per
     tool call, ordered the same way `outcomes.py` orders Claude Code's, so
     `emit.py --outcomes` (which only ever reads `outcomes.json` + counts
     `  > ` lines) is unmodified too.

Why a separate module instead of teaching prep.py/outcomes.py a second format:
those files' inner loops are keyed on Claude Code's raw shapes (`type=="user"`
with `message.content` blocks, `tool_use`/`tool_result` id pairing). A codex
rollout's raw shape is different enough (see below) that bolting an `if
source == "codex"` branch into every function would make BOTH formats harder to
read. Keeping the codex-specific extraction here, emitting into the exact same
line grammar / JSON schema, is what makes "unchanged downstream" literally true
of ground.py/tag_score.py/emit.py/redact.py — none of the four is touched by
this file's existence.

Real corpus shape (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`), confirmed by
reading real rollouts before writing this: one JSON object per line,
`{timestamp, type, payload}`. Relevant `type`s:

  session_meta   payload.originator: "codex-tui"|"cli"|"vscode"|"codex_exec"|"exec"
                 (cwd, session_id, cli_version). ALWAYS the first line.
  event_msg      payload.type in:
                   user_message   payload.message   (the human's verbatim ask)
                   agent_message  payload.message   (the model's prose)
                   patch_apply_end  payload.{call_id, success}
                   task_started / task_complete / token_count / turn_context (skip)
  response_item  payload.type in:
                   function_call         {name, arguments (JSON string), call_id}
                   function_call_output  {call_id, output}
                   custom_tool_call        {name, input (raw string), call_id}
                   custom_tool_call_output {call_id, output}
                   message (role="developer"/"user", harness-injected boilerplate;
                            NOT the human's text — real user text lives in the
                            event_msg.user_message line above) / reasoning (skip)

`payload.originator` is codex's analogue of Claude Code's `entrypoint`: sessions
launched by `codex exec` (`codex_exec`/`exec`) are HEADLESS DISPATCHED AGENT
RUNS — several observed in this corpus are kitsoki's own sub-agents. Mining
those is the same self-cannibalism prep.py already guards against for Claude
Code; `is_agent_originator` is the equivalent filter, keyed on the field that
actually carries this signal for codex (originator, not entrypoint).

Stdlib only. No LLM. Deterministic.
"""
import json
import os
import re

AGENT_ORIGINATORS = {"codex_exec", "exec"}

# arguments/input keys distill.jq-equivalent picks as "the interesting argument"
# for a tool call, in priority order (mirrors distill.jq's command // description
# // file_path // prompt // query fallback chain for Claude Code tool_use blocks).
_ARG_KEYS = ("cmd", "command", "description", "file_path", "path", "prompt", "query")

_WS = re.compile(r"\s+")
_EXIT_CODE_RE = re.compile(r"(?:Process exited with code|Exit code:)\s*(-?\d+)", re.I)
_TIMEOUT_RE = re.compile(r"timed out|timeout", re.I)


def _collapse(text):
    return _WS.sub(" ", text or "")


def iter_records(path):
    """Yield parsed JSON objects from a rollout, skipping unparsable lines."""
    with open(path, "r", errors="ignore") as fh:
        for raw in fh:
            raw = raw.strip()
            if not raw:
                continue
            try:
                yield json.loads(raw)
            except (json.JSONDecodeError, ValueError):
                continue


def session_originator(path):
    """`payload.originator` off the session's FIRST record (always session_meta
    in real rollouts), or None if absent/undeterminable. Scans a few more lines
    defensively in case session_meta isn't literally line 1."""
    for i, obj in enumerate(iter_records(path)):
        if obj.get("type") == "session_meta":
            return (obj.get("payload") or {}).get("originator")
        if i >= 5:
            break
    return None


def is_agent_session(path):
    """True if this rollout is a headless dispatched-agent run (codex exec),
    not an interactive human session. Structural (originator), not content."""
    orig = session_originator(path)
    return orig is not None and orig in AGENT_ORIGINATORS


def find_session_files(root):
    """Recursively find rollout-*.jsonl under root (codex nests by YYYY/MM/DD)."""
    out = []
    for dirpath, _dirnames, filenames in os.walk(root):
        for fn in filenames:
            if fn.endswith(".jsonl"):
                out.append(os.path.join(dirpath, fn))
    out.sort()
    return out


def session_id(path):
    """Stable session id: the rollout filename minus its .jsonl extension —
    already unique (timestamp + uuid) so this is a 1:1 stand-in for Claude
    Code's <session-id>.jsonl basename convention."""
    return os.path.basename(path)[: -len(".jsonl")]


def _arg_for_function_call(arguments_raw):
    """Pick the 'interesting' argument out of a function_call's JSON arguments
    string, mirroring distill.jq's command//description//file_path//... chain.
    Falls back to the raw JSON (or the raw string, if it isn't JSON at all)."""
    if not arguments_raw:
        return ""
    try:
        parsed = json.loads(arguments_raw)
    except (json.JSONDecodeError, ValueError, TypeError):
        return str(arguments_raw)
    if isinstance(parsed, dict):
        for k in _ARG_KEYS:
            if k in parsed and parsed[k]:
                return str(parsed[k])
        return json.dumps(parsed)
    return str(parsed)


def distill_records(records):
    """Distill an already-parsed record stream into (lines, tool_call_ids).

    lines: list[str] — the SAME line grammar distill.jq emits (USER:/AI:/  > ),
    one entry per emitted line, in document order.
    tool_call_ids: list[str|None] — parallel-ordered list of the call_id behind
    each `  > Tool: arg` line (None if the record carried no call_id), used to
    build the outcomes join in the exact ordinal order emit.py's tool_ordinal
    counts `  > ` lines in.
    """
    lines = []
    tool_call_ids = []
    for obj in records:
        typ = obj.get("type")
        payload = obj.get("payload") or {}
        ptype = payload.get("type")

        if typ == "event_msg" and ptype == "user_message":
            text = payload.get("message")
            if text:
                lines.append("USER: " + _collapse(text)[:600])
        elif typ == "event_msg" and ptype == "agent_message":
            text = _collapse(payload.get("message"))
            if len(text) > 8:
                lines.append("AI: " + text[:180])
        elif typ == "response_item" and ptype == "function_call":
            name = payload.get("name") or "?"
            arg = _arg_for_function_call(payload.get("arguments"))
            lines.append("  > " + name + ": " + _collapse(arg)[:200])
            tool_call_ids.append(payload.get("call_id"))
        elif typ == "response_item" and ptype == "custom_tool_call":
            name = payload.get("name") or "?"
            arg = payload.get("input") or ""
            lines.append("  > " + name + ": " + _collapse(str(arg))[:200])
            tool_call_ids.append(payload.get("call_id"))
        # everything else (reasoning, message/developer boilerplate,
        # function_call_output, custom_tool_call_output, patch_apply_end,
        # token_count, task_started/complete, turn_context, session_meta) is
        # either not a genuine turn/tool-call or belongs to the outcomes pass
        # below, not the distilled trace.
    return lines, tool_call_ids


def distill_session(path):
    """distill_records over one rollout file on disk."""
    return distill_records(iter_records(path))


def _outcome_from_text(output_text, success, stdout_head_n):
    """Build the {is_error, stdout_head, stderr_head, interrupted} entry from a
    function_call_output/custom_tool_call_output's `output` text (and, for
    apply_patch, the sibling patch_apply_end event's `success` bool, which is
    the more reliable signal when present).

    codex's exec_command wraps output as
      "...\\nProcess exited with code N\\n...\\nOutput:\\n<real output>"
    and custom_tool_call_output (apply_patch) as "Exit code: N\\nOutput:\\n...".
    Neither shape separates stdout/stderr, so both go in stdout_head (stderr_head
    stays "" — an honest gap, not a fabricated split) and interrupted is a
    lexical "timed out" scan (codex reports MCP/tool timeouts as prose in the
    output text, not a structured flag like Claude Code's toolUseResult).

    KNOWN FALSE POSITIVE (found calibrating against real rollouts): a grep/rg
    tool call whose OWN matched content happens to contain the word "timeout"
    (e.g. grepping docs/memory notes that discuss timeouts) sets
    interrupted=True even though the call itself completed normally (exit code
    0, no truncation). Same epistemic class as emit.py's documented
    satisfaction heuristics -- review flag, not a verdict; a real fix would
    need codex to expose a structured signal the way Claude Code's
    toolUseResult.interrupted does.
    """
    text = output_text or ""
    is_error = False
    m = _EXIT_CODE_RE.search(text)
    if m:
        is_error = int(m.group(1)) != 0
    if success is not None:
        is_error = not success
    interrupted = bool(_TIMEOUT_RE.search(text))
    return {
        "is_error": bool(is_error),
        "stdout_head": text[:stdout_head_n],
        "stderr_head": "",
        "interrupted": interrupted,
    }


def extract_outcomes(records, stdout_head_n=600):
    """Return {call_id: outcome_dict} for every function_call_output /
    custom_tool_call_output (+ patch_apply_end success override) in records."""
    outputs = {}
    successes = {}
    for obj in records:
        typ = obj.get("type")
        payload = obj.get("payload") or {}
        ptype = payload.get("type")
        if typ == "response_item" and ptype in ("function_call_output", "custom_tool_call_output"):
            cid = payload.get("call_id")
            if cid:
                outputs[cid] = payload.get("output", "")
        elif typ == "event_msg" and ptype == "patch_apply_end":
            cid = payload.get("call_id")
            if cid:
                successes[cid] = payload.get("success")
    result = {}
    for cid, text in outputs.items():
        result[cid] = _outcome_from_text(text, successes.get(cid), stdout_head_n)
    # a call whose patch_apply_end arrived with no matching custom_tool_call_output
    # (rare, but keep the success signal from being silently dropped)
    for cid, success in successes.items():
        if cid not in result:
            result[cid] = _outcome_from_text("", success, stdout_head_n)
    return result


def session_tool_outcomes(records, stdout_head_n=600):
    """Full per-session outcomes.py-shaped result: {"join": "id",
    "tool_outcomes": [...]} ordered to match distill_records' tool_call_ids.

    codex's function_call/custom_tool_call carry a real call_id, so the join is
    always id-based (never the positional fallback Claude Code sometimes needs);
    a call_id with no matching output (interrupted/abandoned mid-call) becomes a
    null entry, same contract as outcomes.py section on the id-join regime.
    """
    records = list(records)
    _, tool_call_ids = distill_records(records)
    by_id = extract_outcomes(records, stdout_head_n)
    tool_outcomes = [by_id.get(cid) if cid else None for cid in tool_call_ids]
    return {"join": "id", "tool_outcomes": tool_outcomes}
