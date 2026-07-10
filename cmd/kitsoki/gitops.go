package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/vcsops"

	_ "modernc.org/sqlite"
)

func gitopsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gitops",
		Short: "Story-owned git and ticket operations",
	}
	cmd.AddCommand(gitopsIssueStatusCmd())
	cmd.AddCommand(gitopsIssueCreateCmd())
	cmd.AddCommand(gitopsIssueCommentCmd())
	cmd.AddCommand(gitopsIssueTransitionCmd())
	cmd.AddCommand(gitopsIssueCloseCmd())
	cmd.AddCommand(gitopsIssueCloseoutCmd())
	cmd.AddCommand(gitopsIssueStateCacheCmd())
	cmd.AddCommand(gitopsAutonomousFixCmd())
	return cmd
}

func gitopsIssueStatusCmd() *cobra.Command {
	var (
		repo    string
		issueID string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "issue-status --repo owner/repo --id N",
		Short: "Fetch GitHub issue status through the native ticket provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
				"op":   "get",
				"repo": repo,
				"id":   issueID,
			})
			if err != nil {
				return err
			}
			if result.Error != "" {
				return fmt.Errorf("gitops issue-status: %s", result.Error)
			}
			data := gitopsIssueStatusResult(result.Data)
			if jsonOut {
				return writeGitopsJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issue: %s\n", stringValue(data, "id"))
			fmt.Fprintf(cmd.OutOrStdout(), "Title: %s\n", stringValue(data, "title"))
			fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", stringValue(data, "issue_state"))
			fmt.Fprintf(cmd.OutOrStdout(), "URL: %s\n", stringValue(data, "url"))
			if legacy := stringValue(data, "legacy_id"); legacy != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Legacy ID: %s\n", legacy)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&issueID, "id", "", "issue number or owner/repo#number")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsIssueCreateCmd() *cobra.Command {
	var (
		repo       string
		title      string
		body       string
		labels     string
		severity   string
		component  string
		target     string
		status     string
		traceRef   string
		kitsokiRev string
		filedBy    string
		legacyID   string
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "issue-create --repo owner/repo --title title [--body body]",
		Short: "Create a GitHub issue through the native ticket provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
				"op":          "create",
				"repo":        repo,
				"title":       title,
				"body":        body,
				"labels":      labels,
				"severity":    severity,
				"component":   component,
				"target":      target,
				"status":      status,
				"trace_ref":   traceRef,
				"kitsoki_rev": kitsokiRev,
				"filed_by":    filedBy,
				"legacy_id":   legacyID,
			})
			if err != nil {
				return err
			}
			if result.Error != "" {
				return fmt.Errorf("gitops issue-create: %s", result.Error)
			}
			data := gitopsIssueCreateResult(result.Data, title)
			if jsonOut {
				return writeGitopsJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", stringValue(data, "url"))
			if warning := stringValue(data, "warning"); warning != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&title, "title", "", "issue title")
	cmd.Flags().StringVar(&body, "body", "", "issue body")
	cmd.Flags().StringVar(&labels, "labels", "", "comma-separated labels")
	cmd.Flags().StringVar(&severity, "severity", "", "issue severity label axis, e.g. P1")
	cmd.Flags().StringVar(&component, "component", "", "component label axis")
	cmd.Flags().StringVar(&target, "target", "", "target label axis")
	cmd.Flags().StringVar(&status, "status", "", "status label axis")
	cmd.Flags().StringVar(&traceRef, "trace-ref", "", "trace reference recorded in kitsoki metadata")
	cmd.Flags().StringVar(&kitsokiRev, "kitsoki-rev", "", "kitsoki revision recorded in kitsoki metadata")
	cmd.Flags().StringVar(&filedBy, "filed-by", "", "actor recorded in kitsoki metadata")
	cmd.Flags().StringVar(&legacyID, "legacy-id", "", "legacy local issue id recorded in kitsoki metadata")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsIssueStateCacheCmd() *cobra.Command {
	var (
		findingsRoot string
		repo         string
		output       string
		jsonOut      bool
	)
	cmd := &cobra.Command{
		Use:   "issue-state-cache --findings-root .artifacts/product-journey [--output issues.json]",
		Short: "Refresh GitHub issue state for filed product-journey findings",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(findingsRoot) == "" {
				return fmt.Errorf("gitops issue-state-cache: --findings-root is required")
			}
			result, err := runGitopsIssueStateCache(cmd.Context(), gitopsIssueStateCacheOptions{
				FindingsRoot: findingsRoot,
				Repo:         repo,
				Output:       output,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				return writeGitopsJSON(cmd, result)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issues: %d\n", intValue(result, "issues_count"))
			fmt.Fprintf(cmd.OutOrStdout(), "Findings root: %s\n", stringValue(result, "findings_root"))
			if out := stringValue(result, "output"); out != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Output: %s\n", out)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&findingsRoot, "findings-root", "", "root containing product-journey findings.json files")
	cmd.Flags().StringVar(&repo, "repo", "", "fallback owner/repo for issue refs without a repo")
	cmd.Flags().StringVar(&output, "output", "", "write issue-state cache JSON to this path")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func writeGitopsJSON(cmd *cobra.Command, data map[string]any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	return enc.Encode(data)
}

func gitopsIssueStatusResult(data map[string]any) map[string]any {
	out := map[string]any{
		"status":      "issue_status",
		"issue_id":    stringValue(data, "id"),
		"id":          stringValue(data, "id"),
		"title":       stringValue(data, "title"),
		"issue_state": stringValue(data, "status"),
		"state":       stringValue(data, "status"),
		"issue_url":   stringValue(data, "url"),
		"url":         stringValue(data, "url"),
		"source":      firstNonBlank(stringValue(data, "source"), "github"),
	}
	if v := stringValue(data, "legacy_id"); v != "" {
		out["legacy_id"] = v
	}
	if v := stringValue(data, "body"); v != "" {
		out["body"] = v
	}
	if comments, ok := data["comments"]; ok {
		out["comments"] = comments
	}
	return out
}

func gitopsIssueCreateResult(data map[string]any, title string) map[string]any {
	out := map[string]any{
		"status":      "issue_created",
		"issue_id":    firstNonBlank(stringValue(data, "id"), stringValue(data, "number")),
		"id":          firstNonBlank(stringValue(data, "id"), stringValue(data, "number")),
		"title":       strings.TrimSpace(title),
		"issue_state": "created",
		"state":       "created",
		"issue_url":   stringValue(data, "url"),
		"url":         stringValue(data, "url"),
		"source":      "github",
	}
	if warning := stringValue(data, "warning"); warning != "" {
		out["warning"] = warning
	}
	return out
}

func gitopsIssueCommentCmd() *cobra.Command {
	var (
		repo    string
		issueID string
		body    string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "issue-comment --repo owner/repo --id N --body text",
		Short: "Comment on a GitHub issue through the native ticket provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
				"op":   "comment",
				"repo": repo,
				"id":   issueID,
				"body": body,
			})
			if err != nil {
				return err
			}
			if result.Error != "" {
				return fmt.Errorf("gitops issue-comment: %s", result.Error)
			}
			data := gitopsIssueCommentResult(result.Data, issueID)
			if jsonOut {
				return writeGitopsJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Comment: %s\n", stringValue(data, "comment_url"))
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&issueID, "id", "", "issue number or owner/repo#number")
	cmd.Flags().StringVar(&body, "body", "", "comment body")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsIssueTransitionCmd() *cobra.Command {
	var (
		repo    string
		issueID string
		to      string
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "issue-transition --repo owner/repo --id N --to resolved",
		Short: "Transition a GitHub issue through the native ticket provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
				"op":   "transition",
				"repo": repo,
				"id":   issueID,
				"to":   to,
			})
			if err != nil {
				return err
			}
			if result.Error != "" {
				return fmt.Errorf("gitops issue-transition: %s", result.Error)
			}
			data := gitopsIssueTransitionResult(result.Data, issueID, to)
			if jsonOut {
				return writeGitopsJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issue: %s\n", stringValue(data, "id"))
			fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", stringValue(data, "issue_state"))
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&issueID, "id", "", "issue number or owner/repo#number")
	cmd.Flags().StringVar(&to, "to", "", "target state, e.g. resolved or open")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsIssueCloseCmd() *cobra.Command {
	var (
		repo        string
		issueID     string
		commentBody string
		jsonOut     bool
	)
	cmd := &cobra.Command{
		Use:   "issue-close --repo owner/repo --id N [--comment-body text]",
		Short: "Close a GitHub issue through the native ticket provider",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var commentData map[string]any
			if strings.TrimSpace(commentBody) != "" {
				comment, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
					"op":   "comment",
					"repo": repo,
					"id":   issueID,
					"body": commentBody,
				})
				if err != nil {
					return err
				}
				if comment.Error != "" {
					return fmt.Errorf("gitops issue-close comment: %s", comment.Error)
				}
				commentData = comment.Data
			}
			transition, err := host.GitHubTicketHandler(cmd.Context(), map[string]any{
				"op":   "transition",
				"repo": repo,
				"id":   issueID,
				"to":   "closed",
			})
			if err != nil {
				return err
			}
			if transition.Error != "" {
				return fmt.Errorf("gitops issue-close: %s", transition.Error)
			}
			data := gitopsIssueCloseResult(transition.Data, commentData, issueID)
			if jsonOut {
				return writeGitopsJSON(cmd, data)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Issue: %s\n", stringValue(data, "id"))
			fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", stringValue(data, "issue_state"))
			if commentURL := stringValue(data, "comment_url"); commentURL != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Comment: %s\n", commentURL)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&issueID, "id", "", "issue number or owner/repo#number")
	cmd.Flags().StringVar(&commentBody, "comment-body", "", "optional close-out comment body posted before closing")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsIssueCloseoutCmd() *cobra.Command {
	var (
		runDir     string
		ticketRepo string
		statusJSON string
		jsonOut    bool
	)
	cmd := &cobra.Command{
		Use:   "issue-closeout --run-dir RUN --ticket-repo owner/repo [--status-json status.json]",
		Short: "Close fixed product-journey issues through native gitops",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(runDir) == "" {
				return fmt.Errorf("gitops issue-closeout: --run-dir is required")
			}
			if strings.TrimSpace(ticketRepo) == "" {
				return fmt.Errorf("gitops issue-closeout: --ticket-repo is required")
			}
			status, err := gitopsCloseoutStatus(runDir, statusJSON)
			if err != nil {
				return err
			}
			result, err := gitopsCloseoutFixedIssues(cmd.Context(), runDir, ticketRepo, status)
			if err != nil {
				return err
			}
			closeoutStatus := stringValue(result, "issue_closeout_status")
			if jsonOut {
				if err := writeGitopsJSON(cmd, result); err != nil {
					return err
				}
				if closeoutStatus != "closed" {
					return fmt.Errorf("gitops issue-closeout: %s", stringValue(result, "issue_closeout_summary"))
				}
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", closeoutStatus)
			fmt.Fprintf(cmd.OutOrStdout(), "Closed: %d\n", intValue(result, "issue_closeout_count"))
			if summary := stringValue(result, "issue_closeout_summary"); summary != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", summary)
			}
			if closeoutStatus != "closed" {
				return fmt.Errorf("gitops issue-closeout: %s", stringValue(result, "issue_closeout_summary"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "product-journey run directory containing findings.json")
	cmd.Flags().StringVar(&ticketRepo, "ticket-repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&statusJSON, "status-json", "", "optional JSON status file with gh_agent_drained_jobs")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

func gitopsCloseoutStatus(runDir, statusJSON string) (map[string]any, error) {
	if strings.TrimSpace(statusJSON) != "" {
		return gitopsReadJSONFile(statusJSON)
	}
	findings, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		return nil, err
	}
	ghAgent := mapValue(findings, "gh_agent")
	if ghAgent == nil {
		return map[string]any{"gh_agent_drained_jobs": []any{}}, nil
	}
	return map[string]any{
		"gh_agent_drained_jobs": ghAgent["drained_jobs"],
	}, nil
}

func gitopsIssueCommentResult(data map[string]any, issueID string) map[string]any {
	return map[string]any{
		"status":      "issue_commented",
		"issue_id":    strings.TrimSpace(issueID),
		"id":          strings.TrimSpace(issueID),
		"comment_id":  stringValue(data, "comment_id"),
		"comment_url": firstNonBlank(stringValue(data, "url"), stringValue(data, "comment_id")),
		"url":         firstNonBlank(stringValue(data, "url"), stringValue(data, "comment_id")),
		"source":      "github",
	}
}

func gitopsIssueTransitionResult(data map[string]any, issueID, to string) map[string]any {
	state := firstNonBlank(stringValue(data, "status"), strings.TrimSpace(to))
	return map[string]any{
		"status":      "issue_transitioned",
		"issue_id":    strings.TrimSpace(issueID),
		"id":          strings.TrimSpace(issueID),
		"issue_state": state,
		"state":       state,
		"source":      "github",
	}
}

func gitopsIssueCloseResult(data, commentData map[string]any, issueID string) map[string]any {
	state := firstNonBlank(stringValue(data, "status"), "closed")
	out := map[string]any{
		"status":      "issue_closed",
		"issue_id":    strings.TrimSpace(issueID),
		"id":          strings.TrimSpace(issueID),
		"issue_state": state,
		"state":       state,
		"source":      "github",
	}
	if commentData != nil {
		out["comment_id"] = stringValue(commentData, "comment_id")
		out["comment_url"] = firstNonBlank(stringValue(commentData, "url"), stringValue(commentData, "comment_id"))
		out["url"] = firstNonBlank(stringValue(commentData, "url"), stringValue(commentData, "comment_id"))
	}
	return out
}

func gitopsAutonomousFixCmd() *cobra.Command {
	var (
		runDir            string
		ticketRepo        string
		agentDB           string
		agentStory        string
		publicBaseURL     string
		projectRoot       string
		incidentRepo      string
		assetDir          string
		commentMode       string
		integrationBranch string
		reportInvalid     bool
		allowTestBackend  bool
		jsonOut           bool
	)
	cmd := &cobra.Command{
		Use:   "autonomous-fix",
		Short: "Run the product-journey issue-to-fix gate through native gitops",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(runDir) == "" {
				return fmt.Errorf("gitops autonomous-fix: --run-dir is required")
			}
			if strings.TrimSpace(ticketRepo) == "" {
				return fmt.Errorf("gitops autonomous-fix: --ticket-repo is required")
			}
			if strings.TrimSpace(agentDB) == "" {
				return fmt.Errorf("gitops autonomous-fix: --agent-db is required")
			}
			result, err := runGitopsAutonomousFix(cmd.Context(), gitopsAutonomousFixOptions{
				RunDir:            runDir,
				TicketRepo:        ticketRepo,
				AgentDB:           agentDB,
				AgentStory:        firstNonBlank(agentStory, "stories/bugfix"),
				PublicBaseURL:     publicBaseURL,
				ProjectRoot:       projectRoot,
				IncidentRepo:      incidentRepo,
				AssetDir:          assetDir,
				CommentMode:       firstNonBlank(commentMode, "none"),
				IntegrationBranch: integrationBranch,
				AllowTestBackend:  allowTestBackend,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetEscapeHTML(false)
				if err := enc.Encode(result); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Status: %s\n", stringValue(result, "status"))
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", stringValue(result, "filing_summary"))
				fmt.Fprintf(cmd.OutOrStdout(), "GH-agent drain: %s (%d done, %d failed, %d active)\n",
					stringValue(result, "gh_agent_drain_status"),
					intValue(result, "gh_agent_done_count"),
					intValue(result, "gh_agent_failed_count"),
					intValue(result, "gh_agent_active_count"))
				fmt.Fprintf(cmd.OutOrStdout(), "Review: %s\n", stringValue(result, "review_summary"))
				fmt.Fprintf(cmd.OutOrStdout(), "Validation: %s (%d errors, %d warnings)\n",
					stringValue(result, "validation_status"),
					intValue(result, "validation_errors"),
					intValue(result, "validation_warnings"))
			}
			if stringValue(result, "status") != "autonomous_fix_valid" && !reportInvalid {
				return fmt.Errorf("gitops autonomous-fix: %s", stringValue(result, "autonomous_gate_summary"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runDir, "run-dir", "", "product-journey run directory")
	cmd.Flags().StringVar(&ticketRepo, "ticket-repo", "", "owner/repo ticket target")
	cmd.Flags().StringVar(&agentDB, "agent-db", "", "sqlite path for durable gh-agent jobs")
	cmd.Flags().StringVar(&agentStory, "agent-story", "stories/bugfix", "story path queued for issue fixes")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "public agent base URL used for reviewable run links")
	cmd.Flags().StringVar(&projectRoot, "project-root", "", "local checkout root used by the agent for onboarded repos")
	cmd.Flags().StringVar(&incidentRepo, "incident-repo", "", "owner/repo for agent incidents; defaults to --ticket-repo")
	cmd.Flags().StringVar(&assetDir, "asset-dir", "", "root directory for agent fix evidence assets")
	cmd.Flags().StringVar(&commentMode, "comment-mode", "none", "comment mode for drained jobs: none or github")
	cmd.Flags().StringVar(&integrationBranch, "integration-branch", "", "shared branch verified job fixes squash-land onto (default: integration/qa-<run id>)")
	cmd.Flags().BoolVar(&reportInvalid, "report-invalid-autonomous-fix", false, "print invalid autonomous-fix results instead of exiting early")
	cmd.Flags().BoolVar(&allowTestBackend, "allow-test-backend", false, "allow the internal no-LLM autonomous-fix test backend when its env gate is set")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON output")
	return cmd
}

type gitopsAutonomousFixOptions struct {
	RunDir            string
	TicketRepo        string
	AgentDB           string
	AgentStory        string
	PublicBaseURL     string
	ProjectRoot       string
	IncidentRepo      string
	AssetDir          string
	CommentMode       string
	IntegrationBranch string
	AllowTestBackend  bool
}

func runGitopsAutonomousFix(ctx context.Context, opts gitopsAutonomousFixOptions) (map[string]any, error) {
	if os.Getenv("KITSOKI_GITOPS_AUTOFIX_USE_KITSOKI_BIN_FAKE") == "1" {
		if !opts.AllowTestBackend {
			return nil, fmt.Errorf("gitops autonomous-fix: test backend env is set but --allow-test-backend was not provided")
		}
		return runGitopsAutonomousFixViaRunner(ctx, opts)
	}
	runDir := strings.TrimSpace(opts.RunDir)
	assetDir := firstNonBlank(opts.AssetDir, filepath.Join(runDir, "gh-agent-assets"))
	watchdog, watchdogOK, watchdogSummary := gitopsAutonomousFixWatchdog(ctx, runDir)
	if !watchdogOK {
		return gitopsAutonomousFixWatchdogInvalid(ctx, opts, runDir, watchdog, watchdogSummary)
	}
	health, readiness, readyOK, readinessIssue, readinessSummary := gitopsAutonomousFixAgentReady(ctx, opts.PublicBaseURL, opts.TicketRepo)
	if !readyOK {
		return gitopsAutonomousFixReadinessInvalid(ctx, opts, runDir, health, readiness, readinessIssue, readinessSummary)
	}
	filed, err := host.GitHubFileFindings(ctx, host.FindingsFilingInput{
		RunDir:     runDir,
		Repo:       opts.TicketRepo,
		KitsokiRev: gitShortRevCWD(),
		FiledBy:    os.Getenv("USER"),
	})
	if err != nil {
		return nil, err
	}
	result := gitopsFilingResult(runDir, opts.TicketRepo, filed)
	gitopsMergeAutonomousWatchdogProof(result, watchdog)
	gitopsMergeGHAgentReadinessProof(result, health, readiness)
	enqueue, err := gitopsEnqueueFixes(ctx, runDir, opts.AgentDB, opts.TicketRepo, opts.AgentStory)
	if err != nil {
		return nil, err
	}
	mergeMaps(result, enqueue)
	integrationBranch := firstNonBlank(opts.IntegrationBranch, "integration/qa-"+filepath.Base(strings.TrimRight(runDir, "/")))
	drain, err := gitopsDrainFixes(ctx, opts.AgentDB, opts.TicketRepo, opts.PublicBaseURL, opts.ProjectRoot, opts.IncidentRepo, assetDir, opts.CommentMode, integrationBranch)
	if err != nil {
		return nil, err
	}
	mergeMaps(result, drain)
	mergeMaps(result, gitopsBatchReviewSummary(ctx, runDir, integrationBranch, "main"))
	if err := gitopsRecordGHAgent(runDir, result); err != nil {
		return nil, err
	}

	storySummary, _ := productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)
	filedIssueCount := len(stringSliceValue(result, "filed_issue_urls"))
	filingOK := stringValue(result, "status") == "findings_filed" &&
		filedIssueCount > 0 &&
		intValue(result, "findings_failed_count") == 0 &&
		intValue(result, "findings_unfiled_count") == 0
	ghAgentOK := gitopsGHAgentGateOK(result)
	independentVerifyOK := gitopsIndependentVerifyGateOK(result)
	closeoutOK := false
	if filingOK && ghAgentOK && independentVerifyOK {
		closeout, err := gitopsCloseoutFixedIssues(ctx, runDir, opts.TicketRepo, result)
		if err != nil {
			return nil, err
		}
		mergeMaps(result, closeout)
		closeoutOK = stringValue(closeout, "issue_closeout_status") == "closed"
	} else {
		result["issue_closeout_status"] = "skipped"
		result["issue_closeout_summary"] = "Issue close-out skipped because one or more autonomous gates failed."
		result["issue_closeout_count"] = 0
	}
	storySummary, _ = productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)
	reportPath, err := writeGitopsAutonomousReport(runDir, result, nil, nil)
	if err != nil {
		return nil, err
	}
	result["autonomous_fix_report_path"] = reportPath

	review, err := productJourneyJSON(ctx, "--review-run", "--run-dir", runDir)
	if err != nil {
		return nil, err
	}
	validation, err := productJourneyJSON(ctx, "--validate-run", "--run-dir", runDir)
	if err != nil {
		return nil, err
	}
	storySummary, _ = productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)

	reviewFailed := firstNonZero(intValue(review, "review_failed_count"), intValue(review, "failed"))
	reviewOK := stringValue(review, "review_status") == "ready" && reviewFailed == 0
	validationOK := stringValue(validation, "status") == "valid" && intValue(validation, "errors") == 0

	status := "autonomous_fix_invalid"
	if reviewOK && validationOK && filingOK && ghAgentOK && independentVerifyOK && closeoutOK {
		status = "autonomous_fix_valid"
	}
	result["status"] = status
	result["autonomous_fix_status"] = status
	result["independent_verify_status"] = passFail(independentVerifyOK)
	result["independent_verify_summary"] = gitopsIndependentVerifySummary(result)
	result["autonomous_gate_summary"] = fmt.Sprintf("filing=%s, gh_agent=%s, independent_verify=%s, review=%s, validation=%s",
		passFail(filingOK), passFail(ghAgentOK), passFail(independentVerifyOK), passFail(reviewOK), passFail(validationOK))
	result["filing_status"] = "findings_filed"
	result["review_status"] = firstNonBlank(stringValue(review, "review_status"), stringValue(review, "status"))
	result["review_summary"] = stringValue(review, "summary")
	result["review_passed_count"] = firstNonZero(intValue(review, "review_passed_count"), intValue(review, "passed"))
	result["review_failed_count"] = reviewFailed
	result["review_warning_count"] = firstNonZero(intValue(review, "review_warning_count"), intValue(review, "warnings"))
	result["review_total_count"] = firstNonZero(intValue(review, "review_total_count"), intValue(review, "total"))
	result["review_backlog_summary"] = stringValue(review, "review_backlog_summary")
	result["validation_status"] = stringValue(validation, "status")
	result["validation_errors"] = intValue(validation, "errors")
	result["validation_warnings"] = intValue(validation, "warnings")
	result["validation_issue_summary"] = stringValue(validation, "validation_issue_summary")
	result["validation_issues"] = validation["issues"]

	storySummary, _ = productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)
	reportPath, err = writeGitopsAutonomousReport(runDir, result, review, validation)
	if err != nil {
		return nil, err
	}
	result["autonomous_fix_report_path"] = reportPath
	return result, nil
}

func gitopsMergeAutonomousWatchdogProof(result, watchdog map[string]any) {
	result["autonomous_watchdog_status"] = firstNonBlank(stringValue(watchdog, "autonomous_watchdog_status"), stringValue(watchdog, "status"))
	result["autonomous_watchdog_summary"] = stringValue(watchdog, "autonomous_watchdog_summary")
	result["autonomous_watchdog_path"] = stringValue(watchdog, "autonomous_watchdog_path")
	result["autonomous_watchdog_markdown_path"] = stringValue(watchdog, "autonomous_watchdog_markdown_path")
	result["autonomous_watchdog_age_minutes"] = intValue(watchdog, "heartbeat_age_minutes")
}

func gitopsAutonomousFixWatchdog(ctx context.Context, runDir string) (map[string]any, bool, string) {
	watchdog, err := productJourneyJSON(ctx,
		"--autonomous-marathon-watchdog",
		"--run-dir", runDir,
		"--report-blocked-autonomous-watchdog",
	)
	if err != nil {
		return map[string]any{
			"autonomous_watchdog_status":  "autonomous_watchdog_blocked",
			"autonomous_watchdog_summary": err.Error(),
		}, false, err.Error()
	}
	ok := stringValue(watchdog, "autonomous_watchdog_status") == "autonomous_watchdog_ok" || stringValue(watchdog, "status") == "autonomous_watchdog_ok"
	return watchdog, ok, stringValue(watchdog, "autonomous_watchdog_summary")
}

func gitopsAutonomousFixWatchdogInvalid(ctx context.Context, opts gitopsAutonomousFixOptions, runDir string, watchdog map[string]any, summary string) (map[string]any, error) {
	result := gitopsAutonomousFixPreflightInvalidBase(opts, runDir, firstNonBlank(summary, "autonomous watchdog failed before autonomous fix"))
	result["autonomous_gate_summary"] = "watchdog=fail, filing=not_run, gh_agent=not_run, independent_verify=fail, review=not_run, validation=fail"
	result["validation_issue_summary"] = "autonomous-watchdog"
	result["autonomous_watchdog_status"] = firstNonBlank(stringValue(watchdog, "autonomous_watchdog_status"), stringValue(watchdog, "status"))
	result["autonomous_watchdog_summary"] = stringValue(watchdog, "autonomous_watchdog_summary")
	result["autonomous_watchdog_path"] = stringValue(watchdog, "autonomous_watchdog_path")
	result["autonomous_watchdog_markdown_path"] = stringValue(watchdog, "autonomous_watchdog_markdown_path")
	result["autonomous_watchdog_age_minutes"] = intValue(watchdog, "heartbeat_age_minutes")
	result["filing_summary"] = "Autonomous watchdog failed before issue filing or gh-agent repair: " + firstNonBlank(summary, stringValue(watchdog, "autonomous_watchdog_summary"))
	result["independent_verify_summary"] = result["filing_summary"]
	result["issue_closeout_summary"] = "Issue close-out skipped because autonomous watchdog failed before filing."
	result["review_summary"] = result["filing_summary"]
	result["gh_agent_missing_evidence_summary"] = result["filing_summary"]
	result["gh_agent_missing_triage_summary"] = result["filing_summary"]
	result["gh_agent_missing_verify_summary"] = result["filing_summary"]
	storySummary, _ := productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)
	reportPath, err := writeGitopsAutonomousReport(runDir, result, nil, nil)
	if err != nil {
		return nil, err
	}
	result["autonomous_fix_report_path"] = reportPath
	return result, nil
}

func gitopsMergeGHAgentReadinessProof(result, health, readiness map[string]any) {
	result["gh_agent_health_status"] = stringValue(health, "status")
	result["gh_agent_health_summary"] = stringValue(health, "summary")
	result["gh_agent_readiness_status"] = stringValue(readiness, "status")
	result["gh_agent_readiness_summary"] = stringValue(readiness, "summary")
}

func gitopsAutonomousFixAgentReady(ctx context.Context, publicBaseURL, ticketRepo string) (map[string]any, map[string]any, bool, string, string) {
	health := gitopsCheckGHAgentHealth(ctx, publicBaseURL)
	if stringValue(health, "status") != "pass" {
		return health, nil, false, "gh-agent-health", fmt.Sprintf("Hosted gh-agent health check failed before autonomous fix: %s", stringValue(health, "summary"))
	}
	readiness := gitopsCheckGHAgentReadiness(ctx, publicBaseURL, ticketRepo)
	if stringValue(readiness, "status") != "pass" {
		return health, readiness, false, "gh-agent-readiness", fmt.Sprintf("Hosted gh-agent readiness check failed before autonomous fix: %s", stringValue(readiness, "summary"))
	}
	return health, readiness, true, "", ""
}

func gitopsCheckGHAgentHealth(ctx context.Context, publicBaseURL string) map[string]any {
	url := gitopsGHAgentEndpoint(publicBaseURL, "/healthz")
	if url == "" {
		return map[string]any{
			"status":  "fail",
			"summary": "gh_agent_public_base_url is required",
			"url":     "",
		}
	}
	code, body, err := gitopsHTTPGet(ctx, url, 256)
	if err != nil {
		return map[string]any{
			"status":  "fail",
			"summary": fmt.Sprintf("%s: %v", url, err),
			"url":     url,
		}
	}
	if code == http.StatusOK && strings.TrimSpace(body) == "ok" {
		return map[string]any{
			"status":      "pass",
			"summary":     fmt.Sprintf("%s: ok", url),
			"url":         url,
			"http_status": code,
		}
	}
	return map[string]any{
		"status":      "fail",
		"summary":     fmt.Sprintf("%s: expected HTTP 200 body ok, got HTTP %d body %q", url, code, strings.TrimSpace(body)),
		"url":         url,
		"http_status": code,
	}
}

func gitopsCheckGHAgentReadiness(ctx context.Context, publicBaseURL, ticketRepo string) map[string]any {
	url := gitopsGHAgentEndpoint(publicBaseURL, "/api/ready")
	if url == "" {
		return map[string]any{
			"status":  "fail",
			"summary": "gh_agent_public_base_url is required",
			"url":     "",
		}
	}
	code, body, err := gitopsHTTPGet(ctx, url, 4096)
	if err != nil {
		return map[string]any{
			"status":  "fail",
			"summary": fmt.Sprintf("%s: %v", url, err),
			"url":     url,
		}
	}
	if code != http.StatusOK {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: expected HTTP 200, got HTTP %d", url, code),
			"url":         url,
			"http_status": code,
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: invalid JSON readiness payload (%v)", url, err),
			"url":         url,
			"http_status": code,
		}
	}
	expectedRepo := strings.TrimSpace(ticketRepo)
	actualRepo := strings.TrimSpace(stringValue(payload, "repo"))
	if stringValue(payload, "status") != "ready" {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: status=%q", url, stringValue(payload, "status")),
			"url":         url,
			"http_status": code,
		}
	}
	if expectedRepo != "" && actualRepo != expectedRepo {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: repo mismatch, expected %s, got %s", url, expectedRepo, firstNonBlank(actualRepo, "(empty)")),
			"url":         url,
			"http_status": code,
		}
	}
	if payload["drain_enabled"] != true {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: drain loop is not enabled", url),
			"url":         url,
			"http_status": code,
		}
	}
	publicBase := strings.TrimRight(strings.TrimSpace(stringValue(payload, "public_base_url")), "/")
	expectedBase := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if publicBase != "" && expectedBase != "" && publicBase != expectedBase {
		return map[string]any{
			"status":      "fail",
			"summary":     fmt.Sprintf("%s: public_base_url mismatch, expected %s, got %s", url, expectedBase, publicBase),
			"url":         url,
			"http_status": code,
		}
	}
	worker := firstNonBlank(strings.TrimSpace(stringValue(payload, "worker")), "(unknown worker)")
	return map[string]any{
		"status":      "pass",
		"summary":     fmt.Sprintf("%s: ready for %s as %s", url, firstNonBlank(actualRepo, expectedRepo), worker),
		"url":         url,
		"http_status": code,
		"payload":     payload,
	}
}

func gitopsHTTPGet(ctx context.Context, url string, maxBody int64) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(body), nil
}

func gitopsGHAgentEndpoint(publicBaseURL, path string) string {
	base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if base == "" {
		return ""
	}
	return base + path
}

func gitopsAutonomousFixReadinessInvalid(ctx context.Context, opts gitopsAutonomousFixOptions, runDir string, health, readiness map[string]any, validationIssue, summary string) (map[string]any, error) {
	result := gitopsAutonomousFixPreflightInvalidBase(opts, runDir, summary)
	result["gh_agent_health_status"] = stringValue(health, "status")
	result["gh_agent_health_summary"] = stringValue(health, "summary")
	result["gh_agent_readiness_status"] = stringValue(readiness, "status")
	result["gh_agent_readiness_summary"] = stringValue(readiness, "summary")
	result["validation_issue_summary"] = validationIssue
	result["issue_closeout_summary"] = "Issue close-out skipped because hosted gh-agent readiness failed before filing."
	storySummary, _ := productJourneyJSON(ctx, "--summarize-run", "--run-dir", runDir)
	mergeMaps(result, storySummary)
	reportPath, err := writeGitopsAutonomousReport(runDir, result, nil, nil)
	if err != nil {
		return nil, err
	}
	result["autonomous_fix_report_path"] = reportPath
	return result, nil
}

func gitopsAutonomousFixPreflightInvalidBase(opts gitopsAutonomousFixOptions, runDir, summary string) map[string]any {
	return map[string]any{
		"status":                            "autonomous_fix_invalid",
		"autonomous_fix_status":             "autonomous_fix_invalid",
		"ticket_repo":                       opts.TicketRepo,
		"gh_agent_public_base_url":          opts.PublicBaseURL,
		"filing_status":                     "not_run",
		"filing_summary":                    summary,
		"findings_filed_count":              0,
		"findings_skipped_count":            0,
		"findings_failed_count":             0,
		"findings_unfiled_count":            0,
		"filed_issue_summary":               "",
		"gh_agent_enqueue_status":           "not_run",
		"gh_agent_enqueued_count":           0,
		"gh_agent_skipped_count":            0,
		"gh_agent_claim_status":             "not_run",
		"gh_agent_claim_count":              0,
		"gh_agent_drain_status":             "not_run",
		"gh_agent_drained_count":            0,
		"gh_agent_done_count":               0,
		"gh_agent_failed_count":             0,
		"gh_agent_active_count":             0,
		"gh_agent_fix_evidence_count":       0,
		"gh_agent_missing_evidence_count":   0,
		"gh_agent_triage_evidence_count":    0,
		"gh_agent_missing_triage_count":     0,
		"gh_agent_independent_verify_count": 0,
		"gh_agent_missing_verify_count":     0,
		"gh_agent_missing_run_url_count":    0,
		"gh_agent_missing_evidence_summary": summary,
		"gh_agent_missing_triage_summary":   summary,
		"gh_agent_missing_verify_summary":   summary,
		"independent_verify_status":         "fail",
		"independent_verify_summary":        summary,
		"issue_closeout_status":             "skipped",
		"issue_closeout_count":              0,
		"issue_closeout_summary":            "Issue close-out skipped because a pre-filing autonomous gate failed.",
		"autonomous_gate_summary":           "filing=not_run, gh_agent=fail, independent_verify=fail, review=not_run, validation=fail",
		"review_status":                     "not_run",
		"review_summary":                    summary,
		"review_passed_count":               0,
		"review_failed_count":               0,
		"review_warning_count":              0,
		"review_total_count":                0,
		"validation_status":                 "invalid",
		"validation_errors":                 1,
		"validation_warnings":               0,
	}
}

func gitopsGHAgentGateOK(result map[string]any) bool {
	enqueued := intValue(result, "gh_agent_enqueued_count")
	return stringValue(result, "gh_agent_enqueue_status") == "queued" &&
		enqueued > 0 &&
		stringValue(result, "gh_agent_drain_status") == "drained" &&
		intValue(result, "gh_agent_failed_count") == 0 &&
		intValue(result, "gh_agent_active_count") == 0 &&
		intValue(result, "gh_agent_done_count") >= enqueued &&
		intValue(result, "gh_agent_fix_evidence_count") >= enqueued &&
		intValue(result, "gh_agent_triage_evidence_count") >= enqueued &&
		intValue(result, "gh_agent_independent_verify_count") >= enqueued &&
		intValue(result, "gh_agent_missing_evidence_count") == 0 &&
		intValue(result, "gh_agent_missing_triage_count") == 0 &&
		intValue(result, "gh_agent_missing_verify_count") == 0 &&
		intValue(result, "gh_agent_missing_run_url_count") == 0
}

func gitopsIndependentVerifyGateOK(result map[string]any) bool {
	enqueued := intValue(result, "gh_agent_enqueued_count")
	return stringValue(result, "gh_agent_enqueue_status") == "queued" &&
		enqueued > 0 &&
		intValue(result, "gh_agent_missing_verify_count") == 0 &&
		intValue(result, "gh_agent_independent_verify_count") >= enqueued
}

func gitopsIndependentVerifySummary(result map[string]any) string {
	enqueued := intValue(result, "gh_agent_enqueued_count")
	verified := intValue(result, "gh_agent_independent_verify_count")
	missing := intValue(result, "gh_agent_missing_verify_count")
	if enqueued == 0 {
		return "no queued fix jobs"
	}
	if missing > 0 {
		return fmt.Sprintf("missing=%d, verified=%d/%d", missing, verified, enqueued)
	}
	return fmt.Sprintf("verified=%d/%d", verified, enqueued)
}

func gitopsCloseoutFixedIssues(ctx context.Context, runDir, ticketRepo string, status map[string]any) (map[string]any, error) {
	findingsPath := filepath.Join(runDir, "findings.json")
	findings, err := gitopsReadJSONFile(findingsPath)
	if err != nil {
		return nil, err
	}
	jobsByOrigin := make(map[string]map[string]any)
	for _, job := range mapSliceValue(status, "gh_agent_drained_jobs") {
		if stringValue(job, "state") != "done" {
			continue
		}
		if origin := stringValue(job, "origin_ref"); origin != "" {
			jobsByOrigin[origin] = job
		}
	}
	var closed []map[string]any
	alreadyClosed := 0
	var failed []string
	closeoutRev := gitShortRevCWD()
	for _, item := range gitopsFindingsItems(findings) {
		if stringValue(item, "kind") != "issue" || stringValue(item, "origin") == "seeded" {
			continue
		}
		repo, number, kind, issueURL := gitopsIssueRef(item, ticketRepo)
		if repo == "" || number == "" || kind != "issue" {
			continue
		}
		origin := "github:" + repo + "/" + kind + "/" + number
		job := jobsByOrigin[origin]
		if job == nil {
			failed = append(failed, fmt.Sprintf("%s missing done gh-agent job", issueURL))
			continue
		}
		issue := mapValue(item, "github_issue")
		if existing := gitopsExistingCloseout(item, repo, number, issueURL, job); existing != nil {
			closed = append(closed, existing)
			alreadyClosed++
			continue
		}
		body := gitopsFixedInCommentBody(runDir, item, job, closeoutRev)
		comment, err := host.GitHubTicketHandler(ctx, map[string]any{
			"op":   "comment",
			"repo": repo,
			"id":   number,
			"body": body,
		})
		if err != nil {
			return nil, err
		}
		if comment.Error != "" {
			failed = append(failed, fmt.Sprintf("%s comment failed: %s", issueURL, comment.Error))
			continue
		}
		transition, err := host.GitHubTicketHandler(ctx, map[string]any{
			"op":   "transition",
			"repo": repo,
			"id":   number,
			"to":   "closed",
		})
		if err != nil {
			return nil, err
		}
		if transition.Error != "" {
			failed = append(failed, fmt.Sprintf("%s close failed: %s", issueURL, transition.Error))
			continue
		}
		commentURL := stringValue(comment.Data, "url")
		closed = append(closed, map[string]any{
			"finding_id":  stringValue(item, "id"),
			"issue_url":   issueURL,
			"repo":        repo,
			"number":      number,
			"comment_url": commentURL,
			"run_url":     stringValue(job, "run_url"),
			"job_id":      stringValue(job, "job_id"),
			"closed":      true,
		})
		if issue == nil {
			issue = map[string]any{}
			item["github_issue"] = issue
		}
		issue["repo"] = repo
		issue["number"] = number
		issue["url"] = firstNonBlank(issueURL, "https://github.com/"+repo+"/issues/"+number)
		issue["state"] = "closed"
		issue["status"] = "closed"
		issue["closed_by"] = "kitsoki gitops autonomous-fix"
		issue["fixed_by"] = closeoutRev
		issue["closeout_comment_url"] = commentURL
		issue["comments"] = append(mapSliceValue(issue, "comments"), map[string]any{
			"body": body,
			"url":  commentURL,
		})
		item["status"] = "fixed"
		item["fixed_by"] = closeoutRev
		item["fixed_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	closeoutStatus := "closed"
	if len(closed) == 0 || len(failed) > 0 {
		closeoutStatus = "failed"
	}
	summary := fmt.Sprintf("Closed %d fixed GitHub issue(s).", len(closed))
	if alreadyClosed > 0 {
		summary = fmt.Sprintf("Closed %d fixed GitHub issue(s) (%d already closed).", len(closed), alreadyClosed)
	}
	if len(failed) > 0 {
		summary = fmt.Sprintf("%s %d close-out failure(s): %s", summary, len(failed), strings.Join(firstStrings(failed, 3), "; "))
	}
	findings["issue_closeout"] = map[string]any{
		"updated_at":           time.Now().UTC().Format(time.RFC3339),
		"status":               closeoutStatus,
		"count":                len(closed),
		"already_closed_count": alreadyClosed,
		"summary":              summary,
		"items":                closed,
		"errors":               failed,
	}
	if err := gitopsWriteJSONFile(findingsPath, findings); err != nil {
		return nil, err
	}
	return map[string]any{
		"issue_closeout_status":      closeoutStatus,
		"issue_closeout_count":       len(closed),
		"issue_already_closed_count": alreadyClosed,
		"issue_closeout_summary":     summary,
		"closed_issue_urls":          closeoutIssueURLs(closed),
		"issue_closeouts":            closed,
		"issue_closeout_errors":      failed,
	}, nil
}

func gitopsExistingCloseout(item map[string]any, repo, number, issueURL string, job map[string]any) map[string]any {
	issue := mapValue(item, "github_issue")
	if issue == nil {
		return nil
	}
	if stringValue(issue, "closeout_comment_url") == "" {
		return nil
	}
	state := firstNonBlank(stringValue(issue, "state"), stringValue(issue, "status"))
	if state != "closed" {
		return nil
	}
	return map[string]any{
		"finding_id":     stringValue(item, "id"),
		"issue_url":      firstNonBlank(issueURL, stringValue(issue, "url"), "https://github.com/"+repo+"/issues/"+number),
		"repo":           repo,
		"number":         number,
		"comment_url":    stringValue(issue, "closeout_comment_url"),
		"run_url":        stringValue(job, "run_url"),
		"job_id":         stringValue(job, "job_id"),
		"closed":         true,
		"already_closed": true,
	}
}

func gitopsFixedInCommentBody(runDir string, finding, job map[string]any, closeoutRev string) string {
	issue := mapValue(finding, "github_issue")
	issueURL := stringValue(issue, "url")
	runURL := stringValue(job, "run_url")
	evidence := gitopsJobEvidenceLinks(job)
	verify := gitopsJobIndependentVerifyLinks(job)
	var lines []string
	lines = append(lines,
		"Fixed by the Kitsoki autonomous bugfix pipeline.",
		"",
		fmt.Sprintf("- Finding: `%s` - %s", firstNonBlank(stringValue(finding, "id"), "finding"), stringValue(finding, "title")),
		fmt.Sprintf("- Run: %s", firstNonBlank(runURL, "(missing run URL)")),
		fmt.Sprintf("- Close-out rev: `%s`", closeoutRev),
	)
	if len(evidence) > 0 {
		lines = append(lines, "- Evidence:")
		for _, link := range evidence {
			lines = append(lines, "  - "+link)
		}
	}
	if len(verify) > 0 {
		lines = append(lines, "- Independent verification:")
		for _, link := range verify {
			lines = append(lines, "  - "+link)
		}
	}
	lines = append(lines,
		"",
		"<!-- kitsoki-fixed-in",
		"issue: "+issueURL,
		"run: "+runURL,
		"job: "+stringValue(job, "job_id"),
		"closeout_rev: "+closeoutRev,
		"evidence:",
	)
	for _, link := range evidence {
		lines = append(lines, "  - "+link)
	}
	lines = append(lines, "verified:")
	for _, link := range verify {
		lines = append(lines, "  - "+link)
	}
	lines = append(lines, "-->")
	return strings.Join(lines, "\n")
}

func gitopsJobIndependentVerifyLinks(job map[string]any) []string {
	var links []string
	for _, asset := range mapSliceValue(job, "assets") {
		name := strings.ToLower(firstNonBlank(stringValue(asset, "name"), stringValue(asset, "path"), stringValue(asset, "url")))
		if strings.Contains(name, "independent") && strings.Contains(name, "verify") {
			for _, key := range []string{"url", "href", "path"} {
				if value := stringValue(asset, key); value != "" {
					links = append(links, value)
					break
				}
			}
		}
	}
	return links
}

func closeoutIssueURLs(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if url := stringValue(item, "issue_url"); url != "" {
			out = append(out, url)
		}
	}
	return out
}

func runGitopsAutonomousFixViaRunner(ctx context.Context, opts gitopsAutonomousFixOptions) (map[string]any, error) {
	args := []string{
		"tools/product-journey/run.py",
		"--autonomous-fix-loop",
		"--run-dir", opts.RunDir,
		"--ticket-repo", opts.TicketRepo,
		"--gh-agent-db", opts.AgentDB,
		"--gh-agent-story", firstNonBlank(opts.AgentStory, "stories/bugfix"),
		"--gh-agent-public-base-url", opts.PublicBaseURL,
		"--gh-agent-project-root", opts.ProjectRoot,
		"--gh-agent-incident-repo", opts.IncidentRepo,
		"--gh-agent-asset-dir", opts.AssetDir,
		"--gh-agent-comment-mode", firstNonBlank(opts.CommentMode, "none"),
		"--report-invalid-autonomous-fix",
		"--json-output",
	}
	py := exec.CommandContext(ctx, "python3", args...)
	py.Dir = "."
	py.Env = os.Environ()
	out, err := py.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("product-journey autonomous-fix test fallback failed: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("product-journey autonomous-fix test fallback printed invalid JSON: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	independentVerifyOK := gitopsIndependentVerifyGateOK(result)
	if stringValue(result, "filing_status") == "findings_filed" && gitopsGHAgentGateOK(result) && independentVerifyOK {
		closeout, err := gitopsCloseoutFixedIssuesFake(opts.RunDir, opts.TicketRepo, result)
		if err != nil {
			return nil, err
		}
		mergeMaps(result, closeout)
		watchdog, watchdogOK, _ := gitopsAutonomousFixWatchdog(ctx, opts.RunDir)
		if watchdogOK {
			gitopsMergeAutonomousWatchdogProof(result, watchdog)
		}
		if opts.PublicBaseURL != "" {
			gitopsMergeGHAgentReadinessProof(result,
				map[string]any{
					"status":  "pass",
					"summary": fmt.Sprintf("%s: ok", gitopsGHAgentEndpoint(opts.PublicBaseURL, "/healthz")),
				},
				map[string]any{
					"status":  "pass",
					"summary": fmt.Sprintf("%s: ready for %s as fake-worker", gitopsGHAgentEndpoint(opts.PublicBaseURL, "/api/ready"), opts.TicketRepo),
				},
			)
		}
		storySummary, _ := productJourneyJSON(ctx, "--summarize-run", "--run-dir", opts.RunDir)
		mergeMaps(result, storySummary)
		reportPath, err := writeGitopsAutonomousReport(opts.RunDir, result, nil, nil)
		if err != nil {
			return nil, err
		}
		result["autonomous_fix_report_path"] = reportPath
		review, err := productJourneyJSON(ctx, "--review-run", "--run-dir", opts.RunDir)
		if err != nil {
			return nil, err
		}
		validation, err := productJourneyJSON(ctx, "--validate-run", "--run-dir", opts.RunDir)
		if err != nil {
			return nil, err
		}
		storySummary, _ = productJourneyJSON(ctx, "--summarize-run", "--run-dir", opts.RunDir)
		mergeMaps(result, storySummary)
		reviewFailed := firstNonZero(intValue(review, "review_failed_count"), intValue(review, "failed"))
		reviewOK := stringValue(review, "review_status") == "ready" && reviewFailed == 0
		validationOK := stringValue(validation, "status") == "valid" && intValue(validation, "errors") == 0
		closeoutOK := stringValue(closeout, "issue_closeout_status") == "closed"
		status := "autonomous_fix_invalid"
		if reviewOK && validationOK && independentVerifyOK && closeoutOK {
			status = "autonomous_fix_valid"
		}
		result["status"] = status
		result["autonomous_fix_status"] = status
		result["independent_verify_status"] = passFail(independentVerifyOK)
		result["independent_verify_summary"] = gitopsIndependentVerifySummary(result)
		result["autonomous_gate_summary"] = fmt.Sprintf("filing=pass, gh_agent=pass, independent_verify=%s, review=%s, validation=%s",
			passFail(independentVerifyOK), passFail(reviewOK), passFail(validationOK))
		result["review_status"] = firstNonBlank(stringValue(review, "review_status"), stringValue(review, "status"))
		result["review_summary"] = stringValue(review, "summary")
		result["review_passed_count"] = firstNonZero(intValue(review, "review_passed_count"), intValue(review, "passed"))
		result["review_failed_count"] = reviewFailed
		result["review_warning_count"] = firstNonZero(intValue(review, "review_warning_count"), intValue(review, "warnings"))
		result["review_total_count"] = firstNonZero(intValue(review, "review_total_count"), intValue(review, "total"))
		result["review_backlog_summary"] = stringValue(review, "review_backlog_summary")
		result["validation_status"] = stringValue(validation, "status")
		result["validation_errors"] = intValue(validation, "errors")
		result["validation_warnings"] = intValue(validation, "warnings")
		result["validation_issue_summary"] = stringValue(validation, "validation_issue_summary")
		result["validation_issues"] = validation["issues"]
		reportPath, err = writeGitopsAutonomousReport(opts.RunDir, result, review, validation)
		if err != nil {
			return nil, err
		}
		result["autonomous_fix_report_path"] = reportPath
	}
	return result, nil
}

func gitopsCloseoutFixedIssuesFake(runDir, ticketRepo string, status map[string]any) (map[string]any, error) {
	findingsPath := filepath.Join(runDir, "findings.json")
	findings, err := gitopsReadJSONFile(findingsPath)
	if err != nil {
		return nil, err
	}
	jobsByOrigin := make(map[string]map[string]any)
	for _, job := range mapSliceValue(status, "gh_agent_drained_jobs") {
		if stringValue(job, "state") != "done" {
			continue
		}
		if origin := stringValue(job, "origin_ref"); origin != "" {
			jobsByOrigin[origin] = job
		}
	}
	var closed []map[string]any
	var failed []string
	closeoutRev := gitShortRevCWD()
	for _, item := range gitopsFindingsItems(findings) {
		if stringValue(item, "kind") != "issue" || stringValue(item, "origin") == "seeded" {
			continue
		}
		repo, number, kind, issueURL := gitopsIssueRef(item, ticketRepo)
		if repo == "" || number == "" || kind != "issue" {
			continue
		}
		origin := "github:" + repo + "/" + kind + "/" + number
		job := jobsByOrigin[origin]
		if job == nil {
			failed = append(failed, fmt.Sprintf("%s missing done gh-agent job", issueURL))
			continue
		}
		commentURL := firstNonBlank(issueURL, "https://github.com/"+repo+"/issues/"+number) + "#issuecomment-kitsoki-fixed-in"
		closed = append(closed, map[string]any{
			"finding_id":  stringValue(item, "id"),
			"issue_url":   firstNonBlank(issueURL, "https://github.com/"+repo+"/issues/"+number),
			"repo":        repo,
			"number":      number,
			"comment_url": commentURL,
			"run_url":     stringValue(job, "run_url"),
			"job_id":      stringValue(job, "job_id"),
			"closed":      true,
		})
		issue := mapValue(item, "github_issue")
		if issue == nil {
			issue = map[string]any{}
			item["github_issue"] = issue
		}
		issue["repo"] = repo
		issue["number"] = number
		issue["url"] = firstNonBlank(issueURL, "https://github.com/"+repo+"/issues/"+number)
		issue["state"] = "closed"
		issue["status"] = "closed"
		issue["closed_by"] = "kitsoki gitops autonomous-fix"
		issue["fixed_by"] = closeoutRev
		issue["closeout_comment_url"] = commentURL
		issue["comments"] = append(mapSliceValue(issue, "comments"), map[string]any{
			"body": gitopsFixedInCommentBody(runDir, item, job, closeoutRev),
			"url":  commentURL,
		})
		item["status"] = "fixed"
		item["fixed_by"] = closeoutRev
		item["fixed_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	closeoutStatus := "closed"
	if len(closed) == 0 || len(failed) > 0 {
		closeoutStatus = "failed"
	}
	summary := fmt.Sprintf("Closed %d fixed GitHub issue(s).", len(closed))
	if len(failed) > 0 {
		summary = fmt.Sprintf("%s %d close-out failure(s): %s", summary, len(failed), strings.Join(firstStrings(failed, 3), "; "))
	}
	findings["issue_closeout"] = map[string]any{
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"status":     closeoutStatus,
		"count":      len(closed),
		"summary":    summary,
		"items":      closed,
		"errors":     failed,
	}
	if err := gitopsWriteJSONFile(findingsPath, findings); err != nil {
		return nil, err
	}
	return map[string]any{
		"issue_closeout_status":  closeoutStatus,
		"issue_closeout_count":   len(closed),
		"issue_closeout_summary": summary,
		"closed_issue_urls":      closeoutIssueURLs(closed),
		"issue_closeouts":        closed,
		"issue_closeout_errors":  failed,
	}, nil
}

func gitopsFilingResult(runDir, ticketRepo string, filed host.FindingsFilingResult) map[string]any {
	filedURLs := make([]string, 0, len(filed.Outcomes))
	for _, out := range filed.Outcomes {
		if out.IssueURL != "" {
			filedURLs = append(filedURLs, out.IssueURL)
		}
	}
	unfiled := 0
	for _, item := range gitopsCredibleFindings(runDir) {
		if stringValue(mapValue(item, "github_issue"), "url") == "" {
			unfiled++
		}
	}
	summary := fmt.Sprintf("Filed findings to %s: %d filed, %d already filed, %d failed; %d credible finding(s) remain unfiled.",
		ticketRepo, filed.Filed, filed.Skipped, filed.Failed, unfiled)
	return map[string]any{
		"status":                 filed.Status,
		"run_dir":                runDir,
		"ticket_repo":            ticketRepo,
		"dry_run":                "no",
		"findings_filed_count":   filed.Filed,
		"findings_skipped_count": filed.Skipped,
		"findings_failed_count":  filed.Failed,
		"findings_unfiled_count": unfiled,
		"filed_issue_urls":       filedURLs,
		"filed_issue_summary":    strings.Join(firstStrings(filedURLs, 5), "; "),
		"filing_summary":         summary,
		"outcomes":               filed.Outcomes,
		"deck_path":              filepath.Join(runDir, "deck.slidey.json"),
		"execution_plan_path":    filepath.Join(runDir, "execution-plan.md"),
		"driver_plan_path":       filepath.Join(runDir, "driver-plan.md"),
		"driver_journal_path":    filepath.Join(runDir, "driver-journal.md"),
		"agent_brief_path":       filepath.Join(runDir, "agent-brief.md"),
		"driver_handoff_path":    filepath.Join(runDir, "driver-handoff.md"),
		"media_manifest_path":    filepath.Join(runDir, "media-manifest.json"),
		"scenario_outcomes_path": filepath.Join(runDir, "scenario-outcomes.md"),
	}
}

func gitopsEnqueueFixes(ctx context.Context, runDir, dbPath, fallbackRepo, story string) (map[string]any, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("gitops autonomous-fix: open db %q: %w", dbPath, err)
	}
	defer db.Close()
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		return nil, err
	}
	findingsPath := filepath.Join(runDir, "findings.json")
	findings, err := gitopsReadJSONFile(findingsPath)
	if err != nil {
		return nil, err
	}
	var jobsOut []map[string]any
	var claims []map[string]any
	skipped := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, item := range gitopsCredibleFindingsFrom(findings) {
		repo, number, kind, issueURL := gitopsIssueRef(item, fallbackRepo)
		if repo == "" || number == "" {
			skipped++
			continue
		}
		job, created, err := store.Enqueue(ctx, jobs.GHMention{
			OriginRef:    "github:" + repo + "/" + kind + "/" + number,
			Repo:         repo,
			ObjectKind:   kind,
			ObjectNumber: number,
		}, firstNonBlank(story, "stories/bugfix"))
		if err != nil {
			return nil, err
		}
		jobMetadata := gitopsFindingJobMetadata(runDir, item, issueURL)
		if err := store.MergeMetadata(ctx, job.JobID, jobMetadata); err != nil {
			return nil, err
		}
		if len(jobMetadata) > 0 {
			if refreshed, err := store.GetJob(ctx, job.JobID); err == nil {
				job = refreshed
			} else {
				return nil, err
			}
		}
		issue := mapValue(item, "github_issue")
		if issue == nil {
			issue = map[string]any{}
			item["github_issue"] = issue
		}
		claimURL := stringValue(issue, "claim_comment_url")
		if claimURL == "" {
			body := gitopsClaimCommentBody(item, job, issueURL)
			claim, err := host.GitHubTicketHandler(ctx, map[string]any{
				"op":   "comment",
				"repo": repo,
				"id":   number,
				"body": body,
			})
			if err != nil {
				return nil, err
			}
			if claim.Error != "" {
				return nil, fmt.Errorf("gitops autonomous-fix: claim %s: %s", firstNonBlank(issueURL, repo+"#"+number), claim.Error)
			}
			claimURL = stringValue(claim.Data, "url")
			issue["claim_comment_url"] = claimURL
			issue["claim_job_id"] = job.JobID
			issue["claimed_by"] = "kitsoki gitops autonomous-fix"
			issue["claimed_at"] = now
			issue["comments"] = append(mapSliceValue(issue, "comments"), map[string]any{
				"body": body,
				"url":  claimURL,
			})
		}
		jobOut := map[string]any{
			"status":         "queued",
			"created":        created,
			"job_id":         job.JobID,
			"origin_ref":     job.OriginRef,
			"repo":           job.Repo,
			"object_kind":    job.ObjectKind,
			"object_number":  job.ObjectNumber,
			"story":          job.Story,
			"state":          job.State,
			"issue_url":      issueURL,
			"finding_id":     stringValue(item, "id"),
			"claim_url":      claimURL,
			"triage_context": len(job.Metadata) > 0,
		}
		jobsOut = append(jobsOut, jobOut)
		claims = append(claims, map[string]any{
			"finding_id":  stringValue(item, "id"),
			"issue_url":   issueURL,
			"repo":        repo,
			"number":      number,
			"job_id":      job.JobID,
			"comment_url": claimURL,
		})
	}
	if len(jobsOut) > 0 {
		if err := gitopsWriteJSONFile(findingsPath, findings); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"gh_agent_enqueue_status": "queued",
		"gh_agent_enqueued_count": len(jobsOut),
		"gh_agent_skipped_count":  skipped,
		"gh_agent_job_summary":    originSummary(jobsOut),
		"gh_agent_jobs":           jobsOut,
		"gh_agent_claim_status":   "claimed",
		"gh_agent_claim_count":    len(claims),
		"gh_agent_claims":         claims,
	}, nil
}

func gitopsDrainFixes(ctx context.Context, dbPath, repo, publicBaseURL, projectRoot, incidentRepo, assetDir, commentMode, integrationBranch string) (map[string]any, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("gitops autonomous-fix: open db %q: %w", dbPath, err)
	}
	defer db.Close()
	store, err := jobs.NewGHJobStore(db)
	if err != nil {
		return nil, err
	}
	store.DataDir = ghAgentDrainAssetDir(dbPath, assetDir)
	opts := ghAgentServeOptions{
		Repo:              repo,
		PublicBaseURL:     publicBaseURL,
		Worker:            "gh-agent-1",
		IncidentRepo:      incidentRepo,
		ProjectRoot:       projectRoot,
		CommentMode:       commentMode,
		IntegrationBranch: integrationBranch,
	}
	drained, err := drainQueuedGHAgentJobs(ctx, store, opts)
	if err != nil {
		return nil, err
	}
	raw := ghAgentDrainResult(ctx, store, drained, publicBaseURL)
	jobsOut := mapSliceValue(raw, "jobs")
	return map[string]any{
		"gh_agent_drain_status":  stringValue(raw, "status"),
		"gh_agent_drained_count": intValue(raw, "drained_count"),
		"gh_agent_done_count":    intValue(raw, "done_count"),
		"gh_agent_failed_count":  intValue(raw, "failed_count"),
		"gh_agent_active_count":  intValue(raw, "active_count"),
		"gh_agent_run_summary":   runSummary(jobsOut),
		"gh_agent_drained_jobs":  jobsOut,
	}, nil
}

// gitopsBatchReviewSummary renders the one human review surface the
// autonomous pipeline's operating model wants (.context/autonomous-product-
// journey-pipeline-howto.md's Gap 3): a single deterministic diff of
// integrationBranch vs baseBranch, written to <run-dir>/batch-review.md,
// instead of walking every fix's per-job diff individually. A no-op (status
// "not_applicable") when integrationBranch never landed a real commit this
// cycle — replay-mode cycles and cycles with zero live fixes both leave the
// branch unborn, per internal/ghagent/realdispatch.go's landFeatureBranch.
func gitopsBatchReviewSummary(ctx context.Context, runDir, integrationBranch, baseBranch string) map[string]any {
	root := gitopsIntegrationRoot()
	if _, exit, _ := vcsops.RunGit(ctx, root, "rev-parse", "--verify", "--quiet", integrationBranch); exit != 0 {
		return map[string]any{
			"batch_review_status":  "not_applicable",
			"batch_review_summary": fmt.Sprintf("No commits landed on %q this cycle — nothing to review.", integrationBranch),
		}
	}
	commitRange := baseBranch + ".." + integrationBranch
	count, _, _ := vcsops.RunGit(ctx, root, "rev-list", "--count", commitRange)
	commitLog, _, _ := vcsops.RunGit(ctx, root, "log", "--format=- %h %s", commitRange)
	stat, _, _ := vcsops.RunGit(ctx, root, "diff", "--stat", baseBranch+"..."+integrationBranch)
	diff, _, _ := vcsops.RunGit(ctx, root, "diff", baseBranch+"..."+integrationBranch)

	var b strings.Builder
	fmt.Fprintf(&b, "# Batch Review — %s vs %s\n\n", integrationBranch, baseBranch)
	fmt.Fprintf(&b, "%s fix commit(s) landed this cycle.\n\n", strings.TrimSpace(count))
	fmt.Fprintf(&b, "## Commits\n\n%s\n\n", firstNonBlank(strings.TrimSpace(commitLog), "(none)"))
	fmt.Fprintf(&b, "## Diffstat\n\n```\n%s\n```\n\n", strings.TrimSpace(stat))
	fmt.Fprintf(&b, "## Full diff\n\n```diff\n%s\n```\n", strings.TrimSpace(diff))
	path := filepath.Join(runDir, "batch-review.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return map[string]any{"batch_review_status": "error", "batch_review_summary": err.Error()}
	}
	return map[string]any{
		"batch_review_status":  "ready",
		"batch_review_summary": fmt.Sprintf("%s vs %s: %s commit(s) — see batch-review.md", integrationBranch, baseBranch, strings.TrimSpace(count)),
		"batch_review_path":    path,
		"batch_review_branch":  integrationBranch,
		"batch_review_base":    baseBranch,
		"batch_review_commits": strings.TrimSpace(count),
	}
}

// gitopsIntegrationRoot resolves the checkout where a real-dispatch job lands
// its integration branch: KITSOKI_REPO when set (mirrors internal/ghagent's
// repoRoot override for onboarded external projects), else the local
// kitsoki checkout gitopsRepoRoot() already resolves for the python shell-outs.
func gitopsIntegrationRoot() string {
	if envRoot := strings.TrimSpace(os.Getenv("KITSOKI_REPO")); envRoot != "" {
		if _, err := os.Stat(filepath.Join(envRoot, "go.mod")); err == nil {
			return envRoot
		}
	}
	return gitopsRepoRoot()
}

func productJourneyJSON(ctx context.Context, args ...string) (map[string]any, error) {
	all := append([]string{"tools/product-journey/run.py"}, args...)
	all = append(all, "--json-output")
	py := exec.CommandContext(ctx, "python3", all...)
	py.Dir = gitopsRepoRoot()
	py.Env = os.Environ()
	out, err := py.CombinedOutput()
	var result map[string]any
	if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
		if err != nil {
			return nil, fmt.Errorf("product-journey %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil, fmt.Errorf("product-journey %s printed invalid JSON: %w\n%s", strings.Join(args, " "), jsonErr, strings.TrimSpace(string(out)))
	}
	return result, nil
}

func gitopsRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for dir := wd; dir != "" && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "tools", "product-journey", "run.py")); err == nil {
			return dir
		}
	}
	return "."
}

