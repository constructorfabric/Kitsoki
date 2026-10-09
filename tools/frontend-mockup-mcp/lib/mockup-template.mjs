// Mockup HTML template generator (contract §7 item 2:
// ~/code/POG/.context/mockup-demo-tooling-contract.md). Renders a scenario
// spec (see scenario-spec.mjs) into a fully self-contained mockup HTML:
// five stable zones (rail / intake / graph / inspector / timeline), stable
// `data-testid`s, a `const states = {...}` data block (delimited so it's
// refreshable), and the `window.storyboard.setStep(id)` contract.
//
// Deliberately generalizes the gravytanker portal mockup
// (~/code/POG/.context/gravytanker-portal-mockup.html) rather than copying
// it: layout/CSS is trimmed to a dependency-free dark theme that scales to
// an arbitrary number of intake fields, metrics, evidence rows, and
// timeline steps, and the generated `render(key)` function ONLY ever
// touches `document.getElementById(id).textContent/.innerHTML` and
// `document.body.setAttribute(...)` -- no `querySelectorAll`, no class-list
// toggling on repeated nodes -- so it stays exercisable by a tiny in-memory
// DOM-lite stub in tests, with no real browser required.

export const STATES_BEGIN = "<!-- mockup:states:begin -->";
export const STATES_END = "<!-- mockup:states:end -->";
export const GRAPH_RENDERER_BEGIN = "<!-- graph-projection:renderer:begin -->";
export const GRAPH_RENDERER_END = "<!-- graph-projection:renderer:end -->";
export const GRAPH_DATA_BEGIN = "<!-- graph-projection:data:begin -->";
export const GRAPH_DATA_END = "<!-- graph-projection:data:end -->";

function escapeHtml(value) {
  return String(value ?? "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;"
  })[ch]);
}

function intakeFieldsHtml(fields) {
  return fields
    .map(
      (label, i) =>
        `<div class="field" data-testid="intake-field-${i}"><label>${escapeHtml(label)}</label><div id="intake-field-${i}">—</div></div>`
    )
    .join("\n      ");
}

