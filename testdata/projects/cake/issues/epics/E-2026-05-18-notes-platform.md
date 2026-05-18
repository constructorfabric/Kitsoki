---
title: "Notes-as-a-Platform: tags, sharing, search"
status: open
priority: P1
assignee: brad
url: ""
component: full-stack
filed_at: 2026-05-18T11:30:00Z
---

# Notes-as-a-Platform: tags, sharing, search

Lift the demo "notes + tasks" app into a small platform: every note can
carry a list of tags, be shared with a named-user ACL, and surface in a
full-text search index that paginates results. The cypilot pipeline
takes this epic through PRD → ADR → DESIGN → DECOMPOSITION → FEATURE ×
N → CODE; each phase produces an artifact that the cake epic flow
asserts on.

## Sub-goals

- Tags: tag CRUD + many-to-many on notes.
- Sharing: ACL with per-note read/write modes; sharing-link generation.
- Search: backend index (Postgres `tsvector` or external service) with
  paginated results in the frontend's notes list.
