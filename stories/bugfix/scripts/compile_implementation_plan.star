def _require_string(value, label):
    if type(value) != "string" or value.strip() == "":
        fail(label + " must be a non-empty string")
    return value.strip()


def _require_list(value, label, minimum):
    if type(value) != "list" or len(value) < minimum:
        fail(label + " must contain at least " + str(minimum) + " item(s)")
    return value


def _safe_id(value):
    if len(value) > 32:
        return False
    first = True
    for ch in value.elems():
        allowed = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
        if ch not in allowed:
            return False
        if first and ch in "_-":
            return False
        first = False
    return value != ""


def _safe_path(value, label):
    path = _require_string(value, label)
    if path.startswith("/") or path == "*" or "**" in path:
        fail(label + " must be a narrow repository-relative path or glob")
    for segment in path.split("/"):
        if segment == "" or segment == "." or segment == "..":
            fail(label + " must not traverse outside its repository path")
    return path


def _string_list(value, label, path_list):
    items = _require_list(value, label, 1)
    out = []
    for index, item in enumerate(items):
        if path_list:
            out.append(_safe_path(item, label + "[" + str(index) + "]"))
        else:
            out.append(_require_string(item, label + "[" + str(index) + "]"))
    return out


def _fallback_reasons(value, label):
    allowed = ["capability_missing", "task_not_decomposable"]
    if type(value) != "list":
        fail(label + " must be a list")
    out = []
    for reason in value:
        if reason not in allowed:
            fail(label + " contains unsupported reason " + str(reason))
        if reason in out:
            fail(label + " contains duplicate reason " + reason)
        out.append(reason)
    return out


def _max_bytes(write_paths, expected_artifacts):
    # A conservative bounded grant. The later runtime may apply a stricter
    # policy, but the compiler never emits an unbounded write allowance.
    estimate = (len(write_paths) + len(expected_artifacts)) * 32768
    if estimate < 65536:
        return 65536
    if estimate > 262144:
        return 262144
    return estimate


def _acceptance_command(commands):
    # The compiler keeps the individual commands for auditability and derives
    # one explicit, fail-fast gate command.  host.run owns execution; CodeAct
    # never receives a process capability or claims it ran these commands.
    wrapped = []
    for command in commands:
        if "\n" in command or "\r" in command or "\x00" in command:
            fail("acceptance_commands must be single-line deterministic commands")
        wrapped.append("(" + command + ")")
    return " && ".join(wrapped)


def _acceptance_gate_command(commands):
    # Preserve the gate verdict as a JSON envelope even when a command fails:
    # host.run's process exit is therefore infrastructure-only while the story
    # can deterministically route a failed acceptance gate back to this item.
    command = _acceptance_command(commands)
    return "set +e; " + command + "; rc=$?; printf '{\\\"ok\\\":%s,\\\"exit_code\\\":%s}\\n' \"$([ \"$rc\" -eq 0 ] && printf true || printf false)\" \"$rc\"; exit 0"


def main(ctx):
    source = _require_list(ctx.inputs["implementation_plan"], "implementation_plan", 1)
    if len(source) > 4:
        fail("implementation_plan may contain at most 4 work items")

    plan = []
    ids = []
    capability_items = []
    for index, raw in enumerate(source):
        if type(raw) != "dict":
            fail("implementation_plan[" + str(index) + "] must be an object")
        item_id = _require_string(raw.get("id", ""), "implementation_plan[" + str(index) + "].id")
        if not _safe_id(item_id):
            fail("implementation_plan[" + str(index) + "].id must be a stable short identifier")
        if item_id in ids:
            fail("implementation_plan has duplicate id " + item_id)

        dependencies = _require_list(raw.get("depends_on", []), "implementation_plan[" + str(index) + "].depends_on", 0)
        normalized_dependencies = []
        for dependency in dependencies:
            dependency = _require_string(dependency, "implementation_plan[" + str(index) + "].depends_on")
            if dependency not in ids:
                fail("implementation_plan item " + item_id + " depends on unknown or later item " + dependency)
            if dependency in normalized_dependencies:
                fail("implementation_plan item " + item_id + " repeats dependency " + dependency)
            normalized_dependencies.append(dependency)

        objective = _require_string(raw.get("objective", ""), "implementation_plan[" + str(index) + "].objective")
        evidence_refs = _string_list(raw.get("evidence_refs", []), "implementation_plan[" + str(index) + "].evidence_refs", False)
        read_paths = _string_list(raw.get("read_paths", []), "implementation_plan[" + str(index) + "].read_paths", True)
        write_paths = _string_list(raw.get("write_paths", []), "implementation_plan[" + str(index) + "].write_paths", True)
        invariants = _string_list(raw.get("invariants", []), "implementation_plan[" + str(index) + "].invariants", False)
        acceptance_commands = _string_list(raw.get("acceptance_commands", []), "implementation_plan[" + str(index) + "].acceptance_commands", False)
        expected_artifacts = _string_list(raw.get("expected_artifacts", []), "implementation_plan[" + str(index) + "].expected_artifacts", False)
        # Effect interpolation preserves nested plan objects but represents
        # scalar YAML values as strings at this host boundary. Normalize only
        # a canonical decimal string; accepting arbitrary coercions here would
        # weaken the plan's bounded-step contract.
        max_steps = raw.get("max_steps", 0)
        if type(max_steps) == "string":
            parsed_max_steps = int(max_steps)
            if str(parsed_max_steps) != max_steps:
                fail("implementation_plan item " + item_id + " max_steps must be an integer from 1 through 5")
            max_steps = parsed_max_steps
        # YAML decoded through a rendered effect can arrive as a float. Accept
        # only an exactly integral value and immediately canonicalize it; 4.5
        # and other fractional budgets remain invalid rather than truncating.
        if type(max_steps) == "float":
            parsed_max_steps = int(max_steps)
            if max_steps != float(parsed_max_steps):
                fail("implementation_plan item " + item_id + " max_steps must be an integer from 1 through 5")
            max_steps = parsed_max_steps
        if type(max_steps) != "int" or max_steps < 1 or max_steps > 5:
            fail("implementation_plan item " + item_id + " max_steps must be an integer from 1 through 5")
        fallback_reason_allowlist = _fallback_reasons(raw.get("fallback_reason_allowlist", []), "implementation_plan[" + str(index) + "].fallback_reason_allowlist")
        capabilities = {
            "world": "read",
            "fs": {"read": read_paths, "write": write_paths, "max_bytes": _max_bytes(write_paths, expected_artifacts)},
            "vcs": "read",
        }
        normalized = {
            "id": item_id,
            "depends_on": normalized_dependencies,
            "objective": objective,
            "evidence_refs": evidence_refs,
            "read_paths": read_paths,
            "write_paths": write_paths,
            "invariants": invariants,
            "acceptance_commands": acceptance_commands,
            "acceptance_command": _acceptance_command(acceptance_commands),
            "acceptance_gate_command": _acceptance_gate_command(acceptance_commands),
            "expected_artifacts": expected_artifacts,
            "max_steps": max_steps,
            "fallback_reason_allowlist": fallback_reason_allowlist,
            "capabilities": capabilities,
        }
        plan.append(normalized)
        capability_items.append({"id": item_id, "capabilities": capabilities})
        ids.append(item_id)

    return {
        "plan": plan,
        "capability_manifest": {"version": "bugfix-implementation-plan/v1", "items": capability_items},
    }
