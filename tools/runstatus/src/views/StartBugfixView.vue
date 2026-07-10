<template>
  <main class="start-bugfix" data-testid="start-bugfix-view">
    <p v-if="error" class="start-bugfix__error" data-testid="start-bugfix-error">
      {{ error }}
    </p>
    <p v-else class="start-bugfix__status" data-testid="start-bugfix-status">
      Starting bugfix session…
    </p>
  </main>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { LiveSource, type StoryHeader } from "../data/live-source.js";

const route = useRoute();
const router = useRouter();
const source = new LiveSource("/");
const error = ref("");

function one(value: unknown): string {
  if (Array.isArray(value)) return typeof value[0] === "string" ? value[0] : "";
  return typeof value === "string" ? value : "";
}

function titleFromPath(path: string): string {
  const base = path.split("/").pop() ?? path;
  return base.replace(/\.md$/i, "").replace(/[-_]+/g, " ").trim();
}

function bugfixStoryPath(stories: StoryHeader[]): string {
  const story =
    stories.find((s) => s.app_id === "bugfix") ??
    stories.find((s) => /(^|\/)stories\/bugfix\/app\.ya?ml$/.test(s.path));
  return story?.path ?? "stories/bugfix/app.yaml";
}

function githubRepo(url: string): string {
  const match = /^https:\/\/github\.com\/([^/]+\/[^/]+)\/issues\/\d+(?:[/?#].*)?$/i.exec(
    url
  );
  return match?.[1] ?? "";
}

onMounted(async () => {
  const id = one(route.query.id).trim();
  const path = one(route.query.path).trim();
  const url = one(route.query.url).trim();
  const title = one(route.query.title).trim() || titleFromPath(path || url || id);
  const sourceRef = path || url;
  if (!id || !sourceRef) {
    error.value = "Bugfix session link is missing the filed bug id or source.";
    return;
  }

  try {
    const stories = await source.listStories();
    const storyPath = bugfixStoryPath(stories);
    const sessionId = await source.newSession(storyPath, {
      initialWorld: {
        ticket_source_mode: url ? "remote" : "local",
        ticket_source_ref: sourceRef,
        ticket_url: url,
        ticket_repo: githubRepo(url),
        thread: sourceRef,
        oversight_mode: "no-gate",
        judge_mode: "human",
      },
    });
    const draft = `work ticket ${id} titled ${title}`;
    await router.replace({
      path: `/s/${sessionId}/chat`,
      query: { draft },
    });
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
});
</script>

<style scoped>
.start-bugfix {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 2rem;
  background: var(--k-bg, #0f172a);
  color: var(--k-fg, #e5e7eb);
}

.start-bugfix__status,
.start-bugfix__error {
  margin: 0;
  font-size: 0.95rem;
}

.start-bugfix__error {
  color: var(--k-danger, #f87171);
}
</style>
