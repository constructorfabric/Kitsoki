---
id: bsacrobatix/Kitsoki#1190
title: 'host.agent.task resolves wrong harness profile/model after on_error'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1190
baseline: main
repro_command: go test ./internal/orchestrator ./internal/host
---

# Agent task resolves wrong profile after on_error

An on_error-triggered dispatch can pick the wrong harness profile/model, which
breaks profile-specific dogfood and cost accounting.
