# presentation.star — @kitsoki/object-graph's starlark-served "presentation"
# op (S5, D2.1's concrete "endpoint handled by starlark" proof; D4: "a
# starlark op serving the layer taxonomy as data"). Serves the same layer
# taxonomy the deleted engine SPA's catalog-model.ts used to hardcode
# client-side, now reachable over kit.object-graph.graph.presentation
# (JSON-RPC) / the kit_call MCP tool — a genuine data source instead of a
# compiled-in constant, and evidence that a kit endpoint really can be
# implemented in starlark rather than Go, per D1's "a kit ships data +
# declarations only, never Go."
#
# This is presentation CURATION over the seed catalog's current type set, not
# something the type registry's `extends` chains encode — every kit-example
# type extends core-node directly. A downstream catalog with a different type
# vocabulary can ship its own presentation.star (or a kit that composes this
# one and overrides scripts/presentation.star) rather than needing an engine
# change.
def main(ctx):
    return {
        "layers": [
            {
                "id": "actors",
                "title": "Actors, agents, and responsibilities",
                "short": "Actors",
                "description": "Who uses, owns, plans, implements, reviews, or automates the work represented in the graph.",
                "types": ["actor", "agent", "persona"],
            },
            {
                "id": "site",
                "title": "Public product site",
                "short": "Site",
                "description": "Editable public pages generated from graph-backed capability copy, demo media, and consistency rules.",
                "types": ["site-page"],
            },
            {
                "id": "capabilities",
                "title": "Product capabilities and requirements",
                "short": "Capabilities",
                "description": "What exists or is desired: product features, requirements, and the user scenarios they support.",
                "types": ["feature", "requirement", "use-case"],
            },
            {
                "id": "delta",
                "title": "Change and roadmap work",
                "short": "Delta",
                "description": "How the project moves from current state to desired state: proposals and work items.",
                "types": ["proposal", "change"],
            },
            {
                "id": "proof",
                "title": "Implementation and proof",
                "short": "Proof",
                "description": "Where shipped capabilities live and what verifies them: code, stories, demos, fixtures, and evidence.",
                "types": ["evidence", "implementation"],
            },
        ],
    }
