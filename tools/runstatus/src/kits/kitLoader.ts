// kitLoader.ts — the SPA-side runtime kit-module loader (S5,
// .context/kits-implementation-plan.md D3). Finishes the half of D3 that
// S3c's vertical slice deliberately deferred (see
// internal/runstatus/server/kit_ui.go's package doc): reading the
// server-injected installed-kit registry out of index.html and mounting each
// kit's UI entry as a real route, generically — nothing here names
// "object-graph" or any other specific kit.
//
// Deliberately scoped down from D3's most ambitious form (a shared Vue
// singleton via an import map + externalized vue in the SPA build — see
// kit-rpc.ts's doc comment in kits/object-graph/ui/src/ for exactly why):
// this SPA still bundles its own Vue via vite-plugin-singlefile, and a kit UI
// module bundles its OWN Vue too. What IS real: index.html injection (S3c),
// the /kit/<ns>/<kit>/ui/* asset route (S3c), and — new here — the runtime
// dynamic import + route registration + nav-link generation, all driven
// purely by the injected registry's data (kit.UIEntry's id/title/entry/nav).
//
// Contract a kit UI module's default export must satisfy:
//   { mount(el: HTMLElement, params: Record<string, string>): () => void }
// mount() renders into el (however the kit likes — its own Vue app, a raw
// DOM API, anything) and returns an unmount/cleanup function. KitPage.vue
// calls mount on activation and the returned cleanup on unmount/param change.
import type { Router } from "vue-router";
import KitPage from "./KitPage.vue";

export interface KitUIEntry {
  id: string;
  title?: string;
  entry: string;
  nav?: boolean;
}

export interface KitHeader {
  kit: string;
  namespace: string;
  version: string;
  title?: string;
  provides_stories?: string[];
  ui?: KitUIEntry[];
}

/** One installed kit UI page, resolved to a mountable route path + module URL. */
export interface KitNavLink {
  title: string;
  path: string;
}

const kitNavLinksInternal: KitNavLink[] = [];

/** Reactive-enough for a v-for in HomeView.vue: read once at module load
 * (the registry is injected server-side per page load, not live-updated —
 * a rescan needs a page reload, same as runstatus.kits.list's other
 * consumers). */
export const kitNavLinks: readonly KitNavLink[] = kitNavLinksInternal;

/** Reads the <script id="kitsoki-kits"> registry blob S3c's index.html
 * injection (internal/runstatus/server/kit_ui.go) writes. Returns [] when
 * absent — the common "no kits installed" case, or a non-browser (SSR/test)
 * environment. */
export function readInstalledKits(): KitHeader[] {
  if (typeof document === "undefined") return [];
  const el = document.getElementById("kitsoki-kits");
  if (!el || !el.textContent) return [];
  try {
    const parsed = JSON.parse(el.textContent) as KitHeader[];
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

/** kitPagePath is the route path a kit UI entry mounts at:
 * /kit/<namespace>/<kit>/<entry-id>. Exported so KitPage.vue and this file
 * agree on the shape without duplicating the format string. */
export function kitPagePath(kit: KitHeader, entry: KitUIEntry): string {
  return `/kit/${kit.namespace}/${kit.kit}/${entry.id}`;
}

/** kitModuleURL is the served URL for a kit UI entry's built module,
 * matching internal/runstatus/server/kit_ui.go's handleKitUI route. */
export function kitModuleURL(kit: KitHeader, entry: KitUIEntry): string {
  return `/kit/${kit.namespace}/${kit.kit}/ui/${entry.entry}.mjs`;
}

/** installKitRoutes reads the injected registry and calls
 * router.addRoute() for every provides.ui entry across every installed kit
 * — generic over however many kits are installed, and over whatever their
 * entries are named. Each route's component is the shared KitPage.vue,
 * parameterized via route.meta.moduleUrl; KitPage does the actual dynamic
 * import + mount(). Also populates kitNavLinks with the nav:true subset for
 * a generic "Kits" nav section (HomeView.vue renders it, unaware of which
 * kits are actually installed). No-op (empty routes/nav) when no kits are
 * installed — the common case today. */
export function installKitRoutes(router: Router): void {
  const kits = readInstalledKits();
  for (const kit of kits) {
    for (const entry of kit.ui ?? []) {
      const path = kitPagePath(kit, entry);
      router.addRoute({
        path,
        component: KitPage,
        meta: { moduleUrl: kitModuleURL(kit, entry) },
      });
      if (entry.nav) {
        kitNavLinksInternal.push({ title: entry.title || entry.id, path });
      }
    }
  }
}