function css() {
  return `
    :root {
      color-scheme: dark;
      --bg: #06101d;
      --panel: #0d1a2b;
      --line: #263f61;
      --text: #f7fbff;
      --muted: #aebed6;
      --blue: #38bdf8;
      --cyan: #67e8f9;
      --green: #34d399;
      --gold: #fbbf24;
      --rose: #fb7185;
    }
    * { box-sizing: border-box; }
    html, body { margin: 0; min-height: 100%; background: var(--bg); color: var(--text); font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .app { max-width: 1600px; margin: 0 auto; display: grid; grid-template-columns: 260px 1fr 360px; grid-template-rows: auto auto 1fr auto; gap: 14px; padding: 18px; }
    header, aside, main, section, footer { border: 1px solid rgba(125, 211, 252, 0.18); background: linear-gradient(180deg, rgba(15,27,45,0.96), rgba(9,18,31,0.96)); border-radius: 8px; }
    header { grid-column: 1 / 4; padding: 16px 22px; display: flex; align-items: baseline; gap: 16px; }
    header h1 { margin: 0; font-size: 26px; }
    header .tag { color: var(--muted); font-size: 14px; }
    h2 { margin: 0 0 8px; color: var(--cyan); font-size: 12px; text-transform: uppercase; letter-spacing: .14em; }
    aside.rail { grid-row: 2 / 5; padding: 16px; display: flex; flex-direction: column; gap: 16px; overflow: auto; }
    .scenario { border: 1px solid rgba(184,194,216,0.18); border-left: 4px solid var(--blue); border-radius: 6px; padding: 10px; }
    .scenario strong { display: block; font-size: 15px; }
    .scenario span { display: block; margin-top: 4px; color: var(--muted); font-size: 12px; }
    .chips { display: flex; flex-wrap: wrap; gap: 6px; }
    .chip { border: 1px solid rgba(184,194,216,0.2); border-radius: 999px; padding: 4px 9px; font-size: 11px; background: rgba(6,16,29,0.6); }
    .active-decision { border-color: var(--gold); }
    section.intake { grid-column: 2; padding: 14px 16px; display: flex; flex-wrap: wrap; gap: 10px; align-items: flex-start; }
    section.intake h2 { flex-basis: 100%; }
    .field { flex: 1 1 160px; border: 1px solid rgba(125,211,252,0.18); background: rgba(6,16,29,0.5); border-radius: 6px; padding: 8px 10px; min-width: 140px; }
    .field label { display: block; color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: .1em; }
    .field div { margin-top: 6px; font-size: 16px; font-weight: 700; }
    main.graph { grid-column: 2; grid-row: 3; position: relative; overflow: hidden; padding: 0; min-height: 340px; }
    .graph-head { position: absolute; left: 18px; top: 14px; z-index: 5; }
    .graph-title { font-size: 22px; font-weight: 800; }
    .graph-sub { margin-top: 4px; color: var(--muted); font-size: 13px; }
    main.graph svg { position: absolute; inset: 64px 8px 8px 8px; width: calc(100% - 16px); height: calc(100% - 72px); }
    section.inspector { grid-column: 3; grid-row: 2 / 4; padding: 14px; display: flex; flex-direction: column; gap: 8px; overflow: auto; }
    .headline { font-size: 19px; font-weight: 800; }
    .meaning { color: #eaf2ff; font-size: 13px; }
    .metric-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; }
    .metric { border: 1px solid rgba(184,194,216,0.18); background: rgba(6,16,29,0.5); border-radius: 6px; padding: 6px 8px; }
    .metric label { display: block; color: var(--muted); font-size: 10px; text-transform: uppercase; }
    .metric strong { display: block; margin-top: 3px; font-size: 14px; }
    .action { margin-top: auto; border: 1px solid rgba(52,211,153,0.42); background: rgba(15,63,54,0.5); border-radius: 6px; padding: 9px 11px; font-weight: 700; }
    .evidence-list { display: grid; gap: 7px; }
    .evidence { display: grid; grid-template-columns: 14px 1fr auto; gap: 8px; align-items: center; border: 1px solid rgba(184,194,216,0.18); border-radius: 6px; padding: 6px 8px; font-size: 12px; background: rgba(6,16,29,0.45); }
    .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--gold); }
    .dot.green { background: var(--green); }
    .dot.red { background: var(--rose); }
    footer.timeline { grid-column: 2 / 4; grid-row: 4; padding: 14px 16px; }
    .steps { display: flex; flex-wrap: wrap; gap: 8px; }
    .step { flex: 1 1 120px; border: 1px solid rgba(184,194,216,0.18); border-radius: 6px; padding: 8px; background: rgba(6,16,29,0.5); }
    .step.active { border-color: var(--gold); }
    .step.done { border-color: rgba(52,211,153,0.56); background: rgba(15,63,54,0.34); }
    .step b { display: block; font-size: 12px; }
    .step span { display: block; margin-top: 4px; color: var(--muted); font-size: 10px; }
`;
}

function railHtml(rail) {
  const heading = rail.heading || "Scenarios";
  const typeHeading = rail.typeHeading || "Types";
  return `<aside class="rail" data-testid="left-rail">
      <h2>${escapeHtml(heading)}</h2>
      <div class="chips" id="scenario-list"></div>
      <h2>${escapeHtml(typeHeading)}</h2>
      <div class="chips" id="type-chips"></div>
      <div class="scenario active-decision" data-testid="active-decision">
        <strong id="decision-card"></strong>
        <span id="decision-detail"></span>
      </div>
      <span id="rail-active-index" hidden></span>
    </aside>`;
}

