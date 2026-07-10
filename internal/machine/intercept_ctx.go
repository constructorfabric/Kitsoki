package machine

import "context"

// interceptDriveKey marks a context as belonging to a synchronous intercept
// drive (orchestrator.DriveToRest). It is the seam that lets the post-bind emit
// settle behave differently for an intercept drive than for a normal operator
// turn WITHOUT threading a bool through every call signature.
type interceptDriveKeyT struct{}

var interceptDriveKey = interceptDriveKeyT{}

// WithInterceptDrive returns a context flagged as a synchronous intercept drive.
// orchestrator.DriveToRest wraps its drive context with this so the settle will
// drive THROUGH an `intercept_drive: rest` room's multi-round auto-route (e.g.
// git-ops's conflict room: conflict_ready → resolver → rebase_continue →
// branch_ops) instead of resting at it the way a normal operator turn does.
func WithInterceptDrive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interceptDriveKey, true)
}

// InterceptDriveActive reports whether ctx was flagged by WithInterceptDrive.
func InterceptDriveActive(ctx context.Context) bool {
	v, _ := ctx.Value(interceptDriveKey).(bool)
	return v
}
