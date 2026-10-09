"""Tiny stdlib YAML subset used when PyYAML is not installed.

The arena no-LLM tests only need the common manifest subset: mappings, lists,
scalars, inline ``[]``/``{}``, and ``>``/``|`` block strings.  When PyYAML is
installed, callers import that instead; this module keeps the offline tests
hermetic in minimal Python environments.
"""

from __future__ import annotations

import json
import re
from typing import Any


class YAMLError(Exception):
    pass


def safe_load(text: str) -> Any:
    if text is None:
        return None
    stripped = str(text).strip()
    if not stripped:
        return None
    if stripped[0] in "{[":
        try:
            return json.loads(stripped)
        except json.JSONDecodeError:
            pass
    lines = _logical_lines(str(text))
    if not lines:
        return None
    value, index = _parse_block(lines, 0, lines[0][0])
    if index < len(lines):
        raise YAMLError(f"unexpected content at line {lines[index][2]}")
    return value


def safe_dump(data: Any, sort_keys: bool = False, **_: Any) -> str:
    out = _dump_value(data, 0, sort_keys)
    return out if out.endswith("\n") else out + "\n"


def _logical_lines(text: str) -> list[tuple[int, str, int]]:
    out: list[tuple[int, str, int]] = []
    raw_lines = text.splitlines()
    i = 0
    while i < len(raw_lines):
        lineno = i + 1
        raw = raw_lines[i]
        i += 1
        if not raw.strip() or raw.lstrip().startswith("#"):
            continue
        indent = len(raw) - len(raw.lstrip(" "))
        content = raw[indent:].rstrip()
        if _has_unclosed_single_quote(content):
            parts = [content]
            while i < len(raw_lines):
                nxt = raw_lines[i]
                i += 1
                parts.append(nxt.strip())
                if not _has_unclosed_single_quote(" ".join(parts)):
                    break
            content = " ".join(parts)
        if (
            out
            and indent > out[-1][0]
            and not content.startswith("- ")
            and not _looks_like_key_value(content)
            and not _line_value(out[-1][1]) in {">", "|"}
        ):
            prev_indent, prev_content, prev_lineno = out[-1]
            out[-1] = (prev_indent, prev_content + " " + content.strip(), prev_lineno)
            continue
        out.append((indent, content, lineno))
    return out


def _has_unclosed_single_quote(s: str) -> bool:
    count = 0
    i = 0
    while i < len(s):
        if s[i] == "'":
            if i + 1 < len(s) and s[i + 1] == "'":
                i += 2
                continue
            count += 1
        i += 1
    return count % 2 == 1


def _line_value(content: str) -> str:
    if ":" not in content:
        return ""
    return content.split(":", 1)[1].strip()


def _parse_block(lines: list[tuple[int, str, int]], index: int, indent: int) -> tuple[Any, int]:
    if index >= len(lines):
        return None, index
    cur_indent, content, _ = lines[index]
    if cur_indent < indent:
        return None, index
    if cur_indent != indent:
        indent = cur_indent
    if content.startswith("- "):
        return _parse_list(lines, index, indent)
    return _parse_map(lines, index, indent)


def _parse_list(lines: list[tuple[int, str, int]], index: int, indent: int) -> tuple[list[Any], int]:
    items: list[Any] = []
    while index < len(lines):
        cur_indent, content, lineno = lines[index]
        if cur_indent != indent or not content.startswith("- "):
            break
        rest = content[2:].strip()
        index += 1
        if not rest:
            child, index = _parse_child(lines, index, indent)
            items.append(child)
            continue
        if _looks_like_key_value(rest):
            item: dict[str, Any] = {}
            key, raw = _split_key_value(rest, lineno)
            value, index = _parse_value_or_child(lines, index, indent, raw)
            item[key] = value
            if index < len(lines) and lines[index][0] > indent:
                more, index = _parse_map(lines, index, lines[index][0])
                if isinstance(more, dict):
                    item.update(more)
            items.append(item)
        else:
            items.append(_parse_scalar(rest))
    return items, index


def _parse_map(lines: list[tuple[int, str, int]], index: int, indent: int) -> tuple[dict[str, Any], int]:
    result: dict[str, Any] = {}
    while index < len(lines):
        cur_indent, content, lineno = lines[index]
        if cur_indent < indent:
            break
        if cur_indent > indent:
            raise YAMLError(f"unexpected indentation at line {lineno}")
        if content.startswith("- "):
            break
        key, raw = _split_key_value(content, lineno)
        index += 1
        value, index = _parse_value_or_child(lines, index, indent, raw)
        result[key] = value
    return result, index


def _parse_value_or_child(
    lines: list[tuple[int, str, int]], index: int, indent: int, raw: str
) -> tuple[Any, int]:
    raw = raw.strip()
    if raw in {">", "|"}:
        return _parse_block_scalar(lines, index, indent, literal=(raw == "|"))
    if raw:
        return _parse_scalar(raw), index
    return _parse_child(lines, index, indent)


def _parse_child(lines: list[tuple[int, str, int]], index: int, indent: int) -> tuple[Any, int]:
    if index < len(lines) and lines[index][0] > indent:
        return _parse_block(lines, index, lines[index][0])
    if index < len(lines) and lines[index][0] == indent and lines[index][1].startswith("- "):
        return _parse_block(lines, index, indent)
    return None, index


