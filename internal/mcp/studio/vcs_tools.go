package studio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
	"kitsoki/internal/vcsops"
)

// vcs_tools.go — a structured git / worktree surface.
//
// Git is the single biggest reason an MCP-driving agent shells out (worktree
// create/remove, status/diff/log, and the squash-merge that lands a fix). The
// driver agent has no git tool at all, so a fix's whole lifecycle escapes the
// MCP. Worse, the by-hand squash ritual
//
//	git -C wt add -A && git -C wt reset --soft main && git -C wt commit \
//	  && git merge --ff-only && git worktree remove …
//
// is the exact pattern that once DESTROYED main: `reset --soft main` inside a
// stale worktree commits that worktree's stale tree, reverting everything landed
// on main since the worktree's base.
//
// These tools cover the lifecycle with structure, and vcs.integrate replaces the
// footgun ritual with a guarded 3-way squash merge run FROM the main checkout —
// which cannot revert post-base work. All git runs through host.RunHandler in
// argv mode (no shell), so there is no word-splitting/glob surface. Read tools
// (status/diff/log, worktree.list) stay available on a read-only server; the
// mutating tools (worktree.create/remove, vcs.commit, vcs.integrate) are omitted
// there. No LLM.

// vcsDiffTruncate caps vcs.diff's returned text; the full diff spills to a
// sidecar (reusing host.run's spill) so nothing is lost.
const vcsDiffTruncate = 16384

// registerVCSTools wires the vcs.* / worktree.* tools.
func (srv *Server) registerVCSTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "vcs.status",
		Description: "Read a worktree's git status, structured. {dir (required, the worktree/repo path)} → {ok, branch, upstream?, ahead, behind, clean, files[{xy, path}]} (porcelain v1; xy is the two-letter status code). Read-only.",
	}, srv.handleVCSStatus)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "vcs.diff",
		Description: "Read a git diff, textual. {dir (required), from? (a rev), to? (a rev), paths? ([]path to limit), stat? (--stat summary), name_only? (just file names), include_untracked?} → {ok, diff, truncated?, output_path?, warnings?, untracked?}. With no from/to, the working-tree diff; with from only, from..working; with both, from..to. Untracked files are omitted unless include_untracked is true; omitted untracked files produce a warning. Read-only.",
	}, srv.handleVCSDiff)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "vcs.log",
		Description: "Read recent commits. {dir (required), n? (max commits, default 20), paths? ([]path to limit)} → {ok, commits[{hash, subject}]}. Read-only.",
	}, srv.handleVCSLog)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "worktree.list",
		Description: "List a repo's git worktrees. {dir (required, any path inside the repo)} → {ok, worktrees[{path, head, branch?, detached?}]}. Read-only.",
	}, srv.handleWorktreeList)

	if srv.readOnly {
		return
	}

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "worktree.create",
		Description: "Create a git worktree on a new branch — the structured `git worktree add -b`. {dir (required, the main repo), branch (required, the new branch), base? (the start point; default \"main\"), path? (the worktree dir; default .worktrees/<branch>)} → {ok, path, branch, base}. The worktree lands under .worktrees/ by convention.",
	}, srv.handleWorktreeCreate)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "worktree.remove",
		Description: "Remove a git worktree — the structured `git worktree remove`. {dir (required, the main repo), path (required, the worktree path), force? (discard a dirty worktree)} → {ok, removed}. ",
	}, srv.handleWorktreeRemove)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "vcs.commit",
		Description: "Stage and commit in a worktree. {dir (required), message (required), paths? ([]path to stage; default: all changes via `git add -A`)} → {ok, commit, nothing_to_commit?}. Mutating.",
	}, srv.handleVCSCommit)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name: "vcs.integrate",
		Description: "Land a worktree's feature branch onto the integration branch SAFELY — the guarded replacement for the `reset --soft main` squash ritual that once destroyed main. Runs FROM the main checkout: {dir (required, the main checkout, which MUST be on `onto`), branch (required, the feature branch to integrate), onto? (the integration branch; default \"main\"), message (required, the squash commit message), worktree_path? (remove this worktree on success), delete_branch? (delete `branch` on success)} → {ok, integrated, commit?, conflicts[]?, refused?}. " +
			"Guards (refuses, never forces): `dir` must be on `onto`; `dir`'s tree must be clean; `branch` must have commits beyond its merge-base with `onto`. On clean success it does `git merge --squash <branch>` (a real 3-way merge against the CURRENT onto tip — it cannot revert work landed since the branch's base) + commit. On conflict it restores the clean tip and returns the conflicting paths. Mutating.",
	}, srv.handleVCSIntegrate)
}

