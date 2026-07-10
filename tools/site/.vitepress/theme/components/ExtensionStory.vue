<script setup lang="ts">
import { computed } from "vue";
import { withBase } from "vitepress";
import type { ExtensionStory } from "../../data/extensions";

const props = defineProps<{ story: ExtensionStory }>();

const runtimeSections = computed(() =>
  [
    { title: "Host interfaces", items: props.story.host_interfaces },
    { title: "Agents", items: props.story.agents },
    { title: "Provider profiles", items: props.story.providers },
    { title: "Toolboxes", items: props.story.toolboxes },
    { title: "Agent plugins", items: props.story.agent_plugins },
  ].filter((section) => section.items.length),
);

const assetSections = computed(() =>
  [
    { title: "Imports", items: props.story.imports },
    { title: "Prompts", items: props.story.prompts },
    { title: "Schemas", items: props.story.schemas },
    { title: "Scripts", items: props.story.scripts },
  ].filter((section) => section.items.length),
);

const generatedComponents = computed(() => props.story.components.slice(0, 48));
const redactedCount = computed(() => props.story.components.filter((component) => component.publish === "summary").length);
</script>

<template>
  <section class="klib-detail">
    <p class="klib-eyebrow">Story - {{ props.story.id }}</p>
    <h1>{{ props.story.title }}</h1>
    <p class="klib-lede">
      <code>{{ props.story.path }}</code> renders as a generated story contract: state machine, runtime surface, public integration contract, source-owned docs, and no-LLM proof.
    </p>
    <p v-if="props.story.package_id && props.story.packageSlug">
      Package:
      <a :href="withBase(`/library/packages/${props.story.packageSlug}.html`)"><code>{{ props.story.package_id }}</code></a>
    </p>
    <div class="klib-stats">
      <span><strong>{{ props.story.states.length }}</strong> states</span>
      <span><strong>{{ props.story.intents.length }}</strong> intents</span>
      <span><strong>{{ props.story.agents.length }}</strong> agents</span>
      <span><strong>{{ props.story.host_interfaces.length }}</strong> host interfaces</span>
      <span><strong>{{ props.story.flows.length }}</strong> flows</span>
      <span><strong>{{ props.story.components.length }}</strong> components</span>
    </div>
  </section>

  <section class="klib-section">
    <h2>Runtime surface</h2>
    <div class="klib-columns" v-if="runtimeSections.length">
      <div v-for="section in runtimeSections" :key="section.title">
        <h3>{{ section.title }}</h3>
        <ul>
          <li v-for="item in section.items" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
    </div>
    <p v-else>No runtime surfaces declared.</p>
  </section>

  <section class="klib-section" v-if="generatedComponents.length">
    <h2>Generated story documentation</h2>
    <p class="klib-section-note">
      The story page links to the same component-detail mechanism used by host interfaces, agent profiles, providers, toolboxes, prompts, schemas, scripts, and hooks.
      <span v-if="redactedCount"> {{ redactedCount }} entries are summary-only by publication policy.</span>
    </p>
    <div class="klib-story-list">
      <a
        v-for="component in generatedComponents"
        :key="component.componentKey"
        class="klib-row"
        :href="withBase(`/library/components/${component.slug}.html`)"
      >
        <span>
          <strong>{{ component.title }}</strong>
          <small>{{ component.summary || component.componentKey }}</small>
        </span>
        <span>{{ component.kind }} - {{ component.publish }}</span>
      </a>
    </div>
  </section>

  <section class="klib-section" v-if="assetSections.length || props.story.publishedDocs.length">
    <h2>Source-owned docs and assets</h2>
    <div class="klib-columns">
      <div v-for="section in assetSections" :key="section.title">
        <h3>{{ section.title }}</h3>
        <ul>
          <li v-for="item in section.items" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
      <div v-if="props.story.publishedDocs.length">
        <h3>Published docs</h3>
        <ul>
          <li v-for="doc in props.story.publishedDocs" :key="doc.id"><code>{{ doc.path || doc.generated_from }}</code></li>
        </ul>
      </div>
    </div>
  </section>

  <section class="klib-section">
    <h2>State machine</h2>
    <div class="klib-columns">
      <div>
        <h3>States</h3>
        <ul class="klib-compact">
          <li v-for="item in props.story.states" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
      <div>
        <h3>Intents</h3>
        <ul class="klib-compact">
          <li v-for="item in props.story.intents" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
      <div v-if="props.story.world_keys.length">
        <h3>World keys</h3>
        <ul class="klib-compact">
          <li v-for="item in props.story.world_keys" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
    </div>
  </section>

  <section class="klib-section" v-if="props.story.exports.length || props.story.exits.length || props.story.flows.length">
    <h2>Integration and proof</h2>
    <div class="klib-columns">
      <div v-if="props.story.exports.length">
        <h3>Exports</h3>
        <ul><li v-for="item in props.story.exports" :key="item"><code>{{ item }}</code></li></ul>
      </div>
      <div v-if="props.story.exits.length">
        <h3>Exits</h3>
        <ul><li v-for="item in props.story.exits" :key="item"><code>{{ item }}</code></li></ul>
      </div>
      <div v-if="props.story.flows.length">
        <h3>No-LLM flows</h3>
        <ul><li v-for="item in props.story.flows" :key="item"><code>{{ item }}</code></li></ul>
      </div>
    </div>
  </section>
</template>
