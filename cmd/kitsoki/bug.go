// bug.go — implements `kitsoki bug create`, `kitsoki bug list`, and
// `kitsoki bug show`.
//
// File-system backend for bug reports filed via /meta story bug and
// /meta kitsoki bug (handled by the `story-bug-reporter` and
// `kitsoki-bug-reporter` agents). Each report is written as a single
// markdown file under <target-root>/issues/bugs/<UTC-timestamp>-<slug>.md
// so the pile is grep-friendly and survives without any database. The
// agent invokes `kitsoki bug create` via Bash; the kitsoki binary's own
// directory is prepended to PATH for every claude subprocess
// (internal/host/oracle_runner.go) so this resolves whether kitsoki
// was launched via `go run`, `go install`, or a packaged build.
//
// `<target-root>` resolves to:
//   - the current working directory (or --target-dir) for `--target story`
//   - $KITSOKI_REPO       (or --target-dir) for `--target kitsoki`
//
// The extra `issues/` prefix reserves room for sibling categories
// (`issues/features/`, `issues/incidents/`) without re-shuffling later.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// bugCmd is the top-level `bug` subcommand. Children:
//   - create: file a new bug report.
//   - list:   one line per bug under issues/bugs/, newest first.
//   - show:   dump a single bug's markdown to stdout by id.
func bugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bug",
		Short: "File and inspect local bug reports",
		Long: `Local-filesystem bug-tracking primitives.

Bugs are stored as markdown files under <target-root>/issues/bugs/,
one file per report, named "<UTC timestamp>-<slug>.md". The
<target-root> is the running app's directory for story bugs
(--target story) and $KITSOKI_REPO for engine bugs (--target kitsoki).

The agent (/meta story bug or /meta kitsoki bug) calls this
subcommand to record what the user described; humans grep or edit
the files directly.

No external service, no schema beyond the markdown template. Move a
bug to an external tracker by copying the file's body verbatim; the
future "kitsoki bug sync" command will write the remote id back into
the file's frontmatter without touching the body.`,
	}
	cmd.AddCommand(bugCreateCmd())
	cmd.AddCommand(bugListCmd())
	cmd.AddCommand(bugShowCmd())
	return cmd
}

