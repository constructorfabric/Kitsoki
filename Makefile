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
VSCODE_DIR    := tools/vscode-kitsoki
EMBED_INDEX   := internal/runstatus/web/assets/index.html
# features/*.yaml is part of the SPA's sources: the tour manifests the bundle
# ships are code-generated from the feature catalog (see `make features`).
SPA_SOURCES   := $(shell find $(RUNSTATUS_DIR)/src $(RUNSTATUS_DIR)/index.html \
	$(RUNSTATUS_DIR)/package.json $(RUNSTATUS_DIR)/vite.config.ts 2>/dev/null) \
	$(wildcard features/*.yaml)

# Every shipped story whose deterministic flow suite `make test` exercises.
STORY_APPS := $(wildcard stories/*/app.yaml)

# Base-story embed: the whole stories/ library is staged into internal/
# basestories/stories/ so //go:embed can ship it in the binary (embed can't
# reference a parent dir, hence the staged copy). The staged tree is
# gitignored; a committed stories/.gitkeep keeps the //go:embed pattern
# matching on a fresh checkout. internal/basestories.Materialize extracts it
# to a content-addressed cache at runtime so `@kitsoki/<name>` resolves with
# only the binary present. See docs/proposals/kitsoki-as-dependency.md (slice 1).
BASESTORIES_DIR   := internal/basestories/stories
BASESTORIES_STAMP := internal/basestories/.embed-stamp

.PHONY: all setup build install uninstall test test-flows starcheck-kitsoki vet fmt tidy clean web web-clean web-dev web-dev-logs embed-stories e2e-docker \
	fetch-models fetch-llama-server demo-tour demo-tour-fast demo-tour-qa cost-report cost-report-test mining-test \
	vscode-e2e vscode-e2e-fast vscode-qa vscode-theming-sidebyside vscode-package vscode-install-local

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

# build / install depend on web + embed-stories so the binary always embeds a
# current SPA and the current story library.
build: check-deps web embed-stories
	go build -o $(BINARY) $(PKG)

install: check-deps web embed-stories
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

# embed-stories stages the top-level stories/ library into the basestories
# embed dir so //go:embed ships it in the binary. It avoids a frozen per-file
# prerequisite list because story fixtures are often renamed during long builds;
# instead it compares a fresh staged tree and only rewrites the embed dir when
# content changed. The copy is deterministic and one-directional (stories/ →
# internal/basestories/stories/, disjoint trees) so there is no embed-of-cwd /
# recursive-embed footgun.
embed-stories:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/kitsoki-basestories.XXXXXX"); \
	trap 'rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/stories"; \
	cp -R stories/. "$$tmp/stories"/; \
	touch "$$tmp/stories/.gitkeep"; \
	if [ -d "$(BASESTORIES_DIR)" ] && diff -qr "$$tmp/stories" "$(BASESTORIES_DIR)" >/dev/null; then \
		touch "$(BASESTORIES_STAMP)"; \
		echo "stories/ already staged in $(BASESTORIES_DIR)"; \
	else \
		rm -rf "$(BASESTORIES_DIR)"; \
		mkdir -p "$(BASESTORIES_DIR)"; \
		cp -R "$$tmp/stories"/. "$(BASESTORIES_DIR)"/; \
		touch "$(BASESTORIES_STAMP)"; \
		echo "staged stories/ -> $(BASESTORIES_DIR)"; \
	fi

# web bundles the runstatus SPA and stages it into the embed dir. Incremental:
# only rebuilds when a source file is newer than the staged bundle.
web: $(EMBED_INDEX)

$(EMBED_INDEX): $(SPA_SOURCES)
	@command -v pnpm >/dev/null 2>&1 || { \
		echo "error: pnpm not found — needed to build the runstatus SPA." >&2; \
		echo "       run 'make setup' to install Node + pnpm, or 'make web-clean' if a bundle is already staged." >&2; \
		exit 1; }
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile && pnpm features:check && pnpm build
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

