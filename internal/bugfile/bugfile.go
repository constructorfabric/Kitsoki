// Package bugfile is the LLM-free, filesystem backend for local bug reports.
//
// It writes one markdown file per report under <target-root>/issues/bugs/
// (<UTC-timestamp>-<slug>.md) with a YAML frontmatter block followed by
// prose. The pile is grep-friendly and survives without any database.
//
// This package was extracted from cmd/kitsoki's bug.go so non-CLI callers
// (the runstatus server's runstatus.bug.report RPC) can reuse the exact
// same creation orchestration without importing package main. The cobra
// `kitsoki bug` command in cmd/kitsoki now delegates here.
package bugfile

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"kitsoki/internal/reportmeta"

	"gopkg.in/yaml.v3"
)

// CreateRequest carries every input needed to file a bug, decoupled from
// cobra so non-CLI callers (e.g. a server RPC) can reuse the exact same
// creation orchestration as `kitsoki bug create`.
type CreateRequest struct {
	// Target is "story" or "kitsoki" (normalised internally; required).
	Target string
	// Title and Body are required; everything else is optional.
	Title      string
	Body       string
	ReproSteps []string

	// target context
	AppID     string // story-only
	StatePath string // story-only
	Component string // kitsoki-only

	// classification / evidence
	Severity string
	Labels   []string
	TraceRef string

	// TargetDir overrides the resolved target-root (escape hatch); when
	// empty the root is resolved from Target via ResolveTargetRoot.
	TargetDir string

	// FiledBy records who filed the bug (frontmatter filed_by). Empty is
	// allowed; the CLI passes $USER.
	FiledBy string

	// Runtime records the kitsoki engine/story versions active at filing time.
	// When empty, Create captures engine metadata from the resolved target root.
	Runtime reportmeta.Snapshot

	// Now injects the filed-at clock for deterministic tests. Zero value
	// means "use time.Now().UTC()".
	Now time.Time

	// Warnf, when non-nil, receives one-line warnings (wrong-target flags,
	// failed git-rev lookups). Defaults to a no-op so callers may ignore
	// them. Mirrors fmt.Fprintf's (format, args...) shape.
	Warnf func(format string, args ...any)
}

// Create runs the full bug-creation orchestration: it normalises the
// target, resolves the target-root, ensures issues/bugs/ exists, builds
// the Record, renders the markdown, and writes the .md file. It is the
// shared core behind `kitsoki bug create` and any non-CLI caller.
//
// Returns the bare id (filename without ".md"), the repo-relative path
// ("issues/bugs/<id>.md"), and the absolute path to the written file.
func Create(req CreateRequest) (id string, relPath string, absPath string, err error) {
	warnf := req.Warnf
	if warnf == nil {
		warnf = func(string, ...any) {}
	}
	if strings.TrimSpace(req.Title) == "" {
		return "", "", "", fmt.Errorf("--title is required")
	}
	if strings.TrimSpace(req.Body) == "" {
		return "", "", "", fmt.Errorf("--body is required")
	}
	normTarget, err := NormaliseTarget(req.Target)
	if err != nil {
		return "", "", "", err
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	root, err := ResolveTargetRoot(normTarget, req.TargetDir)
	if err != nil {
		return "", "", "", err
	}

	// Warn — but do not fail — when target-specific flags are passed
	// against the wrong target.
	appID, statePath, component := req.AppID, req.StatePath, req.Component
	switch normTarget {
	case "story":
		if component != "" {
			warnf("warning: --component is kitsoki-only; ignored for --target story\n")
			component = ""
		}
	case "kitsoki":
		if statePath != "" {
			warnf("warning: --state-path is story-only; ignored for --target kitsoki\n")
			statePath = ""
		}
		if appID != "" {
			warnf("warning: --app-id is story-only; ignored for --target kitsoki\n")
			appID = ""
		}
	}

	bugsDir := filepath.Join(root, "issues", "bugs")
	if err := os.MkdirAll(bugsDir, 0o755); err != nil {
		return "", "", "", fmt.Errorf("mkdir %s: %w", bugsDir, err)
	}
	filename := Filename(now, req.Title)
	full := filepath.Join(bugsDir, filename)

	rec := Record{
		ID:         strings.TrimSuffix(filename, ".md"),
		Title:      req.Title,
		Body:       req.Body,
		ReproSteps: req.ReproSteps,
		Target:     normTarget,
		FiledAt:    now,
		FiledBy:    req.FiledBy,
		AppID:      appID,
		StatePath:  statePath,
		Component:  component,
		Severity:   req.Severity,
		Labels:     cleanLabels(req.Labels),
		Status:     "open",
		TraceRef:   req.TraceRef,
		Runtime:    req.Runtime,
	}
	if rec.Runtime.Empty() {
		rec.Runtime = reportmeta.Capture(root, nil)
	}

	// Pull the short git SHA at filing time for kitsoki-target bugs.
	if normTarget == "kitsoki" {
		rev, gitErr := ReadShortGitSHA(root)
		if gitErr != nil {
			warnf("warning: could not read kitsoki_rev from %s: %v\n", root, gitErr)
		} else {
			rec.KitsokiRev = rev
		}
	}

	content := RenderMarkdown(rec)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write %s: %w", full, err)
	}

	rel, relErr := filepath.Rel(root, full)
	if relErr != nil {
		rel = full
	}
	return rec.ID, rel, full, nil
}