// bugCreateCmd implements `kitsoki bug create`. Writes one markdown
// file to <target-root>/issues/bugs/ and prints its path (relative to
// the resolved target-root) on stdout so the agent can echo it back
// to the user.
func bugCreateCmd() *cobra.Command {
	var (
		target      string
		title       string
		body        string
		reproSteps  []string
		statePath   string
		appID       string
		component   string
		severity    string
		traceRef    string
		targetDir   string
		clockNowSec int64
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "File a new bug report (writes a markdown file)",
		Long: `Append a bug report to <target-root>/issues/bugs/.

Target resolution:
  --target story    writes under <--target-dir | $PWD>/issues/bugs/
  --target kitsoki  writes under <--target-dir | $KITSOKI_REPO>/issues/bugs/
                    (errors if neither flag nor env is set)

Required:
  --target      story|kitsoki (no default — pick the surface that surprised you)
  --title       one-line title (becomes the slug after lowercasing + hyphenating)
  --body        the narrative — what was expected, what happened, why it matters

Optional:
  --repro       repeatable: one reproduction step per flag (numbered in output)
  --state-path  state where the bug surfaced, e.g. main.foyer (story-target only)
  --app-id      running app's id, e.g. cloak                   (story-target only)
  --component   kitsoki package the bug surfaced from, e.g. tui (kitsoki-target only)
  --severity    free-form severity tag; agent prompts use low|med|high
  --trace-ref   relative path to a trace file or a session id
  --target-dir  override the resolved target-root (escape hatch)

Output: prints the path to the created file, relative to the
resolved target-root. Exit 1 on error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if strings.TrimSpace(title) == "" {
				return fmt.Errorf("--title is required")
			}
			if strings.TrimSpace(body) == "" {
				return fmt.Errorf("--body is required")
			}
			normTarget, err := normaliseTarget(target)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			if clockNowSec > 0 {
				now = time.Unix(clockNowSec, 0).UTC()
			}

			root, err := resolveTargetRoot(normTarget, targetDir)
			if err != nil {
				return err
			}

			// Warn — but do not fail — when target-specific flags are
			// passed against the wrong target. Mirrors the proposal §1.4
			// "silently ignored (with a one-line stderr warning)" rule.
			warnW := cmd.ErrOrStderr()
			switch normTarget {
			case "story":
				if component != "" {
					fmt.Fprintln(warnW, "warning: --component is kitsoki-only; ignored for --target story")
					component = ""
				}
			case "kitsoki":
				if statePath != "" {
					fmt.Fprintln(warnW, "warning: --state-path is story-only; ignored for --target kitsoki")
					statePath = ""
				}
				if appID != "" {
					fmt.Fprintln(warnW, "warning: --app-id is story-only; ignored for --target kitsoki")
					appID = ""
				}
			}

			bugsDir := filepath.Join(root, "issues", "bugs")
			if err := os.MkdirAll(bugsDir, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", bugsDir, err)
			}
			filename := bugFilename(now, title)
			full := filepath.Join(bugsDir, filename)

			rec := bugRecord{
				ID:         strings.TrimSuffix(filename, ".md"),
				Title:      title,
				Body:       body,
				ReproSteps: reproSteps,
				Target:     normTarget,
				FiledAt:    now,
				FiledBy:    os.Getenv("USER"),
				AppID:      appID,
				StatePath:  statePath,
				Component:  component,
				Severity:   severity,
				Status:     "open",
				TraceRef:   traceRef,
			}

			// Pull the short git SHA at filing time for kitsoki-target
			// bugs. Best-effort: if `git` is missing or the target-root
			// is not a repo, leave the field empty and emit a warning.
			if normTarget == "kitsoki" {
				rev, gitErr := readShortGitSHA(root)
				if gitErr != nil {
					fmt.Fprintf(warnW, "warning: could not read kitsoki_rev from %s: %v\n", root, gitErr)
				} else {
					rec.KitsokiRev = rev
				}
			}

			content := renderBugMarkdown(rec)
			if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", full, err)
			}
			// Print the path relative to the target-root so the agent's
			// echo stays portable across machines.
			rel, err := filepath.Rel(root, full)
			if err != nil {
				rel = full
			}
			fmt.Fprintln(cmd.OutOrStdout(), rel)
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "bug target: story|kitsoki (required)")
	cmd.Flags().StringVar(&title, "title", "", "one-line bug title (required)")
	cmd.Flags().StringVar(&body, "body", "", "the narrative — what was expected, what happened (required)")
	cmd.Flags().StringArrayVar(&reproSteps, "repro", nil,
		"reproduction step; pass --repro repeatedly to record numbered steps")
	cmd.Flags().StringVar(&statePath, "state-path", "", "FSM state where the bug surfaced (story-target only)")
	cmd.Flags().StringVar(&appID, "app-id", "", "id of the running app (story-target only)")
	cmd.Flags().StringVar(&component, "component", "", "kitsoki package the bug surfaced from (kitsoki-target only)")
	cmd.Flags().StringVar(&severity, "severity", "", "free-form severity tag (agent prompts use low|med|high)")
	cmd.Flags().StringVar(&traceRef, "trace-ref", "", "path to a trace file or a session id")
	cmd.Flags().StringVar(&targetDir, "target-dir", "", "override the resolved target-root (escape hatch)")
	cmd.Flags().Int64Var(&clockNowSec, "clock-now", 0,
		"Unix-seconds override for the filed-at timestamp (tests only; 0 = use real clock)")
	_ = cmd.Flags().MarkHidden("clock-now")
	return cmd
}

// bugListCmd implements `kitsoki bug list`. Walks the issues/bugs/
// directory (non-recursive), parses each file's frontmatter, and
// prints one line per bug sorted newest-first.
func bugListCmd() *cobra.Command {
	var (
		target    string
		targetDir string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List bugs filed under <target-root>/issues/bugs/",
		Long: `Print one line per filed bug, sorted newest first.

Columns (tab-separated): id, severity, status, title. Missing
severity renders as "?"; missing status defaults to "open".

A missing issues/bugs/ directory is not an error — the command
prints nothing and exits 0.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			normTarget, err := normaliseTarget(target)
			if err != nil {
				return err
			}
			root, err := resolveTargetRoot(normTarget, targetDir)
			if err != nil {
				return err
			}
			bugsDir := filepath.Join(root, "issues", "bugs")
			entries, err := os.ReadDir(bugsDir)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return fmt.Errorf("read %s: %w", bugsDir, err)
			}

			type row struct {
				ID       string
				Severity string
				Status   string
				Title    string
			}
			var rows []row
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if !strings.HasSuffix(name, ".md") {
					continue
				}
				full := filepath.Join(bugsDir, name)
				data, readErr := os.ReadFile(full)
				if readErr != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: skip %s: %v\n", name, readErr)
					continue
				}
				fm := parseFrontmatter(data)
				rows = append(rows, row{
					ID:       strings.TrimSuffix(name, ".md"),
					Severity: stringOrDefault(fm["severity"], "?"),
					Status:   stringOrDefault(fm["status"], "open"),
					Title:    stringOrDefault(fm["title"], ""),
				})
			}
			// Newest first: id starts with the UTC timestamp so lex
			// descending == chrono descending.
			sort.Slice(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
			for _, r := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n",
					r.ID, r.Severity, r.Status, r.Title)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "bug target: story|kitsoki (required)")
	cmd.Flags().StringVar(&targetDir, "target-dir", "", "override the resolved target-root (escape hatch)")
	return cmd
}

