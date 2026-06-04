BINARY    := kitsoki
PKG       := ./cmd/kitsoki
INSTALLDIR ?= $(HOME)/bin

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

.PHONY: all build install uninstall test test-flows vet fmt tidy clean web web-clean e2e-docker \
	fetch-models fetch-llama-server

all: build

# build / install depend on web so the binary always embeds a current SPA.
build: web
	go build -o $(BINARY) $(PKG)

install: web
	@mkdir -p $(INSTALLDIR)
	GOBIN=$(INSTALLDIR) go install $(PKG)
	@echo "installed $(BINARY) -> $(INSTALLDIR)/$(BINARY)"

uninstall:
	rm -f $(INSTALLDIR)/$(BINARY)

# web bundles the runstatus SPA and stages it into the embed dir. Incremental:
# only rebuilds when a source file is newer than the staged bundle.
web: $(EMBED_INDEX)

$(EMBED_INDEX): $(SPA_SOURCES)
	@command -v pnpm >/dev/null 2>&1 || { \
		echo "error: pnpm not found — needed to build the runstatus SPA." >&2; \
		echo "       install Node + pnpm, or run 'make web-clean' if a bundle is already staged." >&2; \
		exit 1; }
	cd $(RUNSTATUS_DIR) && pnpm install --frozen-lockfile && pnpm build
	@mkdir -p $(dir $(EMBED_INDEX))
	cp $(RUNSTATUS_DIR)/dist/index.html $(EMBED_INDEX)
	@echo "staged runstatus SPA -> $(EMBED_INDEX)"

# web-clean removes the staged bundle (the binary then reports the SPA as
# unbuilt until the next `make web`).
web-clean:
	rm -f $(EMBED_INDEX)

# test runs the Go unit tests AND the Mode-2 deterministic story flow suites
# (no LLM, no cost). The flow suites guard the shipped stories under stories/,
# which `go test ./...` does not touch. scripts/run-tests.sh collects every
# failure across both suites (never bails early), prints a terse summary on
# success / full detail on failure, and always writes a rotated full report to
# .artifacts/test-reports/.
test:
	@./scripts/run-tests.sh

# test-flows replays every story's flow fixtures against a scratch binary built
# from the working tree (plain `go build` — no SPA embed needed), so it tracks
# local edits rather than a stale $(INSTALLDIR) copy. Fails if any story fails.
test-flows:
	@go build -o ./.kitsoki-flows $(PKG)
	@rc=0; for app in $(STORY_APPS); do \
		printf '\n-- flows: %s\n' "$$app"; \
		./.kitsoki-flows test flows "$$app" || rc=1; \
	done; rm -f ./.kitsoki-flows; exit $$rc

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
