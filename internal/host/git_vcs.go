// Package host — host.git — git-backed VCS provider.
//
// Implements the `vcs` host_interface (see docs/architecture/hosts.md).  One
// prefix-fallback handler dispatches the seven vcs ops via the `op`
// arg.  Local git ops shell out to the `git` CLI; GitHub PR status/comment
// ops use the native GitHub REST API with GH_TOKEN/GITHUB_TOKEN. PR creation
// and deterministic rebase still use local git plumbing plus the remaining
// compatibility shims noted below.
//
// All exec calls go through the shared `cliExec` seam declared in
// cli_exec.go so tests don't shell out for real.
package host

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runRealCommand executes name+args with the given working directory
// and captures stdout/stderr/exit-code.  An infrastructure error
// (binary not found, etc.) is returned as err; non-zero exits surface
// via exitCode + stderr only.
func runRealCommand(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = envWithCLIExec(os.Environ(), CLIExecEnvFromCtx(ctx))
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err == nil {
		return stdoutBuf.String(), stderrBuf.String(), 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdoutBuf.String(), stderrBuf.String(), exitErr.ExitCode(), nil
	}
	return stdoutBuf.String(), stderrBuf.String(), -1, err
}

// GitVCSHandler implements host.git (prefix-fallback for all 7 ops).
//
// Required args:
//   - op (string): one of branch, diff, commit, push, open_pr, split_prs, pr_status, pr_comment, pr_rebase.
//
// Common optional args:
//   - workdir (string): working directory for the git command; defaults to cwd.
//
// Per-op input/output follows the vcs iface contract.
func GitVCSHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.git: op argument is required"}, nil
	}
	workdir, _ := args["workdir"].(string)

	switch op {
	case "branch":
		return gitBranch(ctx, workdir, args)
	case "diff":
		return gitDiff(ctx, workdir, args)
	case "commit":
		return gitCommit(ctx, workdir, args)
	case "push":
		return gitPush(ctx, workdir, args)
	case "open_pr":
		return ghOpenPR(ctx, workdir, args)
	case "split_prs":
		return gitSplitPRs(ctx, workdir, args)
	case "pr_status":
		return ghPRStatus(ctx, workdir, args)
	case "pr_comment":
		return ghPRComment(ctx, workdir, args)
	case "pr_rebase":
		return ghPRRebase(ctx, args)
	default:
		return Result{Error: fmt.Sprintf("host.git: unknown op %q", op)}, nil
	}
}

// ─── git ops (always shell to `git`) ───────────────────────────────────────

func gitBranch(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	name, _ := args["name"].(string)
	base, _ := args["base"].(string)
	if strings.TrimSpace(name) == "" {
		return Result{Error: "git.branch: name argument is required"}, nil
	}
	gitArgs := []string{"checkout", "-b", name}
	if base != "" {
		gitArgs = append(gitArgs, base)
	}
	_, stderr, code, err := cliExec(ctx, workdir, "git", gitArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.branch: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("git.branch: %s", strings.TrimSpace(stderr))}, nil
	}
	return Result{Data: map[string]any{"ok": true, "branch": name}}, nil
}

