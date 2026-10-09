import { nextTick } from "vue";
import { inBrowser } from "vitepress";
import type mermaidAPI from "mermaid";

let mermaidPromise: Promise<typeof mermaidAPI> | null = null;
let renderPass = 0;

function cssVar(name: string, fallback: string): string {
  if (!inBrowser) return fallback;
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value || fallback;
}

function loadMermaid(): Promise<typeof mermaidAPI> {
  if (!mermaidPromise) {
    mermaidPromise = import("mermaid").then((module) => module.default);
  }
  return mermaidPromise;
}

function configureMermaid(mermaid: typeof mermaidAPI): void {
  const bg = cssVar("--vp-c-bg", "#ffffff");
  const bgSoft = cssVar("--vp-c-bg-soft", "#f6f6f7");
  const bgAlt = cssVar("--vp-c-bg-alt", "#f6f6f7");
  const text = cssVar("--vp-c-text-1", "#213547");
  const textMuted = cssVar("--vp-c-text-2", "#476582");
  const border = cssVar("--vp-c-divider", "#dcdfe6");
  const brand = cssVar("--vp-c-brand-1", "#b7791f");
  const brandSoft = cssVar("--vp-c-brand-soft", "#fff3d7");

  mermaid.initialize({
    startOnLoad: false,
    securityLevel: "strict",
    theme: "base",
    themeVariables: {
      background: bg,
      mainBkg: bgSoft,
      primaryColor: bgSoft,
      primaryTextColor: text,
      primaryBorderColor: brand,
      secondaryColor: brandSoft,
      secondaryTextColor: text,
      secondaryBorderColor: border,
      tertiaryColor: bgAlt,
      tertiaryTextColor: text,
      tertiaryBorderColor: border,
      lineColor: brand,
      textColor: text,
      nodeTextColor: text,
      edgeLabelBackground: bg,
      clusterBkg: bgAlt,
      clusterBorder: border,
      noteBkgColor: brandSoft,
      noteTextColor: text,
      noteBorderColor: brand,
      actorBkg: bgSoft,
      actorTextColor: text,
      actorBorder: brand,
      actorLineColor: border,
      signalColor: textMuted,
      signalTextColor: text,
      labelBoxBkgColor: bgSoft,
      labelBoxBorderColor: border,
      labelTextColor: text,
      loopTextColor: text,
      activationBkgColor: brandSoft,
      activationBorderColor: brand,
      sequenceNumberColor: bg,
    },
  });
}

export async function renderMermaidDiagrams(): Promise<void> {
  if (!inBrowser) return;
  const pass = ++renderPass;
  await nextTick();

  const roots = Array.from(document.querySelectorAll<HTMLElement>("[data-kmermaid]"));
  if (roots.length === 0) return;

  const mermaid = await loadMermaid();
  configureMermaid(mermaid);

  for (let index = 0; index < roots.length; index++) {
    if (pass !== renderPass) return;
    const root = roots[index];
    const source = root.querySelector<HTMLElement>(".kmermaid__source")?.textContent?.trim();
    const diagram = root.querySelector<HTMLElement>(".kmermaid__diagram");
    if (!source || !diagram) continue;

    const id = `kmermaid-${pass}-${index}`;
    try {
      const { svg, bindFunctions } = await mermaid.render(id, source);
      if (pass !== renderPass) return;
      diagram.innerHTML = svg;
      bindFunctions?.(diagram);
      root.dataset.rendered = "true";
      root.dataset.error = "false";
    } catch (error) {
      root.dataset.rendered = "false";
      root.dataset.error = "true";
      diagram.textContent = error instanceof Error ? error.message : String(error);
    }
  }
}

export function watchMermaidTheme(): () => void {
  if (!inBrowser) return () => {};
  const observer = new MutationObserver(() => {
    void renderMermaidDiagrams();
  });
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  return () => observer.disconnect();
}