# test runs the Go unit tests, the Mode-2 deterministic story flow suites, the
# feature catalog, AND the session-mining no-LLM invariants (== mining-test) —
# all without an LLM or cost. The flow suites guard the shipped stories under
# stories/ and the mining suites guard tools/session-mining/, neither of which
# `go test ./...` touches. scripts/run-tests.sh collects every failure across
# all suites (never bails early), prints a terse summary on success / full
# detail on failure, and always writes a rotated full report to
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

# cost-report builds the per-story cost-savings report (the reusable form of
# docs/case-studies/git-ops-cost.md): the deterministic story cost (agent spend
# from each story's host cassette) vs the REAL raw-agentic cost of the same
# operations, from telemetry already on disk. NO LLM, no cost. Writes a markdown
# table to .artifacts/cost-report/ (gitignored — it reads your local transcripts).
# Override the transcript pool with PROJECTS='~/.claude/projects/<glob>*'.
COST_REPORT_OUT ?= .artifacts/cost-report/cost-report.md
PROJECTS ?=
cost-report:
	@python3 tools/session-mining/cost_report.py --all \
		$(if $(PROJECTS),--projects '$(PROJECTS)',) --out $(COST_REPORT_OUT)
	@echo "report: $(COST_REPORT_OUT)"

# mining-test runs every no-LLM invariant in the session-mining stack: the
# intent pipeline + parsers, outcome/satisfaction capture, the git-ops coverage
# end-to-end, and the whole real-cost stack (pricing, extractor, estimator
# fallback, report driver). All run against committed fixtures + frozen agent
# JSON — NEVER a live LLM (AGENTS.md). `scripts/run-tests.sh` runs this as its
# fourth suite so `make test` / CI guard it; run standalone for a fast loop.
mining-test:
	@rc=0; for t in tools/session-mining/tests/test_*.py; do \
		printf '\n-- %s\n' "$$t"; python3 "$$t" || rc=1; \
	done; exit $$rc

# cost-report-test — alias kept for the cost case study's docs; mining-test is
# the canonical target (it's a superset: cost + coverage + pipeline invariants).
cost-report-test: mining-test

# starcheck-kitsoki is the static host.starlark.run pre-flight: it runs the
# starcheck tool's -kitsoki profile (predeclared={json,math}, strict dialect,
# requires def main) over every story's *.star glue scripts. It parses +
# resolves WITHOUT executing — so it is instant, safe, and catches scripts that
# would fail to load (a missing main, a reference outside the sandbox surface)
# long before `make test-flows` boots an app. starcheck is its own Go module
# (.agents/skills/starlark/tools/starcheck) so we invoke it from there with the
# scripts passed as absolute paths.
STARCHECK_DIR := .agents/skills/starlark/tools/starcheck
starcheck-kitsoki:
	@scripts=$$(find stories -type f -name '*.star' | sort); \
	if [ -z "$$scripts" ]; then echo "starcheck-kitsoki: no .star scripts under stories/"; exit 0; fi; \
	abs=$$(for f in $$scripts; do echo "$(CURDIR)/$$f"; done); \
	cd $(STARCHECK_DIR) && go run . -kitsoki $$abs

# fix-tests drives the stories/fix-tests auto-fixer: it runs the full test
# suite (`make test`), and if anything fails it uses claude (sonnet), via the
# story's host.agent.task, to fix the failures — re-running the suite up to 3
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

# fetch-models / fetch-llama-server pre-warm the local-model agent cache for
# offline/CI use: they run the SAME fetch-and-verify path managed mode runs
# lazily on the first agent.local call (internal/agent/server.Fetcher), just
# ahead of time. Artifacts land in ~/.cache/kitsoki (or $KITSOKI_CACHE_DIR) and
# are gitignored — nothing binary is committed. endpoint: mode needs neither.
# MODEL overrides the model id (default: the proposal's Qwen2.5-1.5B default).
MODEL ?=

fetch-models:
	go run ./tools/agent-fetch -model "$(MODEL)"

