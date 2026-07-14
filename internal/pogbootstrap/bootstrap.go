package pogbootstrap

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/kit"
)

type Options struct {
	RepoPath       string
	BriefPath      string
	Custody        string
	Remote         string
	DryRun         bool
	Yes            bool
	TracePath      string
	Commit         bool
	KitsokiCommand string
}

type Result struct {
	RepoPath     string
	TracePath    string
	ChangedFiles []string
	CommitID     string
	Verification []Verification
	Plan         []Action
}

type Action struct {
	Room      string `json:"room"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Detail    string `json:"detail,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type Verification struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Output  string `json:"output,omitempty"`
}

type briefModel struct {
	Product      string   `json:"product"`
	Slug         string   `json:"slug"`
	Summary      string   `json:"summary"`
	Requirements []string `json:"requirements"`
	UseCases     []string `json:"use_cases"`
	Constraints  []string `json:"constraints"`
	Unknowns     []string `json:"unknowns"`
	Custody      string   `json:"custody"`
	Remote       string   `json:"remote"`
}

type traceEvent struct {
	Time  string `json:"time"`
	Room  string `json:"room"`
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

var bulletRE = regexp.MustCompile(`^\s*(?:[-*]|\d+[.)])\s+`)

//go:embed kit/kit.yaml kit/stories/init/app.yaml
var kitFS embed.FS

func Run(opts Options, stdout io.Writer) (Result, error) {
	return NewStory(opts, stdout).Run()
}

type Story struct {
	opts   Options
	stdout io.Writer
}

func NewStory(opts Options, stdout io.Writer) *Story {
	if stdout == nil {
		stdout = io.Discard
	}
	return &Story{opts: opts, stdout: stdout}
}

func validateEmbeddedKitStory() error {
	tmp, err := os.MkdirTemp("", "kitsoki-pog-kit-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, rel := range []string{"kit.yaml", filepath.Join("stories", "init", "app.yaml")} {
		raw, err := kitFS.ReadFile(filepath.ToSlash(filepath.Join("kit", rel)))
		if err != nil {
			return fmt.Errorf("read embedded POG kit %s: %w", rel, err)
		}
		out := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			return err
		}
	}
	manifest, err := kit.LoadDir(tmp)
	if err != nil {
		return fmt.Errorf("load embedded POG kit: %w", err)
	}
	if manifest.Identity() != "@kitsoki/pog" || !manifest.HasStory("init") {
		return fmt.Errorf("embedded POG kit manifest mismatch: identity=%q stories=%v", manifest.Identity(), manifest.Provides.Stories)
	}
	return nil
}

