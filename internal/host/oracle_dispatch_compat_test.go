package host_test

// oracle_dispatch_compat_test.go — backwards-compatibility regression guard for
// the B-7 production wiring.
//
// Production (cmd/kitsoki/main.go) always wires an oracle.Registry into the
// orchestrator. Before this guard, TryDispatchVerb hijacked *every* oracle call
// once a registry was present, returning the dispatch result shape
// (submission/submitted/ok/meta) and dropping the legacy `stdout` key that
// existing stories bind (e.g. stories/dev-story/rooms/oracle.yaml binds
// `oracle_answer: stdout`; stories/code-review/rooms/comment.yaml binds
// `draft_comment: stdout`). That silently broke those rooms in production.
//
// The contract: the plugin dispatch path is opt-in. It engages only when the
// story explicitly names a plugin via the effect's `oracle:` field (the
// orchestrator injects the plugin name into context only in that case). With no
// explicit plugin, TryDispatchVerb falls through (handled=false) so the caller
// runs its legacy in-process handler unchanged.

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/oracle"
)

func compatCtx(t *testing.T) context.Context {
	t.Helper()
	reg := oracle.NewRegistry()
	reg.Register("oracle.claude", oracle.New(oracle.AskFunc(func(_ context.Context, _ oracle.AskRequest) (oracle.AskResponse, error) {
		return oracle.AskResponse{Submission: json.RawMessage(`{"answer":"42"}`)}, nil
	})))
	ctx := host.WithOracleRegistry(context.Background(), reg)
	ctx = host.WithOracleEventSink(ctx, &captureSink{})
	ctx = host.WithOracleCallCtx(ctx, host.OracleCallCtx{
		SessionID: app.SessionID("s"), Turn: 1, StatePath: "r.s",
	})
	return ctx
}

// TestTryDispatchVerb_NoExplicitPlugin_FallsThrough is the regression guard:
// with a registry wired but no explicit `oracle:` plugin name in context, the
// dispatch path must NOT engage, so the legacy handler runs and preserves the
// `stdout` result shape.
func TestTryDispatchVerb_NoExplicitPlugin_FallsThrough(t *testing.T) {
	t.Parallel()
	ctx := compatCtx(t) // no WithOraclePluginName

	_, handled, err := host.TryDispatchVerb(ctx, "ask", "what is the answer?", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Fatalf("default oracle call (no explicit oracle: field) must fall through to the legacy handler, but dispatch hijacked it — this drops the stdout bind key and breaks existing stories")
	}
}

// TestTryDispatchVerb_ExplicitPlugin_Dispatches confirms the opt-in path still
// works: when the story names a plugin via `oracle:`, dispatch engages and
// returns the plugin result shape.
func TestTryDispatchVerb_ExplicitPlugin_Dispatches(t *testing.T) {
	t.Parallel()
	ctx := host.WithOraclePluginName(compatCtx(t), "oracle.claude")

	res, handled, err := host.TryDispatchVerb(ctx, "ask", "what is the answer?", "", "", "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Fatalf("explicit oracle: plugin must route through dispatch, but it fell through")
	}
	if _, ok := res.Data["submission"]; !ok {
		t.Fatalf("dispatch result must carry submission key; got keys %v", res.Data)
	}
}
