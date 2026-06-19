package store

// Mining proposal event payloads. The mining loop (internal/mining) builds
// these and appends them as side-channel events beside GateDecided so the
// kitsoki trace records which mined recipe proposed which structure and whether
// it stuck. Both fold as no-ops in BuildJourney (see replay.go); the typed
// helpers here give the mining package a single authoritative shape and keep the
// dotted on-disk strings (mining.proposal_raised / mining.proposal_decided) in
// one place. See docs/architecture/ambient-mining.md.

// MiningProposalRaisedPayload is the typed body of a MiningProposalRaised event.
type MiningProposalRaisedPayload struct {
	// RecipeID is the scored recipe the proposal was drafted from.
	RecipeID string `json:"recipe_id"`
	// Kind ∈ binding|world|intent|stub-wire|gate|dev-story-enrich.
	Kind string `json:"kind"`
	// Target ∈ root-instance|dev-story.
	Target string `json:"target"`
	// Priority is the recipe's score that cleared the surface threshold.
	Priority float64 `json:"priority"`
	// Rung ∈ 1|2 — the lightest rung the delta fits at.
	Rung int `json:"rung"`
	// DraftPath points at the staged delta under .artifacts/mining/<recipe_id>/.
	DraftPath string `json:"draft_path"`
}

// MiningProposalDecidedPayload is the typed body of a MiningProposalDecided
// event — the recorded accept/refine/reject verdict.
type MiningProposalDecidedPayload struct {
	// RecipeID ties the verdict back to its MiningProposalRaised.
	RecipeID string `json:"recipe_id"`
	// Verdict ∈ accept|refine|reject.
	Verdict string `json:"verdict"`
	// By ∈ human|llm — who made the call (v1 is human-only for accept).
	By string `json:"by"`
	// FlowsGreen is the no-LLM flow-gate result on an accept (false otherwise).
	FlowsGreen bool `json:"flows_green"`
	// Reverted is true when a green-gate failure rolled the applied edit back.
	Reverted bool `json:"reverted"`
}

// MiningPassRanPayload is the typed body of a MiningPassRan event — one per
// completed ambient-miner pass. The ambient session miner builds these; the
// proposer's MiningProposal* events ride on top, so the chain is
// transcript → MiningPassRan → recipe → MiningProposalRaised → decided.
type MiningPassRanPayload struct {
	// Trigger ∈ seed|live (MiningTriggerSeed / MiningTriggerLive) — the first-run
	// history seed vs a debounced live pass over new transcripts.
	Trigger string `json:"trigger"`
	// Slug is the Claude Code projects slug the pass mined (mining.Slug of the
	// repo path).
	Slug string `json:"slug"`
	// Sessions is the count of transcript sessions folded into the pass sample.
	Sessions int `json:"sessions"`
	// Recipes is the count of scored recipes the pass emitted.
	Recipes int `json:"recipes"`
	// JobID is the background job id (jobs.JobID) the pass ran under, for
	// correlating with the job store.
	JobID string `json:"job_id"`
	// Paused is true when the miner was disabled (mining.enabled=false) at the
	// moment the pass would have fired — recorded so the trace shows the gap.
	Paused bool `json:"paused,omitempty"`
}

// Trigger strings for MiningPassRan.Trigger.
const (
	MiningTriggerSeed = "seed"
	MiningTriggerLive = "live"
)

// Verdict strings for MiningProposalDecided.By / .Verdict.
const (
	MiningVerdictAccept = "accept"
	MiningVerdictRefine = "refine"
	MiningVerdictReject = "reject"

	MiningByHuman = "human"
	MiningByLLM   = "llm"

	MiningTargetRootInstance = "root-instance"
	MiningTargetDevStory     = "dev-story"
)
