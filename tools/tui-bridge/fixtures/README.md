# TUI bridge fixtures

These fixtures drive the browser/xterm demo through the real Kitsoki TUI process
served by `kitsoki tui-serve`. They are deterministic: free-text routing is
replayed from `dogfood-marathon-recording.yaml`, and LLM-bearing `host.agent.*`
calls are replayed from `dogfood-marathon.host.cassette.yaml`.

The bug markdown files mirror live issue identities and URLs for demo realism,
but the cassette outcomes are authored replay data. They are not a claim that a
paid live GPT-5.5 marathon fixed those issues.

The canonical 15-case list for the dogfood marathon TUI proof lives in
`tools/product-journey/scenarios.json` (`dogfood-marathon-tui`). Keep these
fixtures aligned with that scenario, but do not move case ownership into the
recording or host cassette. The Playwright recorder must consume the emitted
product-journey `driver-plan.json` and attach its MP4/frames back to the run.
