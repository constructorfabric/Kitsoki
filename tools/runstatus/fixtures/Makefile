# Regenerate the runstatus test fixtures.
# Run from repo root: make -C tools/runstatus/fixtures
# or from this directory: make

REPO_ROOT := $(shell git rev-parse --show-toplevel)
APP        := $(REPO_ROOT)/testdata/apps/cloak/app.yaml
SRC        := $(REPO_ROOT)/tools/runstatus/fixtures/sources
OUT        := $(REPO_ROOT)/tools/runstatus/fixtures

.PHONY: all in-progress completed edge-cases bugfix oracle-rich oracle-with-separate-prompts

all: in-progress completed edge-cases bugfix oracle-rich oracle-with-separate-prompts

# bugfix: drive stories/bugfix/flows/happy_human.yaml through the REAL
# orchestrator (in-memory store, host stubs from the flow) and dump the
# resulting event log as a snapshot. Event names + ordering match production.
bugfix:
	go run $(REPO_ROOT)/tools/runstatus/fixtures/cmd/fromflow \
		--app  $(REPO_ROOT)/stories/bugfix/app.yaml \
		--flow $(REPO_ROOT)/stories/bugfix/flows/happy_human.yaml \
		-o $(OUT)/bugfix.snapshot.json

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

# oracle-rich: self-contained Python generator; no Go toolchain required.
# Reads bugfix.snapshot.json for the app/mermaid blocks and emits
# a new snapshot exercising all five oracle verbs with full rich attrs.
oracle-rich:
	python3 $(SRC)/oracle-rich.gen.py > $(OUT)/oracle-rich.snapshot.json

# oracle-with-separate-prompts: like oracle-rich but with large prompts stored
# in sidecar oracle-prompts/*.txt files (prompt_file / system_prompt_file).
# Exercises usePromptLoader resolving sidecars under file://.
oracle-with-separate-prompts:
	python3 $(SRC)/oracle-with-separate-prompts.gen.py > $(OUT)/oracle-with-separate-prompts.snapshot.json
