/**
 * Build-time loader joining the feature-catalog contract
 * (.vitepress/gen/features-index.json — emitted by `make site-data` from
 * features/*.yaml) with the staged media under src/public/media/. Used by the
 * VitePress config (themeConfig.features + sidebars) and the dynamic
 * /features/[id] route's paths loader. Pure fs — runs only at build time.
 *
 * URL convention: every URL returned here is site-absolute WITHOUT the base
 * prefix; components apply `withBase()`.
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";
// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-ignore — plain-node module shared with scripts/stage-docs.mjs
import { expandManifest } from "../../scripts/manifest.mjs";
import type { LocaleCode } from "./i18n.js";
import { prefixed } from "./i18n.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const sourceSiteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.resolve(__dirname, "../.."));
export const siteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? sourceSiteDir);
export const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.resolve(sourceSiteDir, "../.."));
const genIndex = path.join(siteDir, ".vitepress", "gen", "features-index.json");
const mediaRoot = path.join(siteDir, "src", "public", "media");
const i18nRoot = path.join(sourceSiteDir, "i18n");

export interface FeatureMedia {
  videoUrl: string | null;
  posterUrl: string | null;
  chaptersUrl: string | null;
  videoAvailable: boolean;
  embedKind: "deck" | "rrweb" | null;
  /** A rrweb-native story-demo: an embedded Slidey deck clip (see
   *  demo.embed in features/*.yaml), opened at its scene via `?scene=N`.
   *  A rrweb-first demo uses the per-feature staged `/media/<id>/demo.html`.
   *  Null unless the viewer html is staged. */
  embedUrl: string | null;
}

export interface SiteFeatureStep {
  id: string;
  title: string;
  body: string;
  shotUrl: string | null;
}

export interface SiteLink {
  text: string;
  href: string; // site-absolute (no base) or external URL
}

export interface SiteFeature {
  id: string;
  kind: "feature" | "product-tour" | "story-demo";
  title: string;
  tagline: string;
  summary: string;
  narrative: string | null;
  promo: { order: number; highlight?: boolean } | null;
  docLinks: SiteLink[];
  related: SiteLink[];
  media: FeatureMedia;
  steps: SiteFeatureStep[];
  demoSpec: string | null; // repo-relative playwright spec, for provenance
}