func gitopsRecordGHAgent(runDir string, status map[string]any) error {
	path := filepath.Join(runDir, "findings.json")
	findings, err := gitopsReadJSONFile(path)
	if err != nil {
		return err
	}
	findings["gh_agent"] = map[string]any{
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
		"enqueue_status": stringValue(status, "gh_agent_enqueue_status"),
		"enqueued_count": intValue(status, "gh_agent_enqueued_count"),
		"skipped_count":  intValue(status, "gh_agent_skipped_count"),
		"job_summary":    stringValue(status, "gh_agent_job_summary"),
		"jobs":           status["gh_agent_jobs"],
		"claim_status":   stringValue(status, "gh_agent_claim_status"),
		"claim_count":    intValue(status, "gh_agent_claim_count"),
		"claims":         status["gh_agent_claims"],
		"drain_status":   stringValue(status, "gh_agent_drain_status"),
		"drained_count":  intValue(status, "gh_agent_drained_count"),
		"done_count":     intValue(status, "gh_agent_done_count"),
		"failed_count":   intValue(status, "gh_agent_failed_count"),
		"active_count":   intValue(status, "gh_agent_active_count"),
		"run_summary":    stringValue(status, "gh_agent_run_summary"),
		"drained_jobs":   status["gh_agent_drained_jobs"],
	}
	return gitopsWriteJSONFile(path, findings)
}

