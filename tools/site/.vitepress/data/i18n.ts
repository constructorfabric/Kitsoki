export type LocaleCode = "en" | "th" | "ja";
export type LocaleKey = "root" | "th" | "ja";

export interface SiteText {
  nav: {
    features: string;
    guide: string;
  };
  labels: {
    demoPosterAlt: string;
    stepByStep: string;
    deeperDocs: string;
    relatedFeatures: string;
    jumpToStep: string;
    videoChapters: string;
    watchOnline: string;
    demoMissing: string;
  };
}

export interface LocaleInfo {
  code: LocaleCode;
  key: LocaleKey;
  label: string;
  lang: string;
  path: string;
  title: string;
  description: string;
  text: SiteText;
}

export const locales: Record<LocaleCode, LocaleInfo> = {
  en: {
    code: "en",
    key: "root",
    label: "English",
    lang: "en-US",
    path: "",
    title: "kitsoki",
    description:
      "A conversational workflow engine: deterministic YAML state machines with the LLM confined to narrow, traceable decision points.",
    text: {
      nav: { features: "Features", guide: "Docs" },
      labels: {
        demoPosterAlt: "demo poster",
        stepByStep: "Step by step",
        deeperDocs: "Deeper docs",
        relatedFeatures: "Related features",
        jumpToStep: "Jump the video to this step",
        videoChapters: "Video chapters",
        watchOnline: "Watch this demo online",
        demoMissing: "demo video not rendered in this build",
      },
    },
  },
  th: {
    code: "th",
    key: "th",
    label: "ไทย",
    lang: "th-TH",
    path: "/th",
    title: "kitsoki",
    description:
      "เอนจินเวิร์กโฟลว์แบบสนทนา: state machine จาก YAML ที่ตรวจสอบซ้ำได้ โดยจำกัด LLM ไว้เฉพาะจุดตัดสินใจที่แคบและติดตามได้",
    text: {
      nav: { features: "ฟีเจอร์", guide: "คู่มือ" },
      labels: {
        demoPosterAlt: "ภาพตัวอย่างเดโม",
        stepByStep: "ทีละขั้นตอน",
        deeperDocs: "เอกสารเชิงลึก",
        relatedFeatures: "ฟีเจอร์ที่เกี่ยวข้อง",
        jumpToStep: "ข้ามวิดีโอไปยังขั้นตอนนี้",
        videoChapters: "บทของวิดีโอ",
        watchOnline: "ดูเดโมนี้ออนไลน์",
        demoMissing: "เดโมวิดีโอไม่ได้ถูกเรนเดอร์ในบิลด์นี้",
      },
    },
  },
  ja: {
    code: "ja",
    key: "ja",
    label: "日本語",
    lang: "ja-JP",
    path: "/ja",
    title: "kitsoki",
    description:
      "会話型ワークフローエンジン。決定的な YAML state machine を中心に、LLM は狭く追跡可能な判断点だけで動きます。",
    text: {
      nav: { features: "機能", guide: "ガイド" },
      labels: {
        demoPosterAlt: "デモのポスター",
        stepByStep: "ステップごとに見る",
        deeperDocs: "詳しいドキュメント",
        relatedFeatures: "関連機能",
        jumpToStep: "動画をこのステップへ移動",
        videoChapters: "動画チャプター",
        watchOnline: "このデモをオンラインで見る",
        demoMissing: "このビルドではデモ動画はレンダリングされていません",
      },
    },
  },
};

export const localeCodes = Object.keys(locales) as LocaleCode[];

export function prefixed(locale: LocaleCode, path: string): string {
  const clean = path.startsWith("/") ? path : `/${path}`;
  return locale === "en" ? clean : `${locales[locale].path}${clean}`;
}
