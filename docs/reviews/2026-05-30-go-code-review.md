# Kitsoki Code Review — Synthesis Report

> Generated 2026-05-30 by a fan-out review workflow: 16 finder agents (13 package
> clusters + whole-tree duplication, dead-code, and architecture passes), each finding
> adversarially verified against the real code before inclusion. 69 findings survived
> verification out of a larger raw set. Static baseline: `go vet` clean; staticcheck +
> `deadcode` seeded the dead-code pass.

## 1. Executive Summary

The codebase is structurally sound and largely honors its core thesis (deterministic execution separated from confined LLM decisions), but the review surfaced a small cluster of genuine correctness defects that undermine that thesis at the margins — most notably untraced world mutations that break replay determinism, and a regression in source-color wrapping that will corrupt enum guards and external audit trails. The dominant theme is *silent error swallowing*: discarded `json.Unmarshal`/`scanner.Scan()`/write errors and error-blind iterators recur across journal, jobs, store, and CLI layers. Beyond correctness, the codebase carries a long tail of dead code (the bulk of findings) and moderate duplication concentrated in the oracle host-call handlers, both of which are low-risk cleanup. There are no security findings and no architectural rot beyond an oversized orchestrator.

---

## 2. Findings by Category

### Correctness

**[HIGH] Untraced `last_error` world mutations violate deterministic replay** — `internal/orchestrator/orchestrator.go:1391` (also 1433, 1458, 2501, 2521)
`w.Vars["last_error"] = bgErr.Error()` mutates world state without emitting an `EffectApplied` event. `last_error` is an author-facing variable usable in templates and guards, so on replay it will be unpopulated — operators lose error context, and the event log is no longer a faithful reconstruction of world state. This is the most direct violation of the project's central determinism contract.
*Fix:* Emit an `EffectApplied` event immediately after each mutation, mirroring the bind operations at lines 1505-1508 / 2537-2540.

**[HIGH] Inconsistent source-color wrapping of validated payloads** — `internal/host/oracle_ask_with_mcp.go:1067` (and check 1057)
The non-validator path (line 867) deliberately does *not* wrap `submitted` data, because wrapping injects zero-width markers that break enum-equality guards (`action == 'edit'`) and leak into Jira/Bitbucket audit trails. The validator-retry-loop path at line 1067 still calls `sourcecolor.WrapTree(...)`, re-introducing exactly the bug the non-validator path was fixed for (commit 1f84882). The test suite masks it by calling `sourcecolor.Strip()` on assertions, so it will only surface in production.
*Fix:* Remove the `WrapTree()` wrapper at line 1067 to match the non-validator path. Verify line 1057 (`stdout_json`) against its referencing comment.

**[HIGH] Race condition in `AnswerClarification` without transaction isolation** — `internal/jobs/clarification.go:72-94`
Non-transactional SELECT (status check) followed by a separate UPDATE. A concurrent transition (cancel/complete) between the two can violate the "only `awaiting_input` may answer" invariant. The sibling `RequestClarification` already uses `SerializableLevel`; this method's asymmetry is a clear smell.
*Fix:* Wrap the check + UPDATE in a `sql.LevelSerializable` transaction, matching `RequestClarification`.

**[HIGH] Goroutine leak in `WaitSessionDrained` on context cancellation** — `internal/jobs/jobs.go:789-818`
A deterministic leak: when the context is cancelled while a subscriber has `pending > 0`, the waiting goroutine may never wake (broadcast races the return, no timeout), leaking for the program lifetime if `pending` never reaches 0. `WaitIdle` demonstrates the correct pattern.
*Fix:* Use a context-aware wait (Cond + explicit flag checked in the loop, or a timeout on `done`), following `WaitIdle`.

**[HIGH] Unchecked map lookup creates false stub edges** — `internal/viz/flowchart.go:605`
`toRoom := rooms.RoomOf[target]` without the `ok` check; an unknown `target` yields an empty string used to emit orphaned stub edges in the Mermaid output. Lines 139, 227, 399 and `mermaid_rooms.go:292` all correctly use the comma-ok idiom.
*Fix:* `toRoom, ok := rooms.RoomOf[target]; if !ok { continue }`.

