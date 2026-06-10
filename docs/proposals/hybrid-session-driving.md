# Runtime: Hybrid Jira + Live Session Driving (web)

**Status:** Mostly implemented. The drive-vs-transport model, operator
identity, the inbound bridge, and persisted-store attach all shipped — see
[`docs/architecture/transports.md` §8](../architecture/transports.md#8-driving-vs-transport).
This proposal now tracks only the **one remaining piece**: a cross-process live
SSE stream.
**Kind:**   runtime
**Epic:**   — standalone

## Shipped

The three original gaps are resolved:

1. **Operator identity** (gap 1) — the runstatus server resolves an author
   (`X-Kitsoki-Actor` header > `actor` RPC field > `kitsoki web --actor`
   default) and injects it as `slots.author` before `SubmitDirect` /
   `ContinueTurn`, so a browser-driven checkpoint records a real principal
   instead of the anonymous `'human'` fallback. A story that gates a turn on an
   author ACL with no configured identity fails fast at session start
   (`SessionRegistry.checkAuthorIdentity` + `AppDef.ReadsWorldKeyInGuard`).
2. **Inbound bridge** (gap 3) — `internal/transport/inbound`: a `Bridge` over an
   injected `Source` + deterministic `PrefixClassifier` + `Driver`, with
   BotMarker self-filter, author filter, dedup, and best-effort retry.
   Transports stay output-only.
3. **Persisted-store attach** (gap 2, in-process) — `runstatus.session.attach`
   / `SessionRegistry.AttachExternal` binds a live web session to an existing
   persisted session by external key and drives it under the writer lock, so a
   browser + the inbound bridge + a separate `session continue` process
   co-drive one session without interleaving (the loser gets `EX_TEMPFAIL`).

## Remaining: cross-process live SSE

Today live SSE reflects only turns the **web process itself** drives. A turn a
*separate* `loop.py` / `session continue` process commits is visible on the next
session reload (read from the shared store), not pushed over SSE.

Two existing primitives block a shared live stream:

- the trace JSONL takes an **exclusive flock** (`store.OpenJSONL`), so two
  processes can't both append to one live trace; and
- `server.LiveSession` serves from the **in-process** sink buffer, not by
  re-reading the file, so even a shared file wouldn't surface another process's
  appends.

### Sketch

Add a store-event-tee → shared, lock-free trace reader the web Source can poll:

- a multi-reader trace medium (append-only file opened **read-shared** by the
  web Source; the writer keeps the exclusive append lock), OR a store-events
  → `runstatus.TraceEvent` poller (`internal/runstatus/fromhistory.go` already
  has `FromHistory`, but the SQLite store drops `state_path` / `call_id` /
  `parent_turn`, so oracle-call pairing and diagram annotation degrade);
- the web Source for an attached session polls that medium each SSE tick instead
  of the in-process sink.

Decision: a read-shared trace reader preserves full fidelity; the store-events
poller is simpler but lossy. Lean toward the read-shared reader.

## Verification (shipped)

- `internal/app` — `TestReadsWorldKeyInGuard` (guard detection, identifier
  boundaries).
- `internal/runstatus/server` — `TestIdentity_*` (precedence: header > actor >
  default; explicit author wins; no-identity leaves slots untouched).
- `cmd/kitsoki` — `TestCheckAuthorIdentity` (fail-fast), `TestAttachExternal_*`
  (create+bind, re-attach reuse, bridge co-drive, bad key).
- `internal/transport/inbound` — `TestPrefixClassifier`, `TestBridge_*`
  (drive, BotMarker self-filter, author allow-list, dedup, best-effort retry).

All no-LLM (nil-harness / direct intent submission).

## Non-goals

- Durable web sessions across a server restart (continue-mode's journal; the
  `web-async-inbox` epic notes the same dependency).
- A web notification/inbox surface for background turns — the separate
  `web-async-inbox` epic.
- Any inbound read path inside a `Transport` implementation — the bridge is a
  distinct package; transports stay output-only.