fetch-llama-server:
	go run ./tools/agent-fetch -binary

# Feature catalog: features/*.yaml at the repo root is the single source of
# truth for feature content — tour steps, demo bindings, promo/docs metadata,
# and ui-qa scenarios. The committed tour manifests under
# tools/runstatus/src/tour/generated/ are CODE-GENERATED from it.
#   features        regenerate the manifests + features/feature.schema.json
#   features-check  validate the catalog and fail on stale generated files
#                   (runs inside `make build` and `make test` — a stale manifest
#                   can never be embedded into the binary)
#   features-index  emit the site/QA contract to .artifacts/features/
.PHONY: features features-check features-index demo-feature feature-qa
features:
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent && pnpm features:gen

features-check:
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent && pnpm features:check

features-index:
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent && pnpm features:index

# demo-feature records ONE feature's demo video at watch-speed and renders the
# GIF + contact sheet. Spec + artifact paths come from the feature catalog.
# Usage: make demo-feature FEATURE=agent-actions
FEATURE ?=
demo-feature: build
	@test -n "$(FEATURE)" || { echo "usage: make demo-feature FEATURE=<id>" >&2; exit 1; }
	@mkdir -p bin && cp $(BINARY) bin/$(BINARY)
	@cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	@set -e; \
	demo=$$(cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/generate.ts --print-demo $(FEATURE)); \
	spec=$$(printf '%s' "$$demo" | cut -f1); \
	video=$$(printf '%s' "$$demo" | cut -f3); \
	(cd $(RUNSTATUS_DIR) && pnpm exec playwright test "$$spec" --project=chromium); \
	.agents/skills/kitsoki-ui-demo/scripts/render.sh "$$video"

# feature-qa records the feature's demo then runs the vision QA gate against it
# with the catalog-generated feature spec + scenarios. GATED: drives the real
# `claude` CLI — never run automatically (CLAUDE.md LLM-test policy).
# Usage: make feature-qa FEATURE=agent-actions
feature-qa: demo-feature features-index
	@set -e; \
	demo=$$(cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/generate.ts --print-demo $(FEATURE)); \
	dir=$$(printf '%s' "$$demo" | cut -f2); \
	video=$$(printf '%s' "$$demo" | cut -f3); \
	.agents/skills/kitsoki-ui-qa/scripts/qa.sh "$$video" \
		--frames "$$dir" \
		--feature .artifacts/features/qa/$(FEATURE).feature.md \
		--scenarios .artifacts/features/qa/$(FEATURE).scenarios.yaml

# tour-qa renders the stitched master then runs the gated vision-QA against it.
# The master is stitched (no recording spec), so it can't go through feature-qa;
# this drives qa.sh on the master video + its generated scenarios instead.
# GATED: drives the real `claude` CLI — never run automatically (CLAUDE.md).
.PHONY: tour-qa
TOUR ?= complete-product-tour
tour-qa: render-tour features-index
	@set -e; \
	dir=.artifacts/$(TOUR); \
	.agents/skills/kitsoki-ui-qa/scripts/qa.sh "$$dir/$(TOUR).mp4" \
		--frames "$$dir" \
		--feature .artifacts/features/qa/$(TOUR).feature.md \
		--scenarios .artifacts/features/qa/$(TOUR).scenarios.yaml

# ── MCP terminal demo (a coding agent driving kitsoki over MCP; Claude Code POC) ──
# Generalizes the demo→QA pipeline to a terminal surface: an xterm.js terminal
# replays a committed termcast cassette, filmed through the shared camera/chapters/
# QA machinery. No-LLM by construction (replays a static cassette). See
# tools/mcp-demo/README.md.
MCP_DEMO_DIR := tools/mcp-demo
.PHONY: mcp-demo-deps mcp-demo-fast mcp-demo mcp-demo-live mcp-qa

mcp-demo-deps:
	cd $(MCP_DEMO_DIR) && pnpm install --silent

