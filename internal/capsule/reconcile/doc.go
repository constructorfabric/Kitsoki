// Package reconcile produces stale-safe, content-addressed Git reconciliation
// plans for Capsule workspaces. It never decides conflict resolution or
// promotion approval; those remain story/human gates supplied to Apply.
package reconcile
