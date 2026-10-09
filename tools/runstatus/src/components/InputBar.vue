<template>
  <div
    ref="rootEl"
    class="input-bar"
    :class="{ 'input-bar--compact': compact, 'input-bar--collapsed': collapsed }"
    data-testid="input-bar"
  >
    <!-- Compact / collapsed: when the chat occupies a short space (e.g. the narrow
         sidebar surface where Chat shares height with Trace + Graph) the structured
         widgets won't fit, so collapse to a SINGLE-LINE text input. Free text always
         routes via session.turn (the semantic router / off-ramp), so this honors the
         text-only contract for every room. A disclosure icon reveals the hidden
         actions (and a count) when there are any; making the pane taller un-collapses
         automatically. -->
    <form
      v-if="collapsed"
      class="input-bar__composer input-bar__composer--compact"
      data-testid="composer"
      @submit.prevent="sendRaw"
    >
      <input
        v-model="rawDraft"
        class="input-bar__input"
        data-testid="composer-input"
        :placeholder="rawPlaceholder('Type a message…')"
        :disabled="pending"
        @keydown.tab="onRawTab"
        @keydown.enter.exact.prevent="sendRaw"
      />
      <span
        v-if="rawActionHint"
        class="input-bar__raw-hint"
        :class="{ 'input-bar__raw-hint--warning': hasWarningContext }"
      >
        Enter: {{ rawActionHint.label }}
      </span>
      <button
        v-if="hasControls"
        type="button"
        class="input-bar__disclose"
        data-testid="input-disclose"
        :title="`Show ${controlCount} action${controlCount === 1 ? '' : 's'}`"
        aria-label="Show quick actions"
        @click="expandControls"
      >
        <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
          <path d="M4 10l4-4 4 4" fill="none" stroke="currentColor" stroke-width="1.5"
            stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        <span v-if="controlCount" class="input-bar__disclose-count">{{ controlCount }}</span>
      </button>
      <button
        class="input-bar__send"
        type="submit"
        data-testid="composer-send"
        :disabled="pending || !canSubmitRaw"
      >
        Send
      </button>
    </form>

    <template v-else>
    <div class="input-bar__expanded">
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
          :class="{
            'input-bar__action-btn--primary': isPrimary(item),
            'input-bar__action-btn--warning-default': isWarningPrimary(item),
          }"
          type="button"
          :disabled="pending"
          :data-testid="`intent-btn-${item.Intent}`"
          :data-slots="JSON.stringify(item.Slots ?? {})"
          :title="item.Hint || undefined"
          @click="fireChoiceItem(item)"
        >
          <span v-if="hasWarningContext && isPrimary(item)" class="input-bar__btn-badge">recommended</span>
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
          <!-- A wrapping, auto-growing textarea (NOT a single-line <input>): a
               long refine instruction typed into a single-line input scrolls its
               START off the left edge, so the operator's message "populates out
               of view" as they type — unreadable in a conversation and on camera.
               This wraps + grows so the full message is always visible. Enter
               still submits (Shift+Enter for a newline). -->
          <textarea
            v-model="paramDrafts[item.Intent + '|' + JSON.stringify(item.Slots)]"
            v-autogrow
            class="input-bar__input input-bar__param-input"
            rows="1"
            :placeholder="item.Param!.Placeholder || item.Label"
            :disabled="pending"
            @keydown.enter.exact.prevent="
              fireChoiceParam(item, paramDrafts[item.Intent + '|' + JSON.stringify(item.Slots)] ?? '')
            "
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
      <div class="input-bar__compact-bar">
        <button
          type="button"
          class="input-bar__disclose"
          data-testid="input-collapse"
          title="Collapse quick actions"
          aria-label="Collapse quick actions"
          @click="collapseControls"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5"
              stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
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
      <div class="input-bar__compact-bar">
        <button
          type="button"
          class="input-bar__disclose"
          data-testid="input-collapse"
          title="Collapse quick actions"
          aria-label="Collapse quick actions"
          @click="collapseControls"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5"
              stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
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
          v-autogrow
          class="input-bar__textarea"
          data-testid="composer-input"
          :placeholder="rawPlaceholder('Type anything — the router handles the rest…')"
          rows="2"
          :disabled="pending"
          @keydown.tab="onRawTab"
          @keydown.enter.exact.prevent="sendRaw"
        />
        <span
          v-if="rawActionHint"
          class="input-bar__raw-hint"
          :class="{ 'input-bar__raw-hint--warning': hasWarningContext }"
        >
          Enter: {{ rawActionHint.label }}
        </span>
        <button
          class="input-bar__send"
          type="submit"
          data-testid="composer-send"
          :disabled="pending || !canSubmitRaw"
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
          :class="{ 'input-bar__action-btn--warning-default': isWarningPrimaryIntent(intent) }"
          type="button"
          :disabled="pending"
          :data-testid="`intent-btn-${intent.name}`"
          :title="intent.description || undefined"
          @click="fireIntent(intent)"
        >
          <span v-if="hasWarningContext && isPrimaryIntent(intent)" class="input-bar__btn-badge">recommended</span>
          <span class="input-bar__btn-label">{{ intent.title || humanizeIntent(intent.name) }}</span>
          <span v-if="intent.description" class="input-bar__btn-hint">{{ intent.description }}</span>
        </button>
      </div>
      <div v-if="actionIntents.length" class="input-bar__compact-bar">
        <button
          type="button"
          class="input-bar__disclose"
          data-testid="input-collapse"
          title="Collapse quick actions"
          aria-label="Collapse quick actions"
          @click="collapseControls"
        >
          <svg width="15" height="15" viewBox="0 0 16 16" aria-hidden="true">
            <path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5"
              stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
    </template>

    <!-- Free-text floor: a choice/form widget otherwise hides all free text,
         but the text-only contract (transports.md §7) says every room must be
         drivable by typing. This persistent, de-emphasized composer routes via
         session.turn (semantic router → agent off-ramp), so arbitrary text is
         always submittable alongside the structured widget — the only path to a
         no-match the off-ramp needs. Mirrors the TUI's Tab escape-hatch
         (choice_widget.go); the widget stays the primary affordance, this is the
         floor beneath it. Semantic/text-slot rooms already expose a text box, so
         they need no extra floor. rawDraft is shared with the semantic composer,
         so a draft survives any widget↔text switch. -->
    <form
      v-if="showTextFloor"
      class="input-bar__composer input-bar__composer--floor"
      data-testid="text-floor"
      @submit.prevent="sendRaw"
    >
      <textarea
        v-model="rawDraft"
        v-autogrow
        class="input-bar__textarea"
        data-testid="text-floor-input"
        :placeholder="rawPlaceholder('…or type a message instead')"
        rows="1"
        :disabled="pending"
        @keydown.tab="onRawTab"
        @keydown.enter.exact.prevent="sendRaw"
      />
      <span
        v-if="rawActionHint"
        class="input-bar__raw-hint"
        :class="{ 'input-bar__raw-hint--warning': hasWarningContext }"
      >
        Enter: {{ rawActionHint.label }}
      </span>
      <button
        class="input-bar__send"
        type="submit"
        data-testid="text-floor-send"
        :disabled="pending || !canSubmitRaw"
      >
        Send
      </button>
    </form>

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
          {{ intent.title || humanizeIntent(intent.name) }}
        </option>
      </select>

      <textarea
        v-model="draft"
        v-autogrow
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
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import type { IntentInfo, View, ChoiceItem, ChoiceField } from "../types.js";
import { humanizeIntent } from "../lib/intent.js";

