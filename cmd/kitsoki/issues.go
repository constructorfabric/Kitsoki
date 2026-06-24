// issues.go — `kitsoki issues migrate`: one-shot migration of the in-repo
// issues/{bugs,features} markdown pile to GitHub issues.
//
// The github-issues-tracker cutover (slice #4). For each local ticket it creates
// a GitHub issue (labels + ```kitsoki metadata block via the shared
// host.GitHubFileBug path), replays the file's `## Comment` thread as issue
// comments, closes the issue if the local status is resolved/wontfix, and writes
// the new issue ref back into the file's `external:` frontmatter so a re-run is
// idempotent (already-migrated tickets are skipped). The local files are NOT
// deleted — issues/ stays as a frozen, deprecated archive.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/bugfile"
	"kitsoki/internal/host"
)

func issuesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issues",
		Short: "Manage the in-repo issues archive (migrate to GitHub)",
		Long: `Tools for the in-repo issues/ pile.

issues/{bugs,features}/*.md was kitsoki's tracker before the project moved to
GitHub Issues. ` + "`kitsoki issues migrate`" + ` lifts that pile into GitHub
issues on a target repo, idempotently, leaving the local files as a frozen
archive.`,
	}
	cmd.AddCommand(issuesMigrateCmd())
	return cmd
}

// migratedComment is one parsed `## Comment <ts> by <author>` block.
type migratedComment struct {
	ts     string
	author string
	body   string
}

var commentHeadingRe = regexp.MustCompile(`(?m)^## Comment\s+(\S+)\s+by\s+(.+?)\s*$`)

// parseIssueFile reads a local ticket: frontmatter, the prose body (before the
// first comment heading), and the comment thread.
func parseIssueFile(path string) (fm map[string]string, body string, comments []migratedComment, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", nil, err
	}
	fm = bugfile.ParseFrontmatter(data)

	// Strip the frontmatter block to get the markdown body.
	text := string(data)
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
			text = text[4+end+5:]
		}
	}

	// Split prose from the comment thread at the first `## Comment` heading.
	loc := commentHeadingRe.FindStringIndex(text)
	if loc == nil {
		return fm, strings.TrimSpace(text), nil, nil
	}
	body = strings.TrimSpace(text[:loc[0]])
	thread := text[loc[0]:]

	idxs := commentHeadingRe.FindAllStringSubmatchIndex(thread, -1)
	for i, m := range idxs {
		ts := thread[m[2]:m[3]]
		author := strings.TrimSpace(thread[m[4]:m[5]])
		start := m[1]
		end := len(thread)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		comments = append(comments, migratedComment{
			ts:     ts,
			author: author,
			body:   strings.TrimSpace(thread[start:end]),
		})
	}
	return fm, body, comments, nil
}

