# Contributing to kitsoki

Thanks for hacking on kitsoki! The short version:

## Before you open a PR

Run the full suite — it's the same one CI runs:

```sh
make test
```

`make test` runs `go test ./...` **plus** every story's deterministic flow
fixtures (no LLM, no cost). It needs only Go, `bash` and `jq`. Green `make test`
is the bar for every change.

Push work-in-progress branches freely; gate **PR creation** on a green run:

```sh
make pr        # runs `make test` locally, then opens the PR with `gh`
make pr-ci     # pushes, runs the real CI on your branch, opens the PR only if green
```

Both need the [`gh` CLI](https://cli.github.com) (`gh auth login`). Extra args
pass through to `gh pr create` via `ARGS=`, e.g. `make pr ARGS="--fill"`.

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs `make test` on
`ubuntu-latest` for every push to `main` and every PR. CI is Linux; write tests
that are cross-platform and hermetic (see the macOS gotchas below).

## More

The authoritative contributor reference — build/vet/test details, the CI + PR
gate, cross-platform test gotchas (macOS `t.TempDir()` symlinks, unix-socket path
limits), coding conventions, debugging, and common-change workflows — is
[`docs/guide/development/developer-guide.md`](docs/guide/development/developer-guide.md).

Project conventions also live in [`CLAUDE.md`](CLAUDE.md).
