package host

import "context"

// worldSnapshotKey is the unexported context key for a read-only world snapshot.
type worldSnapshotKey struct{}

// WithWorldSnapshot injects a read-only snapshot of the current world Vars into
// ctx so a handler can expose world to scripted logic without the world package
// being threaded through the Handler signature. The orchestrator injects the
// world as it stands at call time (after earlier on_enter binds have applied);
// host.starlark.run surfaces it to scripts as ctx.world.get("key").
//
// The map is shared by reference: handlers MUST treat it as read-only. Outputs
// flow back to world only through a handler's Result.Data + the effect's bind:
// spec — never by mutating this snapshot.
func WithWorldSnapshot(ctx context.Context, vars map[string]any) context.Context {
	return context.WithValue(ctx, worldSnapshotKey{}, vars)
}

// WorldSnapshotFromContext returns the world snapshot injected by
// WithWorldSnapshot, or nil when none was injected (e.g. a unit test that calls
// a handler directly). nil is a valid empty snapshot: a read of any key yields
// the absent result.
func WorldSnapshotFromContext(ctx context.Context) map[string]any {
	v, _ := ctx.Value(worldSnapshotKey{}).(map[string]any)
	return v
}
