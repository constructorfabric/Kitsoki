---
id: 2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace
title: "MCP-driven live sessions (session_new) leave no discoverable trace under ~/.kitsoki/sessions/<app>/ — cost/token evidence is unrecoverable"
target: kitsoki
filed_at: 2026-06-24T09:00:00Z
status: open
severity: P1
component: observability
kitsoki_rev: 501cbd28
trace_ref: ""
external: {}
assignee: ""
url: "issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md"
---

## Body

Live `bugfix` sessions started through the **studio MCP** `session_new` tool
(harness `live`, profile `claude-native`, run locally during the 2026-06
bug-smashing marathon) leave **no discoverable session trace** under
`~/.kitsoki/sessions/bugfix/`. By contrast, every trace that *is* present in
that directory comes from the **web** transport (`<sha8>-web-<uuid>.jsonl`).

The MCP `session_new` handler defaults its trace path to a random file in the
OS temp dir (`$TMPDIR/kitsoki-studio-*.jsonl`) instead of
`~/.kitsoki/sessions/<app>/<id>.jsonl`. `kitsoki trace` only walks
`store.SessionsDir()` (`~/.kitsoki/sessions`), so MCP-driven traces are never
discoverable by app/id — they survive only as anonymous temp files that
`$TMPDIR` reaping eventually deletes.

**Impact:** blocks cost/token mining (`tools/session-mining/cost_extract.py`)
of *any* MCP-driven dogfood session, and undermines reproducibility/auditing of
live drives. The cost/token evidence for the entire 2026-06 marathon is
unrecoverable. This is why the bake-off must **re-run** the kitsoki arm rather
than reuse marathon traces.

### Trace evidence

Local `~/.kitsoki/sessions/bugfix/` on kitsoki_rev `501cbd28` (2026-06-24):

- **Every** `.jsonl` is a web-transport session — zero MCP/session-driven
  traces:

  ```
  $ ls ~/.kitsoki/sessions/bugfix/ | sed 's/-.*//' | sort | uniq -c
  # 180+ distinct sha8 prefixes, EACH followed by "-web-<uuid>"; no other infix
  $ ls -t ~/.kitsoki/sessions/bugfix/*.jsonl | head
  7c98905b-web-b6b71709-….jsonl
  a439428d-web-a6ff91c2-….jsonl
  1df47490-web-3d2ecb0c-….jsonl
  …                            # all *-web-* 
  ```

- The marathon's final drives ran ~2026-06-24 ~05:00, but the **newest** trace
  in the dir is Jun 23 ~19:35 — there are **no Jun 24 traces at all**:

  ```
  $ ls -lt ~/.kitsoki/sessions/bugfix/ | head
  -rw-r--r--  …  Jun 23 19:35  7c98905b-web-….jsonl   # newest
  -rw-r--r--  …  Jun 23 19:28  a439428d-web-….jsonl
  …
  $ ls -lt ~/.kitsoki/sessions/bugfix/ | grep "Jun 24"
  # (no output)
  ```

- The `trace_ref`s recorded by marathon-era bug tickets are all
  non-resolvable:
  - **#1 / #2 / #8** — empty string (`trace_ref: ""`).
  - **#12** (`2026-06-10T141756Z-decide-postcmd-captured-submit-reported-abandoned.md`)
    — points to a **remote** path
    `/home/cloud-user/.kitsoki/sessions/bugfix/d2e21c10-jira-PLTFRM-90872.jsonl`
    (not present locally).
  - **#14** (`2026-06-12T022703Z-web-transport-hides-background-completion-failure.md`)
    — points to local filename
    `94c6daa4-web-0391e58b-f236-4261-a74b-ca107674f5aa.jsonl`, which is
    **absent**:

    ```
    $ ls ~/.kitsoki/sessions/bugfix/94c6daa4*
    no matches found
    ```

### Expected vs actual

- **Expected:** a live session started via MCP `session_new` (harness `live`,
  profile `claude-native`) writes its JSONL event trace to
  `~/.kitsoki/sessions/<app>/<id>.jsonl` — the same discoverable location the
  `web` transport uses — so `kitsoki trace --app bugfix --latest` and
  `tools/session-mining/cost_extract.py` can find it by app/id after the fact.
