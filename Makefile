BINARY    := kitsoki
PKG       := ./cmd/kitsoki
KITSOKI_CALLER_PATH := $(PATH)
export KITSOKI_CALLER_PATH
# `make setup` can install required tools into standard locations that the
# current shell has not picked up yet (for example a fresh Homebrew install on
# Apple Silicon, or the official Go tarball on Linux). Append those locations so
# `make install` works immediately after setup without overriding a toolchain
# the caller already put on PATH.
KITSOKI_PATH_FALLBACKS ?= /opt/homebrew/bin /usr/local/go/bin /usr/local/bin
KITSOKI_PATH_APPEND := $(shell for d in $(KITSOKI_PATH_FALLBACKS); do [ -d "$$d" ] && printf '%s%s' "$${sep:-}" "$$d"; sep=:; done)
ifneq ($(strip $(KITSOKI_PATH_APPEND)),)
export PATH := $(KITSOKI_CALLER_PATH):$(KITSOKI_PATH_APPEND)
endif
KITSOKI_BUILD_REVISION := $(shell git rev-parse HEAD 2>/dev/null || true)
KITSOKI_BUILD_REVISION_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null || true)
KITSOKI_BUILD_LDFLAGS := -X kitsoki/internal/buildinfo.Revision=$(KITSOKI_BUILD_REVISION) -X kitsoki/internal/buildinfo.RevisionShort=$(KITSOKI_BUILD_REVISION_SHORT)
# Default install dir: pick a location that's actually on PATH on both Linux and
# macOS, instead of the per-OS ~/bin / ~/.local/bin guess (neither is reliably on
# PATH, which left the binary unfindable). Resolution order:
#   - $GOBIN, if the user already set one (respect their choice);
#   - /usr/local/bin when writable — the standard local-binary dir, on PATH by
#     default on virtually every Linux and macOS (homebrew-Intel, root VMs, …);
#   - else ~/.local/bin — the XDG user-bin fallback when /usr/local/bin needs sudo
#     (Apple-Silicon homebrew, non-root users). The post-install PATH check below
#     warns if whatever we picked isn't on PATH.
# Override with `make install INSTALLDIR=...`.
INSTALLDIR ?= $(shell \
	if [ -n "$$GOBIN" ]; then printf '%s' "$$GOBIN"; \
	elif [ -w /usr/local/bin ]; then printf '%s' /usr/local/bin; \
	else printf '%s' "$$HOME/.local/bin"; fi)

# codesign_adhoc <binary>: on macOS, force a fresh ad-hoc code signature on a
# just-built binary. Go's linker-internal ad-hoc signature is rejected as
# invalid by recent macOS (≥26) on some lazily-faulted code pages, and copying a
# binary invalidates it outright — either way the kernel SIGKILLs it (exit 137,
# no output) when it executes the affected path. An explicit `codesign --force
# --sign -` pass produces a CodeDirectory taskgated accepts. No-op off darwin.
define codesign_adhoc
	@if [ "$$(uname -s)" = "Darwin" ]; then codesign --force --sign - "$(1)" >/dev/null 2>&1 && echo "codesign: ad-hoc signed $(1)" || echo "codesign: WARN could not sign $(1)"; fi
endef

# Runstatus SPA: built by vite (pnpm) under tools/runstatus, then staged into
# the Go embed dir so the binary can serve it (status serve) and inline it into
# HTML artifacts (export-status). The staged file is gitignored; a committed
# .gitkeep keeps the //go:embed pattern matching on a fresh checkout.
RUNSTATUS_DIR := tools/runstatus
VSCODE_DIR    := tools/vscode-kitsoki
EMBED_INDEX   := internal/runstatus/web/assets/index.html
TEMP_DIR      := .temp
RUNSTATUS_TEMP_ENV := TMPDIR="$(abspath $(TEMP_DIR))" KITSOKI_TEMP_ROOT="$(abspath $(TEMP_DIR))"
# Default to the Go toolchain's own shared build cache so bootstrap warms the
# same cache plain `go run` / `go test` will use in the workspace.
KITSOKI_GOCACHE ?= $(shell go env GOCACHE 2>/dev/null || { if [ -d /private/tmp ]; then printf '%s' /private/tmp/kitsoki-gocache; else printf '%s' /tmp/kitsoki-gocache; fi; })
RUNSTATUS_DIST     := $(TEMP_DIR)/runstatus/dist
RUNSTATUS_ABS      := $(abspath $(RUNSTATUS_DIR))
VSCODE_INSTALL_ROOT          := $(TEMP_DIR)/vi
VSCODE_INSTALL_TMP           := $(TEMP_DIR)/t
VSCODE_RUNSTATUS_DIR         := $(VSCODE_INSTALL_ROOT)/tools/runstatus
VSCODE_RUNSTATUS_DIST        := $(VSCODE_INSTALL_ROOT)/runstatus/dist
VSCODE_RUNSTATUS_TEMP_ENV    := TMPDIR="$(abspath $(VSCODE_INSTALL_TMP))" KITSOKI_TEMP_ROOT="$(abspath $(VSCODE_INSTALL_ROOT))"
VSCODE_PACKAGE_DIR           := $(VSCODE_INSTALL_ROOT)/tools/vscode-kitsoki
VSCODE_VSIX_DIR              := $(VSCODE_INSTALL_ROOT)/vsix
VSCODE_EMBED_ROOT            := $(VSCODE_INSTALL_ROOT)/embed
VSCODE_GO_OVERLAY            := $(VSCODE_INSTALL_ROOT)/go-overlay.json
VSCODE_LOCAL_VSIX            := $(VSCODE_VSIX_DIR)/kitsoki-local.vsix

define runstatus_pnpm_install
	@set -e; \
	relock_root=0; relock_modules=0; \
	if [ -d "$(RUNSTATUS_ABS)" ] && [ ! -w "$(RUNSTATUS_ABS)" ]; then chmod u+w "$(RUNSTATUS_ABS)"; relock_root=1; fi; \
	if [ -d "$(RUNSTATUS_ABS)/node_modules" ] && [ ! -L "$(RUNSTATUS_ABS)/node_modules" ] && [ ! -w "$(RUNSTATUS_ABS)/node_modules" ]; then chmod -R u+w "$(RUNSTATUS_ABS)/node_modules"; relock_modules=1; fi; \
	restore_runstatus_guard() { \
		if [ "$$relock_modules" = 1 ] || [ "$$relock_root" = 1 ]; then [ ! -e "$(RUNSTATUS_ABS)/node_modules" ] || [ -L "$(RUNSTATUS_ABS)/node_modules" ] || chmod -R a-w "$(RUNSTATUS_ABS)/node_modules" 2>/dev/null || true; fi; \
		if [ "$$relock_root" = 1 ]; then chmod a-w "$(RUNSTATUS_ABS)" 2>/dev/null || true; fi; \
	}; \
	trap restore_runstatus_guard EXIT INT TERM; \
	if scripts/runstatus-node-modules-cache.sh restore; then \
		restore_runstatus_guard; \
		trap - EXIT INT TERM; \
		exit 0; \
	fi; \
	scripts/runstatus-node-modules-cache.sh prepare-install; \
	(cd $(RUNSTATUS_DIR) && $(RUNSTATUS_TEMP_ENV) pnpm install --frozen-lockfile $(1)); \
	scripts/runstatus-node-modules-cache.sh save; \
	restore_runstatus_guard; \
	trap - EXIT INT TERM
endef

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
# only the binary present. See docs/web/tour.md and stories/dev-story/README.md.
BASESTORIES_DIR   := internal/basestories/stories
BASESTORIES_STAMP := internal/basestories/.embed-stamp

# baseskills mirrors basestories for the agent toolkit: .agents/{skills,agents}
# is staged into the embed dir so //go:embed ships it and `kitsoki project-tools
# install` can install skills/agents + the studio MCP into an onboarded project
# with only the binary present.
BASESKILLS_DIR    := internal/baseskills/assets
BASESKILLS_STAMP  := internal/baseskills/.embed-stamp

.PHONY: all setup setup-visual-qa-deps bootstrap-workspace bootstrap-worktree build build-lean install uninstall test test-full test-flows onboard-smoke onboard-sisters qs-bakeoff gears-bakeoff repo-history-capsules oracle-capsules history-smoke history-pending-smoke gears-history-full-smoke starcheck-kitsoki vet fmt tidy clean web web-clean web-dev web-dev-logs embed-stories embed-skills e2e-docker \
	fetch-models fetch-llama-server demo-tour demo-tour-fast demo-tour-qa cost-report cost-report-test mining-test \
	vscode-e2e vscode-e2e-fast vscode-qa vscode-theming-sidebyside vscode-package vscode-install-local vscode-install-local-in-place \
	vscode-stage-runstatus-temp vscode-runstatus-spa-temp vscode-stage-package-temp vscode-package-temp vscode-stage-embed-overlay-temp vscode-install-binary-temp check-vscode-code-cli

all: build

# setup installs every build/runtime dependency `make install` needs (Go, Node,
# pnpm, git, plus optional jq/ffmpeg/gh), links the local agent toolkit, and
# prepares browser-driven visual QA surfaces on a fresh machine. Covers macOS
# (Homebrew), RockyLinux/RHEL (dnf) and Debian/Ubuntu (apt). Idempotent — skips
# anything already present at a sufficient version. Run this once, then
# `make install` or visual QA targets should work without first-run bootstrap.
setup:
	@./scripts/setup.sh
	@$(MAKE) --no-print-directory setup-visual-qa-deps

# setup-visual-qa-deps makes the browser-driven QA surfaces ready on a fresh
# checkout. pnpm installs the package-local CLIs; playwright install downloads
# the browser revision those CLIs expect, avoiding a later first-run failure in
# TUI/web visual QA.
setup-visual-qa-deps: runstatus-playwright-deps tui-bridge-deps
	@echo "visual QA deps ready — runstatus and tui-bridge can launch Chromium"

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

