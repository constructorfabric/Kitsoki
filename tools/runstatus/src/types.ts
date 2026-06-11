export interface NodeRef {
  kind: "state" | "effect" | "transition" | "world";
  ref: string;
}

export interface SessionHeader {
  session_id: string;
  app_id: string;
  current_state: string;
  turn: number;
  started_at: string; // ISO 8601
  terminal: boolean;
}

export interface MermaidSnapshot {
  source: string;
  node_map: Record<string, NodeRef>;
}

export interface TraceEvent {
  time: string;
  level: string;
  msg: string;
  session_id: string;
  turn: number;
  state_path: string;
  /** Non-zero for off-path event batches: the foreground turn that was active
   *  when the off-path interaction occurred. The trace UI uses this to render
   *  off-path groups as nested sub-items rather than independent sibling turns. */
  parent_turn?: number;
  attrs: Record<string, unknown>;
}

// Keep AppDef loose; components destructure what they need.
export interface AppDef {
  id: string;
  name?: string;
  root: string;
  states: Record<string, unknown>;
  [key: string]: unknown;
}

/**
 * AnnotationEntry is one operator score/label from the annotation sidecar
 * JSONL. It mirrors runstatus.Annotation on the Go side.
 */
export interface AnnotationEntry {
  ts: string; // ISO 8601
  session_id: string;
  target_call_id?: string;
  target_turn?: number;
  score?: number;
  label?: string;
  comment?: string;
  annotator?: string;
  schema_version: number;
}

/**
 * ReplayResult is the response from runstatus.call.replay. In v1 the dispatch
 * is not actually wired (no LLM cost), so replayable:true + note is the
 * expected response shape. new_verdict and diff are reserved for when real
 * dispatch lands.
 */
export interface ReplayResult {
  call_id: string;
  original_verb: string;
  replayable: boolean;
  note?: string;
  /** Reserved: the new verdict from the re-dispatched call (not yet populated in v1). */
  new_verdict?: unknown;
  /** Reserved: diff between original and new verdict (not yet populated in v1). */
  diff?: unknown;
}

export interface Snapshot {
  session: SessionHeader;
  app: AppDef;
  mermaid: MermaidSnapshot;
  events: TraceEvent[];
  /** Operator annotations from the sidecar JSONL. Absent when no sidecar exists. */
  annotations?: AnnotationEntry[];
}

// ── Write/read RPC: typed view + turn result ────────────────────────────────
//
// These mirror the Go wire shapes returned by runstatus.session.view / .submit
// / .turn / .continue. IMPORTANT: app.View marshals with default Go JSON, so
// the JSON keys are PascalCase (Kind, Source, Elements, …) — NOT a
// `{prose: "..."}` kind-keyed object. Each element is a flat struct
// discriminated by its `Kind` field; the kind-specific body rides on the
// shared fields (Source for prose/heading/code/template/banner, Items for
// list, Pairs for kv, Choice* for choice). Confirmed by curling
// runstatus.session.view against a live `kw web … --flow …` server.

/** One ordered {Key, Value} entry of a "kv" element's Pairs (Go MapSlice). */
export interface KVPair {
  Key: string;
  Value: string;
}

/** One entry of a "list" element's Items. */
export interface ListItem {
  Label: string;
  Hint?: string;
  When?: string;
}

/**
 * One field in a form-mode choice element. Mirrors Go's app.ChoiceField
 * (PascalCase on the wire).
 */
export interface ChoiceField {
  Name: string;
  Type?: string; // "string" | "int" | "float" | "bool" | "enum"
  Hint?: string;
  Placeholder?: string;
  Unit?: string;
  Values?: string[];
  Default?: unknown;
  Min?: unknown;
  Max?: unknown;
  Required?: boolean;
  Readonly?: boolean;
}

/**
 * One item in a single-mode choice element. Slots are pre-filled values to send
 * when the item is selected. Param, when present, captures one extra free-text
 * slot from the user before firing.
 */
export interface ChoiceItem {
  Label: string;
  Hint?: string;
  Intent: string;
  Slots?: Record<string, unknown> | null;
  Param?: {
    Slot: string;
    Type: string;
    Placeholder?: string;
    Required?: boolean;
    Values?: string[];
  } | null;
}

