<template>
  <Teleport to="body">
    <div
      v-if="visible"
      class="workflow-modal__backdrop"
      data-testid="workflow-modal"
      @click.self="workflow.close()"
    >
      <div class="workflow-modal">
        <header class="workflow-modal__header">
          <span class="workflow-modal__title">Dynamic workflow</span>
          <button
            class="workflow-modal__close"
            data-testid="workflow-close"
            title="Close"
            @click="workflow.close()"
          >
            ✕
          </button>
        </header>

        <section class="workflow-modal__section">
          <label class="workflow-modal__label" for="workflow-goal">Goal</label>
          <textarea
            id="workflow-goal"
            v-model="workflow.goal"
            class="workflow-modal__input workflow-modal__input--goal"
            data-testid="workflow-goal"
            rows="4"
            placeholder="Describe the one-off workflow you want Kitsoki to decompose."
          ></textarea>

          <div class="workflow-modal__grid">
            <label class="workflow-modal__field">
              <span>Slug</span>
              <input
                v-model="workflow.slug"
                class="workflow-modal__input"
                data-testid="workflow-slug"
                type="text"
                placeholder="optional slug"
              />
            </label>
            <label class="workflow-modal__field">
              <span>Export target</span>
              <input
                v-model="workflow.target"
                class="workflow-modal__input"
                data-testid="workflow-target"
                type="text"
                placeholder="stories/<slug>"
              />
            </label>
          </div>

          <label class="workflow-modal__check">
            <input
              v-model="workflow.allowBaseStory"
              data-testid="workflow-allow-base"
              type="checkbox"
            />
            <span>Allow base-story export</span>
          </label>

          <div class="workflow-modal__actions">
            <button
              class="workflow-modal__action"
              data-testid="workflow-create"
              :disabled="busy"
              @click="create"
            >
              Create
            </button>
            <button
              class="workflow-modal__action"
              data-testid="workflow-validate"
              :disabled="busy || !hasReceipt"
              @click="validate"
            >
              Validate
            </button>
            <button
              class="workflow-modal__action"
              data-testid="workflow-launch"
              :disabled="busy || !hasReceipt"
              @click="launch"
            >
              Launch
            </button>
            <button
              class="workflow-modal__action"
              data-testid="workflow-export"
              :disabled="busy || !hasReceipt"
              @click="exportDraft"
            >
              Export
            </button>
            <button
              class="workflow-modal__action workflow-modal__action--ghost"
              data-testid="workflow-status"
              :disabled="busy || !hasReceipt"
              @click="refresh"
            >
              Status
            </button>
          </div>
        </section>

        <section v-if="workflow.receipt" class="workflow-modal__receipt" data-testid="workflow-receipt">
          <div class="workflow-modal__receipt-title">
            <span>workflow {{ workflow.receipt.workflow_id }}</span>
            <span v-if="workflow.receipt.url" class="workflow-modal__pill">URL ready</span>
            <span v-else-if="workflow.receipt.validation.ok" class="workflow-modal__pill">Validated</span>
          </div>
          <div class="workflow-modal__rows">
            <div data-testid="workflow-goal"><strong>goal:</strong> {{ workflow.receipt.goal }}</div>
            <div data-testid="workflow-draft"><strong>draft:</strong> {{ workflow.receipt.draft_dir }}</div>
            <div data-testid="workflow-manifest"><strong>manifest:</strong> {{ workflow.receipt.manifest_path }}</div>
            <div data-testid="workflow-story"><strong>story:</strong> {{ workflow.receipt.app_path }}</div>
            <div data-testid="workflow-validation"><strong>validation:</strong> {{ workflow.receipt.validation.ok ? "ok" : `${workflow.receipt.validation.errors.length} error(s)` }}</div>
            <div v-if="workflow.receipt.launch_command" data-testid="workflow-launch-command"><strong>launch:</strong> {{ workflow.receipt.launch_command }}</div>
            <div v-if="workflow.receipt.session_id" data-testid="workflow-session"><strong>session:</strong> {{ workflow.receipt.session_id }}</div>
            <div v-if="workflow.receipt.url" data-testid="workflow-url"><strong>url:</strong> {{ workflow.receipt.url }}</div>
            <div v-if="workflow.receipt.export_path" data-testid="workflow-export-path"><strong>export:</strong> {{ workflow.receipt.export_path }}</div>
            <div v-if="workflow.receipt.export_report_path" data-testid="workflow-export-report"><strong>export report:</strong> {{ workflow.receipt.export_report_path }}</div>
          </div>
        </section>

        <div v-if="workflow.error" class="workflow-modal__error" data-testid="workflow-error">
          {{ workflow.error }}
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch } from "vue";
import { useRouter } from "vue-router";
import { LiveSource } from "../../data/live-source.js";
import { useWorkflowStore } from "../../stores/workflow.js";

