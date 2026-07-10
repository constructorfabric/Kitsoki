import { describe, expect, it } from "vitest";
import { deriveOperationRun } from "../../src/stores/run.js";
import type { TraceEvent } from "../../src/types.js";

function traceEvent(over: Partial<TraceEvent>): TraceEvent {
  return {
    time: "2026-01-01T00:00:00Z",
    level: "info",
    msg: "",
    session_id: "sess-1",
    turn: 0,
    state_path: "root/idle",
    attrs: {},
    ...over,
  };
}

describe("deriveOperationRun", () => {
  it("reads the running operation handle from durable world updates", () => {
    const run = deriveOperationRun([
      traceEvent({
        msg: "world.update",
        attrs: {
          set: {
            operation_run: {
              operation_id: "bf__capsule_demo",
              policy_id: "bf__capsule_demo",
              title: "Capsule bugfix",
              status: "running",
              from: "idle",
              to: "bugfix.reproduce",
              entry_intent: "fix_bug",
              run_in_background: true,
              phase_summary_from: ["bf_summary"],
              stop_on: ["needs_human"],
              pause_on: ["awaiting_review"],
            },
          },
        },
      }),
    ]);

    expect(run).toMatchObject({
      operationId: "bf__capsule_demo",
      policyId: "bf__capsule_demo",
      title: "Capsule bugfix",
      status: "running",
      from: "idle",
      to: "bugfix.reproduce",
      entryIntent: "fix_bug",
      runInBackground: true,
    });
    expect(run?.phaseSummaryFrom).toEqual(["bf_summary"]);
    expect(run?.stopOn).toEqual(["needs_human"]);
    expect(run?.pauseOn).toEqual(["awaiting_review"]);
  });

  it("keeps start context when the completion event carries only terminal fields", () => {
    const run = deriveOperationRun([
      traceEvent({
        msg: "operation.run_started",
        attrs: {
          operation_id: "bf__capsule_demo",
          policy_id: "bf__capsule_demo",
          title: "Capsule bugfix",
          status: "running",
          from: "idle",
          to: "bugfix.reproduce",
        },
      }),
      traceEvent({
        msg: "operation.completed",
        attrs: {
          operation_id: "bf__capsule_demo",
          policy_id: "bf__capsule_demo",
          status: "completed",
          terminal_state: "__exit__shipped",
          terminal_artifact: "bf__done_artifact",
          terminal_artifact_handle: "done#abc123",
        },
      }),
    ]);

    expect(run).toMatchObject({
      title: "Capsule bugfix",
      status: "completed",
      from: "idle",
      to: "bugfix.reproduce",
      terminalState: "__exit__shipped",
      terminalArtifact: "bf__done_artifact",
      terminalArtifactHandle: "done#abc123",
    });
  });

  it("derives a waiting operation with stop reason and detail", () => {
    const run = deriveOperationRun([
      traceEvent({
        msg: "world.update",
        attrs: {
          set: {
            operation_run: {
              operation_id: "bf__capsule_demo",
              policy_id: "bf__capsule_demo",
              title: "Capsule bugfix",
              status: "running",
              from: "idle",
              to: "bugfix.reproduce",
            },
          },
        },
      }),
      traceEvent({
        msg: "operation.waiting",
        attrs: {
          operation_id: "bf__capsule_demo",
          policy_id: "bf__capsule_demo",
          status: "waiting",
          terminal_state: "__exit__needs-human",
          stop_reason: "needs-human",
          stop_detail: "Regression gate was never RED.",
        },
      }),
    ]);

    expect(run).toMatchObject({
      title: "Capsule bugfix",
      status: "waiting",
      stopReason: "needs-human",
      stopDetail: "Regression gate was never RED.",
      terminalState: "__exit__needs-human",
      from: "idle",
      to: "bugfix.reproduce",
    });
  });
});
