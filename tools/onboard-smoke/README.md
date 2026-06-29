# onboard-smoke — reproducible project-onboarding checks

Gated checks that prove **project onboarding produces a working kitsoki
environment** without using a real LLM.

## Sister Replay

`make onboard-sisters` is the local deterministic replay for the two sister
projects used as onboarding guinea pigs: gears-rust and slidey.

It:

1. Clones the local sibling checkout into a temp dir.
2. Checks out the same saved pre-init baseline commit recorded in
   `.kitsoki/project-profile.yaml`.
3. Runs the real `stories/dev-story/scripts/init_apply.py` against that temp
   checkout.
4. Asserts onboarding writes `.kitsoki.yaml`, `.kitsoki/project-profile.yaml`,
   `.kitsoki/stories/<id>-dev/app.yaml`, and the local README.
5. Asserts root `stories/<id>-dev/` is not created.
6. Loads the generated app through the current Kitsoki dev-story resolver.

Run it with:

```sh
make onboard-sisters
# or:
go test -tags onboardsisters -run TestOnboardSisterProjects_FromBaseline -count=1 -v ./tools/onboard-smoke/
```

By default the harness reads `../gears-rust` and `../slidey` relative to this
checkout. Override those with:

```sh
KITSOKI_GEARS_RUST_REPO=/path/to/gears-rust \
KITSOKI_SLIDEY_REPO=/path/to/slidey \
make onboard-sisters
```

This check is local-only: no network, no installed `kitsoki` binary, no project
build/test commands, and no LLM.

## Binary Smoke

`make onboard-smoke` is a separate binary-user E2E smoke test. It proves a
binary-only user can onboard a pinned third-party repo using the embedded
dev-story library.

It:

1. Clones a **pinned SHA** of [`sindresorhus/yocto-queue`](https://github.com/sindresorhus/yocto-queue)
   (tiny, MIT, Node) into a temp dir — byte-reproducible (the SHA is the
   dereferenced `v1.1.1` tag commit, immune to later tag movement).
2. Drives onboarding **headless** against the binary's **embedded** dev-story
   (`kitsoki session … --app @kitsoki/dev-story`) — no local kitsoki repo, no
   `--kitsoki-repo`, no network beyond the clone.
3. Asserts the onboarded repo is a working environment:
   - `.kitsoki.yaml`, `.kitsoki/stories/yocto-queue-dev/app.yaml` (the dev-story instance)
   - `.mcp.json` registering the kitsoki studio MCP server
   - `.claude/skills/<name>` relative symlinks into `.agents/skills/<name>`
   - `.claude/agents/kitsoki-mcp-driver.md`
   - the generated instance **loads** (a fresh session against it creates cleanly)

Run it with:

```sh
make onboard-smoke
# or, against an already-installed binary:
go test -tags onboardsmoke -run TestOnboardPinnedRepo -count=1 -v ./tools/onboard-smoke/
```

It is **excluded from `make test`** by the `onboardsmoke` build tag — it needs
network, git, and `kitsoki` on PATH. It skips cleanly if `kitsoki`/`git` are
absent.

## Updating the pin

Edit `repoURL` / `repoSHA` in `onboard_smoke_test.go`. Resolve a tag's commit
SHA with:

```sh
git ls-remote --tags https://github.com/sindresorhus/yocto-queue.git | grep '\^{}'
```

Use the `refs/tags/<v>^{}` SHA (the dereferenced commit), not the tag-object SHA.
