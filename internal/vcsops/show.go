package vcsops

import (
	"context"
	"fmt"
	"strings"
)

// GitShowFile returns the content of relpath as it existed at rev, inside
// the git repo rooted at (or containing) dir — a thin wrapper over RunGit's
// `git show <rev>:<relpath>` form, for callers that need a historical file
// blob rather than a diff or a checkout (graph-mcp P5's git-era history
// walk, internal/host/graph_history_ops.go, is the first caller — but that
// caller lives in package host and CANNOT import this package, since
// vcsops already imports host; see graph_history_ops.go's own inline git
// helper for host's use of the same `git show` primitive).
func GitShowFile(ctx context.Context, dir, rev, relpath string) (string, error) {
	out, exit, err := RunGit(ctx, dir, "show", rev+":"+relpath)
	if err != nil {
		return "", fmt.Errorf("vcsops.GitShowFile: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("vcsops.GitShowFile: git show %s:%s: git exited %d: %s", rev, relpath, exit, strings.TrimSpace(out))
	}
	return out, nil
}
