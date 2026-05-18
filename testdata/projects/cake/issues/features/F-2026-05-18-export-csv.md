---
title: "Export notes as CSV"
status: open
priority: P3
assignee: brad
url: ""
component: backend
filed_at: 2026-05-18T10:02:00Z
---

# Export notes as CSV

Add a `GET /notes.csv` endpoint to the backend that streams every note
as a CSV row (`id,title,body,created_at`). The frontend exposes the
endpoint behind a "Download" button on the notes list.

## Acceptance

- Endpoint returns 200 with `Content-Type: text/csv`.
- One row per note; the first row is the header.
- Empty list still returns the header row.
