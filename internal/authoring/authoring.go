// Package authoring implements the LLM-driven app-editing flow used by
// the TUI's "Edit mode". Given a free-text proposal and a path to an
// app.yaml, Propose asks Claude to translate the prose into a minimal
// YAML edit, validates the result by running app.LoadBytes against the
// new bytes in memory, and computes a unified diff against the
// original. The caller (the TUI) shows the diff for review and calls
// Apply on user approval to write it to disk.
//
// This package never persists, never reloads — it only produces and
// applies file edits. The orchestrator is responsible for re-loading
// the app definition after Apply succeeds.
package authoring

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"hally/internal/app"
	"hally/internal/host"
)

//go:embed prompt.md
var promptTemplate string

// Proposal is one Claude-generated edit ready for human review.
type Proposal struct {
	AppPath      string // absolute or working-dir-relative path to app.yaml
	OriginalYAML []byte // file contents at the time Propose was called
	NewYAML      []byte // Claude's full updated file (validated via app.LoadBytes)
	Summary      string // SUMMARY line parsed from Claude's reply
	UnifiedDiff  string // unified diff string of OriginalYAML → NewYAML
}

// ErrClaudeUnavailable signals that no claude binary could be located.
// The TUI surfaces this as "install Claude Code to use edit mode".
var ErrClaudeUnavailable = errors.New("claude binary not found on PATH; edit mode requires Claude Code")

// ErrClaudeRefused signals that Claude returned an ERROR: line instead
// of a YAML fence — the proposal was ambiguous or schema-violating.
type ErrClaudeRefused struct{ Reason string }

func (e *ErrClaudeRefused) Error() string {
	return "claude refused: " + e.Reason
}

// Propose asks Claude to translate proposalText into a minimal YAML
// edit against the app at appPath. The returned Proposal's NewYAML is
// guaranteed to load via app.LoadBytes; Apply must be called separately
// to write it to disk.
func Propose(ctx context.Context, appPath, proposalText string) (*Proposal, error) {
	proposalText = strings.TrimSpace(proposalText)
	if proposalText == "" {
		return nil, errors.New("authoring: empty proposal")
	}
	original, err := os.ReadFile(appPath)
	if err != nil {
		return nil, fmt.Errorf("authoring: read app %q: %w", appPath, err)
	}

	bin, err := resolveClaudeBin()
	if err != nil {
		return nil, err
	}

	prompt := buildPrompt(promptTemplate, string(original), proposalText)

	cmd := exec.CommandContext(ctx, bin,
		"-p",
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
	)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if runErr := cmd.Run(); runErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("authoring: claude exec failed: %v: %s", runErr, msg)
	}

	summary, newYAML, parseErr := parseResponse(stdout.String())
	if parseErr != nil {
		return nil, fmt.Errorf("authoring: %w (raw output: %q)", parseErr, truncate(stdout.String(), 400))
	}

	if _, err := app.LoadBytes(newYAML); err != nil {
		return nil, fmt.Errorf("authoring: edit does not validate: %w", err)
	}

	diff, err := unifiedDiff(appPath, string(original), string(newYAML))
	if err != nil {
		return nil, fmt.Errorf("authoring: build diff: %w", err)
	}

	return &Proposal{
		AppPath:      appPath,
		OriginalYAML: original,
		NewYAML:      newYAML,
		Summary:      summary,
		UnifiedDiff:  diff,
	}, nil
}

// Apply writes p.NewYAML to p.AppPath. The caller is responsible for
// the snapshot/undo story; this package does not back the file up.
func Apply(p *Proposal) error {
	if p == nil {
		return errors.New("authoring: Apply called with nil proposal")
	}
	return os.WriteFile(p.AppPath, p.NewYAML, 0o644)
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

// buildPrompt fills the embedded template with the current app.yaml and
// the user's proposal. We use plain string substitution rather than
// text/template so YAML braces in the file don't have to be escaped.
func buildPrompt(template, currentYAML, proposal string) string {
	s := strings.ReplaceAll(template, "{{CURRENT_YAML}}", currentYAML)
	s = strings.ReplaceAll(s, "{{PROPOSAL}}", proposal)
	return s
}

// yamlFenceRE captures the first ```yaml ... ``` block in Claude's reply.
// The (?s) flag makes `.` match newlines.
var yamlFenceRE = regexp.MustCompile("(?s)```ya?ml\\s*\\n(.*?)```")
var summaryRE = regexp.MustCompile(`(?im)^SUMMARY:\s*(.+)$`)
var errorPrefixRE = regexp.MustCompile(`(?ms)^ERROR:\s*(.+)$`)

// parseResponse extracts the SUMMARY and the new YAML body from
// Claude's reply, or returns ErrClaudeRefused if Claude emitted an
// ERROR: line instead.
//
// For ERROR replies we capture **everything from the ERROR: prefix
// onwards** (multi-line). The prompt instructs Claude to follow up
// the one-sentence root cause with a hint at where the edit likely
// belongs — that follow-up is the most actionable part for the user,
// so we must not throw it away.
func parseResponse(reply string) (summary string, newYAML []byte, err error) {
	hasYAML := yamlFenceRE.FindStringIndex(reply) != nil
	if m := errorPrefixRE.FindStringSubmatch(reply); m != nil && !hasYAML {
		return "", nil, &ErrClaudeRefused{Reason: strings.TrimSpace(m[1])}
	}
	if m := summaryRE.FindStringSubmatch(reply); m != nil {
		summary = strings.TrimSpace(m[1])
	}
	m := yamlFenceRE.FindStringSubmatch(reply)
	if m == nil {
		return "", nil, errors.New("no ```yaml block found in claude reply")
	}
	body := strings.TrimRight(m[1], "\n") + "\n"
	return summary, []byte(body), nil
}

func unifiedDiff(path, before, after string) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: path,
		ToFile:   path,
		Context:  3,
	}
	return difflib.GetUnifiedDiffString(diff)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