func (s *Story) Run() (Result, error) {
	opts := s.opts
	stdout := s.stdout
	if opts.RepoPath == "" {
		return Result{}, errors.New("repo path is required")
	}
	if opts.BriefPath == "" {
		return Result{}, errors.New("--brief is required")
	}
	if opts.Custody == "" {
		opts.Custody = "personal-private"
	}
	if opts.Remote == "" {
		opts.Remote = "none"
	}
	if opts.Remote != "none" && !strings.HasPrefix(opts.Remote, "github:") && !strings.HasPrefix(opts.Remote, "git:") && !strings.HasPrefix(opts.Remote, "adapter:") {
		return Result{}, fmt.Errorf("--remote must be none, github:owner/name, git:<url>, or adapter:<name>; got %q", opts.Remote)
	}
	if opts.KitsokiCommand == "" {
		opts.KitsokiCommand = "kitsoki"
	}
	if err := validateEmbeddedKitStory(); err != nil {
		return Result{}, err
	}
	repoAbs, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return Result{}, err
	}
	briefAbs, err := filepath.Abs(opts.BriefPath)
	if err != nil {
		return Result{}, err
	}

	tracePath := opts.TracePath
	if tracePath == "" {
		tracePath = filepath.Join(repoAbs, ".artifacts", "pog-init-trace.jsonl")
	}
	trace := &traceWriter{path: tracePath}
	if opts.DryRun {
		trace = &traceWriter{out: io.Discard}
	} else {
		if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
			return Result{}, err
		}
		f, err := os.Create(tracePath)
		if err != nil {
			return Result{}, err
		}
		defer f.Close()
		trace.out = f
	}

	model, err := parseBrief(briefAbs, opts.Custody, opts.Remote)
	if err != nil {
		return Result{}, err
	}
	trace.emit("brief.intake", "model", model)

	plan := buildPlan(repoAbs, briefAbs, model, opts)
	trace.emit("repo.plan", "actions", plan)
	printPlan(stdout, plan, opts.DryRun)

	decisions := decisionNodes(model)
	trace.emit("operator.consent", "decisions", decisions)
	if opts.DryRun {
		return Result{RepoPath: repoAbs, TracePath: tracePath, Plan: plan}, nil
	}
	if opts.Remote != "none" {
		return Result{}, fmt.Errorf("remote mode %q requires an interactive operator consent implementation; rerun with --remote none for local bootstrap", opts.Remote)
	}
	if !opts.Yes {
		return Result{}, errors.New("refusing to mutate without --yes; rerun with --dry-run to preview or --yes to apply local file/git actions")
	}

	changed, commitID, err := apply(repoAbs, model, opts, decisions)
	if err != nil {
		trace.emit("repo.apply", "error", map[string]string{"error": err.Error()})
		return Result{}, err
	}
	sort.Strings(changed)
	trace.emit("repo.apply", "applied", map[string]any{"changed_files": changed, "commit_id": commitID})

	verification := verify(repoAbs, opts.KitsokiCommand)
	trace.emit("repo.verify", "verification", verification)
	for _, v := range verification {
		if !v.OK {
			return Result{}, fmt.Errorf("%s failed: %s", v.Name, strings.TrimSpace(v.Output))
		}
	}
	trace.emit("handoff", "next_action", map[string]string{
		"trace": tracePath,
		"next":  "Open docs/onboarding.md and decompose the first requirement into change nodes.",
	})
	fmt.Fprintf(stdout, "pog init: initialized %s\ntrace: %s\nnext: open docs/onboarding.md and decompose the first requirement into change nodes\n", repoAbs, tracePath)
	return Result{RepoPath: repoAbs, TracePath: tracePath, ChangedFiles: changed, CommitID: commitID, Verification: verification, Plan: plan}, nil
}

func parseBrief(path, custody, remote string) (briefModel, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return briefModel{}, fmt.Errorf("read brief: %w", err)
	}
	lines := strings.Split(string(raw), "\n")
	model := briefModel{Custody: custody, Remote: remote}
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if model.Product == "" {
				model.Product = heading
			}
			section = strings.ToLower(heading)
			continue
		}
		if model.Summary == "" && !bulletRE.MatchString(trimmed) {
			model.Summary = trimmed
		}
		item := strings.TrimSpace(bulletRE.ReplaceAllString(trimmed, ""))
		lowerSection := strings.ToLower(section)
		switch {
		case strings.Contains(lowerSection, "require"):
			model.Requirements = append(model.Requirements, item)
		case strings.Contains(lowerSection, "use case") || strings.Contains(lowerSection, "user") || strings.Contains(lowerSection, "workflow"):
			model.UseCases = append(model.UseCases, item)
		case strings.Contains(lowerSection, "constraint") || strings.Contains(lowerSection, "non-goal"):
			model.Constraints = append(model.Constraints, item)
		case strings.Contains(lowerSection, "unknown") || strings.Contains(lowerSection, "question") || strings.Contains(item, "?"):
			model.Unknowns = append(model.Unknowns, item)
		}
		if strings.Contains(item, "?") && !contains(model.Unknowns, item) {
			model.Unknowns = append(model.Unknowns, item)
		}
	}
	if model.Product == "" {
		model.Product = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	model.Slug = slug(model.Product)
	if model.Summary == "" {
		model.Summary = "Product initialized from brief."
	}
	if len(model.Requirements) == 0 {
		model.Requirements = []string{"Turn the brief into a lint-clean POG-tracked product repository."}
	}
	if len(model.UseCases) == 0 {
		model.UseCases = []string{"An operator initializes the product repo from a short brief and gets a green local gate."}
	}
	return model, nil
}