**[HIGH] Race condition in chatattach heartbeat loop context** — `internal/chatattach/attach.go:189`
`heartbeatLoop` runs on `lockedCtx`, which shares the tmux session's cancellation. If the CLI attach is interrupted while tmux still runs, the heartbeat stops and the lock goes stale — other processes can see it as abandoned and corrupt the attach state. (Affects the CLI caller only; the TUI `/attach` already uses `context.Background()`.)
*Fix:* Run `heartbeatLoop` on `context.Background()` or a separately-derived context with its own lifetime.

**[HIGH] Journal iterators and `LatestCheckpoint` silently swallow scan/query errors** — `internal/journal/sqlite.go:295-343, 406-440`
`ReplayFrom`/`ReplayTyped` (`iter.Seq[Entry]`) return silently on scan or query failures (lines 306, 321, 380) — callers cannot tell a clean end from a corrupt/incomplete one. `LatestCheckpoint` (line 427) collapses errors into `(Entry{}, false)`, making "error" indistinguishable from "not found." This hides DB corruption from the very replay path the architecture depends on.
*Fix:* For iterators, change the signature to `(iter.Seq[Entry], error)` or cache the error and expose `Error()`. For `LatestCheckpoint`, return `(Entry, bool, error)`. (Breaking change to the `Reader` interface.)

**[MEDIUM] Empty-string slot treated as missing for required fields** — `internal/machine/machine.go:627`
`if slotDef.Required && (!present || val == nil || val == "")` conflates absence with an empty-but-provided value, rejecting `""` as missing.
*Fix:* Check only `!present` and `val == nil`. If empty-string rejection is wanted, make it an explicit schema validation rule.

**[MEDIUM] `context.Background()` instead of session context in session listener** — `internal/orchestrator/orchestrator.go:517`
`startSessionListener` creates `context.WithCancel(context.Background())`, orphaning the listener from the caller's context hierarchy. On caller timeout/cancel the listener keeps running. Bounded by `stopSessionListener` firing on terminal state, so the orphan only persists if the drain timeout (~10 min) is hit.
*Fix:* Pass a session-scoped (or derived child) context.

**[MEDIUM] Unchecked `scanner.Scan()` in interactive prompts** — `cmd/kitsoki/main.go:432` and `cmd/kitsoki/session.go:447`
Both prompts call `scanner.Text()` without checking `Scan()`. On EOF/I/O error (piped input), `main.go:432` silently falls into the first switch case; `session.go:447` aborts but reports only "Aborted." with no I/O indication. `picker.go:56` shows the correct pattern.
*Fix:* Check `Scan()`; on false, treat as abort/early-return and surface the I/O condition.

**[MEDIUM] Imprecise context-cancellation detection in async turn handler** — `internal/tui/tui.go:1700-1715` (same at `offpath.go:185`)
`ctx.Err() != nil` is used to classify an error as cancellation, but it only means the context was cancelled at some point — a genuine non-cancellation error after async cancel becomes a false positive. Real-world risk low.
*Fix:* Use `errors.Is(err, context.Canceled)`; rely on `ctx.Err()` only when `err == nil`.

**[MEDIUM] `MarkDriveFailed` error discarded** — `internal/host/chat_dispatch.go:155`
`_ = cs.MarkDriveFailed(...)` on an infra error path. If the DB write fails, the drive can stay in `dispatching` indefinitely.
*Fix:* Log at error level with context (or propagate for retry).

**[MEDIUM] Silent state-file write failure in validator** — `internal/mcp/validator.go:447`
`_ = writeOutputAtomically(...)` is intentional (must not block the LLM response), but the state file drives the multi-iteration retry loop, so disk problems degrade validator behavior with zero alerting.
*Fix:* Log the failure to stderr at error level before returning; keep the non-blocking behavior.

**[MEDIUM] Discarded `json.Unmarshal` errors in jobs store** — `internal/jobs/store.go:311` (also 221, 439)
`_ = json.Unmarshal(...)` leaves `Payload` zero on corrupt DB JSON with no signal.
*Fix:* Log a warning on failure; consider returning an error rather than silently defaulting.

**[MEDIUM] Discarded `json.Unmarshal` error in `journalEntriesForEvents`** — `internal/orchestrator/journal_write.go:140`
Can yield journal entries with empty `from`/`to`/`intent`, obscuring transition traces on replay.
*Fix:* Log the error or validate payload format at event-creation time.

