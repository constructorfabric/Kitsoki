/**
 * serverSha.ts — a stand-in for a "server build sha" to stamp into finding
 * bundles.
 *
 * There is no live `/api/version` RPC or endpoint on `kitsoki web` today (the
 * only existing precedent is `cmd/kitsoki/main.go`'s `version` var, stamped
 * via `-ldflags` at RELEASE build time only, and
 * `internal/runstatus/server/bug_report.go`'s best-effort `gitShortRev`, used
 * to label filed-bug evidence with the repo state at filing time). Rather than
 * inventing a new RPC surface for this change, we reuse that same
 * best-effort convention client-side: the short git HEAD sha of the repo the
 * swarm is running against, which for a `go run ./cmd/kitsoki` dev server (the
 * normal swarm posture — see tools/swarm/README.md) IS the server's actual
 * build state.
 *
 * Falls back to "unknown" (never throws) so a capture always completes even
 * outside a git checkout (e.g. a stripped CI archive).
 */
import { spawnSync } from "child_process";

let cached: string | null = null;

/** Short git HEAD sha for `repoRoot`, memoized for the process lifetime.
 *  Returns "unknown" if git isn't available or the tree isn't a repo. */
export function getServerSha(repoRoot: string): string {
  if (cached !== null) return cached;
  try {
    const res = spawnSync("git", ["-C", repoRoot, "rev-parse", "--short", "HEAD"], {
      encoding: "utf-8",
    });
    const sha = res.stdout?.trim();
    cached = res.status === 0 && sha ? sha : "unknown";
  } catch {
    cached = "unknown";
  }
  return cached;
}

/** Test-only: clear the memoized sha. */
export function resetServerShaCacheForTests(): void {
  cached = null;
}