# Fast, no-LLM validation (safe in CI): the no-spawn/camera/chapters lint + an
# assert-only PACE=0 record (throwaway .fast.mp4, never the canonical name).
mcp-demo-fast: mcp-demo-deps
	cd $(MCP_DEMO_DIR) && pnpm run lint:no-llm && WEB_CHAT_PACE=0 pnpm exec playwright test

# Watch-speed record → .artifacts/mcp-demo/<agent>.mp4 (+ chapters.json). No LLM:
# replays the committed cassette. AGENT selects it (default claude-code).
AGENT ?= claude-code
mcp-demo: mcp-demo-deps
	cd $(MCP_DEMO_DIR) && MCP_DEMO_AGENT=$(AGENT) pnpm run record

# Record the captured-live cassette (the authentic Claude-Code session, committed)
# → .artifacts/mcp-demo/claude-code-live.mp4. Still a pure replay, no LLM.
mcp-demo-live: mcp-demo-deps
	cd $(MCP_DEMO_DIR) && MCP_DEMO_CAST_JSON=casts/claude-code-live.json pnpm run record

# Vision QA on the recorded demo (kitsoki-ui-qa). GATED: drives the local `claude`
# CLI for the grounded review — never run automatically (CLAUDE.md LLM policy).
MCP_QA_VIDEO ?= .artifacts/mcp-demo/$(AGENT).mp4
mcp-qa:
	@rm -rf .artifacts/mcp-demo/frames && mkdir -p .artifacts/mcp-demo/frames
	@cp .artifacts/mcp-demo/0*-*.png .artifacts/mcp-demo/frames/ 2>/dev/null || true
	.agents/skills/kitsoki-ui-qa/scripts/qa.sh $(MCP_QA_VIDEO) \
		--feature .agents/skills/kitsoki-ui-qa/templates/mcp-feature.md \
		--scenarios .agents/skills/kitsoki-ui-qa/templates/mcp-scenarios.yaml \
		--frames .artifacts/mcp-demo/frames \
		--blank-min-coverage 0.18

# demos records every recordable feature demo at watch-speed, incrementally:
# per-demo content stamps (feature YAML + spec + story inputs + binary) skip
# anything unchanged. demos-force re-records everything. See
# scripts/record-demos.sh for the stamp design.
.PHONY: demos demos-force site-full
demos: build features-index
	@mkdir -p bin && cp $(BINARY) bin/$(BINARY)
	@cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	./scripts/record-demos.sh

demos-force: build features-index
	@mkdir -p bin && cp $(BINARY) bin/$(BINARY)
	@cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	./scripts/record-demos.sh --force

# site-full is the everything path: record any stale demos, then build the
# Pages site from them. (What the CI deploy effectively runs.)
site-full: demos site

# ── render: one friendly, extensible front door for local artifacts ─────────
# The rendering machinery already exists (demos / demo-feature / site), but
# under names you have to know. `make render` is the discoverable entrypoint
# for "generate the things people watch and read, locally": today the full set
# of demo videos, tomorrow docs and a stitched product-tour master.
#
# It is reuse-first — every render-* target delegates to the underlying target
# so there is exactly one implementation each. The video path stays incremental
# (per-demo content stamps skip anything unchanged — see scripts/record-demos.sh),
# so re-running `make render` after a docs-only edit records nothing.
#
# To grow it: add a render-<kind> target below and list it in `make render-help`
# (next up: `render-tour` — stitch the per-section videos into one master with
# .agents/skills/kitsoki-ui-demo/scripts/concat-videos.sh).
.PHONY: render render-help render-videos render-video render-docs render-tour render-all
FORCE ?=

render: render-videos
	@echo
	@echo "rendered demo videos -> .artifacts/<id>/*.mp4"
	@echo "more: 'make render-help' (single feature, docs, everything)"

