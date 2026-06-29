#!/usr/bin/env node
// rrweb-scroll-scan.mjs — DETERMINISTIC (no-LLM) FOLLOWABILITY detector for
// embedded rrweb CONVERSATION clips.
//
// The companion `rrweb-pacing-scan.mjs` measures the TIME between content
// reveals — and is structurally blind to SCROLL. A captured conversation can
// space its reveals a comfortable 2.8s apart and still be unwatchable: the chat
// component (`ChatTranscript.vue`) snaps the scroller to the BOTTOM on every new
// message, so a reply taller than the fold renders with its opening lines (and a
// just-typed user input) already scrolled off-camera. The pacing scan green-lights
// it (the reveals ARE well-spaced in time); the viewer can read none of it.
//
// rrweb logs do NOT serialize node geometry (offsetTop isn't in the stream), so a
// structural scan can't know a reveal landed below the fold. But the defect has a
// clean STRUCTURAL SIGNATURE in the scroll-event stream that needs no geometry:
//
//   • A SNAP run  — the component's `scrollTop = scrollHeight`: a lone, ~instant
//     downward jump (n≤2 events, span < --snap-span ms, net downward delta ≥
//     --snap-min-dy px). Repeated snaps = an auto-scroll-to-bottom capture.
//   • An EASED run — the `revealTurn` choreography (ease the new input up, hold,
//     ease down through the reply): a dense requestAnimationFrame burst (≥
//     --ease-min-events events over ≥ --ease-min-ms ms). A followable conversation
//     eases roughly once per turn; an UPWARD ease (the input scrolled up to the
//     top) is its strongest fingerprint.
//
// Calibration (this repo): the dev-story dogfood captures — driven live, WITHOUT
// the revealTurn discipline — show 5–14 snap runs and 0–1 eased runs, 0 upward
// eases. The revealTurn-driven gears-bugfix capture shows 14 eased runs of 15. So
// the rule below cleanly separates them.
//
// FLAG (clip is snap-dominated / unfollowable) when:
//     snap_runs ≥ --min-snaps  AND  snap_runs > eased_runs
// A clip that barely scrolls (few runs, content fit the fold) is NOT flagged —
// the positive signal is repeated snapping, never the absence of easing.
//
// Usage:
//   rrweb-scroll-scan.mjs <clip.rrweb.json | dir> [--out scan.json]
//     [--scroll-id N] [--snap-span N] [--snap-min-dy N] [--ease-min-events N]
//     [--ease-min-ms N] [--run-gap N] [--min-snaps N] [--fail-on-find]
// Defaults: --snap-span 250 --snap-min-dy 40 --ease-min-events 6 --ease-min-ms 600
//           --run-gap 400 --min-snaps 3
// Exit: 0 = scanned OK (no flags, or advisory); 3 = flags AND --fail-on-find;
//       2 = usage/parse error.
//
// Advisory by default; qa.sh promotes to a hard gate under --scroll-strict — the
// same advisory/strict shape as blank-scan / pacing-scan / rrweb-pacing-scan.

