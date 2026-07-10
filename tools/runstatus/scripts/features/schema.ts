/**
 * Zod schema for the feature catalog (features/*.yaml at the repo root).
 *
 * The catalog is the single source of truth for every kitsoki feature: its
 * promo/docs metadata, its tour steps (from which src/tour/generated/*.ts are
 * code-generated), its demo capture binding, and its gated ui-qa scenarios.
 * generate.ts consumes this module; tests/unit/features-catalog.test.ts runs
 * every YAML file through it on each `pnpm test`.
 */
import * as fs from "fs";
import * as path from "path";
import { z } from "zod";

export const TourRouteSchema = z.enum(["home", "interactive", "any"]);
export const PlacementSchema = z.enum(["top", "bottom", "left", "right", "center"]);
export const AdvanceSchema = z.enum(["next", "click-target", "route-match"]);

/**
 * Mirrors src/tour/types.ts DriveAction — a single self-driving step action.
 * The `type` discriminator selects which of the other fields is required; the
 * cross-field requirement is enforced in FeatureSchema's superRefine (so the
 * shape stays JSON-Schema-representable). Mirror of internal/tour DriveAction.
 */
export const DriveActionSchema = z.strictObject({
  type: z.enum(["type-and-send", "click-intent", "wait-state", "reveal-turn", "dwell-ms"]),
  text: z.string().min(1).optional(),
  intent: z.string().min(1).optional(),
  state: z.string().min(1).optional(),
  ms: z.number().int().positive().optional(),
});

/**
 * Mirrors src/tour/types.ts TourStep — field for field, no renaming.
 * Cross-field step rules live in FeatureSchema's superRefine so this stays
 * JSON-Schema-representable for the generated feature.schema.json.
 */
export const TourStepSchema = z.strictObject({
  id: z.string().min(1),
  route: TourRouteSchema,
  target: z.string().min(1).optional(),
  targetText: z.string().min(1).optional(),
  title: z.string().min(1),
  body: z.string().min(1),
  placement: PlacementSchema,
  kind: z.enum(["explain", "action"]),
  advance: AdvanceSchema,
  advanceRoute: TourRouteSchema.optional(),
  waitForTarget: z.string().min(1).optional(),
  dwellMs: z.number().int().positive().optional(),
  drive: z.array(DriveActionSchema).optional(),
});

export const DemoSchema = z.strictObject({
  /** Capture backend. Playwright is the default; binary uses legacy `kitsoki tour`. */
  renderer: z.enum(["playwright", "binary"]).optional(),
  /**
   * Primary product-site media artifact. Defaults to rrweb; mp4 is an explicit
   * legacy fallback for surfaces rrweb cannot reconstruct or rendered-video
   * exports requested outside the normal catalog path.
   */
  format: z.enum(["mp4", "rrweb"]).optional(),
  /** Playwright spec path, relative to tools/runstatus. Optional ONLY for a
   *  product-tour whose legacy video is stitched from sections, not captured by
   *  a single spec (enforced in FeatureSchema's superRefine). */
  spec: z.string().min(1).optional(),
  /** Playwright capture spec for rrweb-first demos, relative to tools/runstatus.
   *  When omitted, rrweb capture uses `spec`. */
  rrwebSpec: z.string().min(1).optional(),
  /** Subdirectory of .artifacts/ the spec captures into. */
  artifactDir: z.string().min(1),
  /** Base artifact name: `<videoBase>.rrweb.json`/`.html`, or legacy `.mp4`. */
  videoBase: z.string().min(1),
  /** Step id whose NN-<id>.png screenshot is the poster frame. */
  posterStep: z.string().min(1).optional(),
  /** Informational, validated to exist: the story the demo drives. */
  story: z.string().min(1).optional(),
  flow: z.string().min(1).optional(),
  hostCassette: z.string().min(1).optional(),
  /**
   * The demo depends on paths outside this repo (e.g. a story in another
   * checkout) — path validation is skipped and record-demos excludes it.
   */
  external: z.boolean().optional(),
  /**
   * Device profiles this demo captures under (the camera registry ids). Defaults
   * to ["desktop"] — the canonical and ONLY enabled profile until a demo's UI is
   * responsive (mobile/tablet are a deliberate per-demo opt-in once breakpoints
   * land; capturing a non-responsive demo at a narrow profile just yields a
   * shrunken desktop). Keep these ids in lockstep with PROFILES in
   * tests/playwright/_helpers/camera.ts.
   */
  profiles: z.array(z.enum(["desktop", "tablet", "mobile"])).nonempty().optional(),
  /**
   * Renders this story-demo as an EMBEDDED Slidey deck clip instead of an mp4 —
   * for a rrweb-native tour whose demo.spec is a PERMANENT stub (see the spec
   * file's STATUS header: captured by a companion *-rrweb-capture.spec.ts,
   * never an mp4). `deck` is the repo-relative SOURCE deck spec (JSON) that
   * already carries this feature's clip as one scene; `rrweb` is that scene's
   * `rrweb` path exactly as written in the deck JSON, used to resolve the scene
   * index at codegen time (never hand-picked in YAML, so a reordered deck can't
   * silently point the page at the wrong scene — see findEmbedScene below).
   * The deck is bundled ONCE, offline (`slidey bundle <deck> <html>`), to
   * docs/decks/bundled/<deck-stem>.html — a committed, self-contained
   * interactive HTML asset (see embedHtmlPath) — and staged into the site
   * verbatim at build time; no slidey CLI is required in CI.
   */
  embed: z
    .strictObject({
      deck: z.string().min(1),
      rrweb: z.string().min(1),
    })
    .optional(),
});

