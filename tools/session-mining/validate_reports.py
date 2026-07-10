#!/usr/bin/env python3
"""validate_reports.py — validate emitted intent-mining reports against their JSON
Schemas.

    python3 validate_reports.py <job-dir>            # validates intents.json + analysis.json
    python3 validate_reports.py --report intents.json --schema schema/intents.schema.json

The deterministic spine (ground.py / tag_score.py / emit.py) is stdlib-only so it
runs anywhere. THIS helper is the optional strict gate: it needs `jsonschema`
(`pip3 install --user jsonschema`). Tests call it after emit.py so the two reports
are checked against schema/{intents,analysis}.schema.json on every run, and it
re-checks the cross-link contract (every intents[].analysis_ref resolves to an
analysis instance, and vice-versa) which JSON Schema alone cannot express.

Exit 0 if all valid, non-zero with a diagnostic otherwise.
"""
import argparse
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
SCHEMA_DIR = os.path.join(HERE, "schema")


def _load(path):
    with open(path) as fh:
        return json.load(fh)


def _require_jsonschema():
    try:
        import jsonschema  # noqa: F401
        from jsonschema import Draft202012Validator
        return Draft202012Validator
    except ImportError:
        sys.stderr.write(
            "note: jsonschema not installed — using stdlib schema subset validator\n"
            "       (install jsonschema for full Draft 2020-12 diagnostics)\n")
        return None


def validate_report(report_path, schema_path):
    """Validate one report file against one schema. Returns a list of error strings."""
    Validator = _require_jsonschema()
    schema = _load(schema_path)
    doc = _load(report_path)
    if Validator is None:
        return _validate_schema_subset(doc, schema, os.path.basename(report_path))
    v = Validator(schema)
    errs = []
    for e in sorted(v.iter_errors(doc), key=lambda e: list(e.path)):
        loc = "/".join(str(p) for p in e.path) or "(root)"
        errs.append("%s: %s @ %s" % (os.path.basename(report_path), e.message, loc))
    return errs


def _validate_schema_subset(doc, schema, report_name):
    """Validate against the JSON Schema subset used by this tool's report schemas.

    This is intentionally small, but it is still a gate: missing required fields,
    wrong primitive types, const/enum/pattern violations, array bounds, numeric
    minimums, additionalProperties=false, additionalProperties schemas, and
    local #/$defs references all fail without the optional jsonschema package.
    """
    defs = schema.get("$defs", {})
    errs = []

    def add(path, msg):
        loc = "/".join(str(p) for p in path) or "(root)"
        errs.append("%s: %s @ %s" % (report_name, msg, loc))

    def resolve(s, path):
        seen = set()
        while isinstance(s, dict) and "$ref" in s:
            ref = s["$ref"]
            if ref in seen:
                add(path, "cyclic schema reference %r" % ref)
                return {}
            seen.add(ref)
            prefix = "#/$defs/"
            if not ref.startswith(prefix):
                add(path, "unsupported schema reference %r" % ref)
                return {}
            name = ref[len(prefix):]
            if name not in defs:
                add(path, "unresolved schema reference %r" % ref)
                return {}
            s = defs[name]
        return s

    def type_ok(value, typ):
        if typ == "object":
            return isinstance(value, dict)
        if typ == "array":
            return isinstance(value, list)
        if typ == "string":
            return isinstance(value, str)
        if typ == "integer":
            return isinstance(value, int) and not isinstance(value, bool)
        if typ == "number":
            return isinstance(value, (int, float)) and not isinstance(value, bool)
        if typ == "boolean":
            return isinstance(value, bool)
        if typ == "null":
            return value is None
        return True

    def check(value, s, path):
        s = resolve(s, path)
        if not isinstance(s, dict):
            return

        if "const" in s and value != s["const"]:
            add(path, "%r was expected" % s["const"])
            return
        if "enum" in s and value not in s["enum"]:
            add(path, "%r is not one of %r" % (value, s["enum"]))

        declared_type = s.get("type")
        if declared_type is not None:
            types = declared_type if isinstance(declared_type, list) else [declared_type]
            if not any(type_ok(value, typ) for typ in types):
                add(path, "%r is not of type %s" % (value, "|".join(types)))
                return

        if isinstance(value, dict):
            properties = s.get("properties", {})
            for key in s.get("required", []):
                if key not in value:
                    add(path, "%r is a required property" % key)

            additional = s.get("additionalProperties", True)
            if additional is False:
                allowed = set(properties.keys())
                for key in sorted(value.keys()):
                    if key not in allowed:
                        add(path + [key], "additional properties are not allowed (%r was unexpected)" % key)
            elif isinstance(additional, dict):
                for key in sorted(value.keys()):
                    if key not in properties:
                        check(value[key], additional, path + [key])

            for key, subschema in properties.items():
                if key in value:
                    check(value[key], subschema, path + [key])

        if isinstance(value, list):
            if "minItems" in s and len(value) < s["minItems"]:
                add(path, "has too few items")
            if "maxItems" in s and len(value) > s["maxItems"]:
                add(path, "has too many items")
            if "items" in s:
                for i, item in enumerate(value):
                    check(item, s["items"], path + [i])

        if isinstance(value, str):
            if "minLength" in s and len(value) < s["minLength"]:
                add(path, "is too short")
            pattern = s.get("pattern")
            if pattern and re.search(pattern, value) is None:
                add(path, "%r does not match %r" % (value, pattern))

        if isinstance(value, (int, float)) and not isinstance(value, bool):
            if "minimum" in s and value < s["minimum"]:
                add(path, "%r is less than the minimum of %r" % (value, s["minimum"]))
            if "maximum" in s and value > s["maximum"]:
                add(path, "%r is greater than the maximum of %r" % (value, s["maximum"]))

    check(doc, schema, [])
    return errs