func gitDiff(ctx context.Context, workdir string, _ map[string]any) (Result, error) {
	stdout, stderr, code, err := cliExec(ctx, workdir, "git", "diff", "--patch")
	if err != nil {
		return Result{Error: fmt.Sprintf("git.diff: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("git.diff: %s", strings.TrimSpace(stderr))}, nil
	}
	// Also surface the list of changed files for `bind:` ergonomics. This is
	// a best-effort convenience: if `--name-only` fails (exec error or
	// non-zero exit) we degrade to an empty file list rather than failing the
	// whole call, since the patch content above is the authoritative result.
	var files []any
	if filesOut, _, fcode, ferr := cliExec(ctx, workdir, "git", "diff", "--name-only"); ferr == nil && fcode == 0 {
		for _, ln := range strings.Split(strings.TrimSpace(filesOut), "\n") {
			if ln != "" {
				files = append(files, ln)
			}
		}
	}
	return Result{Data: map[string]any{"diff": stdout, "files": files}}, nil
}

func gitCommit(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	message, _ := args["message"].(string)
	if strings.TrimSpace(message) == "" {
		return Result{Error: "git.commit: message argument is required"}, nil
	}
	// stage_all: when true, run `git add -A` first so new untracked files are
	// included in the commit (analogous to `git commit -A` but also covers
	// deletions). Takes precedence over the files list and the -a fallback.
	stageAll, _ := args["stage_all"].(bool)
	if stageAll {
		if _, addStderr, addCode, addErr := cliExec(ctx, workdir, "git", "add", "-A"); addErr != nil || addCode != 0 {
			return Result{Error: fmt.Sprintf("git.commit: stage_all: %s", strings.TrimSpace(addStderr))}, nil
		}
	}
	// Optional files list; when empty, fall back to `git commit -a`.
	// Tolerate two states that aren't really failures from the
	// pipeline's perspective:
	//   1. A listed file doesn't exist in the worktree yet (the
	//      proposer named files the operator hasn't created).
	//   2. There's nothing staged/dirty (the operator hasn't made
	//      edits yet, or the proposed file list was conceptual).
	// Both used to bubble out as `on_error: idle` and silently
	// bounce the bugfix pipeline back to the parked room. Treat as
	// "no commit made" success so the pipeline keeps moving and the
	// downstream phases can show their own diagnostics.
	// `files` may arrive as []any (literal YAML list) or []string (when
	// the rendered expression preserved the inner element type). Accept
	// both — the pre-fix path-comparison shape that hid the
	// "implementing is a no-op" bug also masquerades here: a YAML
	// `{{ world.x }}` reference whose underlying value is `[]string`
	// renders as `[]string` and the `[]any` type assertion silently
	// returns false → len()==0 → handler falls back to `git commit -a`
	// → doesn't stage new files → commit is a no-op → test thinks
	// implementing succeeded when it didn't.
	var filesAny []any
	if v, ok := args["files"].([]any); ok {
		filesAny = v
	} else if v, ok := args["files"].([]string); ok {
		filesAny = make([]any, len(v))
		for i, s := range v {
			filesAny[i] = s
		}
	}
	if len(filesAny) > 0 {
		addArgs := []string{"add", "--"}
		var listed []string
		for _, f := range filesAny {
			if s, ok := f.(string); ok && s != "" {
				addArgs = append(addArgs, s)
				listed = append(listed, s)
			}
		}
		if _, addStderr, addCode, addErr := cliExec(ctx, workdir, "git", addArgs...); addErr != nil || addCode != 0 {
			// `pathspec ... did not match any files` — files the
			// proposal named don't exist yet. Surface as a soft skip
			// rather than failing the room.
			if strings.Contains(addStderr, "did not match any files") {
				return Result{Data: map[string]any{
					"ok":             true,
					"sha":            "",
					"skipped_reason": "pathspec did not match (no files to commit)",
					"files":          listed,
				}}, nil
			}
			return Result{Error: fmt.Sprintf("git.commit: stage: %s", strings.TrimSpace(addStderr))}, nil
		}
	}
	commitArgs := []string{"commit", "-m", message}
	if len(filesAny) == 0 && !stageAll {
		// No explicit files and no stage_all → assume -a so authors can use the
		// fast-path on a dirty tree.
		commitArgs = []string{"commit", "-a", "-m", message}
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "git", commitArgs...)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.commit: exec: %v", err)}, nil
	}
	if code != 0 {
		// `nothing to commit` and `no changes added to commit` go to
		// git's STDOUT (not stderr), so check both streams. Without
		// this the leniency check above silently misses the most
		// common no-op state and the on_error: idle arc fires.
		combined := stdout + "\n" + stderr
		// `nothing to commit`            — clean tree
		// `no changes added to commit`   — `git add` skipped, tracked changes exist but unstaged
		// `nothing added to commit but untracked files present` — clean tracked tree, only untracked
		// All three are "no-op success" from the pipeline's perspective.
		if strings.Contains(combined, "nothing to commit") ||
			strings.Contains(combined, "no changes added to commit") ||
			strings.Contains(combined, "nothing added to commit") {
			return Result{Data: map[string]any{
				"ok":             true,
				"sha":            "",
				"skipped_reason": "nothing to commit",
			}}, nil
		}
		// Surface a non-empty message even when stderr is empty —
		// otherwise the operator sees `git.commit: ` with no clue.
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(stdout)
		}
		if msg == "" {
			msg = fmt.Sprintf("git exited with code %d (no output)", code)
		}
		return Result{Error: fmt.Sprintf("git.commit: %s", msg)}, nil
	}
	sha, _, _, _ := cliExec(ctx, workdir, "git", "rev-parse", "HEAD")
	return Result{Data: map[string]any{
		"ok":  true,
		"sha": strings.TrimSpace(sha),
	}}, nil
}

func gitPush(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	remote, _ := args["remote"].(string)
	if remote == "" {
		remote = "origin"
	}
	// Push the current HEAD.  `git push -u <remote> HEAD` makes the
	// upstream tracking branch on first push, no-ops on subsequent
	// pushes.
	_, stderr, code, err := cliExec(ctx, workdir, "git", "push", "-u", remote, "HEAD")
	if err != nil {
		return Result{Error: fmt.Sprintf("git.push: exec: %v", err)}, nil
	}
	if code != 0 {
		return Result{Error: fmt.Sprintf("git.push: %s", strings.TrimSpace(stderr))}, nil
	}
	// Best-effort URL discovery (remote URL).  Not all push targets
	// have a fetchable URL (e.g. local file remotes); empty string is
	// fine and the contract just says `url: string`.
	urlOut, _, _, _ := cliExec(ctx, workdir, "git", "remote", "get-url", remote)
	return Result{Data: map[string]any{
		"ok":  true,
		"url": strings.TrimSpace(urlOut),
	}}, nil
}

// ─── GitHub PR ops ────────────────────────────────────────────────────────