export const QaScenarioSchema = z.strictObject({
  id: z.string().min(1),
  title: z.string().min(1),
  required: z.boolean(),
  /** Observable claims, judged frame-by-frame by the gated ui-qa pipeline. */
  steps: z.array(z.string().min(1)).min(1),
});

/** One source clip in a legacy product-tour section: a cataloged feature's video,
 *  optionally trimmed to a [startChapterId, endChapterId] window of its chapter
 *  sidecar (inclusive; omit = whole legacy video). Chapter ids are validated against
 *  the captured sidecar at STITCH time — the sidecar is a build artifact, not a
 *  catalog fact, so a catalog-time check would be a lie. */
export const SectionClipSchema = z.strictObject({
  source: z.string().min(1),
  chapters: z.tuple([z.string().min(1), z.string().min(1)]).optional(),
});

/** A product-tour section: one narrative beat (its own title card) stitched from
 *  one or more source clips. Most sections have a single clip; pt-author spans
 *  story-editor + meta-mode. The id is the chapter-rail group key (pt-<name>). */
export const SectionSchema = z.strictObject({
  id: z.string().regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/),
  title: z.string().min(1),
  body: z.string().min(1),
  clips: z.array(SectionClipSchema).min(1),
});

/**
 * Refinement-free shape (JSON-Schema-representable). FeatureSchema layers the
 * cross-field rules on top; feature.schema.json is generated from this one.
 */
export const FeatureObjectSchema = z.strictObject({
    /** kebab-case, unique, must match the filename stem. */
    id: z.string().regex(/^[a-z0-9]+(?:-[a-z0-9]+)*$/),
    /**
     * feature    — a product capability (promo feature grid + docs page)
     * product-tour — a cross-feature walkthrough of the product
     * story-demo — a showcase of one authored story
     */
    kind: z.enum(["feature", "product-tour", "story-demo"]),
    title: z.string().min(1),
    tagline: z.string().min(1),
    summary: z.string().min(1),
    /** Optional long-form markdown rendered on the feature's site page. */
    narrative: z.string().min(1).optional(),
    /** Present ⇒ the feature appears on the promo landing page. */
    promo: z
      .strictObject({
        order: z.number().int().nonnegative(),
        highlight: z.boolean().optional(),
      })
      .optional(),
    /** Repo-relative paths to deeper narrative docs (validated to exist). */
    docs: z.array(z.string().min(1)).optional(),
    /** Other feature ids (validated to resolve). */
    related: z.array(z.string().min(1)).optional(),
    demo: DemoSchema.optional(),
    /**
     * Master product-tour composition: ordered sections, each stitched from its
     * source clips into one chaptered film. Valid ONLY for kind: product-tour
     * (enforced in superRefine); each clip.source must resolve to a cataloged
     * feature with a demo (enforced in validateCatalog).
     */
    sections: z.array(SectionSchema).min(1).optional(),
    tour: z
      .strictObject({
        /** Generated const name, e.g. AGENT_ACTIONS_TOUR_STEPS. */
        export: z.string().regex(/^[A-Z][A-Z0-9_]*$/),
        steps: z.array(TourStepSchema).min(1),
      })
      .optional(),
    qa: z.strictObject({ scenarios: z.array(QaScenarioSchema).min(1) }).optional(),
});

