package mining

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// RungFor returns the lightest rung a delta kind lands at. Rung 1 is a
// .kitsoki.yaml override (binding/world/intent-synonym); rung 2 materializes a
// file under .kitsoki/stories/<project>/ (stub-wire/gate, a new room-backed intent, or a
// dev-story-enrich). This is pure policy — see the rung-ladder table in
// docs/architecture/ambient-mining.md and the mechanics in
// docs/proposals/implicit-project-root.md.
func RungFor(kind DeltaKind, needsRoom bool) int {
	switch kind {
	case KindBinding, KindWorld:
		return 1
	case KindIntent:
		if needsRoom {
			return 2 // a new intents: entry that needs a room
		}
		return 1 // a slot-template synonym is a rung-1 override
	case KindStubWire, KindGate, KindDevStoryEnrich:
		return 2
	default:
		// Unknown kinds are conservatively materialized (rung 2) so a malformed
		// recipe never silently edits .kitsoki.yaml.
		return 2
	}
}

// Proposer turns scored recipes into staged, validated proposals. It is
// deterministic except for the two injected interpretive seams (Mapper,
// Drafter). It never touches the live tree — it only writes a staging dir under
// StageRoot and appends a MiningProposalRaised record via the injected sink.
type Proposer struct {
	// PriorityThreshold gates which recipes are acted on (recipes below it are
	// skipped before any agent pass). Mirrors mining.priority_threshold.
	PriorityThreshold float64
	// Mapper is the dedup seam (dev-story-mining mapper persona in production).
	Mapper Mapper
	// Drafter is the author seam (dev-story-mining author persona in production,
	// cassette-backed for any real LLM pass).
	Drafter Drafter
	// AuthorSchema is the raw author_artifact.json schema the draft is validated
	// against (stories/dev-story-mining/schemas/author_artifact.json). Required.
	AuthorSchema json.RawMessage
	// StageRoot is the directory staged drafts are written under; one
	// subdirectory per recipe id. Defaults to ".artifacts/mining" when empty.
	StageRoot string
	// LiveEntry is the entry-manifest path of the live tree (relative to the
	// tree root), used to dry-load the staged tree. Required for the dry-load
	// validation; when empty the dry load is skipped (schema-only validation).
	LiveEntry string
	// LiveFiles is the current live tree (relpath → bytes), overlaid with the
	// draft for the dry app.Load. Required alongside LiveEntry.
	LiveFiles map[string][]byte
}

// Proposal is a staged, validated delta ready to surface. The surface
// (docs/proposals/mine-command-ux.md) renders it; the apply gate (Apply) acts
// on the operator's verdict.
type Proposal struct {
	// Recipe is the source recipe.
	Recipe Recipe
	// Rung is the chosen rung (1|2).
	Rung int
	// DraftPath is the staging directory the delta was written to.
	DraftPath string
	// Files is the delta (relpath relative to the live tree root → new bytes) —
	// what Apply copies onto the live tree on accept.
	Files map[string][]byte
	// Artifact is the validated author_artifact.
	Artifact json.RawMessage
}

// ErrDropped is returned by Propose when the recipe is dropped before
// surfacing — below threshold or ALREADY-MODELED. It is not a failure; the
// caller skips it.
type ErrDropped struct {
	Reason string
}

func (e ErrDropped) Error() string { return "mining: recipe dropped: " + e.Reason }