func ghOpenPR(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	title, _ := args["title"].(string)
	body, _ := args["body"].(string)
	base, _ := args["base"].(string)
	if strings.TrimSpace(title) == "" {
		return Result{Error: "git.open_pr: title argument is required"}, nil
	}
	// GitHub cannot open a PR until the head branch is published. Publish the
	// current HEAD first, mirroring the sibling gitPush handler.
	remote, _ := args["remote"].(string)
	if remote == "" {
		remote = "origin"
	}
	if _, pushStderr, pushCode, pushErr := cliExec(ctx, workdir, "git", "push", "-u", remote, "HEAD"); pushErr != nil {
		return Result{Error: fmt.Sprintf("git.open_pr: push: exec: %v", pushErr)}, nil
	} else if pushCode != 0 {
		return Result{Error: fmt.Sprintf("git.open_pr: push: %s", strings.TrimSpace(pushStderr))}, nil
	}
	repo, errMsg := resolveGitHubRepo(ctx, workdir, args)
	if errMsg != "" {
		return Result{Error: "git.open_pr: " + errMsg}, nil
	}
	head, _ := args["head"].(string)
	if strings.TrimSpace(head) == "" {
		stdout, stderr, code, err := cliExec(ctx, workdir, "git", "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil {
			return Result{Error: fmt.Sprintf("git.open_pr: head branch: %v", err)}, nil
		}
		if code != 0 {
			return Result{Error: fmt.Sprintf("git.open_pr: head branch: %s", strings.TrimSpace(stderr))}, nil
		}
		head = strings.TrimSpace(stdout)
	}
	if head == "" || head == "HEAD" {
		return Result{Error: "git.open_pr: head branch is required for native GitHub PR creation"}, nil
	}
	if strings.TrimSpace(base) == "" {
		var repoMeta githubRepository
		code, resp, err := githubAPIJSON(ctx, "GET", "repos/"+repo, nil, &repoMeta)
		if err != nil {
			return Result{Error: fmt.Sprintf("git.open_pr: default branch: %v", err)}, nil
		}
		if code >= 300 {
			return Result{Error: fmt.Sprintf("git.open_pr: default branch: %s", githubAPIError(resp))}, nil
		}
		base = strings.TrimSpace(repoMeta.DefaultBranch)
		if base == "" {
			base = "main"
		}
	}
	payload := map[string]any{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	}
	if optBool(args, "draft", false) {
		payload["draft"] = true
	}
	var pr githubPullRequestCreateResponse
	code, resp, err := githubAPIJSON(ctx, "POST", "repos/"+repo+"/pulls", payload, &pr)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.open_pr: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("git.open_pr: %s", githubAPIError(resp))}, nil
	}
	prID := fmt.Sprintf("%d", pr.Number)
	return Result{Data: map[string]any{
		"ok":      true,
		"outcome": "opened",
		"url":     pr.HTMLURL,
		"pr_id":   prID,
		"repo":    repo,
	}}, nil
}

type gitSplitPRPlan struct {
	Buckets []gitSplitPRBucket `json:"buckets"`
}