# build / install depend on web + embed-stories + embed-skills so the binary
# always embeds a current SPA, the current story library, and the current agent
# toolkit.
build: check-deps web embed-stories embed-skills
	go build -ldflags "$(KITSOKI_BUILD_LDFLAGS)" -o $(BINARY) $(PKG)
	$(call codesign_adhoc,$(BINARY))

# build-bin produces the binary the Playwright/VS Code demo specs SPAWN
# (bin/kitsoki), the canonical alternative to the footgun `cp ./kitsoki
# bin/kitsoki`. NEVER `cp` the binary on macOS: copying a Go linker-signed
# Mach-O invalidates its ad-hoc signature and macOS (Gatekeeper/taskgated)
# SIGKILLs the spawned child the moment it faults in an unsigned code page —
# e.g. the story-load path — so `kitsoki web --stories-dir …` dies with exit
# 137 and ZERO output. Building straight to the target (then a defensive
# ad-hoc re-sign) keeps the signature valid.
build-bin: web embed-stories embed-skills
	@mkdir -p bin
	go build -ldflags "$(KITSOKI_BUILD_LDFLAGS)" -o bin/kitsoki $(PKG)
	$(call codesign_adhoc,bin/kitsoki)

# build-lean — headless build for users who don't need the web UI. Unlike
# `build`, it does NOT depend on `web` (the pnpm/node SPA build) or check-deps'
# node/pnpm requirement: the committed //go:embed .gitkeep stub keeps the embed
# pattern matching, so the binary compiles and everything except `kitsoki web`
# works. Toolkit + story library are still embedded. Change 4.6.
build-lean: embed-stories embed-skills
	go build -ldflags "$(KITSOKI_BUILD_LDFLAGS)" -o $(BINARY) $(PKG)
	$(call codesign_adhoc,$(BINARY))

install: check-deps web embed-stories embed-skills
	@mkdir -p $(INSTALLDIR)
	GOBIN=$(INSTALLDIR) go install -ldflags "$(KITSOKI_BUILD_LDFLAGS)" $(PKG)
	@echo "installed $(BINARY) -> $(INSTALLDIR)/$(BINARY)"
	@echo "smoke-checking installed $(BINARY) can load git-ops stories"
	@log="$$(mktemp)"; \
	if ! "$(INSTALLDIR)/$(BINARY)" validate stories/git-ops/app.yaml >"$$log" 2>&1; then \
		cat "$$log" >&2; \
		rm -f "$$log"; \
		exit 1; \
	fi; \
	rm -f "$$log"
	@case ":$$KITSOKI_CALLER_PATH:" in \
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
	@mkdir -p $(TEMP_DIR); chmod u+rwx $(TEMP_DIR); \
	tmp=$$(mktemp -d "$(abspath $(TEMP_DIR))/kitsoki-basestories.XXXXXX"); \
	trap 'chmod -R u+w "$$tmp" 2>/dev/null || true; rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/stories"; \
	cp -R stories/. "$$tmp/stories"/; \
	chmod -R u+w "$$tmp"; \
	touch "$$tmp/stories/.gitkeep"; \
	if [ -d "$(BASESTORIES_DIR)" ] && diff -qr "$$tmp/stories" "$(BASESTORIES_DIR)" >/dev/null; then \
		relock_stamp=0; \
		if ! touch "$(BASESTORIES_STAMP)" 2>/dev/null; then chmod u+w "$(dir $(BASESTORIES_STAMP))" "$(BASESTORIES_STAMP)" 2>/dev/null || true; relock_stamp=1; touch "$(BASESTORIES_STAMP)"; fi; \
		if [ "$$relock_stamp" = 1 ]; then chmod u-w "$(BASESTORIES_STAMP)" "$(dir $(BASESTORIES_STAMP))" 2>/dev/null || true; fi; \
		echo "stories/ already staged in $(BASESTORIES_DIR)"; \
	else \
		relock=0; \
		probe="$(BASESTORIES_DIR)/.kitsoki-write-probe"; \
		if [ -e "$(BASESTORIES_DIR)" ] && ! ( touch "$$probe" && rm -f "$$probe" ) 2>/dev/null; then chmod -R u+w "$(BASESTORIES_DIR)" && chmod u+w "$(dir $(BASESTORIES_DIR))" && relock=1; fi; \
		if [ ! -e "$(BASESTORIES_DIR)" ] && ! ( touch "$(dir $(BASESTORIES_DIR))/.kitsoki-write-probe" && rm -f "$(dir $(BASESTORIES_DIR))/.kitsoki-write-probe" ) 2>/dev/null; then chmod u+w "$(dir $(BASESTORIES_DIR))" && relock=1; fi; \
		rm -rf "$(BASESTORIES_DIR)"; \
		mkdir -p "$(BASESTORIES_DIR)"; \
		cp -R "$$tmp/stories"/. "$(BASESTORIES_DIR)"/; \
		touch "$(BASESTORIES_STAMP)"; \
		if [ "$$relock" = 1 ]; then chmod -R u-w "$(BASESTORIES_DIR)"; fi; \
		echo "staged stories/ -> $(BASESTORIES_DIR)"; \
	fi

# embed-skills stages .agents/skills + .agents/agents into the baseskills embed
# dir (as assets/skills + assets/agents) so //go:embed ships the agent toolkit.
# Same content-compare/one-directional-copy discipline as embed-stories.
embed-skills:
	@mkdir -p $(TEMP_DIR); chmod u+rwx $(TEMP_DIR); \
	tmp=$$(mktemp -d "$(abspath $(TEMP_DIR))/kitsoki-baseskills.XXXXXX"); \
	trap 'chmod -R u+w "$$tmp" 2>/dev/null || true; rm -rf "$$tmp"' EXIT; \
	mkdir -p "$$tmp/assets/skills" "$$tmp/assets/agents"; \
	cp -R .agents/skills/. "$$tmp/assets/skills"/; \
	cp -R .agents/agents/. "$$tmp/assets/agents"/; \
	chmod -R u+w "$$tmp"; \
	touch "$$tmp/assets/.gitkeep"; \
	if [ -d "$(BASESKILLS_DIR)" ] && diff -qr "$$tmp/assets" "$(BASESKILLS_DIR)" >/dev/null; then \
		relock_stamp=0; \
		if ! touch "$(BASESKILLS_STAMP)" 2>/dev/null; then chmod u+w "$(dir $(BASESKILLS_STAMP))" "$(BASESKILLS_STAMP)" 2>/dev/null || true; relock_stamp=1; touch "$(BASESKILLS_STAMP)"; fi; \
		if [ "$$relock_stamp" = 1 ]; then chmod u-w "$(BASESKILLS_STAMP)" "$(dir $(BASESKILLS_STAMP))" 2>/dev/null || true; fi; \
		echo "agent toolkit already staged in $(BASESKILLS_DIR)"; \
	else \
		relock=0; \
		probe="$(BASESKILLS_DIR)/.kitsoki-write-probe"; \
		if [ -e "$(BASESKILLS_DIR)" ] && ! ( touch "$$probe" && rm -f "$$probe" ) 2>/dev/null; then chmod -R u+w "$(BASESKILLS_DIR)" && chmod u+w "$(dir $(BASESKILLS_DIR))" && relock=1; fi; \
		if [ ! -e "$(BASESKILLS_DIR)" ] && ! ( touch "$(dir $(BASESKILLS_DIR))/.kitsoki-write-probe" && rm -f "$(dir $(BASESKILLS_DIR))/.kitsoki-write-probe" ) 2>/dev/null; then chmod u+w "$(dir $(BASESKILLS_DIR))" && relock=1; fi; \
		rm -rf "$(BASESKILLS_DIR)"; \
		mkdir -p "$(BASESKILLS_DIR)"; \
		cp -R "$$tmp/assets"/. "$(BASESKILLS_DIR)"/; \
		touch "$(BASESKILLS_STAMP)"; \
		if [ "$$relock" = 1 ]; then chmod -R u-w "$(BASESKILLS_DIR)"; fi; \
		echo "staged .agents/{skills,agents} -> $(BASESKILLS_DIR)"; \
	fi

# web bundles the runstatus SPA and stages it into the embed dir. Incremental:
# only rebuilds when a source file is newer than the staged bundle.
web: $(EMBED_INDEX)

.PHONY: runstatus-deps
runstatus-deps:
	$(call runstatus_pnpm_install,)

.PHONY: runstatus-deps-if-needed
runstatus-deps-if-needed:
	$(call runstatus_pnpm_install,--silent)

.PHONY: runstatus-playwright-deps
runstatus-playwright-deps: runstatus-deps
	cd $(RUNSTATUS_DIR) && pnpm exec playwright install chromium

$(EMBED_INDEX): $(SPA_SOURCES)
	@command -v pnpm >/dev/null 2>&1 || { \
		echo "error: pnpm not found — needed to build the runstatus SPA." >&2; \
		echo "       run 'make setup' to install Node + pnpm, or 'make web-clean' if a bundle is already staged." >&2; \
		exit 1; }
	@mkdir -p $(TEMP_DIR)
	@chmod u+rwx $(TEMP_DIR)
	@$(MAKE) --no-print-directory runstatus-deps-if-needed
	cd $(RUNSTATUS_DIR) && $(RUNSTATUS_TEMP_ENV) ./node_modules/.bin/tsx scripts/features/generate.ts --check && $(RUNSTATUS_TEMP_ENV) ./node_modules/.bin/tsx scripts/features/lint-demos.ts
	cd $(RUNSTATUS_DIR) && $(RUNSTATUS_TEMP_ENV) ./node_modules/.bin/vite --configLoader runner build
	@mkdir -p $(dir $(EMBED_INDEX))
	@embed_dir="$(dir $(EMBED_INDEX))"; relock_dir=0; relock_file=0; \
	if [ -e "$(EMBED_INDEX)" ] && [ ! -w "$(EMBED_INDEX)" ]; then chmod u+w "$(EMBED_INDEX)" && relock_file=1; fi; \
	if [ ! -e "$(EMBED_INDEX)" ] && [ ! -w "$$embed_dir" ]; then chmod u+w "$$embed_dir" && relock_dir=1; fi; \
	cp $(RUNSTATUS_DIST)/index.html $(EMBED_INDEX); \
	if [ "$$relock_file" = 1 ]; then chmod u-w "$(EMBED_INDEX)"; fi; \
	if [ "$$relock_dir" = 1 ]; then chmod u-w "$$embed_dir"; fi
	@echo "staged runstatus SPA -> $(EMBED_INDEX)"

