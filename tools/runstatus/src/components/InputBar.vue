<template>
  <div class="input-bar" data-testid="input-bar">
    <!-- Choice items from typed view: labeled buttons with pre-filled slots.
         When present, these replace the legacy no-slot action buttons since
         choice items subsume them (back/look appear as choice items too). -->
    <template v-if="choiceItems.length">
      <div v-if="choicePrompt" class="input-bar__choice-prompt">{{ choicePrompt }}</div>
      <!-- Plain buttons first (no Param) — rendered in a consistent grid. -->
      <div v-if="buttonChoiceItems.length" class="input-bar__actions" data-testid="intent-actions">
        <button
          v-for="item in buttonChoiceItems"
          :key="item.Intent + '|' + JSON.stringify(item.Slots)"
          class="input-bar__action-btn"
          :class="{ 'input-bar__action-btn--primary': isPrimary(item) }"
          type="button"
          :disabled="pending"
          :data-testid="`intent-btn-${item.Intent}`"
          :data-slots="JSON.stringify(item.Slots ?? {})"
          :title="item.Hint || undefined"
          @click="fireChoiceItem(item)"
        >
          <span class="input-bar__btn-label">{{ item.Label }}</span>
          <span v-if="item.Hint" class="input-bar__btn-hint">{{ item.Hint }}</span>
        </button>
      </div>
      <!-- Param forms below (each needs a text input) — stacked vertically. -->
      <div v-if="formChoiceItems.length" class="input-bar__forms">
        <form
          v-for="item in formChoiceItems"
          :key="item.Intent + '|' + JSON.stringify(item.Slots)"
          class="input-bar__choice-param-form"
          :data-intent="item.Intent"
          @submit.prevent="fireChoiceParam(item, paramDrafts[item.Intent + '|' + JSON.stringify(item.Slots)] ?? '')"
        >
          <span class="input-bar__choice-param-label">{{ item.Label }}</span>
          <input
            v-model="paramDrafts[item.Intent + '|' + JSON.stringify(item.Slots)]"
            class="input-bar__input"
            type="text"
            :placeholder="item.Param!.Placeholder || item.Label"
            :disabled="pending"
          />
          <button
            class="input-bar__send"
            type="submit"
            :disabled="pending || !(paramDrafts[item.Intent + '|' + JSON.stringify(item.Slots)] ?? '').trim()"
          >
            Send
          </button>
        </form>
      </div>
    </template>

    <!-- Form mode: multi-field numeric/text form (mode: form in YAML). -->
    <template v-else-if="formElement">
      <div v-if="formPrompt" class="input-bar__choice-prompt">{{ formPrompt }}</div>
      <form class="input-bar__form-grid" @submit.prevent="submitForm">
        <div v-for="field in formFields" :key="field.Name" class="input-bar__form-row">
          <label class="input-bar__form-label">
            {{ field.Name }}
            <span v-if="field.Unit" class="input-bar__form-unit">{{ field.Unit }}</span>
          </label>
          <input
            v-model="formDrafts[field.Name]"
            class="input-bar__input input-bar__form-input"
            :type="field.Type === 'int' || field.Type === 'float' ? 'number' : 'text'"
            :placeholder="field.Placeholder ?? '0'"
            :min="field.Min != null ? String(field.Min) : undefined"
            :max="field.Max != null ? String(field.Max) : undefined"
            :required="field.Required ?? false"
            :disabled="pending || field.Readonly"
            step="1"
          />
        </div>
        <button
          class="input-bar__send input-bar__form-submit"
          type="submit"
          :disabled="pending"
        >
          Submit
        </button>
      </form>
    </template>

    <!-- Semantic routing room: no choice items, no form, no text-slot intents.
         Show a free-text textarea; submission goes via session.turn so the
         semantic router handles natural-language dispatch. The room view
         already documents the action vocabulary via list: elements. -->
    <template v-else-if="isSemanticRoom">
      <form
        class="input-bar__composer input-bar__composer--semantic"
        data-testid="composer"
        @submit.prevent="sendRaw"
      >
        <textarea
          v-model="rawDraft"
          class="input-bar__textarea"
          data-testid="composer-input"
          placeholder="Type anything — the router handles the rest…"
          rows="2"
          :disabled="pending"
          @keydown.enter.exact.prevent="sendRaw"
        />
        <button
          class="input-bar__send"
          type="submit"
          data-testid="composer-send"
          :disabled="pending || !rawDraft.trim()"
        >
          Send
        </button>
      </form>
    </template>

    <!-- Legacy path: no typed-view choice items — fall back to IntentInfo buttons. -->
    <template v-else>
      <div v-if="actionIntents.length" class="input-bar__actions" data-testid="intent-actions">
        <button
          v-for="intent in actionIntents"
          :key="intent.name"
          class="input-bar__action-btn input-bar__action-btn--primary"
          type="button"
          :disabled="pending"
          :data-testid="`intent-btn-${intent.name}`"
          :title="intent.description || undefined"
          @click="fireIntent(intent)"
        >
          <span class="input-bar__btn-label">{{ intent.title || intent.name }}</span>
          <span v-if="intent.description" class="input-bar__btn-hint">{{ intent.description }}</span>
        </button>
      </div>
    </template>

    <form
      v-if="textIntents.length && !choiceItems.length && !formElement"
      class="input-bar__composer"
      data-testid="composer"
      :data-active-intent="selectedTextName"
      @submit.prevent="send"
    >
      <select
        v-if="textIntents.length > 1"
        v-model="selectedTextName"
        class="input-bar__select"
        data-testid="composer-select"
        :disabled="pending"
      >
        <option v-for="intent in textIntents" :key="intent.name" :value="intent.name">
          {{ intent.title || intent.name }}
        </option>
      </select>

      <textarea
        v-model="draft"
        class="input-bar__textarea"
        data-testid="composer-input"
        :placeholder="placeholder"
        rows="2"
        :disabled="pending"
        @keydown.enter.exact.prevent="send"
      />

      <button
        class="input-bar__send"
        type="submit"
        data-testid="composer-send"
        :disabled="pending || !draft.trim()"
      >
        Send
      </button>
    </form>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import type { IntentInfo, View, ChoiceItem, ChoiceField } from "../types.js";

