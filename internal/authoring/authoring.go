// Package authoring implements the LLM-driven authoring flow used by
// the TUI's "Edit mode". Given a free-text proposal and a path to an
// app.yaml, Propose runs `claude -p` (with full Read/Edit/Write tool
// access) inside a shadow copy of the story directory. Claude is free
// to touch anything — YAML, prompt templates, scripts. After it
// returns, we walk the shadow tree against the real one, build a
// per-file unified diff, and return a Proposal for the caller to show
// the user.
//
// Apply commits the shadow's changes by copying each modified file
// back into the real app dir; Discard throws the shadow away. The
// orchestrator's hot-reload (handled by the caller) refreshes the
// engine after Apply succeeds.
package authoring

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

//go:embed prompt.md
var promptTemplate string

// FileChange describes one file modified during a Propose run.
type FileChange struct {
	RelPath string // path relative to AppDir / ShadowDir
	Kind    string // "added", "modified", "removed"
	Before  []byte // empty for added
	After   []byte // empty for removed
}

// Context carries the runtime context that helps Claude pin a
// proposal to the right file. State is the dotted state path the
// player is currently in (e.g. "main"); View is the rendered view
// they're staring at when they invoke Edit mode. Both are optional —
// Propose accepts a nil Context when there is no live session
// (e.g. CLI use).
type Context struct {
	State string
	View  string
}

// Proposal bundles a Claude-authored edit ready for human review.
// The actual files have NOT been written to AppDir yet — call Apply
// to commit, or Discard to throw the shadow away.
type Proposal struct {
	AppDir      string       // absolute path to the real story directory
	AppPath     string       // absolute path to the manifest YAML inside AppDir
	ShadowDir   string       // absolute path to the temp shadow copy
	Summary     string       // SUMMARY: line from Claude's reply
	Changes     []FileChange // sorted by RelPath
	UnifiedDiff string       // git-diff-style combined unified diff
}

// ErrClaudeUnavailable signals that no claude binary could be located.
var ErrClaudeUnavailable = errors.New("claude binary not found on PATH; edit mode requires Claude Code")

// ErrClaudeRefused signals Claude returned an ERROR: line instead of
// making edits. Reason carries the explanation Claude provided.
type ErrClaudeRefused struct{ Reason string }

func (e *ErrClaudeRefused) Error() string {
	return "claude refused: " + e.Reason
}

// ErrNoChanges is returned when Claude's run completed cleanly but
// produced no file diffs. Almost always means the proposal didn't
// land — refine and retry.
var ErrNoChanges = errors.New("authoring: claude returned no file changes; refine your proposal")

