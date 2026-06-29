# Kitsoki pitch deck update

Updated `docs/decks/kitsoki-pitch.slidey.json` for the Cherny-loop / phases section:

- Scene "The Cherny loop": changed the forward `check` and `✓ pass` links to gate-style edges.
- Scene "The Cherny loop": moved the return path farther out with `bus: 860` and `lift: 18` so the fail arrow reads as a visible elbowed loop-back.
- Theme CSS: overrode primary/secondary diagram node fills so the phase slides stop inheriting the default blue/purple template fills.

Validation:

- `node src/index.js validate /Users/brad/code/Kitsoki/docs/decks/kitsoki-pitch.slidey.json` passes.
- `node src/index.js bundle /Users/brad/code/Kitsoki/docs/decks/kitsoki-pitch.slidey.json /tmp/kitsoki-pitch-review.html` passes.

Visual QA:

- Attempted browser screenshot QA with Puppeteer after installing `chrome@121.0.6167.85`.
- The browser process still crashes on launch in this environment with macOS `UniversalExceptionRaise`, so no screenshot evidence was produced.
