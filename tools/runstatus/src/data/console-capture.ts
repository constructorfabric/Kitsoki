/**
 * Console capture: patches console.{log,info,warn,error} into a bounded ring
 * buffer of {level, ts, text}. Used to attach recent console output to a bug
 * report. Injectable console object so tests can drive it without touching the
 * global console.
 */

export interface ConsoleEntry {
  level: string;
  ts: number;
  text: string;
}

/** Minimal console surface we patch. */
export type PatchableConsole = Pick<
  Console,
  "log" | "info" | "warn" | "error"
>;

const RING_CAPACITY = 200;
const ring: ConsoleEntry[] = [];

function push(level: string, args: unknown[]): void {
  try {
    const text = args
      .map((a) => {
        if (typeof a === "string") return a;
        try {
          return JSON.stringify(a);
        } catch {
          return String(a);
        }
      })
      .join(" ");
    ring.push({ level, ts: Date.now(), text });
    if (ring.length > RING_CAPACITY) ring.splice(0, ring.length - RING_CAPACITY);
  } catch {
    /* never let capture throw */
  }
}

let installed = false;

/**
 * Patch the given console (defaults to the global) so each call is mirrored
 * into the ring buffer before delegating to the original. Idempotent for the
 * global console; an injected console is always patched (tests pass a fresh
 * stub each time).
 */
export function installConsoleCapture(
  consoleObj: PatchableConsole = console
): void {
  const isGlobal = consoleObj === (console as PatchableConsole);
  if (isGlobal && installed) return;
  if (isGlobal) installed = true;

  for (const level of ["log", "info", "warn", "error"] as const) {
    const orig = consoleObj[level].bind(consoleObj);
    consoleObj[level] = (...args: unknown[]): void => {
      push(level, args);
      orig(...args);
    };
  }
}

/** Return the most recent `n` console entries (default 10), oldest-first. */
export function recentConsole(n = 10): ConsoleEntry[] {
  return ring.slice(Math.max(0, ring.length - n));
}

/** Test helper: clear the ring buffer. */
export function __resetConsoleCapture(): void {
  ring.length = 0;
  installed = false;
}
