/**
 * Unit tests for src/components/TraceTimeline.vue
 */

import { describe, it, expect } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import TraceTimeline from "../../src/components/TraceTimeline.vue";
import type { TraceEvent } from "../../src/types.js";

// ---- Fixtures --------------------------------------------------------------

function makeEvent(
  overrides: Partial<TraceEvent> & { msg: string; turn: number }
): TraceEvent {
  return {
    time: "2026-01-01T00:00:01Z",
    level: "info",
    session_id: "sess-1",
    state_path: "root.active",
    attrs: {},
    ...overrides,
  };
}

// EVENTS: turn.start/end and machine.transition/state_* are suppressed.
// Visible by default: host.invoked, host.returned (group 1), agent.decide.start (group 2).
const EVENTS: TraceEvent[] = [
  makeEvent({ msg: "turn.start",          turn: 1, state_path: "root.active" }),
  makeEvent({ msg: "host.invoked",        turn: 1, state_path: "root.active", time: "2026-01-01T00:00:02Z" }),
  makeEvent({ msg: "host.returned",       turn: 1, state_path: "root.active", time: "2026-01-01T00:00:03Z" }),
  makeEvent({ msg: "agent.call.start", turn: 2, state_path: "root.done",   time: "2026-01-01T00:00:04Z", attrs: { call_id: "c1", verb: "decide" } }),
  makeEvent({ msg: "turn.end",            turn: 2, state_path: "root.done",   time: "2026-01-01T00:00:05Z" }),
];

// ---- Tests -----------------------------------------------------------------

describe("TraceTimeline — rendering", () => {
  it("renders events matching default filter state (turn subsystem off by default)", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
    });
    await flushPromises();

    // turn.start and turn.end are "turn" subsystem, hidden by default.
    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(3);
    wrapper.unmount();
  });

  it("shows empty state when events array is empty", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: [], selectedEventIndex: null },
    });
    await flushPromises();

    expect(wrapper.find(".trace-timeline__empty").exists()).toBe(true);
    wrapper.unmount();
  });
});

describe("TraceTimeline — grouping by turn", () => {
  it("renders one turn-header per distinct turn (ascending order)", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
    });
    await flushPromises();

    // turn.start lives in its own turn's group: 2 groups total (1, 2).
    const headers = wrapper.findAll(".trace-timeline__turn-header");
    expect(headers.length).toBe(2);

    const labels = headers.map((h) => h.find(".trace-timeline__turn-label").text());
    expect(labels[0]).toContain("1");
    expect(labels[1]).toContain("2");

    wrapper.unmount();
  });

  it("shows event count in each turn header", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
    });
    await flushPromises();

    const headers = wrapper.findAll(".trace-timeline__turn-header");
    // turn.start/end and machine.transition are suppressed.
    // Group 1: [host.invoked, host.returned] → 2 events
    // Group 2: [agent.decide.start] → 1 event
    expect(headers[0]!.find(".trace-timeline__turn-count").text()).toContain("2");
    expect(headers[1]!.find(".trace-timeline__turn-count").text()).toContain("1");

    wrapper.unmount();
  });

  it("collapses a turn when its header is clicked", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
    });
    await flushPromises();

    // 3 visible by default (turn chip off).
    expect(wrapper.findAll(".trace-timeline__row").length).toBe(3);

    // Click the second turn header (group 2, which has 1 visible event).
    const headers = wrapper.findAll(".trace-timeline__turn-header");
    await headers[1]!.trigger("click");

    // Group 2 row (1) hidden; group 1 (2 events) still visible.
    expect(wrapper.findAll(".trace-timeline__row").length).toBe(2);

    wrapper.unmount();
  });
});

describe("TraceTimeline — subsystem chips", () => {
  it("derives subsystem from msg prefix and renders a chip", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
    });
    await flushPromises();

    // 3 visible rows (host, host, agent) — turn/machine rows are suppressed.
    const chips = wrapper.findAll(".trace-timeline__subsystem-chip");
    expect(chips.length).toBe(3);

    const sysList = chips.map((c) => c.attributes("data-subsystem"));
    expect(sysList).toContain("host");
    expect(sysList).toContain("agent");

    wrapper.unmount();
  });
});