func buildPlan(repoAbs, briefAbs string, model briefModel, opts Options) []Action {
	actions := []Action{
		{"repo.apply", "git", repoAbs, "git init -b main", false},
		{"repo.apply", "mkdir", ".context/ .artifacts/ .worktrees/ docs/ pog/ scripts/", "", false},
		{"repo.apply", "write", ".gitignore", "private-by-default folders", false},
		{"repo.apply", "write", "AGENTS.md", "POG conventions block", false},
		{"repo.apply", "write", "pog/catalog.yaml", "seed catalog from " + briefAbs, false},
		{"repo.apply", "write", "docs/onboarding.md", "brief-derived onboarding plan", false},
		{"repo.apply", "write", "scripts/checks.sh scripts/pog-doctor scripts/merge-to-main.sh scripts/land-branch.sh scripts/sync-main-from-remote.sh", "local deterministic gates and protected-main landing helpers", false},
		{"repo.apply", "hook", ".git/hooks/reference-transaction", "protected main hook", false},
		{"repo.verify", "check", "scripts/pog-doctor .", "", false},
		{"repo.verify", "check", opts.KitsokiCommand + " graph lint pog/catalog.yaml", "", false},
		{"repo.verify", "check", "scripts/checks.sh", "", false},
	}
	if opts.Commit {
		actions = append(actions, Action{"repo.apply", "git", "initial commit", "git add . && git commit", false})
	}
	if model.Remote != "none" {
		actions = append(actions, Action{"operator.consent", "remote", model.Remote, "requires explicit consent before any create/push/wiring", true})
	} else {
		actions = append(actions, Action{"operator.consent", "remote", "none", "no remote will be created or pushed", false})
	}
	return actions
}

func printPlan(w io.Writer, plan []Action, dryRun bool) {
	if dryRun {
		fmt.Fprintln(w, "pog init dry-run plan:")
	} else {
		fmt.Fprintln(w, "pog init plan:")
	}
	for _, a := range plan {
		fmt.Fprintf(w, "- [%s] %s %s", a.Room, a.Kind, a.Target)
		if a.Detail != "" {
			fmt.Fprintf(w, " — %s", a.Detail)
		}
		if a.Sensitive {
			fmt.Fprint(w, " (consent required)")
		}
		fmt.Fprintln(w)
	}
}

func decisionNodes(model briefModel) []map[string]string {
	unknowns := append([]string{}, model.Unknowns...)
	for _, q := range []string{
		"Confirm license intent before publishing or wiring a remote.",
		"Confirm visibility model for public vs internal catalog nodes.",
		"Confirm backend or hosted-service requirements before private-backend wiring.",
	} {
		if !contains(unknowns, q) {
			unknowns = append(unknowns, q)
		}
	}
	nodes := make([]map[string]string, 0, len(unknowns))
	for i, q := range unknowns {
		nodes = append(nodes, map[string]string{
			"id":         fmt.Sprintf("decision-%s-%02d", model.Slug, i+1),
			"title":      q,
			"status":     "pending",
			"visibility": "internal",
		})
	}
	return nodes
}