# web-clean removes the staged bundle (the binary then reports the SPA as
# unbuilt until the next `make web`).
web-clean:
	rm -f $(EMBED_INDEX)

# bootstrap-workspace is the one-shot setup for a FRESH clone/capsule checkout:
# stories/ and the runstatus SPA start as empty .gitkeep placeholders
# (embed-only dirs, staged by embed-stories/web but gitignored once staged),
# tools/runstatus/node_modules is gitignored and restored from the shared
# capsule cache when possible, and the first `go run` in a new workspace
# compiles cold (slow enough to blow past short test timeouts). Run this once
# from inside a new dev workspace before `go run ./cmd/kitsoki` or any
# Playwright spec.
bootstrap-workspace: embed-stories embed-skills web
	@$(MAKE) --no-print-directory runstatus-deps-if-needed
	@echo "bootstrap-workspace: warming shared Go build cache at $(KITSOKI_GOCACHE) (first compile is slow)…"
	@GOCACHE="$(KITSOKI_GOCACHE)" go run $(PKG) --help >/dev/null
	@echo "workspace bootstrapped — go run/Playwright specs should work now"

# Backward-compatible alias for old local habits and scripts.
bootstrap-worktree: bootstrap-workspace

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
STAGING_BRANCH  ?= staging/local
STAGING_CAPSULE ?= .capsules/staging/local
STAGING_REFRESH_GATE ?= git diff --check
STAGING_GIT_ENV := GIT_EDITOR=true GIT_SEQUENCE_EDITOR=true GIT_MERGE_AUTOEDIT=no GIT_PAGER=cat

.PHONY: capsule-ci-quick ensure-staging-capsule refresh-staging staging-ready test-staging web-dev-staging install-staging site-dev-staging
capsule-ci-quick:
	@bash scripts/capsule-ci-quick-gate.sh

ensure-staging-capsule:
	@test -f "$(STAGING_CAPSULE)/.kitsoki-capsule" -o -f "$(STAGING_CAPSULE)/.kitsoki-clone" || { \
		echo "error: staging capsule not found at $(STAGING_CAPSULE)" >&2; \
		echo "       create it with: scripts/dev-workspace.sh create --root .capsules/staging --id local --branch $(STAGING_BRANCH) --base $(STAGING_BRANCH) --target main --bootstrap" >&2; \
		exit 1; \
	}
	@test "$$(git -C "$(STAGING_CAPSULE)" branch --show-current)" = "$(STAGING_BRANCH)" || { \
		echo "error: $(STAGING_CAPSULE) is not on $(STAGING_BRANCH)" >&2; \
		git -C "$(STAGING_CAPSULE)" status --short --branch >&2; \
		exit 1; \
	}
	@test "$$(git rev-parse "$(STAGING_BRANCH)")" = "$$(git -C "$(STAGING_CAPSULE)" rev-parse HEAD)" || { \
		echo "error: $(STAGING_CAPSULE) is stale for $(STAGING_BRANCH)" >&2; \
		echo "       run: make refresh-staging" >&2; \
		echo "       branch:  $$(git rev-parse --short "$(STAGING_BRANCH)")" >&2; \
		echo "       capsule: $$(git -C "$(STAGING_CAPSULE)" rev-parse --short HEAD)" >&2; \
		exit 1; \
	}

refresh-staging:
	@if [ -n "$(strip $(STAGING_REFRESH_GATE))" ]; then \
		$(STAGING_GIT_ENV) scripts/refresh-staging-local.sh --staging-branch "$(STAGING_BRANCH)" --staging-capsule "$(STAGING_CAPSULE)" --gate "$(STAGING_REFRESH_GATE)"; \
	else \
		$(STAGING_GIT_ENV) scripts/refresh-staging-local.sh --staging-branch "$(STAGING_BRANCH)" --staging-capsule "$(STAGING_CAPSULE)"; \
	fi

staging-ready: refresh-staging
	@$(MAKE) --no-print-directory ensure-staging-capsule STAGING_BRANCH="$(STAGING_BRANCH)" STAGING_CAPSULE="$(STAGING_CAPSULE)"

test-staging: staging-ready
	$(STAGING_GIT_ENV) $(MAKE) -C "$(STAGING_CAPSULE)" test

web-dev-staging: staging-ready
	$(STAGING_GIT_ENV) $(MAKE) -C "$(STAGING_CAPSULE)" web-dev

install-staging: staging-ready
	$(STAGING_GIT_ENV) $(MAKE) -C "$(STAGING_CAPSULE)" install

site-dev-staging: staging-ready
	$(STAGING_GIT_ENV) $(MAKE) -C "$(STAGING_CAPSULE)" site-dev

web-dev:
	@command -v pnpm >/dev/null 2>&1 || { echo "error: pnpm not found" >&2; exit 1; }
	@mkdir -p $(WEB_LOG_DIR)
	@find $(WEB_LOG_DIR) -maxdepth 1 -name "web-dev-*.log" | sort | head -n -$(WEB_LOG_KEEP) | xargs -r rm --
	$(call runstatus_pnpm_install,--silent)
	@LOG=$(WEB_LOG_DIR)/web-dev-$$(date +%Y%m%d-%H%M%S).log; \
	  printf 'kitsoki: debug log → %s\n' "$$LOG" >&2; \
	  trap 'kill 0' INT TERM EXIT; \
	  { go run $(PKG) web $(WEB_ARGS) 2>&1; } | tee -a "$$LOG" & \
	  { cd $(RUNSTATUS_DIR) && FORCE_COLOR=1 pnpm dev 2>&1; } | tee -a "$$LOG"; \
	  wait

# web-dev-logs tails the most recent web-dev log file.
web-dev-logs:
	@latest=$$(find $(WEB_LOG_DIR) -maxdepth 1 -name "web-dev-*.log" | sort | tail -1); \
	  if [ -z "$$latest" ]; then echo "no web-dev logs found in $(WEB_LOG_DIR)" >&2; exit 1; fi; \
	  echo "tailing $$latest" >&2; \
	  tail -f "$$latest"

# test runs the short Go unit tests, the Mode-2 deterministic story flow suites,
# the runstatus Vitest suite, the feature catalog, AND the session-mining no-LLM
# invariants (== mining-test) — all without an LLM or cost. The flow suites guard
# the shipped stories under stories/, the web suite guards tools/runstatus/, and
# the mining suites guard tools/session-mining/, none of which `go test ./...`
# covers by itself. scripts/run-tests.sh collects every failure across all suites
# (never bails early), prints a terse summary on success / full detail on
# failure, and always writes a rotated full report to .artifacts/test-reports/.
test:
	$(call runstatus_pnpm_install,--silent)
	@KITSOKI_REQUIRE_VITEST=1 KITSOKI_GO_TEST_FLAGS="$${KITSOKI_GO_TEST_FLAGS:--short}" ./scripts/run-tests.sh

# test-full preserves the exhaustive Go lane for CI/release gates and local
# validation of integration/property tests skipped by -short.
test-full:
	$(call runstatus_pnpm_install,--silent)
	@KITSOKI_REQUIRE_VITEST=1 ./scripts/run-tests.sh

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
# See scripts/open-pr.sh and docs/guide/development/developer-guide.md (§3.3).
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

# onboard-smoke is a gated, reproducible end-to-end test: clone a PINNED
# open-source repo and onboard it to a fully working kitsoki environment via the
# binary's EMBEDDED dev-story (no kitsoki checkout). NOT part of `make test` —
# it needs network + git + an installed `kitsoki` binary. See
# tools/onboard-smoke/README.md. Depends on a current install.
onboard-smoke: install
	go test -tags onboardsmoke -run TestOnboardPinnedRepo -count=1 -v ./tools/onboard-smoke/

# onboard-sisters is the local, deterministic sister-project replay. It clones
# ../gears-rust and ../slidey (or KITSOKI_ONBOARD_RUST_REPO / KITSOKI_SLIDEY_REPO)
# into temp dirs at their saved pre-init commits, runs the real dev-story init
# apply script, and proves the generated .kitsoki/stories instance loads. No
# network, no install, no LLM.
onboard-sisters:
	go test -tags onboardsisters -run TestOnboardSisterProjects_FromBaseline -count=1 -v ./tools/onboard-smoke/

# qs-bakeoff gears-bakeoff is the EXTERNAL-project bake-off scaffold check ("should I use
# kitsoki for my project?"): clone a pinned mature third-party JS repo
# (sindresorhus/query-string), onboard it via the embedded dev-story, and prove
# the 3 hidden-oracle good/bad detectors are armed (RED at baseline, GREEN at the
# real fix) — DETERMINISTIC, no LLM, no cost. NOT part of `make test`; needs
# network + git + node/npm + an installed `kitsoki`. The cost-bearing LLM cells
# stay operator-run. See tools/bugfix-bakeoff/external/ + the case study.
# pr-split — group the current branch's commits into concern-grouped PRs (one PR
# per concern). Pure story: all logic is in stories/pr-split (deterministic git +
# one fenced bucketer agent); this target is just the entry point. Run from the
# checkout whose branch you want to split.
pr-split:
	go run ./cmd/kitsoki run stories/pr-split/app.yaml

# Flow-test the pr-split story (no LLM, no cost).
pr-split-test:
	go run ./cmd/kitsoki validate stories/pr-split/app.yaml
	go run ./cmd/kitsoki test flows stories/pr-split/app.yaml

qs-bakeoff: install
	python3 tools/bugfix-bakeoff/external/bench_grade_test.py
	python3 tools/bugfix-bakeoff/external/bench.py lint-oracles --project kitsoki --strict
	go test -tags qsbakeoff -run TestExternalBakeoff -count=1 -v ./tools/bugfix-bakeoff/external/