// ── vcs.status ────────────────────────────────────────────────────────────────

// VCSStatusArgs is the input to vcs.status.
type VCSStatusArgs struct {
	Dir string `json:"dir"`
}

// VCSStatusOK is the vcs.status result.
type VCSStatusOK struct {
	OK       bool             `json:"ok"`
	Branch   string           `json:"branch"`
	Upstream string           `json:"upstream,omitempty"`
	Ahead    int              `json:"ahead"`
	Behind   int              `json:"behind"`
	Clean    bool             `json:"clean"`
	Files    []VCSStatusEntry `json:"files"`
}

// VCSStatusEntry is one porcelain status line: the two-letter XY code and path.
type VCSStatusEntry struct {
	XY   string `json:"xy"`
	Path string `json:"path"`
}

func (srv *Server) handleVCSStatus(ctx context.Context, req *mcpsdk.CallToolRequest, args VCSStatusArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "vcs.status"); rerr != nil {
		return rerr, nil, nil
	}
	out, exit, err := gitRun(ctx, args.Dir, "status", "--porcelain", "--branch")
	if rerr := gitErr("vcs.status", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	res := VCSStatusOK{OK: true, Clean: true, Files: []VCSStatusEntry{}}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			parseStatusBranch(strings.TrimPrefix(line, "## "), &res)
			continue
		}
		res.Clean = false
		xy := line
		path := ""
		if len(line) >= 3 {
			xy = line[:2]
			path = strings.TrimSpace(line[3:])
		}
		res.Files = append(res.Files, VCSStatusEntry{XY: xy, Path: path})
	}
	return nil, res, nil
}

// parseStatusBranch parses a porcelain `## ` branch header, e.g.
// "feat...origin/feat [ahead 2, behind 1]" or "main".
func parseStatusBranch(s string, res *VCSStatusOK) {
	// Trailing "[ahead N, behind M]".
	if i := strings.Index(s, " ["); i >= 0 {
		tail := strings.Trim(s[i+2:], "[]")
		for _, part := range strings.Split(tail, ", ") {
			if n, ok := parseCount(part, "ahead "); ok {
				res.Ahead = n
			}
			if n, ok := parseCount(part, "behind "); ok {
				res.Behind = n
			}
		}
		s = s[:i]
	}
	// "branch...upstream".
	if i := strings.Index(s, "..."); i >= 0 {
		res.Branch = s[:i]
		res.Upstream = s[i+3:]
		return
	}
	res.Branch = s
}

func parseCount(part, prefix string) (int, bool) {
	if !strings.HasPrefix(part, prefix) {
		return 0, false
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimPrefix(part, prefix), "%d", &n); err != nil {
		return 0, false
	}
	return n, true
}

// ── vcs.diff ──────────────────────────────────────────────────────────────────

// VCSDiffArgs is the input to vcs.diff.
type VCSDiffArgs struct {
	Dir              string   `json:"dir"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
	Paths            []string `json:"paths,omitempty"`
	Stat             bool     `json:"stat,omitempty"`
	NameOnly         bool     `json:"name_only,omitempty"`
	IncludeUntracked bool     `json:"include_untracked,omitempty"`
}

// VCSDiffOK is the vcs.diff result.
type VCSDiffOK struct {
	OK         bool     `json:"ok"`
	Diff       string   `json:"diff"`
	Truncated  bool     `json:"truncated,omitempty"`
	OutputPath string   `json:"output_path,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
	Untracked  []string `json:"untracked,omitempty"`
}

