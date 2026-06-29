---
title: Features
---

# Proof before catalog

Kitsoki's claim is not that the UI is pleasant or that the model is clever. The
claim is that the workflow stays in charge while the model works inside declared
boundaries. Start with the demos that prove that claim, then browse the rest of
the catalog.

- **Agent action transcripts:** the host rejects a model submission, injects a
  nudge, and accepts the corrected result inside one bounded call.
- **Trace introspection:** deterministic routing advances a turn with no model
  call, and every decision/world mutation is inspectable after the fact.
- **Operator ask:** agent questions park the run and wait for the operator
  instead of silently defaulting.
- **Meta mode:** the running machine can explain and edit its own story, then
  hot-reload the YAML.
- **Story demos:** real repo workflows replay from captured runs, so the proof
  is not a scripted mock conversation.

Each page below pairs a deterministic demo video with a step-by-step
walkthrough, generated from the same feature catalog that drives in-app tours
and QA scenarios. Recorded demos replay from cassettes; synthetic demos use
no-LLM fixtures. The published site render does not improvise with a live model.

## Features

<FeatureGrid :kinds="['feature']" />

## Product tours

<FeatureGrid :kinds="['product-tour']" />

## Story demos

<FeatureGrid :kinds="['story-demo']" />
