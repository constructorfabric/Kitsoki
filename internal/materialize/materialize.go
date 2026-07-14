// Package materialize implements slice 3 of the node-artifact-
// materialization plan (POG .context/node-artifact-materialization-plan.md):
// resolving a graph node type's `materialize:` binding, validating its
// gates server-side, snapshotting the node's transitive context closure,
// and running the bound kitsoki story as an async [jobs.Scheduler] job that
// heartbeats {stage, status} on every room entry/exit.
//
// # Why not host.graph.* or DriveOperation
//
// The POG plan's survey suggested `internal/host/graph_handlers.go`'s
// `GraphHandler` as the place to add a `materialize` op, and
// [orchestrator.Orchestrator.DriveOperation] as the headless-drive primitive.
// Neither fits:
//
//   - `internal/host` is imported BY `internal/orchestrator` (host.Registry,
//     host.Handler, ...), so a GraphHandler op body cannot import the
//     orchestrator to drive a story without an import cycle. This package is
//     the substitute: a leaf that imports both `internal/graph` and
//     `internal/orchestrator`, callable from cmd/kitsoki (CLI) and the
//     runstatus server's graph.materialize.* dispatch.
//   - DriveOperation loops a whole operation to a stop-reason with no
//     per-turn hook, and requires the story to carry an operation run handle
//     in world. Materialize needs a [jobs.Scheduler.Heartbeat] on every room
//     entry/exit to feed the portal's live stage pills, and binds arbitrary
//     catalog stories, so it drives turn-by-turn via the lower-level
//     [orchestrator.Orchestrator.NewSession] /
//     [orchestrator.Orchestrator.RunInitialOnEnter] /
//     [orchestrator.Orchestrator.SubmitDirect] /
//     [orchestrator.Orchestrator.LoadJourney] (see spike_test.go, which
//     proves this drives the pilot story headless with per-room hooks).
package materialize

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/graph"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/ulid"
)

// Stage is one room in a materialize job's stage sequence.
type Stage struct {
	ID     string `json:"id"`
	Status string `json:"status"` // waiting | in-progress | complete | failed
}

// StageEvent is the progress payload passed to [jobs.Scheduler.Heartbeat] on
// every room entry/exit — the wire shape the RPC/SSE layer (slice 4)
// translates into `stage` frames.
type StageEvent struct {
	Stage  string `json:"stage"`
	Status string `json:"status"`
}

// Binding is a type's resolved materialize: declaration.
type Binding struct {
	TypeID       string
	Story        string
	ContextEdges []graph.EdgeField
	Params       []graph.MaterializeParamDecl
	Gates        []string
	Checks       []graph.MaterializeCheckDecl
	// ArtifactSchema/ArtifactFormat carry the type's artifact: declaration
	// alongside the materialize: binding — used only to title/format the
	// write-back artifact (writeback.go); ResolveBinding still errors if a
	// type declares materialize: without artifact: (Start's existing
	// contract), so these are always populated whenever Binding is.
	ArtifactSchema string
	ArtifactFormat string
}

// GateError reports node fields required by a type's materialize.gates that
// are unset — the "reject with the unmet field list" contract from the plan.
type GateError struct {
	NodeID graph.NodeID
	Unmet  []string
}

func (e *GateError) Error() string {
	return fmt.Sprintf("materialize: node %q is missing gate field(s): %s", e.NodeID, strings.Join(e.Unmet, ", "))
}

// Request is the input to [Start].
type Request struct {
	// CatalogPath is the catalog file or bundle dir (graph.LoadCatalog shape).
	CatalogPath string
	// RepoRoot is the repository root a type's materialize.story path is
	// relative to (NOT the catalog path — the catalog may live in a
	// subdirectory such as pog/catalog.yaml while the story lives at
	// <repo root>/stories/<name>).
	RepoRoot string
	// NodeID is the node to materialize.
	NodeID graph.NodeID
	// Params overrides materialize.params defaults by param id.
	Params map[string]any
}

