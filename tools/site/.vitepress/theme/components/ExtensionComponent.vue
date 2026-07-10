<script setup lang="ts">
import { computed } from "vue";
import { withBase } from "vitepress";
import type { ExtensionComponent } from "../../data/extensions";

const props = defineProps<{ component: ExtensionComponent }>();

const ownerHref = computed(() => {
  if (props.component.ownerStorySlug) return `/library/stories/${props.component.ownerStorySlug}.html`;
  if (props.component.ownerPackageSlug) return `/library/packages/${props.component.ownerPackageSlug}.html`;
  return "";
});
const sourcePath = computed(() => props.component.generated_from || props.component.path || "");
const primaryFacts = computed(() => props.component.facts.slice(0, 6));
const remainingFacts = computed(() => props.component.facts.slice(6));
const redacted = computed(() => props.component.publish === "summary" || props.component.tags.includes("redacted"));
</script>

<template>
  <section class="klib-detail">
    <p class="klib-eyebrow">Component - {{ props.component.kind }}</p>
    <h1>{{ props.component.title }}</h1>
    <p class="klib-lede">{{ props.component.summary || props.component.componentKey }}</p>
    <div class="klib-stats">
      <span><strong>{{ props.component.publish }}</strong> publish</span>
      <span v-if="props.component.owner"><strong>owner</strong> {{ props.component.owner }}</span>
      <span v-if="sourcePath"><strong>source</strong> {{ sourcePath }}</span>
      <span v-if="redacted"><strong>safe</strong> redacted</span>
    </div>
  </section>

  <section class="klib-section">
    <h2>Public contract</h2>
    <div class="klib-columns">
      <div>
        <h3>Identity</h3>
        <dl class="klib-facts">
          <div><dt>Kind</dt><dd><code>{{ props.component.kind }}</code></dd></div>
          <div><dt>ID</dt><dd><code>{{ props.component.id }}</code></dd></div>
          <div v-if="props.component.owner"><dt>Owner</dt><dd><code>{{ props.component.owner }}</code></dd></div>
          <div v-if="sourcePath"><dt>Generated from</dt><dd><code>{{ sourcePath }}</code></dd></div>
        </dl>
        <p v-if="ownerHref" class="klib-actionline">
          <a :href="withBase(ownerHref)">Open owner</a>
        </p>
      </div>
      <div v-if="primaryFacts.length">
        <h3>Generated facts</h3>
        <dl class="klib-facts">
          <div v-for="fact in primaryFacts" :key="fact.label">
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>
      </div>
      <div>
        <h3>Publication policy</h3>
        <p v-if="redacted">
          This component is published as a summary. Raw prompts, credential values, headers, transcripts, and cassettes stay out of the site index.
        </p>
        <p v-else>
          This component is safe to publish from source-owned declarations and generated metadata.
        </p>
        <div class="klib-tags" v-if="props.component.tags.length">
          <span v-for="tag in props.component.tags" :key="tag" class="klib-chip">{{ tag }}</span>
        </div>
      </div>
    </div>
  </section>

  <section class="klib-section" v-if="remainingFacts.length || props.component.uses.length">
    <h2>Usage and details</h2>
    <div class="klib-columns">
      <div v-if="remainingFacts.length">
        <h3>More facts</h3>
        <dl class="klib-facts">
          <div v-for="fact in remainingFacts" :key="fact.label">
            <dt>{{ fact.label }}</dt>
            <dd>{{ fact.value }}</dd>
          </div>
        </dl>
      </div>
      <div v-if="props.component.uses.length">
        <h3>Uses</h3>
        <ul>
          <li v-for="item in props.component.uses" :key="item"><code>{{ item }}</code></li>
        </ul>
      </div>
    </div>
  </section>

  <section class="klib-section" v-if="props.component.publishedDocs.length">
    <h2>Published docs</h2>
    <div class="klib-docs">
      <div v-for="doc in props.component.publishedDocs" :key="doc.id" class="klib-doc">
        <span class="klib-chip">{{ doc.kind || doc.publish }}</span>
        <h3>{{ doc.title }}</h3>
        <p><code>{{ doc.path || doc.generated_from }}</code></p>
      </div>
    </div>
  </section>
</template>
