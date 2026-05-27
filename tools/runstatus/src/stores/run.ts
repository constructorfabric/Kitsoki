import { defineStore } from "pinia";
import { ref, computed } from "vue";
import type { AppDef, MermaidSnapshot, TraceEvent, NodeRef } from "../types.js";
import type { DataSource } from "../data/source.js";

export const useRunStore = defineStore("run", () => {
  // ---- state ----
  const appDef = ref<AppDef | null>(null);
  const mermaid = ref<MermaidSnapshot | null>(null);
  const events = ref<TraceEvent[]>([]);
  const currentStatePath = ref<string>("");
  const selectedNode = ref<NodeRef | null>(null);
  const selectedEventIndex = ref<number | null>(null);
  const terminal = ref<boolean>(false);
  const loading = ref<boolean>(false);
  // Set of state_path values that should be highlighted in the timeline.
  // Driven by clicks on diagram rooms/phases.  Empty = no highlight.
  const highlightedStatePaths = ref<string[]>([]);
  // Bumped each time the highlight set changes; TraceTimeline watches it to
  // scroll the first matching row into view (so re-clicking the same room
  // scrolls again).
  const highlightTick = ref<number>(0);

  // ---- internal ----
  let _unsubscribe: (() => void) | null = null;

  // ---- actions ----

  /**
   * Hydrate from a DataSource: load session + app + mermaid + initial trace,
   * then subscribe to keep events/currentStatePath updated.
   */
  async function hydrate(source: DataSource, sessionId: string): Promise<void> {
    loading.value = true;
    try {
      const [session, app, mer, traceResult] = await Promise.all([
        source.getSession(sessionId),
        source.getApp(sessionId),
        source.getMermaid(sessionId),
        source.getTrace(sessionId),
      ]);

      appDef.value = app;
      mermaid.value = mer;
      currentStatePath.value = session.current_state;
      terminal.value = session.terminal;
      events.value = traceResult.events.slice();
    } finally {
      loading.value = false;
    }

    // Subscribe for live updates.
    _unsubscribe = source.subscribe(sessionId, (e: TraceEvent) => {
      events.value.push(e);
      if (e.state_path) {
        currentStatePath.value = e.state_path;
      }
    });
  }

  /** Stop the live subscription. */
  function teardown(): void {
    _unsubscribe?.();
    _unsubscribe = null;
  }

  /**
   * Look up nodeId in mermaid.node_map and set selectedNode.
   * Sets selectedNode to null if nodeId is not found.
   */
  function selectNode(nodeId: string): void {
    const map = mermaid.value?.node_map;
    if (map === undefined) {
      selectedNode.value = null;
      return;
    }
    const ref = map[nodeId];
    selectedNode.value = ref ?? null;
  }

  /** The currently selected event object (null when none or index out of range). */
  const selectedEvent = computed<TraceEvent | null>(() => {
    const i = selectedEventIndex.value;
    if (i === null || i < 0 || i >= events.value.length) return null;
    return events.value[i] ?? null;
  });

  /** Set the selected event by index. */
  function selectEvent(index: number): void {
    selectedEventIndex.value = index;
  }

  /** Clear both the selected node and selected event. */
  function clearSelection(): void {
    selectedNode.value = null;
    selectedEventIndex.value = null;
  }

  /** Set the highlighted state paths (driven by diagram clicks). */
  function setHighlightedStatePaths(paths: string[]): void {
    highlightedStatePaths.value = paths.slice();
    highlightTick.value += 1;
  }

  return {
    // state
    appDef,
    mermaid,
    events,
    currentStatePath,
    selectedNode,
    selectedEventIndex,
    selectedEvent,
    terminal,
    loading,
    highlightedStatePaths,
    highlightTick,
    // actions
    hydrate,
    teardown,
    selectNode,
    selectEvent,
    clearSelection,
    setHighlightedStatePaths,
  };
});