**[MEDIUM] TOCTOU in `Subscribe` between status check and channel registration** — `internal/jobs/jobs.go:482-535`
Status read under RLock is released before `rj.subs` registration, leaving a window where a concurrent fanout can send on the channel after it's returned but before `unsub`. Currently only exercised by test code.
*Fix:* Acquire the subscriber lock before releasing `s.mu` (mind lock ordering).

**[LOW] `Workspace.ToMap` discards marshal/unmarshal errors** — `internal/workspace/workspace.go:83`
Harmless today (only safe-to-marshal types) but becomes a silent empty-map trap if extended. `FromMap` already handles errors.
*Fix:* Handle the errors, or add a comment explaining why they're safe to ignore.

---

### Architecture

**[MEDIUM] Tight coupling between orchestrator and journal schema for rehydration** — `internal/orchestrator/attach_session.go:158-206`
`AttachSession` walks the typed-entry stream with direct knowledge of `journal.Entry` and `KindClarifyRequested`/`KindClarifyAnswered` semantics. Schema changes break this path without compile-time warnings.
*Fix:* Introduce a `RehydrationHelper` on `journalReader` that encapsulates clarify/transcript reconstruction.

**[MEDIUM] Orchestrator file is 3178 lines with mixed responsibilities** — `internal/orchestrator/orchestrator.go:1`
Combines session lifecycle, the turn loop, host dispatch + re-render, background-job handling, timeout dispatch, cache sweeping, and journal coordination. The remaining host-dispatch block (~609 lines) is the next cohesive unit. Maintainability, not correctness.
*Fix:* Extract host dispatch (`dispatchHostCalls*`, `enterRedirectState`, `rerenderHostArgs`, `dispatchBackground`) and background-job handling into their own files.

**[MEDIUM] No compile-time interface assertion for `ClarificationRequester`** — `internal/jobs/clarification.go:125-147`
`RequestClarificationAny`/`AnswerClarificationRaw` are meant to satisfy `host.ClarificationRequester`, but nothing forces the compiler to verify it.
*Fix:* Add `var _ host.ClarificationRequester = (*JobStore)(nil)` near the `JobStore` definition.

**[LOW] Unvalidated `PostCmdArg.Key` concatenation into argv** — `internal/mcp/validator.go:496-504`
`"--"+kv.Key` is appended without validation; a Key with spaces could create stray argv slots. Defensive gap, not a security hole (authoring input).
*Fix:* Validate Key against `[a-z0-9-]+` at parse time.

**[LOW] Glamour renderer silently nil on init failure** — `internal/tui/transcript.go:115-140`
Degrades gracefully to undecorated output but with no log.
*Fix:* Log a warning when init fails.

---

### Duplication

**[HIGH] Duplicated MCP-config tempfile creation/marshaling pattern** — `internal/host/oracle_ask.go:276-295`
The marshal → create tempfile → write → defer-cleanup sequence is copy-pasted across ~6 sites (oracle_ask, oracle_decide, oracle_ask_with_mcp, oracle_extract, ask_structured). `oracle_task.go` already proves extraction is viable.
*Fix:* Extract `writeMCPConfigTempfile(mcpServers map[string]any, prefix string) (string, error)` and adopt it everywhere.

**[MEDIUM] Duplicated Bash-tool detection + MCP rewrite** — `internal/host/oracle_ask.go:191-220` (and `oracle_decide.go:148-161`)
Detect-hasBash / validate-BashProfile / rewrite-tools repeats across 2 handlers plus the rewrite helper.
*Fix:* Extract `containsBashTool(tools []string) bool` and `validateBashProfile(agent Agent) string`.

**[MEDIUM] Duplicated prompt resolution + pongo2 render across ask/decide/task** — `internal/host/oracle_ask.go:85-150` (and `oracle_decide.go:390-428`, `oracle_task.go:460-500`)
`resolvePromptPath` + render + sourcecolor-strip + args-fallback repeats in all three.
*Fix:* Extract `resolveAndRenderPrompt(args, fallbackDir) (string, error)`; share at least between decide and task.

**[LOW] Duplicated base CLI-args construction** — `internal/host/oracle_ask.go:223-233` (and `oracle_decide.go:133-142`)
The `-p`/`--permission-mode`/system-prompt/model/tools prefix repeats. (`oracle_converse.go` differs intentionally — exclude.)
*Fix:* Extract `buildBaseCLIArgs(systemPrompt, model, permMode string) []string` for ask/decide.