/**
 * ViewElement is one typed entry of a View's Elements slice, discriminated by
 * `Kind`. The kinds prose / heading / code / template / banner carry their body
 * in `Source`; list carries `Items`; kv carries `Pairs`; choice carries the
 * `Choice*` fields; media carries `Handle`, `Mime`, and `Label`. All fields
 * beyond `Kind` are optional because Go marshals the full struct with zero
 * values for the kinds that don't use them (e.g. a prose element still emits
 * `Items: null`, `Pairs: null`, …). `When` is the optional element-level guard
 * expression.
 */
export interface ViewElement {
  Kind:
    | "prose"
    | "heading"
    | "code"
    | "template"
    | "list"
    | "kv"
    | "banner"
    | "choice"
    | "media";
  Source?: string;
  // ── Media fields (populated only when Kind === "media"). ──
  /** Artifact handle/ref — resolved to a URL via the DataSource. */
  Handle?: string;
  /** MIME type, e.g. "video/mp4", "image/png". */
  Mime?: string;
  /** Optional human-readable display caption or alt text. */
  Caption?: string;
  // The Go engine (internal/app.ViewElement) marshals media fields with a
  // `Media`-prefix and selects the renderer family by `MediaKind`
  // (video/image/pdf/html), not a MIME string. The component normalises these
  // to Handle/Caption/Mime below, so both wire shapes render identically.
  /** Engine wire field for the artifact handle (pre-interpolated). */
  MediaHandle?: string;
  /** Engine wire field for the caption. */
  MediaCaption?: string;
  /** Engine wire field for the artifact kind (video/image/pdf/html/slideshow). */
  MediaKind?: string;
  Items?: ListItem[] | null;
  Pairs?: KVPair[] | null;
  Marker?: string;
  Subtitle?: string;
  Color?: string;
  When?: string;
  // ── Choice fields (populated only when Kind === "choice"). ──
  ChoiceMode?: string;
  ChoicePrompt?: string;
  ChoiceItems?: ChoiceItem[] | null;
  ChoiceIntent?: string;
  ChoiceSlot?: string;
  ChoiceMin?: number;
  ChoiceMax?: number;
  ChoiceMinSet?: boolean;
  ChoiceMaxSet?: boolean;
  ChoiceTemplate?: string;
  ChoiceFields?: ChoiceField[] | null;
  ChoiceRaw?: unknown;
}

/**
 * View is the resolved typed view payload. `Source` is the legacy raw template
 * text (empty for the element-array form); `Elements` is the normalised element
 * list the browser lays out itself.
 */
export interface View {
  Source?: string;
  Elements?: ViewElement[];
  Extends?: string;
  Blocks?: Record<string, ViewElement[]> | null;
  TemplateFile?: string;
}

/**
 * IntentInfo is one entry of TurnResult.intents — per-intent menu metadata the
 * UI uses to label a button and bind a free-text input box.
 */
export interface IntentInfo {
  /** Intent name to submit (matches an `allowed_intents` entry). */
  name: string;
  /** Author-declared intent title (may be absent). */
  title?: string;
  /** Author-declared intent description — shown as a tooltip or subtitle. */
  description?: string;
  /**
   * Name of the single free-text/string slot the UI binds its input box to.
   * Present iff the intent has exactly one string-typed slot and no required
   * non-string slot; absent for no-slot intents and multi-field forms.
   */
  text_slot?: string;
  /** True when the intent declares any slots at all. */
  has_slots: boolean;
}

/**
 * One slot the engine needs filled before it can proceed (mode === "clarify").
 * Go SlotNeed marshals PascalCase.
 */
export interface SlotNeed {
  Name: string;
  Prompt?: string;
  Description?: string;
  Type?: string;
  Values?: string[];
  FormatHint?: string;
  Examples?: string[];
}

/** Interpreted outcome mode of a turn. */
export type TurnMode =
  | "transitioned"
  | "clarify"
  | "rejected"
  | "completed"
  | "offpath"
  | "cancelled";

/**
 * TurnResult is the shared wire shape returned by runstatus.session.view /
 * .submit / .turn / .continue. A guard rejection or missing slot is NOT a
 * transport error — it rides back as mode "rejected" / "clarify". Only infra
 * failures surface as a JSON-RPC error.
 */
export interface TurnResult {
  mode: TurnMode | string;
  state: string;
  view?: string;
  typed_view?: View;
  allowed_intents?: string[];
  intents?: IntentInfo[];
  slots_needed?: SlotNeed[];
  pending_intent?: string;
  pending_slots?: Record<string, unknown>;
  error_code?: string;
  error_message?: string;
  guard_hint?: string;
  harness_error?: string;
  turn_number: number;
}
