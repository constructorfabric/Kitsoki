/**
 * WebMCP-shaped registry: the in-page control half of tour-player.
 * `start_guided_tour`/`abort_tour`/`list_tour_anchors` are exposed as
 * `navigator.modelContext` tools when the browser supports native WebMCP,
 * else via a postMessage transport compatible with the
 * @mcp-b/webmcp-polyfill shape — so an agent (or the same headless
 * chromium tools/browser-mcp drives) can push a tour or ask what a page's
 * anchors are without an extension. `registerAnchor` doubles the catalog
 * as the plan-against surface a tour generator (P5) reads.
 */
import { TourPlayer } from "./player.js";
import type { TargetBundle, TourManifestV2 } from "./types.js";

export interface AnchorCatalogEntry {
  name: string;
  bundle: TargetBundle;
  description?: string;
}

export interface WebMcpTool {
  name: string;
  description: string;
  inputSchema: Record<string, unknown>;
  execute: (args: unknown) => Promise<unknown> | unknown;
}

export class TourRegistry {
  private anchors = new Map<string, AnchorCatalogEntry>();
  private activePlayer: TourPlayer | null = null;

  registerAnchor(name: string, bundle: TargetBundle, description?: string): void {
    this.anchors.set(name, { name, bundle, description });
  }

  listAnchors(): AnchorCatalogEntry[] {
    return Array.from(this.anchors.values());
  }

  private tools(): WebMcpTool[] {
    return [
      {
        name: "start_guided_tour",
        description: "Start a click-through tour (a tour format v2 document) in this page.",
        inputSchema: { type: "object", properties: { tour: { type: "object" } }, required: ["tour"] },
        execute: (args) => {
          const { tour } = args as { tour: TourManifestV2 };
          this.activePlayer?.abort();
          this.activePlayer = new TourPlayer(tour);
          this.activePlayer.start();
          return { started: true };
        }
      },
      {
        name: "abort_tour",
        description: "Stop the active tour, if any.",
        inputSchema: { type: "object", properties: {} },
        execute: () => {
          this.activePlayer?.abort();
          this.activePlayer = null;
          return { aborted: true };
        }
      },
      {
        name: "list_tour_anchors",
        description: "List this app's registered anchor catalog, for a tour generator to plan against.",
        inputSchema: { type: "object", properties: {} },
        execute: () => this.listAnchors()
      }
    ];
  }

  /**
   * Registers the tool surface on navigator.modelContext when present
   * (native WebMCP, behind an opt-in flag until the origin trial
   * graduates), else installs a postMessage listener:
   *   in:  {type:"webmcp:invoke", tool, args, requestId}
   *   out: {type:"webmcp:result", requestId, result} | {..., error}
   */
  registerModelContext(): void {
    const nav = navigator as Navigator & { modelContext?: { registerTool: (t: WebMcpTool) => void } };
    if (nav.modelContext?.registerTool) {
      for (const tool of this.tools()) nav.modelContext.registerTool(tool);
      return;
    }
    const toolsByName = new Map(this.tools().map((t) => [t.name, t]));
    window.addEventListener("message", (event: MessageEvent) => {
      const data = event.data as { type?: string; tool?: string; args?: unknown; requestId?: unknown } | undefined;
      if (!data || data.type !== "webmcp:invoke" || typeof data.tool !== "string") return;
      const tool = toolsByName.get(data.tool);
      if (!tool) return;
      Promise.resolve(tool.execute(data.args))
        .then((result) => window.postMessage({ type: "webmcp:result", requestId: data.requestId, result }, "*"))
        .catch((err) => window.postMessage({ type: "webmcp:result", requestId: data.requestId, error: String(err) }, "*"));
    });
  }
}
