import { createRouter, createWebHashHistory } from "vue-router";
import SessionList from "./views/SessionList.vue";
import RunView from "./views/RunView.vue";

const router = createRouter({
  // Hash history: works fine for both live and file:// artifact mode.
  history: createWebHashHistory(),
  routes: [
    { path: "/", component: SessionList },
    { path: "/s/:sessionId", component: RunView, props: true },
  ],
});

export default router;