func writeGitopsAutonomousReport(runDir string, status, review, validation map[string]any) (string, error) {
	findings, _ := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	var lines []string
	lines = append(lines,
		"# Autonomous Fix Report",
		"",
		fmt.Sprintf("- Run: `%s`", filepath.Base(runDir)),
		fmt.Sprintf("- Ticket repo: `%s`", stringValue(status, "ticket_repo")),
		fmt.Sprintf("- Status: `%s`", firstNonBlank(stringValue(status, "autonomous_fix_status"), stringValue(status, "status"))),
		fmt.Sprintf("- Gates: %s", firstNonBlank(stringValue(status, "autonomous_gate_summary"), "(not evaluated)")),
		fmt.Sprintf("- Independent verification: `%s` - %s", stringValue(status, "independent_verify_status"), stringValue(status, "independent_verify_summary")),
		fmt.Sprintf("- Issue close-out: `%s` (%d closed)", stringValue(status, "issue_closeout_status"), intValue(status, "issue_closeout_count")),
		"",
		"## Autonomous Watchdog",
		"",
		fmt.Sprintf("- Status: `%s` - %s", firstNonBlank(stringValue(status, "autonomous_watchdog_status"), "(not checked)"), stringValue(status, "autonomous_watchdog_summary")),
		fmt.Sprintf("- Age: %d minute(s)", intValue(status, "autonomous_watchdog_age_minutes")),
		fmt.Sprintf("- Report: %s", firstNonBlank(stringValue(status, "autonomous_watchdog_markdown_path"), "(not generated)")),
		"",
		"## Hosted GH-agent",
		"",
		fmt.Sprintf("- Health: `%s` - %s", firstNonBlank(stringValue(status, "gh_agent_health_status"), "(not checked)"), stringValue(status, "gh_agent_health_summary")),
		fmt.Sprintf("- Readiness: `%s` - %s", firstNonBlank(stringValue(status, "gh_agent_readiness_status"), "(not checked)"), stringValue(status, "gh_agent_readiness_summary")),
		"",
		"## Filed Issues",
		"",
	)
	issueLines := 0
	for _, item := range gitopsFindingsItems(findings) {
		issue := mapValue(item, "github_issue")
		if issueURL := stringValue(issue, "url"); issueURL != "" {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", firstNonBlank(stringValue(item, "id"), stringValue(item, "title"), "finding"), issueURL))
			for _, asset := range gitopsIssueEvidenceAssets(issue) {
				lines = append(lines, fmt.Sprintf("  - evidence `%s`: %s", firstNonBlank(stringValue(asset, "name"), "evidence"), stringValue(asset, "url")))
			}
			issueLines++
		}
	}
	if issueLines == 0 {
		lines = append(lines, "- (none)")
	}
	lines = append(lines,
		"",
		"## GH-agent Runs",
		"",
		fmt.Sprintf("- Queue: `%s` (enqueued %d, skipped %d)", stringValue(status, "gh_agent_enqueue_status"), intValue(status, "gh_agent_enqueued_count"), intValue(status, "gh_agent_skipped_count")),
		fmt.Sprintf("- Claims: `%s` (%d claimed)", stringValue(status, "gh_agent_claim_status"), intValue(status, "gh_agent_claim_count")),
		fmt.Sprintf("- Drain: `%s` (drained %d, done %d, failed %d, active %d)", stringValue(status, "gh_agent_drain_status"), intValue(status, "gh_agent_drained_count"), intValue(status, "gh_agent_done_count"), intValue(status, "gh_agent_failed_count"), intValue(status, "gh_agent_active_count")),
		"",
		"## Batch Review",
		"",
		fmt.Sprintf("- Status: `%s` - %s", firstNonBlank(stringValue(status, "batch_review_status"), "(not evaluated)"), stringValue(status, "batch_review_summary")),
		fmt.Sprintf("- Diff: %s", firstNonBlank(stringValue(status, "batch_review_path"), "(none)")),
		"",
	)
	claimsOut := mapSliceValue(status, "gh_agent_claims")
	if len(claimsOut) > 0 {
		lines = append(lines,
			"### Claims",
			"",
		)
		for _, claim := range claimsOut {
			lines = append(lines, fmt.Sprintf("- %s — %s", firstNonBlank(stringValue(claim, "issue_url"), stringValue(claim, "repo")+"#"+stringValue(claim, "number")), firstNonBlank(stringValue(claim, "comment_url"), "(missing)")))
		}
		lines = append(lines, "")
	}
	jobsOut := mapSliceValue(status, "gh_agent_drained_jobs")
	if len(jobsOut) == 0 {
		lines = append(lines, "- (none)")
	} else {
		for _, job := range jobsOut {
			lines = append(lines,
				fmt.Sprintf("### %s", firstNonBlank(stringValue(job, "origin_ref"), stringValue(job, "job_id"), "unknown")),
				"",
				fmt.Sprintf("- State: `%s`", stringValue(job, "state")),
				fmt.Sprintf("- Run URL: %s", firstNonBlank(stringValue(job, "run_url"), "(missing)")),
				fmt.Sprintf("- Integration branch: `%s`", firstNonBlank(stringValue(job, "integration_branch"), "(missing)")),
				fmt.Sprintf("- Commit: `%s`", firstNonBlank(stringValue(job, "commit_sha"), "(missing)")),
			)
			if commitURL := stringValue(job, "commit_url"); commitURL != "" {
				lines = append(lines, "- Commit URL: "+commitURL)
			}
			if incident := stringValue(job, "incident_url"); incident != "" {
				lines = append(lines, "- Incident: "+incident)
			}
			if errMsg := stringValue(job, "err_msg"); errMsg != "" {
				lines = append(lines, "- Error: "+errMsg)
			}
			lines = append(lines, "- Evidence:")
			links := gitopsJobEvidenceLinks(job)
			if len(links) == 0 {
				lines = append(lines, "  - (missing)")
			} else {
				for _, link := range links {
					lines = append(lines, "  - "+link)
				}
			}
			lines = append(lines, "")
		}
	}
	lines = append(lines,
		"## Issue Close-out",
		"",
		fmt.Sprintf("- Status: `%s`", stringValue(status, "issue_closeout_status")),
		fmt.Sprintf("- Summary: %s", firstNonBlank(stringValue(status, "issue_closeout_summary"), "(not run)")),
		"",
	)
	for _, item := range mapSliceValue(status, "issue_closeouts") {
		lines = append(lines, fmt.Sprintf("- %s — %s", stringValue(item, "issue_url"), stringValue(item, "comment_url")))
	}
	if intValue(status, "issue_closeout_count") == 0 {
		lines = append(lines, "- (none)")
	}
	if errors := stringSliceValue(status, "issue_closeout_errors"); len(errors) > 0 {
		lines = append(lines, "", "Errors:")
		for _, item := range errors {
			lines = append(lines, "- "+item)
		}
	}
	lines = append(lines,
		"",
		"## Review",
		"",
		fmt.Sprintf("- Status: `%s`", firstNonBlank(stringValue(review, "review_status"), stringValue(review, "status"), stringValue(status, "review_status"))),
		fmt.Sprintf("- Summary: %s", firstNonBlank(stringValue(review, "summary"), stringValue(status, "review_summary"))),
		"",
		"## Validation",
		"",
		fmt.Sprintf("- Status: `%s`", firstNonBlank(stringValue(validation, "status"), stringValue(status, "validation_status"))),
		fmt.Sprintf("- Issues: %s", firstNonBlank(stringValue(validation, "validation_issue_summary"), stringValue(status, "validation_issue_summary"), "(none)")),
		"",
	)
	path := filepath.Join(runDir, "autonomous-fix-report.md")
	return path, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func gitopsIssueEvidenceAssets(issue map[string]any) []map[string]any {
	var out []map[string]any
	seen := map[string]bool{}
	for _, asset := range mapSliceValue(issue, "evidence_assets") {
		url := stringValue(asset, "url")
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		out = append(out, asset)
	}
	return out
}

func gitopsCredibleFindings(runDir string) []map[string]any {
	findings, err := gitopsReadJSONFile(filepath.Join(runDir, "findings.json"))
	if err != nil {
		return nil
	}
	return gitopsCredibleFindingsFrom(findings)
}

func gitopsCredibleFindingsFrom(findings map[string]any) []map[string]any {
	var out []map[string]any
	for _, item := range gitopsFindingsItems(findings) {
		if stringValue(item, "kind") != "issue" || stringValue(item, "status") == "blocked" || stringValue(item, "origin") == "seeded" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func gitopsClaimCommentBody(finding map[string]any, job *jobs.GHJob, issueURL string) string {
	lines := []string{
		"Claimed by the Kitsoki autonomous bugfix pipeline.",
		"",
		fmt.Sprintf("- Finding: `%s` - %s", firstNonBlank(stringValue(finding, "id"), "finding"), stringValue(finding, "title")),
		fmt.Sprintf("- Job: `%s`", job.JobID),
		fmt.Sprintf("- Story: `%s`", job.Story),
		"",
		"<!-- kitsoki-autofix-claim",
		"issue: " + issueURL,
		"job: " + job.JobID,
		"origin: " + job.OriginRef,
		"finding: " + stringValue(finding, "id"),
		"-->",
	}
	return strings.Join(lines, "\n")
}

func gitopsFindingJobMetadata(runDir string, finding map[string]any, issueURL string) map[string]string {
	title := firstNonBlank(stringValue(finding, "title"), stringValue(finding, "id"), "product-journey finding")
	bodyLines := []string{
		"# Product-journey finding",
		"",
		fmt.Sprintf("- Finding: `%s`", firstNonBlank(stringValue(finding, "id"), "(unknown)")),
		fmt.Sprintf("- Title: %s", title),
	}
	for _, key := range []string{"summary", "scenario", "persona", "severity", "evidence_path"} {
		if value := stringValue(finding, key); value != "" {
			bodyLines = append(bodyLines, fmt.Sprintf("- %s: %s", key, value))
		}
	}
	if issueURL != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("- GitHub issue: %s", issueURL))
	}
	if runDir != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("- Product-journey run: %s", runDir))
	}
	if summary := stringValue(finding, "summary"); summary != "" {
		bodyLines = append(bodyLines, "", "## Summary", "", summary)
	}
	if evidencePath := stringValue(finding, "evidence_path"); evidencePath != "" {
		bodyLines = append(bodyLines, "", "## Evidence", "", evidencePath)
	}
	out := map[string]string{
		"ticket_title":       title,
		"ticket_body":        strings.Join(bodyLines, "\n"),
		"ticket_source_mode": "remote",
	}
	if issueURL != "" {
		out["ticket_source_ref"] = issueURL
	}
	return out
}

