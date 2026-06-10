/**
 * Story-editor wire types — mirror internal/app/graph/*.go and the
 * runstatus.editor.* RPC family (internal/runstatus/server/editor.go).
 *
 * These are PER-STORY reads (keyed by `story_path`, the absolute app.yaml
 * path), independent of any live session: the editor inspects a story's room
 * graph statically.
 */
import type { View, ViewElement } from "../types.js";

/** One BFS-ordered entry in the room list (graph.RoomSummary). */
export interface RoomSummary {
  id: string;
  label: string;
  distance: number;
  has_oracle: boolean;
}

/** A flattened on_enter effect card (graph.EffectSpec). */
export interface EffectSpec {
  kind: string;
  invoke?: string;
  id?: string;
  when?: string;
  bind?: string[];
  sets?: string[];
}

/** One world variable a room references (graph.WorldKey). */
export interface WorldKey {
  name: string;
  type: string;
  direction: "read" | "write" | "readwrite";
}

/** One intent available in a room (graph.IntentSpec). */
export interface IntentSpec {
  name: string;
  title?: string;
  description?: string;
}

/** One intent→target edge (graph.TransitionSpec). */
export interface TransitionSpec {
  intent: string;
  target: string;
  when?: string;
}

/** IDE deep-link pointer (graph.SourceRef). */
export interface SourceRef {
  path: string;
  line: number;
}

/** Full per-room detail (graph.RoomDetail). */
export interface RoomDetail {
  id: string;
  label: string;
  distance: number;
  on_enter: EffectSpec[];
  world_keys: WorldKey[];
  intents: IntentSpec[];
  transitions: TransitionSpec[];
  /** Raw typed-view elements; rendered read-only via ViewElement.vue. */
  view: ViewElement[];
  source_ref?: SourceRef;
}

/** The cassette-match tuple for an oracle call (graph.CassetteKey). */
export interface CassetteKey {
  handler: string;
  phase: string;
  schema_name?: string;
  call?: string;
}

/** One host.oracle.* contract a room makes (graph.OracleContract). */
export interface OracleContract {
  kind: string;
  prompt_path?: string;
  output_schema?: string;
  cassette_key: CassetteKey;
  effect_index: number;
}

/** Response of runstatus.editor.oracles. */
export interface OraclesResult {
  contracts: OracleContract[];
  cassette_globs: string[];
}

/** One cassette episode the editor lists (server.CassetteEpisodeSummary). */
export interface CassetteEpisodeSummary {
  cassette_file: string;
  episode_id: string;
  handler?: string;
  phase?: string;
  schema_name?: string;
  input_digest: string;
  output_preview: string;
}

/** Response of runstatus.editor.replay. */
export interface ReplayResult {
  output: unknown;
  world_snapshot: Record<string, unknown>;
  source: string;
  cassette_file?: string;
  episode_id?: string;
  note?: string;
}

/** Re-export View so editor components can reference it from one place. */
export type { View, ViewElement };