func apply(repo string, model briefModel, opts Options, decisions []map[string]string) ([]string, string, error) {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); errors.Is(err, os.ErrNotExist) {
		if out, err := run(repo, "git", "init", "-b", "main"); err != nil {
			return nil, "", fmt.Errorf("git init: %w: %s", err, out)
		}
	}
	for _, dir := range []string{".context", ".artifacts", ".worktrees", "docs", "pog", "scripts"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			return nil, "", err
		}
	}
	files := map[string]string{
		".gitignore":                       privateGitignore(),
		"AGENTS.md":                        agents(model),
		"pog/catalog.yaml":                 catalog(model, decisions),
		"docs/onboarding.md":               onboarding(model),
		"scripts/checks.sh":                checksScript(opts.KitsokiCommand),
		"scripts/pog-doctor":               doctorScript(),
		"scripts/merge-to-main.sh":         mergeToMainScript(),
		"scripts/land-branch.sh":           landBranchScript(),
		"scripts/sync-main-from-remote.sh": syncMainScript(),
	}
	var changed []string
	for rel, body := range files {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, "", err
		}
		existing, _ := os.ReadFile(path)
		if string(existing) != body {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return nil, "", err
			}
			changed = append(changed, rel)
		}
	}
	for _, rel := range []string{"scripts/checks.sh", "scripts/pog-doctor", "scripts/merge-to-main.sh", "scripts/land-branch.sh", "scripts/sync-main-from-remote.sh"} {
		if err := os.Chmod(filepath.Join(repo, rel), 0o755); err != nil {
			return nil, "", err
		}
	}
	hookPath := filepath.Join(repo, ".git", "hooks", "reference-transaction")
	if err := os.WriteFile(hookPath, []byte(referenceTransactionHook()), 0o755); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(hookPath, 0o755); err != nil {
		return nil, "", err
	}
	changed = append(changed, ".git/hooks/reference-transaction")
	if out, err := run(repo, "git", "config", "pog.readOnlyMain", "true"); err != nil {
		return nil, "", fmt.Errorf("git config: %w: %s", err, out)
	}
	if opts.Commit {
		if out, err := run(repo, "git", "add", "."); err != nil {
			return nil, "", fmt.Errorf("git add: %w: %s", err, out)
		}
		if out, err := run(repo, "git", "-c", "user.name=Kitsoki POG Init", "-c", "user.email=pog-init@kitsoki.local", "commit", "-m", "Initialize POG repo from brief"); err != nil {
			return nil, "", fmt.Errorf("git commit: %w: %s", err, out)
		}
		id, err := run(repo, "git", "rev-parse", "--short", "HEAD")
		if err != nil {
			return nil, "", err
		}
		return changed, strings.TrimSpace(id), nil
	}
	return changed, "", nil
}

func verify(repo, kitsokiCommand string) []Verification {
	checks := []Verification{
		{Name: "pog-doctor", Command: "scripts/pog-doctor ."},
		{Name: "graph-lint", Command: kitsokiCommand + " graph lint pog/catalog.yaml"},
		{Name: "checks", Command: "scripts/checks.sh"},
	}
	for i := range checks {
		parts := strings.Fields(checks[i].Command)
		out, err := run(repo, parts[0], parts[1:]...)
		checks[i].Output = strings.TrimSpace(out)
		checks[i].OK = err == nil
	}
	return checks
}

func run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

type traceWriter struct {
	path string
	out  io.Writer
}

func (t *traceWriter) emit(room, event string, data any) {
	if t.out == nil {
		return
	}
	_ = json.NewEncoder(t.out).Encode(traceEvent{
		Time:  time.Now().UTC().Format(time.RFC3339),
		Room:  room,
		Event: event,
		Data:  data,
	})
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "product"
	}
	if out[0] < 'a' || out[0] > 'z' {
		return "product-" + out
	}
	return out
}

