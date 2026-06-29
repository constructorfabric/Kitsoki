---
layout: home

hero:
  name: kitsoki
  text: 決定的な LLM ワークフロー
  tagline: 会話型ワークフローエンジン。ワークフローは監査可能な YAML state machine で、LLM は狭く名前付きで追跡可能な判断点だけで動きます。
  image:
    src: /branding/mesa-sun.svg
    alt: kitsoki mesa sun
  actions:
    - theme: brand
      text: はじめる
      link: /guide/getting-started
    - theme: alt
      text: 機能を見る
      link: /ja/features/
---

<HeroDemo />

## kitsoki を使う理由

**決定性を最初に置く。** runtime は YAML で書く room の有向グラフです。すべての transition、guard、effect は毎回同じように replay できます。LLM は宣言された判断点に閉じ込められ、その結果は構造化された replay 可能な trace に残ります。

**ひとつの story がすべての surface を動かす。** 同じ `app.yaml` が terminal UI、web surface、Jira comments、headless daemon を駆動します。人が操作しても、agent に渡しても、cassette から replay しても、state machine は同じです。

**ソフトウェアとしてテストできる。実際にソフトウェアだから。** flow fixture は LLM コストなしで会話全体を replay します。cassette は agent の応答を byte-for-byte で固定します。このサイトのデモも同じ deterministic run から記録されています。

## 機能

下のカードは、アプリ内ツアー、録画済みデモ、QA scenario を動かすものと同じ feature catalog から生成されています。

<FeatureGrid :kinds="['feature', 'product-tour']" :promo-only="true" />
