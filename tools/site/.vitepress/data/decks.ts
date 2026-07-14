/**
 * Build-time loader for the product site's Slidey deck gallery.
 *
 * Source decks live under docs/decks/. Their self-contained HTML bundles live
 * under docs/decks/bundled/ and are staged to src/public/deck-viewers/ by
 * scripts/stage-media.mjs. URLs returned here are site-absolute without the
 * VitePress base prefix; components apply withBase().
 */
import * as fs from "fs";
import * as path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
export const sourceSiteDir = path.resolve(process.env.KITSOKI_SITE_SOURCE_ROOT ?? path.resolve(__dirname, "../.."));
export const siteDir = path.resolve(process.env.KITSOKI_SITE_ROOT ?? sourceSiteDir);
export const repoRoot = path.resolve(process.env.KITSOKI_REPO_ROOT ?? path.resolve(sourceSiteDir, "../.."));
const decksRoot = path.join(repoRoot, "docs", "decks");
const bundledRoot = path.join(decksRoot, "bundled");
const publicViewerRoot = path.join(siteDir, "src", "public", "deck-viewers");

export interface DeckPreviewTheme {
  background: string;
  surface: string;
  text: string;
  subtle: string;
  accent: string;
  accent2: string;
}

export interface SiteDeck {
  id: string;
  title: string;
  eyebrow: string;
  subtitle: string;
  sceneCount: number;
  sourcePath: string;
  bundlePath: string;
  bundled: boolean;
  viewerUrl: string | null;
  previewTheme: DeckPreviewTheme;
}

function deckIdFor(name: string): string {
  return name.replace(/\.slidey\.json$/, "").replace(/\.json$/, "");
}

function textValue(value: unknown): string {
  if (Array.isArray(value)) return value.map(textValue).filter(Boolean).join(" ");
  if (typeof value === "string") return stripHtml(value);
  if (value == null) return "";
  return stripHtml(String(value));
}

function stripHtml(value: string): string {
  return value
    .replace(/<br\s*\/?>/gi, " ")
    .replace(/<[^>]*>/g, "")
    .replace(/\s+/g, " ")
    .trim();
}

function previewTheme(metaTheme: any): DeckPreviewTheme {
  const colors = metaTheme?.colors ?? {};
  return {
    background: metaTheme?.background ?? colors.base ?? "#1c140d",
    surface: colors.surface ?? colors.overlay ?? "#2a1a10",
    text: colors.text ?? "#f3ead8",
    subtle: colors.subtle ?? colors.muted ?? "#c1a188",
    accent: colors.gold ?? colors.accent ?? "#f4cf95",
    accent2: colors.rose ?? colors.accent2 ?? "#a8492b",
  };
}

export function loadDecks(): SiteDeck[] {
  if (!fs.existsSync(decksRoot)) return [];
  const files = fs
    .readdirSync(decksRoot)
    .filter((name) => name.endsWith(".json"))
    .sort();

  const decks = files.map((name): SiteDeck => {
    const sourceAbs = path.join(decksRoot, name);
    const spec = JSON.parse(fs.readFileSync(sourceAbs, "utf8"));
    const id = deckIdFor(name);
    const first = Array.isArray(spec.scenes) ? spec.scenes[0] ?? {} : {};
    const title = textValue(first.title) || textValue(spec.meta?.title) || id;
    const subtitle = textValue(first.subtitleHtml) || textValue(first.subtitle) || textValue(spec.meta?.description);
    const bundleName = `${id}.html`;
    const bundleAbs = path.join(bundledRoot, bundleName);
    const stagedBundleAbs = path.join(publicViewerRoot, bundleName);
    const bundled = fs.existsSync(bundleAbs) && fs.existsSync(stagedBundleAbs);

    return {
      id,
      title,
      eyebrow: textValue(first.eyebrow),
      subtitle,
      sceneCount: Array.isArray(spec.scenes) ? spec.scenes.length : 0,
      sourcePath: path.relative(repoRoot, sourceAbs),
      bundlePath: path.relative(repoRoot, bundleAbs),
      bundled,
      viewerUrl: bundled ? `/deck-viewers/${bundleName}?mode=present` : null,
      previewTheme: previewTheme(spec.meta?.theme),
    };
  });

  return decks.sort((a, b) => a.title.localeCompare(b.title));
}
