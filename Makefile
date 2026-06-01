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

.PHONY: all build install uninstall test vet fmt tidy clean web web-clean

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

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