describe("TraceTimeline — filters", () => {
  it("filters events when a subsystem chip is deactivated", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    // Default: 3 visible (turn chip already off). Click "host" to deselect it.
    const chips = wrapper.findAll(".trace-timeline__chip");
    const hostChip = chips.find((c) => c.text() === "host");
    expect(hostChip).toBeDefined();

    await hostChip!.trigger("click");

    // 2 host events hidden; 1 remains (agent.decide.start).
    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(1);

    wrapper.unmount();
  });

  it("shows clear button when filters are active, resets on click", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    // Initially no clear button — "turn" off is the default, not an active filter.
    expect(wrapper.find(".trace-timeline__chip--clear").exists()).toBe(false);

    // Deactivate a subsystem beyond the default.
    const chips = wrapper.findAll(".trace-timeline__chip");
    const hostChip = chips.find((c) => c.text() === "host");
    await hostChip!.trigger("click");

    expect(wrapper.find(".trace-timeline__chip--clear").exists()).toBe(true);

    // Click clear — should restore to default state (3 visible, not 5).
    await wrapper.find(".trace-timeline__chip--clear").trigger("click");

    expect(wrapper.findAll(".trace-timeline__row").length).toBe(3);
    expect(wrapper.find(".trace-timeline__chip--clear").exists()).toBe(false);

    wrapper.unmount();
  });

  it("filters by state_path when a state is selected", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    const select = wrapper.find(".trace-timeline__select");
    expect(select.exists()).toBe(true);

    // Select "root.done" — agent.decide.start matches (turn.end is "turn" subsystem, hidden).
    await select.setValue("root.done");

    expect(wrapper.findAll(".trace-timeline__row").length).toBe(1);

    wrapper.unmount();
  });
});

describe("TraceTimeline — row click emits select", () => {
  it("emits select with the correct index when a row is clicked", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    // Click the first visible row (group 1, host.invoked = original index 1).
    await rows[0]!.trigger("click");

    const emitted = wrapper.emitted("select") as [number][] | undefined;
    expect(emitted).toBeDefined();
    expect(typeof emitted![0]![0]).toBe("number");

    wrapper.unmount();
  });

  it("applies .selected class to the row matching selectedEventIndex", async () => {
    // Index 1 = host.invoked, which is visible by default (not a "turn" event).
    const wrapper = mount(TraceTimeline, {
      props: { events: EVENTS, selectedEventIndex: 1 },
    });
    await flushPromises();

    const selectedRows = wrapper.findAll(".trace-timeline__row.selected");
    expect(selectedRows.length).toBe(1);

    wrapper.unmount();
  });
});

describe("TraceTimeline — row expand", () => {
  it("shows attrs pre block when expand button is clicked", async () => {
    const wrapper = mount(TraceTimeline, {
      props: {
        events: [makeEvent({ msg: "machine.intent_accepted", turn: 1, attrs: { foo: "bar" } })],
        selectedEventIndex: null,
      },
      attachTo: document.body,
    });
    await flushPromises();

    expect(wrapper.find(".trace-timeline__row-body").exists()).toBe(false);

    await wrapper.find(".trace-timeline__expand-btn").trigger("click");

    const pre = wrapper.find(".event-detail__pre");
    expect(pre.exists()).toBe(true);
    expect(pre.text()).toContain("bar");

    wrapper.unmount();
  });
});