repo-history-capsules:
	go run ./cmd/kitsoki capsule list --kind repo-history --markdown

oracle-capsules: repo-history-capsules

# gears-bakeoff arms the gears-rust corpus (projects/gears-rust): prove each
# captured fixture's hidden oracle is RED at its baseline and GREEN after the real
# fix's source, against a LOCAL checkout (gears-rust is heavy + private, never
# network-cloned). DETERMINISTIC, no LLM. Point BUGFIX_BAKEOFF_REPO at a local
# checkout; needs git + cargo + python3. Skips cleanly when BUGFIX_BAKEOFF_REPO unset.
#   BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-bakeoff
gears-bakeoff:
	go test -tags gearsbakeoff -run TestGearsBakeoff -count=1 -v ./tools/bugfix-bakeoff/external/

# history-smoke is the free product-path smoke for any external repo-history
# training manifest: harness unit checks, candidate/profile preflight, scoped
# RED/GREEN arming, exact drive-command rendering, and repo-bakeoff story flow
# validation. It never calls a real LLM.
#   make history-smoke HISTORY_PROJECT=query-string HISTORY_BUGS=qs1 HISTORY_CANDIDATES=gpt-5.5
#   make history-smoke HISTORY_PROJECT=gears-rust HISTORY_REPO_DIR=~/code/gears-rust HISTORY_BUGS=bug1 HISTORY_CANDIDATES=opus-4.8
HISTORY_PROJECT ?=
HISTORY_REPO_DIR ?=
HISTORY_BUGS ?=
HISTORY_CANDIDATES ?=
HISTORY_PREPARE_FIRST_CELL ?= 1
HISTORY_PREPARE_ALL_CELLS ?= 0
HISTORY_PENDING_REASON ?= provider/profile blocked before model attempt
history-smoke:
	@test -n "$(HISTORY_PROJECT)" || (echo "HISTORY_PROJECT is required"; exit 1)
	@test -n "$(HISTORY_BUGS)" || (echo "HISTORY_BUGS is required"; exit 1)
	@test -n "$(HISTORY_CANDIDATES)" || (echo "HISTORY_CANDIDATES is required"; exit 1)
	python3 tools/bugfix-bakeoff/external/bench_grade_test.py
	@mkdir -p .artifacts/history-training/readiness
	GOCACHE="$(KITSOKI_GOCACHE)" go run ./cmd/kitsoki history task-cases adapt-bugfix "tools/bugfix-bakeoff/external/projects/$(HISTORY_PROJECT)/manifest.yaml" --bug "$(HISTORY_BUGS)" --validate > ".artifacts/history-training/readiness/$(HISTORY_PROJECT)-task-cases.json"
	GOCACHE="$(KITSOKI_GOCACHE)" go run ./cmd/kitsoki history task-cases validate tools/history-training/examples
	@if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		python3 tools/bugfix-bakeoff/external/bench.py preflight --project "$(HISTORY_PROJECT)" --bug "$(HISTORY_BUGS)" $$repo_arg --candidate "$(HISTORY_CANDIDATES)"
	@if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		python3 tools/bugfix-bakeoff/external/bench.py verify --project "$(HISTORY_PROJECT)" --bug "$(HISTORY_BUGS)" $$repo_arg
	@if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		python3 tools/bugfix-bakeoff/external/bench.py drive-plan --project "$(HISTORY_PROJECT)" --bug "$(HISTORY_BUGS)" $$repo_arg --candidate "$(HISTORY_CANDIDATES)"
	@if [ "$(HISTORY_PREPARE_FIRST_CELL)" = "1" ] || [ "$(HISTORY_PREPARE_ALL_CELLS)" = "1" ]; then \
		bugs="$(HISTORY_BUGS)"; candidates="$(HISTORY_CANDIDATES)"; \
		if [ "$(HISTORY_PREPARE_ALL_CELLS)" != "1" ]; then bugs="$${bugs%%,*}"; candidates="$${candidates%%,*}"; fi; \
		if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		cache="$${EXTERNAL_BAKEOFF_CACHE:-.artifacts/external-bakeoff}"; \
		mkdir -p .artifacts/external-bakeoff/readiness; \
		handoff_json=".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT)-handoffs.json"; \
		if ! tools/bugfix-bakeoff/external/prepare_handoffs.sh --project "$(HISTORY_PROJECT)" --bug "$$bugs" --candidate "$$candidates" $$repo_arg --markdown ".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT)-handoffs.md" > "$$handoff_json"; then \
			cat "$$handoff_json"; \
			exit 1; \
		fi; \
		cat "$$handoff_json"; \
		for bug in $$(printf '%s' "$$bugs" | tr ',' ' '); do \
			for candidate in $$(printf '%s' "$$candidates" | tr ',' ' '); do \
				python3 -c 'import json, sys; path, bug = sys.argv[1:3]; data = json.load(open(path)); assert data.get("bugs") == [bug], "cell preflight should be scoped to %s, got %r" % (bug, data.get("bugs"))' "$$cache/preflight/$(HISTORY_PROJECT)-$$bug-$$candidate.json" "$$bug"; \
				python3 -c 'import json, pathlib, sys; path, project, bug, candidate = sys.argv[1:5]; data = json.load(open(path)); assert data.get("project") == project and data.get("bug") == bug and data.get("candidate") == candidate, data; assert pathlib.Path(data["prompt"]).exists(), data["prompt"]; assert pathlib.Path(data["worktree"]).exists(), data["worktree"]; assert pathlib.Path(data["preflight"]).exists(), data["preflight"]' "$$cache/prepared/$(HISTORY_PROJECT)-$$bug-$$candidate.json" "$(HISTORY_PROJECT)" "$$bug" "$$candidate"; \
			done; \
		done; \
	fi
	@mkdir -p .artifacts/external-bakeoff/readiness
	@if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		readiness_json=".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT).json"; \
		if ! python3 tools/bugfix-bakeoff/external/bench.py readiness --project "$(HISTORY_PROJECT)" --bug "$(HISTORY_BUGS)" $$repo_arg --candidate "$(HISTORY_CANDIDATES)" --armed --markdown ".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT).md" > "$$readiness_json"; then \
			cat "$$readiness_json"; \
			exit 1; \
		fi; \
		cat "$$readiness_json"; \
		if [ "$(HISTORY_PREPARE_ALL_CELLS)" = "1" ]; then \
			python3 -c 'import json, sys; data = json.load(open(sys.argv[1])); results = data["results"]; assert results["prepared_cells"] == results["selected_cells"], results; assert results["stale_prepared_cells"] == 0, results; assert results["unprepared_cells"] == 0, results' "$$readiness_json"; \
		fi
	@if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		completion_json=".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT)-completion.json"; \
		if ! python3 tools/bugfix-bakeoff/external/bench.py completion --project "$(HISTORY_PROJECT)" --bug "$(HISTORY_BUGS)" $$repo_arg --candidate "$(HISTORY_CANDIDATES)" --armed --markdown ".artifacts/external-bakeoff/readiness/$(HISTORY_PROJECT)-completion.md" > "$$completion_json"; then \
			cat "$$completion_json"; \
			exit 1; \
		fi; \
		cat "$$completion_json"; \
		python3 -c 'import json, sys; data = json.load(open(sys.argv[1])); checks = data["checks"]; assert checks["no_cost_ready"], data; assert data["results"]["stale_result_cells"] == 0, data' "$$completion_json"; \
		if [ "$(HISTORY_PREPARE_ALL_CELLS)" = "1" ]; then \
			python3 -c 'import json, sys; data = json.load(open(sys.argv[1])); checks = data["checks"]; assert checks["ready_to_drive"], data' "$$completion_json"; \
		fi
	GOCACHE="$(KITSOKI_GOCACHE)" go run ./cmd/kitsoki validate stories/repo-bakeoff/app.yaml
	GOCACHE="$(KITSOKI_GOCACHE)" go run ./cmd/kitsoki test flows stories/repo-bakeoff/app.yaml

# history-pending-smoke proves the no-cost blocked-provider path without
# modifying the normal live results dir: write one pending cell in a temp result
# root, summarize it, and render Markdown + Slidey JSON from that pending result.
history-pending-smoke:
	@test -n "$(HISTORY_PROJECT)" || (echo "HISTORY_PROJECT is required"; exit 1)
	@test -n "$(HISTORY_BUGS)" || (echo "HISTORY_BUGS is required"; exit 1)
	@test -n "$(HISTORY_CANDIDATES)" || (echo "HISTORY_CANDIDATES is required"; exit 1)
	@tmp="$$(mktemp -d)"; \
		first_bug="$(HISTORY_BUGS)"; first_bug="$${first_bug%%,*}"; \
		first_candidate="$(HISTORY_CANDIDATES)"; first_candidate="$${first_candidate%%,*}"; \
		cell="$$tmp/results/cells/$(HISTORY_PROJECT)-$$first_bug-$$first_candidate-kitsoki.json"; \
		python3 tools/bugfix-bakeoff/external/bench.py pending --project "$(HISTORY_PROJECT)" --bug "$$first_bug" --candidate "$$first_candidate" --reason "$(HISTORY_PENDING_REASON)" --out "$$cell"; \
		rel="$$(python3 -c 'import os,sys; print(os.path.relpath(sys.argv[1], os.path.join(os.getcwd(), "tools/bugfix-bakeoff/external")))' "$$tmp/results")"; \
		python3 tools/bugfix-bakeoff/external/bench.py summarize --project "$(HISTORY_PROJECT)" --results "$$rel" --deck "$$tmp/deck.slidey.json" --markdown "$$tmp/report.md"; \
		if [ -n "$(HISTORY_REPO_DIR)" ]; then repo_arg="--repo-dir $(HISTORY_REPO_DIR)"; else repo_arg=""; fi; \
		python3 tools/bugfix-bakeoff/external/bench.py completion --project "$(HISTORY_PROJECT)" --bug "$$first_bug" $$repo_arg --candidate "$$first_candidate" --results "$$rel" --armed --markdown "$$tmp/completion.md" > "$$tmp/completion.json"; \
		python3 -c 'import json, sys; data = json.load(open(sys.argv[1])); checks = data["checks"]; results = data["results"]; assert data["status"] == "complete-with-pending", data; assert checks["result_evidence_complete"], data; assert not checks["live_scored"], data; assert results["pending_cells"] == 1, data; assert results["attempted_cells"] == 0, data' "$$tmp/completion.json"; \
		python3 -m json.tool "$$tmp/deck.slidey.json" >/dev/null; \
		echo "pending smoke report: $$tmp/report.md"; \
		sed -n '1,120p' "$$tmp/report.md"; \
		echo "pending completion: $$tmp/completion.md"; \
		sed -n '1,80p' "$$tmp/completion.md"

