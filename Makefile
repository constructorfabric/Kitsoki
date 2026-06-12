BINARY    := kitsoki
PKG       := ./cmd/kitsoki
# Default install dir: macOS doesn't put ~/bin on PATH (and doesn't create it),
# so default to ~/.local/bin there — already on PATH for typical setups. Linux
# keeps the classic ~/bin. Override with `make install INSTALLDIR=...`.
INSTALLDIR ?= $(if $(filter Darwin,$(shell uname -s)),$(HOME)/.local/bin,$(HOME)/bin)

# Runstatus SPA: built by vite (pnpm) under tools/runstatus, then staged into
# the Go embed dir so the binary can serve it (status serve) and inline it into
# HTML artifacts (export-status). The staged file is gitignored; a committed
# .gitkeep keeps the //go:embed pattern matching on a fresh checkout.
RUNSTATUS_DIR := tools/runstatus
EMBED_INDEX   := internal/runstatus/web/assets/index.html
SPA_SOURCES   := $(shell find $(RUNSTATUS_DIR)/src $(RUNSTATUS_DIR)/index.html \
	$(RUNSTATUS_DIR)/package.json $(RUNSTATUS_DIR)/vite.config.ts 2>/dev/null)

# Every shipped story whose deterministic flow suite `make test` exercises.
STORY_APPS := $(wildcard stories/*/app.yaml)

.PHONY: all setup build install uninstall test test-flows starcheck-kitsoki vet fmt tidy clean web web-clean web-dev web-dev-logs e2e-docker \
	fetch-models fetch-llama-server demo-tour demo-tour-fast demo-tour-qa

all: build

# setup installs every build/runtime dependency `make install` needs (Go, Node,
# pnpm, git, plus optional jq/ffmpeg/gh) on a fresh machine. Covers macOS
# (Homebrew), RockyLinux/RHEL (dnf) and Debian/Ubuntu (apt). Idempotent — skips
# anything already present at a sufficient version. Run this once, then
# `make install`.
setup:
	@./scripts/setup.sh

# check-deps verifies the build toolchain is present before build/install do
# real work, so a fresh machine gets a clear "run make setup" hint instead of a
# raw command-not-found failure.
.PHONY: check-deps
check-deps:
	@missing=""; \
	for t in go pnpm node git; do \
		command -v $$t >/dev/null 2>&1 || missing="$$missing $$t"; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "error: missing build dependencies:$$missing" >&2; \
		echo "       run 'make setup' to install everything 'make install' needs." >&2; \
		exit 1; \
	fi

# build / install depend on web so the binary always embeds a current SPA.
build: check-deps web
	go build -o $(BINARY) $(PKG)

install: check-deps web
	@mkdir -p $(INSTALLDIR)
	GOBIN=$(INSTALLDIR) go install $(PKG)
	@echo "installed $(BINARY) -> $(INSTALLDIR)/$(BINARY)"
	@case ":$$PATH:" in \
		*":$(INSTALLDIR):"*) ;; \
		*) echo "warning: $(INSTALLDIR) is not on your PATH — '$(BINARY)' won't be found." >&2; \
		   echo "         add it (e.g. 'export PATH=\"$(INSTALLDIR):\$$PATH\"' in your shell profile)" >&2; \
		   echo "         or reinstall with 'make install INSTALLDIR=<dir-on-path>'." >&2;; \
	esac

uninstall:
	rm -f $(INSTALLDIR)/$(BINARY)

# web bundles the runstatus SPA and stages it into the embed dir. Incremental:
# only rebuilds when a source file is newer than the staged bundle.
web: $(EMBED_INDEX)

$(EMBED_INDEX): $(SPA_SOURCES)
	@command -v pnpm >/dev/null 2>&1 || { \
		echo "error: pnpm not found — needed to build the runstatus SPA." >&2; \
		echo "       run 'make setup' to install Node + pnpm, or 'make web-clean' if a bundle is already staged." >&2; \
		exit 1; }
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile && pnpm build
	@mkdir -p $(dir $(EMBED_INDEX))
	cp $(RUNSTATUS_DIR)/dist/index.html $(EMBED_INDEX)
	@echo "staged runstatus SPA -> $(EMBED_INDEX)"

# web-clean removes the staged bundle (the binary then reports the SPA as
# unbuilt until the next `make web`).
web-clean:
	rm -f $(EMBED_INDEX)

# web-dev starts the kitsoki Go backend and the Vite HMR dev server in
# parallel so edits to tools/runstatus/src/** are reflected instantly without
# a full pnpm build + go build cycle. Access the app on http://localhost:5173;
# the Vite dev server proxies /rpc and /rpc/events to the Go backend on
# http://127.0.0.1:7777 (override with KITSOKI_API=http://host:port).
#
# Both processes write to stdout/stderr AND to a rotating log file under
# .artifacts/logs/. The 10 most recent logs are kept (older ones are pruned at
# each startup). Use 'make web-dev-logs' to tail the latest log.
#
# Pass extra Go flags via WEB_ARGS, e.g.:
#   make web-dev WEB_ARGS="--stories-dir stories/my-story"
WEB_ARGS     ?=
WEB_LOG_DIR  := .artifacts/logs
WEB_LOG_KEEP := 10
web-dev:
	@command -v pnpm >/dev/null 2>&1 || { echo "error: pnpm not found" >&2; exit 1; }
	@mkdir -p $(WEB_LOG_DIR)
	@find $(WEB_LOG_DIR) -maxdepth 1 -name "web-dev-*.log" | sort | head -n -$(WEB_LOG_KEEP) | xargs -r rm --
	@LOG=$(WEB_LOG_DIR)/web-dev-$$(date +%Y%m%d-%H%M%S).log; \
	  printf 'kitsoki: debug log → %s\n' "$$LOG" >&2; \
	  trap 'kill 0' INT TERM EXIT; \
	  { go run $(PKG) web $(WEB_ARGS) 2>&1; } | tee -a "$$LOG" & \
	  { cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent && FORCE_COLOR=1 pnpm dev 2>&1; } | tee -a "$$LOG"; \
	  wait

# web-dev-logs tails the most recent web-dev log file.
web-dev-logs:
	@latest=$$(find $(WEB_LOG_DIR) -maxdepth 1 -name "web-dev-*.log" | sort | tail -1); \
	  if [ -z "$$latest" ]; then echo "no web-dev logs found in $(WEB_LOG_DIR)" >&2; exit 1; fi; \
	  echo "tailing $$latest" >&2; \
	  tail -f "$$latest"

# test runs the Go unit tests AND the Mode-2 deterministic story flow suites
# (no LLM, no cost). The flow suites guard the shipped stories under stories/,
# which `go test ./...` does not touch. scripts/run-tests.sh collects every
# failure across both suites (never bails early), prints a terse summary on
# success / full detail on failure, and always writes a rotated full report to
# .artifacts/test-reports/.
test:
	@./scripts/run-tests.sh

# pr / pr-ci gate PR creation on a green test run, then open the PR with `gh`.
# Push half-finished branches freely; this is the checkpoint that runs only when
# you choose to open a PR (there's no point opening one CI will fail). Args after
# the target pass through to `gh pr create` via ARGS, e.g.:
#   make pr ARGS="--fill"
#   make pr-ci ARGS="--draft --title 'wip: x'"
#
#   pr     LOCAL gate — runs `make test` here, then opens the PR. Fast/offline;
#          it's the SAME suite CI runs.
#   pr-ci  CI gate    — pushes the branch, triggers the CI workflow on it, waits
#          for it to go green (Linux — exactly the PR check), then opens the PR.
# See scripts/open-pr.sh and docs/architecture/developer-guide.md (§3.3).
.PHONY: pr pr-ci
ARGS ?=
pr:
	@./scripts/open-pr.sh --local $(ARGS)
pr-ci:
	@./scripts/open-pr.sh --ci $(ARGS)

# test-flows replays every story's flow fixtures against a scratch binary built
# from the working tree (plain `go build` — no SPA embed needed), so it tracks
# local edits rather than a stale $(INSTALLDIR) copy. Fails if any story fails.
test-flows:
	@go build -o ./.kitsoki-flows $(PKG)
	@rc=0; for app in $(STORY_APPS); do \
		printf '\n-- flows: %s\n' "$$app"; \
		./.kitsoki-flows test flows "$$app" || rc=1; \
	done; rm -f ./.kitsoki-flows; exit $$rc

# starcheck-kitsoki is the static host.starlark.run pre-flight: it runs the
# starcheck tool's -kitsoki profile (predeclared={json,math}, strict dialect,
# requires def main) over every story's *.star glue scripts. It parses +
# resolves WITHOUT executing — so it is instant, safe, and catches scripts that
# would fail to load (a missing main, a reference outside the sandbox surface)
# long before `make test-flows` boots an app. starcheck is its own Go module
# (docs/skills/starlark/tools/starcheck) so we invoke it from there with the
# scripts passed as absolute paths.
STARCHECK_DIR := docs/skills/starlark/tools/starcheck
starcheck-kitsoki:
	@scripts=$$(find stories -type f -name '*.star' | sort); \
	if [ -z "$$scripts" ]; then echo "starcheck-kitsoki: no .star scripts under stories/"; exit 0; fi; \
	abs=$$(for f in $$scripts; do echo "$(CURDIR)/$$f"; done); \
	cd $(STARCHECK_DIR) && go run . -kitsoki $$abs

# fix-tests drives the stories/fix-tests auto-fixer: it runs the full test
# suite (`make test`), and if anything fails it uses claude (sonnet), via the
# story's host.oracle.task, to fix the failures — re-running the suite up to 3
# cycles — then writes a Markdown report under .artifacts/fix-tests/.
#
# Headless / one-shot: `session create` + `session continue --intent start`
# drives the background-job pipeline to a terminal state in a single drain.
# The fixer EDITS YOUR WORKING TREE (it has Edit/Write); review the diff after.
# It never touches git and never makes network calls.
#
# Exit code: 0 when the suite is green (done_clean); nonzero when tests are
# still red after the budget (done_exhausted) or the fixer needs a human
# decision (blocked) — the report says which, and lists any open questions.
.PHONY: fix-tests
FIX_TESTS_APP := stories/fix-tests/app.yaml
fix-tests:
	@command -v jq >/dev/null 2>&1 || { echo "error: jq is required for 'make fix-tests'" >&2; exit 1; }
	@go build -o ./.kitsoki-fixtests $(PKG)
	@db=$$(mktemp -u $${TMPDIR:-/tmp}/kitsoki-fixtests-XXXXXX.db); \
	 sid=$$(./.kitsoki-fixtests session create --app $(FIX_TESTS_APP) --db "$$db" | jq -r .session_id); \
	 echo "fix-tests: driving session $$sid"; \
	 echo "fix-tests: running the suite and auto-fixing with claude (sonnet) — this may take a while…"; \
	 out=$$(./.kitsoki-fixtests session continue --app $(FIX_TESTS_APP) --db "$$db" --id "$$sid" --intent start --mode one-shot); \
	 state=$$(printf '%s' "$$out" | jq -r .new_state); \
	 echo; echo "fix-tests: final state = $$state"; \
	 report=$$(ls -t .artifacts/fix-tests/report-*.md 2>/dev/null | head -1); \
	 if [ -n "$$report" ]; then echo; echo "──────── $$report ────────"; cat "$$report"; echo "─────────────────────────"; fi; \
	 rm -f ./.kitsoki-fixtests "$$db"; \
	 case "$$state" in \
	   done_clean) echo "fix-tests: PASS — suite is green."; exit 0;; \
	   *) echo "fix-tests: FAIL ($$state) — see the report above." >&2; exit 1;; \
	 esac

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)

# e2e-docker builds a faithful image (make install: Go + Node + pnpm) and runs
# the in-container smoke suite, verifying runtime system deps + deterministic
# flow suites. See test/e2e/ for details.
e2e-docker:
	./test/e2e/run.sh

# fetch-models / fetch-llama-server pre-warm the local-model oracle cache for
# offline/CI use: they run the SAME fetch-and-verify path managed mode runs
# lazily on the first oracle.local call (internal/oracle/server.Fetcher), just
# ahead of time. Artifacts land in ~/.cache/kitsoki (or $KITSOKI_CACHE_DIR) and
# are gitignored — nothing binary is committed. endpoint: mode needs neither.
# MODEL overrides the model id (default: the proposal's Qwen2.5-1.5B default).
MODEL ?=

fetch-models:
	go run ./tools/oracle-fetch -model "$(MODEL)"

fetch-llama-server:
	go run ./tools/oracle-fetch -binary

# demo-tour records the onboarding tour as a shareable MP4/GIF at watch-speed
# and renders the post-production artifacts. Requires pnpm + ffmpeg.
# The spec emits the canonical MP4 directly (never .webm); render adds GIF +
# contact sheet. Output: .artifacts/tour-video/ (mp4, gif, contact-sheet, PNGs).
demo-tour: build
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	cd $(RUNSTATUS_DIR) && pnpm exec playwright test tour-video --project=chromium
	docs/skills/kitsoki-ui-demo/scripts/render.sh .artifacts/tour-video/tour-video-demo.mp4

# demo-tour-fast validates the tour spec assertions only (no dwells, no render).
# Use this in CI or to iterate on spec changes quickly.
demo-tour-fast: build
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	cd $(RUNSTATUS_DIR) && WEB_CHAT_PACE=0 pnpm exec playwright test tour-video --project=chromium

# demo-tour-qa records the tour video then runs the vision QA gate against it.
# Requires the `claude` CLI on PATH. Output: .artifacts/ui-qa/tour-video-demo/.
demo-tour-qa: demo-tour
	docs/skills/kitsoki-ui-qa/scripts/qa.sh \
		.artifacts/tour-video/tour-video-demo.mp4 \
		--frames .artifacts/tour-video \
		--feature docs/skills/kitsoki-ui-qa/templates/tour-feature.md \
		--scenarios docs/skills/kitsoki-ui-qa/templates/tour-scenarios.yaml
