---
id: bsacrobatix/Kitsoki#1199
title: 'gh-agent incident: hosted bugfix preflight does not reach terminal triage'
source_kind: github
source_repo: bsacrobatix/Kitsoki
source_url: https://github.com/bsacrobatix/Kitsoki/issues/1199
baseline: main
repro_command: go test ./internal/ghagent ./cmd/kitsoki
---

# Hosted GitHub agent preflight does not reach terminal triage

A hosted GitHub-agent job failed because a bugfix preflight flow remained in
`triaging` instead of reaching the expected terminal triage state.