# gears-history-smoke is the reference private/heavy repo wrapper around the
# generic history-smoke target. Override the bug/candidate matrix to match the
# live cell you intend to run.
#   BUGFIX_BAKEOFF_REPO=/path/to/checkout make gears-history-smoke
GEARS_HISTORY_BUGS ?= bug1
GEARS_HISTORY_CANDIDATES ?= opus-4.8
gears-history-smoke:
	@test -n "$(BUGFIX_BAKEOFF_REPO)" || (echo "BUGFIX_BAKEOFF_REPO must point at the local project checkout"; exit 1)
	$(MAKE) history-smoke HISTORY_PROJECT=gears-rust HISTORY_REPO_DIR="$(BUGFIX_BAKEOFF_REPO)" HISTORY_BUGS="$(GEARS_HISTORY_BUGS)" HISTORY_CANDIDATES="$(GEARS_HISTORY_CANDIDATES)"

# gears-history-full-smoke is the no-cost full-corpus proof for the armable
# gears-rust fixtures. It verifies all four RED/GREEN oracles, renders the full
# live command matrix, prepares every selected prompt/worktree, validates
# deterministic pending report/deck generation, and runs the repo-bakeoff flow
# checks.
gears-history-full-smoke:
	@test -n "$(BUGFIX_BAKEOFF_REPO)" || (echo "BUGFIX_BAKEOFF_REPO must point at the local project checkout"; exit 1)
	$(MAKE) history-smoke HISTORY_PROJECT=gears-rust HISTORY_REPO_DIR="$(BUGFIX_BAKEOFF_REPO)" HISTORY_BUGS="bug1,bug4,bug5,bug9" HISTORY_CANDIDATES="$(GEARS_HISTORY_CANDIDATES)" HISTORY_PREPARE_ALL_CELLS=1
	$(MAKE) history-pending-smoke HISTORY_PROJECT=gears-rust HISTORY_REPO_DIR="$(BUGFIX_BAKEOFF_REPO)" HISTORY_BUGS="bug1,bug4,bug5,bug9" HISTORY_CANDIDATES="$(GEARS_HISTORY_CANDIDATES)" HISTORY_PENDING_REASON="provider/profile blocked before model attempt"

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
# sixth suite so `make test` / CI guard it; run standalone for a fast loop.
mining-test:
	@rc=0; for t in tools/session-mining/tests/test_*.py; do \
		printf '\n-- %s\n' "$$t"; python3 "$$t" || rc=1; \
	done; exit $$rc

# cost-report-test — alias kept for the cost case study's docs; mining-test is
# the canonical target (it's a superset: cost + coverage + pipeline invariants).
cost-report-test: mining-test

# starcheck-kitsoki is the static host.starlark.run pre-flight: it runs the
# starcheck tool's -kitsoki profile (predeclared={json,math,yaml}, strict dialect,
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
# Exit code: 0 when the suite is green. `session continue` reports terminal
# story exits as `__exit__<name>` rather than the preceding terminal room, so
# a successful review arrives here as `__exit__achieved`; all other exits are
# nonzero and the report says which, with any open questions.
.PHONY: fix-tests
FIX_TESTS_APP := stories/fix-tests/app.yaml
FIX_TESTS_TEST_CMD ?= make test
# `make test` is the authoritative gate and routinely exceeds the story's
# 180-second quick-loop default. The standalone entry point must give its
# first run the same budget as the full gate; callers with a genuinely quick
# reproducer can override this command and timeout explicitly.
FIX_TESTS_QUICK_TEST_CMD ?= go test -short ./...
FIX_TESTS_QUICK_TIMEOUT_SECONDS ?= 300
FIX_TESTS_FULL_TIMEOUT_SECONDS ?= 1200
FIX_TESTS_MAX_CYCLES ?= 3
# This is only the outer session envelope. Agent liveness is governed by the
# story's much shorter sandbox activity_timeout, so a real repair cannot sit
# silently for this whole period.
FIX_TESTS_SESSION_DRAIN_TIMEOUT ?= 90m
fix-tests:
	@command -v jq >/dev/null 2>&1 || { echo "error: jq is required for 'make fix-tests'" >&2; exit 1; }
	@fix_tests_gocache="$${KITSOKI_FIX_TESTS_GOCACHE:-$${GOCACHE:-$$(pwd)/.temp/go-build}}"; mkdir -p "$$fix_tests_gocache"; GOCACHE="$$fix_tests_gocache" go build -o ./.kitsoki-fixtests $(PKG)
	@fix_tests_gocache="$${KITSOKI_FIX_TESTS_GOCACHE:-$${GOCACHE:-$$(pwd)/.temp/go-build}}"; mkdir -p "$$fix_tests_gocache"; export GOCACHE="$$fix_tests_gocache"; \
	 tmpdir=$$(mktemp -d "$${TMPDIR:-/tmp}/kitsoki-fixtests.XXXXXX"); \
	 db="$$tmpdir/session.db"; \
	 marker="$$tmpdir/report-marker"; touch "$$marker"; \
	 report_dir=.artifacts/fix-tests/runs/$$(date +%Y%m%dT%H%M%S)-$$$$; mkdir -p "$$report_dir"; \
	 trap 'rm -f ./.kitsoki-fixtests; rm -rf "$$tmpdir"' EXIT; \
	 slots=$$(jq -cn \
	   --arg test_cmd "$(FIX_TESTS_TEST_CMD)" \
	   --arg quick_test_cmd "$(FIX_TESTS_QUICK_TEST_CMD)" \
	   --argjson quick_test_timeout_seconds "$(FIX_TESTS_QUICK_TIMEOUT_SECONDS)" \
	   --argjson full_test_timeout_seconds "$(FIX_TESTS_FULL_TIMEOUT_SECONDS)" \
	   --argjson max_cycles "$(FIX_TESTS_MAX_CYCLES)" \
	   --arg report_dir "$$report_dir" \
	   '{test_cmd: $$test_cmd, quick_test_cmd: $$quick_test_cmd, quick_test_timeout_seconds: $$quick_test_timeout_seconds, full_test_timeout_seconds: $$full_test_timeout_seconds, max_cycles: $$max_cycles, report_dir: $$report_dir}'); \
	 sid=$$(./.kitsoki-fixtests session create --app $(FIX_TESTS_APP) --db "$$db" | jq -r .session_id); \
	 echo "fix-tests: driving session $$sid"; \
	 echo "fix-tests: quick gate: $(FIX_TESTS_QUICK_TEST_CMD) ($(FIX_TESTS_QUICK_TIMEOUT_SECONDS)s timeout)"; \
	 echo "fix-tests: full gate: $(FIX_TESTS_TEST_CMD) ($(FIX_TESTS_FULL_TIMEOUT_SECONDS)s timeout)"; \
	 echo "fix-tests: auto-fixing with claude (sonnet) — this may take a while…"; \
	 out=$$(./.kitsoki-fixtests session continue --app $(FIX_TESTS_APP) --db "$$db" --id "$$sid" --intent start --mode one-shot --drain-timeout "$(FIX_TESTS_SESSION_DRAIN_TIMEOUT)" --slots "$$slots"); \
	 state=$$(printf '%s' "$$out" | jq -r .new_state); \
	 echo; echo "fix-tests: final state = $$state"; \
	 report=$$(find "$$report_dir" -maxdepth 1 -name 'report-*.md' -newer "$$marker" -print 2>/dev/null | head -1); \
	 if [ -n "$$report" ]; then echo; echo "──────── $$report ────────"; cat "$$report"; echo "─────────────────────────"; fi; \
	 case "$$state" in \
	   __exit__achieved) echo "fix-tests: PASS — suite is green."; exit 0;; \
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

# Feature catalog: the project object graph's public site-page nodes
# (docs/proposals/project-object-graph/seed-objects.yaml) are the single
# source of truth for feature content — tour steps, demo bindings,
# promo/docs metadata, and ui-qa scenarios. `kitsoki graph render-features`
# regenerates features/*.yaml from the graph (W3.1); the committed tour
# manifests under tools/runstatus/src/tour/generated/ are CODE-GENERATED
# from THAT in turn. Edit graph nodes, not features/*.yaml by hand.
#   features        render features/*.yaml from the graph, then regenerate
#                   the manifests + features/feature.schema.json
#   features-check  fail if features/*.yaml has drifted from the graph, or
#                   any generated file is stale (runs inside `make build`
#                   and `make test` — a stale manifest can never be
#                   embedded into the binary)
#   features-index  emit the site/QA contract to .artifacts/features/
OBJECT_GRAPH_CATALOG := docs/proposals/project-object-graph/seed-objects.yaml
.PHONY: features features-check features-index media-check media-check-promo demo-feature demo-feature-rrweb feature-qa \
	arena-treatments arena-showdown-plan arena-showdown-run arena-showdown-live
features:
	go run ./cmd/kitsoki graph render-features $(OBJECT_GRAPH_CATALOG) features
	$(call runstatus_pnpm_install,--silent)
	cd $(RUNSTATUS_DIR) && pnpm features:gen

features-check:
	go run ./cmd/kitsoki graph render-features $(OBJECT_GRAPH_CATALOG) features --check
	$(call runstatus_pnpm_install,--silent)
	cd $(RUNSTATUS_DIR) && pnpm features:check