// NormaliseTarget validates --target. Empty / unknown values fail; the
// flag has no default so the caller must pick a side explicitly.
func NormaliseTarget(target string) (string, error) {
	switch strings.TrimSpace(target) {
	case "story", "kitsoki":
		return strings.TrimSpace(target), nil
	case "":
		return "", fmt.Errorf("--target is required (story|kitsoki)")
	default:
		return "", fmt.Errorf("--target must be story or kitsoki (got %q)", target)
	}
}

// ResolveTargetRoot returns the directory that <target-root>/issues/bugs
// lives under. target is assumed already normalised. --target-dir always
// wins; otherwise story => $PWD, kitsoki => $KITSOKI_REPO.
func ResolveTargetRoot(target, targetDir string) (string, error) {
	if targetDir != "" {
		return targetDir, nil
	}
	switch target {
	case "story":
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		return cwd, nil
	case "kitsoki":
		repo := strings.TrimSpace(os.Getenv("KITSOKI_REPO"))
		if repo == "" {
			return "", fmt.Errorf("--target kitsoki: kitsoki repo not found — run once from a kitsoki checkout to save it under ~/.kitsoki/repo, or pass --target-dir / set $KITSOKI_REPO")
		}
		return repo, nil
	default:
		return "", fmt.Errorf("internal: unknown target %q", target)
	}
}

// ReadShortGitSHA shells out to `git -C <root> rev-parse --short HEAD`.
func ReadShortGitSHA(root string) (string, error) {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("empty git rev-parse output")
	}
	return sha, nil
}

// Record is the in-memory representation of a single bug, passed to
// RenderMarkdown.
type Record struct {
	// identity
	ID      string
	Title   string
	Target  string // "story" | "kitsoki"
	FiledAt time.Time
	FiledBy string // $USER at write time; empty allowed

	// target context
	AppID      string // story-only
	StatePath  string // story-only
	Component  string // kitsoki-only
	KitsokiRev string // kitsoki-only, short SHA at write time

	// classification
	Severity string   // optional
	Labels   []string // optional
	Status   string   // "open" default; for now always "open" on create

	// evidence
	TraceRef string // optional, both
	Runtime  reportmeta.Snapshot

	// body
	Body       string
	ReproSteps []string
}

// Filename produces the on-disk name for a bug: "<UTC timestamp>-<slug>.md".
func Filename(filedAt time.Time, title string) string {
	ts := filedAt.Format("2006-01-02T150405Z")
	slug := Slug(title)
	return ts + "-" + slug + ".md"
}

// Slug converts a freeform title into a filesystem-safe slug.
func Slug(title string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if out == "" {
		out = "bug"
	}
	if len(out) > 60 {
		out = strings.TrimRight(out[:60], "-")
	}
	return out
}