describe("TraceTimeline — agent start/complete merge", () => {
  function agentEvents(): TraceEvent[] {
    return [
      makeEvent({
        msg: "agent.call.start",
        turn: 1,
        time: "2026-01-01T00:00:01Z",
        attrs: { call_id: "call-paired", verb: "task", agent: "reproducer" },
      }),
      makeEvent({
        msg: "agent.call.complete",
        turn: 1,
        time: "2026-01-01T00:00:03Z",
        attrs: { call_id: "call-paired", duration_ms: 2000, verb: "task" },
      }),
      makeEvent({
        msg: "agent.call.start",
        turn: 1,
        time: "2026-01-01T00:00:04Z",
        attrs: { call_id: "call-orphan", verb: "ask" },
      }),
    ];
  }

  it("collapses a paired start/complete into one row carrying the elapsed time", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: agentEvents(), selectedEventIndex: null },
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    // 3 raw events → 2 rows (the paired .complete is suppressed).
    expect(rows.length).toBe(2);

    const durations = wrapper.findAll(".trace-timeline__duration");
    expect(durations.length).toBe(1);
    expect(durations[0]!.text()).toMatch(/^2(\.\d+)?s$/);

    wrapper.unmount();
  });

  it("flags a start with no matching complete as incomplete", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: agentEvents(), selectedEventIndex: null },
    });
    await flushPromises();

    const incomplete = wrapper.findAll(".trace-timeline__incomplete");
    expect(incomplete.length).toBe(1);
    expect(incomplete[0]!.text()).toBe("incomplete");

    wrapper.unmount();
  });

  it("collapses a failed call into one row and nests stream frames under it", async () => {
    const events: TraceEvent[] = [
      makeEvent({
        msg: "agent.call.start",
        turn: 1,
        time: "2026-01-01T00:00:01Z",
        attrs: { call_id: "call-failed", verb: "decide", agent: "gate_planner" },
      }),
      makeEvent({
        msg: "agent.stream",
        turn: 1,
        time: "2026-01-01T00:00:02Z",
        attrs: {
          call_id: "call-failed",
          type: "assistant",
          text: "thinking about the gate",
          preview: "thinking about the gate",
        },
      }),
      makeEvent({
        msg: "agent.stream",
        turn: 1,
        time: "2026-01-01T00:00:03Z",
        attrs: {
          call_id: "call-failed",
          type: "assistant",
          tool: "Read",
          preview: "app.yaml",
        },
      }),
      makeEvent({
        msg: "agent.call.error",
        turn: 1,
        time: "2026-01-01T00:00:04Z",
        attrs: {
          call_id: "call-failed",
          duration_ms: 3000,
          verb: "decide",
          error: "validator failed",
        },
      }),
    ];
    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(1);
    expect(rows[0]!.find(".trace-timeline__msg").text()).toBe("agent.decide");
    expect(wrapper.text()).not.toContain("agent.tool Read");
    expect(wrapper.findAll('[data-testid="trace-agent-stream-row"]').length).toBe(0);

    await rows[0]!.find(".trace-timeline__expand-btn").trigger("click");
    await flushPromises();

    const streamRows = wrapper.findAll('[data-testid="trace-agent-stream-row"]');
    expect(streamRows.length).toBe(2);
    expect(streamRows[0]!.text()).toContain("agent.delta");
    expect(streamRows[0]!.text()).toContain("thinking about the gate");
    expect(streamRows[1]!.text()).toContain("agent.tool Read");
    expect(streamRows[1]!.text()).toContain("app.yaml");

    wrapper.unmount();
  });
});