# vitest-check runs the runstatus (web UI) component/unit test suite by itself.
# The same suite is part of `make test` / `make test-full`; keep this target for
# the fast frontend loop and for checking the pnpm dependency install in
# isolation.
vitest-check:
	$(call runstatus_pnpm_install,--silent)
	cd $(RUNSTATUS_DIR) && pnpm test

# usable-kitsoki-gate-check runs the no-LLM half of the usable-kitsoki
# release gate (docs/proposals/usable-kitsoki-release-gate.md Task 5.1):
# the arena plugin/schema/golden-fixture suite plus the 18-scenario
# calibration sweep, regenerated and diffed against the checked-in report.
# Every test here is cassette/flow-replay only (`go run ./cmd/kitsoki test
# flows`, never a real LLM) — see AGENTS.md's "never use a real LLM or
# incur costs" rule. Wired into .github/workflows/usable-kitsoki-gate.yml's
# `no-llm-gate` job with path filters on the S1/S2/S4/S5 code it exercises.
.PHONY: usable-kitsoki-gate-check
usable-kitsoki-gate-check:
	python3 tools/arena/tests/test_usable_kitsoki_gate_schema.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_plugin.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_corpus.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_golden_fixtures.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_live_gate.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_live_calibration.py
	python3 tools/arena/tests/test_usable_kitsoki_gate_calibration.py
	# WS-G G1 — completion-state check_type discriminator + arena check-suite
	# plumbing (the schema every gate cell reports through).
	python3 tools/arena/tests/test_check_types.py

# ── Arena treatment UX + CodeAct-vs-Codex arena run ─────────────────────────
ARENA_SHOWDOWN_SPEC ?= tools/arena/specs/codex-codeact-action-surface.yaml
ARENA_SHOWDOWN_OUT ?= .artifacts/arena/codeact-showdown

arena-treatments:
	python3 tools/arena/arena.py treatments --aliases

arena-showdown-plan:
	python3 tools/arena/arena.py validate --spec $(ARENA_SHOWDOWN_SPEC)
	python3 tools/arena/arena.py treatments
	python3 tools/arena/arena.py plan --spec $(ARENA_SHOWDOWN_SPEC)

# No-LLM/default arena run. This still needs Docker because arena cells run in
# containers, but it does not call a model.
arena-showdown-run:
	python3 tools/arena/arena.py validate --spec $(ARENA_SHOWDOWN_SPEC)
	python3 tools/arena/arena.py run --spec $(ARENA_SHOWDOWN_SPEC) --out $(ARENA_SHOWDOWN_OUT)

# Paid live path: intentionally gated by both validate --live and arena --live.
arena-showdown-live:
	python3 tools/arena/arena.py validate --spec $(ARENA_SHOWDOWN_SPEC) --live
	python3 tools/arena/arena.py doctor --spec $(ARENA_SHOWDOWN_SPEC) --live
	python3 tools/arena/arena.py run --spec $(ARENA_SHOWDOWN_SPEC) --out $(ARENA_SHOWDOWN_OUT) --live

# dev-workflow-matrix regenerates the 5-workflow x 4-surface x 2-repo
# support matrix (docs/testing/dev-workflow-matrix.md) from its hand-edited
# manifest plus any standing completion-state verdict files (WS-F F1 of
# .context/dev-workflows-surface-matrix-plan.md). The generated file carries
# a DO-NOT-HAND-EDIT header; edit tools/dev-workflow-matrix/manifest.yaml
# and rerun this target. Deterministic, no LLM, no network.
.PHONY: dev-workflow-matrix dev-workflow-matrix-check
dev-workflow-matrix:
	python3 tools/dev-workflow-matrix/generate.py --out docs/testing/dev-workflow-matrix.md

dev-workflow-matrix-check:
	python3 tools/dev-workflow-matrix/generate_test.py
	python3 tools/dev-workflow-matrix/run_checks_test.py
	# WS-G G1 experience-check runners (docs-fidelity + ux-heuristic) — fully
	# mocked, DI'd agent dispatch, no LLM/network/subprocess.
	python3 tools/dev-workflow-matrix/docs_fidelity_test.py
	python3 tools/dev-workflow-matrix/ux_heuristic_test.py

# dev-workflow-experience-list enumerates the declared docs-fidelity /
# ux-heuristic experience checks (WS-G G1) without dispatching any agent —
# the --list entry point both runners expose alongside --dry-run.
.PHONY: dev-workflow-experience-list
dev-workflow-experience-list:
	python3 tools/dev-workflow-matrix/docs_fidelity.py --list
	python3 tools/dev-workflow-matrix/ux_heuristic.py --list

.PHONY: roadmap-ledger-check
roadmap-ledger-check:
	go run ./cmd/kitsoki roadmap ledger check --ledger .artifacts/roadmap/progress.yaml --repo-root . --strict

# dev-workflow-gate is the WS-F F1 exit criterion: "make target / CI job that
# prints the live matrix; a red cell blocks declaring the workflow
# supported." Runs the check suite (real no-LLM story-flow-suite + a
# product-journey smoke replay) into a scratch verdicts dir, prints the LIVE
# matrix report (verdict-aware, NOT the checked-in doc — that stays
# manifest-only and deterministic), and exits non-zero if any cell the
# manifest marks `works` has regressed against a real verdict. Deterministic
# story replays, no LLM, no network, no docker.
.PHONY: dev-workflow-gate
DEV_WORKFLOW_GATE_VERDICTS_DIR ?= .artifacts/dev-workflow-matrix/verdicts
dev-workflow-gate:
	python3 tools/dev-workflow-matrix/run_checks.py --verdicts-dir $(DEV_WORKFLOW_GATE_VERDICTS_DIR)
	python3 tools/dev-workflow-matrix/generate.py --out .artifacts/dev-workflow-matrix/dev-workflow-matrix.live.md \
		--verdicts-dir $(DEV_WORKFLOW_GATE_VERDICTS_DIR) --gate
	python3 tools/dev-workflow-matrix/generate.py --out docs/testing/dev-workflow-matrix.md

features-index:
	$(call runstatus_pnpm_install,--silent)
	cd $(RUNSTATUS_DIR) && pnpm features:index

media-check: features-index
	$(SITE_ENV) node $(SITE_DIR)/scripts/check-media.mjs --index .artifacts/features/features-index.json

# media-check-promo is the hard presence gate: run AFTER `make site` (which
# stages src/public/media/ via its prebuild step) so every promo-grid feature
# (a `promo:` block in features/<id>.yaml — the landing-page set) is proven to
# have real staged media, not just shape-valid paths. Non-promo features
# missing media only warn. CI wires this in as a non-continue-on-error step.
media-check-promo: features-index
	$(SITE_ENV) node $(SITE_DIR)/scripts/check-media.mjs --index .artifacts/features/features-index.json --require-promo-media

# demo-feature is the legacy MP4 fallback for one feature. Prefer
# demo-feature-rrweb unless the surface cannot be captured as rrweb or the
# explicit deliverable is a rendered video export.
# Usage: make demo-feature FEATURE=agent-actions
FEATURE ?=
demo-feature: build-bin
	@test -n "$(FEATURE)" || { echo "usage: make demo-feature FEATURE=<id>" >&2; exit 1; }
	$(call runstatus_pnpm_install,--silent)
	@set -e; \
	demo=$$(cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/generate.ts --print-demo $(FEATURE)); \
	spec=$$(printf '%s' "$$demo" | cut -f1); \
	video=$$(printf '%s' "$$demo" | cut -f3); \
	(cd $(RUNSTATUS_DIR) && pnpm exec playwright test "$$spec" --project=chromium); \
	.agents/skills/kitsoki-ui-demo/scripts/render.sh "$$video"

# demo-feature-rrweb captures ONE feature's rrweb tour and bundles a
# self-contained Slidey HTML viewer. It skips the MP4 replay-render step.
# Usage: make demo-feature-rrweb FEATURE=web-inbox
demo-feature-rrweb: build-bin features-index
	@test -n "$(FEATURE)" || { echo "usage: make demo-feature-rrweb FEATURE=<id>" >&2; exit 1; }
	$(call runstatus_pnpm_install,--silent)
	@set -e; \
	demo=$$(cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/generate.ts --print-demo-rrweb $(FEATURE)); \
	spec=$$(printf '%s' "$$demo" | cut -f1); \
	rrweb=$$(printf '%s' "$$demo" | cut -f3); \
	viewer=$$(printf '%s' "$$demo" | cut -f4); \
	rrweb_abs="$$(pwd)/$$rrweb"; \
	(cd $(RUNSTATUS_DIR) && KITSOKI_RRWEB_OUT="$$rrweb_abs" WEB_CHAT_PACE=1 pnpm exec playwright test "$$spec" --project=chromium); \
	bash scripts/build-rrweb-viewer.sh "$$rrweb" "$$viewer"

# feature-qa is the legacy MP4 visual-QA path. For rrweb-first demos, use
# captured step PNGs or an explicit rendered-video export with kitsoki-ui-qa.
# GATED: drives the real `claude` CLI — never run automatically
# (CLAUDE.md LLM-test policy).
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
# The master is stitched (no direct capture spec), so it can't go through
# feature-qa; this drives qa.sh on the legacy master video + generated scenarios.
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
MCP_QA_FEATURE ?= .agents/skills/kitsoki-ui-qa/templates/mcp-feature.md
MCP_QA_SCENARIOS ?= .agents/skills/kitsoki-ui-qa/templates/mcp-scenarios.yaml
mcp-qa:
	@rm -rf .artifacts/mcp-demo/frames && mkdir -p .artifacts/mcp-demo/frames
	@cp .artifacts/mcp-demo/0*-*.png .artifacts/mcp-demo/frames/ 2>/dev/null || true
	.agents/skills/kitsoki-ui-qa/scripts/qa.sh $(MCP_QA_VIDEO) \
		--feature $(MCP_QA_FEATURE) \
		--scenarios $(MCP_QA_SCENARIOS) \
		--frames .artifacts/mcp-demo/frames \
		--blank-min-coverage 0.18

# ── TUI live pty-over-websocket bridge (real keystrokes in, real ANSI render
# out) — the live counterpart to the mcp-demo cassette replay above. See
# tools/tui-bridge/README.md.
TUI_BRIDGE_DIR := tools/tui-bridge
.PHONY: tui-bridge-deps tui-bridge-test

