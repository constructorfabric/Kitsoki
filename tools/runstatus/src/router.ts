import { createRouter, createWebHashHistory } from "vue-router";
import HomeView from "./views/HomeView.vue";
import RunView from "./views/RunView.vue";
import InteractiveView from "./views/InteractiveView.vue";
import EditorPage from "./views/EditorPage.vue";
import ReviewPage from "./views/ReviewPage.vue";
import StartBugfixView from "./views/StartBugfixView.vue";
import { installKitRoutes } from "./kits/kitLoader.js";

const router = createRouter({
  // Hash history: works fine for both live and file:// artifact mode.
  history: createWebHashHistory(),
  routes: [
    // The home screen is the multi-story browser + live-session list. (The old
    // single-session SessionList is subsumed by HomeView's active-sessions
    // section.)
    { path: "/", component: HomeView },
    { path: "/start/bugfix", component: StartBugfixView },
    { path: "/s/:sessionId", component: RunView, props: true },
    // The chat route also honours an optional `?notif=<id>` query param (a
    // shareable inbox deep-link): InteractiveView teleports to that
    // notification's target room on mount, then clears the param. No separate
    // route — it's a query param so existing /s/:id/chat links keep working.
    { path: "/s/:sessionId/chat", component: InteractiveView, props: true },
    // Story editor: per-story static inspector. Story + room selected via
    // query params: /editor?story=<id|path>&room=<id>[&session=<id>].
    { path: "/editor", component: EditorPage },
    // /review/:sessionId?video=<handle> — the video feedback surface.
    { path: "/review/:sessionId", component: ReviewPage, props: true },
    // /graph used to be a hardcoded route to the engine's own
    // ObjectGraphPage.vue (W5.0). S5 (.context/kits-implementation-plan.md
    // D4) moved that viewer to @kitsoki/object-graph's own UI module; it
    // mounts at runtime via kitLoader.ts's router.addRoute("/kit/graph")
    // call (installedKitRoutes.ts / index.html's injected kit registry),
    // not as a route declared here — the engine SPA has no
    // object-graph-specific code left.
  ],
});

installKitRoutes(router);

export default router;
