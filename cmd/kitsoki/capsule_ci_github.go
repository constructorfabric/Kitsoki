package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/ci"
)

func capsuleCIGitHubCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Project GitHub webhook and check-run payloads for Capsule CI",
		Long: `Project GitHub webhook and check-run payloads for Capsule CI.

These commands are pure adapters: they normalize GitHub webhook JSON into the
standard Capsule CI trigger shape and project persisted Capsule CI run records
into GitHub check-run JSON. The publish-check command is the explicit network
boundary and requires GitHub credentials.`,
	}
	cmd.AddCommand(capsuleCIGitHubTriggerCmd(), capsuleCIGitHubCheckCmd(), capsuleCIGitHubPublishCheckCmd())
	return cmd
}

func capsuleCIGitHubTriggerCmd() *cobra.Command {
	var payloadPath, event, pipeline string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "trigger",
		Short: "Normalize a GitHub webhook payload into a Capsule CI trigger",
		RunE: func(cmd *cobra.Command, _ []string) error {
			raw, err := readPayloadFile(cmd, payloadPath)
			if err != nil {
				return err
			}
			switch event {
			case "", "pull_request":
				var payload ci.GitHubPullRequestEvent
				if err := json.Unmarshal(raw, &payload); err != nil {
					return fmt.Errorf("capsule ci github trigger: parse pull_request payload: %w", err)
				}
				trigger, err := ci.NormalizeGitHubPullRequestTrigger(payload, pipeline)
				if err != nil {
					return err
				}
				return capsuleWorkspaceWrite(cmd, trigger, jsonOut)
			default:
				return fmt.Errorf("capsule ci github trigger: unsupported event %q", event)
			}
		},
	}
	cmd.Flags().StringVar(&payloadPath, "payload", "", "GitHub webhook JSON payload path, or - for stdin")
	cmd.Flags().StringVar(&event, "event", "pull_request", "GitHub event name")
	cmd.Flags().StringVar(&pipeline, "pipeline", "change", "requested Capsule CI pipeline")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("payload")
	return cmd
}

func capsuleCIGitHubCheckCmd() *cobra.Command {
	var project, job, detailsURL string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Project a Capsule CI run record to a GitHub check-run payload",
		RunE: func(cmd *cobra.Command, _ []string) error {
			record, err := (ci.FileRunStore{ProjectRoot: project}).Get(job)
			if err != nil {
				return err
			}
			check, err := ci.BuildGitHubCheckRun(record.Result, detailsURL)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, check, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&job, "job", "", "Capsule CI job id")
	cmd.Flags().StringVar(&detailsURL, "details-url", "", "GitHub check details URL")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("job")
	return cmd
}

func capsuleCIGitHubPublishCheckCmd() *cobra.Command {
	var project, job, detailsURL, repo, tokenEnv, apiURL string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "publish-check",
		Short: "Publish a Capsule CI run record as a GitHub check run",
		RunE: func(cmd *cobra.Command, _ []string) error {
			record, err := (ci.FileRunStore{ProjectRoot: project}).Get(job)
			if err != nil {
				return err
			}
			check, err := ci.BuildGitHubCheckRun(record.Result, detailsURL)
			if err != nil {
				return err
			}
			token, err := githubTokenFromEnv(tokenEnv)
			if err != nil {
				return err
			}
			publication, err := (ci.GitHubCheckPublisher{BaseURL: apiURL, Token: token}).PublishCheckRun(cmd.Context(), repo, check)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, publication, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&job, "job", "", "Capsule CI job id")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository owner/name")
	cmd.Flags().StringVar(&detailsURL, "details-url", "", "GitHub check details URL")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "environment variable containing a GitHub token (default: GH_TOKEN, then GITHUB_TOKEN)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "GitHub API base URL (default: https://api.github.com)")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("job")
	_ = cmd.MarkFlagRequired("repo")
	return cmd
}

func githubTokenFromEnv(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, nil
		}
		return "", fmt.Errorf("capsule ci github: %s is not set", name)
	}
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("capsule ci github: GH_TOKEN or GITHUB_TOKEN is required")
}

func readPayloadFile(cmd *cobra.Command, path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("payload path is required")
	}
	if path == "-" {
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read payload %s: %w", path, err)
	}
	return raw, nil
}
