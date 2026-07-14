package materialize

import (
	"context"
	"fmt"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/harness"
)

// noopHarness is a [harness.Harness] that never runs: the materialize story
// is deterministic (next/restart/look only, per the node-artifact-
// materialization plan's "no live LLM" pilot), so RunTurn is only ever
// reached if a materialize-bound story unexpectedly falls through to the
// harness (e.g. an utterance the deterministic tiers can't route). That is
// a story-authoring bug, not something to route to a model — it fails loud.
type noopHarness struct{}

func (noopHarness) RunTurn(_ context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, fmt.Errorf("materialize: harness invoked in state %q — materialize-bound stories must be fully deterministic (next/restart/look), no LLM fallback", in.StatePath)
}

func (noopHarness) Close() error { return nil }
