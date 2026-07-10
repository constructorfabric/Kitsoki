// bug.go — implements `kitsoki bug create`, `kitsoki bug list`, and
// `kitsoki bug show`.
//
// The creation/rendering core lives in internal/bugfile so non-CLI
// callers (the runstatus server's runstatus.bug.report RPC) can reuse it
// without importing package main. This file is the cobra adapter; the
// thin type aliases below preserve the historical local names used by
// the bug command and its tests.
//
// Each local report is written as a single markdown file under
// <target-root>/issues/bugs/<UTC-timestamp>-<slug>.md or, for the
// local-artifact sink, <target-root>/.artifacts/issues/bugs/<id>.md so the pile
// is grep-friendly and survives without any database.
//
// `<target-root>` resolves to:
//   - the current working directory (or --target-dir) for `--target story`
//   - $KITSOKI_REPO       (or --target-dir) for `--target kitsoki`
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/bugfile"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/harscrub"
	"kitsoki/internal/webconfig"
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

const (
	bugSinkLocalProject  = "local-project"
	bugSinkLocalArtifact = "local-artifact"
	bugSinkGitHub        = "github"
)

// bugCmd is the top-level `bug` subcommand.
func bugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bug",
		Short: "File and inspect local bug reports",
		Long: `Bug-tracking primitives.

Local bugs are stored as markdown files under <target-root>/issues/bugs/
or <target-root>/.artifacts/issues/bugs/, one file per report, named
"<UTC timestamp>-<slug>.md". The <target-root> is the running app's directory
for story bugs (--target story) and $KITSOKI_REPO for engine bugs
(--target kitsoki).

The agent (/meta story bug or /meta kitsoki bug) calls this
subcommand to record what the user described; humans grep or edit
the files directly.

GitHub filing is available through --sink github / --github. Local sinks have
no external service and no schema beyond the markdown template.`,
	}
	cmd.AddCommand(bugCreateCmd())
	cmd.AddCommand(bugListCmd())
	cmd.AddCommand(bugShowCmd())
	cmd.AddCommand(bugFileFindingsCmd())
	return cmd
}