func gitopsFindingsItems(findings map[string]any) []map[string]any {
	rows, _ := findings["items"].([]any)
	var out []map[string]any
	for _, row := range rows {
		if item, ok := row.(map[string]any); ok {
			out = append(out, item)
		}
	}
	return out
}

func gitopsIssueRef(item map[string]any, fallbackRepo string) (string, string, string, string) {
	issue := mapValue(item, "github_issue")
	issueURL := stringValue(issue, "url")
	repo := firstNonBlank(stringValue(issue, "repo"), fallbackRepo)
	number := stringValue(issue, "number")
	kind := "issue"
	if (repo == "" || number == "") && issueURL != "" {
		if parsed, err := url.Parse(issueURL); err == nil && strings.EqualFold(parsed.Host, "github.com") {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) >= 4 && (parts[2] == "issues" || parts[2] == "pull") {
				repo = firstNonBlank(repo, parts[0]+"/"+parts[1])
				number = firstNonBlank(number, parts[3])
				if parts[2] == "pull" {
					kind = "pr"
				}
			}
		}
	}
	return repo, number, kind, issueURL
}

type gitopsIssueStateCacheOptions struct {
	FindingsRoot string
	Repo         string
	Output       string
}

func runGitopsIssueStateCache(ctx context.Context, opts gitopsIssueStateCacheOptions) (map[string]any, error) {
	root := filepath.Clean(opts.FindingsRoot)
	var findingsPaths []string
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "stats" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "findings.json" {
			findingsPaths = append(findingsPaths, path)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("gitops issue-state-cache: scan %q: %w", root, err)
	}

	type issueRef struct {
		Repo      string
		Number    string
		URL       string
		FindingID string
		RunDir    string
	}
	seen := map[string]bool{}
	var refs []issueRef
	for _, path := range findingsPaths {
		findings, err := gitopsReadJSONFile(path)
		if err != nil {
			return nil, fmt.Errorf("gitops issue-state-cache: read %q: %w", path, err)
		}
		runDir := filepath.Dir(path)
		for _, item := range gitopsFindingsItems(findings) {
			repo, number, kind, issueURL := gitopsIssueRef(item, opts.Repo)
			if repo == "" || number == "" || kind != "issue" {
				continue
			}
			key := repo + "#" + number
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, issueRef{
				Repo:      repo,
				Number:    number,
				URL:       firstNonBlank(issueURL, "https://github.com/"+repo+"/issues/"+number),
				FindingID: stringValue(item, "id"),
				RunDir:    runDir,
			})
		}
	}

	issues := make([]any, 0, len(refs))
	for _, ref := range refs {
		result, err := host.GitHubTicketHandler(ctx, map[string]any{
			"op":   "get",
			"repo": ref.Repo,
			"id":   ref.Number,
		})
		if err != nil {
			return nil, err
		}
		if result.Error != "" {
			return nil, fmt.Errorf("gitops issue-state-cache: fetch %s#%s: %s", ref.Repo, ref.Number, result.Error)
		}
		issue := gitopsIssueStatusResult(result.Data)
		issue["repo"] = ref.Repo
		issue["number"] = ref.Number
		issue["url"] = firstNonBlank(stringValue(issue, "url"), ref.URL)
		issue["issue_url"] = firstNonBlank(stringValue(issue, "issue_url"), ref.URL)
		if ref.FindingID != "" {
			issue["finding_id"] = ref.FindingID
		}
		if ref.RunDir != "" {
			issue["run_dir"] = ref.RunDir
		}
		issues = append(issues, issue)
	}

	out := map[string]any{
		"status":         "issue_state_cached",
		"findings_root":  root,
		"findings_files": len(findingsPaths),
		"issues_count":   len(issues),
		"issues":         issues,
		"source":         "kitsoki gitops issue-state-cache",
		"updated_at":     time.Now().UTC().Format(time.RFC3339),
	}
	if strings.TrimSpace(opts.Output) != "" {
		outPath := filepath.Clean(opts.Output)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, fmt.Errorf("gitops issue-state-cache: mkdir output: %w", err)
		}
		out["output"] = outPath
		if err := gitopsWriteJSONFile(outPath, out); err != nil {
			return nil, fmt.Errorf("gitops issue-state-cache: write %q: %w", outPath, err)
		}
	}
	return out, nil
}

func gitopsReadJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	return out, json.Unmarshal(data, &out)
}

func gitopsWriteJSONFile(path string, value map[string]any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func mapValue(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	value, _ := m[key].(map[string]any)
	return value
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return fmt.Sprintf("%g", v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return ""
	}
}

func intValue(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func stringSliceValue(m map[string]any, key string) []string {
	raw, ok := m[key].([]string)
	if ok {
		return raw
	}
	rows, _ := m[key].([]any)
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if s, ok := row.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func mapSliceValue(m map[string]any, key string) []map[string]any {
	if m == nil {
		return nil
	}
	if rows, ok := m[key].([]map[string]any); ok {
		return rows
	}
	rawRows, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(rawRows))
	for _, raw := range rawRows {
		if row, ok := raw.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func mergeMaps(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstStrings(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func originSummary(jobs []map[string]any) string {
	var parts []string
	for _, job := range firstMaps(jobs, 5) {
		parts = append(parts, stringValue(job, "origin_ref"))
	}
	return strings.Join(parts, "; ")
}

func runSummary(jobs []map[string]any) string {
	var parts []string
	for _, job := range firstMaps(jobs, 5) {
		part := stringValue(job, "origin_ref") + "=" + stringValue(job, "state")
		if runURL := stringValue(job, "run_url"); runURL != "" {
			part += " " + runURL
		}
		parts = append(parts, strings.TrimSpace(part))
	}
	return strings.Join(parts, "; ")
}

func firstMaps(values []map[string]any, n int) []map[string]any {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func gitopsJobEvidenceLinks(job map[string]any) []string {
	var links []string
	for _, asset := range mapSliceValue(job, "assets") {
		for _, key := range []string{"url", "href", "path"} {
			if value := stringValue(asset, key); value != "" {
				links = append(links, value)
				break
			}
		}
	}
	return links
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
