---
id: constructorfabric/Kitsoki#61
title: 'First-run local harness/profile setup'
source_kind: github
source_repo: constructorfabric/Kitsoki
source_url: https://github.com/constructorfabric/Kitsoki/issues/61
baseline: main
repro_command: go test ./internal/webconfig ./cmd/kitsoki
---

# First-run local harness/profile setup

The first-run path should inspect local harness configuration, check common
environment variables, and securely write local profile settings.