// bugShowCmd implements `kitsoki bug show <id>`. Reads the bug file
// by id (filename without ".md") under the resolved target-root and
// writes it verbatim to stdout. Exits 1 if the file is missing.
func bugShowCmd() *cobra.Command {
	var (
		target    string
		targetDir string
	)
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Print a single bug file verbatim",
		Long: `Read <target-root>/issues/bugs/<id>.md and write it to stdout.

<id> is the filename without ".md" (the same id printed by
"kitsoki bug list"). Exit 1 with a clear message if no file
with that id exists.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			id := args[0]
			normTarget, err := normaliseTarget(target)
			if err != nil {
				return err
			}
			root, err := resolveTargetRoot(normTarget, targetDir)
			if err != nil {
				return err
			}
			full := filepath.Join(root, "issues", "bugs", id+".md")
			data, err := os.ReadFile(full)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("bug %q not found in %s",
						id, filepath.Join(root, "issues", "bugs"))
				}
				return fmt.Errorf("read %s: %w", full, err)
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "bug target: story|kitsoki (required)")
	cmd.Flags().StringVar(&targetDir, "target-dir", "", "override the resolved target-root (escape hatch)")
	return cmd
}

// normaliseTarget validates --target. Empty / unknown values fail; the
// flag has no default so the caller must pick a side explicitly (§1.4).
func normaliseTarget(target string) (string, error) {
	switch strings.TrimSpace(target) {
	case "story", "kitsoki":
		return strings.TrimSpace(target), nil
	case "":
		return "", fmt.Errorf("--target is required (story|kitsoki)")
	default:
		return "", fmt.Errorf("--target must be story or kitsoki (got %q)", target)
	}
}

// resolveTargetRoot returns the directory that <target-root>/issues/bugs
// lives under. `target` is assumed to be already normalised. The
// `--target-dir` flag always wins; otherwise:
//
//   - story:   $PWD
//   - kitsoki: $KITSOKI_REPO (errors if unset)
func resolveTargetRoot(target, targetDir string) (string, error) {
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
			return "", fmt.Errorf("--target kitsoki requires --target-dir or $KITSOKI_REPO")
		}
		return repo, nil
	default:
		// Caller normalised; the validator already errored.
		return "", fmt.Errorf("internal: unknown target %q", target)
	}
}

// readShortGitSHA shells out to `git -C <root> rev-parse --short HEAD`
// and returns the trimmed output. Returns an error if the command
// fails for any reason — the caller decides whether to warn.
func readShortGitSHA(root string) (string, error) {
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

// bugRecord is the in-memory representation of a single bug, passed to
// renderBugMarkdown. Field order mirrors the frontmatter sections in
// the rendered output (identity, target-context, classification,
// evidence) so the struct itself reads as a quick spec.
type bugRecord struct {
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
	Severity string // optional
	Status   string // "open" default; for now always "open" on create

	// evidence
	TraceRef string // optional, both

	// body
	Body       string
	ReproSteps []string
}

// bugFilename produces the on-disk name for a bug: "<UTC timestamp>-<slug>.md".
// Timestamp format is RFC-3339-ish but filesystem-safe (no colons). Slug is
// the title lowercased, ASCII-only, hyphenated, and truncated to keep paths
// reasonable. Two bugs filed in the same second with the same title produce
// the same filename — that's intentional: the second WriteFile silently
// overwrites the first, which is the right behaviour for an agent that
// re-runs the same call after a transient error.
func bugFilename(filedAt time.Time, title string) string {
	ts := filedAt.Format("2006-01-02T150405Z")
	slug := bugSlug(title)
	return ts + "-" + slug + ".md"
}

// bugSlug converts a freeform title into a filesystem-safe slug:
// lowercase, ASCII letters/digits and hyphens, hyphen-separated, trimmed
// to 60 chars. Empty result falls back to "bug" so the filename is
// always well-formed.
func bugSlug(title string) string {
	var b strings.Builder
	lastHyphen := true
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// non-ASCII letters/digits — collapse to a hyphen so the
			// slug stays portable across filesystems.
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

// renderBugMarkdown produces the file body. Format is human-edit-friendly:
// a YAML front-matter block for machine fields followed by markdown prose.
// The frontmatter is grouped into commented sections (identity, target
// context, classification, evidence) so a human skimming the file knows
// what to edit. Empty optional fields are omitted; section comments are
// suppressed when their entire section is empty (except identity, which
// always has at least id/title/target/filed_at).
func renderBugMarkdown(r bugRecord) string {
	var sb strings.Builder
	sb.WriteString("---\n")

	// identity — always emitted.
	sb.WriteString("# --- identity ------------------------------------------------\n")
	sb.WriteString("id: ")
	sb.WriteString(yamlQuoteLine(r.ID))
	sb.WriteString("\ntitle: ")
	sb.WriteString(yamlQuoteLine(r.Title))
	sb.WriteString("\ntarget: ")
	sb.WriteString(yamlQuoteLine(r.Target))
	sb.WriteString("\nfiled_at: ")
	sb.WriteString(r.FiledAt.Format(time.RFC3339))
	if r.FiledBy != "" {
		sb.WriteString("\nfiled_by: ")
		sb.WriteString(yamlQuoteLine(r.FiledBy))
	}
	sb.WriteString("\n")

	// target context — only when at least one field is set.
	if r.AppID != "" || r.StatePath != "" || r.Component != "" || r.KitsokiRev != "" {
		sb.WriteString("\n# --- target context ------------------------------------------\n")
		if r.AppID != "" {
			sb.WriteString("app_id: ")
			sb.WriteString(yamlQuoteLine(r.AppID))
			sb.WriteString("\n")
		}
		if r.StatePath != "" {
			sb.WriteString("state_path: ")
			sb.WriteString(yamlQuoteLine(r.StatePath))
			sb.WriteString("\n")
		}
		if r.Component != "" {
			sb.WriteString("component: ")
			sb.WriteString(yamlQuoteLine(r.Component))
			sb.WriteString("\n")
		}
		if r.KitsokiRev != "" {
			sb.WriteString("kitsoki_rev: ")
			sb.WriteString(yamlQuoteLine(r.KitsokiRev))
			sb.WriteString("\n")
		}
	}

	// classification — status is always emitted; labels: [] is always
	// emitted as a forward-compat hand-edit hook (§2.1).
	sb.WriteString("\n# --- classification ------------------------------------------\n")
	if r.Severity != "" {
		sb.WriteString("severity: ")
		sb.WriteString(yamlQuoteLine(r.Severity))
		sb.WriteString("\n")
	}
	status := r.Status
	if status == "" {
		status = "open"
	}
	sb.WriteString("status: ")
	sb.WriteString(yamlQuoteLine(status))
	sb.WriteString("\n")
	sb.WriteString("labels: []\n")

	// evidence — related: [] is always emitted as a hand-edit hook.
	sb.WriteString("\n# --- evidence ------------------------------------------------\n")
	if r.TraceRef != "" {
		sb.WriteString("trace_ref: ")
		sb.WriteString(yamlQuoteLine(r.TraceRef))
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

// yamlQuoteLine returns s wrapped in double quotes with inner quotes and
// backslashes escaped. Keeps the front-matter parseable when the value
// contains colons, quotes, or other YAML metacharacters.
func yamlQuoteLine(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
	return `"` + escaped + `"`
}

// parseFrontmatter extracts the first YAML frontmatter block from a
// markdown file's bytes and returns a flat string map of its scalar
// fields. The parser is deliberately minimal — Phase C only needs
// title/severity/status/target, and we round-trip through yaml.v3 so
// quoting (`title: "foo: bar"`) is handled correctly.
//
// Returns an empty map on any error (no frontmatter, bad YAML) so the
// list command stays robust against half-written files.
func parseFrontmatter(data []byte) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	// Increase the scanner buffer — long bodies might otherwise blow
	// past the default 64 KiB line cap. Frontmatter itself is small,
	// but the underlying scanner sees the whole file.
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
			// skip
		default:
			out[k] = fmt.Sprintf("%v", t)
		}
	}
	return out
}

// stringOrDefault returns v if non-empty, else def.
func stringOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