func (srv *Server) handleVCSDiff(ctx context.Context, req *mcpsdk.CallToolRequest, args VCSDiffArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "vcs.diff"); rerr != nil {
		return rerr, nil, nil
	}
	gitArgs := []string{"diff"}
	if args.Stat {
		gitArgs = append(gitArgs, "--stat")
	}
	if args.NameOnly {
		gitArgs = append(gitArgs, "--name-only")
	}
	if args.From != "" {
		gitArgs = append(gitArgs, args.From)
	}
	if args.To != "" {
		gitArgs = append(gitArgs, args.To)
	}
	if len(args.Paths) > 0 {
		gitArgs = append(gitArgs, "--")
		gitArgs = append(gitArgs, args.Paths...)
	}
	out, exit, err := gitRun(ctx, args.Dir, gitArgs...)
	if rerr := gitErr("vcs.diff", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	untracked, uerr := workingTreeUntracked(ctx, args)
	if uerr != nil {
		return uerr, nil, nil
	}
	var warnings []string
	if len(untracked) > 0 {
		if args.IncludeUntracked {
			var aerr *mcpsdk.CallToolResult
			out, aerr = appendUntrackedDiff(ctx, args, out, untracked)
			if aerr != nil {
				return aerr, nil, nil
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("%d untracked file(s) omitted by git diff; pass include_untracked:true to include them", len(untracked)))
		}
	}
	res := VCSDiffOK{OK: true, Diff: out}
	if len(warnings) > 0 {
		res.Warnings = warnings
		res.Untracked = untracked
	}
	if len(out) > vcsDiffTruncate {
		res.Truncated = true
		if path, werr := writeHostRunOutput(out); werr == nil {
			res.OutputPath = path
		}
		res.Diff = out[:vcsDiffTruncate] + "\n… diff truncated (full: " + res.OutputPath + ") …\n"
	}
	return nil, res, nil
}

func workingTreeUntracked(ctx context.Context, args VCSDiffArgs) ([]string, *mcpsdk.CallToolResult) {
	if args.To != "" {
		return nil, nil
	}
	out, exit, err := gitRun(ctx, args.Dir, "ls-files", "--others", "--exclude-standard")
	if rerr := gitErr("vcs.diff (untracked)", out, exit, err); rerr != nil {
		return nil, rerr
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !pathFilterAllows(args.Paths, line) {
			continue
		}
		files = append(files, line)
	}
	return files, nil
}

func appendUntrackedDiff(ctx context.Context, args VCSDiffArgs, out string, untracked []string) (string, *mcpsdk.CallToolResult) {
	if len(untracked) == 0 {
		return out, nil
	}
	if args.NameOnly {
		var b strings.Builder
		b.WriteString(strings.TrimRight(out, "\n"))
		for _, p := range untracked {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p)
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		return b.String(), nil
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(out, "\n"))
	for _, p := range untracked {
		diffArgs := []string{"diff", "--no-index"}
		if args.Stat {
			diffArgs = append(diffArgs, "--stat")
		}
		diffArgs = append(diffArgs, "--", "/dev/null", p)
		uout, exit, err := gitRun(ctx, args.Dir, diffArgs...)
		if err != nil {
			return "", buildToolError(ErrBadRequest, fmt.Sprintf("vcs.diff (untracked): %v", err))
		}
		// git diff --no-index exits 1 when it found differences.
		if exit != 0 && exit != 1 {
			return "", buildToolError(ErrBadRequest, fmt.Sprintf("vcs.diff (untracked): git exited %d: %s", exit, strings.TrimSpace(uout)))
		}
		if strings.TrimSpace(uout) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimRight(uout, "\n"))
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	return b.String(), nil
}

