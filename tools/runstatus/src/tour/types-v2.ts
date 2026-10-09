/**
 * Tour format v2 — app-agnostic, portable click-through tour steps.
 *
 * Mirror of internal/tour/manifest_v2.go and schemas/tour-v2.schema.json.
 * Keep all three in lockstep: a field added here needs the matching Go
 * struct field + json tag and the matching schema property, or
 * TestTourManifestV2SchemaFieldParity (internal/tour/manifest_v2_test.go)
 * fails.
 *
 * v1 (./types.ts) stays the format `internal/tour` renders today; v2 is the
 * migration target for the browser MCP / player work (P2-P5). A
 * `ConvertToV2` (Go) converter maps v1 tours onto v2 losslessly, riding any
 * v1-only concept with no first-class v2 slot through `data`.
 */

/** Ranked resolution order: role+name -> testid -> label/text -> fuzzy css -> ancestor fallback. */
export type StepKind = "highlight" | "gate" | "act" | "navigate";

/** Consent tier for `act` steps. Defaults to "confirm" when omitted. */
export type ActPolicy = "watch" | "confirm" | "auto";

/** Whether the live page beneath the overlay may be clicked during a step. */
export type Interaction = "block" | "allow";

/** Real user event a `gate` step reacts to. */
export type AdvanceEvent = "click" | "input" | "route" | "submit";

/** Deterministic action kind an `act` step performs against its resolved target. */
export type ActKind = "click" | "fill" | "scroll" | "press";

export type PopoverSide = "top" | "bottom" | "left" | "right" | "center";
export type PopoverAlign = "start" | "center" | "end";

export interface TargetWaitFor {
  timeoutMs?: number;
}

/**
 * A ranked, multi-anchor bundle for one step's element. Never a single
 * selector: resolution walks role+name, then testid, then label/text, then a
 * fuzzy css fragment, then an ancestor-scoped fallback. When the primary
 * anchor misses and a secondary anchor uniquely resolves, the resolver emits
 * a [HealEvent] — never a silent rebind.
 */
export interface TargetBundle {
  role?: string;
  name?: string;
  testid?: string;
  text?: string;
  /** Fuzzy, stable CSS fragment only (e.g. `[data-qa*='save']`) — not a deep structural path. */
  css?: string;
  ancestor?: string;
  /** Ordered iframe selector path from the document root; empty for the top-level document. */
  frame?: string[];
  waitFor?: TargetWaitFor;
}

export interface PopoverV2 {
  title?: string;
  body?: string;
  side?: PopoverSide;
  align?: PopoverAlign;
}

export interface AdvanceOn {
  event: AdvanceEvent;
}

export interface ActSpec {
  kind: ActKind;
  value?: string;
}

export interface TourStepV2 {
  id: string;
  /** Route this step lives on; the player navigates or waits when it differs from the current route. */
  route?: string;
  target?: TargetBundle;
  popover?: PopoverV2;
  kind: StepKind;
  /** Required when kind === "gate". */
  advanceOn?: AdvanceOn;
  /** Required when kind === "act". */
  act?: ActSpec;
  policy?: ActPolicy;
  interaction?: Interaction;
  /** Scroll-container id/testid this step scrolls within, when not the window. */
  viewport?: string;
  /** Opaque per-step payload; used by the v1 converter to carry fields with no first-class v2 slot. */
  data?: Record<string, unknown>;
}

export interface TourManifestV2 {
  version: 2;
  id: string;
  /** Exact-match origin binding (e.g. `https://app.example.com`); omitted for origin-agnostic fixtures. */
  origin?: string;
  steps: TourStepV2[];
}

/**
 * Emitted by anchor resolution (the player, or `tour_replay`) whenever a
 * step's primary anchor misses and a secondary anchor in its bundle uniquely
 * resolves. Always audited — a heal is never silent.
 */
export interface HealEvent {
  stepId: string;
  failedAnchor: string;
  matchedAnchor: string;
  confidence: number;
}

/** Resolves `step.policy`, defaulting `act` steps to `"confirm"` when unset. */
export function effectivePolicy(step: TourStepV2): ActPolicy | undefined {
  if (step.kind === "act" && !step.policy) return "confirm";
  return step.policy;
}
