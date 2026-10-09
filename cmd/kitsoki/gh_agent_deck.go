package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/ghagent"
	"kitsoki/internal/ghagent/bugdeck"
	"kitsoki/internal/host"
)

// newGHAgentDeckCmd builds `kitsoki gh-agent deck`: the agent-side reaction that
// turns a filed bug report's on-agent evidence (rrweb + HAR) into a hosted,
// in-browser-viewable slidey deck and comments the link on the issue.
//
// It is the deterministic, no-LLM entry the webhook reaction will also call. For
// the end-to-end proof it can read evidence from explicit --rrweb/--har paths
// instead of the agent's evidence store.
func newGHAgentDeckCmd() *cobra.Command {
	var (
		repo          string
		issue         string
		evidenceDir   string
		rrwebPath     string
		harPath       string
		decksDir      string
		slideyDir     string
		slideyBin     string
		publicBaseURL string
		title         string
		comment       bool
	)
	cmd := &cobra.Command{
		Use:   "deck",
		Short: "Produce + host a no-LLM session-replay deck for a filed bug report and comment the link",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			ctx := cmd.Context()

			if strings.TrimSpace(repo) == "" || strings.TrimSpace(issue) == "" {
				return fmt.Errorf("gh-agent deck: --repo and --issue are required")
			}
			if strings.TrimSpace(slideyDir) == "" && strings.TrimSpace(slideyBin) == "" {
				slideyDir = os.Getenv("SLIDEY_DIR")
			}
			if strings.TrimSpace(slideyDir) == "" && strings.TrimSpace(slideyBin) == "" {
				return fmt.Errorf("gh-agent deck: set --slidey-dir (or SLIDEY_DIR) or --slidey-bin")
			}

			ev, err := loadDeckEvidence(repo, issue, evidenceDir, rrwebPath, harPath)
			if err != nil {
				return err
			}
			ev.Title = strings.TrimSpace(title)
			ev.IssueNumber = strings.TrimSpace(issue)
			ev.IssueURL = fmt.Sprintf("https://github.com/%s/issues/%s", repo, strings.TrimSpace(issue))

			store, err := ghagent.NewDeckStore(decksDir)
			if err != nil {
				return err
			}

			d := &ghagent.DeckReactor{
				Renderer:      bugdeck.SlideyRenderer{Dir: slideyDir, Bin: slideyBin},
				Store:         store,
				Repo:          repo,
				PublicBaseURL: publicBaseURL,
			}
			if comment {
				d.Ticket = host.GitHubTicketHandler
			}

			url, err := d.React(ctx, issue, ev)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			return nil
		},
	}
	cmd.Flags().StringVar(&repo, "repo", "", "owner/repo the issue lives on")
	cmd.Flags().StringVar(&issue, "issue", "", "issue number")
	cmd.Flags().StringVar(&evidenceDir, "evidence-dir", "", "agent evidence store root (reads <id>/rrweb.json,har.json)")
	cmd.Flags().StringVar(&rrwebPath, "rrweb", "", "explicit rrweb.json path (overrides the evidence store)")
	cmd.Flags().StringVar(&harPath, "har", "", "explicit har.json path (overrides the evidence store)")
	cmd.Flags().StringVar(&decksDir, "decks-dir", "", "directory the agent hosts rendered decks from")
	cmd.Flags().StringVar(&slideyDir, "slidey-dir", "", "slidey project dir (or set SLIDEY_DIR)")
	cmd.Flags().StringVar(&slideyBin, "slidey-bin", "", "slidey binary/entrypoint (alternative to --slidey-dir)")
	cmd.Flags().StringVar(&publicBaseURL, "public-base-url", "", "agent public base URL used in the deck link")
	cmd.Flags().StringVar(&title, "title", "", "issue title for deck framing")
	cmd.Flags().BoolVar(&comment, "comment", false, "post the deck-link comment on the issue (needs GitHub API auth)")
	return cmd
}

// loadDeckEvidence resolves the rrweb + HAR bytes for a report, preferring
// explicit --rrweb/--har paths, then the agent's evidence store keyed by the
// issue's deck id.
func loadDeckEvidence(repo, issue, evidenceDir, rrwebPath, harPath string) (bugdeck.Evidence, error) {
	if strings.TrimSpace(rrwebPath) != "" || strings.TrimSpace(harPath) != "" {
		ev := bugdeck.Evidence{}
		if p := strings.TrimSpace(rrwebPath); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				return ev, fmt.Errorf("read rrweb: %w", err)
			}
			ev.RRWeb = b
		}
		if p := strings.TrimSpace(harPath); p != "" {
			b, err := os.ReadFile(p)
			if err != nil {
				return ev, fmt.Errorf("read har: %w", err)
			}
			ev.HAR = b
		}
		return ev, nil
	}
	if strings.TrimSpace(evidenceDir) == "" {
		return bugdeck.Evidence{}, fmt.Errorf("gh-agent deck: provide --rrweb/--har or --evidence-dir")
	}
	es, err := ghagent.NewEvidenceStore(evidenceDir)
	if err != nil {
		return bugdeck.Evidence{}, err
	}
	return es.Load(ghagent.DeckID(repo, issue))
}