const props = defineProps<{
  intents: IntentInfo[];
  typedView?: View | null;
  pending?: boolean;
}>();

const emit = defineEmits<{
  (e: "send", text: string, intentName: string): void;
  (e: "intent", name: string, slots: Record<string, unknown>, displayLabel?: string): void;
}>();

// ── Choice items from typed view ──────────────────────────────────────────────

/** All choice items from the first single-mode choice element in the typed view. */
const choiceItems = computed<ChoiceItem[]>(() => {
  const elements = props.typedView?.Elements;
  if (!elements?.length) return [];
  for (const el of elements) {
    if (el.Kind === "choice" && el.ChoiceMode === "single" && el.ChoiceItems?.length) {
      return el.ChoiceItems;
    }
  }
  return [];
});

const choicePrompt = computed<string>(() => {
  const elements = props.typedView?.Elements;
  if (!elements?.length) return "";
  for (const el of elements) {
    if (el.Kind === "choice" && el.ChoiceItems?.length) {
      return el.ChoicePrompt ?? "";
    }
  }
  return "";
});

/** Choice items without a Param — rendered as grid buttons. */
const buttonChoiceItems = computed<ChoiceItem[]>(() => choiceItems.value.filter(i => !i.Param));

/** Choice items with a Param — rendered as text-input forms below the buttons. */
const formChoiceItems = computed<ChoiceItem[]>(() => choiceItems.value.filter(i => !!i.Param));

// ── Form-mode choice ──────────────────────────────────────────────────────────

/** The form-mode choice element, if the current view has one. */
const formElement = computed(() => {
  const elements = props.typedView?.Elements;
  if (!elements?.length) return null;
  for (const el of elements) {
    if (el.Kind === "choice" && el.ChoiceMode === "form" && el.ChoiceFields?.length) {
      return el;
    }
  }
  return null;
});

const formFields = computed<ChoiceField[]>(() => formElement.value?.ChoiceFields ?? []);
const formIntent = computed<string>(() => formElement.value?.ChoiceIntent ?? "");
const formPrompt = computed<string>(() => formElement.value?.ChoicePrompt ?? "");

/** Mutable draft values keyed by field name. Reset whenever the form element changes. */
const formDrafts = reactive<Record<string, string>>({});

watch(formFields, (fields) => {
  // Reset drafts to defaults when the form changes.
  for (const f of fields) {
    if (!(f.Name in formDrafts)) {
      formDrafts[f.Name] = f.Default != null ? String(f.Default) : "";
    }
  }
}, { immediate: true });

