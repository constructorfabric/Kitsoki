/**
 * Unit tests for the "Agent actions" drawer components:
 *   - AgentActions.vue        — the drawer (rows, waterfall toggle, accrual, diff control)
 *   - AgentActionRow.vue      — one typed row (collapsible I/O, guardrail, nudge, banner)
 *   - AgentActionWaterfall.vue — duration-proportional bars from the offsets
 *   - TranscriptDiff.vue      — cassette-vs-live drift, honest no-compare degradation
 *
 * All fixtures are passed in as already-fetched TranscriptData, so the tests
 * never touch the network / a real LLM (CLAUDE.md). The fixtures mirror the
 * real captured shapes from the producer brief (the 12-event task arc + the
 * 8-event decide arc with the _kitsoki reject/nudge/accept rows).
 */

import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import AgentActions from "../../src/components/agent/AgentActions.vue";
import AgentActionRow from "../../src/components/agent/AgentActionRow.vue";
import AgentActionWaterfall from "../../src/components/agent/AgentActionWaterfall.vue";
import TranscriptDiff from "../../src/components/agent/TranscriptDiff.vue";
import { normalizeTranscript, type TranscriptData } from "../../src/data/transcript.js";

function data(
  events: Record<string, unknown>[],
  timings: number[] = [],
  format = "claude-stream-json"
): TranscriptData {
  return { format, events, timings, schemaVersion: 1 };
}

// A tool_use/tool_result pair (the Edit row from the brief).
const TOOL_PAIR = data(
  [
    {
      type: "assistant",
      message: {
        content: [
          {
            type: "tool_use",
            id: "toolu_03",
            name: "Edit",
            input: { file_path: "internal/foo/bar.go", old_string: "a", new_string: "b" },
          },
        ],
      },
    },
    {
      type: "user",
      message: { content: [{ type: "tool_result", tool_use_id: "toolu_03", content: "edited" }] },
    },
  ],
  [10, 20]
);

// The decide arc: submit(bad) → reject → nudge → submit(good) → accept.
const DECIDE_ARC = data(
  [
    {
      type: "assistant",
      message: {
        content: [
          { type: "tool_use", id: "s0", name: "mcp__validator__submit", input: { decision: "refund", amount: "lots" } },
        ],
      },
    },
    { _kitsoki: "validator_reject", source: "schema", reason: 'amount: expected number, got string "lots"' },
    { _kitsoki: "nudge", outer_iter: 1, text: "Your previous turn ended … rejected: amount …" },
    {
      type: "assistant",
      message: {
        content: [
          { type: "tool_use", id: "s1", name: "mcp__validator__submit", input: { decision: "refund", amount: 49.0 } },
        ],
      },
    },
    { _kitsoki: "validator_accept", outer_iter: 1 },
    {
      type: "result",
      subtype: "success",
      result: "refund",
      usage: { input_tokens: 1400, output_tokens: 320 },
      total_cost_usd: 0.03,
    },
  ],
  [0, 100, 110, 200, 260, 300]
);

describe("AgentActions drawer", () => {
  it("renders the drawer with one typed row per action and a waterfall toggle", () => {
    const w = mount(AgentActions, { props: { data: TOOL_PAIR } });
    expect(w.find('[data-testid="agent-actions-drawer"]').exists()).toBe(true);
    // One tool row (the tool_result is absorbed).
    const rows = w.findAll('[data-testid="agent-action-row"]');
    expect(rows).toHaveLength(1);
    expect(rows[0]!.attributes("data-kind")).toBe("tool");
    // The diff control degrades honestly (no live transcript).
    expect(w.find('[data-testid="transcript-diff-control"]').exists()).toBe(true);
    expect(w.find('[data-testid="transcript-diff-identical"]').exists()).toBe(true);
  });

  it("switches to the waterfall mode and renders bars from the offsets", async () => {
    const w = mount(AgentActions, { props: { data: DECIDE_ARC } });
    await w.find('[data-testid="agent-actions-mode-waterfall"]').trigger("click");
    expect(w.find('[data-testid="agent-action-waterfall"]').exists()).toBe(true);
    const bars = w.findAll('[data-testid="agent-action-waterfall-bar"]');
    // One bar per normalized row of the decide arc (submit→rejected, nudge, submit→accepted, result).
    expect(bars.length).toBe(normalizeTranscript(DECIDE_ARC).length);
    // The bars carry their captured duration so the waterfall is byte-stable.
    expect(bars[0]!.attributes("data-duration-ms")).toBeDefined();
  });

  it("shows running token + cost accrual, not just the terminal total", () => {
    const w = mount(AgentActions, { props: { data: DECIDE_ARC } });
    const accrual = w.find('[data-testid="agent-actions-accrual"]');
    expect(accrual.exists()).toBe(true);
    expect(accrual.text()).toContain("1,400");
  });
});

