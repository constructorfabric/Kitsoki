# Story QA Runner

This runner is the local, reproducible front door for the exploratory QA agent
surface.

It does two things:

1. summarizes the project lanes that the exploratory QA pass cares about;
2. runs the deterministic verification path for `gears-rust` when a local
   checkout is available.

## Commands

```sh
python3 tools/story-qa/run.py
python3 tools/story-qa/run.py --project gears-rust
python3 tools/story-qa/run.py --project all
```

The runner writes a transient report to `.context/story-qa-run.md`.

## Local product site

Stage the Kitsoki product site as a local production build so the QA agent
always points at a deterministic, host-local surface:

```sh
make web
GOCACHE=$(mktemp -d) go run ./cmd/kitsoki web --addr 127.0.0.1:7777
```

`gears-rust` is cached as validated in the summary path; set
`GEARS_RUST_RECHECK=1` if you want the heavy external benchmark to rerun.
