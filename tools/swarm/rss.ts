/**
 * rss.ts — informational server memory watcher for a swarm run.
 *
 * Tier-1 has no cap yet (that's swarm-session-cap, a separate change) — this
 * module only OBSERVES RSS across the run and records it in the results JSON
 * so a future cap has a real baseline to tune against. It is deliberately NOT
 * a gate: a swarm run never fails on memory here.
 *
 * Finds the server's process (and, in `go run` mode, its compiled child —
 * `go run` execs a child binary under a supervisor process, and go run mode
 * is exactly what this repo's own conventions steer local/dev/test runs
 * toward — see AGENTS.md "avoid generating a binary of kitsoki for
 * testing") by grepping `ps` for a command line containing the bound
 * address, then sums RSS (KB) across every matching pid. Uses only `ps`
 * (no extra npm dependency) — available on both the macOS and Linux CI
 * images this repo runs on.
 */
import { execSync } from "child_process";

/** Every live pid whose command line mentions `--addr <addr>` (covers both
 *  the `go run` supervisor and its compiled child, and the built-binary
 *  case, whichever is running). Best-effort: returns [] on any ps failure
 *  (e.g. sandboxed CI without /bin/ps) rather than throwing — RSS tracking
 *  is informational, never load-bearing. */
export function findPidsByAddr(addr: string): number[] {
  try {
    const out = execSync("ps -eo pid=,command=", { encoding: "utf8", maxBuffer: 8 * 1024 * 1024 });
    const pids: number[] = [];
    for (const line of out.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      const m = trimmed.match(/^(\d+)\s+(.*)$/);
      if (!m) continue;
      const [, pidStr, cmd] = m;
      if (cmd.includes(`--addr ${addr}`) || cmd.includes(`--addr=${addr}`)) {
        pids.push(Number(pidStr));
      }
    }
    return pids;
  } catch {
    return [];
  }
}

/** Sum RSS (KB) across `pids`. Best-effort: a pid that has already exited
 *  between findPidsByAddr and this call is silently skipped. */
export function totalRssKB(pids: number[]): number {
  if (pids.length === 0) return 0;
  try {
    const out = execSync(`ps -o rss= -p ${pids.join(",")}`, { encoding: "utf8" });
    return out
      .split("\n")
      .map((s) => parseInt(s.trim(), 10))
      .filter((n) => Number.isFinite(n))
      .reduce((a, b) => a + b, 0);
  } catch {
    return 0;
  }
}

export interface RssSample {
  t_ms: number;
  kb: number;
}

export interface RssSummary {
  addr: string;
  samples: RssSample[];
  min_kb: number;
  max_kb: number;
  avg_kb: number;
}

/** Polls total RSS for the server bound at `addr` on an interval, in-memory,
 *  until `stop()` is called. */
export class RssWatcher {
  private readonly addr: string;
  private readonly t0 = Date.now();
  private readonly samples: RssSample[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;

  constructor(addr: string) {
    this.addr = addr;
  }

  start(intervalMs = 1000): void {
    this.sampleOnce();
    this.timer = setInterval(() => this.sampleOnce(), intervalMs);
    // Don't hold the process open for this timer alone.
    if (typeof this.timer.unref === "function") this.timer.unref();
  }

  private sampleOnce(): void {
    const pids = findPidsByAddr(this.addr);
    const kb = totalRssKB(pids);
    this.samples.push({ t_ms: Date.now() - this.t0, kb });
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  summary(): RssSummary {
    const kbs = this.samples.map((s) => s.kb).filter((k) => k > 0);
    return {
      addr: this.addr,
      samples: this.samples,
      min_kb: kbs.length ? Math.min(...kbs) : 0,
      max_kb: kbs.length ? Math.max(...kbs) : 0,
      avg_kb: kbs.length ? Math.round(kbs.reduce((a, b) => a + b, 0) / kbs.length) : 0,
    };
  }
}
