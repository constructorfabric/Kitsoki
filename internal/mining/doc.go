// Package mining is the ambient mining → propose → apply loop against the
// active root instance. It is the connective tissue between the ambient session
// miner (which turns transcripts into scored recipes) and the meta-mode
// edit-and-reload path (which turns a YAML edit into a live app swap with the
// world preserved):
//
//	scored recipe (≥ threshold)
//	  + regenerated instance inventory
//	      → mapper dedup   (ALREADY-MODELED dropped; ENRICH/GAP proceed)
//	      → author drafts a concrete delta, STAGED under
//	        .artifacts/mining/<recipe_id>/ (never the live tree)
//	      → draft validation (author_artifact schema + a dry app.Load)
//	      → MiningProposalRaised
//	          → accept → write delta onto the live tree → Reload + RerunOnEnter
//	                       → testrunner.RunFlows as the GATE
//	                       → keep-on-green / revert-and-hold-on-red
//	                       → MiningProposalDecided{flows_green, reverted}
//	          → refine → open story.edit meta mode with the draft preloaded
//	          → reject → recorded negative (won't re-surface)
//
// Two interpretive acts are recorded as datapoints — the author's YAML draft
// (MiningProposalRaised) and the operator's accept/refine/reject verdict
// (MiningProposalDecided). Everything else — the dedup, the rung choice, the
// apply, the reload, and the flow gate — is engine-side and deterministic.
//
// Dependency injection is the spine here: the proposer takes a Mapper and a
// Drafter (the dev-story-mining mapper/author personas in production; stubs in
// tests), and the apply gate takes a Reloader (the orchestrator in production;
// a fake in tests) plus a FlowGate (testrunner.RunFlows in production). No path
// in this package calls a live LLM — the single oracle draft pass is the
// caller's injected Drafter, which is cassette-backed when real.
//
// See docs/architecture/ambient-mining.md for the loop, the two events, and the
// rung ladder it rides (docs/proposals/implicit-project-root.md).
package mining