function intakeHtml(intake) {
  const heading = intake.heading || "Intake";
  const fields = intake.fields || [];
  return `<section class="intake" data-testid="intake-strip">
      <h2>${escapeHtml(heading)}</h2>
      ${intakeFieldsHtml(fields)}
    </section>`;
}

function graphHtml() {
  return `<main class="graph" data-testid="graph-canvas">
      <div class="graph-head">
        <div class="graph-title" id="graph-title"></div>
        <div class="graph-sub" id="graph-sub"></div>
      </div>
      <svg id="graph-svg" viewBox="0 0 900 600" role="img" aria-label="scenario graph"></svg>
    </main>`;
}

function inspectorHtml(inspector) {
  const heading = inspector.heading || "Inspector";
  const evidenceHeading = inspector.evidenceHeading || "Evidence";
  return `<section class="inspector" data-testid="inspector">
      <h2>${escapeHtml(heading)}</h2>
      <div class="headline" id="inspector-headline"></div>
      <div class="meaning" id="inspector-meaning"></div>
      <div class="metric-grid" id="metrics"></div>
      <h2>${escapeHtml(evidenceHeading)}</h2>
      <div class="evidence-list" id="evidence-list" data-testid="evidence-list"></div>
      <div class="action" id="next-action" data-testid="next-action"></div>
    </section>`;
}

function timelineHtml(timeline) {
  const heading = timeline.heading || "Timeline";
  return `<footer class="timeline" data-testid="timeline">
      <h2>${escapeHtml(heading)}</h2>
      <div class="steps" id="timeline-steps"></div>
    </footer>`;
}

/** JS source of the generated `render(key)` app script (everything except
 * the delimited `states` data block, which is emitted separately so it can
 * be found/refreshed independently -- see STATES_BEGIN/STATES_END). */
function appScript(scenario, firstStateKey) {
  const rail = scenario.raw.zones.rail || {};
  const scenarios = JSON.stringify(rail.scenarios || []);
  const types = JSON.stringify(rail.types || []);
  return `<script>
    var SCENARIOS = ${scenarios};
    var TYPE_CHIPS = ${types};

    function mockupEl(id) { return document.getElementById(id); }
    function mockupSetText(id, value) { var n = mockupEl(id); if (n) n.textContent = value == null ? '' : value; }
    function mockupSetHTML(id, value) { var n = mockupEl(id); if (n) n.innerHTML = value; }

    function mockupRenderRail() {
      mockupSetHTML('scenario-list', SCENARIOS.map(function (s) {
        return '<span class="chip">' + s[0] + '</span>';
      }).join(''));
      mockupSetHTML('type-chips', TYPE_CHIPS.map(function (t) {
        return '<span class="chip">' + t + '</span>';
      }).join(''));
    }

    function mockupDrawGraph(stateId) {
      var svg = mockupEl('graph-svg');
      if (!svg || !stateId) return;
      if (window.__GRAPH_PROJECTION__ && typeof window.renderGraphProjection === 'function') {
        window.renderGraphProjection(svg, window.__GRAPH_PROJECTION__, stateId);
      }
    }

    function render(key) {
      var s = states[key];
      if (!s) return;
      var rail = s.rail || {};
      var intake = s.intake || [];
      var graph = s.graph || {};
      var insp = s.inspector || {};
      var timeline = s.timeline || {};

      mockupSetText('rail-active-index', String(rail.active || 0));
      mockupSetText('decision-card', rail.decision || '');
      mockupSetText('decision-detail', rail.detail || '');

      for (var i = 0; i < intake.length; i++) mockupSetText('intake-field-' + i, intake[i]);

      mockupSetText('graph-title', graph.title || '');
      mockupSetText('graph-sub', graph.sub || '');

      mockupSetText('inspector-headline', insp.headline || '');
      mockupSetText('inspector-meaning', insp.meaning || '');
      mockupSetText('next-action', insp.action || '');
      mockupSetHTML('metrics', (insp.metrics || []).map(function (m) {
        return '<div class="metric"><label>' + m[0] + '</label><strong>' + m[1] + '</strong></div>';
      }).join(''));
      mockupSetHTML('evidence-list', (insp.evidence || []).map(function (e) {
        return '<div class="evidence"><span class="dot ' + (e[0] || '') + '"></span><span>' + e[1] + '</span><small>' + (e[2] || '') + '</small></div>';
      }).join(''));

      var steps = timeline.steps || [];
      var done = timeline.done || 0;
      mockupSetHTML('timeline-steps', steps.map(function (t, i) {
        var cls = i < done ? 'done' : (i === done ? 'active' : '');
        var status = i < done ? 'closed' : (i === done ? 'active' : 'waiting');
        return '<div class="step ' + cls + '"><b>' + t + '</b><span>' + status + '</span></div>';
      }).join(''));

      mockupDrawGraph(graph.state);
      document.body.setAttribute('data-state', key);
    }

    mockupRenderRail();
    window.storyboard = { setStep: render };
    render(${JSON.stringify(firstStateKey)});
  </script>`;
}

