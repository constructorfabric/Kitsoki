---
layout: home

hero:
  name: kitsoki
  text: Deterministic LLM workflows
  tagline: A conversational workflow engine — your workflow is an auditable YAML state machine, and the LLM acts only at narrow, identified, traceable decision points.
  image:
    src: /branding/mesa-sun.svg
    alt: kitsoki mesa sun
  actions:
    - theme: brand
      text: Get started
      link: /guide/
    - theme: alt
      text: Explore the features
      link: /features/
---

<HeroDemo />

## Why kitsoki

**Deterministic first.** The runtime is a directed graph of rooms you author in YAML. Every transition, guard, and effect replays the same way every time — the LLM is confined to declared decision points, each one captured in a structured, replayable trace.

**One story, every surface.** The same `app.yaml` drives a terminal UI, the web surface, Jira comments, and headless daemons. Drive it as a human, hand it to an agent, or replay it from a cassette — the state machine doesn't care.

**Testable like software, because it is.** Flow fixtures replay whole conversations with zero LLM cost; cassettes pin every agent response byte-for-byte; the demos on this site are recorded from those same deterministic runs — what you watch here is a test that passes.

## The features

Every card below is generated from the same feature catalog that drives the in-app tours, the recorded demos, and the QA scenarios — the videos are the spec.

<FeatureGrid :kinds="['feature', 'product-tour']" :promo-only="true" />
