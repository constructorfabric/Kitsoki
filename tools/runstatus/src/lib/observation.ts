/**
 * observation.ts — semantic taxonomy over EventKind strings.
 *
 * Mirrors internal/store/observation.go exactly. The mapping is a pure total
 * function: same input, same output. The Go test (observation_test.go) is the
 * canonical drift guard; the Vitest counterpart asserts equality against the
 * Go golden dump.
 *
 * No new event field is added. The category is derived at read time.
 */

export type ObservationKind =
  | "decision"
  | "agent-call"
  | "host-call"
  | "narration"
  | "world-mutation"
  | "routing"
  | "lifecycle";

/**
 * observationKind returns the semantic category for a given event msg string.
 * Falls back to "lifecycle" for any unrecognised string — never returns empty.
 */
export function observationKind(msg: string): ObservationKind {
  switch (msg) {
    // decision — interpretive choices with available/chosen/confidence
    case "machine.gate_decided":
    case "machine.write_mode_granted":
    case "agent.off_path.question":
    case "agent.off_path.answer":
      return "decision";

    // routing — what advanced the turn and how it was routed
    case "turn.start":
    case "machine.intent_accepted":
      return "routing";

    // agent-call — LLM/operator calls with prompt/response/cost/latency
    case "agent.call.start":
    case "agent.call.complete":
    case "agent.call.error":
    case "agent.tool_call":
      return "agent-call";

    // host-call — deterministic side-effecting execution
    case "harness.called":
    case "harness.dispatched":
    case "harness.returned":
    case "harness.error":
      return "host-call";

    // narration — operator-facing text
    case "machine.say":
    case "turn.end":
      return "narration";

    // world-mutation — set: world writes
    case "world.update":
      return "world-mutation";

    // lifecycle — structural/bookkeeping (and all unknown strings)
    default:
      return "lifecycle";
  }
}

/**
 * COLOR_MAP maps each ObservationKind to a canonical hex color.
 * Used by TraceTimeline category chips and per-row observation dots.
 */
export const COLOR_MAP: Record<ObservationKind, string> = {
  decision:       "#7c3aed", // purple
  "agent-call":  "#0ea5e9", // blue
  "host-call":    "#f59e0b", // amber
  narration:      "#10b981", // emerald
  "world-mutation": "#ec4899", // pink
  routing:        "#6366f1", // indigo
  lifecycle:      "#475569", // slate
};