// RenderMarkdown produces the file body: YAML frontmatter + prose.
func RenderMarkdown(r Record) string {
	var sb strings.Builder
	sb.WriteString("---\n")

	sb.WriteString("# --- identity ------------------------------------------------\n")
	sb.WriteString("id: ")
	sb.WriteString(YAMLQuoteLine(r.ID))
	sb.WriteString("\ntitle: ")
	sb.WriteString(YAMLQuoteLine(r.Title))
	sb.WriteString("\ntarget: ")
	sb.WriteString(YAMLQuoteLine(r.Target))
	sb.WriteString("\nfiled_at: ")
	sb.WriteString(r.FiledAt.Format(time.RFC3339))
	if r.FiledBy != "" {
		sb.WriteString("\nfiled_by: ")
		sb.WriteString(YAMLQuoteLine(r.FiledBy))
	}
	sb.WriteString("\n")

	if r.AppID != "" || r.StatePath != "" || r.Component != "" || r.KitsokiRev != "" {
		sb.WriteString("\n# --- target context ------------------------------------------\n")
		if r.AppID != "" {
			sb.WriteString("app_id: ")
			sb.WriteString(YAMLQuoteLine(r.AppID))
			sb.WriteString("\n")
		}
		if r.StatePath != "" {
			sb.WriteString("state_path: ")
			sb.WriteString(YAMLQuoteLine(r.StatePath))
			sb.WriteString("\n")
		}
		if r.Component != "" {
			sb.WriteString("component: ")
			sb.WriteString(YAMLQuoteLine(r.Component))
			sb.WriteString("\n")
		}
		if r.KitsokiRev != "" {
			sb.WriteString("kitsoki_rev: ")
			sb.WriteString(YAMLQuoteLine(r.KitsokiRev))
			sb.WriteString("\n")
		}
	}

	if fields := r.Runtime.Fields(); len(fields) > 0 {
		sb.WriteString("\n# --- runtime -------------------------------------------------\n")
		for _, f := range fields {
			sb.WriteString(f.Key)
			sb.WriteString(": ")
			sb.WriteString(YAMLQuoteLine(f.Value))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n# --- classification ------------------------------------------\n")
	if r.Severity != "" {
		sb.WriteString("severity: ")
		sb.WriteString(YAMLQuoteLine(r.Severity))
		sb.WriteString("\n")
	}
	status := r.Status
	if status == "" {
		status = "open"
	}
	sb.WriteString("status: ")
	sb.WriteString(YAMLQuoteLine(status))
	sb.WriteString("\n")
	if len(r.Labels) == 0 {
		sb.WriteString("labels: []\n")
	} else {
		sb.WriteString("labels:\n")
		for _, label := range r.Labels {
			sb.WriteString("  - ")
			sb.WriteString(YAMLQuoteLine(label))
			sb.WriteString("\n")
		}
	}

	sb.WriteString("\n# --- evidence ------------------------------------------------\n")
	if r.TraceRef != "" {
		sb.WriteString("trace_ref: ")
		sb.WriteString(YAMLQuoteLine(r.TraceRef))
		sb.WriteString("\n")
	}
	sb.WriteString("related: []\n")

	sb.WriteString("---\n\n")
	sb.WriteString("# ")
	sb.WriteString(r.Title)
	sb.WriteString("\n\n")
	sb.WriteString(strings.TrimSpace(r.Body))
	sb.WriteString("\n")
	if len(r.ReproSteps) > 0 {
		sb.WriteString("\n## Steps to reproduce\n\n")
		for i, step := range r.ReproSteps {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, strings.TrimSpace(step))
		}
	}
	return sb.String()
}

func cleanLabels(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	out := make([]string, 0, len(labels))
	seen := map[string]struct{}{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

// YAMLQuoteLine returns s wrapped in double quotes with inner quotes and
// backslashes escaped.
func YAMLQuoteLine(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + escaped + `"`
}

// ParseFrontmatter extracts the first YAML frontmatter block from a
// markdown file's bytes and returns a flat string map of its scalar fields.
func ParseFrontmatter(data []byte) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	if !scanner.Scan() {
		return out
	}
	if strings.TrimSpace(scanner.Text()) != "---" {
		return out
	}
	var fmBody strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		fmBody.WriteString(line)
		fmBody.WriteString("\n")
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(fmBody.String()), &raw); err != nil {
		return out
	}
	for k, v := range raw {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
		default:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}
