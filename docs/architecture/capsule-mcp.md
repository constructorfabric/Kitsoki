# Capsule MCP authority boundary

`kitsoki capsule mcp` is a standalone MCP server for a single project. Its
startup `ScopeGrant` is immutable: a request can narrow authority but cannot add
workspace roots, definitions, executors, effects, branches, remotes, or tools.

The server returns opaque `{id,generation}` handles. Every mutation requires
the current generation; stale handles fail. Filesystem operations accept only
relative paths and resolve symlinks beneath the managed workspace. Declared
commands run argv-only; raw argv requires both definition and grant approval.

The exposed effect families are intentionally distinct:

- FS, declared execution, and local commit affect one managed workspace;
- environment lock persistence requires `env_write`;
- project-scoped disk hygiene planning is read-only, while applying cleanup
  requires `cleanup` and returns only project-relative paths;
- local reconciliation requires `local_reconcile`, may target only a branch
  granted at server startup, and keeps sync conflict work inside server-owned
  plan digests plus project-relative `.capsules/sync/` artifacts; and
- remote publication remains absent unless the server is started with a future
  explicit `remote_publish` grant and provider; remote CI execution is available
  only when the checked-in CI config names a remote executor and the launching
  process supplies its credential environment.

Verifier-only overlays and secret values are not placed in the agent-visible
filesystem or MCP response payloads. Lifecycle operations emit deterministic
Capsule facts for receipt/tracing consumers.

## Launch profiles

`capsule mcp` is the coding-agent server, not Studio MCP. Start it with the
smallest immutable effect set that fits the role. The default is the writer
profile: `workspace_manage,fs_write,exec,vcs_commit,ci_run`. It intentionally
does **not** grant cleanup mutation or branch reconciliation.

| Role | Effects | May do | Does not receive |
|---|---|---|---|
| reviewer | `workspace_manage` | create/reacquire a scoped read-only workspace; inspect files/diff/status; read CI evidence | writes, commands, commits, CI starts, sync, cleanup mutation |
| writer (default) | `workspace_manage,fs_write,exec,vcs_commit,ci_run` | edit, run declared commands, commit locally, run the fixed pipeline | branch sync/promotion, remote publication, cleanup mutation, raw argv |
| integrator | writer effects + `local_reconcile` and explicit `--branch` | create/continue a stale-safe local reconciliation plan | remote publication unless a separate future grant exists |
| hygiene operator | reviewer effects + `cleanup` | apply a reviewed project-scoped cleanup plan | host-global cache deletion, arbitrary paths |
| environment curator | writer effects + `env_write` | persist an environment lock | new executors, credentials, remote publication |

The always-readable catalog is intentionally small: project/definition and
owned-workspace facts, bounded project-relative file reads/search, VCS status/
diff, CI status/summary, cleanup plan, and environment resolve/verify. Passing
any `--effect` replaces the default rather than extending it.

`fs_write` grants both `capsule.fs.write` and `capsule.fs.patch`. Patch is a
guarded full-file replacement: it requires the caller to supply the current
`sha256:<digest>` preimage and rejects a stale read before returning a fresh
workspace generation. It never accepts a host path or a shell patch command.

### Strict client attachment

For Claude Code, keep a dedicated Capsule-only config rather than reusing the
project Studio `.mcp.json`:

```json
{
  "mcpServers": {
    "kitsoki-capsule": {
      "command": "kitsoki",
      "args": [
        "capsule", "mcp", "--project", "/repo", "--pipeline", "change",
        "--owner", "reviewer", "--effect", "workspace_manage"
      ]
    }
  }
}
```

Attach it headlessly with the client’s strict MCP mode and no inherited project
tool configuration:

```sh
claude -p --mcp-config .capsule-reviewer.mcp.json --strict-mcp-config \
  --tools mcp__kitsoki-capsule__* "Review the scoped workspace only."
```

For a writer, replace the final effects with the default writer set explicitly:

```sh
kitsoki capsule mcp --project /repo --pipeline change --owner writer \
  --effect workspace_manage --effect fs_write --effect exec \
  --effect vcs_commit --effect ci_run
```

For Codex, register the same command as a single MCP server and launch the
agent with only that MCP server enabled; do not also grant shell, filesystem,
Git, or a Studio MCP server. The server’s opaque handles and startup grant are
the authority boundary, so client-side tool filtering is defense in depth—not
the enforcement mechanism.

Start reconciliation authority narrowly, for example:

```sh
kitsoki capsule mcp --project /path/to/project --branch staging/local
```

Omitting `--branch` still permits workspace and CI operations but denies all
`capsule.sync.plan` calls. A required promotion gate additionally verifies the
persisted receipt, its run projection, and its exact candidate source digest.
Diverged plans continue through `capsule.sync.conflicts`,
`capsule.sync.integration`, `capsule.sync.continue`, and `capsule.sync.abort`;
these tools never accept host paths. Continuation apply requires resolver
decision, independent lost-work review, and validation receipt inputs before
updating the target ref; abort can preserve a project-relative patch artifact
before removing the managed integration instance.

`capsule.cleanup.plan` is available as a safe read-only operation for ongoing
CI hygiene. `capsule.cleanup.apply` requires the startup `cleanup` effect and
is limited to project Capsule state such as old `.capsules/ci` run sidecars and
explicitly requested `.capsules/cache` entries. It does not clear host-global
build caches; operators use the CLI for that broader maintenance path.