export const FeatureSchema = FeatureObjectSchema.superRefine((f, ctx) => {
    if (f.tour && !f.demo) {
      ctx.addIssue({ code: "custom", message: `feature "${f.id}" has a tour but no demo binding` });
    }
    // sections are a product-tour-only composition (most product-tours are still
    // single captures — only a STITCHED one carries sections). A demo without
    // a capture spec is legal ONLY when its legacy video is stitched from sections.
    if (f.sections && f.kind !== "product-tour") {
      ctx.addIssue({ code: "custom", message: `feature "${f.id}" declares sections but kind is not product-tour` });
    }
    if (f.demo && !f.demo.spec && !f.sections && f.demo.renderer !== "binary") {
      ctx.addIssue({ code: "custom", message: `feature "${f.id}" demo needs a spec unless renderer is binary (only a sectioned product-tour stitches without one)` });
    }
    if (f.demo?.format === "rrweb" && !f.demo.rrwebSpec && !f.demo.spec) {
      ctx.addIssue({ code: "custom", message: `feature "${f.id}" rrweb demo needs demo.rrwebSpec or demo.spec` });
    }
    const secIds = new Set<string>();
    for (const s of f.sections ?? []) {
      if (secIds.has(s.id)) {
        ctx.addIssue({ code: "custom", message: `feature "${f.id}" repeats section id "${s.id}"` });
      }
      secIds.add(s.id);
    }
    const ids = new Set<string>();
    for (const s of f.tour?.steps ?? []) {
      if (ids.has(s.id)) {
        ctx.addIssue({ code: "custom", message: `feature "${f.id}" repeats step id "${s.id}"` });
      }
      ids.add(s.id);
      if (s.kind === "explain" && s.advance !== "next") {
        ctx.addIssue({ code: "custom", message: `explain step "${s.id}" must use advance: next` });
      }
      if (s.advance === "route-match" && !s.advanceRoute) {
        ctx.addIssue({ code: "custom", message: `route-match step "${s.id}" needs advanceRoute` });
      }
      if (s.advance === "click-target" && !s.target) {
        ctx.addIssue({ code: "custom", message: `click-target step "${s.id}" needs a target` });
      }
      for (const [j, d] of (s.drive ?? []).entries()) {
        const need = (field: string, ok: boolean) => {
          if (!ok)
            ctx.addIssue({
              code: "custom",
              message: `step "${s.id}" drive[${j}] type "${d.type}" requires "${field}"`,
            });
        };
        if (d.type === "type-and-send") need("text", !!d.text);
        if (d.type === "click-intent") need("intent", !!d.intent);
        if (d.type === "wait-state") need("state", !!d.state);
        if (d.type === "dwell-ms") need("ms", typeof d.ms === "number");
      }
    }
    if (f.demo?.posterStep && f.tour && !ids.has(f.demo.posterStep)) {
      ctx.addIssue({
        code: "custom",
        message: `feature "${f.id}" posterStep "${f.demo.posterStep}" is not a declared step id`,
      });
    }
    // Completeness: a promoted feature's grid card renders its demo media —
    // a promo entry with no demo binding ships an empty card.
    if (f.promo && !f.demo) {
      ctx.addIssue({
        code: "custom",
        message: `feature "${f.id}" is promoted (promo:) but has no demo binding — the promo card needs captured media`,
      });
    }
    // Completeness: a capturable, tour-bearing demo must name a deterministic
    // poster frame (a step id, validated above). Without one the feature page
    // and grid card fall back to a black first frame. Tourless demos
    // (harness-picker, meta-mode) and stitched product-tours are exempt.
    const capturable = f.demo && (f.demo.spec || f.demo.rrwebSpec) && !f.demo.external && !f.sections;
    if (capturable && f.tour && !f.demo!.posterStep) {
      ctx.addIssue({
        code: "custom",
        message: `feature "${f.id}" has a capturable tour demo but no demo.posterStep — pick a step id for the poster frame`,
      });
    }
});

