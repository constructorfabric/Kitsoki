/**
 * Tour format v2 types. Hand-mirror of tools/runstatus/src/tour/types-v2.ts
 * / internal/tour/manifest_v2.go / schemas/tour-v2.schema.json — the same
 * three-way lockstep discipline the rest of the format uses, now extended
 * to a fourth mirror here rather than a cross-package source import, so
 * tools/tour-player stays independently publishable with zero sibling-tool
 * coupling (the brief's "framework-free ... published as a single IIFE/ESM
 * artifact" requirement).
 */

export type StepKind = "highlight" | "gate" | "act" | "navigate";
export type ActPolicy = "watch" | "confirm" | "auto";
export type Interaction = "block" | "allow";
export type AdvanceEvent = "click" | "input" | "route" | "submit";
export type ActKind = "click" | "fill" | "scroll" | "press";
export type PopoverSide = "top" | "bottom" | "left" | "right" | "center";
export type PopoverAlign = "start" | "center" | "end";

export interface TargetWaitFor {
  timeoutMs?: number;
}

export interface TargetBundle {
  role?: string;
  name?: string;
  testid?: string;
  text?: string;
  css?: string;
  ancestor?: string;
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
  route?: string;
  target?: TargetBundle;
  popover?: PopoverV2;
  kind: StepKind;
  advanceOn?: AdvanceOn;
  act?: ActSpec;
  policy?: ActPolicy;
  interaction?: Interaction;
  viewport?: string;
  data?: Record<string, unknown>;
}

export interface TourManifestV2 {
  version: 2;
  id: string;
  origin?: string;
  steps: TourStepV2[];
}

export interface HealEvent {
  stepId: string;
  failedAnchor: string;
  matchedAnchor: string;
  confidence: number;
}

export function effectivePolicy(step: TourStepV2): ActPolicy | undefined {
  if (step.kind === "act" && !step.policy) return "confirm";
  return step.policy;
}
