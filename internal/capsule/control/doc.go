// Package control provides the project-scoped Capsule control plane.
//
// A Definition is an immutable checked-in recipe; an Instance is its mutable
// materialization; and a ScopeGrant is the immutable authority boundary for a
// CLI, story, or MCP server. The package deliberately keeps machine paths and
// provider credentials behind the manager: callers operate on handles and
// project-relative paths only.
package control