type gitSplitPRBucket struct {
	Concern string   `json:"concern"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	SHAs    []string `json:"shas"`
}

func gitSplitPRs(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	bucketsJSON, _ := args["buckets_json"].(string)
	if strings.TrimSpace(bucketsJSON) == "" {
		return Result{Data: map[string]any{
			"ok":         true,
			"opened_prs": map[string]any{"items": []any{}},
		}}, nil
	}
	var plan gitSplitPRPlan
	if err := json.Unmarshal([]byte(bucketsJSON), &plan); err != nil {
		return Result{Error: fmt.Sprintf("git.split_prs: buckets_json: %v", err)}, nil
	}
	integration, _ := args["integration_branch"].(string)
	if strings.TrimSpace(integration) == "" {
		integration = "main"
	}
	source, _ := args["source_branch"].(string)
	if strings.TrimSpace(source) == "" {
		return Result{Error: "git.split_prs: source_branch argument is required"}, nil
	}
	remote, _ := args["remote"].(string)
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	repo, _ := args["repo"].(string)
	dryRun := optBool(args, "dry_run", false)
	root := workdir
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	items := make([]any, 0, len(plan.Buckets))
	for _, bucket := range plan.Buckets {
		concern := strings.TrimSpace(bucket.Concern)
		branch := source + "-split-" + slugForBranch(concern)
		item := map[string]any{
			"concern": concern,
			"branch":  branch,
			"url":     "",
			"commits": len(bucket.SHAs),
		}
		if !dryRun {
			if strings.TrimSpace(bucket.Title) == "" {
				return Result{Error: fmt.Sprintf("git.split_prs: bucket %q title is required", concern)}, nil
			}
			if len(bucket.SHAs) == 0 {
				return Result{Error: fmt.Sprintf("git.split_prs: bucket %q has no commits", concern)}, nil
			}
			tempDir, err := os.MkdirTemp("", "kitsoki-pr-split-*")
			if err != nil {
				return Result{Error: fmt.Sprintf("git.split_prs: tempdir: %v", err)}, nil
			}
			wt := filepath.Join(tempDir, "wt")
			cleanup := func() {
				_, _, _, _ = cliExec(ctx, root, "git", "worktree", "remove", "--force", wt)
				_ = os.RemoveAll(tempDir)
			}
			if _, stderr, code, err := cliExec(ctx, root, "git", "worktree", "add", "-q", "--detach", wt, integration); err != nil {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: worktree add: exec: %v", err)}, nil
			} else if code != 0 {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: worktree add: %s", strings.TrimSpace(stderr))}, nil
			}
			if _, stderr, code, err := cliExec(ctx, wt, "git", "switch", "-q", "-c", branch); err != nil {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: switch: exec: %v", err)}, nil
			} else if code != 0 {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: switch: %s", strings.TrimSpace(stderr))}, nil
			}
			cherryPickArgs := append([]string{"cherry-pick"}, bucket.SHAs...)
			if _, stderr, code, err := cliExec(ctx, wt, "git", cherryPickArgs...); err != nil {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: cherry-pick: exec: %v", err)}, nil
			} else if code != 0 {
				cleanup()
				return Result{Error: fmt.Sprintf("git.split_prs: cherry-pick: %s", strings.TrimSpace(stderr))}, nil
			}
			prRes, _ := ghOpenPR(ctx, wt, map[string]any{
				"title":  bucket.Title,
				"body":   bucket.Body,
				"base":   integration,
				"head":   branch,
				"remote": remote,
				"repo":   repo,
			})
			cleanup()
			if prRes.Error != "" {
				return Result{Error: "git.split_prs: " + prRes.Error}, nil
			}
			item["url"] = prRes.Data["url"]
		}
		items = append(items, item)
	}
	return Result{Data: map[string]any{
		"ok":         true,
		"opened_prs": map[string]any{"items": items},
	}}, nil
}

func slugForBranch(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '/':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func ghPRStatus(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	prID, _ := args["pr_id"].(string)
	repo, num, errMsg := resolveGitHubPRRef(ctx, workdir, args, prID)
	if errMsg != "" {
		return Result{Error: "git.pr_status: " + errMsg}, nil
	}
	if num == "" {
		return Result{Error: "git.pr_status: pr_id argument is required"}, nil
	}

	var pr githubPullRequest
	code, resp, err := githubAPIJSON(ctx, "GET", fmt.Sprintf("repos/%s/pulls/%s", repo, num), nil, &pr)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.pr_status: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("git.pr_status: %s", githubAPIError(resp))}, nil
	}

	var comments []githubIssueComment
	code, resp, err = githubAPIJSON(ctx, "GET", fmt.Sprintf("repos/%s/issues/%s/comments?per_page=100", repo, num), nil, &comments)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.pr_status comments: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("git.pr_status comments: %s", githubAPIError(resp))}, nil
	}

	checks, ciState, errMsg := githubPRChecks(ctx, repo, pr.Head.SHA)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}
	if pr.State == "closed" && pr.Merged && ciState == "pending" && len(checks) == 0 {
		ciState = "success"
	}
	checksSummary, failedLog := githubPRChecksText(ciState, checks)
	return Result{Data: map[string]any{
		"state":          ciState,
		"pr_state":       strings.ToLower(strings.TrimSpace(pr.State)),
		"merged":         pr.Merged,
		"url":            pr.HTMLURL,
		"head_sha":       pr.Head.SHA,
		"checks":         checks,
		"checks_summary": checksSummary,
		"failed_log":     failedLog,
		"comments":       githubIssueCommentsForWorld(comments),
		"raw_comments":   comments,
	}}, nil
}

func ghPRComment(ctx context.Context, workdir string, args map[string]any) (Result, error) {
	prID, _ := args["pr_id"].(string)
	body, _ := args["body"].(string)
	if strings.TrimSpace(prID) == "" || strings.TrimSpace(body) == "" {
		return Result{Error: "git.pr_comment: pr_id and body are required"}, nil
	}
	repo, num, errMsg := resolveGitHubPRRef(ctx, workdir, args, prID)
	if errMsg != "" {
		return Result{Error: "git.pr_comment: " + errMsg}, nil
	}
	path := fmt.Sprintf("repos/%s/issues/%s/comments", repo, num)
	payload := map[string]any{"body": body}
	if optBool(args, "approve", false) || optBool(args, "request_changes", false) {
		event := "COMMENT"
		if optBool(args, "approve", false) {
			event = "APPROVE"
		}
		if optBool(args, "request_changes", false) {
			event = "REQUEST_CHANGES"
		}
		path = fmt.Sprintf("repos/%s/pulls/%s/reviews", repo, num)
		payload = map[string]any{"body": body, "event": event}
	}
	var raw map[string]any
	code, resp, err := githubAPIJSON(ctx, "POST", path, payload, &raw)
	if err != nil {
		return Result{Error: fmt.Sprintf("git.pr_comment: %v", err)}, nil
	}
	if code >= 300 {
		return Result{Error: fmt.Sprintf("git.pr_comment: %s", githubAPIError(resp))}, nil
	}
	commentURL, _ := raw["html_url"].(string)
	if commentURL == "" {
		commentURL, _ = raw["url"].(string)
	}
	return Result{Data: map[string]any{"ok": true, "url": commentURL, "comment_id": commentURL}}, nil
}

type githubPullRequest struct {
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type githubPullRequestCreateResponse struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
}

type githubRepository struct {
	DefaultBranch string `json:"default_branch"`
}

type githubIssueComment struct {
	ID      any    `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	URL     string `json:"url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

type githubCombinedStatus struct {
	State    string `json:"state"`
	Statuses []struct {
		Context   string `json:"context"`
		State     string `json:"state"`
		TargetURL string `json:"target_url"`
	} `json:"statuses"`
}

type githubCheckRunsResponse struct {
	CheckRuns []struct {
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HTMLURL    string `json:"html_url"`
	} `json:"check_runs"`
}

func resolveGitHubPRRef(ctx context.Context, workdir string, args map[string]any, prID string) (string, string, string) {
	repo, num := splitGitHubPRRef(prID)
	if r, _ := args["repo"].(string); strings.TrimSpace(r) != "" {
		repo = strings.TrimSpace(r)
	}
	if num == "" {
		num = strings.TrimSpace(prID)
	}
	if strings.TrimSpace(num) == "" {
		return repo, num, "pr_id argument is required"
	}
	if strings.TrimSpace(repo) != "" {
		return repo, num, ""
	}
	repo, errMsg := resolveGitHubRepo(ctx, workdir, args)
	return repo, num, errMsg
}

func resolveGitHubRepo(ctx context.Context, workdir string, args map[string]any) (string, string) {
	if r, _ := args["repo"].(string); strings.TrimSpace(r) != "" {
		return strings.TrimSpace(r), ""
	}
	remote, _ := args["remote"].(string)
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	stdout, stderr, code, err := cliExec(ctx, workdir, "git", "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Sprintf("repo argument is required and git remote lookup failed: %v", err)
	}
	if code != 0 {
		return "", fmt.Sprintf("repo argument is required and git remote lookup failed: %s", strings.TrimSpace(stderr))
	}
	repo := githubRepoFromRemote(stdout)
	if repo == "" {
		return "", fmt.Sprintf("repo argument is required and remote %q is not a GitHub repository", remote)
	}
	return repo, ""
}

func splitGitHubPRRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}
	if !strings.Contains(ref, "github.com/") {
		return "", ref
	}
	after := ref[strings.Index(ref, "github.com/")+len("github.com/"):]
	after = strings.Trim(after, "/")
	parts := strings.Split(after, "/")
	if len(parts) >= 4 && parts[2] == "pull" {
		return parts[0] + "/" + strings.TrimSuffix(parts[1], ".git"), parts[3]
	}
	return "", ref
}

func githubRepoFromRemote(remoteURL string) string {
	s := strings.TrimSpace(remoteURL)
	s = strings.TrimSuffix(s, ".git")
	switch {
	case strings.HasPrefix(s, "git@github.com:"):
		return strings.TrimPrefix(s, "git@github.com:")
	case strings.Contains(s, "github.com/"):
		after := s[strings.Index(s, "github.com/")+len("github.com/"):]
		after = strings.Trim(after, "/")
		parts := strings.Split(after, "/")
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
	}
	return ""
}

func githubPRChecks(ctx context.Context, repo, sha string) ([]map[string]any, string, string) {
	if strings.TrimSpace(sha) == "" {
		return nil, "pending", ""
	}
	var combined githubCombinedStatus
	code, resp, err := githubAPIJSON(ctx, "GET", fmt.Sprintf("repos/%s/commits/%s/status", repo, sha), nil, &combined)
	if err != nil {
		return nil, "", fmt.Sprintf("git.pr_status checks: %v", err)
	}
	if code >= 300 {
		return nil, "", fmt.Sprintf("git.pr_status checks: %s", githubAPIError(resp))
	}
	var runs githubCheckRunsResponse
	code, resp, err = githubAPIJSON(ctx, "GET", fmt.Sprintf("repos/%s/commits/%s/check-runs?per_page=100", repo, sha), nil, &runs)
	if err != nil {
		return nil, "", fmt.Sprintf("git.pr_status check-runs: %v", err)
	}
	if code >= 300 {
		return nil, "", fmt.Sprintf("git.pr_status check-runs: %s", githubAPIError(resp))
	}

	state := normalizeCIState(combined.State)
	if state == "" {
		state = "pending"
	}
	var failed []map[string]any
	for _, st := range combined.Statuses {
		stState := normalizeCIState(st.State)
		if stState == "failure" {
			state = "failure"
			failed = append(failed, map[string]any{"name": st.Context, "state": st.State, "url": st.TargetURL})
		} else if stState == "pending" && state != "failure" {
			state = "pending"
		}
	}
	sawRun := false
	allRunsGreen := true
	for _, run := range runs.CheckRuns {
		sawRun = true
		runState := normalizeCheckRunState(run.Status, run.Conclusion)
		if runState == "failure" {
			state = "failure"
			allRunsGreen = false
			failed = append(failed, map[string]any{"name": run.Name, "status": run.Status, "conclusion": run.Conclusion, "url": run.HTMLURL})
		} else if runState == "pending" {
			allRunsGreen = false
			if state != "failure" {
				state = "pending"
			}
		}
	}
	if sawRun && allRunsGreen && state != "failure" {
		state = "success"
	}
	return failed, state, ""
}

func normalizeCIState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "success":
		return "success"
	case "failure", "error":
		return "failure"
	case "pending", "expected":
		return "pending"
	default:
		return ""
	}
}

func normalizeCheckRunState(status, conclusion string) string {
	switch strings.ToLower(strings.TrimSpace(conclusion)) {
	case "success", "neutral", "skipped":
		return "success"
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
		return "failure"
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed":
		return "pending"
	case "queued", "in_progress", "requested", "waiting", "pending":
		return "pending"
	}
	return "pending"
}

func githubIssueCommentsForWorld(comments []githubIssueComment) []map[string]any {
	out := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		url := c.HTMLURL
		if url == "" {
			url = c.URL
		}
		out = append(out, map[string]any{
			"id":     c.ID,
			"body":   c.Body,
			"author": c.User.Login,
			"url":    url,
		})
	}
	return out
}

func githubPRChecksText(state string, checks []map[string]any) (string, string) {
	normalized := strings.TrimSpace(state)
	if normalized == "" {
		normalized = "unknown"
	}
	if len(checks) == 0 {
		return "state: " + normalized + "\nNo failed checks reported by GitHub.", ""
	}
	var summary strings.Builder
	fmt.Fprintf(&summary, "state: %s\nfailed checks: %d\n", normalized, len(checks))
	var failed strings.Builder
	for _, check := range checks {
		name := strings.TrimSpace(fmt.Sprint(check["name"]))
		if name == "" {
			name = "(unnamed check)"
		}
		status := strings.TrimSpace(fmt.Sprint(check["state"]))
		if status == "" {
			status = strings.TrimSpace(fmt.Sprint(check["conclusion"]))
		}
		if status == "" {
			status = "failure"
		}
		url := strings.TrimSpace(fmt.Sprint(check["url"]))
		line := "- " + name + ": " + status
		if url != "" {
			line += " (" + url + ")"
		}
		summary.WriteString(line + "\n")
		failed.WriteString(line + "\n")
	}
	return strings.TrimRight(summary.String(), "\n"), strings.TrimRight(failed.String(), "\n")
}

func optBool(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true", "1", "yes", "y":
			return true
		case "false", "0", "no", "n":
			return false
		}
	}
	return def
}

func ghPRRebase(ctx context.Context, args map[string]any) (Result, error) {
	repo, _ := args["repo"].(string)
	prID, _ := args["pr_id"].(string)
	if strings.TrimSpace(repo) == "" || strings.TrimSpace(prID) == "" {
		return Result{Error: "git.pr_rebase: repo and pr_id are required"}, nil
	}
	token := cliGitHubToken(ctx)
	if token == "" {
		return Result{Error: "git.pr_rebase: GH_TOKEN is required for authenticated fetch/push"}, nil
	}

	meta, errMsg := ghPRRebaseMetadata(ctx, repo, prID)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}
	baseRef := strings.TrimSpace(meta.BaseRefName)
	if baseRef == "" {
		baseRef = "main"
	}
	headRef := strings.TrimSpace(meta.HeadRefName)
	if headRef == "" {
		return Result{Error: "git.pr_rebase: PR headRefName is empty"}, nil
	}
	headSHA := strings.TrimSpace(meta.HeadRefOID)
	headRepo := strings.TrimSpace(meta.HeadRepository.NameWithOwner)
	if headRepo == "" {
		headRepo = repo
	}

	tmp, err := os.MkdirTemp("", "kitsoki-pr-rebase-*")
	if err != nil {
		return Result{Error: fmt.Sprintf("git.pr_rebase: tempdir: %v", err)}, nil
	}
	defer os.RemoveAll(tmp)

	run := func(dir string, args ...string) (string, string, int, error) {
		gitArgs := append([]string{
			"-c", "http.extraheader=AUTHORIZATION: basic " + basicGitHubToken(token),
			"-c", "user.name=kitsoki-gh-agent",
			"-c", "user.email=kitsoki-gh-agent@users.noreply.github.com",
		}, args...)
		return cliExec(ctx, dir, "git", gitArgs...)
	}
	if _, stderr, code, err := run(tmp, "init"); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: git init: %s", cliErr(stderr, err, code))}, nil
	}
	if _, stderr, code, err := run(tmp, "remote", "add", "origin", "https://github.com/"+repo+".git"); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: add origin: %s", cliErr(stderr, err, code))}, nil
	}
	if _, stderr, code, err := run(tmp, "remote", "add", "head", "https://github.com/"+headRepo+".git"); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: add head: %s", cliErr(stderr, err, code))}, nil
	}
	baseRemoteRef := "refs/remotes/origin/" + baseRef
	if _, stderr, code, err := run(tmp, "fetch", "--no-tags", "origin", baseRef+":"+baseRemoteRef); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: fetch base: %s", cliErr(stderr, err, code))}, nil
	}
	if _, stderr, code, err := run(tmp, "fetch", "--no-tags", "origin", "refs/pull/"+prID+"/head:pr-"+prID); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: fetch PR: %s", cliErr(stderr, err, code))}, nil
	}
	if _, stderr, code, err := run(tmp, "checkout", "pr-"+prID); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: checkout PR: %s", cliErr(stderr, err, code))}, nil
	}

	resolved, errMsg := runRebaseWithDeterministicResolvers(ctx, tmp, run, baseRemoteRef)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}
	repairs, errMsg := repairKnownPRPostRebase(tmp, run)
	if errMsg != "" {
		return Result{Error: errMsg}, nil
	}
	resolved = append(resolved, repairs...)
	pushArgs := []string{"push", "--force-with-lease", "head", "HEAD:" + headRef}
	if headSHA != "" {
		pushArgs = []string{"push", "--force-with-lease=" + headRef + ":" + headSHA, "head", "HEAD:" + headRef}
	}
	if _, stderr, code, err := run(tmp, pushArgs...); err != nil || code != 0 {
		return Result{Error: fmt.Sprintf("git.pr_rebase: push: %s", cliErr(stderr, err, code))}, nil
	}

	newHeadSHA, _, _, _ := run(tmp, "rev-parse", "HEAD")
	summary := fmt.Sprintf("Rebased PR #%s onto `%s` and pushed `%s`.", prID, baseRef, headRef)
	if len(resolved) > 0 {
		summary += "\n\nResolved conflicts:\n- " + strings.Join(resolved, "\n- ")
	}
	return Result{Data: map[string]any{
		"ok":       true,
		"sha":      strings.TrimSpace(newHeadSHA),
		"summary":  summary,
		"resolved": resolved,
	}}, nil
}

type ghPRRebaseView struct {
	HeadRefName    string `json:"headRefName"`
	HeadRefOID     string `json:"headRefOid"`
	BaseRefName    string `json:"baseRefName"`
	HeadRepository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"headRepository"`
}

