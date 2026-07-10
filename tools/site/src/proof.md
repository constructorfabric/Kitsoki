---
title: Proof
---

# Proof path

Kitsoki's product claim is falsifiable: the workflow should stay in charge
while the model works inside declared boundaries. This page is the short path
through the demos and docs that answer that claim.

## 1. Can the runtime control an agent?

Watch the host reject a model submission, inject a nudge, and accept the fixed
result inside the same bounded call. Then watch an agent question block on a
real operator answer instead of silently defaulting.

<FeatureGrid :ids="['agent-actions', 'operator-ask']" />

## 2. Can I audit and replay the run?

Trace pages show decisions, confidence, evidence, host calls, world mutation,
latency, and replay affordances from the recorded trace. The useful question is
not "was there a transcript?" but "can I point at the decision edge that moved
the workflow?"

<FeatureGrid :ids="['trace-introspection', 'trace-features']" />

## 3. Can the system improve from its own misses?

A completed run should not disappear into a chat transcript. Kitsoki keeps the
improvement loop close to the run: launch `/meta improve`, review prompt, tool,
script, and permission lessons against the trace, then file an evidence-backed
report locally, to GitHub, or to a private ticket provider.

<FeatureGrid :ids="['meta-improvement', 'meta-mode']" />

## 4. Can this drive real repo work?

These demos show Kitsoki as a graph of rooms and gates over a real engineering
workflow: triage, reproduce, propose, implement, test, review, validate, and
open the PR.

<FeatureGrid :ids="['dev-story-bugfix', 'slidey-bugfix', 'slidey-open-pr']" />

## 5. Can I try it locally?

The install path starts from your repository, not from a Kitsoki checkout. Use
the binary, run onboarding from the project root, inspect what it discovered,
then commit the small setup it writes.

<FeatureGrid :ids="['stranger-install', 'kitsoki-doctor']" />

## Read next

- [Evaluate Kitsoki](/guide/evaluate-kitsoki.html) for the comparison against
  coding agents, graph orchestrators, durable workflow engines, and scripts.
- [Getting started](/guide/getting-started.html) for the first local run.
- [Demos and features](/features/) for the full catalog.
- [Extension library](/library/) for the build-time package and story index.
