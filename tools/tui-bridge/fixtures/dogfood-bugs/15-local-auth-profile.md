---
id: local/bugfix-auth-profile
title: 'bugfix live run blocked: codex profile dispatches unauthenticated Claude Code agent'
source_kind: local
source_repo: ''
source_url: .artifacts/issues/bugs/2026-07-08T063431Z-bugfix-live-run-blocked-codex-profile-dispatches-unauthentic.md
baseline: staging/local
repro_command: go run ./cmd/kitsoki mcp
---

# Codex profile dispatches unauthenticated Claude Code agent

The local dogfood run attempted to use a codex profile but dispatched through an
unauthenticated Claude Code agent, blocking a real GitHub bugfix session before
triage could complete.