**[LOW] Duplicate `ghAvailable`/`ghCLIAvailable`** — `internal/host/github.go:78` and `internal/host/git_vcs.go:256`
Identical `gh --version` probe differing only by an irrelevant workdir; the code comment already flags it.
*Fix:* Unify into a single `ghAvailable(ctx context.Context) bool`.

**[LOW] Duplicated state-tree nil-check** — `internal/app/loader.go` (~11 functions)
Real but each site wraps different logic; a callback helper adds overhead for modest gain.
*Fix:* Extract `walkStatesSafely(states, fn)` only if it reads cleanly; otherwise leave as-is.

**[LOW] Duplicated placeholder regex/Sscanf in Jira markdown** — `internal/transport/jira_markdown.go:325-326` (and 132-149)
Borderline cosmetic; the INLINE/FENCE distinction is intentional.

**[LOW] `marshalJSON`/`unmarshalJSON` thin wrappers** — `internal/host/oracle_task.go:588-594`
Add naming overhead with no validation. *Fix:* Inline or rename semantically.

**[LOW] Duplicated null-time millis encoding** — `internal/turncache/sqlite.go:88-105`
Trivial and local; "duplicated elsewhere" claim overstated. Minor gap: no assertion that schema is millis-not-micros.

---

### Dead Code

All items below are confirmed zero-caller definitions. The `*Traced`-supersedes-untraced cluster in `machine.go` is the most worthwhile because it removes parallel implementations of live logic.

**[MEDIUM] Untraced machine functions superseded by `*Traced` variants** — `internal/machine/machine.go`
- `findTransition` (1596) — replaced by `findTransitionTraced`.
- `evaluateArms` (1642) — only called by the dead `findTransition`.
- `applyEffects` (1719) — replaced by `applyEffectsTraced`.
*Fix:* Remove all three together.

**[MEDIUM] `resolveTemplateValue`** — `internal/orchestrator/orchestrator.go:1915` — only self-recurses; `resolveTemplateValueLeafFallback` is the live variant. *Fix:* Remove.

**[MEDIUM] `findParallelAncestor`** — `internal/machine/parallel.go:211` — no callers. *Fix:* Remove.

**[MEDIUM] `runStartupGC`** — `cmd/kitsoki/chat_gc.go:111` — documented placeholder, never wired. *Fix:* Remove if GC no longer planned.

**[MEDIUM] `extractUsage`** — `internal/harness/live.go:282` — tied to future "Stage 7"; no callers. *Fix:* Remove or keep as documented placeholder.

**[LOW] Unused production/helper functions and types (cleanup pass):**
- `promptForEvent` + `responseForEvent` — `internal/host/oracle_event_sink.go:343, 396`.
- `resolveTaskWorkingDir` — `internal/host/oracle_task.go:599` (superseded by `appendDefaultCwd`).
- `taskReplayState` type — `internal/host/oracle_task_replay.go:76`.
- `taskStreamEvent` type — `internal/host/oracle_task_transport.go:49`.
- `bugsDir` — `internal/host/localfiles_ticket.go:95` (superseded by `ticketKindDirs`).
- `rearmFromStore` (no-op) — `internal/orchestrator/timeout.go:464`.
- `timeoutPending` — `internal/orchestrator/timeout.go:684`.
- `firstNonStop` — `internal/slotparse/parser.go:142`.
- `validateUTF8NoNUL` — `internal/store/jsonl.go:602`.
- `isOffPathEvent` — `internal/store/replay.go:150`.
- `viewProg` field — `internal/machine/machine.go:294`.
- `compiled` field — `internal/app/types.go:854`.

**[LOW] Unused test helpers/types:**
- `runKitsokiCapturingStderr` — `cmd/kitsoki/chat_test.go:546`.
- `turnExitAccepted` const — `cmd/kitsoki/turn.go:48`.
- `decideArgs` + `decideArgsWithPath` — `internal/host/oracle_decide_test.go:61, 69`.
- `slowHandler` — `internal/jobs/jobs_test.go:26`.
- `ptr[T]` — `internal/machine/machine_test.go:27`.
- `stubHarnessForRegistry` — `internal/oracle/build_registry_test.go:13`.
- `containsString` — `internal/store/jsonl_crash_test.go:412`.
- `foldJourney` + `historyBytesNoTS` — `internal/store/replay_equivalence_test.go:56, 77`.
- `invokeDispatcherWithJournal` — `internal/testrunner/cassette_oracle_test.go:441`.