// writeBackExternal records the new GitHub issue ref in the file's `external:`
// frontmatter, so a re-run skips this ticket. Handles the common `external: {}`
// (or missing) shape.
func writeBackExternal(path, repo, number, url string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	ext := fmt.Sprintf("external: {github: \"%s#%s\", url: \"%s\"}", repo, number, url)
	re := regexp.MustCompile(`(?m)^external:.*$`)
	if re.MatchString(text) {
		text = re.ReplaceAllString(text, ext)
	} else if strings.HasPrefix(text, "---\n") {
		// Insert just after the opening fence.
		text = "---\n" + ext + "\n" + text[4:]
	} else {
		return fmt.Errorf("no frontmatter to annotate in %s", path)
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

func issuesMigrateCmd() *cobra.Command {
	var (
		repo   string
		root   string
		dryRun bool
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate issues/{bugs,features}/*.md to GitHub issues (idempotent)",
		Long: `Create a GitHub issue for each local ticket, replay its comment thread,
close it if resolved/wontfix, and write the new issue ref back into the file's
external: frontmatter (so a re-run skips it). The local files are never deleted.

Requires gh auth. --repo is the owner/repo to file into.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if strings.TrimSpace(repo) == "" {
				return fmt.Errorf("--repo <owner/repo> is required")
			}
			if root == "" {
				root = "."
			}
			ctx := context.Background()
			out := cmd.OutOrStdout()

			var files []string
			for _, sub := range []string{"bugs", "features"} {
				matches, _ := filepath.Glob(filepath.Join(root, "issues", sub, "*.md"))
				for _, m := range matches {
					if strings.HasSuffix(m, "README.md") {
						continue
					}
					files = append(files, m)
				}
			}
			sort.Strings(files)
			if len(files) == 0 {
				fmt.Fprintln(out, "no local tickets found under", filepath.Join(root, "issues"))
				return nil
			}

			migrated, skipped, failed := 0, 0, 0
			for _, f := range files {
				id := strings.TrimSuffix(filepath.Base(f), ".md")
				fm, body, comments, err := parseIssueFile(f)
				if err != nil {
					fmt.Fprintf(out, "FAIL  %s: %v\n", id, err)
					failed++
					continue
				}
				if strings.Contains(fm["external"], "github:") {
					fmt.Fprintf(out, "skip  %s (already migrated)\n", id)
					skipped++
					continue
				}
				title := nonEmptyStr(fm["title"], id)
				target := nonEmptyStr(fm["target"], "kitsoki")
				migBody := body + fmt.Sprintf("\n\n---\n_Migrated from the in-repo issue `%s` (filed %s)._\n", id, nonEmptyStr(fm["filed_at"], "unknown"))

				if dryRun {
					fmt.Fprintf(out, "DRY   %s → would create on %s (sev=%s comp=%s target=%s, %d comment(s))\n",
						id, repo, fm["severity"], fm["component"], target, len(comments))
					continue
				}

				res, err := host.GitHubFileBug(ctx, host.GitHubBugFiling{
					Repo:       repo,
					Title:      title,
					Body:       migBody,
					Severity:   fm["severity"],
					Component:  fm["component"],
					Target:     target,
					TraceRef:   fm["trace_ref"],
					KitsokiRev: fm["kitsoki_rev"],
					FiledBy:    fm["filed_by"],
				})
				if err != nil {
					fmt.Fprintf(out, "FAIL  %s: %v\n", id, err)
					failed++
					continue
				}

				// Replay the comment thread.
				for _, c := range comments {
					cbody := fmt.Sprintf("_Migrated comment — originally %s by %s:_\n\n%s", c.ts, c.author, stripCommentHeading(c.body))
					if _, cerr := host.GitHubTicketHandler(ctx, map[string]any{
						"op": "comment", "repo": repo, "id": res.Number, "body": cbody,
					}); cerr != nil {
						fmt.Fprintf(out, "      warn: comment replay failed on %s: %v\n", res.Number, cerr)
					}
				}

				// Close if the local status is a terminal one.
				switch strings.ToLower(strings.TrimSpace(fm["status"])) {
				case "resolved", "wontfix", "closed", "done", "fixed":
					_, _ = host.GitHubTicketHandler(ctx, map[string]any{
						"op": "transition", "repo": repo, "id": res.Number, "to": "closed",
					})
				}

				if werr := writeBackExternal(f, repo, res.Number, res.URL); werr != nil {
					fmt.Fprintf(out, "      warn: external write-back failed for %s: %v\n", id, werr)
				}
				fmt.Fprintf(out, "ok    %s → %s\n", id, res.URL)
				migrated++
			}

			fmt.Fprintf(out, "\nmigrated %d, skipped %d, failed %d (of %d)\n", migrated, skipped, failed, len(files))
			if failed > 0 {
				return fmt.Errorf("%d ticket(s) failed to migrate", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo to migrate into (required)")
	cmd.Flags().StringVar(&root, "root", "", "repo root containing issues/ (default: cwd)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print what would migrate without creating issues")
	return cmd
}

// stripCommentHeading removes a leading `## Comment …` heading line from a
// captured comment body (the regex split keeps the body after the heading, but
// guard against any heading text leaking in).
func stripCommentHeading(body string) string {
	return strings.TrimSpace(commentHeadingRe.ReplaceAllString(body, ""))
}

// nonEmptyStr returns v if non-blank, else fallback.
func nonEmptyStr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
