<script setup lang="ts">
import { computed } from "vue";
import { useData, withBase } from "vitepress";
import type { ExtensionComponent, ExtensionLibrary as ExtensionLibraryData } from "../../data/extensions";

const { theme } = useData();
const library = computed(() => theme.value.extensions as ExtensionLibraryData);

const featuredComponents = computed(() => {
  const priority = [
    "hook",
    "host-interface",
    "agent-profile",
    "provider-profile",
    "toolbox",
    "agent-plugin",
    "starlark-script",
    "schema",
  ];
  const out: ExtensionComponent[] = [];
  for (const kind of priority) {
    const found = library.value.components.find((component) => component.kind === kind);
    if (found) out.push(found);
  }
  return out.slice(0, 8);
});
</script>

<template>
  <section class="klib-hero" aria-labelledby="extension-library-title">
    <p class="klib-eyebrow">Build-time extension index</p>
    <h1 id="extension-library-title">Generated marketplace and reference docs</h1>
    <p>
      Browse Kitsoki packages, stories, and generated component reference from one source-owned index.
      <code>docs.yaml</code>, <code>app.yaml</code>, story assets, and selected Go source are staged into the product-site Library during the build.
    </p>
    <div class="klib-stats" aria-label="Library summary">
      <span><strong>{{ library.stats.packages }}</strong> packages</span>
      <span><strong>{{ library.stats.stories }}</strong> stories</span>
      <span><strong>{{ library.stats.components }}</strong> components</span>
      <span><strong>{{ library.stats.hostInterfaces }}</strong> host interfaces</span>
      <span><strong>{{ library.stats.agents }}</strong> agent surfaces</span>
      <span><strong>{{ library.stats.hooks }}</strong> hooks</span>
    </div>
  </section>

  <section class="klib-section" aria-labelledby="pipeline-title">
    <h2 id="pipeline-title">Build-time pipeline</h2>
    <div class="klib-pipeline">
      <div>
        <span class="klib-chip">source</span>
        <h3>app.yaml + docs.yaml</h3>
        <p>Stories, kits, docs sidecars, prompts, schemas, scripts, agents, providers, toolboxes, and flows.</p>
      </div>
      <div>
        <span class="klib-chip">index</span>
        <h3>kitsoki docs index</h3>
        <p>Loads source through the real validators and emits <code>kitsoki.extensions-index/v1</code>.</p>
      </div>
      <div>
        <span class="klib-chip">stage</span>
        <h3>stage-extensions.mjs</h3>
        <p>Writes <code>.vitepress/gen/extensions-index.json</code> for the site build.</p>
      </div>
      <div>
        <span class="klib-chip">render</span>
        <h3>Library routes</h3>
        <p>Packages, stories, and generated components share one marketplace/reference surface.</p>
      </div>
    </div>
  </section>

  <section v-if="library.packages.length" class="klib-section" aria-labelledby="packages-title">
    <h2 id="packages-title">Packages</h2>
    <div class="klib-grid">
      <a
        v-for="pkg in library.packages"
        :key="pkg.id"
        class="klib-card"
        :href="withBase(`/library/packages/${pkg.slug}.html`)"
      >
        <span class="klib-chip">{{ pkg.kind }}</span>
        <h3>{{ pkg.title }}</h3>
        <p>{{ pkg.summary || pkg.id }}</p>
        <dl class="klib-meta">
          <div><dt>Stories</dt><dd>{{ pkg.stories.length }}</dd></div>
          <div><dt>Components</dt><dd>{{ pkg.components.length }}</dd></div>
          <div><dt>Docs</dt><dd>{{ pkg.publishedDocs.length }}</dd></div>
        </dl>
      </a>
    </div>
  </section>

  <section id="generated-components" class="klib-section" aria-labelledby="components-title">
    <h2 id="components-title">Generated components</h2>
    <p class="klib-section-note">
      Host interfaces, Starlark scripts, schemas, story contracts, agent profiles, provider profiles, toolboxes, plugins, prompts, and hooks use the same generated page format.
    </p>
    <div class="klib-grid klib-kind-grid">
      <a
        v-for="group in library.componentGroups"
        :key="group.kind"
        class="klib-card"
        :href="withBase(`/library/components/${group.components[0].slug}.html`)"
      >
        <span class="klib-chip">{{ group.kind }}</span>
        <h3>{{ group.title }}</h3>
        <p>{{ group.count }} generated {{ group.count === 1 ? "entry" : "entries" }} from the extension index.</p>
      </a>
    </div>
  </section>

  <section v-if="featuredComponents.length" class="klib-section" aria-labelledby="featured-components-title">
    <h2 id="featured-components-title">Reference examples</h2>
    <div class="klib-story-list">
      <a
        v-for="component in featuredComponents"
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

  <section class="klib-section" aria-labelledby="stories-title">
    <h2 id="stories-title">Stories</h2>
    <div class="klib-story-list">
      <a
        v-for="story in library.stories.slice(0, 24)"
        :key="story.id"
        class="klib-row"
        :href="withBase(`/library/stories/${story.slug}.html`)"
      >
        <span>
          <strong>{{ story.title }}</strong>
          <small>{{ story.package_id || story.id }}</small>
        </span>
        <span>{{ story.states.length }} states - {{ story.agents.length }} agents - {{ story.flows.length }} flows</span>
      </a>
    </div>
  </section>
</template>