// ── v-autogrow ────────────────────────────────────────────────────────────────
// Grow a composer textarea to fit its content so the operator's full message
// stays visible as they type. A fixed-size field scrolls the START of a long
// message out of view — horizontally for a single-line <input>, vertically past
// its rows for a <textarea> — which is unreadable in a conversation and on
// camera (Kitsoki demos CONSTANTLY show typing). CSS `min-height` floors the
// resting size and `max-height` caps the growth (then it scrolls), so this only
// ever resizes WITHIN those bounds — the resting look of every composer is
// unchanged. `updated` re-fits after v-model resets to "" on send.
function autoGrowEl(el: HTMLTextAreaElement): void {
  el.style.height = "auto";
  el.style.height = `${el.scrollHeight}px`;
}
const vAutogrow = {
  mounted(el: HTMLTextAreaElement) {
    autoGrowEl(el);
    el.addEventListener("input", () => autoGrowEl(el));
  },
  updated(el: HTMLTextAreaElement) {
    autoGrowEl(el);
  },
};

const props = defineProps<{
  intents: IntentInfo[];
  typedView?: View | null;
  /**
   * The room's free-text sink (its `default_intent`, resolved to an intent
   * name) when it declares one. The composer defaults its text-input box to
   * this intent so a typed reply routes the way the room author intended
   * (e.g. `answer` in the PRD clarifying room) rather than the arbitrary first
   * text-slot intent. Falls back to the first text intent when absent or not
   * itself a text intent.
   */
  defaultIntent?: string;
  pending?: boolean;
  initialRawDraft?: string;
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

const hasWarningContext = computed<boolean>(() => {
  const elements = props.typedView?.Elements ?? [];
  return elements.some((element) => {
    if (element.Kind !== "banner") return false;
    const color = (element.Color ?? "").toLowerCase();
    if (color === "warn" || color === "warning" || color === "amber") return true;
    const text = `${element.Source ?? ""} ${element.Subtitle ?? ""} ${element.Marker ?? ""}`;
    return /\b(warn|warning|stale|changed|removed)\b/i.test(text);
  });
});

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
function isWarningPrimary(item: ChoiceItem): boolean {
  return hasWarningContext.value && isPrimary(item);
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

const firstPrimaryActionIntent = computed<string>(() => {
  for (const intent of actionIntents.value) {
    if (!navIntents.has(intent.name)) return intent.name;
  }
  return actionIntents.value[0]?.name ?? "";
});
function isPrimaryIntent(intent: IntentInfo): boolean {
  return intent.name === firstPrimaryActionIntent.value;
}
function isWarningPrimaryIntent(intent: IntentInfo): boolean {
  return hasWarningContext.value && isPrimaryIntent(intent);
}

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

/**
 * True when a structured widget (single-mode choice or form-mode choice) is the
 * room's primary affordance and would otherwise hide free text entirely. In that
 * case we still render a free-text floor below the widget so arbitrary text stays
 * submittable via session.turn (semantic router → off-ramp), honoring the
 * text-only contract. Semantic and text-slot rooms already expose a text box, so
 * they get no extra floor.
 */
const showTextFloor = computed(() => choiceItems.value.length > 0 || formElement.value != null);

const selectedTextName = ref<string>("");

// Default the selected text intent to the room's free-text sink
// (default_intent) when it is itself a text intent; otherwise the first one.
// Keep it valid as intents change. Defaulting to default_intent is what keeps
// a typed reply in a sink room (e.g. PRD clarifying) routing to `answer`
// rather than to whatever text-slot intent happens to sort first — the bug
// that sent a single typed answer straight to the brief.
function preferredTextName(list: IntentInfo[]): string {
  const di = props.defaultIntent;
  if (di && list.some((i) => i.name === di)) return di;
  return list[0].name;
}
watch(
  [textIntents, () => props.defaultIntent],
  ([list], prev) => {
    if (!list.length) {
      selectedTextName.value = "";
      return;
    }
    const prevDefault = prev?.[1];
    const defaultChanged = props.defaultIntent !== prevDefault;
    // Re-prefer the room's sink when the CURRENT selection is no longer offered,
    // OR when the room changed its free-text sink (default_intent). The latter
    // is load-bearing across a room transition: an intent like `core__work`
    // (the landing workbench sink) can remain a *valid* text-intent in the next
    // room (it is globally inherited), so a "still valid" check alone would
    // keep the stale selection and route a typed reply to the wrong intent —
    // e.g. typing a PRD idea at core.prd.idle fired core__work and bounced
    // back to landing instead of routing to core__prd__discuss. Honoring a
    // changed default_intent re-pins the composer to the new room's sink.
    if (defaultChanged || !list.some((i) => i.name === selectedTextName.value)) {
      selectedTextName.value = preferredTextName(list);
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
  // Humanise the default text-intent for the placeholder — never leak the raw
  // slug (`core__prd__discuss…`) onto the composer the operator reads.
  return `${it.title || humanizeIntent(it.name)}…`;
});

const draft = ref("");

// Free-text draft for semantic routing rooms.
const rawDraft = ref("");

interface RawAction {
  label: string;
  hint?: string;
  intent: string;
  slots: Record<string, unknown>;
  displayLabel?: string;
  aliases: string[];
}

function normalizeActionText(value: string): string {
  return value.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
}

function rawActionAliases(label: string, intent: string, hint?: string): string[] {
  return [label, intent, humanizeIntent(intent), hint ?? ""]
    .map(normalizeActionText)
    .filter(Boolean);
}

const rawActions = computed<RawAction[]>(() => {
  if (!hasWarningContext.value) return [];
  const actions: RawAction[] = [];
  for (const item of buttonChoiceItems.value) {
    actions.push({
      label: item.Label,
      hint: item.Hint,
      intent: item.Intent,
      slots: (item.Slots as Record<string, unknown>) ?? {},
      displayLabel: item.Label,
      aliases: rawActionAliases(item.Label, item.Intent, item.Hint),
    });
  }
  for (const intent of actionIntents.value) {
    const label = intent.title || humanizeIntent(intent.name);
    actions.push({
      label,
      hint: intent.description,
      intent: intent.name,
      slots: {},
      aliases: rawActionAliases(label, intent.name, intent.description),
    });
  }
  return actions;
});

const recommendedRawAction = computed<RawAction | undefined>(() => {
  const preferred = rawActions.value.find((action) => !navIntents.has(action.intent));
  return preferred ?? rawActions.value[0];
});

function matchRawAction(text: string): RawAction | undefined {
  const query = normalizeActionText(text);
  if (!query) return undefined;
  const exact = rawActions.value.find((action) => action.aliases.some((alias) => alias === query));
  if (exact) return exact;
  return rawActions.value.find((action) =>
    action.aliases.some((alias) => alias.startsWith(query))
  );
}

const rawActionHint = computed<RawAction | undefined>(() =>
  matchRawAction(rawDraft.value) ?? (rawDraft.value.trim() ? undefined : recommendedRawAction.value)
);

function rawPlaceholder(defaultText: string): string {
  if (recommendedRawAction.value) {
    return `Recommended: ${recommendedRawAction.value.label} — Enter follows, Tab fills`;
  }
  return defaultText;
}

const canSubmitRaw = computed<boolean>(() => rawDraft.value.trim().length > 0 || !!recommendedRawAction.value);

function acceptRawActionHint(): void {
  if (props.pending || !rawActionHint.value) return;
  rawDraft.value = rawActionHint.value.label;
}

function onRawTab(event: KeyboardEvent): void {
  if (props.pending || !rawActionHint.value) return;
  event.preventDefault();
  acceptRawActionHint();
}

function fireIntent(intent: IntentInfo) {
  if (props.pending) return;
  emit("intent", intent.name, {});
}

function fireRawAction(action: RawAction): void {
  emit("intent", action.intent, action.slots, action.displayLabel);
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
  if (props.pending) return;
  const action = text ? matchRawAction(text) : recommendedRawAction.value;
  if (action) {
    fireRawAction(action);
    rawDraft.value = "";
    return;
  }
  if (!text) return;
  emit("send", text, "");
  rawDraft.value = "";
}

// ── Height-responsive collapse ────────────────────────────────────────────────
//
// In a short space (the narrow sidebar surface where Chat shares height with
// Trace + Graph) the stacked buttons/forms don't fit, so we collapse to a single
// single-line text input plus a disclosure icon. The signal is the height of the
// input bar's CONTAINING block (`.surface__chat` / the chat column), which is
// constrained by the surface — NOT the input bar's own content — so toggling the
// collapse can't change the measured height and there's no feedback loop. A
// hysteresis band keeps a few px of resize jitter from flapping the state.
const COMPACT_ENTER = 400; // px — at/below this the chat column is too short for stacked actions
const COMPACT_EXIT = 460; // grow past this to un-collapse (gap = hysteresis)

const rootEl = ref<HTMLElement | null>(null);
const compact = ref(false);
const expanded = ref(false);
const manualCollapsed = ref(false);
let ro: ResizeObserver | null = null;

function focusRawDraftInput(): void {
  const schedule = globalThis.requestAnimationFrame ?? ((fn: FrameRequestCallback) => setTimeout(fn, 0));
  void nextTick(() => schedule(() => {
    const el = rootEl.value?.querySelector<HTMLTextAreaElement | HTMLInputElement>(
      '[data-testid="text-floor-input"], [data-testid="composer-input"]'
    );
    el?.focus();
    if (el) {
      const end = el.value.length;
      el.setSelectionRange(end, end);
    }
  }));
}

function applyInitialRawDraft(value?: string): void {
  if (!value || rawDraft.value) return;
  rawDraft.value = value;
  focusRawDraftInput();
}

watch(
  () => props.initialRawDraft,
  (value) => applyInitialRawDraft(value),
  { immediate: true },
);

watch(
  () => [showTextFloor.value, isSemanticRoom.value, textIntents.value.length],
  () => {
    if (props.initialRawDraft && rawDraft.value === props.initialRawDraft) {
      focusRawDraftInput();
    }
  },
  { flush: "post" }
);

function applyHeight(h: number): void {
  // Test mounts and transient hidden hosts can report 0px. Treat that as
  // "unmeasured" rather than forcing the user into compact mode.
  if (h <= 0) return;
  if (h <= COMPACT_ENTER) compact.value = true;
  else if (h >= COMPACT_EXIT) compact.value = false;
}

// Re-collapse whenever the space becomes short again: a height-driven expand
// lasts only until the next shrink, so shrinking the pane always returns to the
// single line. Manual collapse is independent and works at any height.
watch(compact, (c) => {
  if (c) expanded.value = false;
});

/** Collapsed = manually hidden, or short space and not height-expanded. */
const collapsed = computed(() => hasControls.value && (manualCollapsed.value || (compact.value && !expanded.value)));

function expandControls(): void {
  manualCollapsed.value = false;
  expanded.value = true;
}

function collapseControls(): void {
  manualCollapsed.value = true;
  expanded.value = false;
}

/** Count of structured controls that the collapsed view hides (drives the badge;
 *  no controls → no disclosure icon, just the single-line input). */
const controlCount = computed(
  () =>
    buttonChoiceItems.value.length +
    formChoiceItems.value.length +
    actionIntents.value.length +
    (formElement.value ? 1 : 0),
);
const hasControls = computed(() => controlCount.value > 0);

onMounted(() => {
  const host = rootEl.value?.parentElement;
  if (!host || typeof ResizeObserver === "undefined") return;
  applyHeight(host.clientHeight);
  ro = new ResizeObserver((entries) => {
    for (const e of entries) applyHeight(e.contentRect.height);
  });
  ro.observe(host);
});

onUnmounted(() => {
  ro?.disconnect();
  ro = null;
});
</script>

<style scoped>
.input-bar {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 16px 20px;
  background: var(--k-bg-widget, #14171d);
  border-top: 1px solid var(--k-border-subtle, #2a2f3a);
}

.input-bar__expanded {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.input-bar__choice-prompt {
  font-size: 0.8rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--k-fg-muted, #64748b);
}

.input-bar__actions {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
}

.input-bar__action-btn {
  appearance: none;
  border: 1px solid var(--k-border, #4a5568);
  background: var(--k-bg-input, #1f2530);
  color: var(--k-fg, #c8cdd8);
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
  background: var(--k-button-bg, #1e3a5f);
  border-color: var(--k-border-focus, #2563eb);
  /* button-FG (not fg-accent): the accent link color is blue in a light theme and
     would vanish on the blue button fill. button-foreground always contrasts. */
  color: var(--k-button-fg, #93c5fd);
}

.input-bar__action-btn--primary:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #1d4ed8);
  border-color: var(--k-border-focus, #3b82f6);
  color: var(--k-button-fg, #fff);
}

.input-bar__action-btn:hover:not(:disabled) {
  background: var(--k-bg-hover, #2a3340);
  border-color: var(--k-border, #6b7588);
  color: var(--k-fg, #e6e9ef);
}

.input-bar__btn-label {
  font-weight: 600;
  font-size: 13px;
  word-break: break-word;
  overflow-wrap: anywhere;
}

.input-bar__btn-hint {
  font-size: 11px;
  color: var(--k-fg-muted, #64748b);
  font-weight: 400;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 100%;
}

.input-bar__action-btn--primary .input-bar__btn-hint {
  /* sits on the primary (blue) fill — must contrast it; accent-blue would vanish
     under a light theme. button-fg with reduced opacity reads as a soft sub-label. */
  color: var(--k-button-fg, #7da8d8);
  opacity: 0.8;
}

.input-bar__action-btn--warning-default {
  background: #431407;
  border-color: #fb923c;
  color: #fed7aa;
  box-shadow: 0 0 0 1px rgba(251, 146, 60, 0.24);
}

.input-bar__action-btn--warning-default:hover:not(:disabled) {
  background: #7c2d12;
  border-color: #fdba74;
  color: #fff7ed;
}

.input-bar__action-btn--warning-default .input-bar__btn-hint {
  color: #fed7aa;
}

.input-bar__btn-badge {
  align-self: flex-start;
  border: 1px solid currentColor;
  border-radius: 999px;
  padding: 1px 6px;
  font-size: 9px;
  font-weight: 800;
  line-height: 1.2;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.8;
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
  color: var(--k-fg-muted, #94a3b8);
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
  color: var(--k-fg-muted, #94a3b8);
  white-space: nowrap;
  flex: 0 0 14rem;
}

.input-bar__form-unit {
  font-size: 11px;
  font-weight: 400;
  color: var(--k-fg-muted, #64748b);
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
  background: var(--k-bg-input, #1f2530);
  color: var(--k-fg, #e6e9ef);
  border: 1px solid var(--k-border, #3a4250);
  border-radius: 8px;
  padding: 0 10px;
  font-size: 13px;
}

.input-bar__input {
  flex: 1 1 auto;
  background: var(--k-bg-inset, #0f1115);
  color: var(--k-fg, #e6e9ef);
  border: 1px solid var(--k-border, #3a4250);
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 14px;
  outline: none;
}

.input-bar__input:focus {
  border-color: var(--k-border-focus, #2563eb);
}

.input-bar__textarea {
  flex: 1 1 auto;
  background: var(--k-bg-inset, #0f1115);
  color: var(--k-fg, #e6e9ef);
  border: 1px solid var(--k-border, #3a4250);
  border-radius: 8px;
  padding: 10px 14px;
  font-size: 14px;
  font-family: inherit;
  outline: none;
  resize: vertical;
  min-height: 2.8rem;
  /* cap growth (v-autogrow) then scroll, so a very long message can't eat the
     whole pane; min-height above keeps the resting 2-row look unchanged. */
  max-height: 11rem;
  /* border-box so v-autogrow's height = scrollHeight is exact (scrollHeight
     includes padding); without it the field would creep taller on each keypress. */
  box-sizing: border-box;
  line-height: 1.5;
}

.input-bar__textarea:focus {
  border-color: var(--k-border-focus, #2563eb);
}

.input-bar__raw-hint {
  align-self: center;
  flex: 0 0 auto;
  max-width: 12rem;
  color: var(--k-fg-muted, #94a3b8);
  border: 1px solid var(--k-border-subtle, #2a2f3a);
  background: var(--k-bg-input, #1f2530);
  border-radius: 999px;
  padding: 0.25rem 0.55rem;
  font-size: 0.72rem;
  font-weight: 650;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.input-bar__raw-hint--warning {
  color: #fed7aa;
  border-color: #fb923c;
  background: #431407;
}

/* The param composer (refine / regenerate / any slot-form) is a wrapping,
   auto-growing textarea — never a single-line input whose long message scrolls
   its start out of view. min-height matches the single-line input it replaced so
   the inline [label · field · Send] row looks unchanged at rest; it grows with
   the message (capped, then scrolls) so the full text stays on screen. */
.input-bar__param-input {
  font-family: inherit;
  line-height: 1.4;
  resize: none;
  overflow-y: auto;
  min-height: 2.6rem;
  max-height: 7.5rem;
  box-sizing: border-box;
}

.input-bar__composer--semantic {
  align-items: flex-end;
}

/* The free-text floor sits below a choice/form widget as a de-emphasized
   escape hatch — a dashed rule and muted, slightly smaller box keep the
   structured widget the visual primary while text stays reachable. */
.input-bar__composer--floor {
  align-items: flex-end;
  padding-top: 12px;
  border-top: 1px dashed var(--k-border-subtle, #2a2f3a);
}

.input-bar__composer--floor .input-bar__textarea {
  font-size: 13px;
  background: var(--k-bg-inset, #0c0e12);
  min-height: 2.4rem;
}

.input-bar__send {
  appearance: none;
  border: none;
  background: var(--k-button-hover-bg, #2563eb);
  color: var(--k-button-fg, #fff);
  font-size: 14px;
  font-weight: 600;
  padding: 0 20px;
  border-radius: 8px;
  cursor: pointer;
}

/* ── Compact / collapsed (short space) ──────────────────────────────────────
   A single row: single-line input + a disclosure icon (reveals the hidden
   actions, or grow the panel) + Send. Tighter padding so it reads as a slim bar. */
.input-bar--collapsed {
  padding: 10px 14px;
}

.input-bar__composer--compact {
  align-items: center;
}

/* The disclosure icon button — indicates structured actions exist (with a count)
   and reveals them on click; a make-it-taller hint lives in its tooltip. */
.input-bar__disclose {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  appearance: none;
  border: 1px solid var(--k-border, #3a4250);
  background: var(--k-bg-input, #1f2530);
  color: var(--k-fg-muted, #94a3b8);
  border-radius: 8px;
  padding: 0 10px;
  cursor: pointer;
  flex: 0 0 auto;
  transition:
    background 0.12s ease,
    color 0.12s ease,
    border-color 0.12s ease;
}
.input-bar__disclose:hover {
  background: var(--k-bg-hover, #2a3340);
  color: var(--k-fg, #e6e9ef);
  border-color: var(--k-border, #6b7588);
}
.input-bar__disclose svg {
  display: block;
}
.input-bar__disclose-count {
  font-size: 11px;
  font-weight: 600;
  line-height: 1;
}

/* The collapse affordance shown when expanded inside a short space — a slim,
   right-aligned chevron-down row above the structured widgets. */
.input-bar__compact-bar {
  display: flex;
  justify-content: flex-end;
  margin: -4px 0 -2px;
}
.input-bar__compact-bar .input-bar__disclose {
  padding: 2px 8px;
}

.input-bar__send:hover:not(:disabled) {
  background: var(--k-button-bg, #1d4ed8);
}
</style>