// Propose snapshots the story directory containing appPath, runs
// Claude inside the shadow with file-edit tools enabled, and returns
// a Proposal describing the diff. The real app directory is NOT
// modified — that happens in Apply.
//
// runCtx carries the player's current state and rendered view so
// Claude can pin the proposal to the right file when there are
// multiple plausible matches across the story. Pass nil when there
// is no live session.
func Propose(ctx context.Context, appPath, proposalText string, runCtx *Context) (*Proposal, error) {
	proposalText = strings.TrimSpace(proposalText)
	if proposalText == "" {
		return nil, errors.New("authoring: empty proposal")
	}

	absApp, err := filepath.Abs(appPath)
	if err != nil {
		return nil, fmt.Errorf("authoring: resolve app path: %w", err)
	}
	appDir := filepath.Dir(absApp)
	appName := filepath.Base(absApp)

	bin, err := resolveClaudeBin()
	if err != nil {
		return nil, err
	}

	shadowDir, err := os.MkdirTemp("", "kitsoki-edit-")
	if err != nil {
		return nil, fmt.Errorf("authoring: mkdir shadow: %w", err)
	}
	cleanupOnErr := shadowDir
	defer func() {
		if cleanupOnErr != "" {
			_ = os.RemoveAll(cleanupOnErr)
		}
	}()

	// Snapshot appDir into shadowDir. The `appDir + "/."` syntax tells
	// cp to copy the directory's *contents* into shadowDir rather than
	// nesting appDir under it. (filepath.Join would drop the trailing
	// "/.", giving us the wrong cp semantics — hence the manual concat.)
	cpCmd := exec.CommandContext(ctx, "cp", "-a", appDir+string(os.PathSeparator)+".", shadowDir)
	var cpStderr bytes.Buffer
	cpCmd.Stderr = &cpStderr
	if err := cpCmd.Run(); err != nil {
		return nil, fmt.Errorf("authoring: snapshot app dir: %w: %s",
			err, strings.TrimSpace(cpStderr.String()))
	}

	prompt := buildPrompt(promptTemplate, shadowDir, appName, proposalText, runCtx)

	cmd := exec.CommandContext(ctx, bin,
		"-p",
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
	)
	cmd.Dir = shadowDir
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("authoring: claude exec failed: %v: %s", err, msg)
	}

	summary, refusal := parseTextReply(stdout.String())
	if refusal != "" {
		return nil, &ErrClaudeRefused{Reason: refusal}
	}

	changes, err := diffDirs(appDir, shadowDir)
	if err != nil {
		return nil, fmt.Errorf("authoring: compute diffs: %w", err)
	}
	if len(changes) == 0 {
		return nil, ErrNoChanges
	}

	// Validate the YAML side: load the shadow app to ensure the edit
	// didn't break the manifest. Script/prompt edits aren't checked
	// here — they're not kitsoki's responsibility.
	if _, err := app.Load(filepath.Join(shadowDir, appName)); err != nil {
		return nil, fmt.Errorf("authoring: edit does not validate: %w", err)
	}

	p := &Proposal{
		AppDir:      appDir,
		AppPath:     absApp,
		ShadowDir:   shadowDir,
		Summary:     summary,
		Changes:     changes,
		UnifiedDiff: buildCombinedDiff(changes),
	}
	cleanupOnErr = "" // hand ownership of shadow dir to caller
	return p, nil
}

// Apply commits the shadow's changes back to AppDir and removes the
// shadow copy. After a successful Apply, the Proposal's ShadowDir is
// cleared.
func Apply(p *Proposal) error {
	if p == nil {
		return errors.New("authoring: Apply called with nil proposal")
	}
	for _, c := range p.Changes {
		dst := filepath.Join(p.AppDir, c.RelPath)
		src := filepath.Join(p.ShadowDir, c.RelPath)
		switch c.Kind {
		case "removed":
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("authoring: remove %s: %w", c.RelPath, err)
			}
		case "added", "modified":
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("authoring: mkdir %s parent: %w", c.RelPath, err)
			}
			body, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("authoring: read shadow %s: %w", c.RelPath, err)
			}
			if err := os.WriteFile(dst, body, 0o644); err != nil {
				return fmt.Errorf("authoring: write %s: %w", c.RelPath, err)
			}
		}
	}
	return Discard(p)
}

// Discard removes the shadow copy. Safe to call repeatedly. Use when
// the user rejects the proposal (or after a successful Apply).
func Discard(p *Proposal) error {
	if p == nil || p.ShadowDir == "" {
		return nil
	}
	err := os.RemoveAll(p.ShadowDir)
	p.ShadowDir = ""
	return err
}

func resolveClaudeBin() (string, error) {
	if env := os.Getenv(host.OracleBinEnv); env != "" {
		return env, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrClaudeUnavailable
	}
	return path, nil
}

// buildPrompt fills the embedded template with the shadow path, the
// manifest filename, the user's proposal, and (when available) the
// runtime context they were looking at. Plain string substitution so
// YAML/Markdown braces don't have to be escaped.
func buildPrompt(template, shadowDir, appName, proposal string, runCtx *Context) string {
	stateLine := "(no live session — best-effort match against the file tree)"
	view := "(none)"
	if runCtx != nil {
		if runCtx.State != "" {
			stateLine = runCtx.State
		}
		if strings.TrimSpace(runCtx.View) != "" {
			view = runCtx.View
		}
	}
	s := strings.ReplaceAll(template, "{{SHADOW_DIR}}", shadowDir)
	s = strings.ReplaceAll(s, "{{APP_FILE}}", appName)
	s = strings.ReplaceAll(s, "{{PROPOSAL}}", proposal)
	s = strings.ReplaceAll(s, "{{CURRENT_STATE}}", stateLine)
	s = strings.ReplaceAll(s, "{{CURRENT_VIEW}}", view)
	return s
}

