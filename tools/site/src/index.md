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
      link: /proof.html
---

<HeroDemo />

## Pick your next question

<div class="kpath-grid">
  <a class="kpath" href="proof.html">
    <strong>Can the workflow control the agent?</strong>
    <span>Start with the proof path: runtime rejection, trace replay, operator handoff, and a real repo workflow.</span>
  </a>
  <a class="kpath" href="guide/evaluate-kitsoki.html">
    <strong>Is this different from a coding agent?</strong>
    <span>Compare Kitsoki with agentic CLIs, graph orchestrators, durable workflow engines, and scripts.</span>
  </a>
  <a class="kpath" href="guide/getting-started.html">
    <strong>Can I try it in my repo?</strong>
    <span>Install the binary, run <code>onboard .</code>, review the profile, and commit the setup Kitsoki writes.</span>
  </a>
  <a class="kpath" href="guide/">
    <strong>How do I author and test stories?</strong>
    <span>Use the docs by task: story authoring, replay without live LLM spend, debugging, and architecture.</span>
  </a>
  <a class="kpath" href="library/">
    <strong>What extensions and stories ship today?</strong>
    <span>Browse the build-time library generated from package sidecars, story manifests, and deterministic flow metadata.</span>
  </a>
</div>

## What changes

Kitsoki is for workflows that repeat, need human gates, or have to be defended
after the run. The runtime owns the state machine. The model is called only at
declared decision points and returns a typed result. Deterministic turns route
without a model call; ambiguous turns, host calls, guardrail retries, and world
mutations land in a structured trace.

The same `app.yaml` drives the web UI, terminal UI, MCP studio surface,
headless runs, docs-driven demos, and replay fixtures. The product surface is
not a mockup of the workflow; it is the workflow.

## Start with the proof

These are the demos a curious developer should watch first. The full feature
and story catalog is still available from [Demos and features](/features/).

<FeatureGrid :ids="['agent-actions', 'meta-improvement', 'trace-introspection', 'operator-ask', 'dev-story-bugfix', 'stranger-install']" />