func ghPRRebaseMetadata(ctx context.Context, repo, prID string) (ghPRRebaseView, string) {
	var meta ghPRRebaseView
	var raw githubPullRequestRebaseMetadata
	code, resp, err := githubAPIJSON(ctx, "GET", fmt.Sprintf("repos/%s/pulls/%s", repo, prID), nil, &raw)
	if err != nil {
		return meta, fmt.Sprintf("git.pr_rebase: PR metadata: %v", err)
	}
	if code >= 300 {
		return meta, fmt.Sprintf("git.pr_rebase: PR metadata: %s", githubAPIError(resp))
	}
	meta.HeadRefName = raw.Head.Ref
	meta.HeadRefOID = raw.Head.SHA
	meta.BaseRefName = raw.Base.Ref
	meta.HeadRepository.NameWithOwner = raw.Head.Repo.FullName
	return meta, ""
}

type githubPullRequestRebaseMetadata struct {
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func runRebaseWithDeterministicResolvers(ctx context.Context, workdir string, run func(string, ...string) (string, string, int, error), baseRef string) ([]string, string) {
	_, stderr, code, err := run(workdir, "rebase", baseRef)
	if err == nil && code == 0 {
		return nil, ""
	}
	var allResolved []string
	for i := 0; i < 8; i++ {
		conflicted, conflictedErr := unresolvedPaths(ctx, workdir)
		if conflictedErr != "" {
			return nil, conflictedErr
		}
		if len(conflicted) == 0 {
			return nil, fmt.Sprintf("git.pr_rebase: rebase failed: %s", cliErr(stderr, err, code))
		}
		resolved, ok, resolveErr := resolveKnownPRConflict(workdir, conflicted)
		if resolveErr != nil {
			return nil, fmt.Sprintf("git.pr_rebase: resolve conflict: %v", resolveErr)
		}
		if !ok {
			return nil, "git.pr_rebase: unresolved conflicts require operator attention: " + strings.Join(conflicted, ", ")
		}
		allResolved = append(allResolved, resolved...)
		addArgs := append([]string{"add", "--"}, conflicted...)
		if _, addStderr, addCode, addErr := run(workdir, addArgs...); addErr != nil || addCode != 0 {
			return nil, fmt.Sprintf("git.pr_rebase: add resolved files: %s", cliErr(addStderr, addErr, addCode))
		}
		if _, contStderr, contCode, contErr := run(workdir, "-c", "core.editor=true", "rebase", "--continue"); contErr != nil || contCode != 0 {
			stillConflicted, conflictedErr := unresolvedPaths(ctx, workdir)
			if conflictedErr != "" {
				return nil, conflictedErr
			}
			if len(stillConflicted) == 0 {
				return nil, fmt.Sprintf("git.pr_rebase: rebase continue: %s", cliErr(contStderr, contErr, contCode))
			}
			continue
		}
		return allResolved, ""
	}
	return nil, "git.pr_rebase: too many conflict-resolution rounds"
}

func unresolvedPaths(ctx context.Context, workdir string) ([]string, string) {
	stdout, stderr, code, err := cliExec(ctx, workdir, "git", "diff", "--name-only", "--diff-filter=U")
	if err != nil || code != 0 {
		return nil, fmt.Sprintf("git.pr_rebase: list conflicts: %s", cliErr(stderr, err, code))
	}
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out, ""
}

func resolveKnownPRConflict(workdir string, paths []string) ([]string, bool, error) {
	if len(paths) != 1 || paths[0] != "scripts/run-tests.sh" {
		return nil, false, nil
	}
	path := filepath.Join(workdir, paths[0])
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	resolved, ok := resolveRunTestsPythonSuiteConflict(string(raw))
	if !ok {
		return nil, false, nil
	}
	if err := os.WriteFile(path, []byte(resolved), 0o755); err != nil {
		return nil, false, err
	}
	return []string{"scripts/run-tests.sh: kept arena tests from base and dev-story script tests from PR"}, true, nil
}

func resolveRunTestsPythonSuiteConflict(content string) (string, bool) {
	out, changed, ok := resolveConflictBlocks(content, func(alts [][]string) ([]string, bool) {
		joined := joinAltLines(alts)
		switch {
		case strings.Contains(joined, "section \"python tool tests") &&
			strings.Contains(joined, "MINING_TESTS=(") &&
			strings.Contains(joined, "arena") &&
			strings.Contains(joined, "dev-story scripts"):
			return runTestsPythonSuiteStanza(), true
		case strings.Contains(joined, "section \"python tool tests") &&
			strings.Contains(joined, "arena") &&
			strings.Contains(joined, "dev-story scripts"):
			return []string{`section "python tool tests (session-mining + product-journey + arena + dev-story scripts)"`}, true
		case strings.Contains(joined, "MINING_TESTS=(") &&
			strings.Contains(joined, "tools/arena/tests/test_*.py") &&
			strings.Contains(joined, "stories/dev-story/scripts/*_test.py"):
			return []string{`	MINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py stories/dev-story/scripts/*_test.py)`}, true
		default:
			return nil, false
		}
	})
	return out, changed && ok
}

func repairKnownPRPostRebase(workdir string, run func(string, ...string) (string, string, int, error)) ([]string, string) {
	path := filepath.Join(workdir, "scripts/run-tests.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ""
		}
		return nil, fmt.Sprintf("git.pr_rebase: read post-rebase run-tests.sh: %v", err)
	}
	repaired, changed := repairRunTestsPythonSuiteStanza(string(raw))
	if !changed {
		return nil, ""
	}
	if err := os.WriteFile(path, []byte(repaired), 0o755); err != nil {
		return nil, fmt.Sprintf("git.pr_rebase: write post-rebase run-tests.sh: %v", err)
	}
	if _, stderr, code, err := run(workdir, "add", "--", "scripts/run-tests.sh"); err != nil || code != 0 {
		return nil, fmt.Sprintf("git.pr_rebase: add post-rebase repair: %s", cliErr(stderr, err, code))
	}
	if _, stderr, code, err := run(workdir, "commit", "--amend", "--no-edit"); err != nil || code != 0 {
		return nil, fmt.Sprintf("git.pr_rebase: amend post-rebase repair: %s", cliErr(stderr, err, code))
	}
	return []string{"scripts/run-tests.sh: repaired missing Python suite test list after rebase"}, ""
}

