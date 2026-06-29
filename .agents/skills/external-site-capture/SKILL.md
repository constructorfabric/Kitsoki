---
name: external-site-capture
description: Capture external browser views (GitHub tickets, JIRA, Teams threads) using rrweb and Playwright, applying spotlights and captions, and embedding them into slidey decks.
---

# External Site Capture Skill

This skill explains how to capture live external site actions (e.g., GitHub tickets, JIRA, Teams threads) using rrweb and Playwright, and embed them as native clips in Slidey decks.

---

## 1. The Reusable Capture Process

To showcase real, live project artifacts on external sites inside a Slidey deck without rendering static images or heavy video formats:

### Step A: Write the Playwright Spec
Create a dedicated spec file under `tools/runstatus/tests/playwright/` (e.g., `pet-github-capture.spec.ts`). Use these context settings to handle security policies and visual consistency:

1. **Bypass Content Security Policy (CSP):** Bypassing CSP is mandatory for rrweb's recorder script injection on external sites.
2. **Emulate Dark Mode:** Emulating `prefers-color-scheme` ensures that the external site renders in dark mode (matching the deck's theme).
3. **Lock Theme Attributes:** Inject attribute updates directly into the document root (`data-color-mode="dark"`) so the layout remains dark during rrweb re-serialization.

```typescript
const context = await browser.newContext({
  ...cameraContext(),
  bypassCSP: true,
  colorScheme: "dark",
});

await page.addInitScript(() => {
  document.documentElement.setAttribute("data-color-mode", "dark");
  document.documentElement.setAttribute("data-dark-theme", "dark");
});
```

### Step B: Apply Captions and Spotlight Annotations
Use the non-obscuring spotlight outline and caption overlay helpers from `_helpers/demo.ts`:

```typescript
const caption = await makeCaption(page);
const spotlight = await makeSpotlight(page);

// Highlight the title
await caption("Design Issue", "The live GitHub issue assigned to the architect.", 3000);
await spotlight("h1.gh-header-title");
await dwell(page, 2000);

// Highlight comment containing the linked deck
await caption("Linked Artifact", "The validated slidey deck linked in the issue body.", 4000);
await spotlight(".comment-body");
await dwell(page, 3000);

// Clear spotlights
await spotlight(null);
```

### Step C: Record and Dump
Wrap the navigation in `installCapture(page)` and `dumpCapture(page)` boundaries, then write the events directly into the deck's asset folder:

```typescript
await installCapture(page);
await page.goto(c.url, { waitUntil: "domcontentloaded" });
// ... drive highlights and dwell ...
const { events, viewport } = await dumpCapture(page);
writeEvents(events, "docs/decks/clips/my-clip.rrweb.json", viewport);
```