render-help:
	@echo "render targets:"
	@echo "  make render                     record every demo video (incremental)"
	@echo "  make render FORCE=1             re-record every demo video"
	@echo "  make render-video FEATURE=<id>  one feature: video + GIF + contact sheet"
	@echo "  make render-tour                stitch the per-section videos into the master tour"
	@echo "  make render-docs                build the promo site + help docs"
	@echo "  make render-all                 videos + tour + docs"

# render-videos delegates to the incremental demos pipeline (FORCE=1 -> force).
render-videos:
	@$(MAKE) $(if $(FORCE),demos-force,demos)

# render-video records ONE feature's video and renders its GIF + contact sheet.
render-video:
	@$(MAKE) demo-feature FEATURE=$(FEATURE)

# render-tour stitches the per-section recordings into the complete-product-tour
# master (one video + a merged 8-group chapter rail). Depends on `demos` so each
# section source is recorded/fresh first (incremental — unchanged demos skip);
# the stitch itself is pure no-LLM post-processing (scripts/features/stitch-tour.mjs).
render-tour: demos
	@cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/stitch-tour.mjs complete-product-tour

# render-docs builds the VitePress promo site + help docs from whatever demos
# have been recorded (a missing video degrades to a poster, never a failure).
render-docs:
	@$(MAKE) site

# render-all is the everything path: refresh the videos, stitch the master tour,
# then build the site.
render-all:
	@$(MAKE) render-tour
	@$(MAKE) site

# ── Promo site + help docs (tools/site, VitePress) ──────────────────────────
# One source tree, two variants: the GitHub Pages site (full videos, base
# $(SITE_BASE)) and the binary-embedded /help/ copy (posters only — built by
# site-embed in a later phase). Videos are NEVER committed: `make demos`
# records them into .artifacts/ and `site` stages whatever exists — a missing
# video degrades to a poster + placeholder, never a build failure.
SITE_DIR  := tools/site
SITE_BASE ?= /Kitsoki/

.PHONY: site site-data site-dev site-clean
# site-data emits the feature-catalog contract (features-index.json + QA files)
# into the site's gitignored gen/ dir.
site-data:
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent && \
		pnpm exec tsx scripts/features/generate.ts --index --out $(CURDIR)/$(SITE_DIR)/.vitepress/gen