tui-bridge-deps:
	cd $(TUI_BRIDGE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(TUI_BRIDGE_DIR) && pnpm exec playwright install chromium

# No-LLM by construction: the bridge spawns /bin/cat for the test, never
# kitsoki or an LLM. Exercises the Go server + browser page together.
tui-bridge-test: tui-bridge-deps
	cd $(TUI_BRIDGE_DIR) && pnpm exec playwright test

# demos captures every recordable feature demo at watch-speed, incrementally:
# per-demo content stamps (feature YAML + spec + story inputs + binary) skip
# anything unchanged. demos-force recaptures everything. See
# scripts/record-demos.sh for the stamp design.
.PHONY: demos demos-force site-full
demos: build-bin features-index
	$(call runstatus_pnpm_install,--silent)
	./scripts/record-demos.sh

demos-force: build-bin features-index
	$(call runstatus_pnpm_install,--silent)
	./scripts/record-demos.sh --force

# site-full is the everything path: capture any stale demos, then build the
# Pages site from them. (What the CI deploy effectively runs.)
site-full: demos site

# ── render: one friendly, extensible front door for local artifacts ─────────
# The local media/docs generation machinery already exists (demos /
# demo-feature-rrweb / site), but under names you have to know. `make render`
# is the discoverable entrypoint for "generate the things people watch and read,
# locally": rrweb demo media and docs. Legacy rendered-video exports are
# explicit opt-ins only.
#
# It is reuse-first — every render-* target delegates to the underlying target
# so there is exactly one implementation each. The media path stays incremental
# (per-demo content stamps skip anything unchanged — see scripts/record-demos.sh),
# so re-running `make render` after a docs-only edit captures nothing.
#
# To grow it: add a render-<kind> target below and list it in `make render-help`.
.PHONY: render render-help render-videos render-video render-docs render-tour render-all
FORCE ?=

render: render-videos
	@echo
	@echo "rendered demo media -> .artifacts/<id>/"
	@echo "more: 'make render-help' (single feature, docs, everything)"

render-help:
	@echo "render targets:"
	@echo "  make render                     capture every stale demo media artifact"
	@echo "  make render FORCE=1             recapture every stale demo media artifact"
	@echo "  make demo-feature-rrweb FEATURE=<id>  one feature: rrweb + HTML viewer"
	@echo "  make render-video FEATURE=<id>  legacy fallback: MP4 + GIF + contact sheet"
	@echo "  make render-tour                legacy export: stitch existing MP4 sections into old master tour"
	@echo "  make render-docs                build the promo site + help docs"
	@echo "  make render-all                 demo replays + docs"

# render-videos delegates to the incremental demos pipeline (FORCE=1 -> force).
render-videos:
	@$(MAKE) $(if $(FORCE),demos-force,demos)

# render-video captures ONE feature's legacy fallback video and renders its GIF +
# contact sheet.
render-video:
	@$(MAKE) demo-feature FEATURE=$(FEATURE)

# render-tour stitches pre-existing per-section legacy MP4 exports into the old
# complete-product-tour master. It is not part of normal catalog media
# generation or CI; create any required section videos explicitly with
# `make render-video FEATURE=<id>` before running this target.
render-tour: features-index
	@echo "render-tour: legacy MP4 export; catalog demos are rrweb-first and CI does not run this target"
	@cd $(RUNSTATUS_DIR) && pnpm exec tsx scripts/features/stitch-tour.mjs complete-product-tour

# render-docs builds the VitePress promo site + help docs from whatever demo
# media has been captured (missing media degrades to a poster, never a failure).
render-docs:
	@$(MAKE) site

# render-all is the everything path: refresh rrweb demo media, then build the site.
render-all:
	@$(MAKE) demos
	@$(MAKE) site

# ── Promo site + help docs (tools/site, VitePress) ──────────────────────────
# One source tree, two variants: the GitHub Pages site (full replay viewers and
# fallback videos, base $(SITE_BASE)) and the binary-embedded /help/ copy (posters only — built by
# site-embed in a later phase). Generated media is NEVER committed: `make demos`
# captures it into .artifacts/ and `site` stages whatever exists — missing media
# degrades to a poster + placeholder, never a build failure.
SITE_DIR  := tools/site
SITE_BASE ?= /Kitsoki/
SITE_ABS  := $(abspath $(SITE_DIR))
SITE_WORKSPACE ?= $(abspath $(TEMP_DIR)/site)
SITE_ENV := TMPDIR="$(abspath $(TEMP_DIR))" KITSOKI_TEMP_ROOT="$(abspath $(TEMP_DIR))" \
	KITSOKI_REPO_ROOT="$(CURDIR)" KITSOKI_SITE_SOURCE_ROOT="$(SITE_ABS)" \
	KITSOKI_SITE_ROOT="$(SITE_WORKSPACE)"
SITE_VITEPRESS := $(SITE_WORKSPACE)/node_modules/.bin/vitepress

.PHONY: site site-data site-deps site-stage site-workspace site-dev site-clean
site-workspace:
	$(SITE_ENV) node $(SITE_DIR)/scripts/prepare-workspace.mjs

site-deps: site-workspace
	cd "$(SITE_WORKSPACE)" && pnpm install --frozen-lockfile --silent

# site-data emits the feature-catalog contract (features-index.json + QA files)
# into the temp VitePress workspace's gen/ dir.
site-data: site-workspace
	$(call runstatus_pnpm_install,--silent)
	mkdir -p "$(SITE_WORKSPACE)/.vitepress/gen"
	cd $(RUNSTATUS_DIR) && $(RUNSTATUS_TEMP_ENV) pnpm exec tsx scripts/features/generate.ts --index --out "$(SITE_WORKSPACE)/.vitepress/gen"

site-stage: site-data
	$(SITE_ENV) node $(SITE_DIR)/scripts/stage-docs.mjs
	$(SITE_ENV) node $(SITE_DIR)/scripts/stage-media.mjs
	$(SITE_ENV) node $(SITE_DIR)/scripts/stage-extensions.mjs

site: site-deps site-stage
	$(SITE_ENV) SITE_BASE=$(SITE_BASE) "$(SITE_VITEPRESS)" build "$(SITE_WORKSPACE)"
	node $(SITE_DIR)/scripts/check-leaks.mjs "$(SITE_WORKSPACE)/dist"
	@echo "site built -> $(SITE_WORKSPACE)/dist"

# site-dev prepares the writable .temp/site workspace, then runs the VitePress
# dev server from there. Rerun after changing source docs/config so source stays
# read-only and Vite's loader scratch files remain under .temp.
site-dev: site-deps site-stage
	$(SITE_ENV) "$(SITE_VITEPRESS)" dev "$(SITE_WORKSPACE)"

# site-embed builds the EMBEDDED variant (base /help/, posters only — no MP4s
# in the binary) and stages it into internal/helpdocs/assets/ so the next
# `make build` serves it offline at /help/. ~2-4MB on the binary.
HELPDOCS_ASSETS := internal/helpdocs/assets
.PHONY: site-embed
site-embed: site-deps site-stage
	$(SITE_ENV) SITE_BASE=/help/ SITE_VARIANT=embedded "$(SITE_VITEPRESS)" build "$(SITE_WORKSPACE)"
	node $(SITE_DIR)/scripts/check-leaks.mjs "$(SITE_WORKSPACE)/dist-embedded" --embedded
	find $(HELPDOCS_ASSETS) -mindepth 1 ! -name .gitkeep -delete
	cp -R "$(SITE_WORKSPACE)/dist-embedded/." $(HELPDOCS_ASSETS)/
	@echo "help docs staged -> $(HELPDOCS_ASSETS) (next 'make build' embeds them at /help/)"

site-clean:
	rm -rf "$(SITE_WORKSPACE)"
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

check-vscode-code-cli:
	@command -v "$(CODE_CLI)" >/dev/null 2>&1 || { \
		echo "error: $(CODE_CLI) not found — install the VS Code shell command or run CODE_CLI=/path/to/code make vscode-install-local." >&2; \
		exit 1; }

vscode-stage-runstatus-temp:
	@rm -rf "$(VSCODE_RUNSTATUS_DIR)" "$(VSCODE_INSTALL_ROOT)/features" "$(VSCODE_INSTALL_ROOT)/docs" "$(VSCODE_INSTALL_ROOT)/stories" "$(VSCODE_INSTALL_ROOT)/testdata"
	@mkdir -p "$(VSCODE_RUNSTATUS_DIR)" "$(VSCODE_INSTALL_ROOT)" "$(VSCODE_INSTALL_TMP)"
	@ln -s "$(abspath features)" "$(VSCODE_INSTALL_ROOT)/features"
	@ln -s "$(abspath docs)" "$(VSCODE_INSTALL_ROOT)/docs"
	@ln -s "$(abspath stories)" "$(VSCODE_INSTALL_ROOT)/stories"
	@ln -s "$(abspath testdata)" "$(VSCODE_INSTALL_ROOT)/testdata"
	@cp "$(RUNSTATUS_DIR)/package.json" "$(VSCODE_RUNSTATUS_DIR)/package.json"
	@cp "$(RUNSTATUS_DIR)/pnpm-lock.yaml" "$(VSCODE_RUNSTATUS_DIR)/pnpm-lock.yaml"
	@cp "$(RUNSTATUS_DIR)/pnpm-workspace.yaml" "$(VSCODE_RUNSTATUS_DIR)/pnpm-workspace.yaml"
	@cp "$(RUNSTATUS_DIR)/vite.config.ts" "$(VSCODE_RUNSTATUS_DIR)/vite.config.ts"
	@cp "$(RUNSTATUS_DIR)/tsconfig.json" "$(VSCODE_RUNSTATUS_DIR)/tsconfig.json"
	@cp "$(RUNSTATUS_DIR)/tsconfig.app.json" "$(VSCODE_RUNSTATUS_DIR)/tsconfig.app.json"
	@cp "$(RUNSTATUS_DIR)/tsconfig.node.json" "$(VSCODE_RUNSTATUS_DIR)/tsconfig.node.json"
	@cp "$(RUNSTATUS_DIR)/vitest.config.ts" "$(VSCODE_RUNSTATUS_DIR)/vitest.config.ts"
	@cp "$(RUNSTATUS_DIR)/index.html" "$(VSCODE_RUNSTATUS_DIR)/index.html"
	@cp "$(RUNSTATUS_DIR)/snap.ts" "$(VSCODE_RUNSTATUS_DIR)/snap.ts"
	@cp -R "$(RUNSTATUS_DIR)/src" "$(VSCODE_RUNSTATUS_DIR)/src"
	@cp -R "$(RUNSTATUS_DIR)/scripts" "$(VSCODE_RUNSTATUS_DIR)/scripts"
	@cp -R "$(RUNSTATUS_DIR)/tests" "$(VSCODE_RUNSTATUS_DIR)/tests"
	@cp -R "$(RUNSTATUS_DIR)/public" "$(VSCODE_RUNSTATUS_DIR)/public"
	@cp -R "$(RUNSTATUS_DIR)/fixtures" "$(VSCODE_RUNSTATUS_DIR)/fixtures"

vscode-runstatus-spa-temp: check-deps
	@command -v pnpm >/dev/null 2>&1 || { \
		echo "error: pnpm not found — needed to build the runstatus SPA." >&2; \
		echo "       run 'make setup' to install Node + pnpm." >&2; \
		exit 1; }
	$(MAKE) --no-print-directory vscode-stage-runstatus-temp
	cd $(VSCODE_RUNSTATUS_DIR) && $(VSCODE_RUNSTATUS_TEMP_ENV) pnpm install --frozen-lockfile --silent
	cd $(VSCODE_RUNSTATUS_DIR) && $(VSCODE_RUNSTATUS_TEMP_ENV) ./node_modules/.bin/tsx scripts/features/generate.ts --check && $(VSCODE_RUNSTATUS_TEMP_ENV) ./node_modules/.bin/tsx scripts/features/lint-demos.ts
	cd $(VSCODE_RUNSTATUS_DIR) && $(VSCODE_RUNSTATUS_TEMP_ENV) ./node_modules/.bin/vite --configLoader runner build
	@echo "[vscode-install-local] staged runstatus SPA -> $(VSCODE_RUNSTATUS_DIST)/index.html"

vscode-stage-package-temp:
	@rm -rf "$(VSCODE_PACKAGE_DIR)" "$(VSCODE_VSIX_DIR)"
	@mkdir -p "$(VSCODE_PACKAGE_DIR)/media" "$(VSCODE_VSIX_DIR)"
	@cp "$(VSCODE_DIR)/.vscodeignore" "$(VSCODE_PACKAGE_DIR)/.vscodeignore"
	@cp "$(VSCODE_DIR)/LICENSE" "$(VSCODE_PACKAGE_DIR)/LICENSE"
	@cp "$(VSCODE_DIR)/README.md" "$(VSCODE_PACKAGE_DIR)/README.md"
	@cp "$(VSCODE_DIR)/esbuild.mjs" "$(VSCODE_PACKAGE_DIR)/esbuild.mjs"
	@cp "$(VSCODE_DIR)/package.json" "$(VSCODE_PACKAGE_DIR)/package.json"
	@cp "$(VSCODE_DIR)/pnpm-lock.yaml" "$(VSCODE_PACKAGE_DIR)/pnpm-lock.yaml"
	@cp "$(VSCODE_DIR)/pnpm-workspace.yaml" "$(VSCODE_PACKAGE_DIR)/pnpm-workspace.yaml"
	@cp "$(VSCODE_DIR)/tsconfig.json" "$(VSCODE_PACKAGE_DIR)/tsconfig.json"
	@cp -R "$(VSCODE_DIR)/src" "$(VSCODE_PACKAGE_DIR)/src"
	@cp "$(VSCODE_DIR)/media/icon.svg" "$(VSCODE_PACKAGE_DIR)/media/icon.svg"
	@cp "$(VSCODE_DIR)/media/icon-128.png" "$(VSCODE_PACKAGE_DIR)/media/icon-128.png"
	@cp "$(VSCODE_DIR)/media/logo.svg" "$(VSCODE_PACKAGE_DIR)/media/logo.svg"

vscode-package-temp: check-deps
	@if [ ! -f "$(VSCODE_RUNSTATUS_DIST)/index.html" ]; then $(MAKE) --no-print-directory vscode-runstatus-spa-temp; fi
	$(MAKE) --no-print-directory vscode-stage-package-temp
	cd $(VSCODE_PACKAGE_DIR) && pnpm install --frozen-lockfile --silent
	cd $(VSCODE_PACKAGE_DIR) && KITSOKI_RUNSTATUS_SPA="$(abspath $(VSCODE_RUNSTATUS_DIST)/index.html)" pnpm build
	cd $(VSCODE_PACKAGE_DIR) && pnpm dlx @vscode/vsce@^3 package --no-dependencies --out "$(abspath $(VSCODE_LOCAL_VSIX))"
	@echo "[vscode-package] $(VSCODE_LOCAL_VSIX)"

vscode-stage-embed-overlay-temp:
	@if [ ! -f "$(VSCODE_RUNSTATUS_DIST)/index.html" ]; then $(MAKE) --no-print-directory vscode-runstatus-spa-temp; fi
	@rm -rf "$(VSCODE_EMBED_ROOT)" "$(VSCODE_GO_OVERLAY)"
	@mkdir -p "$(VSCODE_EMBED_ROOT)/internal/runstatus/web/assets" "$(VSCODE_EMBED_ROOT)/internal/basestories/stories" "$(VSCODE_EMBED_ROOT)/internal/baseskills/assets/skills" "$(VSCODE_EMBED_ROOT)/internal/baseskills/assets/agents"
	@cp "$(VSCODE_RUNSTATUS_DIST)/index.html" "$(VSCODE_EMBED_ROOT)/internal/runstatus/web/assets/index.html"
	@cp -R stories/. "$(VSCODE_EMBED_ROOT)/internal/basestories/stories"/
	@cp -R .agents/skills/. "$(VSCODE_EMBED_ROOT)/internal/baseskills/assets/skills"/
	@cp -R .agents/agents/. "$(VSCODE_EMBED_ROOT)/internal/baseskills/assets/agents"/
	@touch "$(VSCODE_EMBED_ROOT)/internal/basestories/stories/.gitkeep" "$(VSCODE_EMBED_ROOT)/internal/baseskills/assets/.gitkeep"
	@node -e 'const fs=require("node:fs"),path=require("node:path"); const root=path.resolve(process.argv[1]); const out=path.resolve(process.argv[2]); const repo=path.resolve(process.argv[3]); const replace={}; function walk(dir){ for (const ent of fs.readdirSync(dir,{withFileTypes:true})) { const p=path.join(dir,ent.name); if (ent.isDirectory()) walk(p); else replace[path.join(repo,path.relative(root,p))]=p; } } walk(root); fs.writeFileSync(out, JSON.stringify({Replace:replace}, null, 2)+"\n");' "$(VSCODE_EMBED_ROOT)" "$(VSCODE_GO_OVERLAY)" "$(abspath .)"
	@echo "[vscode-install-local] staged Go embed overlay -> $(VSCODE_GO_OVERLAY)"

vscode-install-binary-temp: check-deps
	@if [ ! -f "$(VSCODE_RUNSTATUS_DIST)/index.html" ]; then $(MAKE) --no-print-directory vscode-runstatus-spa-temp; fi
	$(MAKE) --no-print-directory vscode-stage-embed-overlay-temp
	@mkdir -p $(INSTALLDIR)
	GOBIN=$(INSTALLDIR) go install -overlay="$(abspath $(VSCODE_GO_OVERLAY))" $(PKG)
	@echo "installed $(BINARY) -> $(INSTALLDIR)/$(BINARY)"
	@echo "smoke-checking installed $(BINARY) can load git-ops stories"
	@log="$$(mktemp)"; \
	if ! "$(INSTALLDIR)/$(BINARY)" validate stories/git-ops/app.yaml >"$$log" 2>&1; then \
		cat "$$log" >&2; \
		rm -f "$$log"; \
		exit 1; \
	fi; \
	rm -f "$$log"
	@case ":$$KITSOKI_CALLER_PATH:" in \
		*":$(INSTALLDIR):"*) ;; \
		*) echo "warning: $(INSTALLDIR) is not on your PATH — '$(BINARY)' won't be found." >&2; \
		   echo "         add it (e.g. 'export PATH=\"$(INSTALLDIR):\$$PATH\"' in your shell profile)" >&2; \
		   echo "         or reinstall with 'make install INSTALLDIR=<dir-on-path>'." >&2;; \
	esac