// ResolveBinding resolves node's type's materialize: binding from the
// catalog's type registry. Returns an error if the type is unknown or does
// not declare both artifact: and materialize:.
func ResolveBinding(cat *graph.Catalog, node *graph.Node) (*Binding, error) {
	eff, ok := cat.Registry.Effective(node.TypeID)
	if !ok {
		return nil, fmt.Errorf("materialize: unknown type %q for node %q", node.TypeID, node.ID)
	}
	if eff.Artifact == nil || eff.Materialize == nil {
		return nil, fmt.Errorf("materialize: type %q does not declare a materialize: binding", node.TypeID)
	}
	md := eff.Materialize
	return &Binding{
		TypeID:         eff.ID,
		Story:          md.Story,
		ContextEdges:   md.ContextEdges,
		Params:         md.Params,
		Gates:          md.Gates,
		Checks:         md.Checks,
		ArtifactSchema: string(eff.Artifact.Schema),
		ArtifactFormat: eff.Artifact.Format,
	}, nil
}

// artifactTitle derives a human title from a type's artifact.schema pin
// (e.g. "pog/artifact/implementation-brief/v0" -> "Implementation brief")
// for the write-back evidence entry's title — the catalog schema has no
// dedicated per-artifact title field today (see slice 4's notes_for_next_
// slices), so this is the same "generic title from the schema id" fallback
// the RPC layer already uses for its wire response, applied consistently
// here for the persisted evidence row.
func artifactTitle(schema string) string {
	parts := strings.Split(schema, "/")
	name := parts[0]
	for _, p := range parts {
		if p != "" && p != "pog" && p != "artifact" && !strings.HasPrefix(p, "v") {
			name = p
		}
	}
	name = strings.ReplaceAll(name, "-", " ")
	if name == "" {
		return "Materialized artifact"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// renderArtifactMarkdown deterministically renders the materialized
// artifact's content from the node, its resolved binding, the drive's
// current world, and its context closure — the pilot story's `draft` room
// only marks `artifact_path` in world state (see stories/materialize-work-
// item/rooms/draft.yaml); actually producing bytes at that path is this
// package's job (the plan's "the job handler writes artifacts under
// .artifacts/<node-id>/"), not the story's.
func renderArtifactMarkdown(node *graph.Node, binding *Binding, w map[string]any, contextIDs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", node.Title)
	fmt.Fprintf(&b, "_Materialized as `%s` via `%s`._\n\n", binding.ArtifactSchema, binding.Story)
	if gate, ok := w["gate"].(string); ok && gate != "" {
		fmt.Fprintf(&b, "**Gate:** %s\n\n", gate)
	}
	b.WriteString("## Context\n\n")
	if len(contextIDs) == 0 {
		b.WriteString("_No context edges declared for this type._\n\n")
	} else {
		for _, id := range contextIDs {
			fmt.Fprintf(&b, "- %s\n", id)
		}
		b.WriteString("\n")
	}
	if len(binding.Params) > 0 {
		b.WriteString("## Parameters\n\n")
		for _, p := range binding.Params {
			if v, ok := w[p.ID]; ok {
				fmt.Fprintf(&b, "- **%s:** %v\n", p.ID, v)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("_Generated deterministically by the bound materialize story — no live LLM._\n")
	return b.String()
}

// UnmetGates returns the subset of gates whose node field is unset (missing,
// empty string, or nil), in the order gates was given.
func UnmetGates(node *graph.Node, gates []string) []string {
	var unmet []string
	for _, g := range gates {
		if v, ok := gateValue(node, g); !ok || strings.TrimSpace(v) == "" {
			unmet = append(unmet, g)
		}
	}
	return unmet
}

// gateValue reads a gate field's value off a node: the handful of promoted
// envelope fields first (title/status/visibility — the ones a gate is
// plausibly named after), falling back to the generic Fields bag every
// other row field (e.g. work-item's `gate` and `owner`) lives in.
func gateValue(node *graph.Node, field string) (string, bool) {
	switch field {
	case "title":
		return node.Title, node.Title != ""
	case "status":
		return node.Status, node.Status != ""
	case "visibility":
		return string(node.Visibility), node.Visibility != ""
	}
	raw, ok := node.Fields[field]
	if !ok || raw == nil {
		return "", false
	}
	if s, ok := raw.(string); ok {
		return s, true
	}
	return fmt.Sprint(raw), true
}

// ResolveParams builds the effective invocation input for a partial node.
// Explicit invocation values win, followed by graph-backed sources, then the
// declared default. A source_edge contributes the ordered target id list for
// that edge, so adding graph edges can move an object from partial to ready
// without duplicating those ids in a modal-only form.
func ResolveParams(cat *graph.Catalog, node *graph.Node, params []graph.MaterializeParamDecl, overrides map[string]any) (map[string]any, []string) {
	resolved := make(map[string]any, len(params))
	var missing []string
	for _, p := range params {
		var value any
		var set bool
		if override, ok := overrides[p.ID]; ok && valuePresent(override) {
			value, set = override, true
		} else if p.SourceField != "" {
			value, set = nodeFieldValue(node, p.SourceField)
		} else if p.SourceEdge != "" {
			if eff, ok := cat.Registry.Effective(node.TypeID); ok {
				for _, decl := range eff.EdgeFields {
					if decl.ID != p.SourceEdge {
						continue
					}
					targets := node.EdgeTargets(decl)
					if len(targets) > 0 {
						ids := make([]string, len(targets))
						for i, id := range targets {
							ids[i] = string(id)
						}
						value, set = ids, true
					}
					break
				}
			}
		}
		if !set && valuePresent(p.Default) {
			value, set = p.Default, true
		}
		if set {
			resolved[p.ID] = value
		} else if p.Required {
			missing = append(missing, p.ID)
		}
	}
	return resolved, missing
}

// Readiness is the shared engine guard for materialize(thing). Ready is true
// only when every required node gate and parameter can be resolved.
type Readiness struct {
	Ready         bool
	MissingGates  []string
	MissingParams []string
	Params        map[string]any
}

func MaterializeReadiness(cat *graph.Catalog, node *graph.Node, binding *Binding, overrides map[string]any) Readiness {
	params, missingParams := ResolveParams(cat, node, binding.Params, overrides)
	missingGates := UnmetGates(node, binding.Gates)
	return Readiness{
		Ready:         len(missingGates) == 0 && len(missingParams) == 0,
		MissingGates:  missingGates,
		MissingParams: missingParams,
		Params:        params,
	}
}

func valuePresent(v any) bool {
	if v == nil {
		return false
	}
	switch value := v.(type) {
	case string:
		return strings.TrimSpace(value) != ""
	case []string:
		return len(value) > 0
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	}
	return true
}

func nodeFieldValue(node *graph.Node, field string) (any, bool) {
	switch field {
	case "title":
		return node.Title, node.Title != ""
	case "status":
		return node.Status, node.Status != ""
	case "visibility":
		return string(node.Visibility), node.Visibility != ""
	default:
		v, ok := node.Fields[field]
		return v, ok && valuePresent(v)
	}
}

// ContextClosure walks node's edges transitively, following only edge kinds
// in edgeKinds (recursively, through the same kinds on every node reached —
// the plan's "node's transitive closure, filtered by edge kinds the story
// declares"), and returns the reached node ids in first-seen (BFS) order.
// The root node itself is not included.
func ContextClosure(cat *graph.Catalog, root graph.NodeID, edgeKinds []graph.EdgeField) []graph.NodeID {
	kindSet := make(map[graph.EdgeField]bool, len(edgeKinds))
	for _, k := range edgeKinds {
		kindSet[k] = true
	}

	visited := map[graph.NodeID]bool{root: true}
	queue := []graph.NodeID{root}
	var out []graph.NodeID

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		node, ok := cat.Nodes[id]
		if !ok {
			continue
		}
		eff, ok := cat.Registry.Effective(node.TypeID)
		if !ok {
			continue
		}
		for _, decl := range eff.EdgeFields {
			if !kindSet[decl.ID] {
				continue
			}
			for _, target := range node.EdgeTargets(decl) {
				if visited[target] {
					continue
				}
				visited[target] = true
				out = append(out, target)
				queue = append(queue, target)
			}
		}
	}
	return out
}

// RoomSequence walks a story's rooms in materialization order: starting at
// the root state, following the "next" arc (the deterministic pilot's, and
// by convention any materialize-bound story's, sole advancing intent — see
// stories/materialize-work-item's intents: block) until a room with no
// "next" arc rests. This is a deliberate MVP convention, not a generic
// state-machine topological walk: a materialize-bound story is expected to
// be a linear stage pipeline (gather -> draft -> verify -> done, ...), and
// "next" is its contract verb, exactly like restart/look are its reset/
// no-op verbs.
func RoomSequence(def *app.AppDef) ([]string, error) {
	rootName, ok := def.Root.(string)
	if !ok || rootName == "" {
		return nil, fmt.Errorf("materialize: story root must be a single state name (got %T)", def.Root)
	}

	var seq []string
	seen := map[string]bool{}
	cur := rootName
	for cur != "" && !seen[cur] {
		st, ok := def.States[cur]
		if !ok {
			return nil, fmt.Errorf("materialize: story state %q not found", cur)
		}
		seq = append(seq, cur)
		seen[cur] = true

		next := ""
		for _, tr := range st.On["next"] {
			next = tr.Target
			break
		}
		cur = next
	}
	return seq, nil
}

// Start resolves node's materialize binding, validates its gates, snapshots
// its context closure, and submits a job via sched that drives the bound
// story headless, heartbeating a [StageEvent] on every room entry/exit.
// Returns the job id and the full stage list (all "waiting" — the plan's
// ".start" contract: pills render as waiting upfront, driven live by
// Heartbeat/subscribe from there).
//
// Returns a *[GateError] (unwrap with errors.As) when gates are unmet; the
// job is never submitted in that case.
func Start(ctx context.Context, sched jobs.Scheduler, req Request) (jobs.JobID, []Stage, error) {
	p, err := Prepare(req)
	if err != nil {
		return "", nil, err
	}
	return p.Submit(ctx, sched, nil, "")
}

// Prepared is a resolved, gate-checked materialize plan for one node — the
// output of [Prepare], everything [Prepared.Submit] needs to run the job.
// Split from Start so a caller that drives the story through its own live
// session (the runstatus server's web-registry path) can seed that session
// from StoryAppPath + InitialWorld between preparing and submitting.
type Prepared struct {
	Req        Request
	Node       *graph.Node
	Binding    *Binding
	ContextIDs []string
	// Def is the loaded story definition; StoryAppPath is where it was
	// loaded from (RepoRoot-joined, suitable for a session registry's
	// story-path key once absolutized).
	Def          *app.AppDef
	StoryAppPath string
	// Stages is the story's room sequence (see RoomSequence) followed by
	// one "check:<id>" stage per resolved gate check; NumRooms says where
	// rooms end and checks begin. InitialWorld is the seed world (node_id +
	// resolved params + the node's gate text).
	Stages       []string
	NumRooms     int
	Checks       []ResolvedCheck
	InitialWorld map[string]any
}

// Prepare resolves req's node binding, validates gates, snapshots the
// context closure, loads the bound story, and computes the room sequence and
// seed world. Returns a *[GateError] (unwrap with errors.As) when gates are
// unmet.
func Prepare(req Request) (*Prepared, error) {
	cat, err := graph.LoadCatalog(req.CatalogPath)
	if err != nil {
		return nil, fmt.Errorf("materialize: load catalog: %w", err)
	}

	node, ok := cat.Nodes[req.NodeID]
	if !ok {
		return nil, fmt.Errorf("materialize: node %q not found in catalog", req.NodeID)
	}

	binding, err := ResolveBinding(cat, node)
	if err != nil {
		return nil, err
	}

	readiness := MaterializeReadiness(cat, node, binding, req.Params)
	unmet := append([]string{}, readiness.MissingGates...)
	for _, id := range readiness.MissingParams {
		unmet = append(unmet, "param:"+id)
	}
	if len(unmet) > 0 {
		return nil, &GateError{NodeID: req.NodeID, Unmet: unmet}
	}

	closure := ContextClosure(cat, req.NodeID, binding.ContextEdges)
	contextIDs := make([]string, len(closure))
	for i, id := range closure {
		contextIDs[i] = string(id)
	}

	storyAppPath := filepath.Join(req.RepoRoot, binding.Story, "app.yaml")
	def, err := app.Load(storyAppPath)
	if err != nil {
		return nil, fmt.Errorf("materialize: load story %q: %w", binding.Story, err)
	}

	roomSeq, err := RoomSequence(def)
	if err != nil {
		return nil, err
	}
	if len(roomSeq) == 0 {
		return nil, fmt.Errorf("materialize: story %q has no rooms", binding.Story)
	}
	checks := ResolveChecks(node, binding.Checks)
	allStages := append(append([]string{}, roomSeq...), CheckStages(checks)...)

	initialWorld := map[string]any{
		"node_id": string(req.NodeID),
		"node": map[string]any{
			"id": string(node.ID), "type": node.TypeID, "title": node.Title,
			"status": node.Status, "visibility": string(node.Visibility), "fields": node.Fields,
		},
		"context_ids": contextIDs,
	}
	for id, value := range readiness.Params {
		initialWorld[id] = value
	}
	if gv, ok := gateValue(node, "gate"); ok {
		initialWorld["gate"] = gv
	}

	return &Prepared{
		Req:          req,
		Node:         node,
		Binding:      binding,
		ContextIDs:   contextIDs,
		Def:          def,
		StoryAppPath: storyAppPath,
		Stages:       allStages,
		NumRooms:     len(roomSeq),
		Checks:       checks,
		InitialWorld: initialWorld,
	}, nil
}

// TurnDriver is one already-seeded live story session the drive loop
// advances turn-by-turn: Next submits the materialize contract verb
// ("next"), World snapshots the session's current world vars. The private
// path (driver == nil on [Prepared.Submit]) builds its own orchestrator rig
// and wraps it in one; the runstatus server wraps a web-registry session so
// the drive is observable — trace and transcript — in the web UI.
type TurnDriver interface {
	Next(ctx context.Context) error
	World(ctx context.Context) (map[string]any, error)
}

// Submit submits the prepared materialize job to sched and returns the job
// id plus the full all-"waiting" stage list (Start's ".start" contract).
//
// driver == nil drives a self-contained in-memory orchestrator rig seeded
// from p.InitialWorld (the CLI / no-registry path). A non-nil driver drives
// the caller's own live session — the caller must have already created and
// seeded it (e.g. via the web registry's NewSessionSeeded on p.StoryAppPath
// + p.InitialWorld); webSessionID then records that session's id in the job
// payload ("web_session") for observability.
func (p *Prepared) Submit(ctx context.Context, sched jobs.Scheduler, driver TurnDriver, webSessionID string) (jobs.JobID, []Stage, error) {
	sessionID := app.SessionID("materialize-" + string(p.Req.NodeID) + "-" + ulid.New())

	payload := map[string]any{
		"node_id": string(p.Req.NodeID),
		"story":   p.Binding.Story,
		"context": p.ContextIDs,
		"stages":  p.Stages,
	}
	if webSessionID != "" {
		payload["web_session"] = webSessionID
	}

	jobID, err := sched.Submit(ctx, jobs.JobSpec{
		SessionID: sessionID,
		Kind:      "graph.materialize",
		Payload:   payload,
		Handler:   driveHandler(p, sched, driver),
	})
	if err != nil {
		return "", nil, fmt.Errorf("materialize: submit job: %w", err)
	}

	stages := make([]Stage, len(p.Stages))
	for i, id := range p.Stages {
		stages[i] = Stage{ID: id, Status: "waiting"}
	}
	return jobID, stages, nil
}

// driveWriteback bundles everything driveHandler's write-back step
// (writeback.go) needs beyond the story-drive machinery itself: where the
// catalog and repo live, and the resolved node/binding/context the artifact
// content and evidence entry are rendered from.
type driveWriteback struct {
	CatalogPath string
	RepoRoot    string
	NodeID      graph.NodeID
	Node        *graph.Node
	Binding     *Binding
	ContextIDs  []string
}

// rigDriver adapts the private orchestrator rig to [TurnDriver] — the
// original headless drive path, unchanged in behaviour (spike_test.go's
// primitives), just behind the interface the web-registry path shares.
type rigDriver struct {
	orch *orchestrator.Orchestrator
	sid  app.SessionID
}

func (d rigDriver) Next(ctx context.Context) error {
	_, err := d.orch.SubmitDirect(ctx, d.sid, "next", nil)
	return err
}

func (d rigDriver) World(context.Context) (map[string]any, error) {
	return d.orch.CurrentWorld(d.sid).Vars, nil
}

// driveHandler builds the [host.Handler] a materialize job runs: it drives
// the story's rooms via TurnDriver.Next ("next" per room), heartbeating
// sched on every entry/exit, per the scheduler-injected "__job_id"
// convention (see [jobs.Scheduler.Submit]). With driver == nil it builds a
// fresh orchestrator rig of its own (in-memory store, no host registry — a
// materialize-bound story declares no hosts) seeded from p.InitialWorld. As
// soon as the driving world exposes an `artifact_path` (checked after every
// room transition, not only at the end — the plan's "artifacts produced
// mid-run land as evidence entries as they appear"), it writes the
// artifact's content to disk under the repo root and appends an evidence
// entry to the catalog (writeback.go). On the job's terminal outcome
// (success or failure) it upserts the node's `materialization:` block.
func driveHandler(p *Prepared, sched jobs.Scheduler, driver TurnDriver) host.Handler {
	stages := p.Stages
	wb := driveWriteback{
		CatalogPath: p.Req.CatalogPath,
		RepoRoot:    p.Req.RepoRoot,
		NodeID:      p.Req.NodeID,
		Node:        p.Node,
		Binding:     p.Binding,
		ContextIDs:  p.ContextIDs,
	}
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		jobID, _ := args["__job_id"].(string)
		statuses := make([]string, len(stages))
		for i := range statuses {
			statuses[i] = "waiting"
		}
		heartbeat := func(i int, status string) {
			statuses[i] = status
			if sched == nil || jobID == "" {
				return
			}
			_ = sched.Heartbeat(jobID, StageEvent{Stage: stages[i], Status: status})
		}
		stageSnapshot := func() []Stage {
			out := make([]Stage, len(stages))
			for i, id := range stages {
				out[i] = Stage{ID: id, Status: statuses[i]}
			}
			return out
		}

		var writtenArtifacts []MaterializationArtifact
		var checkResults []CheckResult
		writeArtifactIfPresent := func(w map[string]any) {
			path, _ := w["artifact_path"].(string)
			if path == "" {
				return
			}
			for _, a := range writtenArtifacts {
				if a.Path == path {
					return // already written this path
				}
			}
			content, _ := w["artifact_content"].(string)
			if content == "" {
				content = renderArtifactMarkdown(wb.Node, wb.Binding, w, wb.ContextIDs)
			}
			fullPath := filepath.Join(wb.RepoRoot, path)
			if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
				return // best-effort: a write-back failure must not fail the materialize job itself
			}
			if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
				return
			}
			title := artifactTitle(wb.Binding.ArtifactSchema)
			produced := MaterializationArtifact{Kind: "doc", Title: title, Path: path, ProducedAt: time.Now().UTC().Format(time.RFC3339)}
			writtenArtifacts = append(writtenArtifacts, produced)
			_ = AppendEvidence(wb.CatalogPath, string(wb.NodeID), EvidenceEntry{Kind: produced.Kind, Title: produced.Title, Path: produced.Path}, jobID, wb.Binding.Story)
		}
		finalizeWriteback := func(status string) {
			_ = WriteMaterialization(wb.CatalogPath, string(wb.NodeID), MaterializationRecord{
				JobID:     jobID,
				Status:    status,
				Story:     wb.Binding.Story,
				Stages:    stageSnapshot(),
				Artifacts: writtenArtifacts,
				Checks:    checkResults,
			})
		}

		drv := driver
		if drv == nil {
			m, err := machine.New(p.Def)
			if err != nil {
				return host.Result{Error: err.Error()}, nil
			}
			st, err := store.OpenMemory()
			if err != nil {
				return host.Result{Error: err.Error()}, nil
			}
			defer st.Close()

			// The private rig wires the same builtin host registry the web
			// path gets from its registry-backed session (and `kitsoki
			// turn` builds for itself — see cmd/kitsoki/turn.go): producer
			// stories declare hosts: (host.run recorders/builders,
			// host.graph status flips) and a rig without a registry turned
			// every such invoke into a silent no-op, so `kitsoki graph
			// materialize` ran the rooms but produced nothing. The
			// orchestrator still enforces the story's hosts: allow-list at
			// invoke time; stories that declare no hosts behave exactly as
			// before.
			hostReg := host.NewRegistry()
			host.RegisterBuiltins(hostReg)
			host.RegisterStarlarkBindings(hostReg, p.Def.StarlarkHostBindings)
			if err := hostReg.ValidateAllowList(p.Def.Hosts); err != nil {
				return host.Result{Error: fmt.Sprintf("materialize: validate story hosts: %v", err)}, nil
			}

			orch := orchestrator.New(p.Def, m, st, &noopHarness{}, orchestrator.WithHostRegistry(hostReg))

			sid, err := orch.NewSession(ctx)
			if err != nil {
				return host.Result{Error: err.Error()}, nil
			}
			if err := orch.RunInitialOnEnter(ctx, sid); err != nil {
				return host.Result{Error: err.Error()}, nil
			}
			if err := orch.PatchWorld(ctx, sid, p.InitialWorld); err != nil {
				return host.Result{Error: err.Error()}, nil
			}
			drv = rigDriver{orch: orch, sid: sid}
		}

		// World snapshots feed the mid-run artifact check; a snapshot error
		// mid-loop is tolerated (skip that check) because the terminal
		// snapshot below is the one the result and write-back depend on.
		worldNow := func() map[string]any {
			w, err := drv.World(ctx)
			if err != nil {
				return nil
			}
			return w
		}

		heartbeat(0, "in-progress")
		writeArtifactIfPresent(worldNow())

		for i := 0; i < p.NumRooms-1; i++ {
			if err := drv.Next(ctx); err != nil {
				heartbeat(i, "failed")
				finalizeWriteback("failed")
				return host.Result{Error: fmt.Sprintf("materialize: room %q: %v", stages[i], err)}, nil
			}
			heartbeat(i, "complete")
			heartbeat(i+1, "in-progress")
			writeArtifactIfPresent(worldNow())
		}

		finalWorld, err := drv.World(ctx)
		if err != nil {
			heartbeat(p.NumRooms-1, "failed")
			finalizeWriteback("failed")
			return host.Result{Error: err.Error()}, nil
		}
		heartbeat(p.NumRooms-1, "complete")
		writeArtifactIfPresent(finalWorld)

		// Gate checks run engine-side after the story's rooms: the sandboxed
		// Starlark assertions judge whether the node's gate is actually met,
		// and a false (or unjudgeable) verdict fails the job — the story
		// cannot vouch for itself.
		for j, rc := range p.Checks {
			idx := p.NumRooms + j
			heartbeat(idx, "in-progress")
			result := RunCheck(ctx, wb.RepoRoot, rc)
			checkResults = append(checkResults, result)
			if !result.OK {
				heartbeat(idx, "failed")
				finalizeWriteback("failed")
				reason := result.Error
				if reason == "" {
					reason = strings.Join(result.Reasons, "; ")
				}
				return host.Result{Error: fmt.Sprintf("materialize: gate check %q failed: %s", rc.ID, reason)}, nil
			}
			heartbeat(idx, "complete")
		}

		finalizeWriteback("complete")

		artifactPath, _ := finalWorld["artifact_path"].(string)

		return host.Result{Data: map[string]any{
			"node_id":       string(p.Req.NodeID),
			"artifact_path": artifactPath,
			"world":         finalWorld,
			"checks":        checkResultsWire(checkResults),
		}}, nil
	}
}

// checkResultsWire renders check results as plain JSON-ready maps for the
// job result payload (host.Result.Data must round-trip through JSON).
func checkResultsWire(results []CheckResult) []map[string]any {
	out := make([]map[string]any, len(results))
	for i, r := range results {
		m := map[string]any{"id": r.ID, "script": r.Script, "ok": r.OK}
		if r.ScriptSHA256 != "" {
			m["script_sha256"] = r.ScriptSHA256
		}
		if len(r.Reasons) > 0 {
			m["reasons"] = r.Reasons
		}
		if r.Error != "" {
			m["error"] = r.Error
		}
		if r.Reproduce != "" {
			m["reproduce"] = r.Reproduce
		}
		out[i] = m
	}
	return out
}