def _parse_block_scalar(
    lines: list[tuple[int, str, int]], index: int, parent_indent: int, *, literal: bool
) -> tuple[str, int]:
    parts: list[str] = []
    while index < len(lines) and lines[index][0] > parent_indent:
        parts.append(lines[index][1].strip())
        index += 1
    if literal:
        return "\n".join(parts) + ("\n" if parts else ""), index
    return " ".join(p for p in parts if p != ""), index


def _looks_like_key_value(s: str) -> bool:
    if ":" not in s:
        return False
    key = s.split(":", 1)[0].strip()
    return bool(key) and not key.startswith(("'", '"')) and re.match(r"^[A-Za-z0-9_.-]+$", key) is not None


def _split_key_value(s: str, lineno: int) -> tuple[str, str]:
    if ":" not in s:
        raise YAMLError(f"expected key: value at line {lineno}")
    key, value = s.split(":", 1)
    key = key.strip().strip("'\"")
    if not key:
        raise YAMLError(f"empty key at line {lineno}")
    return key, value


def _parse_scalar(raw: str) -> Any:
    raw = raw.strip()
    if raw == "":
        return ""
    if raw in {"null", "Null", "NULL", "~"}:
        return None
    if raw in {"true", "True", "TRUE"}:
        return True
    if raw in {"false", "False", "FALSE"}:
        return False
    if raw.startswith("[") and raw.endswith("]"):
        return [_parse_scalar(part) for part in _split_inline(raw[1:-1]) if part.strip()]
    if raw.startswith("{") and raw.endswith("}"):
        out: dict[str, Any] = {}
        for part in _split_inline(raw[1:-1]):
            if not part.strip():
                continue
            key, value = part.split(":", 1)
            out[key.strip().strip("'\"")] = _parse_scalar(value)
        return out
    if raw.startswith('"') and raw.endswith('"'):
        try:
            return json.loads(raw)
        except json.JSONDecodeError:
            return raw[1:-1]
    if raw.startswith("'"):
        if raw.endswith("'") and len(raw) > 1:
            return raw[1:-1].replace("''", "'")
        return raw.strip("'").replace("''", "'")
    if re.match(r"^-?[0-9]+$", raw):
        try:
            return int(raw)
        except ValueError:
            pass
    if re.match(r"^-?([0-9]+\.[0-9]*|\.[0-9]+)$", raw):
        try:
            return float(raw)
        except ValueError:
            pass
    return raw


def _split_inline(s: str) -> list[str]:
    parts: list[str] = []
    buf: list[str] = []
    quote = ""
    depth = 0
    for ch in s:
        if quote:
            buf.append(ch)
            if ch == quote:
                quote = ""
            continue
        if ch in {"'", '"'}:
            quote = ch
            buf.append(ch)
        elif ch in "[{":
            depth += 1
            buf.append(ch)
        elif ch in "]}":
            depth -= 1
            buf.append(ch)
        elif ch == "," and depth == 0:
            parts.append("".join(buf).strip())
            buf = []
        else:
            buf.append(ch)
    parts.append("".join(buf).strip())
    return parts


def _dump_value(value: Any, indent: int, sort_keys: bool) -> str:
    sp = " " * indent
    if isinstance(value, dict):
        keys = sorted(value) if sort_keys else value.keys()
        lines: list[str] = []
        for key in keys:
            v = value[key]
            if isinstance(v, (dict, list)):
                lines.append(f"{sp}{key}:")
                lines.append(_dump_value(v, indent + 2, sort_keys).rstrip("\n"))
            else:
                lines.append(f"{sp}{key}: {_format_scalar(v, indent + 2)}")
        return "\n".join(lines) + "\n"
    if isinstance(value, list):
        lines = []
        for item in value:
            if isinstance(item, dict):
                keys = sorted(item) if sort_keys else item.keys()
                keys = list(keys)
                if not keys:
                    lines.append(f"{sp}- {{}}")
                    continue
                first = keys[0]
                first_value = item[first]
                if isinstance(first_value, (dict, list)):
                    lines.append(f"{sp}- {first}:")
                    lines.append(_dump_value(first_value, indent + 4, sort_keys).rstrip("\n"))
                else:
                    lines.append(f"{sp}- {first}: {_format_scalar(first_value, indent + 4)}")
                for key in keys[1:]:
                    v = item[key]
                    if isinstance(v, (dict, list)):
                        lines.append(f"{sp}  {key}:")
                        lines.append(_dump_value(v, indent + 4, sort_keys).rstrip("\n"))
                    else:
                        lines.append(f"{sp}  {key}: {_format_scalar(v, indent + 4)}")
            elif isinstance(item, list):
                lines.append(f"{sp}-")
                lines.append(_dump_value(item, indent + 2, sort_keys).rstrip("\n"))
            else:
                lines.append(f"{sp}- {_format_scalar(item, indent + 2)}")
        return "\n".join(lines) + "\n"
    return sp + _format_scalar(value, indent) + "\n"


def _format_scalar(value: Any, indent: int) -> str:
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, (int, float)):
        return str(value)
    s = str(value)
    if "\n" in s:
        pad = " " * indent
        return "|\n" + "\n".join(pad + line for line in s.splitlines())
    if s and re.match(r"^[A-Za-z0-9_./@:+-][A-Za-z0-9_./@:+ -]*$", s) and not s.startswith(("-", "?", ":")) and " #" not in s:
        return s
    return json.dumps(s, ensure_ascii=False)