---

### Cleanliness

**[LOW] Dead `err` variable and unreachable error check in `BuildRegistry`** — `internal/oracle/build_registry.go:88-127`
`var err error` is never assigned; the `if err != nil` block at 125-127 is unreachable. *Fix:* Remove.

**[LOW] Unused variable + misleading comment in `mcp/server.go`** — `internal/mcp/server.go:115-116`
`t := &mcpsdk.InMemoryTransport{}; _ = t` plus a comment describing an approach the method never takes. *Fix:* Remove the variable and comment.

**[LOW] Inefficient double marshal/unmarshal in `Workspace.ToMap`** — `internal/workspace/workspace.go:82-87`
JSON round-trip where direct field construction would be clearer (cf. `proposal.ToMap`). *Fix:* Construct the map directly.

**[LOW] Unclear discard pattern in `gitDiff`** — `internal/host/git_vcs.go:111`
`filesOut, _, _, _ := cliExec(...)` silently treats a failed `--name-only` as empty. *Fix:* Clarify intent or degrade explicitly.

**[LOW] Repetitive `m.menu, _ = ...` updates** — `internal/tui/tui.go` (~11 sites)
The discard is intentional; sites vary enough that a helper yields marginal benefit. Safe to omit.

---

## 3. Architecture Assessment

The code largely honors its stated thesis — interpretive LLM decisions are confined to oracle host calls while deterministic execution flows through the state machine and event log — and the moat is visibly real: decisions are routed through dedicated handlers, and execution is event-sourced for replay. The thesis is leaking at two specific seams. First and most important, **determinism is only as good as the event log**, and the untraced `last_error` mutations (`orchestrator.go:1391` et al.) plus the error-blind journal iterators (`journal/sqlite.go`) mean replay can silently diverge from live execution or fail to detect corruption — these directly erode the central guarantee and should be the top structural priority. Second, the **source-color wrapping contract** is enforced in one path but not the validator path, a hazard for both guard evaluation and external transports.

The structural debt most worth paying down is the **error-swallowing culture** at the I/O boundaries (journal, jobs store, scanner prompts, validator/state-file writes): individually low-risk, collectively they undermine the introspection/observability story the project sells. A close second is the **oracle host-call layer's copy-paste** (MCP tempfiles, prompt resolution, CLI-args, Bash detection). The 3178-line orchestrator is real debt but already mid-refactor and lower urgency. The dead-code tail is large but inert; a single cleanup pass clears most of it.

---

## 4. Top 10 Fixes (Prioritized)

1. **Trace `last_error` world mutations** (`orchestrator.go:1391, 1433, 1458, 2501, 2521`) — emit `EffectApplied`; restores replay determinism for an operator-visible variable.
2. **Stop wrapping validated `submitted` in the validator path** (`oracle_ask_with_mcp.go:1067`, check 1057) — prevents broken enum guards and corrupted Jira/BB audit trails.
3. **Serialize `AnswerClarification`** (`jobs/clarification.go:72-94`) — wrap check+UPDATE in a `LevelSerializable` transaction.
4. **Fix `WaitSessionDrained` goroutine leak** (`jobs/jobs.go:789-818`) — context-aware wait per the `WaitIdle` pattern.
5. **Decouple heartbeat from the tmux context** (`chatattach/attach.go:189`) — lock heartbeats must survive CLI interruption.
6. **Surface journal iterator / checkpoint errors** (`journal/sqlite.go:295-343, 406-440`) — so replay can detect DB corruption.
7. **Fix the unchecked map lookup in flowchart** (`viz/flowchart.go:605`) — comma-ok and skip unknown targets.
8. **Add `errors.Is` / `Scan()` checks at I/O boundaries** (`tui.go:1700-1715`, `offpath.go:185`, `main.go:432`, `session.go:447`) and log swallowed errors in `chat_dispatch.go:155`, `mcp/validator.go:447`, `jobs/store.go:311`, `journal_write.go:140`.
9. **Extract `writeMCPConfigTempfile`** and adopt across all ~6 oracle handlers — highest-value duplication fix.
10. **Single dead-code cleanup pass** — remove the `machine.go` untraced trio, `resolveTemplateValue`, `findParallelAncestor`, unused event-sink/task helpers and struct fields, and the test-helper tail; add `var _ host.ClarificationRequester = (*JobStore)(nil)`.
