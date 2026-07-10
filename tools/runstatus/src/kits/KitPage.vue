<template>
  <div ref="mountEl" class="kit-page" data-testid="kit-page"></div>
  <div v-if="error" class="kit-page__error" data-testid="kit-page-error">{{ error }}</div>
</template>

<script setup lang="ts">
/**
 * KitPage — the SPA's one generic mount point for ANY installed kit's UI
 * entry (S5, .context/kits-implementation-plan.md D3). kitLoader.ts's
 * installKitRoutes() registers one route per provides.ui entry, all pointed
 * at this same component with `route.meta.moduleUrl` set — this file has no
 * knowledge of which kit it's rendering.
 *
 * The route's query string is forwarded to the kit module's mount() as
 * plain string params (e.g. ?catalog=...&overlay=... for
 * @kitsoki/object-graph) — see kits/object-graph/ui/src/kit-rpc.ts's doc
 * comment for why a kit module reads params this way rather than importing
 * vue-router itself.
 */
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";

interface KitModule {
  default: {
    mount(el: HTMLElement, params: Record<string, string>): () => void;
  };
}

const route = useRoute();
const mountEl = ref<HTMLElement | null>(null);
const error = ref("");
let cleanup: (() => void) | null = null;

function queryParams(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(route.query)) {
    if (typeof v === "string") out[k] = v;
  }
  return out;
}

async function mountKit(): Promise<void> {
  error.value = "";
  cleanup?.();
  cleanup = null;
  const moduleUrl = route.meta.moduleUrl as string | undefined;
  if (!moduleUrl || !mountEl.value) {
    error.value = "kit page: no module URL for this route";
    return;
  }
  try {
    const mod = (await import(/* @vite-ignore */ moduleUrl)) as KitModule;
    cleanup = mod.default.mount(mountEl.value, queryParams());
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

onMounted(mountKit);
watch(() => [route.meta.moduleUrl, route.query], mountKit);
onBeforeUnmount(() => cleanup?.());
</script>

<style scoped>
.kit-page {
  height: 100vh;
  overflow: auto;
}
.kit-page__error {
  padding: 1rem;
  color: var(--error-color, #b00020);
}
</style>