func nodeSlug(prefix string, text string, idx int) string {
	base := slug(text)
	words := strings.Split(base, "-")
	if len(words) > 5 {
		base = strings.Join(words[:5], "-")
	}
	return fmt.Sprintf("%s-%02d-%s", prefix, idx+1, base)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func privateGitignore() string {
	return ".context/\n.artifacts/\n.worktrees/\n"
}

func agents(model briefModel) string {
	return fmt.Sprintf(`# Agent conventions for %s

This repo was initialized by `+"`kitsoki pog init`"+`. It is a POG-tracked
product repo, with plans, catalog, local gates, and generated evidence stored
in git.

<!-- pack:conventions:begin — managed by the pog conventions pack installer; edits inside this block are overwritten on reinstall -->
## Repo conventions (POG product repo)

- **Protected main.** The primary checkout stays on `+"`main`"+` at its tip. All
  implementation work happens in branch worktrees:
  `+"`git worktree add .worktrees/<name> -b <branch> main`"+`.
- **Landing path.** Land branches through the repo's scripts after
  `+"`scripts/checks.sh`"+` passes. No direct-to-main commits for implementation
  work.
- **Private-by-default folders** (all gitignored, never committed):
  `+"`.context/`"+`, `+"`.artifacts/`"+`, `+"`.worktrees/`"+`.
- **Validation:** `+"`scripts/checks.sh`"+` must exit 0 before landing.
- **Testing rule:** no live LLM calls in tests or CI — cassettes, flows, and
  mocks only.
- **Doctor:** `+"`scripts/pog-doctor`"+` lints these conventions
  deterministically.
<!-- pack:conventions:end -->
`, model.Product)
}

func catalog(model briefModel, decisions []map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema: project-object-graph/seed-catalog/v0\n")
	fmt.Fprintf(&b, "catalog:\n  id: %s-catalog\n  title: %q\n  status: active\n  visibility: internal\n", model.Slug, model.Product+" catalog")
	fmt.Fprintf(&b, "type_registry:\n")
	for _, t := range []string{"document", "product", "requirement", "use-case", "decision"} {
		fmt.Fprintf(&b, "  - id: %s\n    schema: pog/%s/v0\n    extends: null\n    summary: %s node.\n", t, t, t)
	}
	fmt.Fprintf(&b, "nodes:\n")
	fmt.Fprintf(&b, "  - schema: pog/document/v0\n    id: source-brief\n    title: Source brief\n    status: active\n    visibility: internal\n    sources: []\n    path: docs/onboarding.md\n")
	fmt.Fprintf(&b, "  - schema: pog/product/v0\n    id: product-%s\n    title: %q\n    status: active\n    visibility: internal\n    sources: [source-brief]\n    summary: %q\n    custody: %q\n    remote: %q\n", model.Slug, model.Product, model.Summary, model.Custody, model.Remote)
	for i, req := range model.Requirements {
		fmt.Fprintf(&b, "  - schema: pog/requirement/v0\n    id: %s\n    title: %q\n    status: planned\n    visibility: internal\n    sources: [source-brief]\n    statement: %q\n", nodeSlug("req", req, i), req, req)
	}
	for i, uc := range model.UseCases {
		fmt.Fprintf(&b, "  - schema: pog/use-case/v0\n    id: %s\n    title: %q\n    status: planned\n    visibility: internal\n    sources: [source-brief]\n    narrative: %q\n", nodeSlug("use-case", uc, i), uc, uc)
	}
	for _, d := range decisions {
		fmt.Fprintf(&b, "  - schema: pog/decision/v0\n    id: %s\n    title: %q\n    status: pending\n    visibility: internal\n    sources: [source-brief]\n", d["id"], d["title"])
	}
	return b.String()
}

func onboarding(model briefModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s onboarding\n\n", model.Product)
	fmt.Fprintf(&b, "**Status:** initialized from brief; gate is `scripts/checks.sh`.\n\n")
	fmt.Fprintf(&b, "## Summary\n\n%s\n\n", model.Summary)
	fmt.Fprintf(&b, "## Requirements\n\n")
	for _, req := range model.Requirements {
		fmt.Fprintf(&b, "- %s\n", req)
	}
	fmt.Fprintf(&b, "\n## Use cases\n\n")
	for _, uc := range model.UseCases {
		fmt.Fprintf(&b, "- %s\n", uc)
	}
	if len(model.Constraints) > 0 {
		fmt.Fprintf(&b, "\n## Constraints\n\n")
		for _, c := range model.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	fmt.Fprintf(&b, "\n## Decision queue\n\n")
	for _, q := range append(model.Unknowns, "Confirm license intent before publishing or wiring a remote.", "Confirm visibility model for public vs internal catalog nodes.", "Confirm backend or hosted-service requirements before private-backend wiring.") {
		fmt.Fprintf(&b, "- [ ] %s\n", q)
	}
	return b.String()
}

func checksScript(kitsokiCommand string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
scripts/pog-doctor .
%s graph lint pog/catalog.yaml
echo "checks: pog-doctor green; catalog lint green"
`, kitsokiCommand)
}

func doctorScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
repo="${1:-.}"
cd "$repo"
git rev-parse --is-inside-work-tree >/dev/null
test "$(git branch --show-current)" = "main"
grep -q 'pack:conventions:begin' AGENTS.md
grep -q '^\.context/$' .gitignore
grep -q '^\.artifacts/$' .gitignore
grep -q '^\.worktrees/$' .gitignore
test -x scripts/checks.sh
test -x scripts/pog-doctor
test -x scripts/merge-to-main.sh
test -x scripts/land-branch.sh
test -x scripts/sync-main-from-remote.sh
test -f pog/catalog.yaml
test -x "$(git rev-parse --git-path hooks)/reference-transaction"
echo "pog-doctor: green"
`
}

func mergeToMainScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
branch="${1:?usage: scripts/merge-to-main.sh <branch>}"
if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  echo "error: run this in the primary checkout, not a linked worktree" >&2
  exit 1
fi
if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)" != "main" ]; then
  echo "error: the primary checkout is not on main" >&2
  exit 1
fi
if ! git rev-parse --verify --quiet "$branch" >/dev/null; then
  echo "error: no such branch: $branch" >&2
  exit 1
fi
if ! git merge-base --is-ancestor HEAD "$branch"; then
  echo "error: $branch is not a fast-forward of main; rebase it first or use scripts/land-branch.sh" >&2
  exit 1
fi
git merge --ff-only "$branch"
echo "main -> $(git rev-parse --short HEAD)"
`
}

func landBranchScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
branch="${1:?usage: scripts/land-branch.sh <branch> [--gate '<cmd>']}"
gate=""
shift || true
while [ "$#" -gt 0 ]; do
  case "$1" in
    --gate) gate="${2:?--gate requires a value}"; shift 2 ;;
    *) echo "error: unexpected argument: $1" >&2; exit 1 ;;
  esac
done
if git merge-base --is-ancestor HEAD "$branch"; then
  if [ -n "$gate" ]; then sh -c "$gate"; fi
  scripts/merge-to-main.sh "$branch"
  exit 0
fi
repo_root="$(git rev-parse --show-toplevel)"
safe_branch="$(basename "$branch")"
landing_branch="$safe_branch-land"
landing_worktree="$repo_root/.worktrees/$landing_branch"
cleanup() {
  git worktree remove "$landing_worktree" --force >/dev/null 2>&1 || true
  git branch -D "$landing_branch" >/dev/null 2>&1 || true
}
trap cleanup EXIT
git worktree add -b "$landing_branch" "$landing_worktree" main >/dev/null
commits="$(git rev-list --reverse main.."$branch")"
[ -n "$commits" ] || { echo "nothing to land"; exit 0; }
git -C "$landing_worktree" cherry-pick $commits
if [ -n "$gate" ]; then (cd "$landing_worktree" && sh -c "$gate"); fi
scripts/merge-to-main.sh "$landing_branch"
echo "path: cherry-pick-and-land ($branch -> $landing_branch)"
`
}

func syncMainScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
remote="${1:-origin}"
if [ "$(git rev-parse --git-dir)" != "$(git rev-parse --git-common-dir)" ]; then
  echo "error: run this in the primary checkout, not a linked worktree" >&2
  exit 1
fi
if [ "$(git symbolic-ref --quiet --short HEAD 2>/dev/null || true)" != "main" ]; then
  echo "error: the primary checkout is not on main" >&2
  exit 1
fi
git fetch "$remote" main
git merge --ff-only "$remote/main"
echo "main -> $(git rev-parse --short HEAD)"
`
}

func referenceTransactionHook() string {
	return `#!/usr/bin/env bash
set -euo pipefail
while read -r old new ref; do
  case "$ref" in
    refs/heads/main)
      case "$new" in
        0000000000000000000000000000000000000000) exit 1 ;;
      esac
      ;;
  esac
done
`
}

func TraceRooms(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	seen := map[string]bool{}
	var rooms []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev traceEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			return nil, err
		}
		if !seen[ev.Room] {
			seen[ev.Room] = true
			rooms = append(rooms, ev.Room)
		}
	}
	return rooms, scanner.Err()
}
