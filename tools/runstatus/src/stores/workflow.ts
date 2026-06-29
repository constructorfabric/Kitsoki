import { defineStore } from "pinia";
import { ref } from "vue";
import type { LiveSource } from "../data/live-source.js";
import type {
  WorkflowExportOptions,
  WorkflowReceipt,
} from "../data/source.js";

export type WorkflowPhase =
  | "idle"
  | "creating"
  | "validating"
  | "launching"
  | "exporting"
  | "error";

type WorkflowSource = Pick<
  LiveSource,
  "workflowCreate" | "workflowValidate" | "workflowLaunch" | "workflowStatus" | "workflowExport"
>;

export const useWorkflowStore = defineStore("workflow", () => {
  const open = ref(false);
  const phase = ref<WorkflowPhase>("idle");
  const error = ref("");
  const receipt = ref<WorkflowReceipt | null>(null);
  const goal = ref("");
  const slug = ref("");
  const target = ref("");
  const allowBaseStory = ref(false);

  function resetError(): void {
    error.value = "";
    if (phase.value === "error") phase.value = "idle";
  }

  function openPanel(): void {
    open.value = true;
    resetError();
  }

  function close(): void {
    open.value = false;
  }

  function setGoal(v: string): void {
    goal.value = v;
  }

  function setSlug(v: string): void {
    slug.value = v;
  }

  function setTarget(v: string): void {
    target.value = v;
  }

  function setAllowBaseStory(v: boolean): void {
    allowBaseStory.value = v;
  }

  function applyReceipt(next: WorkflowReceipt): void {
    receipt.value = next;
    target.value = next.export_path || defaultExportTarget(next.slug);
  }

  function defaultExportTarget(slugValue: string): string {
    return slugValue ? `stories/${slugValue}` : "";
  }

  async function create(source: WorkflowSource): Promise<WorkflowReceipt | null> {
    const trimmed = goal.value.trim();
    if (!trimmed) {
      phase.value = "error";
      error.value = "goal is required";
      return null;
    }
    phase.value = "creating";
    resetError();
    try {
      const next = await source.workflowCreate(trimmed, slug.value.trim());
      applyReceipt(next);
      phase.value = "idle";
      return next;
    } catch (e) {
      phase.value = "error";
      error.value = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  async function validate(
    source: WorkflowSource
  ): Promise<WorkflowReceipt | null> {
    const id = receipt.value?.workflow_id?.trim();
    if (!id) {
      phase.value = "error";
      error.value = "create a workflow first";
      return null;
    }
    phase.value = "validating";
    resetError();
    try {
      const next = await source.workflowValidate(id);
      applyReceipt(next);
      phase.value = "idle";
      return next;
    } catch (e) {
      phase.value = "error";
      error.value = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  async function launch(
    source: WorkflowSource
  ): Promise<WorkflowReceipt | null> {
    const id = receipt.value?.workflow_id?.trim();
    if (!id) {
      phase.value = "error";
      error.value = "create a workflow first";
      return null;
    }
    phase.value = "launching";
    resetError();
    try {
      const next = await source.workflowLaunch(id);
      applyReceipt(next);
      phase.value = "idle";
      return next;
    } catch (e) {
      phase.value = "error";
      error.value = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  async function status(
    source: WorkflowSource
  ): Promise<WorkflowReceipt | null> {
    const id = receipt.value?.workflow_id?.trim();
    if (!id) {
      phase.value = "error";
      error.value = "create a workflow first";
      return null;
    }
    phase.value = "idle";
    resetError();
    try {
      const next = await source.workflowStatus(id);
      applyReceipt(next);
      return next;
    } catch (e) {
      phase.value = "error";
      error.value = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  async function exportDraft(
    source: WorkflowSource
  ): Promise<WorkflowReceipt | null> {
    const id = receipt.value?.workflow_id?.trim();
    if (!id) {
      phase.value = "error";
      error.value = "create a workflow first";
      return null;
    }
    phase.value = "exporting";
    resetError();
    try {
      const opts: WorkflowExportOptions = {
        target: target.value.trim() || undefined,
        allow_base_story: allowBaseStory.value,
      };
      const next = await source.workflowExport(id, opts);
      applyReceipt(next);
      phase.value = "idle";
      return next;
    } catch (e) {
      phase.value = "error";
      error.value = e instanceof Error ? e.message : String(e);
      return null;
    }
  }

  return {
    open,
    phase,
    error,
    receipt,
    goal,
    slug,
    target,
    allowBaseStory,
    openPanel,
    close,
    setGoal,
    setSlug,
    setTarget,
    setAllowBaseStory,
    create,
    validate,
    launch,
    status,
    exportDraft,
  };
});