import { readFileSync, writeFileSync, statSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

const argv = process.argv.slice(2);
if (!argv.length) usage('missing <clip.rrweb.json | dir>');
const src = argv[0];
const opt = {
  out: '', scrollId: null, snapSpan: 250, snapMinDy: 40,
  easeMinEvents: 6, easeMinMs: 600, runGap: 400, minSnaps: 3, failOnFind: false,
};
for (let i = 1; i < argv.length; i++) {
  const a = argv[i];
  const num = () => { const v = Number(argv[++i]); if (!Number.isFinite(v)) usage(`bad number for ${a}`); return v; };
  switch (a) {
    case '--out': opt.out = argv[++i]; break;
    case '--scroll-id': opt.scrollId = num(); break;
    case '--snap-span': opt.snapSpan = num(); break;
    case '--snap-min-dy': opt.snapMinDy = num(); break;
    case '--ease-min-events': opt.easeMinEvents = num(); break;
    case '--ease-min-ms': opt.easeMinMs = num(); break;
    case '--run-gap': opt.runGap = num(); break;
    case '--min-snaps': opt.minSnaps = num(); break;
    case '--fail-on-find': opt.failOnFind = true; break;
    default: usage(`unknown arg: ${a}`);
  }
}

function usage(msg) {
  process.stderr.write(`rrweb-scroll-scan: ${msg}\n` +
    'usage: rrweb-scroll-scan.mjs <clip.rrweb.json | dir> [--out f] [--scroll-id N] ' +
    '[--snap-span N] [--snap-min-dy N] [--ease-min-events N] [--ease-min-ms N] ' +
    '[--run-gap N] [--min-snaps N] [--fail-on-find]\n');
  process.exit(2);
}

function clipPaths(p) {
  let st;
  try { st = statSync(p); } catch { usage(`no such path: ${p}`); }
  if (st.isDirectory()) {
    return readdirSync(p).filter(f => f.endsWith('.rrweb.json')).sort().map(f => join(p, f));
  }
  return [p];
}

function loadEvents(path) {
  const raw = JSON.parse(readFileSync(path, 'utf8'));
  const events = Array.isArray(raw) ? raw : (raw && raw.events);
  if (!Array.isArray(events) || events.length < 2) throw new Error('not an rrweb event array');
  return events;
}

function scanClip(path) {
  const events = loadEvents(path);
  const t0 = events[0].timestamp;
  const durationMs = events[events.length - 1].timestamp - t0;

  // Scroll mutations: rrweb incremental, source 3 (IncrementalSource.Scroll),
  // shape {id,x,y}. A clip has several scrollable nodes (the page, the transcript,
  // an inner panel); rrweb does not tell us WHICH is the chat transcript by name.
  // So we classify EACH scrolled node independently and flag the clip if ANY node
  // is snap-dominated — the transcript is exactly the node that jumps to the
  // bottom on every message. (`--scroll-id` pins one node when known.)
  const all = events
    .filter(e => e.type === 3 && e.data && e.data.source === 3 && typeof e.data.y === 'number')
    .map(e => ({ ts: e.timestamp - t0, y: e.data.y, id: e.data.id }));
  const ids = opt.scrollId != null ? [opt.scrollId]
    : [...new Set(all.map(s => s.id))];

  // Classify one scroller node's scroll stream into snap vs eased runs. A SNAP is
  // an instant reposition: a run whose ENTRY JUMP (delta from the previous scroll
  // position, not the within-run delta) is a large downward leap, and which is not
  // itself a dense easing burst — i.e. `scrollTop = scrollHeight`. An EASED run is
  // a dense requestAnimationFrame burst (≥ --ease-min-events over ≥ --ease-min-ms).
  function classifyNode(id) {
    const scrolls = all.filter(s => s.id === id);
    const runs = [];
    let cur = null;
    for (const s of scrolls) {
      if (!cur || s.ts - cur.end > opt.runGap) { cur = { start: s.ts, end: s.ts, ys: [s.y] }; runs.push(cur); }
      else { cur.end = s.ts; cur.ys.push(s.y); }
    }
    let prevEndY = 0;
    let snaps = 0, eased = 0, upwardEases = 0;
    const snapAt = [];
    for (const r of runs) {
      const n = r.ys.length;
      const span = r.end - r.start;
      const entryJump = r.ys[0] - prevEndY;
      const withinUp = r.ys.some((y, i) => i > 0 && y < r.ys[i - 1]);
      const isEased = n >= opt.easeMinEvents && span >= opt.easeMinMs;
      if (isEased) { eased++; if (withinUp || entryJump < -opt.snapMinDy) upwardEases++; }
      else if (entryJump >= opt.snapMinDy) { snaps++; snapAt.push({ atMs: Math.round(r.start), jump: entryJump }); }
      prevEndY = r.ys[r.ys.length - 1];
    }
    return { id, scroll_events: scrolls.length, scroll_runs: runs.length, snaps, eased, upwardEases, snapAt };
  }

  const nodes = ids.map(classifyNode).sort((a, b) => b.snaps - a.snaps);
  const worst = nodes[0] || { snaps: 0, eased: 0, upwardEases: 0, scroll_events: 0, scroll_runs: 0, snapAt: [] };
  const flagged = worst.snaps >= opt.minSnaps && worst.snaps > worst.eased;

  return {
    clip: path,
    duration_ms: durationMs,
    scroller_nodes: nodes.length,
    snap_runs: worst.snaps,
    eased_runs: worst.eased,
    upward_eases: worst.upwardEases,
    worst_scroller_id: worst.id,
    flagged,
    issue: flagged
      ? `snap-to-bottom conversation capture — the transcript scroller jumps to the bottom ` +
        `${worst.snaps} time(s) (instant repositions) with ${worst.eased} eased reveal run(s) and ` +
        `${worst.upward_eases} upward eases. The chat snaps past each message instead of easing through ` +
        `it: user inputs and the tops of long replies flash off-camera. Re-render with the conversation ` +
        `reveal track (revealTurn / slidey rrweb-reveal).`
      : '',
    nodes,
  };
}

const results = [];
for (const p of clipPaths(src)) {
  try { results.push(scanClip(p)); }
  catch (e) { results.push({ clip: p, error: String(e.message || e), flagged: false }); }
}

const report = {
  min_snaps: opt.minSnaps,
  clips_scanned: results.length,
  unfollowable_clips: results.filter(r => r.flagged).length,
  clips: results,
};

const json = JSON.stringify(report, null, 2);
if (opt.out) writeFileSync(opt.out, json + '\n');
else process.stdout.write(json + '\n');

let flags = 0;
for (const r of results) {
  if (r.error) { process.stderr.write(`rrweb-scroll-scan: ${r.clip}: ${r.error}\n`); continue; }
  if (r.flagged) {
    flags++;
    process.stderr.write(`rrweb-scroll-scan: ${r.clip} — UNFOLLOWABLE: ${r.snap_runs} snap(s) / ` +
      `${r.eased_runs} eased, ${r.upward_eases} upward eases\n`);
  } else {
    process.stderr.write(`rrweb-scroll-scan: ${r.clip} — followable ` +
      `(${r.eased_runs} eased run(s), ${r.snap_runs} snap(s))\n`);
  }
}

if (flags > 0 && opt.failOnFind) process.exit(3);
process.exit(0);
