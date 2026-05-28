<template>
  <div class="unified-diff">
    <template v-for="(block, bi) in parsed" :key="bi">
      <div class="unified-diff__hunk-header">{{ block.header }}</div>
      <div
        v-for="(line, li) in block.lines"
        :key="li"
        class="unified-diff__line"
        :class="lineClass(line)"
      ><span class="unified-diff__gutter">{{ lineGutter(line) }}</span><span class="unified-diff__body">{{ lineBody(line) }}</span></div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";

interface HunkBlock {
  header: string;
  lines: string[];
}

const props = defineProps<{ diff: string }>();

const parsed = computed<HunkBlock[]>(() => {
  const blocks: HunkBlock[] = [];
  let current: HunkBlock | null = null;

  for (const raw of props.diff.split("\n")) {
    // Skip file-header lines (--- a/... or +++ b/...)
    if (/^(---|\+\+\+) /.test(raw)) continue;

    if (raw.startsWith("@@")) {
      if (current) blocks.push(current);
      current = { header: raw, lines: [] };
    } else if (current) {
      current.lines.push(raw);
    }
    // Lines before first @@ hunk are ignored (file-header context)
  }
  if (current) blocks.push(current);
  return blocks;
});

function lineClass(line: string): string {
  if (line.startsWith("+")) return "diff-add";
  if (line.startsWith("-")) return "diff-del";
  return "diff-ctx";
}

function lineGutter(line: string): string {
  if (line.startsWith("+")) return "+";
  if (line.startsWith("-")) return "-";
  return " ";
}

function lineBody(line: string): string {
  // Strip the leading sigil so the content column aligns
  return line.length > 0 ? line.slice(1) : "";
}
</script>

<style scoped>
.unified-diff {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  line-height: 1.45;
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
  overflow-x: auto;
}

.unified-diff__hunk-header {
  background: #0f1e38;
  color: #60a5fa;
  padding: 0.15rem 0.5rem;
  border-bottom: 1px solid #1e293b;
  font-style: italic;
}

.unified-diff__line {
  display: flex;
  white-space: pre;
}

.unified-diff__gutter {
  width: 1.2rem;
  flex-shrink: 0;
  text-align: center;
  user-select: none;
}

.unified-diff__body {
  flex: 1;
  padding-right: 0.5rem;
}

.diff-add {
  background: #052e16;
  color: #86efac;
}

.diff-del {
  background: #2d0707;
  color: #fca5a5;
}

.diff-ctx {
  color: #94a3b8;
}
</style>