vscode-install-local: check-deps check-vscode-code-cli
	@rm -rf "$(VSCODE_INSTALL_ROOT)" "$(VSCODE_INSTALL_TMP)"
	$(MAKE) --no-print-directory vscode-runstatus-spa-temp
	$(MAKE) --no-print-directory vscode-install-binary-temp
	$(MAKE) --no-print-directory vscode-package-temp
	@vsix="$(VSCODE_LOCAL_VSIX)"; \
	if [ ! -f "$$vsix" ]; then \
		echo "error: vscode-package did not produce a .vsix" >&2; \
		exit 1; \
	fi; \
	"$(CODE_CLI)" --install-extension "$$vsix" --force; \
	echo "[vscode-install-local] installed $$vsix"

vscode-install-local-in-place: vscode-install-local

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
	$(call runstatus_pnpm_install,--silent)
	cd $(RUNSTATUS_DIR) && pnpm exec playwright test tour-video --project=chromium
	.agents/skills/kitsoki-ui-demo/scripts/render.sh .artifacts/tour-video/tour-video-demo.mp4

# demo-tour-fast validates the tour spec assertions only (no dwells, no render).
# Use this in CI or to iterate on spec changes quickly.
demo-tour-fast: build
	$(call runstatus_pnpm_install,--silent)
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