export type Feature = z.infer<typeof FeatureSchema>;
export type TourStepData = z.infer<typeof TourStepSchema>;

/**
 * Resolve a `demo.embed` binding to a scene index by matching `rrweb` against
 * the SOURCE deck JSON's scenes — the index is always derived, never authored,
 * so a reordered/edited deck can't silently strand the embed on the wrong
 * scene. Shared by validateCatalog (existence/shape) and generate.ts's
 * buildDemoIndex (the computed `sceneIndex` the site consumes).
 */
export function findEmbedScene(
  repoRoot: string,
  embed: { deck: string; rrweb: string },
): { sceneIndex: number } | { error: string } {
  const deckPath = path.join(repoRoot, embed.deck);
  if (!fs.existsSync(deckPath)) return { error: `deck "${embed.deck}" does not exist` };
  let deck: { scenes?: Array<Record<string, unknown>> };
  try {
    deck = JSON.parse(fs.readFileSync(deckPath, "utf8"));
  } catch (e) {
    return { error: `deck "${embed.deck}" is not valid JSON: ${e instanceof Error ? e.message : e}` };
  }
  const idx = (deck.scenes ?? []).findIndex((s) => s.rrweb === embed.rrweb);
  if (idx < 0) return { error: `deck "${embed.deck}" has no scene with rrweb "${embed.rrweb}"` };
  return { sceneIndex: idx };
}

/**
 * docs/decks/<stem>.slidey.json → docs/decks/bundled/<stem>.html — the
 * committed, self-contained Slidey bundle a `demo.embed` serves. Bundled
 * offline (`slidey bundle`); see DemoSchema.embed's doc comment for why this
 * isn't rebuilt in CI.
 */
export function embedHtmlPath(deck: string): string {
  const stem = path
    .basename(deck)
    .replace(/\.slidey\.json$/, "")
    .replace(/\.json$/, "");
  return path.join(path.dirname(deck), "bundled", `${stem}.html`);
}

/**
 * Match a docs-manifest `from` pattern against a repo-relative path. The
 * manifest globs are deliberately simple — an exact path or a single `*`
 * standing in for one path segment (e.g. `docs/architecture/*.md`). We model
 * `*` as "no slash" so a glob never reaches into a subdirectory.
 */
