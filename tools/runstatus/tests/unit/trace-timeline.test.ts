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
// Visible by default: host.invoked, host.returned (group 1), oracle.decide.start (group 2).
const EVENTS: TraceEvent[] = [
  makeEvent({ msg: "turn.start",          turn: 1, state_path: "root.active" }),
  makeEvent({ msg: "host.invoked",        turn: 1, state_path: "root.active", time: "2026-01-01T00:00:02Z" }),
  makeEvent({ msg: "host.returned",       turn: 1, state_path: "root.active", time: "2026-01-01T00:00:03Z" }),
  makeEvent({ msg: "oracle.decide.start", turn: 2, state_path: "root.done",   time: "2026-01-01T00:00:04Z", attrs: { call_id: "c1", verb: "decide" } }),
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
    // Group 2: [oracle.decide.start] → 1 event
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

    // 3 visible rows (host, host, oracle) — turn/machine rows are suppressed.
    const chips = wrapper.findAll(".trace-timeline__subsystem-chip");
    expect(chips.length).toBe(3);

    const sysList = chips.map((c) => c.attributes("data-subsystem"));
    expect(sysList).toContain("host");
    expect(sysList).toContain("oracle");

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

    // 2 host events hidden; 1 remains (oracle.decide.start).
    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(1);

    wrapper.unmount();
  });

  it("filters by level when a level chip is activated", async () => {
    const eventsWithLevels: TraceEvent[] = [
      makeEvent({ msg: "host.invoked",  turn: 1, level: "info" }),
      makeEvent({ msg: "host.invoked",  turn: 1, level: "warn", time: "2026-01-01T00:00:02Z" }),
      makeEvent({ msg: "host.returned", turn: 1, level: "error", time: "2026-01-01T00:00:03Z" }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events: eventsWithLevels, selectedEventIndex: null },
      attachTo: document.body,
    });
    await flushPromises();

    // All 3 are "host" subsystem — visible by default.
    expect(wrapper.findAll(".trace-timeline__row").length).toBe(3);

    // Click the "warn" level chip to activate it.
    const levelChips = wrapper.findAll(".trace-timeline__chip");
    const warnChip = levelChips.find((c) => c.text() === "warn");
    expect(warnChip).toBeDefined();
    await warnChip!.trigger("click");

    // Only the warn event should be visible.
    expect(wrapper.findAll(".trace-timeline__row").length).toBe(1);

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

    // Select "root.done" — oracle.decide.start matches (turn.end is "turn" subsystem, hidden).
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

describe("TraceTimeline — oracle start/complete merge", () => {
  function oracleEvents(): TraceEvent[] {
    return [
      makeEvent({
        msg: "oracle.task.start",
        turn: 1,
        time: "2026-01-01T00:00:01Z",
        attrs: { call_id: "call-paired", verb: "task", agent: "reproducer" },
      }),
      makeEvent({
        msg: "oracle.task.complete",
        turn: 1,
        time: "2026-01-01T00:00:03Z",
        attrs: { call_id: "call-paired", duration_ms: 2000, verb: "task" },
      }),
      makeEvent({
        msg: "oracle.ask.start",
        turn: 1,
        time: "2026-01-01T00:00:04Z",
        attrs: { call_id: "call-orphan", verb: "ask" },
      }),
    ];
  }

  it("collapses a paired start/complete into one row carrying the elapsed time", async () => {
    const wrapper = mount(TraceTimeline, {
      props: { events: oracleEvents(), selectedEventIndex: null },
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
      props: { events: oracleEvents(), selectedEventIndex: null },
    });
    await flushPromises();

    const incomplete = wrapper.findAll(".trace-timeline__incomplete");
    expect(incomplete.length).toBe(1);
    expect(incomplete[0]!.text()).toBe("incomplete");

    wrapper.unmount();
  });
});

describe("TraceTimeline — turn.input ordering", () => {
  // Reproduces the 'reproducing' phase: oracle in turn 1, user accepts triggering
  // turn 2 (host + world), then oracle again. turn.input ('[intent] accept') is
  // synthesised at turn=1 (N-1) by fromhistory but must render LAST within the
  // reproducing phase — after the host/world work it triggered in turn 2.
  function reproducingEvents(): TraceEvent[] {
    return [
      makeEvent({ msg: "oracle.task.start",  turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:01Z", attrs: { call_id: "c-task", verb: "task" } }),
      makeEvent({ msg: "oracle.task.complete", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:02Z", attrs: { call_id: "c-task", duration_ms: 1000, verb: "task" } }),
      makeEvent({ msg: "oracle.decide.start", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:03Z", attrs: { call_id: "c-decide", verb: "decide" } }),
      makeEvent({ msg: "oracle.decide.complete", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:04Z", attrs: { call_id: "c-decide", duration_ms: 500, verb: "decide" } }),
      // turn.input synthesised at turn=1 (N-1) by fromhistory — should display LAST.
      makeEvent({ msg: "turn.input", turn: 1, state_path: "reproducing", time: "2026-01-01T00:00:05Z", attrs: { input: "[intent] accept" } }),
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
    // Visible: oracle.task (merged), oracle.decide (merged), host.append, world.update, turn.input.
    // oracle.complete rows are suppressed; harness.dispatched/returned absorbed.
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
      makeEvent({ msg: "oracle.ask.start",   turn: 1, state_path: "root.active", attrs: { call_id: "cx", verb: "ask" } }),
      makeEvent({ msg: "oracle.ask.complete", turn: 1, state_path: "root.active", time: "2026-01-01T00:00:02Z", attrs: { call_id: "cx", duration_ms: 100, verb: "ask" } }),
      makeEvent({ msg: "turn.input", turn: 1, state_path: "root.active", time: "2026-01-01T00:00:03Z", attrs: { input: "[intent] done" } }),
    ];

    const wrapper = mount(TraceTimeline, {
      props: { events, selectedEventIndex: null },
    });
    await flushPromises();

    const rows = wrapper.findAll(".trace-timeline__row");
    expect(rows.length).toBe(2); // oracle.ask (merged) + turn.input

    const msgs = rows.map((r) => r.find(".trace-timeline__msg").text());
    const inputIdx = msgs.findIndex((m) => m.includes("[intent] done"));
    expect(inputIdx).toBe(1); // last row in the only group

    wrapper.unmount();
  });
});
