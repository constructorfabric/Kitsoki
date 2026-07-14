/**
 * personas.ts — loads tools/product-journey/personas.json (the shared persona
 * catalogue product-journey already uses) and derives the swarm's PER-PERSONA
 * journey lens from it.
 *
 * Kept deliberately thin: the swarm's tier-1 users all drive the SAME
 * flow-fixture graph (one story, one --flow), so "derived from persona
 * lenses" means picking a small, meaningful behavioral difference per persona
 * rather than inventing a parallel journey catalogue (that would be tier-2/3
 * territory — free-text realism — not this tier). The one lens implemented
 * here is `watchesTrace`: a "terminal-first" persona additionally polls the
 * session trace RPC mid-journey (mirroring a user tailing logs instead of
 * only watching the rendered chat surface), which doubles as an extra live
 * assertion surface for that persona's isolation check.
 *
 * No external deps beyond node's fs/path — this file (and the rest of
 * tools/swarm/) is imported via relative path from
 * tools/runstatus/tests/playwright/swarm-replay-users.spec.ts, which is the
 * only place Playwright/axe-core need to resolve; keeping tools/swarm free of
 * npm dependencies means it never needs its own node_modules or package.json
 * (see tools/swarm/README.md "Why no package.json here").
 */
import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

/** tools/swarm -> repo root is two levels up. */
export const repoRoot = path.resolve(__dirname, "../..");
export const PERSONAS_PATH = path.join(repoRoot, "tools", "product-journey", "personas.json");

export interface Persona {
  id: string;
  label: string;
  description: string;
  surface_preference: string;
  risk_focus: string[];
}

/** Reads and validates the shared persona catalogue. Throws loudly on a
 *  malformed file — a swarm run over an empty/garbled catalogue would be a
 *  silent false green, not a real soak. */
export function loadPersonas(personasPath: string = PERSONAS_PATH): Persona[] {
  const raw = JSON.parse(fs.readFileSync(personasPath, "utf8")) as { personas?: Persona[] };
  if (!raw.personas || raw.personas.length === 0) {
    throw new Error(`personas.json at ${personasPath} has no personas`);
  }
  for (const p of raw.personas) {
    if (!p.id || !p.surface_preference) {
      throw new Error(`malformed persona entry (missing id/surface_preference): ${JSON.stringify(p)}`);
    }
  }
  return raw.personas;
}

/** Cycles the persona catalogue to cover N users, N possibly exceeding the
 *  catalogue size (the swarm wants 24+ concurrent users over a ~5-persona
 *  catalogue). Deterministic (index-based), not random, so a failing user
 *  index always reproduces the same persona assignment. */
export function personaForIndex(personas: Persona[], index: number): Persona {
  return personas[index % personas.length];
}

/** The one behavioral lens this tier derives from persona data: does this
 *  persona additionally watch the session via the trace RPC mid-journey? */
export function watchesTrace(persona: Persona): boolean {
  return persona.surface_preference.toLowerCase().includes("terminal");
}
