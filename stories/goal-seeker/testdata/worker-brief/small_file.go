// Package fixture is testdata for flows/worker_brief_dispatch.yaml — a small,
// in-scope file the worker-brief projection should inline in full (it is well
// under the default 180-line inline threshold).
package fixture

// Placeholder is the thing the fixture's agent_brief asks a worker to update.
func Placeholder() string {
	return "before"
}