def check_crosslinks(intents, analysis):
    """The cross-link contract JSON Schema can't express: every intents[].analysis_ref
    must resolve to an analysis instance, and every analysis instance must be cited by
    some intent. Returns a list of error strings."""
    errs = []
    inst_ids = {i["instance_id"] for i in analysis.get("instances", [])}
    intent_ids = set()
    for it in intents.get("intents", []):
        iid = it["instance_id"]
        intent_ids.add(iid)
        ref = it.get("analysis_ref", "")
        want = "analysis.json#" + iid
        if ref != want:
            errs.append("intents[%s]: analysis_ref %r != %r" % (iid, ref, want))
        if iid not in inst_ids:
            errs.append("intents[%s]: no matching analysis instance" % iid)
    for iid in inst_ids:
        if iid not in intent_ids:
            errs.append("analysis[%s]: instance not catalogued in intents.json" % iid)
    return errs


def validate_job(job_dir):
    """Validate intents.json + analysis.json in a job dir + the cross-link. Returns errors."""
    intents_p = os.path.join(job_dir, "intents.json")
    analysis_p = os.path.join(job_dir, "analysis.json")
    errs = []
    errs += validate_report(intents_p, os.path.join(SCHEMA_DIR, "intents.schema.json"))
    errs += validate_report(analysis_p, os.path.join(SCHEMA_DIR, "analysis.schema.json"))
    errs += check_crosslinks(_load(intents_p), _load(analysis_p))
    return errs


def main(argv=None):
    ap = argparse.ArgumentParser(description="Validate intent-mining reports against their schemas.")
    ap.add_argument("job_dir", nargs="?", help="job dir holding intents.json + analysis.json")
    ap.add_argument("--report", help="validate a single report file")
    ap.add_argument("--schema", help="schema to validate --report against")
    args = ap.parse_args(argv)

    if args.report:
        if not args.schema:
            ap.error("--report requires --schema")
        errs = validate_report(args.report, args.schema)
    elif args.job_dir:
        errs = validate_job(args.job_dir)
    else:
        ap.error("pass a job dir, or --report with --schema")

    if errs:
        sys.stderr.write("INVALID (%d):\n" % len(errs))
        for e in errs:
            sys.stderr.write("  - %s\n" % e)
        return 1
    sys.stderr.write("valid: reports conform to schema + cross-link contract\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
