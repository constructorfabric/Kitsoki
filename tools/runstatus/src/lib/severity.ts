/**
 * Severity presentation for the web inbox — mirrors the TUI's severityGlyph
 * (internal/tui/inbox.go) so the browser and terminal agree visually.
 */

export type Severity =
  | "info"
  | "success"
  | "warn"
  | "error"
  | "action_required";

export function severityGlyph(sev: string): string {
  switch (sev) {
    case "success":
      return "✓";
    case "error":
      return "✗";
    case "warn":
      return "⚠";
    case "action_required":
      return "⋯";
    case "info":
      return "ℹ";
    default:
      return "·";
  }
}

export function severityColor(sev: string): string {
  switch (sev) {
    case "success":
      return "#22c55e"; // green
    case "error":
      return "#ef4444"; // red
    case "warn":
      return "#f59e0b"; // amber
    case "action_required":
      return "#fb923c"; // orange
    case "info":
      return "#3b82f6"; // blue
    default:
      return "#94a3b8";
  }
}

/** Compact relative time, e.g. "now", "2m", "5h", "3d". */
export function relativeTime(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  if (sec < 5) return "now";
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h`;
  const day = Math.floor(hr / 24);
  return `${day}d`;
}