function submitForm() {
  if (props.pending || !formIntent.value) return;
  const slots: Record<string, unknown> = {};
  for (const f of formFields.value) {
    const raw = formDrafts[f.Name] ?? "";
    if (f.Type === "int") {
      slots[f.Name] = parseInt(raw, 10) || 0;
    } else if (f.Type === "float") {
      slots[f.Name] = parseFloat(raw) || 0;
    } else {
      slots[f.Name] = raw;
    }
  }
  emit("intent", formIntent.value, slots);
}

// Primary = the FIRST non-navigation item only — so one action is visually
// highlighted as the recommended choice rather than all of them.
const navIntents = new Set(["back", "look", "cancel", "exit"]);
const firstPrimaryIntent = computed<string>(() => {
  for (const item of buttonChoiceItems.value) {
    if (!navIntents.has(item.Intent)) return item.Intent + "|" + JSON.stringify(item.Slots ?? {});
  }
  return "";
});
function isPrimary(item: ChoiceItem): boolean {
  return (item.Intent + "|" + JSON.stringify(item.Slots ?? {})) === firstPrimaryIntent.value;
}

// Per-item free-text drafts (keyed by intent+slots to handle duplicate intent names)
const paramDrafts = ref<Record<string, string>>({});

function fireChoiceItem(item: ChoiceItem) {
  if (props.pending) return;
  emit("intent", item.Intent, (item.Slots as Record<string, unknown>) ?? {}, item.Label);
}

function fireChoiceParam(item: ChoiceItem, text: string) {
  const t = text.trim();
  if (props.pending || !t || !item.Param) return;
  const slots: Record<string, unknown> = { ...(item.Slots ?? {}), [item.Param.Slot]: t };
  emit("intent", item.Intent, slots);
  const key = item.Intent + "|" + JSON.stringify(item.Slots);
  paramDrafts.value[key] = "";
}

// ── Legacy IntentInfo path ────────────────────────────────────────────────────

/** Intents with no free-text slot and no slots at all -> plain action buttons. */
const actionIntents = computed(() =>
  props.intents.filter((i) => !i.text_slot && !i.has_slots),
);

// Intent names already covered by a choice-item — suppress the legacy
// text-slot textarea to avoid rendering the same intent twice.
//
// Rule:
//   - Param forms (formChoiceItems): always suppress — the param form IS the
//     text input for that intent.
//   - Plain buttons with no pre-filled slots (buttonChoiceItems with Slots
//     empty/null): suppress — the button fires the intent; a textarea adds
//     nothing and clutters the UI for non-conversational rooms.
//   - Plain buttons WITH pre-filled slots (e.g. "refine" → discuss {message:""}):
//     do NOT suppress — the pre-filled slot is a shortcut; the textarea still
//     lets the operator provide actual content (e.g. typed feedback).
const coveredByChoiceItem = computed<Set<string>>(() => {
  const names = new Set<string>();
  for (const item of formChoiceItems.value) {
    names.add(item.Intent);
  }
  for (const item of buttonChoiceItems.value) {
    const hasPrefilledSlots = item.Slots != null && Object.keys(item.Slots).length > 0;
    if (!hasPrefilledSlots) names.add(item.Intent);
  }
  return names;
});

/** Intents that bind a single free-text slot -> the composer input.
 *  Suppressed when a choice-item already covers the intent (see above). */
const textIntents = computed(() =>
  props.intents.filter((i) => !!i.text_slot && !coveredByChoiceItem.value.has(i.name)),
);

/**
 * True when this room uses semantic routing as its primary affordance.
 * Requirements: the engine sent a typedView (meaning the room has a structured
 * view declaration), but the view contains no choice or form elements and no
 * text-slot intents. The room view already describes the action vocabulary via
 * list: / prose: elements; the input surface should be a free-text textarea
 * that routes via session.turn. Without a typedView the legacy button path applies.
 */
const isSemanticRoom = computed(() => {
  if (!props.typedView?.Elements?.length) return false;
  if (choiceItems.value.length || formElement.value) return false;
  if (textIntents.value.length) return false;
  return true;
});

const selectedTextName = ref<string>("");

// Default the selected text intent to the first one; keep it valid as intents change.
watch(
  textIntents,
  (list) => {
    if (!list.length) {
      selectedTextName.value = "";
      return;
    }
    if (!list.some((i) => i.name === selectedTextName.value)) {
      selectedTextName.value = list[0].name;
    }
  },
  { immediate: true },
);

const activeTextIntent = computed<IntentInfo | undefined>(() =>
  textIntents.value.find((i) => i.name === selectedTextName.value),
);

