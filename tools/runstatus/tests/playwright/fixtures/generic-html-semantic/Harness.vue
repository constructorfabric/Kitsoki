<script setup lang="ts">
import { computed, ref } from "vue";
import ArtifactAnnotator from "../../../../src/components/ArtifactAnnotator.vue";
import type { DataSource } from "../../../../src/data/source.js";
import type { AnnotationAnchor, MediaKind } from "../../../../src/lib/annotationAnchor.js";
import { serializeAnchor } from "../../../../src/lib/annotationAnchor.js";
import type { SemanticSidecar } from "../../../../src/lib/semanticPlugins.js";
import replayEvents from "../../../fixtures/spatial-replay.rrweb.json";

type DemoCase = {
  id: string;
  title: string;
  eyebrow: string;
  mediaHandle: string;
  mediaKind: MediaKind;
  note: string;
  naturalWidth?: number;
  naturalHeight?: number;
  liveEmbed?: boolean;
};

const sidecars: Record<string, SemanticSidecar> = {
  "structure.html": {
    plugin: "html-structure",
    schema_version: 1,
    viewport: { width: 900, height: 520 },
    elements: [
      {
        ref: "form.submit",
        kind: "control",
        label: "Submit button",
        selector: "[data-node='submit']",
        description: "Primary action in the static HTML mockup",
        data: { path: "mockup.form.actions.submit" },
      },
      {
        ref: "form.email",
        kind: "field",
        label: "Email input",
        selector: "[data-node='email']",
        data: { path: "mockup.form.fields.email" },
      },
    ],
  },
  "object-record.html": {
    plugin: "html-data",
    schema_version: 1,
    viewport: { width: 900, height: 520 },
    elements: [
      {
        ref: "issue.priority",
        kind: "field",
        label: "Priority",
        selector: "[data-field='priority']",
        value: "P1",
        description: "The issue priority value rendered from the backing object",
        data: { path: "issue.priority", object_id: "ISS-248" },
      },
      {
        ref: "issue.status",
        kind: "field",
        label: "Status",
        selector: "[data-field='status']",
        value: "Blocked",
        data: { path: "issue.status", object_id: "ISS-248" },
      },
    ],
  },
  "wireframe.svg": {
    plugin: "wireframe",
    schema_version: 1,
    viewport: { width: 900, height: 520 },
    elements: [
      {
        ref: "wire.submit",
        kind: "control",
        label: "Create account CTA",
        description: "BBox-only marker over a rendered mockup",
        bbox: [574, 374, 190, 54],
        data: { path: "screen.signup.primaryCta" },
      },
      {
        ref: "wire.plan-card",
        kind: "layout-node",
        label: "Plan card",
        bbox: [108, 196, 260, 182],
        data: { path: "screen.signup.planCard" },
      },
    ],
  },
  "bug-playback.rrweb": {
    plugin: "bug-rrweb",
    schema_version: 1,
    elements: [
      {
        ref: "run.start",
        kind: "control",
        label: "Start intent",
        selector: "[data-testid='intent-btn-start']",
        description: "Selector resolved against the reconstructed rrweb DOM",
        data: { path: "replay.actions.start" },
      },
    ],
  },
};

const cases: DemoCase[] = [
  {
    id: "html-selector",
    eyebrow: "HTML structure",
    title: "Selector-only static HTML field",
    mediaHandle: "structure.html",
    mediaKind: "html",
    note: "Why does this submit control look enabled before the required email field is valid?",
  },
  {
    id: "object-data",
    eyebrow: "Object representation",
    title: "Rendered data field with value context",
    mediaHandle: "object-record.html",
    mediaKind: "html",
    note: "This priority value should explain why ISS-248 is still blocked.",
  },
  {
    id: "mockup-bbox",
    eyebrow: "Mockup / demo",
    title: "BBox-only semantic marker on a visual mockup",
    mediaHandle: "wireframe.svg",
    mediaKind: "png",
    note: "Make this create-account CTA less dominant than the plan comparison.",
  },
  {
    id: "rrweb-selector",
    eyebrow: "Bug playback",
    title: "Semantic field resolved inside rrweb playback",
    mediaHandle: "bug-playback.rrweb",
    mediaKind: "rrweb",
    naturalWidth: 1280,
    naturalHeight: 720,
    note: "In this bug replay, is Start the control the operator should click next?",
  },
  {
    id: "live-embed",
    eyebrow: "Live producer",
    title: "Interactive embed owns element picking",
    mediaHandle: "live-embed.html",
    mediaKind: "slidey",
    liveEmbed: true,
    note: "Tighten the headline on this scene without changing the metrics row.",
  },
];