site: site-data
	cd $(SITE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(SITE_DIR) && pnpm stage:docs && pnpm stage:media
	cd $(SITE_DIR) && SITE_BASE=$(SITE_BASE) pnpm build
	node $(SITE_DIR)/scripts/check-leaks.mjs $(SITE_DIR)/.vitepress/dist
	@echo "site built -> $(SITE_DIR)/.vitepress/dist"

# site-dev runs the VitePress HMR dev server (docs iterate instantly; media —
# videos/posters — reflect whatever `make demos` has recorded so far).
site-dev: site-data
	cd $(SITE_DIR) && pnpm install --frozen-lockfile --silent && pnpm stage:docs && pnpm stage:media && pnpm dev

# site-embed builds the EMBEDDED variant (base /help/, posters only — no MP4s
# in the binary) and stages it into internal/helpdocs/assets/ so the next
# `make build` serves it offline at /help/. ~2-4MB on the binary.
HELPDOCS_ASSETS := internal/helpdocs/assets
.PHONY: site-embed
site-embed: site-data
	cd $(SITE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(SITE_DIR) && pnpm stage:docs && pnpm stage:media --variant embedded
	cd $(SITE_DIR) && SITE_BASE=/help/ SITE_VARIANT=embedded pnpm build
	node $(SITE_DIR)/scripts/check-leaks.mjs $(SITE_DIR)/.vitepress/dist-embedded --embedded
	find $(HELPDOCS_ASSETS) -mindepth 1 ! -name .gitkeep -delete
	cp -R $(SITE_DIR)/.vitepress/dist-embedded/. $(HELPDOCS_ASSETS)/
	@echo "help docs staged -> $(HELPDOCS_ASSETS) (next 'make build' embeds them at /help/)"

site-clean:
	rm -rf $(SITE_DIR)/.vitepress/gen $(SITE_DIR)/.vitepress/dist \
		$(SITE_DIR)/.vitepress/dist-embedded $(SITE_DIR)/.vitepress/cache \
		$(SITE_DIR)/src/guide $(SITE_DIR)/src/public/media
	find $(HELPDOCS_ASSETS) -mindepth 1 ! -name .gitkeep -delete 2>/dev/null || true

# vscode-package builds the SPA + extension bundle, then packages an installable
# .vsix for a real VS Code instance (Extensions: Install from VSIX… or
# `code --install-extension`). The .vsix carries ONLY the bundled host + inlined
# SPA + icons (.vscodeignore whitelist); esbuild bundles every runtime dep, so
# --no-dependencies ships no node_modules. It deliberately does NOT bundle the
# `kitsoki` binary — point `kitsoki.binaryPath` at one (or have `kitsoki` on PATH).
# Output: tools/vscode-kitsoki/kitsoki-<version>.vsix.
vscode-package: web
	cd $(VSCODE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(VSCODE_DIR) && pnpm build
	cd $(VSCODE_DIR) && pnpm dlx @vscode/vsce@^3 package --no-dependencies
	@echo "[vscode-package] $$(ls -t $(VSCODE_DIR)/*.vsix | head -1)"

# vscode-install-local is the full local refresh loop for the real editor:
# rebuild the embedded SPA from scratch, install a fresh kitsoki binary, package
# the extension, then force-install the newest VSIX into the local VS Code.
# Override CODE_CLI when testing another compatible editor CLI.
CODE_CLI ?= code
vscode-install-local: check-deps
	@command -v $(CODE_CLI) >/dev/null 2>&1 || { \
		echo "error: $(CODE_CLI) not found — install the VS Code shell command or run CODE_CLI=/path/to/code make vscode-install-local." >&2; \
		exit 1; }
	@rm -f $(VSCODE_DIR)/*.vsix
	$(MAKE) web-clean
	$(MAKE) install
	$(MAKE) vscode-package
	@vsix="$$(ls -t $(VSCODE_DIR)/*.vsix | head -1)"; \
	if [ -z "$$vsix" ]; then \
		echo "error: vscode-package did not produce a .vsix" >&2; \
		exit 1; \
	fi; \
	$(CODE_CLI) --install-extension "$$vsix" --force; \
	echo "[vscode-install-local] installed $$vsix"

# vscode-e2e-fast is the deterministic, no-LLM end-to-end GATE for the VS Code
# extension: it launches real VS Code 1.96.4, opens the Kitsoki view, asserts the
# embedded SPA renders + a session can be started/driven + the trace surfaces
# render — the same critical path the demo video records. KITSOKI_VSCODE_PACE=0
# (assert-only, no recording). This is the de-risk gate; run it before recording.
vscode-e2e-fast: web
	go build -o bin/kitsoki ./cmd/kitsoki   # NOT `cp` — copying invalidates the
	                                        # ad-hoc Mach-O signature on macOS and
	                                        # Gatekeeper SIGKILLs the spawned child.
	cd $(VSCODE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(VSCODE_DIR) && pnpm build
	cd $(VSCODE_DIR) && KITSOKI_VSCODE_PACE=0 pnpm exec playwright test vscode-tour.e2e

# vscode-e2e records the SAME asserted beats as a paced video (recordVideo on,
# per-beat dwells) — the recorder only ADDS pacing on top of the proven path.
# KITSOKI_VSCODE_PACE=N scales the dwells. Output: .artifacts/vscode-e2e/.
vscode-e2e: web
	go build -o bin/kitsoki ./cmd/kitsoki   # NOT `cp` — see vscode-e2e-fast.
	cd $(VSCODE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(VSCODE_DIR) && pnpm build
	cd $(VSCODE_DIR) && KITSOKI_VSCODE_PACE=$${KITSOKI_VSCODE_PACE:-1} pnpm exec playwright test vscode-tour.e2e

# vscode-qa is the DETERMINISTIC (no-LLM) review pass over the recorded VS Code
# demo VIDEO. blank-scan samples the .mp4 to frames and flags large monochromatic
# regions, one-sided dead edge gutters, AND foreign flat edge bars — including a
# RECORDER LETTERBOX bar (a solid grey strip down an edge when the captured window
# is smaller than the recordVideo size). It MUST scan the video, not the window
# screenshots: a recorder-pad bar is composited into the .mp4 and is absent from
# the PNG screenshots — which is exactly how a 14%-wide grey bar shipped unseen.
# Gates on a FOREIGN bar only (a composited recorder/letterbox strip — never
# acceptable); bg-coloured "content doesn't reach the edge" gutters are reported
# but advisory, since sparse-but-correct UI (a code editor, a chat column) leaves
# themed bg at an edge legitimately. ADVISORY=1 downgrades to report-only.
#   VIDEO overrides the target (default the dark-theme tour mp4).
VSCODE_QA_VIDEO ?= .artifacts/vscode-tour-default-dark-modern/vscode-tour.mp4
.PHONY: vscode-qa
vscode-qa:
	docs/skills/kitsoki-ui-qa/scripts/blank-scan.sh $(VSCODE_QA_VIDEO) \
		--out .artifacts/vscode-tour-blank-scan.json \
		$(if $(filter 1,$(ADVISORY)),,--fail-foreign) >/dev/null
	@# Stuck-placeholder gate: a panel sitting on "Loading…" for a long unbroken run
	@# is a code/perf bug (a loading flag never lowered), invisible to blank-scan
	@# (mostly themed bg). OCR catches it deterministically; skips if tesseract absent.
	docs/skills/kitsoki-ui-qa/scripts/placeholder-scan.sh $(VSCODE_QA_VIDEO) \
		--out .artifacts/vscode-tour-placeholder-scan.json \
		$(if $(filter 1,$(ADVISORY)),,--fail-on-find)

# vscode-theming-sidebyside renders the SAME tour under the dark and light editor
# themes, then composes them into one dark|light comparison MP4 (proves the embed
# themes NATIVELY off the editor theme). It records both source videos via the
# paced e2e recorder (KITSOKI_VSCODE_THEME selects the theme), gates EACH SOURCE on
# a foreign recorder bar (--fail-foreign — the real letterbox guard), then hstacks
# them. The composite itself is scanned ADVISORY-only: a two-theme side-by-side is
# bichromatic BY DESIGN, so blank-scan picks one half as "background" and reads the
# other theme's flat surfaces as a foreign bar — a structural false positive. The
# hstack is pure ffmpeg over already-gated sources and cannot introduce a recorder
# bar, so the source gate is the authoritative letterbox check. Output:
# .artifacts/vscode-tour-theming-sidebyside.mp4 (2*1400 x 874).
DARK_TOUR  := .artifacts/vscode-tour-default-dark-modern/vscode-tour.mp4
LIGHT_TOUR := .artifacts/vscode-tour-default-light-modern/vscode-tour.mp4
SIDEBYSIDE := .artifacts/vscode-tour-theming-sidebyside.mp4
.PHONY: vscode-theming-sidebyside
vscode-theming-sidebyside: web
	go build -o bin/kitsoki ./cmd/kitsoki   # NOT `cp` — see vscode-e2e-fast.
	cd $(VSCODE_DIR) && pnpm install --frozen-lockfile --silent && pnpm build
	cd $(VSCODE_DIR) && KITSOKI_VSCODE_THEME="Default Dark Modern"  KITSOKI_VSCODE_PACE=$${KITSOKI_VSCODE_PACE:-1} pnpm exec playwright test vscode-tour.e2e
	cd $(VSCODE_DIR) && KITSOKI_VSCODE_THEME="Default Light Modern" KITSOKI_VSCODE_PACE=$${KITSOKI_VSCODE_PACE:-1} pnpm exec playwright test vscode-tour.e2e
	docs/skills/kitsoki-ui-qa/scripts/blank-scan.sh $(DARK_TOUR)  --fail-foreign --out .artifacts/qa-vscode-dark.json  >/dev/null
	docs/skills/kitsoki-ui-qa/scripts/blank-scan.sh $(LIGHT_TOUR) --fail-foreign --out .artifacts/qa-vscode-light.json >/dev/null
	@# Crop both panels to the COMMON (even) height before stacking: the screen
	@# work area clamps the window, so a render can land 872 or 874 px tall run to
	@# run, and hstack rejects mismatched heights. Crop from top-left (drops at most
	@# a couple bg-coloured px off the bottom).
	H=$$(ffprobe -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 $(DARK_TOUR)); \
	HL=$$(ffprobe -v error -select_streams v:0 -show_entries stream=height -of csv=p=0 $(LIGHT_TOUR)); \
	H=$$(( H < HL ? H : HL )); H=$$(( H - (H % 2) )); \
	ffmpeg -y -loglevel error -i $(DARK_TOUR) -i $(LIGHT_TOUR) \
		-filter_complex "[0:v]crop=iw:$$H:0:0[d];[1:v]crop=iw:$$H:0:0[l];[d][l]hstack=inputs=2:shortest=1[v]" \
		-map "[v]" -c:v libx264 -preset slow -crf 20 -pix_fmt yuv420p -movflags +faststart -an $(SIDEBYSIDE)
	-docs/skills/kitsoki-ui-qa/scripts/blank-scan.sh $(SIDEBYSIDE) --out .artifacts/qa-vscode-sidebyside.json >/dev/null  # advisory (bichromatic)
	@echo "[sidebyside] $(SIDEBYSIDE)"

# surface-panels renders each decomposed surface (chat / trace / graph) at the REAL
# sizes + orientations it occupies in VS Code (editor panel; narrow sidebar; wide
# bottom panel) into .artifacts/surface-panels/, so each panel can be reviewed /
# QA'd as actually presented (catches cut-off at narrow/short docks). No-LLM, Vue
# layer (browser). Rebuilds the embedded binary first so the captures reflect the
# latest SPA. Feed the PNGs to kitsoki-ui-qa via --frames.
.PHONY: surface-panels
surface-panels: web
	go build -o bin/kitsoki ./cmd/kitsoki
	cd $(RUNSTATUS_DIR) && pnpm exec playwright test surface-panels --project=chromium

# demo-tour records the onboarding tour as a shareable MP4/GIF at watch-speed
# and renders the post-production artifacts. Requires pnpm + ffmpeg.
# The spec emits the canonical MP4 directly (never .webm); render adds GIF +
# contact sheet. Output: .artifacts/tour-video/ (mp4, gif, contact-sheet, PNGs).
demo-tour: build
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	cd $(RUNSTATUS_DIR) && pnpm exec playwright test tour-video --project=chromium
	.agents/skills/kitsoki-ui-demo/scripts/render.sh .artifacts/tour-video/tour-video-demo.mp4

# demo-tour-fast validates the tour spec assertions only (no dwells, no render).
# Use this in CI or to iterate on spec changes quickly.
demo-tour-fast: build
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile --silent
	cd $(RUNSTATUS_DIR) && WEB_CHAT_PACE=0 pnpm exec playwright test tour-video --project=chromium

# demo-tour-qa records the tour video then runs the vision QA gate against it,
# with the feature spec + scenarios generated from features/onboarding-tour.yaml.
# Requires the `claude` CLI on PATH. Output: .artifacts/ui-qa/tour-video-demo/.
demo-tour-qa: demo-tour features-index
	.agents/skills/kitsoki-ui-qa/scripts/qa.sh \
		.artifacts/tour-video/tour-video-demo.mp4 \
		--frames .artifacts/tour-video \
		--feature .artifacts/features/qa/onboarding-tour.feature.md \
		--scenarios .artifacts/features/qa/onboarding-tour.scenarios.yaml
