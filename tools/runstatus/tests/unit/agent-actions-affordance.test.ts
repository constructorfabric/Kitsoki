/**
 * Unit tests for the affordance + rollup wiring (no network: we assert the
 * pre-fetch render only — the drawer/transcript fetch is exercised in the
 * playwright e2e the caller runs, never with a real LLM here).
 *   - OracleDetail.vue  — the "Agent actions (N)" affordance gates on transcript_ref
 *   - SessionRollup.vue — groups a run's transcript-bearing calls by turn
 */

import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import OracleDetail from "../../src/components/oracle/OracleDetail.vue";
import SessionRollup from "../../src/components/oracle/SessionRollup.vue";
import type { TraceEvent } from "../../src/types.js";

function oracleEvent(overrides: Partial<TraceEvent["attrs"]> = {}, turn = 1): TraceEvent {
  return {
    time: "2026-06-09T18:22:04Z",
    level: "info",
    session_id: "sess-1",
    state_path: "root.fix",
    turn,
    msg: "oracle.call.complete",
    attrs: {
      call_id: "4e96533378e89461",
      verb: "task",
      model: "claude-x",
      duration_ms: 8123,
      ...overrides,
    },
  } as TraceEvent;
}

describe("OracleDetail — agent-actions affordance", () => {
  it("shows the affordance with the transcript_ref event count for any verb", () => {
    const ev = oracleEvent({
      transcript_ref: { format: "claude-stream-json", path: "transcripts/x.jsonl", events: 12, schema_version: 1 },
    });
    const w = mount(OracleDetail, { props: { event: ev, sessionId: "sess-1" } });
    const aff = w.find('[data-testid="agent-actions-affordance"]');
    expect(aff.exists()).toBe(true);
    expect(aff.text()).toContain("Agent actions (12)");
    // The drawer is lazy: not rendered until the affordance is clicked.
    expect(w.find('[data-testid="agent-actions-drawer"]').exists()).toBe(false);
  });

  it("hides the affordance when there is no transcript_ref", () => {
    const w = mount(OracleDetail, { props: { event: oracleEvent(), sessionId: "sess-1" } });
    expect(w.find('[data-testid="agent-actions-affordance"]').exists()).toBe(false);
  });

  it("shows the affordance for a decide verb too (not task-only)", () => {
    const ev = oracleEvent(
      {
        verb: "decide",
        call_id: "e5129592efb9250c",
        transcript_ref: { format: "claude-stream-json", path: "transcripts/y.jsonl", events: 8, schema_version: 1 },
      }
    );
    const w = mount(OracleDetail, { props: { event: ev, sessionId: "sess-1" } });
    expect(w.find('[data-testid="agent-actions-affordance"]').text()).toContain("(8)");
  });
});

describe("SessionRollup", () => {
  it("groups transcript-bearing calls by turn into the rollup", () => {
    const events: TraceEvent[] = [
      oracleEvent(
        { call_id: "4e96533378e89461", verb: "task", transcript_ref: { events: 12 } },
        1
      ),
      oracleEvent(
        { call_id: "e5129592efb9250c", verb: "decide", transcript_ref: { events: 8 } },
        2
      ),
      // No transcript_ref → excluded from the rollup.
      oracleEvent({ call_id: "nope", verb: "ask" }, 2),
    ];
    const w = mount(SessionRollup, { props: { events, sessionId: "sess-1" } });
    expect(w.find('[data-testid="agent-actions-rollup"]').exists()).toBe(true);
    const calls = w.findAll('[data-testid="agent-actions-rollup-call"]');
    expect(calls).toHaveLength(2);
    expect(w.text()).toContain("4e96533378e89461");
    expect(w.text()).toContain("e5129592efb9250c");
    expect(w.text()).not.toContain("nope");
  });

  it("shows the empty state when no call carries a transcript", () => {
    const w = mount(SessionRollup, { props: { events: [oracleEvent({ call_id: "x" })], sessionId: "sess-1" } });
    expect(w.find('[data-testid="agent-actions-rollup"]').text()).toContain("No agent actions");
  });
});