function manifestMatch(glob: string, p: string): boolean {
  if (!glob.includes("*")) return glob === p;
  const rx = new RegExp(
    "^" + glob.split("*").map((s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("[^/]*") + "$",
  );
  return rx.test(p);
}

/**
 * Build the site's published-docs predicate from tools/site/docs-manifest.json
 * (the allowlist of repo docs copied into the site). Returns null when the
 * manifest is absent so the check is skipped rather than failing spuriously.
 * A `docs:` link under `docs/` that this predicate rejects renders on the site
 * as an external GitHub blob link, not an in-site page.
 */
function siteDocAllowlist(repoRoot: string): ((rel: string) => boolean) | null {
  const mp = path.join(repoRoot, "tools", "site", "docs-manifest.json");
  if (!fs.existsSync(mp)) return null;
  let froms: string[];
  try {
    type ManifestItem = { from?: string };
    type ManifestGroup = { items?: ManifestItem[] };
    type ManifestSection = { items?: ManifestItem[]; groups?: ManifestGroup[] };
    const collect = (items: ManifestItem[] | undefined): string[] =>
      (items ?? []).map((it) => it.from).filter(Boolean) as string[];
    const m = JSON.parse(fs.readFileSync(mp, "utf8")) as {
      sections?: ManifestSection[];
    };
    froms = (m.sections ?? []).flatMap((s) => [
      ...collect(s.items),
      ...(s.groups ?? []).flatMap((g) => collect(g.items)),
    ]);
  } catch {
    return null;
  }
  return (rel: string) => froms.some((g) => manifestMatch(g, rel));
}

/**
 * Cross-file catalog checks that one feature alone cannot express. Returns
 * human-readable problems (empty = valid). `repoRoot` grounds path-existence
 * checks; pass `skipPaths` to validate structure only (unit tests).
 */
export function validateCatalog(
  features: Array<{ file: string; feature: Feature }>,
  repoRoot: string,
  opts: { skipPaths?: boolean } = {},
): string[] {
  const problems: string[] = [];
  const byId = new Map<string, string>();
  const byExport = new Map<string, string>();
  const featById = new Map<string, Feature>();

  for (const { file, feature: f } of features) {
    const stem = path.basename(file).replace(/\.ya?ml$/, "");
    if (stem !== f.id) {
      problems.push(`${file}: id "${f.id}" does not match filename stem "${stem}"`);
    }
    if (byId.has(f.id)) {
      problems.push(`${file}: duplicate feature id "${f.id}" (also in ${byId.get(f.id)})`);
    }
    byId.set(f.id, file);
    featById.set(f.id, f);
    if (f.tour) {
      if (byExport.has(f.tour.export)) {
        problems.push(
          `${file}: duplicate tour export "${f.tour.export}" (also in ${byExport.get(f.tour.export)})`,
        );
      }
      byExport.set(f.tour.export, file);
    }
  }

  for (const { file, feature: f } of features) {
    for (const rel of f.related ?? []) {
      if (!byId.has(rel)) problems.push(`${file}: related id "${rel}" does not resolve`);
    }
    for (const s of f.sections ?? []) {
      for (const c of s.clips) {
        const src = featById.get(c.source);
        if (!src) {
          problems.push(`${file}: section "${s.id}" clip source "${c.source}" does not resolve to a feature`);
        } else if (!src.demo) {
          problems.push(`${file}: section "${s.id}" clip source "${c.source}" has no demo to stitch`);
        }
      }
    }
    if (opts.skipPaths) continue;
    // A `docs:` link under docs/ must be published by the site allowlist, else
    // it silently degrades to an external GitHub blob link instead of an in-site
    // page. Paths outside docs/ (e.g. stories/<name>/app.yaml) are deliberate
    // source links and are exempt.
    const allow = siteDocAllowlist(repoRoot);
    if (allow) {
      for (const d of f.docs ?? []) {
        if (d.startsWith("docs/") && !allow(d)) {
          problems.push(
            `${file}: docs link "${d}" is not in the site allowlist (tools/site/docs-manifest.json) — ` +
              `it would render as an external GitHub link, not an in-site page`,
          );
        }
      }
    }
    const mustExist: Array<[string, string]> = [];
    for (const d of f.docs ?? []) mustExist.push(["docs", d]);
    if (f.demo) {
      if (f.demo.spec) mustExist.push(["demo.spec", path.join("tools/runstatus", f.demo.spec)]);
      if (f.demo.rrwebSpec) mustExist.push(["demo.rrwebSpec", path.join("tools/runstatus", f.demo.rrwebSpec)]);
      if (!f.demo.external) {
        if (f.demo.story) mustExist.push(["demo.story", f.demo.story]);
        if (f.demo.flow) mustExist.push(["demo.flow", f.demo.flow]);
        if (f.demo.hostCassette) mustExist.push(["demo.hostCassette", f.demo.hostCassette]);
      }
      if (f.demo.embed) mustExist.push(["demo.embed.deck", f.demo.embed.deck]);
    }
    for (const [what, rel] of mustExist) {
      if (!fs.existsSync(path.join(repoRoot, rel))) {
        problems.push(`${file}: ${what} path "${rel}" does not exist`);
      }
    }
    if (f.demo?.embed) {
      const embed = f.demo.embed;
      if (fs.existsSync(path.join(repoRoot, embed.deck))) {
        const res = findEmbedScene(repoRoot, embed);
        if ("error" in res) problems.push(`${file}: demo.embed ${res.error}`);
        const html = embedHtmlPath(embed.deck);
        if (!fs.existsSync(path.join(repoRoot, html))) {
          problems.push(
            `${file}: demo.embed bundled html "${html}" does not exist — bundle it once: slidey bundle ${embed.deck} ${html}`,
          );
        }
      }
    }
  }

  return problems;
}
