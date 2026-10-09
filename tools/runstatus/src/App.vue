<template>
  <!-- Chrome-less spatial-handoff window (?chromeless=1): the transient `/point`
       surface a terminal operator reaches via the OSC 8 link. It renders ONLY
       the picker + chat composer — none of the app chrome below — so the window
       is single-purpose and self-closing (docs/tui/spatial-handoff.md). -->
  <PointPage v-if="chromeless" />
  <template v-else>
    <router-view />
    <!-- Global meta-mode launcher + overlay. Mounted once at the shell so the
         overlay state (and its persistent chat) survives route navigation. -->
    <MetaButton />
    <MetaOverlay />
    <!-- Global guided-tour surface, same single-mount rationale: one tour can
         walk from the home screen into a live session without losing its place. -->
    <TourButton />
    <TourOverlay />
    <!-- Global dynamic-workflow surface: one launcher item, one modal, same
         shared service as the CLI and MCP surfaces. -->
    <WorkflowModal />
    <!-- Global inbox surface: badge launcher + panel + transient toast. Mounted
         once at the shell so the inbox follows the operator across every route. -->
    <InboxBadge />
    <InboxPanel />
    <InboxToast />
    <!-- Global forwarded-question surface: a hard modal shown when a dispatched
         agent forwards an AskUserQuestion into kitsoki and blocks for the answer. -->
    <OperatorQuestionModal />
    <!-- Global bug-report review surface: capture → review → file. -->
    <BugReportModal />
    <!-- Global fullscreen artifact viewer: full-screens a produced markdown
         artifact (PRD, design, summary) so it can be opened and scrolled. -->
    <ArtifactModal />
  </template>
</template>

<script setup lang="ts">
// App shell — mounts the router view plus the global meta-mode + tour surfaces.
import { onMounted, onUnmounted } from "vue";
import { useRouter } from "vue-router";
import MetaButton from "./components/meta/MetaButton.vue";
import MetaOverlay from "./components/meta/MetaOverlay.vue";
import TourButton from "./components/tour/TourButton.vue";
import TourOverlay from "./components/tour/TourOverlay.vue";
import WorkflowModal from "./components/workflow/WorkflowModal.vue";
import InboxBadge from "./components/InboxBadge.vue";
import InboxPanel from "./components/InboxPanel.vue";
import InboxToast from "./components/InboxToast.vue";
import OperatorQuestionModal from "./components/OperatorQuestionModal.vue";
import BugReportModal from "./components/BugReportModal.vue";
import ArtifactModal from "./components/ArtifactModal.vue";
import PointPage from "./views/PointPage.vue";
import { useTourStore } from "./stores/tour.js";
import { useInboxStore } from "./stores/inbox.js";
import { useOperatorQuestionStore } from "./stores/operatorQuestions.js";
import { LiveSource } from "./data/live-source.js";
import { isChromeless } from "./lib/chromeless.js";

const router = useRouter();

// Chrome-less spatial-handoff window: when ?chromeless=1 the shell renders ONLY
// PointPage and skips every global feed/surface below (no tour auto-start, no
// SSE inbox/question streams) — the window is transient and single-purpose.
const chromeless = isChromeless();

// The global notification feed is a single cross-session EventSource; start it
// once at the shell (unless snapshot/artifact mode — no live server to feed it).
const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;
const inbox = useInboxStore();
const operatorQuestions = useOperatorQuestionStore();
const source = !isSnapshot && !chromeless ? new LiveSource("/") : null;
if (!isSnapshot && !chromeless) {
  inbox.init(source!);
  // Forwarded-question feed shares the live-server lifecycle with the inbox.
  operatorQuestions.init(source!);
}
onUnmounted(() => {
  inbox.teardown();
  operatorQuestions.teardown();
});

// First-login auto-start — ONLY when landing on the home screen, where the tour
// begins. Auto-starting on a deep-linked session view would strand it on a step
// whose home anchors don't exist. Elsewhere the "?" button starts it on demand.
// Wait for the router so the initial hash route is resolved (it reads as "/"
// before that). No-op once completed, in snapshot mode, or under automation.
onMounted(async () => {
  if (chromeless) return;
  await router.isReady();
  if (router.currentRoute.value.path === "/") {
    let projectOnboarded = false;
    try {
      projectOnboarded = (await source?.setupStatus())?.project_onboarded === true;
    } catch {
      projectOnboarded = false;
    }
    useTourStore().maybeAutoStart({ projectOnboarded });
  }
});
</script>