// Propose runs the full proposer pipeline for one recipe: threshold check →
// mapper dedup (ALREADY-MODELED drops) → rung choice → author draft → stage →
// validate (author_artifact schema + dry app.Load of the staged tree). On
// success it returns the Proposal and emits MiningProposalRaised via sink (when
// non-nil). A dropped recipe returns ErrDropped; a malformed draft returns a
// validation error and never surfaces.
func (p *Proposer) Propose(ctx context.Context, recipe Recipe, inventory []InventoryRow, sink *SessionSink) (*Proposal, error) {
	if recipe.Priority < p.PriorityThreshold {
		return nil, ErrDropped{Reason: fmt.Sprintf("priority %.3f below threshold %.3f", recipe.Priority, p.PriorityThreshold)}
	}
	if p.Mapper == nil || p.Drafter == nil {
		return nil, fmt.Errorf("mining: proposer requires a Mapper and a Drafter")
	}

	status, err := p.Mapper.Classify(ctx, recipe, inventory)
	if err != nil {
		return nil, fmt.Errorf("mining: dedup classify: %w", err)
	}
	if status == StatusAlreadyModeled {
		return nil, ErrDropped{Reason: "ALREADY-MODELED"}
	}

	rung := RungFor(recipe.Kind, recipe.NeedsRoom)

	draft, err := p.Drafter.Draft(ctx, recipe, rung)
	if err != nil {
		return nil, fmt.Errorf("mining: author draft: %w", err)
	}
	if len(draft.Files) == 0 {
		return nil, fmt.Errorf("mining: author draft produced no files for recipe %q", recipe.ID)
	}

	// Validate the author_artifact against the schema BEFORE surfacing.
	if len(p.AuthorSchema) > 0 {
		if verr := agent.ValidateSubmission(p.AuthorSchema, draft.Artifact); verr != nil {
			return nil, fmt.Errorf("mining: author_artifact failed schema for recipe %q: %w", recipe.ID, verr)
		}
	}

	// Dry app.Load of the staged tree (live overlaid with the draft) — a
	// structurally-broken draft fails fast here, never reaching the operator.
	if p.LiveEntry != "" {
		if derr := dryLoad(p.LiveFiles, draft.Files, p.LiveEntry); derr != nil {
			return nil, fmt.Errorf("mining: staged tree failed dry app.Load for recipe %q: %w", recipe.ID, derr)
		}
	}

	// Stage the draft under .artifacts/mining/<recipe_id>/ (never the live tree).
	draftPath, err := p.stage(recipe.ID, draft.Files)
	if err != nil {
		return nil, err
	}

	prop := &Proposal{
		Recipe:    recipe,
		Rung:      rung,
		DraftPath: draftPath,
		Files:     draft.Files,
		Artifact:  draft.Artifact,
	}

	if aerr := appendPayload(sink, store.MiningProposalRaised, store.MiningProposalRaisedPayload{
		RecipeID:  recipe.ID,
		Kind:      string(recipe.Kind),
		Target:    recipe.Target,
		Priority:  recipe.Priority,
		Rung:      rung,
		DraftPath: draftPath,
	}); aerr != nil {
		return nil, fmt.Errorf("mining: append MiningProposalRaised: %w", aerr)
	}

	return prop, nil
}

// stage writes the draft files under StageRoot/<recipeID>/ and returns the
// staging directory.
func (p *Proposer) stage(recipeID string, files map[string][]byte) (string, error) {
	root := p.StageRoot
	if root == "" {
		root = filepath.Join(".artifacts", "mining")
	}
	dir := filepath.Join(root, recipeID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mining: stage mkdir %q: %w", dir, err)
	}
	for rel, b := range files {
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", fmt.Errorf("mining: stage mkdir %q: %w", dest, err)
		}
		if err := os.WriteFile(dest, b, 0o644); err != nil {
			return "", fmt.Errorf("mining: stage write %q: %w", dest, err)
		}
	}
	return dir, nil
}

// dryLoad overlays draft onto live and runs app.LoadFromFiles to prove the
// staged tree compiles. The temp tree is cleaned up before returning.
func dryLoad(live, draft map[string][]byte, entry string) error {
	merged := make(map[string][]byte, len(live)+len(draft))
	for k, v := range live {
		merged[k] = v
	}
	for k, v := range draft {
		merged[k] = v
	}
	_, cleanup, err := app.LoadFromFiles(merged, entry)
	cleanup()
	return err
}
