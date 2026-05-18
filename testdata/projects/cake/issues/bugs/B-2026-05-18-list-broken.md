---
title: "Note list disappears after creating a second note"
status: open
priority: P2
assignee: brad
url: ""
component: frontend
filed_at: 2026-05-18T09:12:00Z
---

# Note list disappears after creating a second note

Expected: creating a second note appends to the list above.
Actual:   the list re-renders as a single placeholder "no notes" entry;
          the existing note is also lost from the UI (the backend still
          has both).

## Reproduction sketch

1. `cd frontend && npm run dev` (port 5173)
2. `cd backend  && npm start`   (port 8080)
3. Open `http://localhost:5173`, click "New note", type "alpha", save.
4. Click "New note" again, type "beta", save.
5. The list flashes "no notes" and stays empty.
