<script setup lang="ts">
/**
 * HookDetail — renders a room's on_enter effect chain as a stack of cards, one
 * per effect, data-driven from RoomDetail.on_enter (graph.EffectSpec). Each card
 * shows a kind badge plus the effect's salient key/value fields (invoke handler,
 * call id, guard, bound world keys, set world keys).
 */
import type { EffectSpec } from "../../data/editor.js";

defineProps<{ onEnter: EffectSpec[] }>();
</script>

<template>
  <section class="hook" data-testid="editor-hook">
    <h3 class="hook__title">on_enter</h3>
    <p v-if="onEnter.length === 0" class="hook__empty">
      This room runs no on_enter effects.
    </p>
    <ol v-else class="hook__list">
      <li
        v-for="(eff, i) in onEnter"
        :key="i"
        class="hook__card"
        data-testid="editor-hook-effect"
      >
        <div class="hook__card-head">
          <span class="hook__badge" :class="`hook__badge--${eff.kind}`">{{ eff.kind }}</span>
          <span v-if="eff.invoke" class="hook__invoke">{{ eff.invoke }}</span>
          <span v-if="eff.id" class="hook__id">#{{ eff.id }}</span>
        </div>
        <dl class="hook__fields">
          <template v-if="eff.when">
            <dt>when</dt>
            <dd><code>{{ eff.when }}</code></dd>
          </template>
          <template v-if="eff.bind && eff.bind.length">
            <dt>binds</dt>
            <dd>{{ eff.bind.join(", ") }}</dd>
          </template>
          <template v-if="eff.sets && eff.sets.length">
            <dt>sets</dt>
            <dd>{{ eff.sets.join(", ") }}</dd>
          </template>
        </dl>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.hook__title {
  margin: 0 0 0.5rem;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.7;
}
.hook__empty {
  opacity: 0.6;
  font-style: italic;
}
.hook__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.hook__card {
  border: 1px solid var(--border, #2a2d35);
  border-radius: 6px;
  padding: 0.5rem 0.6rem;
}
.hook__card-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.3rem;
}
.hook__badge {
  font-size: 0.7rem;
  text-transform: uppercase;
  font-weight: 700;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
  background: #2a2d35;
}
.hook__badge--invoke { background: #2d4a63; }
.hook__badge--set { background: #3a4a2d; }
.hook__badge--emit_intent { background: #4a3a2d; }
.hook__invoke {
  font-family: monospace;
  font-size: 0.85rem;
}
.hook__id {
  font-family: monospace;
  opacity: 0.7;
  font-size: 0.8rem;
}
.hook__fields {
  margin: 0;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.1rem 0.6rem;
  font-size: 0.85rem;
}
.hook__fields dt {
  opacity: 0.65;
}
.hook__fields dd {
  margin: 0;
}
</style>
