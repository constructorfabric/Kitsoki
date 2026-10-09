/**
 * concurrency.ts — a tiny in-process concurrency limiter, no npm dependency.
 *
 * FINDING this harness surfaced (see tools/swarm/README.md "Known finding:
 * turn-processing throughput under a true 24-way burst"): with all 24
 * sessions minted and live, letting every user's remaining scripted clicks
 * fire with NO throttling causes some users' turns to queue behind others
 * for well over a minute on this environment (session MINTING itself is
 * comparatively cheap and was not the bottleneck once tools/swarm/retry.ts's
 * retry was in place — the sustained per-turn processing throughput under
 * 24-way concurrent load was). This is a legitimate capacity signal a soak
 * harness is supposed to produce, not something to fix here (out of this
 * change's scope — cmd/kitsoki's turn-processing path, not tools/swarm/**).
 *
 * The harness's job is to soak REALISTICALLY: real end users do not click
 * their next button in perfect lockstep with 23 other people, and a
 * responsible load-generator throttles its OWN request concurrency rather
 * than assuming the target has unbounded capacity. `createLimiter` bounds
 * how many users' INTERACTIVE steps (the actual clickIntent/sendText calls)
 * are in flight at once — every session is still minted and its page kept
 * open for the WHOLE run (see swarm-replay-users.spec.ts's two-phase
 * mint-then-drive structure), so "24 concurrently live" is never
 * compromised; only how many are simultaneously mid-turn is bounded.
 */

export type Release = () => void;

/** Returns a `run` function: `await run(fn)` waits for a free slot (up to
 *  `concurrency` callers may be inside `fn` at once), runs `fn`, then frees
 *  the slot whether `fn` resolved or rejected. */
export function createLimiter(concurrency: number): <T>(fn: () => Promise<T>) => Promise<T> {
  if (concurrency < 1) throw new Error("createLimiter: concurrency must be >= 1");
  let active = 0;
  const queue: Array<() => void> = [];

  function acquire(): Promise<Release> {
    return new Promise((resolve) => {
      const tryAcquire = () => {
        if (active < concurrency) {
          active++;
          resolve(() => {
            active--;
            const next = queue.shift();
            if (next) next();
          });
        } else {
          queue.push(tryAcquire);
        }
      };
      tryAcquire();
    });
  }

  return async <T>(fn: () => Promise<T>): Promise<T> => {
    const release = await acquire();
    try {
      return await fn();
    } finally {
      release();
    }
  };
}