function statesScript(scenario) {
  const body = `const states = ${JSON.stringify(scenario.raw.states, null, 2)};`;
  return `${STATES_BEGIN}\n<script>\n  ${body}\n</script>\n${STATES_END}`;
}

/**
 * Build the full self-contained mockup HTML for a scenario. Does NOT wire
 * the graph-projection renderer/data blocks -- that's the caller's job
 * (create-mockup.mjs), either by shelling out to slidey's own
 * tools/graph-projection-sync.js (kept as the single source of truth for
 * that block format) or via injectGraphProjectionBlocks() below when a
 * `--renderer` file is supplied instead of a live slidey checkout.
 */
export function buildMockupHtml(scenario) {
  const raw = scenario.raw;
  const zones = raw.zones || {};
  const firstStateKey = scenario.stateKeys[0];
  const title = raw.title;
  const tagline = raw.tagline || "";

  return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHtml(title)}</title>
  <style>${css()}</style>
</head>
<body>
  <div class="app" data-testid="portal">
    <header data-testid="header">
      <h1>${escapeHtml(title)}</h1>
      ${tagline ? `<div class="tag">${escapeHtml(tagline)}</div>` : ""}
    </header>
    ${railHtml(zones.rail || {})}
    ${intakeHtml(zones.intake || {})}
    ${graphHtml()}
    ${inspectorHtml(zones.inspector || {})}
    ${timelineHtml(zones.timeline || {})}
  </div>

  ${statesScript(scenario)}

  ${appScript(scenario, firstStateKey)}
</body>
</html>
`;
}

/**
 * Insert (or refresh) the graph-projection renderer + data blocks directly,
 * mirroring slidey's tools/graph-projection-sync.js marker format and
 * insertion point (right before the mockup's first non-projection
 * `<script>` tag) so a mockup built this way is byte-compatible with one
 * synced by the real slidey tool. Used for the `--renderer <file>` path
 * (tests; also usable when no slidey checkout is resolvable).
 */
export function injectGraphProjectionBlocks(html, rendererSrc, projectionJson) {
  const rendererInner = `<script>\n${rendererSrc.trimEnd()}\n</script>`;
  const dataInner = `<script>\n  window.__GRAPH_PROJECTION__ = ${JSON.stringify(projectionJson, null, 2)};\n</script>`;

  const firstScriptIdx = (() => {
    const m = /<script[\s>]/i.exec(html);
    return m ? m.index : html.length;
  })();

  const before = html.slice(0, firstScriptIdx);
  const after = html.slice(firstScriptIdx);
  const indent = before.match(/[ \t]*$/)[0];

  return (
    before +
    `${GRAPH_RENDERER_BEGIN}\n${rendererInner}\n${GRAPH_RENDERER_END}${GRAPH_DATA_BEGIN}\n${dataInner}\n${GRAPH_DATA_END}\n` +
    indent +
    after
  );
}
