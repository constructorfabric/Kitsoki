#!/usr/bin/env python3
"""Shared helpers for the intent-mining deterministic spine (steps C-F).

Stdlib only. Holds the trace/line primitives, vocab parsing, and the oracle/job
shapes so ground.py, tag_score.py, and emit.py stay small and DI-friendly.

The oracle output (step B, the one LLM pass) is the only input these steps trust,
and they trust it ONLY after grounding against the deterministic trace. See
README "Intent mining" and review §3.
"""
import json
import os
import re

SCHEMA_VERSION = "1.0"
HERE = os.path.dirname(os.path.abspath(__file__))


# ---- trace primitives -------------------------------------------------------

TOOL_LINE = re.compile(r"^\s*>\s*(?P<tool>\S+):\s*(?P<arg>.*)$")


def read_trace_lines(path):
    """Return the trace's lines as a 1-based list (index 0 unused/None).

    The oracle cites 1-based line numbers into traces/<sid>.txt; keeping the list
    1-based means cite['line'] indexes directly without off-by-one juggling.
    """
    with open(path, "r", errors="ignore") as fh:
        lines = fh.read().splitlines()
    return [None] + lines  # 1-based


def parse_tool_line(line):
    """Parse a `  > Tool: arg` trace line -> (tool, arg) or (None, None)."""
    if line is None:
        return (None, None)
    m = TOOL_LINE.match(line)
    if not m:
        return (None, None)
    return (m.group("tool"), m.group("arg"))


# ---- vocab parsing (no pyyaml; mirrors report.py's minimal parser) ----------

def load_tag_vocab(path):
    """Parse vocab/tags.yaml -> {tags_version, dimensions: {dim: set(members)}}.

    Minimal indentation-aware reader (stdlib only). Recognizes the shape:
        tags_version: "..."
        dimensions:
          <dim>:
            members:
              - <member>
    """
    out = {"tags_version": None, "dimensions": {}}
    if not path or not os.path.exists(path):
        return out
    cur_dim = None
    in_members = False
    for raw in open(path).read().splitlines():
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        m = re.match(r'^tags_version:\s*"?([^"]+)"?\s*$', raw)
        if m:
            out["tags_version"] = m.group(1).strip()
            continue
        # a dimension header: exactly 2-space indent, "<name>:"
        m = re.match(r"^  (\w[\w-]*):\s*$", raw)
        if m:
            cur_dim = m.group(1)
            out["dimensions"].setdefault(cur_dim, set())
            in_members = False
            continue
        if re.match(r"^    members:\s*$", raw):
            in_members = True
            continue
        m = re.match(r"^      -\s*(\S+)\s*$", raw)
        if m and cur_dim and in_members:
            out["dimensions"][cur_dim].add(m.group(1))
    return out


def default_tags_vocab_path():
    return os.path.join(HERE, "vocab", "tags.yaml")


# ---- io ---------------------------------------------------------------------

def load_json(path):
    with open(path) as fh:
        return json.load(fh)


def dump_json(obj, path):
    with open(path, "w") as fh:
        json.dump(obj, fh, indent=2)
        fh.write("\n")
