/**
 * Unit tests for the bug-report capture layer: console-capture ring buffer,
 * error-capture's gatherErrorInfo (injected window + stub last-RPC source), and
 * session-capture's rolling rrweb buffer (stub emitter). No DOM-heavy libs, no
 * network.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import {
  installConsoleCapture,
  recentConsole,
  __resetConsoleCapture,
  type PatchableConsole,
} from "../../src/data/console-capture.js";
import {
  installErrorCapture,
  gatherErrorInfo,
  recentErrors,
  __resetErrorCapture,
  type ErrorWindow,
} from "../../src/data/error-capture.js";
import {
  startSessionCapture,
  snapshotSessionEvents,
  buildSessionEnvelope,
  __resetSessionCapture,
  type RrwebRecord,
  type RrwebEvent,
  type RecordOptions,
} from "../../src/data/session-capture.js";

describe("console-capture", () => {
  beforeEach(() => __resetConsoleCapture());

  it("mirrors console calls into a ring buffer keyed by level + text", () => {
    const fake: PatchableConsole = {
      log: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };
    const origLog = fake.log;
    installConsoleCapture(fake);

    fake.log("hello", 42);
    fake.warn("careful");
    fake.error("oops");

    const recent = recentConsole(10);
    expect(recent.map((e) => e.level)).toEqual(["log", "warn", "error"]);
    expect(recent[0].text).toBe("hello 42");
    expect(recent[2].text).toBe("oops");
    // Still delegates to the original console.
    expect(origLog).toHaveBeenCalledWith("hello", 42);
  });

  it("recentConsole returns only the last n entries", () => {
    const fake: PatchableConsole = {
      log: vi.fn(),
      info: vi.fn(),
      warn: vi.fn(),
      error: vi.fn(),
    };
    installConsoleCapture(fake);
    for (let i = 0; i < 25; i++) fake.log("line", i);
    const last3 = recentConsole(3);
    expect(last3.length).toBe(3);
    expect(last3[2].text).toBe("line 24");
  });
});

describe("error-capture", () => {
  beforeEach(() => __resetErrorCapture());

  it("records window.onerror + unhandledrejection and chains prior handlers", () => {
    const prior = vi.fn();
    const win: ErrorWindow = {
      onerror: prior as unknown as ErrorWindow["onerror"],
      onunhandledrejection: null,
    };
    installErrorCapture(win);

    win.onerror!("ignored", "src.js", 1, 2, new Error("kaboom"));
    win.onunhandledrejection!({
      reason: new Error("rejected"),
    } as PromiseRejectionEvent);

    const errs = recentErrors();
    expect(errs.length).toBe(2);
    expect(errs[0].kind).toBe("window");
    expect(errs[0].message).toBe("kaboom");
    expect(errs[1].kind).toBe("unhandledrejection");
    expect(errs[1].message).toBe("rejected");
    expect(prior).toHaveBeenCalledTimes(1);
  });

  it("gatherErrorInfo combines errors with the source's last RPC error", () => {
    const win: ErrorWindow = { onerror: null, onunhandledrejection: null };
    installErrorCapture(win);
    win.onerror!("boom", "s", 0, 0, new Error("boom"));

    const source = {
      lastRpcError: () => ({
        method: "runstatus.bug.report",
        code: 500,
        message: "server fail",
      }),
    };
    const info = gatherErrorInfo(source);
    expect(info.errors.length).toBe(1);
    expect(info.last_rpc).toEqual({
      method: "runstatus.bug.report",
      code: 500,
      message: "server fail",
    });
  });

  it("gatherErrorInfo tolerates a missing source (null last_rpc)", () => {
    const info = gatherErrorInfo();
    expect(info.last_rpc).toBeNull();
  });
});

describe("session-capture", () => {
  beforeEach(() => __resetSessionCapture());

  it("buffers emitted events and snapshots a copy", () => {
    let emit!: (e: RrwebEvent) => void;
    const record: RrwebRecord = (opts: RecordOptions) => {
      emit = opts.emit;
      return () => undefined;
    };
    startSessionCapture(record);

    emit({ type: 2 }); // full snapshot (checkpoint A)
    emit({ type: 3, data: "a" });
    emit({ type: 3, data: "b" });

    const snap = snapshotSessionEvents();
    expect(snap.length).toBe(3);
    expect(snap[0].type).toBe(2);
    // Snapshot is a copy — mutating it doesn't affect the buffer.
    snap.push({ type: 99 });
    expect(snapshotSessionEvents().length).toBe(3);
  });

  it("drops events older than the previous checkpoint on a new full snapshot", () => {
    let emit!: (e: RrwebEvent) => void;
    startSessionCapture((opts) => {
      emit = opts.emit;
      return undefined;
    });

    emit({ type: 2, data: "snapA" }); // checkpoint A
    emit({ type: 3, data: "a1" });
    emit({ type: 2, data: "snapB" }); // checkpoint B — keep A..now
    emit({ type: 3, data: "b1" });
    emit({ type: 2, data: "snapC" }); // checkpoint C — drop before B

    const snap = snapshotSessionEvents();
    // Should retain from checkpoint B onward (snapB, b1, snapC), dropping A/a1.
    expect(snap.map((e) => e.data)).toEqual(["snapB", "b1", "snapC"]);
  });

  it("re-prepends the original Meta event when buffer trimming dropped it", () => {
    // rrweb emits one Meta (type=4) at record start and never again. Once a
    // later checkout trims the buffer past it, snapshotSessionEvents must put it
    // back ahead of the first FullSnapshot, or the Replayer renders blank.
    let emit!: (e: RrwebEvent) => void;
    startSessionCapture((opts) => {
      emit = opts.emit;
      return undefined;
    });

    emit({ type: 4, data: "meta" }); // Meta at record start
    emit({ type: 2, data: "snapA" }); // checkpoint A
    emit({ type: 3, data: "a1" });
    emit({ type: 2, data: "snapB" }); // checkpoint B
    emit({ type: 3, data: "b1" });
    emit({ type: 2, data: "snapC" }); // checkpoint C — trims past Meta + A

    const snap = snapshotSessionEvents();
    // Meta is re-prepended ahead of the first retained FullSnapshot (snapB).
    expect(snap[0].type).toBe(4);
    expect(snap.map((e) => e.data)).toEqual([
      "meta",
      "snapB",
      "b1",
      "snapC",
    ]);
  });

  it("does not duplicate the Meta when it is still in the buffer", () => {
    let emit!: (e: RrwebEvent) => void;
    startSessionCapture((opts) => {
      emit = opts.emit;
      return undefined;
    });

    emit({ type: 4, data: "meta" });
    emit({ type: 2, data: "snapA" });
    emit({ type: 3, data: "a1" });

    const snap = snapshotSessionEvents();
    expect(snap.filter((e) => e.type === 4).length).toBe(1);
    expect(snap.map((e) => e.data)).toEqual(["meta", "snapA", "a1"]);
  });

  it("passes the required record options (checkout interval, masking)", () => {
    const record = vi.fn().mockReturnValue(() => undefined);
    startSessionCapture(record as unknown as RrwebRecord);
    const opts = record.mock.calls[0][0] as RecordOptions;
    expect(opts.checkoutEveryNms).toBe(15000);
    // Privacy boundary (committed artifact): both form values AND text nodes are
    // masked, and password inputs are blocked outright.
    expect(opts.maskAllInputs).toBe(true);
    expect(opts.maskAllText).toBe(true);
    expect(opts.blockSelector).toContain("password");
    expect(typeof opts.emit).toBe("function");
  });

  it("builds a Slidey-compatible rrweb envelope from the rolling buffer", () => {
    let emit!: (e: RrwebEvent) => void;
    startSessionCapture((opts) => {
      emit = opts.emit;
      return undefined;
    });

    emit({ type: 4, timestamp: 100, data: { href: "http://127.0.0.1/#/", width: 1280, height: 720 } });
    emit({ type: 2, timestamp: 125, data: "snap" });
    emit({ type: 3, timestamp: 250, data: "mutation" });

    const envelope = buildSessionEnvelope();
    expect(envelope).toMatchObject({
      schemaVersion: 1,
      source: "kitsoki-visual-record",
      viewport: { width: 1280, height: 720 },
      startTime: 100,
      endTime: 250,
      durationMs: 150,
    });
    expect(envelope.events.map((event) => event.type)).toEqual([4, 2, 3]);
  });
});
