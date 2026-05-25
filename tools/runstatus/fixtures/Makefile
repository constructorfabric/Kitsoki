# Regenerate the three runstatus test fixtures from their source JSONL files.
# Run from repo root: make -C tools/runstatus/fixtures
# or from this directory: make

REPO_ROOT := $(shell git rev-parse --show-toplevel)
APP       := $(REPO_ROOT)/testdata/apps/cloak/app.yaml
SRC       := $(REPO_ROOT)/tools/runstatus/fixtures/sources
OUT       := $(REPO_ROOT)/tools/runstatus/fixtures

.PHONY: all in-progress completed edge-cases

all: in-progress completed edge-cases

in-progress:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/in-progress.jsonl \
		--app $(APP) \
		--current-state cloakroom \
		-o $(OUT)/in-progress.snapshot.json

completed:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/completed.jsonl \
		--app $(APP) \
		-o $(OUT)/completed.snapshot.json

edge-cases:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/edge-cases.jsonl \
		--app $(APP) \
		-o $(OUT)/edge-cases.snapshot.json