describe("TraceTimeline — turn.input ordering", () => {
  // Reproduces the 'reproducing' phase: agent decides in turn 1, the user
  // accepts and the machine executes that intent (host + world) in turn 2.
  // turn.input ('[intent] accept') carries the SAME turn (2) as the work it
  // triggers; the UI defers it to the end of that turn group so the input chip
  // renders LAST — after the host/world rows it triggered.
  function reproducingEvents(): TraceEvent[] {
    return [
      makeEvent({ msg: "agent.call.start",  turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:01Z", attrs: { call_id: "c-task", verb: "task" } }),
      makeEvent({ msg: "agent.call.complete", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:02Z", attrs: { call_id: "c-task", duration_ms: 1000, verb: "task" } }),
      makeEvent({ msg: "agent.call.start", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:03Z", attrs: { call_id: "c-decide", verb: "decide" } }),
      makeEvent({ msg: "agent.call.complete", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:04Z", attrs: { call_id: "c-decide", duration_ms: 500, verb: "decide" } }),
      // turn.input carries the SAME turn (2) as the work it triggers, and the
      // UI defers it to the end of that turn group — so it should display LAST.
      makeEvent({ msg: "turn.input", turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:05Z", attrs: { input: "[intent] accept" } }),
      // turn 2: machine executes the accepted intent.
      makeEvent({ msg: "harness.called",   turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:05Z", attrs: { namespace: "host.append_to_file.post" } }),
      makeEvent({ msg: "harness.dispatched", turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:05Z", attrs: { namespace: "host.append_to_file.post" } }),
      makeEvent({ msg: "harness.returned",   turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:05Z", attrs: { namespace: "host.append_to_file.post" } }),
      makeEvent({ msg: "world.update",     turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:06Z", attrs: { set: { bf_status: "done" } } }),
    ];
  }

  it("turn.input [intent] appears after host and world.update rows", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: reproducingEvents(), selectedEventIndex: null },
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    // Visible: agent.task (merged), agent.decide (merged), host.append, world.update, turn.input.
    // agent.complete rows are suppressed; harness.dispatched/returned absorbed.
    expect(rows.length).toBe(5);

    const msgs = rows.map((r) => r.find(".trace-timeline__msg").text());
    const inputIdx = msgs.findIndex((m) => m.includes("[intent] accept"));
    const worldIdx = msgs.findIndex((m) => m.includes("world.update"));
    const hostIdx  = msgs.findIndex((m) => m.includes("host.append_to_file.post"));

    expect(inputIdx).toBeGreaterThan(-1);
    expect(worldIdx).toBeGreaterThan(-1);
    expect(hostIdx).toBeGreaterThan(-1);

    // [intent] accept must appear after both host and world rows.
    expect(inputIdx).toBeGreaterThan(hostIdx);
    expect(inputIdx).toBeGreaterThan(worldIdx);
  });

  it("turn.input with no subsequent turn group stays in its original group", async () => {
    // Only turn 1 — no turn 2 to move turn.input into.
    const events: TraceEvent[] = [
      makeEvent({ msg: "agent.call.start",   turn: 1, state_path: "root.active", attrs: { call_id: "cx", verb: "ask" } }),
      makeEvent({ msg: "agent.call.complete", turn: 1, state_path: "root.active", time: "2026-01-01T00:00:02Z", attrs: { call_id: "cx", duration_ms: 100, verb: "ask" } }),
      makeEvent({ msg: "turn.input", turn: 1, state_path: "root.active", time: "2026-01-01T00:00:03Z", attrs: { input: "[intent] done" } }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null },
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(2); // agent.ask (merged) + turn.input

    const msgs = rows.map((r) => r.find(".trace-timeline__msg").text());
    const inputIdx = msgs.findIndex((m) => m.includes("[intent] done"));
    expect(inputIdx).toBe(1); // last row in the only group

    wrapper.unmount();
  });
});

describe("TraceTimeline — phase headers", () => {
  // A minimal mermaid source with two phases, each owning one room. Room
  // labels ("reproducing", "proposing") match the events' state_path values,
  // so phaseForStatePath resolves the header text from the diagram.
  const MERMAID = `flowchart LR
  Start(["<b>Start</b>"]):::input
  subgraph SG_reproducing["<b>phase 1 · reproducing</b> — Reproduce the bug."]
    direction LR
    ST_reproducing[/"reproducing"/]:::room
  end
  subgraph SG_proposing["<b>phase 4 · proposing</b> — Draft the fix."]
    direction LR
    ST_proposing[/"proposing"/]:::room
  end
  Start --> ST_reproducing
  ST_reproducing -- "accept" --> ST_proposing
`;

  const PHASE_EVENTS: TraceEvent[] = [
    makeEvent({ msg: "host.invoked",  turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:01Z" }),
    makeEvent({ msg: "host.returned", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:02Z" }),
    makeEvent({ msg: "host.invoked",  turn: 2, state_path: "proposing",   time: "2026-01-01T00:00:03Z" }),
    makeEvent({ msg: "host.returned", turn: 2, state_path: "proposing",   time: "2026-01-01T00:00:04Z" }),
  ];

  it("labels each turn's phase header from the mermaid source (regression: was '—')", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: PHASE_EVENTS, selectedEventIndex: null, mermaidSource: MERMAID },
    });
    await flushPromises();

    const phaseNames = wrapper
      .findAll(".trace-timeline__phase-header .trace-timeline__turn-phase")
      .map((h) => h.text());

    expect(phaseNames).toEqual(["reproducing", "proposing"]);
    // The '—' fallback (empty / unresolved state_path) must NOT appear.
    expect(phaseNames).not.toContain("—");

    wrapper.unmount();
  });

  it("falls back to '—' when events carry empty state_path", async () => {
    // This is exactly the broken-trace symptom: empty state_path → no phase.
    // Guards that the header text is data-driven, so a faithful trace (with
    // state_path) is what restores the phase names — not a UI default.
    const emptyPathEvents: TraceEvent[] = [
      makeEvent({ msg: "host.invoked",  turn: 1, state_path: "", time: "2026-01-01T00:00:01Z" }),
      makeEvent({ msg: "host.returned", turn: 1, state_path: "", time: "2026-01-01T00:00:02Z" }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events: emptyPathEvents, selectedEventIndex: null, mermaidSource: MERMAID },
    });
    await flushPromises();

    const phaseNames = wrapper
      .findAll(".trace-timeline__phase-header .trace-timeline__turn-phase")
      .map((h) => h.text());
    expect(phaseNames).toEqual(["—"]);

    wrapper.unmount();
  });

  // A phase whose work is revisited after another phase must render at the
  // revisit point. The trace is the audit record, so chronological order wins
  // over globally de-duping phase headers.
  it("preserves chronological phase visits when a phase is revisited", async () => {
    const events: TraceEvent[] = [
      makeEvent({ msg: "host.invoked",  turn: 1, state_path: "proposing",   time: "2026-01-01T00:00:01Z" }),
      makeEvent({ msg: "host.invoked",  turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:02Z" }),
      // proposing revisited in a later, non-adjacent turn:
      makeEvent({ msg: "host.invoked",  turn: 3, state_path: "proposing",   time: "2026-01-01T00:00:03Z" }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null, mermaidSource: MERMAID },
    });
    await flushPromises();

    const phaseNames = wrapper
      .findAll(".trace-timeline__phase-header .trace-timeline__turn-phase")
      .map((h) => h.text());
    expect(phaseNames).toEqual(["proposing", "reproducing", "proposing"]);

    wrapper.unmount();
  });
});

describe("TraceTimeline — visit grouping (intent, not raw turn)", () => {
  // A turn straddles the transition it triggers: room X's intent fires in turn
  // N, and the *entered* room Y's on-enter work + state_entered land in that
  // SAME turn N — so a room's entry events carry the PREVIOUS decision's turn
  // number. Grouping by raw turn therefore splits one room visit across two
  // turn boxes. The timeline groups by VISIT instead: every event for a room
  // routes to the turn whose machine.intent_accepted closes it, so one visit =
  // one group carrying its single intent, even across two raw turns.
  function reproducingVisit(): TraceEvent[] {
    return [
      // Entry: on-enter task, recorded under turn 1 (idle's decision turn).
      makeEvent({ msg: "agent.call.start",    turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:01Z", attrs: { call_id: "c-task", verb: "task" } }),
      makeEvent({ msg: "agent.call.complete", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:02Z", attrs: { call_id: "c-task", duration_ms: 1000, verb: "task" } }),
      // Decision: reproducing's own intent fires in turn 2.
      makeEvent({ msg: "machine.intent_accepted", turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:03Z", attrs: { intent: "accept" } }),
      makeEvent({ msg: "harness.called",   turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:04Z", attrs: { namespace: "host.append_to_file.post" } }),
      makeEvent({ msg: "harness.dispatched", turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:04Z", attrs: { namespace: "host.append_to_file.post" } }),
      makeEvent({ msg: "harness.returned",   turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:04Z", attrs: { namespace: "host.append_to_file.post" } }),
    ];
  }

  it("merges a room's entry turn and decision turn into ONE visit group", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: reproducingVisit(), selectedEventIndex: null },
    });
    await flushPromises();

    // Old behaviour grouped by (state_path, turn) → 2 headers (turn 1, turn 2).
    // Visit grouping collapses them into a single reproducing visit.
    const headers = wrapper.findAll(".trace-timeline__turn-header");
    expect(headers.length).toBe(1);

    wrapper.unmount();
  });

  it("labels the visit with its accepted intent and the spanned turn range", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: reproducingVisit(), selectedEventIndex: null },
    });
    await flushPromises();

    const header = wrapper.find(".trace-timeline__turn-header");
    // Intent badge carries the single decider break for the visit.
    expect(header.find(".trace-timeline__intent-value").text()).toBe("accept");
    // The raw turn span stays visible (faithful to the trace): turn 1–2.
    expect(header.find(".trace-timeline__turn-label").text()).toBe("turn 1–2");

    wrapper.unmount();
  });

  it("falls back to per-turn groups when no intent was accepted", async () => {
    // Without a machine.intent_accepted to anchor a visit, we cannot identify
    // the decision, so the faithful default is to group by raw turn.
    const events: TraceEvent[] = [
      makeEvent({ msg: "host.invoked",  turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:01Z" }),
      makeEvent({ msg: "host.invoked",  turn: 2, state_path: "reproducing", time: "2026-01-01T00:00:02Z" }),
    ];
    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null },
    });
    await flushPromises();

    const headers = wrapper.findAll(".trace-timeline__turn-header");
    expect(headers.length).toBe(2);
    const labels = headers.map((h) => h.find(".trace-timeline__turn-label").text());
    expect(labels).toEqual(["turn 1", "turn 2"]);

    wrapper.unmount();
  });
});

describe("TraceTimeline — machine.say narration", () => {
  it("renders machine.say as a narration row showing its text", async () => {
    const events: TraceEvent[] = [
      makeEvent({
        msg: "machine.say",
        turn: 1,
        state_path: "proposing",
        attrs: { text: "Fix applied — TKT-001 done." },
      }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null },
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(1);
    // The text must be shown (not a bare "machine.say" or "world.update").
    const sayText = wrapper.find(".trace-timeline__say-text");
    expect(sayText.exists()).toBe(true);
    expect(sayText.text()).toContain("Fix applied — TKT-001 done.");
    // The subsystem chip is "machine".
    expect(rows[0]!.find(".trace-timeline__subsystem-chip").attributes("data-subsystem")).toBe("machine");

    wrapper.unmount();
  });
});