// summaryRE captures the SUMMARY: line. We accept it anywhere in the
// reply but the prompt asks Claude to put it last.
var summaryRE = regexp.MustCompile(`(?im)^SUMMARY:\s*(.+?)\s*$`)

// errorRE captures an ERROR: prefix and everything after it. Used
// when Claude refuses; the multi-line tail carries the "right place
// to edit" hint the prompt asks for.
var errorRE = regexp.MustCompile(`(?ms)^ERROR:\s*(.+)`)

// parseTextReply pulls SUMMARY and ERROR out of Claude's reply.
// ERROR wins over SUMMARY (a refusal should never co-exist with a
// successful summary, but if it does, treat as refusal).
func parseTextReply(reply string) (summary, refusal string) {
	if m := errorRE.FindStringSubmatch(reply); m != nil {
		return "", strings.TrimSpace(m[1])
	}
	if m := summaryRE.FindStringSubmatch(reply); m != nil {
		return strings.TrimSpace(m[1]), ""
	}
	return strings.TrimSpace(reply), ""
}

// diffDirs compares two directory trees and returns FileChanges for
// every file that differs in content (or exists in only one side).
// Uses byte-level comparison; binaries that happen to differ are
// reported but their diffs render as gibberish — Claude shouldn't be
// editing binaries anyway.
func diffDirs(realDir, shadowDir string) ([]FileChange, error) {
	realFiles, err := walkFiles(realDir)
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", realDir, err)
	}
	shadowFiles, err := walkFiles(shadowDir)
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", shadowDir, err)
	}

	var changes []FileChange
	seen := make(map[string]bool, len(realFiles)+len(shadowFiles))
	for rel, before := range realFiles {
		seen[rel] = true
		after, ok := shadowFiles[rel]
		if !ok {
			changes = append(changes, FileChange{
				RelPath: rel, Kind: "removed", Before: before,
			})
			continue
		}
		if !bytes.Equal(before, after) {
			changes = append(changes, FileChange{
				RelPath: rel, Kind: "modified", Before: before, After: after,
			})
		}
	}
	for rel, after := range shadowFiles {
		if seen[rel] {
			continue
		}
		changes = append(changes, FileChange{
			RelPath: rel, Kind: "added", After: after,
		})
	}
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].RelPath < changes[j].RelPath
	})
	return changes, nil
}

// maxFileBytes caps per-file reads at 1 MiB. Files larger than this
// are silently ignored — the diff machinery is for source code, not
// binary blobs.
const maxFileBytes = 1 << 20

// walkFiles reads every regular file under root (skipping common
// junk dirs) and returns rel→bytes.
func walkFiles(root string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "__pycache__", "node_modules", ".venv":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxFileBytes {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildCombinedDiff renders a git-diff-style multi-file unified diff
// suitable for display inside a Markdown ```diff fence.
func buildCombinedDiff(changes []FileChange) string {
	var sb strings.Builder
	for _, c := range changes {
		switch c.Kind {
		case "added":
			fmt.Fprintf(&sb, "=== ADDED %s ===\n", c.RelPath)
		case "removed":
			fmt.Fprintf(&sb, "=== REMOVED %s ===\n", c.RelPath)
		default:
			fmt.Fprintf(&sb, "=== MODIFIED %s ===\n", c.RelPath)
		}
		diff := difflib.UnifiedDiff{
			A:        difflib.SplitLines(string(c.Before)),
			B:        difflib.SplitLines(string(c.After)),
			FromFile: "a/" + c.RelPath,
			ToFile:   "b/" + c.RelPath,
			Context:  3,
		}
		s, err := difflib.GetUnifiedDiffString(diff)
		if err != nil {
			continue
		}
		sb.WriteString(s)
		sb.WriteString("\n")
	}
	return sb.String()
}