const ds = {
  artifactUrl(handle: string): string {
    return `./${handle}`;
  },
  artifactPosterUrl(handle: string): string {
    return `./${handle}`;
  },
  async semanticMap(_sessionId: string, handle: string): Promise<SemanticSidecar | null> {
    return sidecars[handle] ?? null;
  },
  async videoEvents() {
    return {
      events: replayEvents as unknown[],
      width: 1280,
      height: 720,
    };
  },
  async videoFrame() {
    return { handle: "frame.png", mime: "image/png", kind: "image" };
  },
} as Partial<DataSource> as DataSource;

const activeId = ref(cases[0].id);
const selected = ref<AnnotationAnchor | null>(null);
const feedback = ref(cases[0].note);
const sent = ref<{ text: string; wire: unknown } | null>(null);

const active = computed(() => cases.find((c) => c.id === activeId.value) ?? cases[0]);
const wire = computed(() => (selected.value ? serializeAnchor(selected.value) : null));

function selectCase(id: string): void {
  const next = cases.find((c) => c.id === id) ?? cases[0];
  activeId.value = next.id;
  selected.value = null;
  sent.value = null;
  feedback.value = next.note;
}

function onAnchor(anchor: AnnotationAnchor): void {
  selected.value = anchor;
  sent.value = null;
}

function send(): void {
  sent.value = { text: feedback.value, wire: wire.value };
}
</script>

<template>
  <main class="semantic-demo" data-testid="semantic-demo">
    <section class="hero">
      <div>
        <p class="kicker">Generic semantic annotation</p>
        <h1>Click a displayed field, then ask or give feedback.</h1>
      </div>
      <div class="hero-meter">
        <span>Anchor union</span>
        <strong>semantic_element</strong>
      </div>
    </section>

    <nav class="case-tabs" aria-label="Semantic annotation variations">
      <button
        v-for="c in cases"
        :key="c.id"
        type="button"
        class="case-tab"
        :class="{ 'case-tab--active': c.id === activeId }"
        :data-testid="'case-tab-' + c.id"
        @click="selectCase(c.id)"
      >
        <span>{{ c.eyebrow }}</span>
        {{ c.title }}
      </button>
    </nav>

    <section class="workspace" :data-testid="'case-' + active.id">
      <div class="surface">
        <div class="surface-head">
          <span>{{ active.eyebrow }}</span>
          <strong>{{ active.title }}</strong>
        </div>
        <ArtifactAnnotator
          :key="active.id"
          :ds="ds"
          session-id="semantic-demo-session"
          :media-handle="active.mediaHandle"
          :media-kind="active.mediaKind"
          :live-embed="active.liveEmbed"
          :natural-width="active.naturalWidth"
          :natural-height="active.naturalHeight"
          route="/demo/generic-html-semantic"
          @anchor="onAnchor"
        />
      </div>

      <aside class="feedback-panel" data-testid="feedback-panel">
        <div>
          <p class="panel-label">Selected anchor</p>
          <pre data-testid="selected-anchor">{{ wire ? JSON.stringify(wire, null, 2) : "Click a semantic field marker." }}</pre>
        </div>
        <label class="feedback-label" for="feedback-input">Question or feedback</label>
        <textarea id="feedback-input" v-model="feedback" data-testid="feedback-input" />
        <button
          type="button"
          class="send-btn"
          data-testid="send-feedback"
          :disabled="!wire"
          @click="send"
        >
          Send read-only feedback
        </button>
        <div v-if="sent" class="sent" data-testid="sent-status">
          Sent read-only feedback with {{ wire?.kind }} anchor.
        </div>
      </aside>
    </section>
  </main>
</template>