- **Actual:** the MCP session writes a real JSONL trace to
  `$TMPDIR/kitsoki-studio-<rand>.jsonl` (no app subdir, no transport infix, the
  session id never appears in the filename). It is invisible to
  `kitsoki trace` (which only walks `store.SessionsDir()`) and is eventually
  reaped by the OS, so cost/token data is lost.

### Investigation hints

The single divergence point is the MCP handler's default trace path; the web
path is fine. Cited against kitsoki_rev `501cbd28`:

- **Discoverable path derivation (the contract):**
  `internal/store/trace_path.go`
  - `DefaultTracePath(app, transport, thread)` (`trace_path.go:48-63`) builds
    `~/.kitsoki/sessions/<appSlug>/<sha8>-<slug>.jsonl`, where
    `slug = transport+":"+thread` slugified and `sha8 = sha256(transport+":"+thread)[:8]`.
    The `-web-` infix is the literal **transport label**, not part of the
    session id.
  - `SessionsDir()` (`trace_path.go:71-77`) = `~/.kitsoki/sessions` — the root
    `kitsoki trace` searches. (Note: `KITSOKI_APP_DIR` is **not** involved in
    trace-path resolution; it is published by `cmd/kitsoki/render.go:40-41`
    only for story `cwd:` interpolation — a red herring for this bug.)

- **Web path (correct, discoverable):**
  `cmd/kitsoki/registry.go:249` calls
  `store.DefaultTracePath(def.App.ID, "web", string(sid))`, then `os.MkdirAll`s
  the dir and opens an on-disk JSONL sink (`registry.go:249-256`). The bare
  UUID session id comes from `orch.NewSession` →
  `internal/orchestrator/orchestrator.go:662-663`.

- **MCP path (the defect):**
  `internal/mcp/studio/session_tools.go`
  - `handleSessionNew` (`session_tools.go:307`) resolves the trace via
    `resolveTracePath(args.Trace)` (`:315`) and passes it as
    `OpenDrivingSessionParams.TracePath` (`:324`).
  - `resolveTracePath(override)` (`session_tools.go:1171-1182`): when the
    caller passes no explicit `trace` arg, it returns
    `os.CreateTemp("", "kitsoki-studio-*.jsonl")` (`:1175`) — `$TMPDIR`, random
    name. It does **not** call `DefaultTracePath`, does **not** use
    `SessionsDir()`, and applies **no** transport infix. This is the root
    cause.
  - The temp path flows into `newSessionRuntime`
    (`internal/mcp/studio/session_runtime.go:164`), which opens the JSONL sink
    there (`session_runtime.go:200`, `store.OpenJSONL`) plus an in-memory
    metadata store (`session_runtime.go:224`, `store.OpenMemory()`). Events
    **are** durably written — just to the temp file, not under
    `~/.kitsoki/sessions`.
  - `attach`/`continue` (`session_tools.go:359`) and the no-LLM helper
    (`:1035`) call the same `resolveTracePath`, so they share the temp-dir
    behavior.
  - `SessionNewArgs.Trace` exists, so a caller *can* opt into a discoverable
    path by passing `trace` explicitly — but the default is temp, and the
    marathon drives did not pass it.

- **Why `kitsoki trace` can't find it:**
  `cmd/kitsoki/trace.go` `resolveTraceArg` (`trace.go:44-84`, called at
  `trace.go:513` as `resolveTraceArg(store.SessionsDir(), arg, appFilter)`)
  only `WalkDir`s `root[/appFilter]` = `~/.kitsoki/sessions[/<app>]` for
  `*.jsonl` (`:54-74`). An explicit existing full path wins (`:48-51`), so an
  MCP trace is findable *only if you already know* its `$TMPDIR` path;
  otherwise the walk never sees it → `"no session trace found under …"`
  (`:75-80`).

Candidate fix surface (observation, not a directive): point the default branch
of `resolveTracePath` (`session_tools.go:1171`) at
`store.DefaultTracePath(def.App.ID, "<transport>", sid)` so MCP sessions land
in the same discoverable per-app dir as web. A `mcp`/`studio` transport infix
would also let cost-mining tools distinguish them from web sessions.
