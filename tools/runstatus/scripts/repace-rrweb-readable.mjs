#!/usr/bin/env node
// Stretch a real rrweb session log into a human-readable demo playback.
//
// This preserves the captured DOM events but rewrites timestamps so meaningful
// content reveals stay on screen long enough to read, and large scroll jumps are
// replayed as paced incremental scrolls. The output is a derived evidence asset:
// keep the original rrweb capture beside it for provenance.

import { readFileSync, writeFileSync } from 'node:fs';

const argv = process.argv.slice(2);
if (argv.length < 2) usage('missing <input.rrweb.json> <output.rrweb.json>');

const input = argv[0];
const output = argv[1];
const opt = {
  minDwell: 1200,
  maxDwell: 3200,
  msPerChar: 18,
  coalesce: 150,
  sigMinAdds: 4,
  sigMinText: 24,
  scrollMsPerPx: 3.6,
  minScrollMs: 750,
  maxScrollMs: 5200,
  scrollStepPx: 140,
};

for (let i = 2; i < argv.length; i++) {
  const a = argv[i];
  const num = () => {
    const v = Number(argv[++i]);
    if (!Number.isFinite(v)) usage(`bad number for ${a}`);
    return v;
  };
  switch (a) {
    case '--min-dwell': opt.minDwell = num(); break;
    case '--max-dwell': opt.maxDwell = num(); break;
    case '--per-char': opt.msPerChar = num(); break;
    case '--coalesce': opt.coalesce = num(); break;
    case '--sig-min-adds': opt.sigMinAdds = num(); break;
    case '--sig-min-text': opt.sigMinText = num(); break;
    case '--scroll-ms-per-px': opt.scrollMsPerPx = num(); break;
    case '--min-scroll-ms': opt.minScrollMs = num(); break;
    case '--max-scroll-ms': opt.maxScrollMs = num(); break;
    case '--scroll-step-px': opt.scrollStepPx = num(); break;
    default: usage(`unknown arg: ${a}`);
  }
}

function usage(msg) {
  process.stderr.write(`repace-rrweb-readable: ${msg}\n` +
    'usage: repace-rrweb-readable.mjs <input.rrweb.json> <output.rrweb.json> [options]\n');
  process.exit(2);
}

function clone(v) {
  return JSON.parse(JSON.stringify(v));
}

function clamp(v, lo, hi) {
  return Math.max(lo, Math.min(hi, v));
}

function eventsOf(raw) {
  if (Array.isArray(raw)) return raw;
  if (raw && Array.isArray(raw.events)) return raw.events;
  throw new Error('not an rrweb event array or envelope');
}

function contentAdds(adds) {
  let nodes = 0;
  let maxText = 0;
  for (const a of adds || []) {
    const n = a && a.node;
    if (!n) continue;
    if (n.type === 2) nodes++;
    else if (n.type === 3) {
      nodes++;
      maxText = Math.max(maxText, String(n.textContent || '').trim().length);
    }
  }
  return { nodes, maxText };
}

function significantReveal(e) {
  if (e.type !== 3 || !e.data || e.data.source !== 0) return null;
  if (!Array.isArray(e.data.adds) || e.data.adds.length === 0) return null;
  const { nodes, maxText } = contentAdds(e.data.adds);
  if (nodes < opt.sigMinAdds && maxText < opt.sigMinText) return null;
  return { textLen: maxText };
}

function requiredDwell(textLen) {
  return Math.round(clamp(opt.minDwell + Math.max(0, textLen) * opt.msPerChar, opt.minDwell, opt.maxDwell));
}

function isScroll(e) {
  return e.type === 3 && e.data && e.data.source === 3 &&
    Number.isFinite(e.data.id) && Number.isFinite(e.data.x) && Number.isFinite(e.data.y);
}

function scrollDuration(px) {
  return Math.round(clamp(px * opt.scrollMsPerPx, opt.minScrollMs, opt.maxScrollMs));
}

const raw = JSON.parse(readFileSync(input, 'utf8'));
const events = eventsOf(raw);
if (events.length < 2) throw new Error('rrweb log needs at least two events');

const out = [];
const firstTs = events[0].timestamp;
let prevInTs = firstTs;
let prevOutTs = firstTs;
let lastReveal = null;
const lastScroll = new Map();
let insertedScrollEvents = 0;
let dwellExtensions = 0;
let scrollExtensions = 0;

function appendEvent(e, ts) {
  const next = clone(e);
  next.timestamp = Math.round(ts);
  out.push(next);
  prevOutTs = next.timestamp;
}

for (const event of events) {
  const inputGap = Math.max(0, event.timestamp - prevInTs);
  let outTs = prevOutTs + inputGap;

  const reveal = significantReveal(event);
  if (reveal) {
    if (lastReveal && outTs - lastReveal.atMs > opt.coalesce) {
      const minAt = lastReveal.atMs + requiredDwell(lastReveal.textLen);
      if (outTs < minAt) {
        outTs = minAt;
        dwellExtensions++;
      }
    }
  }

  if (isScroll(event)) {
    const key = event.data.id;
    const previous = lastScroll.get(key);
    if (previous) {
      const dx = event.data.x - previous.x;
      const dy = event.data.y - previous.y;
      const distance = Math.abs(dx) + Math.abs(dy);
      if (distance > opt.scrollStepPx) {
        const duration = scrollDuration(distance);
        const minAt = previous.outTs + duration;
        if (outTs < minAt) {
          outTs = minAt;
          scrollExtensions++;
        }

        const steps = Math.min(28, Math.max(2, Math.ceil(distance / opt.scrollStepPx)));
        for (let step = 1; step < steps; step++) {
          const f = step / steps;
          const intermediate = clone(event);
          intermediate.data.x = Math.round(previous.x + dx * f);
          intermediate.data.y = Math.round(previous.y + dy * f);
          intermediate.timestamp = Math.round(previous.outTs + (outTs - previous.outTs) * f);
          out.push(intermediate);
          insertedScrollEvents++;
        }
      }
    }
    lastScroll.set(key, { x: event.data.x, y: event.data.y, outTs });
  }

  appendEvent(event, outTs);

  if (reveal) {
    if (lastReveal && outTs - lastReveal.atMs <= opt.coalesce) {
      lastReveal.textLen = Math.max(lastReveal.textLen, reveal.textLen);
    } else {
      lastReveal = { atMs: outTs, textLen: reveal.textLen };
    }
  }
  prevInTs = event.timestamp;
}

const result = Array.isArray(raw) ? out : { ...raw, events: out };
writeFileSync(output, JSON.stringify(result, null, 2) + '\n');
writeFileSync(`${output}.repace.json`, JSON.stringify({
  input,
  output,
  source_event_count: events.length,
  output_event_count: out.length,
  inserted_scroll_events: insertedScrollEvents,
  dwell_extensions: dwellExtensions,
  scroll_extensions: scrollExtensions,
  original_duration_ms: events[events.length - 1].timestamp - events[0].timestamp,
  repaced_duration_ms: out[out.length - 1].timestamp - out[0].timestamp,
  options: opt,
}, null, 2) + '\n');

process.stderr.write(`repace-rrweb-readable: wrote ${output} (${events.length} -> ${out.length} events, ` +
  `${events[events.length - 1].timestamp - events[0].timestamp}ms -> ` +
  `${out[out.length - 1].timestamp - out[0].timestamp}ms)\n`);
