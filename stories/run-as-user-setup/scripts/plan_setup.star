def _trim(v):
    return str(v or "").strip()


def _shq(v):
    return "'" + str(v).replace("'", "'\"'\"'") + "'"


def _path_join(a, b):
    a = _trim(a).rstrip("/")
    b = _trim(b).lstrip("/")
    if a == "":
        return b
    if b == "":
        return a
    return a + "/" + b


def _join(lines):
    return "\n".join(lines)


def _cli_ref(v, name):
    v = _trim(v)
    if v == "" or v == "auto":
        return "<resolved-%s-from-PATH>" % name
    return v


def main(ctx):
    platform = _trim(ctx.inputs["platform"]) or "darwin"
    operator_user = _trim(ctx.inputs["operator_user"])
    agent_user = _trim(ctx.inputs["agent_user"]) or "kitsoki-agent"
    group_name = _trim(ctx.inputs["group_name"]) or "kitsoki_agents"
    capsule_root = _trim(ctx.inputs["capsule_root"]) or "/Users/Shared/kitsoki/capsules"
    wrapper_bin = _trim(ctx.inputs["wrapper_bin"]) or "/Users/Shared/kitsoki/agent-bin"
    protected_root = _trim(ctx.inputs["protected_root"]) or "."
    codex_bin = _trim(ctx.inputs["codex_bin"]) or "auto"
    claude_bin = _trim(ctx.inputs["claude_bin"]) or "auto"
    codex_ref = _cli_ref(codex_bin, "codex")
    claude_ref = _cli_ref(claude_bin, "claude")
    sample_capsule = _trim(ctx.inputs["sample_capsule"]) or "room-123-dev"

    if platform != "darwin":
        fail("run-as-user setup currently supports the macOS local-user path; got platform %q" % platform)

    operator_ref = operator_user
    if operator_ref == "":
        operator_ref = "<operator-user>"

    sample_path = _path_join(capsule_root, sample_capsule)

    setup_summary = _join([
        "This setup creates or reuses a Standard macOS account named %s as the delegated coding-agent user." % agent_user,
        "Kitsoki still owns the deterministic harness and merge/integration operations.",
        "The backend CLIs run through root-owned wrappers as %s, so OS permissions can deny writes to %s." % (agent_user, protected_root),
    ])

    config_snippet = _join([
        "agent_user_delegation:",
        "  enabled: true",
        "  run_as_user: %s" % agent_user,
        "  wrapper_bin: %s" % wrapper_bin,
        "  capsule_root: %s" % capsule_root,
        "",
        "agent_launch_policy:",
        "  enabled: true",
        "  require_capsule: true",
        "  protected_roots:",
        "    - %s" % protected_root,
        "  allowed_roots:",
        "    - %s" % capsule_root,
        "  protected_branches: [main, master, trunk, \"integration/*\", \"staging/*\"]",
    ])

    account_steps = _join([
        "# 1. Create a Standard, non-admin macOS account named %s." % agent_user,
        "#    Use System Settings > Users & Groups, or your normal fleet-management path.",
        "# 2. Log into that account once.",
        "# 3. Authenticate subscription-backed CLIs in that account if needed:",
        "sudo -H -u %s %s --version" % (_shq(agent_user), _shq(codex_ref)),
        "sudo -H -u %s %s --version" % (_shq(agent_user), _shq(claude_ref)),
        "# If auth/status fails because Keychain is unavailable under sudo, use a launchd-backed user agent next.",
    ])

    group_commands = _join([
        "sudo dseditgroup -o create %s || true" % _shq(group_name),
        "sudo dseditgroup -o edit -a %s -t user %s" % (_shq(agent_user), _shq(group_name)),
        "sudo mkdir -p %s" % _shq(capsule_root),
        "sudo chown root:%s %s" % (_shq(group_name), _shq(capsule_root)),
        "sudo chmod 0710 %s" % _shq(capsule_root),
    ])

    wrapper_commands = _join([
        "sudo mkdir -p %s" % _shq(wrapper_bin),
        "sudo tee %s >/dev/null <<'SH'" % _shq(_path_join(wrapper_bin, "codex")),
        "#!/bin/sh",
        "umask 0002",
        "exec /usr/bin/sudo -n -H -u %s %s \"$@\"" % (_shq(agent_user), _shq(codex_ref)),
        "SH",
        "sudo tee %s >/dev/null <<'SH'" % _shq(_path_join(wrapper_bin, "claude")),
        "#!/bin/sh",
        "umask 0002",
        "exec /usr/bin/sudo -n -H -u %s %s \"$@\"" % (_shq(agent_user), _shq(claude_ref)),
        "SH",
        "sudo chmod 0755 %s %s" % (_shq(_path_join(wrapper_bin, "codex")), _shq(_path_join(wrapper_bin, "claude"))),
    ])

    sudoers_snippet = _join([
        "# Edit with: sudo visudo -f /private/etc/sudoers.d/kitsoki-agent",
        "# The apply path resolves auto before writing this file.",
        "Cmnd_Alias KITSOKI_AGENT_CLIS = %s, %s" % (codex_ref, claude_ref),
        "%s ALL=(%s) NOPASSWD: KITSOKI_AGENT_CLIS" % (operator_ref, agent_user),
        "",
        "# Do not grant broad passwordless access to /bin/sh.",
    ])

    assignment_commands = _join([
        "go run ./cmd/kitsoki capsule open clean-repo --dest %s" % _shq(sample_path),
        "sudo chgrp -R %s %s" % (_shq(group_name), _shq(sample_path)),
        "sudo chmod -R ug+rwX,o-rwx %s" % _shq(sample_path),
        "sudo find %s -type d -exec chmod g+s {} +" % _shq(sample_path),
    ])

    validation_commands = _join([
        "sudo -n -H -u %s touch %s" % (_shq(agent_user), _shq(_path_join(sample_path, ".agent-write-ok"))),
        "sudo -n -H -u %s touch %s && echo FAIL || echo 'ok: protected checkout denied'" % (_shq(agent_user), _shq(_path_join(protected_root, ".agent-should-fail"))),
        "go run ./cmd/kitsoki agent launch --raw --interactive --backend codex --working-dir %s" % _shq(protected_root),
        "# Expected: Kitsoki rejects the protected root before codex starts.",
        "%s --version" % _shq(_path_join(wrapper_bin, "codex")),
        "sudo -n -H -u %s %s --version" % (_shq(agent_user), _shq(codex_ref)),
    ])

    security_notes = _join([
        "The apply path resolves codex_bin/claude_bin=auto before installing sudoers or wrappers; missing CLIs stop in an edit-and-retry state.",
        "agent_launch_policy proves launch placement only; it does not confine a started process.",
        "run_as_user delegation is critical on macOS because the delegated account should lack write permission to the protected checkout.",
        "The delegated account may still write its own HOME, /tmp, and any group-writable path. Full capsule-only confinement requires the future runtime sandbox slice.",
        "Keep merge-to-main and protected integration operations in the operator-owned deterministic harness account.",
    ])

    receipt_markdown = _join([
        "run_as_user setup receipt",
        "",
        "- platform: %s" % platform,
        "- operator_user: %s" % operator_ref,
        "- run_as_user: %s" % agent_user,
        "- group: %s" % group_name,
        "- capsule_root: %s" % capsule_root,
        "- wrapper_bin: %s" % wrapper_bin,
        "- protected_root: %s" % protected_root,
        "- validated probes: delegated capsule write, protected checkout write denied, launch policy denial",
    ])

    return {
        "setup_summary": setup_summary,
        "config_snippet": config_snippet,
        "account_steps": account_steps,
        "group_commands": group_commands,
        "wrapper_commands": wrapper_commands,
        "sudoers_snippet": sudoers_snippet,
        "assignment_commands": assignment_commands,
        "validation_commands": validation_commands,
        "receipt_markdown": receipt_markdown,
        "security_notes": security_notes,
    }
