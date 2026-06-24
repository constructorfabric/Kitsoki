// bug.go — implements `kitsoki bug create`, `kitsoki bug list`, and
// `kitsoki bug show`.
//
// The creation/rendering core lives in internal/bugfile so non-CLI
// callers (the runstatus server's runstatus.bug.report RPC) can reuse it
// without importing package main. This file is the cobra adapter; the
// thin type aliases below preserve the historical local names used by
// the bug command and its tests.
//
// Each report is written as a single markdown file under
// <target-root>/issues/bugs/<UTC-timestamp>-<slug>.md so the pile is
// grep-friendly and survives without any database.
//
// `<target-root>` resolves to:
//   - the current working directory (or --target-dir) for `--target story`
//   - $KITSOKI_REPO       (or --target-dir) for `--target kitsoki`
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/bugfile"
	"kitsoki/internal/host"
)

// Back-compat local aliases over the extracted internal/bugfile core.
type (
	BugCreateRequest = bugfile.CreateRequest
	bugRecord        = bugfile.Record
)

var (
	CreateBug         = bugfile.Create
	normaliseTarget   = bugfile.NormaliseTarget
	resolveTargetRoot = bugfile.ResolveTargetRoot
	renderBugMarkdown = bugfile.RenderMarkdown
	bugFilename       = bugfile.Filename
	bugSlug           = bugfile.Slug
	yamlQuoteLine     = bugfile.YAMLQuoteLine
	parseFrontmatter  = bugfile.ParseFrontmatter
)

// bugCmd is the top-level `bug` subcommand.
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

No external service, no schema beyond the markdown template.`,
	}
	cmd.AddCommand(bugCreateCmd())
	cmd.AddCommand(bugListCmd())
	cmd.AddCommand(bugShowCmd())
	return cmd
}

// bugCreateCmd implements `kitsoki bug create`.
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
		githubRepo  string
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
			var now time.Time
			if clockNowSec > 0 {
				now = time.Unix(clockNowSec, 0).UTC()
			}

			// GitHub mode (--github owner/repo): file a real GitHub issue via the
			// same host.GitHubFileBug path the web Report-bug RPC uses (text-only —
			// the CLI captures no screenshot/HAR/rrweb), and print the issue URL.
			if strings.TrimSpace(githubRepo) != "" {
				normTarget, err := normaliseTarget(target)
				if err != nil {
					return err
				}
				ghBody := body
				if len(reproSteps) > 0 {
					var sb strings.Builder
					sb.WriteString("\n\n## Steps to reproduce\n\n")
					for i, r := range reproSteps {
						fmt.Fprintf(&sb, "%d. %s\n", i+1, r)
					}
					ghBody += sb.String()
				}
				res, err := host.GitHubFileBug(context.Background(), host.GitHubBugFiling{
					Repo:       githubRepo,
					Title:      title,
					Body:       ghBody,
					Severity:   severity,
					Component:  component,
					Target:     normTarget,
					TraceRef:   traceRef,
					KitsokiRev: gitShortRevCWD(),
					FiledBy:    os.Getenv("USER"),
				})
				if err != nil {
					return fmt.Errorf("file bug to github (%s): %w", githubRepo, err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), res.URL)
				return nil
			}

			req := BugCreateRequest{
				Target:     target,
				Title:      title,
				Body:       body,
				ReproSteps: reproSteps,
				AppID:      appID,
				StatePath:  statePath,
				Component:  component,
				Severity:   severity,
				TraceRef:   traceRef,
				TargetDir:  targetDir,
				FiledBy:    os.Getenv("USER"),
				Now:        now,
				Warnf: func(format string, args ...any) {
					fmt.Fprintf(cmd.ErrOrStderr(), format, args...)
				},
			}
			_, rel, _, err := CreateBug(req)
			if err != nil {
				return err
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
	cmd.Flags().StringVar(&githubRepo, "github", "", "file a GitHub issue on this owner/repo instead of a local markdown file (requires gh auth)")
	cmd.Flags().Int64Var(&clockNowSec, "clock-now", 0,
		"Unix-seconds override for the filed-at timestamp (tests only; 0 = use real clock)")
	_ = cmd.Flags().MarkHidden("clock-now")
	return cmd
}

// bugListCmd implements `kitsoki bug list`.
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

// bugShowCmd implements `kitsoki bug show <id>`.
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

// gitShortRevCWD returns the short HEAD sha of the repo containing the process
// cwd (best-effort; "" when not a repo / git unavailable).
func gitShortRevCWD() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// stringOrDefault returns v if non-empty, else def.
func stringOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