describe("AgentActionRow", () => {
  it("renders a tool row with collapsible input and shown output", async () => {
    const [row] = normalizeTranscript(TOOL_PAIR);
    const w = mount(AgentActionRow, { props: { row: row! } });
    expect(w.find('[data-testid="agent-action-row"]').attributes("data-kind")).toBe("tool");
    // Tool rows collapse by default; expand to reveal the input diff.
    expect(w.text()).not.toContain("internal/foo/bar.go");
    await w.find('[data-testid="agent-action-row-header"]').trigger("click");
    expect(w.text()).toContain("internal/foo/bar.go");
    expect(w.text()).toContain("edited");
  });

  it("renders the guardrail row as a PASS verdict on accept", () => {
    const rows = normalizeTranscript(DECIDE_ARC);
    // The final submit row is stamped accepted by the trailing validator_accept.
    const accepted = rows.find((r) => r.title === "Guardrail accepted")!;
    const w = mount(AgentActionRow, { props: { row: accepted } });
    expect(w.find('[data-testid="guardrail-row"]').exists()).toBe(true);
    expect(w.text()).toContain("PASS");
  });

  it("renders the guardrail row as a REJECTED verdict on reject", () => {
    const rows = normalizeTranscript(DECIDE_ARC);
    const reject = rows.find((r) => r.isError && r.kind === "guardrail")!;
    const w = mount(AgentActionRow, { props: { row: reject } });
    expect(w.find('[data-testid="guardrail-row"]').exists()).toBe(true);
    expect(w.text()).toContain("REJECTED");
    expect(w.text()).toContain("expected number");
  });

  it("renders the host nudge as a distinct row (not a model turn)", () => {
    const rows = normalizeTranscript(DECIDE_ARC);
    const nudge = rows.find((r) => r.kind === "host-nudge")!;
    const w = mount(AgentActionRow, { props: { row: nudge } });
    expect(w.find('[data-testid="nudge-row"]').exists()).toBe(true);
    expect(w.text()).toContain("NUDGE");
  });

  it("renders a tool_bypassed banner row", () => {
    const [row] = normalizeTranscript(
      data([{ _kitsoki: "tool_bypassed", verdict_recovered_from: "code_block" }])
    );
    const w = mount(AgentActionRow, { props: { row: row! } });
    expect(w.find('[data-testid="banner-row"]').exists()).toBe(true);
    expect(w.text()).toContain("code_block");
  });
});

describe("AgentActionWaterfall", () => {
  it("renders one bar per row with proportional widths", () => {
    const rows = normalizeTranscript(DECIDE_ARC);
    const w = mount(AgentActionWaterfall, { props: { rows } });
    const bars = w.findAll('[data-testid="agent-action-waterfall-bar"]');
    expect(bars).toHaveLength(rows.length);
    // The rejected submit is row 0 (offset 0); row 1 is the host nudge at offset 110
    // (the validator_reject is stamped onto the prior submit row, not a separate bar).
    expect(bars[1]!.attributes("data-offset-ms")).toBe("110");
  });

  it("degrades to an empty notice when there are no rows", () => {
    const w = mount(AgentActionWaterfall, { props: { rows: [] } });
    expect(w.find('[data-testid="agent-action-waterfall"]').text()).toContain("No capture-time");
  });
});

describe("TranscriptDiff", () => {
  it("shows the honest no-compare state when there is no live transcript", () => {
    const w = mount(TranscriptDiff, { props: { recorded: TOOL_PAIR, live: null } });
    expect(w.find('[data-testid="transcript-diff"]').exists()).toBe(true);
    expect(w.find('[data-testid="transcript-diff-identical"]').exists()).toBe(true);
    expect(w.find('[data-testid="transcript-diff-identical"]').text()).toContain("byte-identical");
  });

  it("reports no drift when the live tool path matches the cassette", () => {
    const w = mount(TranscriptDiff, { props: { recorded: TOOL_PAIR, live: TOOL_PAIR } });
    expect(w.find('[data-testid="transcript-diff-identical"]').exists()).toBe(false);
    expect(w.text()).toContain("No drift");
  });

  it("flags drift when the live tool path diverges", () => {
    const live = data([
      {
        type: "assistant",
        message: { content: [{ type: "tool_use", id: "x", name: "Write", input: {} }] },
      },
    ]);
    const w = mount(TranscriptDiff, { props: { recorded: TOOL_PAIR, live } });
    expect(w.text()).toContain("Drift detected");
    const rows = w.findAll('[data-testid="transcript-diff-row"]');
    // Edit (removed) + Write (added).
    expect(rows.length).toBe(2);
    expect(rows.some((r) => r.attributes("data-status") === "removed")).toBe(true);
    expect(rows.some((r) => r.attributes("data-status") === "added")).toBe(true);
  });
});
