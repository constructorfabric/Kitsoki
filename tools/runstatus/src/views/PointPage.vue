<script setup lang="ts">
/**
 * PointPage — the transient, chrome-less spatial-handoff window
 * (docs/tui/spatial-handoff.md).
 *
 * It is the slice-2 capture surface stripped to JUST frame + point + element +
 * chat: a terminal operator clicked the OSC 8 link the TUI printed
 * (`/point?token=…&chromeless=1`), this page boots in chrome-less mode (App.vue
 * mounts it INSTEAD of the router shell when isChromeless()), they point at the
 * frame, type a question, and Send. On Send it POSTs the visual bundle to
 * `/point/return?token=…` (the server's one-time-token return endpoint, which
 * resolves the parked TUI turn) and then `window.close()`s — with a "✓ sent —
 * you can close this tab" fallback when the browser refuses programmatic close.
 *
 * It reuses SpatialPicker verbatim (no new picker code, epic decision); the only
 * new surface is this thin frame + composer wrapper and the return POST.
 */
import { ref, computed } from "vue";
import { createDataSource } from "../data/source.js";
import SpatialPicker from "../components/SpatialPicker.vue";
import type { ResolvedElement } from "../lib/resolveElement.js";
import { pointToken } from "../lib/chromeless.js";

const ds = createDataSource();

// The window's display context rides on the query string the TUI minted: the
// media handle to show + the timestamp + the originating route. The picker
// resolves the element against the LIVE document (one resolver, two roots — the
// rendered frame is in this very page).
const params = new URLSearchParams(window.location.search);
const mediaHandle = params.get("media_handle") ?? "";
const tMs = Number(params.get("t_ms") ?? "0");
const route = params.get("route") ?? "";
const prompt = params.get("prompt") ?? "Point at what you mean, then send.";

const frameNatural = ref({ width: 1280, height: 720 });
const pickerRoot = computed<Document | null>(() =>
  typeof document !== "undefined" ? document : null,
);

const picked = ref<{
  point: { x: number; y: number };
  element?: ResolvedElement;
} | null>(null);
const question = ref("");
const sent = ref(false);
const sending = ref(false);
const errMsg = ref("");

function mediaUrl(): string {
  return mediaHandle ? ds.artifactUrl(mediaHandle) : "";
}

function onLoadedImage(ev: Event) {
  const img = ev.target as HTMLImageElement;
  if (img.naturalWidth && img.naturalHeight) {
    frameNatural.value = { width: img.naturalWidth, height: img.naturalHeight };
  }
}

function onPick(bundle: {
  point: { x: number; y: number };
  element?: ResolvedElement;
}) {
  picked.value = { point: bundle.point, element: bundle.element };
}

/** send POSTs the bundle to the one-time-token return endpoint, then closes the
 * window (best-effort) or shows the close-it-yourself fallback. */
async function send() {
  if (sending.value || sent.value) return;
  sending.value = true;
  errMsg.value = "";
  const token = pointToken();
  const visual: Record<string, unknown> = {
    ...(mediaHandle ? { media_handle: mediaHandle } : {}),
    ...(route ? { route } : {}),
    ...(tMs ? { t_ms: tMs } : {}),
    ...(picked.value?.point ? { point: picked.value.point } : {}),
    ...(picked.value?.element
      ? {
          element: {
            selector: picked.value.element.selector,
            role: picked.value.element.role,
            text: picked.value.element.text,
            bbox: [
              picked.value.element.bbox.x,
              picked.value.element.bbox.y,
              picked.value.element.bbox.width,
              picked.value.element.bbox.height,
            ],
          },
        }
      : {}),
  };
  // The question is appended to the route note so the oracle hears WHY the
  // operator pointed (the bundle carries WHERE).
  if (question.value.trim()) visual.route = `${route} — ${question.value.trim()}`;
  try {
    const resp = await fetch(
      `/point/return?token=${encodeURIComponent(token)}`,
      {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ visual }),
      },
    );
    if (!resp.ok) throw new Error(`return failed: ${resp.status}`);
    sent.value = true;
    // Best-effort close: browsers block programmatic close of tabs they didn't
    // script-open, so the "✓ sent" fallback (below) is what the operator sees
    // when this no-ops. The bundle has already returned regardless.
    window.close();
  } catch (e) {
    errMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    sending.value = false;
  }
}
</script>

<template>
  <div class="point-page" data-testid="point-page">
    <p class="pp-prompt" data-testid="pp-prompt">{{ prompt }}</p>

    <div class="pp-frame" data-testid="pp-frame">
      <img
        v-if="mediaHandle"
        class="pp-media"
        data-testid="pp-media"
        :src="mediaUrl()"
        alt="frame"
        @load="onLoadedImage"
      />
      <!-- Slice-2 picker, reused verbatim. Over the media (or the bare frame
           box when no media handle rode the request). -->
      <SpatialPicker
        :natural-width="frameNatural.width"
        :natural-height="frameNatural.height"
        :root="pickerRoot"
        @pick="onPick"
      />
    </div>

    <div class="pp-composer">
      <input
        v-model="question"
        class="pp-input"
        data-testid="pp-input"
        type="text"
        placeholder="why is this disabled here?"
        @keydown.enter="send"
      />
      <button
        class="pp-send"
        data-testid="pp-send"
        :disabled="sending || sent"
        @click="send"
      >
        Send
      </button>
    </div>

    <p v-if="sent" class="pp-sent" data-testid="pp-sent">
      ✓ sent — you can close this tab
    </p>
    <p v-if="errMsg" class="pp-error" data-testid="pp-error">{{ errMsg }}</p>
  </div>
</template>

<style scoped>
.point-page {
  max-width: 760px;
  margin: 1.5em auto;
  padding: 0 1em;
  display: flex;
  flex-direction: column;
  gap: 0.9em;
}
.pp-prompt {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.pp-frame {
  position: relative;
  min-height: 240px;
  border-radius: 8px;
  background: #0f172a;
}
.pp-media {
  display: block;
  width: 100%;
  border-radius: 8px;
}
.pp-composer {
  display: flex;
  gap: 0.5em;
}
.pp-input {
  flex: 1;
  padding: 0.55em 0.7em;
  border: 1px solid #cbd5e1;
  border-radius: 6px;
  font-size: 14px;
}
.pp-send {
  padding: 0.55em 1.1em;
  border: none;
  border-radius: 6px;
  background: #2563eb;
  color: #fff;
  font-weight: 600;
  cursor: pointer;
}
.pp-send:disabled {
  opacity: 0.5;
  cursor: default;
}
.pp-sent {
  margin: 0;
  color: #166534;
  font-weight: 600;
}
.pp-error {
  margin: 0;
  color: #b91c1c;
}
</style>
