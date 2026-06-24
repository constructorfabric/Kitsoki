/**
 * Embed-host detection.
 *
 * The SPA renders a chat-dominant "embed" layout (a hint rail for trace + graph)
 * ONLY when it runs inside the VS Code webview; the standalone browser app keeps
 * its full multi-pane layout untouched. Detection mirrors the BridgeTransport
 * host check (transport/transport.ts): the webview preload defines a global
 * `acquireVsCodeApi`, the browser never does.
 *
 * `?embed=1` also forces it on so the embed layout can be demoed/inspected in a
 * plain browser. `setEmbeddedOverride()` lets unit tests pin the value without
 * touching globals.
 */

let override: boolean | null = null;

/** Pin isEmbedded()'s result (tests). Pass null to fall back to detection. */
export function setEmbeddedOverride(value: boolean | null): void {
  override = value;
}

/** True when the SPA is hosted inside the VS Code webview (or forced via query). */
export function isEmbedded(): boolean {
  if (override !== null) return override;
  return (
    typeof (globalThis as Record<string, unknown>)["acquireVsCodeApi"] === "function"
  );
}
