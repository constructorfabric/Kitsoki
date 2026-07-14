// Package graphsrv implements the dedicated "kitsoki mcp-graph" stdio MCP
// server (graph-mcp plan §3, stage P2). Every tool in this package invokes
// host.graph.* engine ops exclusively through a host.Registry — never
// internal/graph directly (the "all capability lands as engine ops" hard
// constraint, plan §1).
package graphsrv

// This file is the single error-code vocabulary for mcp-graph. P2 defines
// the codes its read family and feedback channel actually raise; P4/P5/P6
// extend this vocabulary as write tools, catalog/github feedback sinks, and
// the studio mount are added. Do not invent per-handler ad hoc error
// strings — add a new named constant here instead.
const (
	// CodeNoCatalog: the server started with no bound catalog (no
	// --catalog flag and no pog/catalog.yaml found under server cwd at
	// startup). Every tool call returns this until the server is
	// restarted with a valid --catalog.
	CodeNoCatalog = "NO_CATALOG"

	// CodeUnknownCatalog: a tool call's `catalog` argument didn't match
	// any bound alias. The hint lists the bound aliases.
	CodeUnknownCatalog = "UNKNOWN_CATALOG"

	// CodeUnknownNode: a requested node id doesn't exist in the bound
	// catalog. The hint carries nearest-id suggestions where available.
	CodeUnknownNode = "UNKNOWN_NODE"

	// CodeUnknownType: a requested type id doesn't exist in the bound
	// catalog's type registry.
	CodeUnknownType = "UNKNOWN_TYPE"

	// CodeUnknownEdge: a requested edge field doesn't exist on the
	// relevant type. The hint lists that type's edge vocabulary.
	CodeUnknownEdge = "UNKNOWN_EDGE"

	// CodeValidation: a tool call's arguments failed shape/semantic
	// validation (including "raw filesystem path passed as `catalog`").
	// error/hint should name the offending argument.
	CodeValidation = "VALIDATION"

	// CodeReadOnlyMode: reserved for P4 — a write tool call arrived while
	// the server was started with --mode read. Nothing raises this yet
	// (P2 has no write tools), but the constant is defined now so P4's
	// mode-gating wires against a stable name.
	CodeReadOnlyMode = "READ_ONLY_MODE"

	// CodeStewardOnly: a steward-only write tool call (graph.authorize
	// always; graph.apply with dry_run:false) arrived while the server
	// was started with --mode read or --mode propose (not steward).
	CodeStewardOnly = "STEWARD_ONLY"

	// CodeCatalogLintBlocked: a write op (propose/apply) was rejected
	// because it would introduce a NEW error-severity lint issue not
	// already present in the catalog's baseline (guards.go's lint-diff
	// gate, hazard guard #3). Pre-existing catalog dirt never triggers
	// this — only issues the write itself would add.
	CodeCatalogLintBlocked = "CATALOG_LINT_BLOCKED"

	// CodeNeedsCanonicalization: a write op was rejected because a file
	// backing the catalog isn't in yaml.v3's canonical re-marshal form
	// (guards.go's checkCanonical, hazard guard #4) — writing through it
	// would silently reflow a hand-wrapped block scalar. Canonicalize the
	// file out-of-band before retrying.
	CodeNeedsCanonicalization = "NEEDS_CANONICALIZATION"

	// CodeNotYourChangeset: a propose-mode caller tried to withdraw a
	// changeset authored by a different actor. Steward mode skips this
	// check — stewards can withdraw anything.
	CodeNotYourChangeset = "NOT_YOUR_CHANGESET"

	// CodeInternal: a tool handler panicked. The `recorded` wrapper
	// converts the panic into this error payload instead of letting it
	// kill the whole server process — mcpsdk v1.0.0 does not recover
	// handler panics itself, so without this a single buggy call (or a
	// panic-inducing catalog state written by a concurrent session) would
	// take down the long-lived stdio server and every bound catalog with
	// it. The panic message and stack still go to stderr for postmortem.
	CodeInternal = "INTERNAL"

	// CodeOutOfScope: the call named (or, for a write, would touch) a node
	// that exists in the bound catalog but sits outside the session's baked
	// scope (--scope / WithGraphScopes). The scope is fixed at server
	// construction; no tool argument can widen it — a caller that needs the
	// node needs a differently-scoped session.
	CodeOutOfScope = "OUT_OF_SCOPE"

	// CodeCapsuleWorkflow: a capsule-routed write's workspace lifecycle
	// failed — the catalog repo declares graph.write_via: capsule (or the
	// server runs --write-via capsule) but the workspace could not be
	// created, or a completed write could not be committed/merged into the
	// staging branch. The hint names where the work physically is (the
	// managed workspace path/branch) so nothing is silently lost.
	CodeCapsuleWorkflow = "CAPSULE_WORKFLOW"

	// CodeClaimHeld means another live actor owns the requested graph node.
	// The result includes holder provenance so a worker can avoid duplicate work.
	CodeClaimHeld = "CLAIM_HELD"
	// CodeNotClaimHolder means release was attempted by a different actor or
	// liveness handle than the current owner.
	CodeNotClaimHolder = "NOT_CLAIM_HOLDER"

)

// defaultIfStuck is the standing advertisement of the feedback channel:
// every error payload names it, per the plan's "the channel is advertised
// at the moment of friction, in every error payload" requirement.
const defaultIfStuck = "Call feedback.report (kind: tool_gap/data_gap/doc_gap/bug/other) to leave a durable record of what blocked you — it's non-blocking and always local."

// ErrorPayload is the teaching-shaped error envelope every mcp-graph tool
// returns on failure: {ok:false, code, error, hint, if_stuck}.
type ErrorPayload struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Error   string `json:"error"`
	Hint    string `json:"hint,omitempty"`
	IfStuck string `json:"if_stuck"`
}

// NewError builds a teaching-shaped error payload. hint may be empty when
// there's nothing more specific to say than the error message itself.
func NewError(code, message, hint string) *ErrorPayload {
	return &ErrorPayload{
		OK:      false,
		Code:    code,
		Error:   message,
		Hint:    hint,
		IfStuck: defaultIfStuck,
	}
}
