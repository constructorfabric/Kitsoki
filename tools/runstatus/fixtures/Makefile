# Regenerate the runstatus test fixtures.
# Run from repo root: make -C tools/runstatus/fixtures
# or from this directory: make

REPO_ROOT := $(shell git rev-parse --show-toplevel)
APP        := $(REPO_ROOT)/testdata/apps/cloak/app.yaml
SRC        := $(REPO_ROOT)/tools/runstatus/fixtures/sources
OUT        := $(REPO_ROOT)/tools/runstatus/fixtures
ARTIFACTS  := $(REPO_ROOT)/.artifacts

.PHONY: all in-progress completed edge-cases bugfix agent-rich agent-with-separate-prompts artifacts

all: in-progress completed edge-cases bugfix agent-rich agent-with-separate-prompts

# artifacts: build a self-contained HTML artifact for every *.snapshot.json
# here, via the kitsoki binary (the Go replacement for the old
# scripts/build-artifact.mjs). The SPA must be bundled into the binary first:
# run `make -C $(REPO_ROOT) web` (or `make build`), otherwise export-status
# reports the SPA as unbuilt. Output lands in <repo-root>/.artifacts/.
artifacts:
	@mkdir -p $(ARTIFACTS)
	@for snap in $(OUT)/*.snapshot.json; do \
		name=$$(basename $$snap .snapshot.json); \
		echo "→ $(ARTIFACTS)/$$name.html"; \
		go run $(REPO_ROOT)/cmd/kitsoki export-status --from-snapshot $$snap -o $(ARTIFACTS)/$$name.html || exit 1; \
	done

# bugfix: drive stories/bugfix/flows/happy_human.yaml through the REAL
# orchestrator (in-memory store, host stubs from the flow) and dump the
# resulting event log as a snapshot. Event names + ordering match production.
# inject_usage.py backfills plausible token-usage meta (input/output/cache
# tokens + cost) onto the agent.call.complete events of cassette-replay
# snapshots, which were recorded without usage. Live traces carry real usage;
# this is synthetic fixture data so the runstatus UI has something to render.
INJECT := python3 $(REPO_ROOT)/tools/runstatus/fixtures/inject_usage.py

bugfix:
	go run $(REPO_ROOT)/tools/runstatus/fixtures/cmd/fromflow \
		--app  $(REPO_ROOT)/stories/bugfix/app.yaml \
		--flow $(REPO_ROOT)/stories/bugfix/flows/happy_human.yaml \
		-o $(OUT)/bugfix.snapshot.json
	$(INJECT) $(OUT)/bugfix.snapshot.json

in-progress:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/in-progress.jsonl \
		--app $(APP) \
		--current-state cloakroom \
		-o $(OUT)/in-progress.snapshot.json
	$(INJECT) $(OUT)/in-progress.snapshot.json

completed:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/completed.jsonl \
		--app $(APP) \
		-o $(OUT)/completed.snapshot.json
	$(INJECT) $(OUT)/completed.snapshot.json

edge-cases:
	go run $(REPO_ROOT)/cmd/kitsoki export-status \
		--from-trace $(SRC)/edge-cases.jsonl \
		--app $(APP) \
		-o $(OUT)/edge-cases.snapshot.json
	$(INJECT) $(OUT)/edge-cases.snapshot.json

# agent-rich: self-contained Python generator; no Go toolchain required.
# Reads bugfix.snapshot.json for the app/mermaid blocks and emits
# a new snapshot exercising all five agent verbs with full rich attrs.
agent-rich:
	python3 $(SRC)/agent-rich.gen.py > $(OUT)/agent-rich.snapshot.json

# agent-with-separate-prompts: like agent-rich but with large prompts stored
# in sidecar agent-prompts/*.txt files (prompt_file / system_prompt_file).
# Exercises usePromptLoader resolving sidecars under file://.
agent-with-separate-prompts:
	python3 $(SRC)/agent-with-separate-prompts.gen.py > $(OUT)/agent-with-separate-prompts.snapshot.json
