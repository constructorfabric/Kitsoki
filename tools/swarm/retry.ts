/**
 * retry.ts — bounded retry-with-backoff for the swarm's RPC calls.
 *
 * FINDING this harness surfaced (see tools/swarm/README.md "Known finding"):
 * cmd/kitsoki/runtime.go opens a FRESH `*sql.DB` (internal/store.Open) per
 * minted session against the SAME shared SQLite file, rather than sharing one
 * connection/pool across the registry. `internal/store/sqlite.go` already
 * sets `PRAGMA busy_timeout=5000` per connection, but that pragma is applied
 * AFTER `PRAGMA journal_mode=WAL` on a brand-new connection, so a burst of
 * concurrent `session.new` calls can still hit a hard SQLITE_BUSY on that
 * very first pragma before its own busy_timeout is in effect. Under a true
 * thundering-herd burst (24 users minting within the same tens of
 * milliseconds) this is reproducible.
 *
 * This tier's job is to soak and REPORT, not to redesign the store's
 * connection strategy (out of this change's declared scope — see
 * docs/goals/ui-qa-scale/decomposition.yaml's swarm-tier1 scope list). A
 * bounded client-side retry on the known-transient "database is locked" /
 * SQLITE_BUSY error is a legitimate, realistic client behavior (any real web
 * client would see a transient 5xx under overload and retry) — NOT a
 * workaround that hides the finding; the finding is written up in the
 * README/report regardless of whether the retry absorbs it in a given run.
 */

export interface RetryOptions {
  attempts?: number;
  baseDelayMs?: number;
  maxDelayMs?: number;
  retryable?: (err: unknown) => boolean;
}

/** Matches the two error shapes this harness has actually observed:
 *  Go's `database is locked` (mattn/modernc sqlite driver message) and the
 *  raw `SQLITE_BUSY` code some driver paths surface instead. */
export function isTransientStoreBusy(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return /database is locked|SQLITE_BUSY/i.test(msg);
}

export async function withRetry<T>(fn: () => Promise<T>, opts: RetryOptions = {}): Promise<T> {
  const attempts = opts.attempts ?? 8;
  const base = opts.baseDelayMs ?? 200;
  const max = opts.maxDelayMs ?? 3000;
  const retryable = opts.retryable ?? isTransientStoreBusy;
  let lastErr: unknown;
  for (let i = 0; i < attempts; i++) {
    try {
      return await fn();
    } catch (err) {
      lastErr = err;
      if (!retryable(err) || i === attempts - 1) throw err;
      const delay = Math.min(max, base * 2 ** i) * (0.5 + Math.random());
      await new Promise((r) => setTimeout(r, delay));
    }
  }
  // Unreachable (the loop always returns or throws), but keeps TS happy.
  throw lastErr;
}

/** Wraps an RPC function so every call retries transient store-busy errors. */
export function wrapRpcWithRetry<Rpc extends <T>(method: string, params: Record<string, unknown>) => Promise<T>>(
  rpc: Rpc,
  opts: RetryOptions = {},
): Rpc {
  const wrapped = (async <T>(method: string, params: Record<string, unknown>): Promise<T> =>
    withRetry(() => rpc<T>(method, params), opts)) as Rpc;
  return wrapped;
}
