---
layout: home

hero:
  name: kitsoki
  text: Put the workflow back in charge of the agent
  tagline: Kitsoki runs conversational workflows as auditable state machines. The LLM is a bounded callee at named decision points, not the hidden dispatcher for every turn.
  image:
    src: /branding/mesa-sun.svg
    alt: kitsoki mesa sun
  actions:
    - theme: brand
      text: Evaluate Kitsoki
      link: /guide/evaluate-kitsoki.html
    - theme: alt
      text: Download
      link: /download.html
    - theme: alt
      text: Watch the proof
      link: /features/
---

<HeroDemo />

## Why kitsoki

**Control inversion.** In most agent systems, the model owns the plan and the runtime executes its tool calls. Kitsoki flips that: the runtime is a YAML state machine, and the model is called only for declared sub-tasks with typed returns.

**Fewer guesses, better evidence.** Deterministic turns route with no model call. Ambiguous turns, agent calls, host calls, world mutations, and guardrail retries land in a structured trace you can inspect instead of trusting a chat transcript.

**One story, every surface.** The same `app.yaml` drives the web UI, terminal UI, MCP studio surface, headless runs, docs-driven demos, and replay fixtures. The product surface is not a mockup of the workflow; it is the workflow.

**Testable like software, because it is.** Flow fixtures replay whole conversations with zero LLM cost; cassettes pin agent responses byte-for-byte; the demos on this site are generated from those same deterministic runs.

## The features

Start with the proof demos: the runtime rejecting and nudging a model mid-call, a turn that routes without any model call, operator questions that block instead of silently defaulting, and real bug-fix runs replayed from cassettes. Then browse the full catalog.

<FeatureGrid :kinds="['feature', 'product-tour']" :promo-only="true" />