const placeholder = computed(() => {
  const it = activeTextIntent.value;
  if (!it) return "Type a message…";
  return `${it.title || it.name}…`;
});

const draft = ref("");

// Free-text draft for semantic routing rooms.
const rawDraft = ref("");

function fireIntent(intent: IntentInfo) {
  if (props.pending) return;
  emit("intent", intent.name, {});
}

function send() {
  const text = draft.value.trim();
  const it = activeTextIntent.value;
  if (props.pending || !text || !it) return;
  emit("intent", it.name, { [it.text_slot as string]: text });
  draft.value = "";
}

function sendRaw() {
  const text = rawDraft.value.trim();
  if (props.pending || !text) return;
  emit("send", text, "");
  rawDraft.value = "";
}
</script>

<style scoped>
.input-bar {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 20px;
  background: #14171d;
  border-top: 1px solid #2a2f3a;
}

.input-bar__choice-prompt {
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: #64748b;
}

.input-bar__actions {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
}

.input-bar__action-btn {
  appearance: none;
  border: 1px solid #4a5568;
  background: #1f2530;
  color: #c8cdd8;
  font-size: 13px;
  font-weight: 500;
  padding: 8px 16px;
  border-radius: 8px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  text-align: left;
  width: 100%;
  transition:
    background 0.12s ease,
    border-color 0.12s ease,
    color 0.12s ease;
}

.input-bar__action-btn--primary {
  background: #1e3a5f;
  border-color: #2563eb;
  color: #93c5fd;
}

.input-bar__action-btn--primary:hover:not(:disabled) {
  background: #1d4ed8;
  border-color: #3b82f6;
  color: #fff;
}

.input-bar__action-btn:hover:not(:disabled) {
  background: #2a3340;
  border-color: #6b7588;
  color: #e6e9ef;
}

.input-bar__btn-label {
  font-weight: 600;
  font-size: 13px;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.input-bar__btn-hint {
  font-size: 11px;
  color: #64748b;
  font-weight: 400;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.input-bar__action-btn--primary .input-bar__btn-hint {
  color: #7da8d8;
}

.input-bar__action-btn:disabled,
.input-bar__send:disabled,
.input-bar__input:disabled,
.input-bar__select:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

.input-bar__forms {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.input-bar__choice-param-form {
  display: flex;
  align-items: center;
  gap: 8px;
}

.input-bar__choice-param-label {
  font-size: 13px;
  font-weight: 600;
  color: #94a3b8;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  /* fixed width so all inputs start at the same x position */
  flex: 0 0 12rem;
}

.input-bar__form-grid {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.input-bar__form-row {
  display: flex;
  align-items: center;
  gap: 10px;
}

.input-bar__form-label {
  font-size: 13px;
  font-weight: 600;
  color: #94a3b8;
  white-space: nowrap;
  flex: 0 0 14rem;
}

.input-bar__form-unit {
  font-size: 11px;
  font-weight: 400;
  color: #64748b;
  margin-left: 0.3em;
}

.input-bar__form-input {
  flex: 1 1 auto;
  max-width: 10rem;
}

.input-bar__form-submit {
  align-self: flex-end;
  margin-top: 4px;
}

.input-bar__composer {
  display: flex;
  align-items: stretch;
  gap: 8px;
}

.input-bar__select {
  background: #1f2530;
  color: #e6e9ef;
  border: 1px solid #3a4250;
  border-radius: 8px;
  padding: 0 10px;
  font-size: 13px;
}

.input-bar__input {
  flex: 1 1 auto;
  background: #0f1115;
  color: #e6e9ef;
  border: 1px solid #3a4250;
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 14px;
  outline: none;
}

.input-bar__input:focus {
  border-color: #2563eb;
}

.input-bar__textarea {
  flex: 1 1 auto;
  background: #0f1115;
  color: #e6e9ef;
  border: 1px solid #3a4250;
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 14px;
  font-family: inherit;
  outline: none;
  resize: vertical;
  min-height: 2.8rem;
  line-height: 1.5;
}

.input-bar__textarea:focus {
  border-color: #2563eb;
}

.input-bar__composer--semantic {
  align-items: flex-end;
}

.input-bar__send {
  appearance: none;
  border: none;
  background: #2563eb;
  color: #fff;
  font-size: 14px;
  font-weight: 600;
  padding: 0 20px;
  border-radius: 8px;
  cursor: pointer;
}

.input-bar__send:hover:not(:disabled) {
  background: #1d4ed8;
}
</style>
