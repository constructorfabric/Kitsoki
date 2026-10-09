# Project journey packs

A journey pack is the project-local composition root for persona QA, a frozen
replay, an executable tutorial, a proof-aware Slidey deck, and a tour. It keeps
project-specific personas and scenarios in the project that owns them while
reusing Kitsoki's shared catalogs and story imports.

## Source resolution

An installed Kitsoki binary resolves `@kitsoki/<story>` imports from its
embedded story library by default. During staging development, pass the staging
checkout explicitly (or export the equivalent environment variable):

```sh
kitsoki --kitsoki-repo /path/to/Kitsoki/.capsules/workspaces/capsule-productization-proposal \
  qa validate .kitsoki/qa/journeys/onboarding/journey.yaml
```

`--kitsoki-repo` also sets `KITSOKI_REPO` for child deterministic checks. That
makes both `@kitsoki/...` story imports and the base
`@kitsoki/product-journey/{personas,scenarios}` catalogs resolve from the same
staging source. Do not commit a machine-specific checkout path; the manifest
contains only portable `@kitsoki/...` references and repository-relative paths.

## Lifecycle

```sh
# no writes, network calls, or model calls
kitsoki qa preview .kitsoki/qa/journeys/onboarding/journey.yaml

# a frozen flow is required: this is a no-LLM replay check, not an exploratory run
kitsoki qa check .kitsoki/qa/journeys/onboarding/journey.yaml --flow .kitsoki/qa/journeys/onboarding/flows/accepted.yaml

# freeze verifies the replay reaches the same transitions without host errors
kitsoki qa freeze .kitsoki/qa/journeys/onboarding/journey.yaml \
  --origin-kind real --origin-trace .artifacts/kitsoki-qa/onboarding/origin.trace.jsonl \
  --flow .kitsoki/qa/journeys/onboarding/flows/accepted.yaml \
  --cassette .kitsoki/qa/journeys/onboarding/flows/accepted.cassette.yaml

# derives the protected generated tutorial region and a tour manifest
kitsoki qa produce .kitsoki/qa/journeys/onboarding/journey.yaml

# requires the real-origin freeze, passed tutorial, rendered MP4, and Slidey deck
kitsoki qa publish .kitsoki/qa/journeys/onboarding/journey.yaml
kitsoki qa verify .kitsoki/qa/journeys/onboarding/journey.yaml
```

`demo` origins are useful for validating the artifact shape, but cannot publish
a successful journey. A passed flow alone is not enough: freeze compares the
origin and replay transition paths and rejects host errors or drift.

Exploratory, provider-backed persona work remains the `@kitsoki/scenario-qa`
story surface. Its independent per-transport judge supplies the real origin;
the `qa` lifecycle then turns accepted evidence into cheap replay and release
gates without spending on an LLM.
