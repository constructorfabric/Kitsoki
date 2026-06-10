<script setup lang="ts">
/**
 * DomainModel — three collapsible sections describing a room's domain surface:
 * world keys (with read/write/readwrite direction), intents, and transitions.
 * Transition targets are clickable; clicking one emits `select-room` with the
 * target room id so the parent can navigate.
 */
import { ref } from "vue";
import type {
  WorldKey,
  IntentSpec,
  TransitionSpec,
} from "../../data/editor.js";

defineProps<{
  worldKeys: WorldKey[];
  intents: IntentSpec[];
  transitions: TransitionSpec[];
}>();

const emit = defineEmits<{ (e: "select-room", roomId: string): void }>();

const openWorld = ref(true);
const openIntents = ref(true);
const openTransitions = ref(true);

/** A synthetic target (@exit:*, .) is not a navigable room. */
function isRoom(target: string): boolean {
  return !!target && target !== "." && !target.startsWith("@") && !target.startsWith("__exit__");
}
</script>

<template>
  <section class="domain" data-testid="editor-domain-model">
    <!-- World keys -->
    <div class="domain__section">
      <button class="domain__head" @click="openWorld = !openWorld">
        <span class="domain__caret">{{ openWorld ? "▾" : "▸" }}</span>
        World keys
        <span class="domain__count">{{ worldKeys.length }}</span>
      </button>
      <div v-if="openWorld" class="domain__body" data-testid="editor-domain-world">
        <p v-if="worldKeys.length === 0" class="domain__empty">No world keys referenced.</p>
        <ul v-else class="domain__rows">
          <li v-for="wk in worldKeys" :key="wk.name" class="domain__row" data-testid="editor-domain-worldkey">
            <span class="domain__key">{{ wk.name }}</span>
            <span v-if="wk.type" class="domain__type">{{ wk.type }}</span>
            <span class="domain__dir" :class="`domain__dir--${wk.direction}`">{{ wk.direction }}</span>
          </li>
        </ul>
      </div>
    </div>

    <!-- Intents -->
    <div class="domain__section">
      <button class="domain__head" @click="openIntents = !openIntents">
        <span class="domain__caret">{{ openIntents ? "▾" : "▸" }}</span>
        Intents
        <span class="domain__count">{{ intents.length }}</span>
      </button>
      <div v-if="openIntents" class="domain__body" data-testid="editor-domain-intents">
        <p v-if="intents.length === 0" class="domain__empty">No intents.</p>
        <ul v-else class="domain__rows">
          <li v-for="it in intents" :key="it.name" class="domain__row" data-testid="editor-domain-intent">
            <span class="domain__key">{{ it.name }}</span>
            <span v-if="it.title" class="domain__title">{{ it.title }}</span>
          </li>
        </ul>
      </div>
    </div>

    <!-- Transitions -->
    <div class="domain__section">
      <button class="domain__head" @click="openTransitions = !openTransitions">
        <span class="domain__caret">{{ openTransitions ? "▾" : "▸" }}</span>
        Transitions
        <span class="domain__count">{{ transitions.length }}</span>
      </button>
      <div v-if="openTransitions" class="domain__body" data-testid="editor-domain-transitions">
        <p v-if="transitions.length === 0" class="domain__empty">No outgoing transitions.</p>
        <ul v-else class="domain__rows">
          <li
            v-for="(tr, i) in transitions"
            :key="i"
            class="domain__row"
            data-testid="editor-domain-transition"
          >
            <span class="domain__key">{{ tr.intent }}</span>
            <span class="domain__arrow">→</span>
            <a
              v-if="isRoom(tr.target)"
              href="#"
              class="domain__target domain__target--link"
              data-testid="editor-domain-transition-target"
              @click.prevent="emit('select-room', tr.target)"
            >{{ tr.target }}</a>
            <span v-else class="domain__target">{{ tr.target }}</span>
            <code v-if="tr.when" class="domain__when">{{ tr.when }}</code>
          </li>
        </ul>
      </div>
    </div>
  </section>
</template>

<style scoped>
.domain__section {
  border: 1px solid var(--border, #2a2d35);
  border-radius: 6px;
  margin-bottom: 0.5rem;
  overflow: hidden;
}
.domain__head {
  width: 100%;
  text-align: left;
  background: #1c1f26;
  border: none;
  color: inherit;
  cursor: pointer;
  padding: 0.45rem 0.6rem;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.domain__caret {
  width: 1em;
  opacity: 0.7;
}
.domain__count {
  margin-left: auto;
  font-size: 0.75rem;
  opacity: 0.6;
}
.domain__body {
  padding: 0.5rem 0.6rem;
}
.domain__empty {
  opacity: 0.6;
  font-style: italic;
  margin: 0;
}
.domain__rows {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.domain__row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.85rem;
}
.domain__key {
  font-family: monospace;
  font-weight: 600;
}
.domain__type {
  opacity: 0.6;
  font-size: 0.8rem;
}
.domain__dir {
  margin-left: auto;
  font-size: 0.7rem;
  text-transform: uppercase;
  padding: 0.05rem 0.35rem;
  border-radius: 3px;
  background: #2a2d35;
}
.domain__dir--read { background: #2d4a63; }
.domain__dir--write { background: #3a4a2d; }
.domain__dir--readwrite { background: #4a3a2d; }
.domain__arrow { opacity: 0.6; }
.domain__target--link {
  color: #6db3f2;
  text-decoration: none;
}
.domain__target--link:hover {
  text-decoration: underline;
}
.domain__when {
  margin-left: auto;
  opacity: 0.65;
  font-size: 0.8rem;
}
</style>
