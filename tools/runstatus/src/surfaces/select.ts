/**
 * Surface selection (VS Code surface decomposition).
 *
 * Each surface — chat / trace / graph — can mount standalone inside its own
 * VS Code webview. The host picks which one by setting a plain-string global on
 * the page before the SPA boots:
 *
 *     window.__KITSOKI_SURFACE = "trace";
 *
 * A `?surface=trace` query param is honoured as a browser dev fallback so a
 * single surface can be inspected without the webview host. When neither selects
 * a valid surface, `resolveSurface()` returns null and the caller keeps the full
 * SPA (App + router) behavior unchanged.
 *
 * This contract (the global name + the literal values) is depended on by the
 * extension host — keep it exact.
 */

export type Surface = "chat" | "trace" | "graph";

const VALID: readonly Surface[] = ["chat", "trace", "graph"];

function isSurface(value: unknown): value is Surface {
  return typeof value === "string" && (VALID as readonly string[]).includes(value);
}

/**
 * Resolve the selected single surface, or null to keep the full SPA.
 * Precedence: the injected global wins; the `?surface=` query param is the
 * browser dev fallback.
 */
export function resolveSurface(): Surface | null {
  const injected = (
    window as Window & { __KITSOKI_SURFACE?: unknown }
  ).__KITSOKI_SURFACE;
  if (isSurface(injected)) return injected;

  const param = new URLSearchParams(location.search).get("surface");
  if (isSurface(param)) return param;

  return null;
}
