/**
 * Zod schema for the feature catalog (features/*.yaml at the repo root).
 *
 * The catalog is the single source of truth for every kitsoki feature: its
 * promo/docs metadata, its tour steps (from which src/tour/generated/*.ts are
 * code-generated), its demo recording binding, and its gated ui-qa scenarios.
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
  /** Playwright spec path, relative to tools/runstatus. */
  spec: z.string().min(1),
  /** Subdirectory of .artifacts/ the spec records into. */
  artifactDir: z.string().min(1),
  /** Base name passed to saveVideoAsMp4 → <artifactDir>/<videoBase>.mp4. */
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
   * Device profiles this demo records under (the camera registry ids). Defaults
   * to ["desktop"] — the canonical and ONLY enabled profile until a demo's UI is
   * responsive (mobile/tablet are a deliberate per-demo opt-in once breakpoints
   * land; recording a non-responsive demo at a narrow profile just yields a
   * shrunken desktop). Keep these ids in lockstep with PROFILES in
   * tests/playwright/_helpers/camera.ts.
   */
  profiles: z.array(z.enum(["desktop", "tablet", "mobile"])).nonempty().optional(),
});

export const QaScenarioSchema = z.strictObject({
  id: z.string().min(1),
  title: z.string().min(1),
  required: z.boolean(),
  /** Observable claims, judged frame-by-frame by the gated ui-qa pipeline. */
  steps: z.array(z.string().min(1)).min(1),
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
});

export type Feature = z.infer<typeof FeatureSchema>;
export type TourStepData = z.infer<typeof TourStepSchema>;

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

  for (const { file, feature: f } of features) {
    const stem = path.basename(file).replace(/\.ya?ml$/, "");
    if (stem !== f.id) {
      problems.push(`${file}: id "${f.id}" does not match filename stem "${stem}"`);
    }
    if (byId.has(f.id)) {
      problems.push(`${file}: duplicate feature id "${f.id}" (also in ${byId.get(f.id)})`);
    }
    byId.set(f.id, file);
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
    if (opts.skipPaths) continue;
    const mustExist: Array<[string, string]> = [];
    for (const d of f.docs ?? []) mustExist.push(["docs", d]);
    if (f.demo) {
      mustExist.push(["demo.spec", path.join("tools/runstatus", f.demo.spec)]);
      if (!f.demo.external) {
        if (f.demo.story) mustExist.push(["demo.story", f.demo.story]);
        if (f.demo.flow) mustExist.push(["demo.flow", f.demo.flow]);
        if (f.demo.hostCassette) mustExist.push(["demo.hostCassette", f.demo.hostCassette]);
      }
    }
    for (const [what, rel] of mustExist) {
      if (!fs.existsSync(path.join(repoRoot, rel))) {
        problems.push(`${file}: ${what} path "${rel}" does not exist`);
      }
    }
  }

  return problems;
}
