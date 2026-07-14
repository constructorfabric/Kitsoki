<script setup lang="ts">
/**
 * The whole demo block of a /features/<id> page: the chaptered video wired to
 * the step-by-step cards (card click → video seeks to that step's chapter),
 * plus the deeper-docs / related-features links. Receives the page's $params
 * (from src/features/[id].paths.ts) verbatim.
 */
import { computed, ref } from "vue";
import { useData, withBase } from "vitepress";
import ChapteredVideo from "./ChapteredVideo.vue";
import TourStepCards from "./TourStepCards.vue";

interface Link {
  text: string;
  href: string;
}

const props = defineProps<{
  feature: {
    id: string;
    kind: string;
    title: string;
    tagline: string;
    summary: string;
    media: {
      videoUrl: string | null;
      posterUrl: string | null;
      chaptersUrl: string | null;
      videoAvailable: boolean;
      embedKind?: "deck" | "rrweb" | null;
      embedUrl: string | null;
    };
    steps: Array<{ id: string; title: string; body: string; shotUrl: string | null }>;
    docLinks: Link[];
    related: Link[];
  };
}>();

const player = ref<InstanceType<typeof ChapteredVideo> | null>(null);
const { theme } = useData();
const text = computed(() => theme.value.siteText?.labels ?? {});

function href(l: Link): string {
  return l.href.startsWith("/") ? withBase(l.href) : l.href;
}

const DEMO_BRIEFS: Record<string, { question: string; watch: string; proof: string }> = {
  "agent-actions": {
    question: "Can the runtime overrule a model before state advances?",
    watch: "The reject, nudge, resubmit, accept arc inside one bounded agent call.",
    proof: "The model proposes; the host contract decides whether the result is valid.",
  },
  "trace-introspection": {
    question: "Can a run be audited from trace data instead of a chat transcript?",
    watch: "Decision detail, evidence, confidence, replay, and the trace view modes.",
    proof: "The recorded trace is the source of truth for replay, inspection, and tests.",
  },
  "trace-features": {
    question: "Can the observer make a complex run debuggable?",
    watch: "View modes, event kinds, latency, world mutation, annotation, and replay.",
    proof: "Every important runtime action is recorded as a structured event.",
  },
  "operator-ask": {
    question: "What happens when a headless agent asks a human-only question?",
    watch: "The agent parks, the operator answers a blocking modal, and the run resumes.",
    proof: "Kitsoki refuses silent default answers at human decision points.",
  },
  "meta-improvement": {
    question: "Can a run make the next run better?",
    watch: "A completed session, the improve reminder, story.improve streaming, and the evidence-backed report.",
    proof: "The improvement loop is trace-backed, read-only, and fileable locally, to GitHub, or to a private ticket provider.",
  },
  "dev-story-bugfix": {
    question: "Can this structure drive a real repo workflow end to end?",
    watch: "Triage, reproduce, propose, implement, test, review, validate, and PR handoff.",
    proof: "The workflow is a named graph of gates rather than one unbounded agent prompt.",
  },
  "stranger-install": {
    question: "Can a developer try Kitsoki without a source checkout?",
    watch: "A released binary runs inside an existing repo and writes auditable setup.",
    proof: "The product path starts from the user repo, not from Kitsoki internals.",
  },
};

const DEFAULT_BRIEFS: Record<string, { question: string; watch: string; proof: string }> = {
  feature: {
    question: "Which product behavior does this surface prove?",
    watch: "The first few beats show the capability in the real Kitsoki UI.",
    proof: "The page is generated from the same catalog and deterministic fixtures used by QA.",
  },
  "product-tour": {
    question: "How do the main product surfaces fit together?",
    watch: "The path through library, session, trace, meta, and observer views.",
    proof: "One story definition drives the same behavior across the product surfaces.",
  },
  "story-demo": {
    question: "Can a Kitsoki story model a realistic repo workflow?",
    watch: "The handoffs between rooms, operator decisions, host calls, and replayed results.",
    proof: "The workflow is replayed from fixtures instead of improvised for the site.",
  },
};

const brief = computed(() => DEMO_BRIEFS[props.feature.id] ?? DEFAULT_BRIEFS[props.feature.kind] ?? DEFAULT_BRIEFS.feature);
const keySteps = computed(() => (props.feature.steps.length > 5 ? props.feature.steps.slice(0, 5) : props.feature.steps));
const remainingSteps = computed(() => (props.feature.steps.length > 5 ? props.feature.steps.slice(5) : []));
</script>

<template>
  <div class="kdemo">
    <p class="kdemo__summary">{{ feature.summary }}</p>

    <section class="kdemo__brief" aria-label="Demo orientation">
      <div>
        <strong>Question</strong>
        <p>{{ brief.question }}</p>
      </div>
      <div>
        <strong>Watch for</strong>
        <p>{{ brief.watch }}</p>
      </div>
      <div>
        <strong>Why it matters</strong>
        <p>{{ brief.proof }}</p>
      </div>
    </section>

    <ChapteredVideo ref="player" :media="feature.media" :title="feature.title" :feature-id="feature.id" />

    <template v-if="feature.steps.length">
      <h2 id="step-by-step">Key beats</h2>
      <TourStepCards :steps="keySteps" @seek="(id) => player?.seekToStep(id)" />
      <details v-if="remainingSteps.length" class="kdemo__walkthrough">
        <summary>Full recorded walkthrough ({{ feature.steps.length }} steps)</summary>
        <TourStepCards :steps="remainingSteps" @seek="(id) => player?.seekToStep(id)" />
      </details>
    </template>

    <aside v-if="feature.docLinks.length || feature.related.length" class="kdemo__links">
      <div v-if="feature.docLinks.length">
        <h3>{{ text.deeperDocs ?? "Deeper docs" }}</h3>
        <ul>
          <li v-for="l in feature.docLinks" :key="l.href">
            <a :href="href(l)">{{ l.text }}</a>
          </li>
        </ul>
      </div>
      <div v-if="feature.related.length">
        <h3>{{ text.relatedFeatures ?? "Related features" }}</h3>
        <ul>
          <li v-for="l in feature.related" :key="l.href">
            <a :href="href(l)">{{ l.text }}</a>
          </li>
        </ul>
      </div>
    </aside>
  </div>
</template>