func repairRunTestsPythonSuiteStanza(content string) (string, bool) {
	if strings.Contains(content, "MINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py stories/dev-story/scripts/*_test.py)") {
		return content, false
	}
	lines := strings.SplitAfter(content, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, `section "python tool tests`) {
			start = i
			break
		}
	}
	if start < 0 {
		return content, false
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], `for t in "${MINING_TESTS[@]}"`) {
			end = i
			break
		}
	}
	if end < 0 {
		return content, false
	}
	var out []string
	out = append(out, lines[:start]...)
	for _, line := range runTestsPythonSuiteStanza() {
		out = append(out, line+"\n")
	}
	out = append(out, lines[end:]...)
	return strings.Join(out, ""), true
}

func runTestsPythonSuiteStanza() []string {
	return []string{
		`section "python tool tests (session-mining + product-journey + arena + dev-story scripts)"`,
		`if command -v python3 >/dev/null 2>&1; then`,
		`	shopt -s nullglob`,
		`	MINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py stories/dev-story/scripts/*_test.py)`,
		`	shopt -u nullglob`,
	}
}

func resolveConflictBlocks(content string, resolve func([][]string) ([]string, bool)) (string, bool, bool) {
	lines := strings.SplitAfter(content, "\n")
	var out []string
	changed := false
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "<<<<<<< ") {
			out = append(out, lines[i])
			continue
		}
		i++
		var ours, base, theirs []string
		for i < len(lines) && !strings.HasPrefix(lines[i], "||||||| ") && !strings.HasPrefix(lines[i], "=======") {
			ours = append(ours, strings.TrimSuffix(lines[i], "\n"))
			i++
		}
		if i < len(lines) && strings.HasPrefix(lines[i], "||||||| ") {
			i++
			for i < len(lines) && !strings.HasPrefix(lines[i], "=======") {
				base = append(base, strings.TrimSuffix(lines[i], "\n"))
				i++
			}
		}
		if i >= len(lines) || !strings.HasPrefix(lines[i], "=======") {
			return content, changed, false
		}
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], ">>>>>>> ") {
			theirs = append(theirs, strings.TrimSuffix(lines[i], "\n"))
			i++
		}
		if i >= len(lines) {
			return content, changed, false
		}
		replacement, ok := resolve([][]string{ours, base, theirs})
		if !ok {
			return content, changed, false
		}
		for _, line := range replacement {
			out = append(out, line+"\n")
		}
		changed = true
	}
	return strings.Join(out, ""), changed, true
}

func joinAltLines(alts [][]string) string {
	var b strings.Builder
	for _, alt := range alts {
		for _, line := range alt {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func cliGitHubToken(ctx context.Context) string {
	env := CLIExecEnvFromCtx(ctx)
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(env[key]); token != "" {
			return token
		}
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token
		}
	}
	return ""
}

func basicGitHubToken(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

func cliErr(stderr string, err error, code int) string {
	msg := strings.TrimSpace(stderr)
	if msg != "" {
		return msg
	}
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("git exited with code %d", code)
}

// prIDFromURL extracts the trailing `/pull/<N>` segment.  Returns "" on
// any URL that doesn't match — the caller's domain error rendering then
// surfaces the empty pr_id explicitly.
func prIDFromURL(url string) string {
	url = strings.TrimSpace(url)
	idx := strings.LastIndex(url, "/pull/")
	if idx < 0 {
		return ""
	}
	return url[idx+len("/pull/"):]
}