const workflow = useWorkflowStore();
const router = useRouter();
const source = new LiveSource("/");

const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;

const visible = computed(() => !isSnapshot && workflow.open);
const busy = computed(() =>
  ["creating", "validating", "launching", "exporting"].includes(workflow.phase)
);
const hasReceipt = computed(() => !!workflow.receipt);

watch(
  () => workflow.receipt?.slug,
  (slug) => {
    if (slug && !workflow.target.trim()) {
      workflow.target = `stories/${slug}`;
    }
  }
);

async function create(): Promise<void> {
  const next = await workflow.create(source);
  if (next) workflow.target = workflow.target.trim() || `stories/${next.slug}`;
}

async function validate(): Promise<void> {
  await workflow.validate(source);
}

async function launch(): Promise<void> {
  const next = await workflow.launch(source);
  if (!next) return;
  workflow.close();
  if (next.url) {
    await router.push(next.url);
  }
}

async function refresh(): Promise<void> {
  await workflow.status(source);
}

async function exportDraft(): Promise<void> {
  await workflow.exportDraft(source);
}
</script>

<style scoped>
.workflow-modal__backdrop {
  position: fixed;
  inset: 0;
  z-index: 1005;
  background: rgba(0, 0, 0, 0.62);
  display: flex;
  align-items: center;
  justify-content: center;
}

.workflow-modal {
  width: min(72rem, 92vw);
  max-height: 88vh;
  overflow: auto;
  background: var(--k-bg-widget, #0d1b2a);
  color: var(--k-fg, #e2e8f0);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 8px;
  box-shadow: 0 16px 50px rgba(0, 0, 0, 0.45);
}

.workflow-modal__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.7rem 0.85rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.workflow-modal__title {
  font-weight: 700;
}

.workflow-modal__close {
  width: 2rem;
  height: 2rem;
  border: 1px solid var(--k-border, #334155);
  border-radius: 6px;
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg, #e2e8f0);
}

.workflow-modal__section,
.workflow-modal__receipt {
  padding: 0.85rem;
}

.workflow-modal__label,
.workflow-modal__field span {
  display: block;
  margin-bottom: 0.35rem;
  font-size: 0.86rem;
  color: var(--k-fg-muted, #94a3b8);
}

.workflow-modal__input {
  width: 100%;
  border: 1px solid var(--k-border, #334155);
  border-radius: 6px;
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg, #e2e8f0);
  padding: 0.55rem 0.7rem;
}

.workflow-modal__input--goal {
  min-height: 7rem;
  resize: vertical;
}

.workflow-modal__grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.75rem;
  margin-top: 0.75rem;
}

.workflow-modal__field {
  min-width: 0;
}

.workflow-modal__check {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.75rem;
  font-size: 0.88rem;
}

.workflow-modal__actions {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
  margin-top: 0.9rem;
}

.workflow-modal__action {
  border: 1px solid var(--k-border, #334155);
  border-radius: 6px;
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg, #e2e8f0);
  padding: 0.5rem 0.8rem;
}

.workflow-modal__action--ghost {
  background: transparent;
}

.workflow-modal__action:disabled {
  opacity: 0.55;
}

.workflow-modal__receipt {
  border-top: 1px solid var(--k-border, #1e293b);
}

.workflow-modal__receipt-title {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.6rem;
  font-weight: 700;
}

.workflow-modal__pill {
  font-size: 0.72rem;
  padding: 0.15rem 0.45rem;
  border-radius: 999px;
  background: rgba(59, 130, 246, 0.15);
  color: var(--k-fg-accent, #93c5fd);
}

.workflow-modal__rows {
  display: grid;
  gap: 0.3rem;
  font-size: 0.9rem;
  line-height: 1.35;
  word-break: break-word;
}

.workflow-modal__error {
  border-top: 1px solid var(--k-border, #1e293b);
  padding: 0.7rem 0.85rem 0.85rem;
  color: var(--k-error, #fca5a5);
}

@media (max-width: 800px) {
  .workflow-modal__grid {
    grid-template-columns: 1fr;
  }
}
</style>
