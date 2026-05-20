/**
 * PELLICULE — Frame Timing Configuration
 *
 * Each value is a frame count at the target FPS (default 30fps).
 * Increase any value to dwell longer on that animation step.
 * Rule of thumb: 30 frames = 1 second.
 */

const TIMING = {
  // ── Shared ──────────────────────────────────────────────────────────────
  inter_scene: 24,          // 0.8 s — brief blank between scenes
  title_card:  90,          // 3.0 s

  // ── API request scene (cyber-repo demo flow) ────────────────────────────
  scene_header:        60,  // 2.0 s
  request_url:         30,
  request_headers:     30,
  request_body:        60,
  sending_ticks:        5,
  sending_per_tick:    15,
  response_status:     75,  // 2.5 s
  response_headers:    30,
  response_body:      120,  // 4.0 s
  response_annotation: 75,
  complete_hold:      300,  // 10.0 s

  // ── Narrative scene (pitch) ─────────────────────────────────────────────
  narrative_eyebrow:   15,
  narrative_body:      30,
  narrative_lede:      20,
  narrative_hold:     120,  // 4.0 s default dwell

  // ── Diagram scene (pitch) ───────────────────────────────────────────────
  diagram_title:       20,
  diagram_panel_0:     30,
  diagram_panel_1:     30,
  diagram_panel_2:     30,
  diagram_caption:     30,
  diagram_hold:       180,  // 6.0 s default dwell

  // ── Terminal-gif scene (pitch) ──────────────────────────────────────────
  termgif_frame:       15,
  termgif_caption:     20,
  termgif_hold:       360,  // 12.0 s default — covers one gif loop

  // ── Stat scene (pitch) ──────────────────────────────────────────────────
  stat_value:          30,
  stat_label:          20,
  stat_detail:         15,
  stat_hold:          120,

  // ── CTA scene (pitch) ───────────────────────────────────────────────────
  cta_wordmark:        20,
  cta_tagline:         20,
  cta_url:             20,
  cta_hold:           180,  // 6.0 s

  // ── Diagram-SVG scene (pitch) ───────────────────────────────────────────
  diagramsvg_title:    20,
  diagramsvg_panel_0:  30,
  diagramsvg_panel_1:  30,
  diagramsvg_panel_2:  30,
  diagramsvg_caption:  30,
  diagramsvg_hold:    210,  // 7.0 s default dwell

  // ── Trace scene (pitch) ─────────────────────────────────────────────────
  trace_title:         20,
  trace_turn_0:        45,  // 1.5 s per turn — slow enough to read
  trace_turn_1:        45,
  trace_turn_2:        45,
  trace_caption:       30,
  trace_hold:         180,  // 6.0 s
};

module.exports = TIMING;