func pathFilterAllows(filters []string, path string) bool {
	if len(filters) == 0 {
		return true
	}
	cleanPath := filepath.Clean(path)
	for _, filter := range filters {
		cleanFilter := filepath.Clean(filter)
		if cleanPath == cleanFilter || strings.HasPrefix(cleanPath, cleanFilter+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ── vcs.log ───────────────────────────────────────────────────────────────────

// VCSLogArgs is the input to vcs.log.
type VCSLogArgs struct {
	Dir   string   `json:"dir"`
	N     int      `json:"n,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// VCSLogOK is the vcs.log result.
type VCSLogOK struct {
	OK      bool        `json:"ok"`
	Commits []VCSCommit `json:"commits"`
}

// VCSCommit is one commit: its full hash and subject line.
type VCSCommit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
}

func (srv *Server) handleVCSLog(ctx context.Context, req *mcpsdk.CallToolRequest, args VCSLogArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "vcs.log"); rerr != nil {
		return rerr, nil, nil
	}
	n := args.N
	if n <= 0 {
		n = 20
	}
	// %x1f is the unit-separator: a delimiter that cannot appear in a hash/subject.
	gitArgs := []string{"log", fmt.Sprintf("--max-count=%d", n), "--pretty=format:%H%x1f%s"}
	if len(args.Paths) > 0 {
		gitArgs = append(gitArgs, "--")
		gitArgs = append(gitArgs, args.Paths...)
	}
	out, exit, err := gitRun(ctx, args.Dir, gitArgs...)
	if rerr := gitErr("vcs.log", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	res := VCSLogOK{OK: true, Commits: []VCSCommit{}}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1f", 2)
		c := VCSCommit{Hash: parts[0]}
		if len(parts) == 2 {
			c.Subject = parts[1]
		}
		res.Commits = append(res.Commits, c)
	}
	return nil, res, nil
}

// ── worktree.list ─────────────────────────────────────────────────────────────

// WorktreeListArgs is the input to worktree.list.
type WorktreeListArgs struct {
	Dir string `json:"dir"`
}

// WorktreeListOK is the worktree.list result.
type WorktreeListOK struct {
	OK        bool           `json:"ok"`
	Worktrees []WorktreeItem `json:"worktrees"`
}

// WorktreeItem is one worktree from `git worktree list --porcelain`.
type WorktreeItem struct {
	Path     string `json:"path"`
	Head     string `json:"head,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Detached bool   `json:"detached,omitempty"`
}

func (srv *Server) handleWorktreeList(ctx context.Context, req *mcpsdk.CallToolRequest, args WorktreeListArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "worktree.list"); rerr != nil {
		return rerr, nil, nil
	}
	out, exit, err := gitRun(ctx, args.Dir, "worktree", "list", "--porcelain")
	if rerr := gitErr("worktree.list", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	res := WorktreeListOK{OK: true, Worktrees: []WorktreeItem{}}
	var cur *WorktreeItem
	flush := func() {
		if cur != nil {
			res.Worktrees = append(res.Worktrees, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &WorktreeItem{Path: strings.TrimPrefix(line, "worktree ")}
		case cur == nil:
			// skip
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "detached":
			cur.Detached = true
		}
	}
	flush()
	return nil, res, nil
}

// ── worktree.create ───────────────────────────────────────────────────────────

// WorktreeCreateArgs is the input to worktree.create.
type WorktreeCreateArgs struct {
	Dir    string `json:"dir"`
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
	Path   string `json:"path,omitempty"`
}

// WorktreeCreateOK is the worktree.create result.
type WorktreeCreateOK struct {
	OK     bool   `json:"ok"`
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
}

func (srv *Server) handleWorktreeCreate(ctx context.Context, req *mcpsdk.CallToolRequest, args WorktreeCreateArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "worktree.create"); rerr != nil {
		return rerr, nil, nil
	}
	if args.Branch == "" {
		return buildToolError(ErrBadRequest, "worktree.create: branch is required"), nil, nil
	}
	base := args.Base
	if base == "" {
		base = "main"
	}
	path := args.Path
	if path == "" {
		// Convention (AGENTS.md): worktrees live under .worktrees/ in the repo root.
		path = filepath.Join(".worktrees", strings.ReplaceAll(args.Branch, "/", "-"))
	}
	out, exit, err := gitRun(ctx, args.Dir, "worktree", "add", "-b", args.Branch, path, base)
	if rerr := gitErr("worktree.create", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	// Resolve the worktree path to absolute for the caller's later use.
	abs := path
	if !filepath.IsAbs(path) {
		abs = filepath.Join(args.Dir, path)
	}
	return nil, WorktreeCreateOK{OK: true, Path: abs, Branch: args.Branch, Base: base}, nil
}

// ── worktree.remove ───────────────────────────────────────────────────────────

// WorktreeRemoveArgs is the input to worktree.remove.
type WorktreeRemoveArgs struct {
	Dir   string `json:"dir"`
	Path  string `json:"path"`
	Force bool   `json:"force,omitempty"`
}

// WorktreeRemoveOK is the worktree.remove result.
type WorktreeRemoveOK struct {
	OK      bool   `json:"ok"`
	Removed string `json:"removed"`
}

func (srv *Server) handleWorktreeRemove(ctx context.Context, req *mcpsdk.CallToolRequest, args WorktreeRemoveArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "worktree.remove"); rerr != nil {
		return rerr, nil, nil
	}
	if args.Path == "" {
		return buildToolError(ErrBadRequest, "worktree.remove: path is required"), nil, nil
	}
	gitArgs := []string{"worktree", "remove"}
	if args.Force {
		gitArgs = append(gitArgs, "--force")
	}
	gitArgs = append(gitArgs, args.Path)
	out, exit, err := gitRun(ctx, args.Dir, gitArgs...)
	if rerr := gitErr("worktree.remove", out, exit, err); rerr != nil {
		return rerr, nil, nil
	}
	return nil, WorktreeRemoveOK{OK: true, Removed: args.Path}, nil
}

// ── vcs.commit ────────────────────────────────────────────────────────────────

// VCSCommitArgs is the input to vcs.commit.
type VCSCommitArgs struct {
	Dir     string   `json:"dir"`
	Message string   `json:"message"`
	Paths   []string `json:"paths,omitempty"`
}

// VCSCommitOK is the vcs.commit result.
type VCSCommitOK struct {
	OK              bool   `json:"ok"`
	Commit          string `json:"commit,omitempty"`
	NothingToCommit bool   `json:"nothing_to_commit,omitempty"`
}

func (srv *Server) handleVCSCommit(ctx context.Context, req *mcpsdk.CallToolRequest, args VCSCommitArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "vcs.commit"); rerr != nil {
		return rerr, nil, nil
	}
	if args.Message == "" {
		return buildToolError(ErrBadRequest, "vcs.commit: message is required"), nil, nil
	}
	// Stage.
	if len(args.Paths) > 0 {
		addArgs := append([]string{"add", "--"}, args.Paths...)
		if out, exit, err := gitRun(ctx, args.Dir, addArgs...); gitErr("vcs.commit (add)", out, exit, err) != nil {
			return gitErr("vcs.commit (add)", out, exit, err), nil, nil
		}
	} else {
		if out, exit, err := gitRun(ctx, args.Dir, "add", "-A"); gitErr("vcs.commit (add)", out, exit, err) != nil {
			return gitErr("vcs.commit (add)", out, exit, err), nil, nil
		}
	}
	// Nothing staged → report rather than fail.
	if _, exit, err := gitRun(ctx, args.Dir, "diff", "--cached", "--quiet"); err == nil && exit == 0 {
		return nil, VCSCommitOK{OK: true, NothingToCommit: true}, nil
	}
	if out, exit, err := gitRun(ctx, args.Dir, "commit", "-m", args.Message); gitErr("vcs.commit", out, exit, err) != nil {
		return gitErr("vcs.commit", out, exit, err), nil, nil
	}
	head, _, _ := gitRun(ctx, args.Dir, "rev-parse", "HEAD")
	return nil, VCSCommitOK{OK: true, Commit: strings.TrimSpace(head)}, nil
}

// ── vcs.integrate ─────────────────────────────────────────────────────────────

// VCSIntegrateArgs is the input to vcs.integrate.
type VCSIntegrateArgs struct {
	Dir          string `json:"dir"`
	Branch       string `json:"branch"`
	Onto         string `json:"onto,omitempty"`
	Message      string `json:"message"`
	WorktreePath string `json:"worktree_path,omitempty"`
	DeleteBranch bool   `json:"delete_branch,omitempty"`
}

// VCSIntegrateOK is the vcs.integrate result. Exactly one of integrated /
// refused / conflicts is meaningful: refused names a precondition that failed
// (the guard fired), conflicts lists the paths a squash merge could not auto-
// resolve (the tip was restored), commit is the squash commit on success.
type VCSIntegrateOK struct {
	OK         bool     `json:"ok"`
	Integrated bool     `json:"integrated"`
	Commit     string   `json:"commit,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Refused    string   `json:"refused,omitempty"`
}

func (srv *Server) handleVCSIntegrate(ctx context.Context, req *mcpsdk.CallToolRequest, args VCSIntegrateArgs) (*mcpsdk.CallToolResult, any, error) {
	if rerr := requireDir(args.Dir, "vcs.integrate"); rerr != nil {
		return rerr, nil, nil
	}
	if args.Branch == "" {
		return buildToolError(ErrBadRequest, "vcs.integrate: branch is required"), nil, nil
	}
	if args.Message == "" {
		return buildToolError(ErrBadRequest, "vcs.integrate: message is required (the squash commit message)"), nil, nil
	}
	result, err := vcsops.Integrate(ctx, args.Dir, args.Branch, args.Onto, args.Message, vcsops.IntegrateOptions{
		WorktreePath: args.WorktreePath,
		DeleteBranch: args.DeleteBranch,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("vcs.integrate: %v", err)), nil, nil
	}
	return nil, VCSIntegrateOK{
		OK:         true,
		Integrated: result.Integrated,
		Commit:     result.Commit,
		Conflicts:  result.Conflicts,
		Refused:    result.Refused,
	}, nil
}

// ── git execution + small helpers ─────────────────────────────────────────────

// gitRun runs git in argv mode (no shell) via the shared host.RunHandler, the
// same primitive host.run uses, and returns combined stdout, the exit code, and
// an infra error (exec could not start). A non-zero exit is data, not err.
func gitRun(ctx context.Context, dir string, args ...string) (stdout string, exit int, err error) {
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}
	res, err := host.RunHandler(ctx, map[string]any{"cmd": "git", "args": anyArgs, "cwd": dir})
	if err != nil {
		return "", -1, err
	}
	if res.Error != "" && res.Data == nil {
		return "", -1, errors.New(res.Error)
	}
	exit, _ = res.Data["exit_code"].(int)
	stdout, _ = res.Data["stdout"].(string)
	return stdout, exit, nil
}

// requireDir validates the dir arg points at an accessible directory.
func requireDir(dir, tool string) *mcpsdk.CallToolResult {
	if dir == "" {
		return buildToolError(ErrBadRequest, tool+": dir is required")
	}
	return nil
}

// gitErr maps a git invocation to a tool error when it could not start (err) or
// exited non-zero (exit). It returns nil when the command succeeded. The git
// output is included so the caller sees the real message ("not a git repository",
// a merge conflict, etc.).
func gitErr(tool, out string, exit int, err error) *mcpsdk.CallToolResult {
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("%s: %v", tool, err))
	}
	if exit != 0 {
		return buildToolError(ErrBadRequest, fmt.Sprintf("%s: git exited %d: %s", tool, exit, strings.TrimSpace(out)))
	}
	return nil
}