function firstHeading(absPath: string): string | null {
  if (!fs.existsSync(absPath)) return null;
  const m = fs.readFileSync(absPath, "utf8").match(/^#\s+(.+)$/m);
  return m ? m[1].trim() : null;
}

/** docs/<...>.md (repo path) → site URL + display text, per the allowlist. */
function docLink(repoPath: string, docMap: Map<string, string>, repoUrl: string, branch: string): SiteLink {
  const text = firstHeading(path.join(repoRoot, repoPath)) ?? path.basename(repoPath, ".md");
  const to = docMap.get(repoPath);
  if (to) {
    return { text, href: "/" + to.replace(/\.md$/, ".html").replace(/index\.html$/, "") };
  }
  return { text, href: `${repoUrl}/blob/${branch}/${repoPath}` };
}

type FeatureTranslation = Partial<
  Pick<SiteFeature, "title" | "tagline" | "summary" | "narrative"> & {
    steps: Array<Partial<Pick<SiteFeatureStep, "title" | "body">> & { id: string }>;
  }
>;

const cache = new Map<LocaleCode, SiteFeature[]>();

function readFeatureTranslation(locale: LocaleCode, id: string): FeatureTranslation {
  if (locale === "en") return {};
  const file = path.join(i18nRoot, locale, "features", `${id}.json`);
  if (!fs.existsSync(file)) return {};
  return JSON.parse(fs.readFileSync(file, "utf8")) as FeatureTranslation;
}

export function loadFeatures(locale: LocaleCode = "en"): SiteFeature[] {
  const cached = cache.get(locale);
  if (cached) return cached;
  if (!fs.existsSync(genIndex)) {
    throw new Error(
      `${path.relative(repoRoot, genIndex)} missing — run: make site-data (emits the feature catalog contract)`,
    );
  }
  const { repoUrl, branch, sections } = expandManifest(sourceSiteDir, repoRoot);
  const docMap = new Map<string, string>();
  for (const s of sections) for (const e of s.entries) docMap.set(e.from, e.to);

  const index = JSON.parse(fs.readFileSync(genIndex, "utf8")) as {
    features: Array<Record<string, any>>;
  };

  const titles = new Map(index.features.map((f) => [f.id as string, f.title as string]));

  const localized = index.features.map((f): SiteFeature => {
    const t = readFeatureTranslation(locale, f.id);
    const stepTranslations = new Map((t.steps ?? []).map((s) => [s.id, s]));
    const staged = path.join(mediaRoot, f.id);
    const hasVideo = fs.existsSync(path.join(staged, "demo.mp4"));
    const hasRrwebViewer = fs.existsSync(path.join(staged, "demo.html"));
    const hasPoster = fs.existsSync(path.join(staged, "poster.png"));
    const hasChapters = fs.existsSync(path.join(staged, "chapters.json"));
    const stepsDir = path.join(staged, "steps");
    const shots = fs.existsSync(stepsDir) ? fs.readdirSync(stepsDir) : [];

    // demo.embed: staged once, shared, under src/public/deck-viewers/ (not per-feature
    // media/<id>/ — several features can point at the same bundled deck).
    const embedRel = f.demo?.embed ? path.join("deck-viewers", path.basename(f.demo.embed.deckHtml)) : null;
    const hasEmbed = !!embedRel && fs.existsSync(path.join(siteDir, "src", "public", embedRel));
    const rrwebEmbedUrl = hasRrwebViewer ? `/media/${f.id}/demo.html` : null;
    const deckEmbedUrl = hasEmbed ? `/${embedRel}?scene=${f.demo.embed.sceneIndex}` : null;
    const embedUrl = deckEmbedUrl ?? rrwebEmbedUrl;

    const steps: SiteFeatureStep[] = (f.tour?.steps ?? []).map((s: Record<string, string>) => {
      const shot = shots.find((n) => n.endsWith(`-${s.id}.png`));
      const st = stepTranslations.get(s.id);
      return {
        id: s.id,
        title: st?.title ?? s.title,
        body: st?.body ?? s.body,
        shotUrl: shot ? `/media/${f.id}/steps/${shot}` : null,
      };
    });

    return {
      id: f.id,
      kind: f.kind,
      title: t.title ?? f.title,
      tagline: t.tagline ?? f.tagline,
      summary: t.summary ?? f.summary,
      narrative: t.narrative ?? f.narrative,
      promo: f.promo,
      docLinks: (f.docs ?? []).map((d: string) => docLink(d, docMap, repoUrl, branch)),
      related: (f.related ?? []).map((id: string) => ({
        text: titles.get(id) ?? id,
        href: `${prefixed(locale, `/features/${id}.html`)}`,
      })),
      media: {
        videoUrl: hasVideo ? `/media/${f.id}/demo.mp4` : null,
        posterUrl: hasPoster ? `/media/${f.id}/poster.png` : null,
        chaptersUrl: hasChapters ? `/media/${f.id}/chapters.json` : null,
        videoAvailable: hasVideo,
        embedKind: deckEmbedUrl ? "deck" : rrwebEmbedUrl ? "rrweb" : null,
        embedUrl,
      },
      steps,
      demoSpec: f.demo?.spec ?? null,
    };
  });
  cache.set(locale, localized);
  return localized;
}

const KIND_TITLES: Record<LocaleCode, Record<string, string>> = {
  en: {
    feature: "Features",
    "product-tour": "Product tours",
    "story-demo": "Story demos",
  },
  th: {
    feature: "ฟีเจอร์",
    "product-tour": "ทัวร์ผลิตภัณฑ์",
    "story-demo": "เดโม story",
  },
  ja: {
    feature: "機能",
    "product-tour": "製品ツアー",
    "story-demo": "Story デモ",
  },
};

/** Sidebar for /features/: grouped by kind, promo order first then title. */
export function featuresSidebar(locale: LocaleCode = "en") {
  const feats = loadFeatures(locale);
  const groups: Array<{ text: string; collapsed: boolean; items: Array<{ text: string; link: string }> }> = [];
  for (const kind of ["feature", "product-tour", "story-demo"] as const) {
    const items = feats
      .filter((f) => f.kind === kind)
      .sort((a, b) => (a.promo?.order ?? 999) - (b.promo?.order ?? 999) || a.title.localeCompare(b.title))
      .map((f) => ({ text: f.title, link: prefixed(locale, `/features/${f.id}`) }));
    if (items.length > 0) groups.push({ text: KIND_TITLES[locale][kind], collapsed: true, items });
  }
  const allFeatures = locale === "th" ? "ฟีเจอร์ทั้งหมด" : locale === "ja" ? "すべての機能" : "All features";
  return [{ text: allFeatures, link: prefixed(locale, "/features/") }, ...groups];
}

/** Sidebar for /guide/: the docs-manifest sections, titled by first heading. */
export function guideSidebar() {
  const { sections } = expandManifest(sourceSiteDir, repoRoot);
  const itemFor = (e: { from: string; to: string; title?: string }) => ({
    text: e.title ?? firstHeading(path.join(repoRoot, e.from)) ?? path.basename(e.from, ".md"),
    link: "/" + e.to.replace(/\.md$/, "").replace(/\/index$/, "/"),
  });

  return [
    { text: "Docs", link: "/guide/" },
    ...sections.map((s) => ({
      text: s.title,
      collapsed: !["Evaluate and install", "Architecture"].includes(s.title),
      items:
        s.groups?.length > 0
          ? [
              ...s.entries
                .filter((e) => !s.groups.some((g) => g.entries.some((ge) => ge.from === e.from)))
                .map(itemFor),
              ...s.groups.map((g) => ({
                text: g.title,
                collapsed: g.collapsed ?? true,
                items: g.entries.map(itemFor),
              })),
            ]
          : s.entries.map(itemFor),
    })),
  ];
}