// bugFileFindingsCmd implements `kitsoki bug file-findings`: file every
// credible `issue` finding recorded in a product-journey run bundle as a
// GitHub issue via the same artifact-preserving orchestration
// (host.GitHubFileFindings → host.GitHubFileBug with UploadArtifacts) the web
// Report-bug and TUI /bug paths use.
func bugFileFindingsCmd() *cobra.Command {
	var (
		runDir string
		repo   string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "file-findings",
		Short: "File a run bundle's issue findings as GitHub issues with uploaded evidence",
		Long: `Walk <--run-dir>/findings.json (a tools/product-journey run bundle),
and for every credible issue finding (kind=issue, origin!=seeded) that has no
recorded GitHub issue yet:

  - assemble an expected/actual/reproduction body from the finding, the
    scenario contract (driver-plan.json), and the driver journal,
  - upload the finding's locally-resolvable evidence as release assets,
  - file one GitHub issue through the kitsoki bug orchestration
    (## Artifacts section + kitsoki metadata block),
  - record the issue URL back into findings.json (item.github_issue) so
    re-runs are idempotent, and stamp findings.filing so the runner's
    review/validate gates can require full filing coverage.

--dry-run renders the issues that WOULD be filed (title/body/evidence) as JSON
without calling GitHub or writing to the bundle.

Output is a single JSON object on stdout. The command exits 0 whenever the
walk completes; per-finding failures are reported in the JSON (failed count +
outcome rows), not as an exit code.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			var privacyChecker bugprivacy.Checker
			if !dryRun {
				if cfg, cfgErr := webconfig.Load(webconfig.DefaultConfigFile); cfgErr == nil {
					privacyChecker = bugPrivacyCheckerFromConfig(cfg, runDir)
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "bug privacy check: starting")
			}
			res, err := host.GitHubFileFindings(context.Background(), host.FindingsFilingInput{
				RunDir:         runDir,
				Repo:           repo,
				DryRun:         dryRun,
				KitsokiRev:     gitShortRevCWD(),
				FiledBy:        os.Getenv("USER"),
				PrivacyChecker: privacyChecker,
			})
			if err != nil {
				return fmt.Errorf("file findings (%s): %w", repo, err)
			}
			if !dryRun {
				fmt.Fprintf(cmd.ErrOrStderr(), "bug privacy check: completed for findings; filed=%d related=%d failed=%d\n", res.Filed, res.Related, res.Failed)
			}
			data, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "product-journey run bundle directory (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo to file issues on (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "render what would be filed without calling GitHub or writing the bundle")
	_ = cmd.MarkFlagRequired("run-dir")
	_ = cmd.MarkFlagRequired("repo")
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
		sink        string
		clockNowSec int64
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "File a new bug report (writes a markdown file)",
		Long: `Append a bug report to a local bug sink or file it remotely.

Target resolution:
  --sink local-project:
    --target story    writes under <--target-dir | $PWD>/issues/bugs/
    --target kitsoki  writes under <--target-dir | $KITSOKI_REPO>/issues/bugs/
                      (errors if neither flag nor env is set)
  --sink local-artifact:
    writes the same markdown format under <resolved-target-root>/.artifacts/issues/bugs/
  --sink github:
    files on --github <owner/repo>

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
  --sink        local-project|local-artifact|github

Output: prints the path to the created file, relative to the
resolved target-root. Exit 1 on error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			var now time.Time
			if clockNowSec > 0 {
				now = time.Unix(clockNowSec, 0).UTC()
			}
			normTarget, err := normaliseTarget(target)
			if err != nil {
				return err
			}
			if strings.TrimSpace(title) == "" {
				return fmt.Errorf("--title is required")
			}
			if strings.TrimSpace(body) == "" {
				return fmt.Errorf("--body is required")
			}
			sinkMode, err := resolveBugCreateSink(sink, githubRepo)
			if err != nil {
				return err
			}
			var localRoot, displayPrefix string
			privacyRoot := bugPrivacyFollowUpRoot(targetDir)
			privacyDisplayPrefix := ""
			if sinkMode != bugSinkGitHub {
				localRoot, displayPrefix, err = resolveBugLocalRoot(normTarget, targetDir, sinkMode)
				if err != nil {
					return err
				}
				if sinkMode == bugSinkLocalArtifact {
					privacyRoot = localRoot
					privacyDisplayPrefix = displayPrefix
				}
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "bug privacy check: starting")
			safeReport, privacy, perr := bugprivacy.Check(context.Background(), nil, bugprivacy.Report{
				Surface:    "cli",
				Target:     normTarget,
				Title:      title,
				Body:       body,
				ReproSteps: reproSteps,
				Component:  component,
				TraceRef:   traceRef,
			}, harscrub.ScrubOptions{
				Home:           os.Getenv("HOME"),
				SecretPatterns: harscrub.DefaultSecretPatterns(),
			}, privacyRoot, os.Getenv("USER"))
			if perr != nil {
				return fmt.Errorf("bug privacy check: %w", perr)
			}
			privacy = prefixBugPrivacyFollowUp(privacy, privacyDisplayPrefix)
			if privacy.Blocked() {
				return fmt.Errorf("%s%s", privacy.Message, cliPrivacyFollowUpSuffix(privacy))
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "bug privacy check: %s%s\n", privacy.Message, cliPrivacyFollowUpSuffix(privacy))
			title = safeReport.Title
			body = safeReport.Body
			reproSteps = safeReport.ReproSteps
			component = safeReport.Component
			traceRef = safeReport.TraceRef

			// GitHub mode (--github owner/repo): file a real GitHub issue via the
			// same host.GitHubFileBug path the web Report-bug RPC uses (text-only —
			// the CLI captures no screenshot/HAR/rrweb), and print the issue URL.
			if sinkMode == bugSinkGitHub {
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
				Target:     normTarget,
				Title:      title,
				Body:       body,
				ReproSteps: reproSteps,
				AppID:      appID,
				StatePath:  statePath,
				Component:  component,
				Severity:   severity,
				TraceRef:   traceRef,
				TargetDir:  localRoot,
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
			fmt.Fprintln(cmd.OutOrStdout(), displayBugPath(displayPrefix, rel))
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
	cmd.Flags().StringVar(&githubRepo, "github", "", "file a GitHub issue on this owner/repo instead of a local markdown file (requires GitHub auth; run `kitsoki gh-agent login`, set up a GitHub App token, or provide GH_TOKEN/GITHUB_TOKEN)")
	cmd.Flags().StringVar(&sink, "sink", "", "filing sink: local-project (default), local-artifact, or github")
	cmd.Flags().Int64Var(&clockNowSec, "clock-now", 0,
		"Unix-seconds override for the filed-at timestamp (tests only; 0 = use real clock)")
	_ = cmd.Flags().MarkHidden("clock-now")
	return cmd
}

func normaliseBugSink(sink string) (string, error) {
	switch strings.TrimSpace(sink) {
	case "", bugSinkLocalProject, "local", "project":
		return bugSinkLocalProject, nil
	case bugSinkLocalArtifact, "artifact", "artifacts":
		return bugSinkLocalArtifact, nil
	case bugSinkGitHub:
		return bugSinkGitHub, nil
	default:
		return "", fmt.Errorf("--sink must be local-project, local-artifact, or github (got %q)", sink)
	}
}

func resolveBugCreateSink(sink, githubRepo string) (string, error) {
	if strings.TrimSpace(sink) == "" && strings.TrimSpace(githubRepo) != "" {
		return bugSinkGitHub, nil
	}
	mode, err := normaliseBugSink(sink)
	if err != nil {
		return "", err
	}
	if mode == bugSinkGitHub && strings.TrimSpace(githubRepo) == "" {
		return "", fmt.Errorf("--sink github requires --github <owner/repo>")
	}
	if mode != bugSinkGitHub && strings.TrimSpace(githubRepo) != "" {
		return "", fmt.Errorf("--github can only be used with --sink github")
	}
	return mode, nil
}

func resolveBugLocalSink(sink string) (string, error) {
	mode, err := normaliseBugSink(sink)
	if err != nil {
		return "", err
	}
	if mode == bugSinkGitHub {
		return "", fmt.Errorf("--sink github is not supported for local bug inspection")
	}
	return mode, nil
}

func resolveBugLocalRoot(target, targetDir, sink string) (root, displayPrefix string, err error) {
	base, err := resolveTargetRoot(target, targetDir)
	if err != nil {
		return "", "", err
	}
	if sink == bugSinkLocalArtifact {
		return filepath.Join(base, ".artifacts"), ".artifacts", nil
	}
	return base, "", nil
}

func displayBugPath(prefix, rel string) string {
	if strings.TrimSpace(prefix) == "" {
		return rel
	}
	return filepath.Join(prefix, rel)
}

func prefixBugPrivacyFollowUp(privacy bugprivacy.Result, prefix string) bugprivacy.Result {
	if strings.TrimSpace(prefix) == "" || strings.TrimSpace(privacy.FollowUpPath) == "" {
		return privacy
	}
	privacy.FollowUpPath = displayBugPath(prefix, privacy.FollowUpPath)
	return privacy
}

func bugPrivacyFollowUpRoot(targetDir string) string {
	if strings.TrimSpace(targetDir) != "" {
		return targetDir
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func cliPrivacyFollowUpSuffix(privacy bugprivacy.Result) string {
	if strings.TrimSpace(privacy.FollowUpPath) == "" {
		return ""
	}
	return "; depersonalized follow-up filed at " + filepath.ToSlash(privacy.FollowUpPath)
}

// bugListCmd implements `kitsoki bug list`.
func bugListCmd() *cobra.Command {
	var (
		target    string
		targetDir string
		sink      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List bugs filed under the selected local sink",
		Long: `Print one line per filed bug, sorted newest first.

Columns (tab-separated): id, severity, status, title. Missing
severity renders as "?"; missing status defaults to "open".

A missing issues/bugs/ directory in the selected sink is not an error — the command
prints nothing and exits 0.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			normTarget, err := normaliseTarget(target)
			if err != nil {
				return err
			}
			sinkMode, err := resolveBugLocalSink(sink)
			if err != nil {
				return err
			}
			root, _, err := resolveBugLocalRoot(normTarget, targetDir, sinkMode)
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
	cmd.Flags().StringVar(&sink, "sink", "", "local inspection sink: local-project (default) or local-artifact")
	return cmd
}

// bugShowCmd implements `kitsoki bug show <id>`.
func bugShowCmd() *cobra.Command {
	var (
		target    string
		targetDir string
		sink      string
	)
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Print a single bug file verbatim",
		Long: `Read <id>.md from the selected local sink and write it to stdout.

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
			sinkMode, err := resolveBugLocalSink(sink)
			if err != nil {
				return err
			}
			root, _, err := resolveBugLocalRoot(normTarget, targetDir, sinkMode)
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
	cmd.Flags().StringVar(&sink, "sink", "", "local inspection sink: local-project (default) or local-artifact")
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
