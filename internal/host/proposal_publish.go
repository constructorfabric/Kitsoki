package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ProposalPublishHandler publishes a dev-story proposal and optionally mints
// its linked feature ticket. The story invokes it through publish_design.star;
// file movement and ticket writing are host capabilities because Starlark's
// filesystem surface intentionally has no rename primitive.
func ProposalPublishHandler(ctx context.Context, args map[string]any) (Result, error) {
	workspace := stringValue(args, "workspace")
	slugInput := stringValue(args, "slug")
	changeTarget := stringValue(args, "change_target")
	titleInput := stringValue(args, "title")
	idea := stringValue(args, "idea")
	workdir := stringValue(args, "workdir")
	if workdir == "" {
		workdir = "."
	}
	durable := stringValue(args, "durable")
	if durable == "" {
		durable = filepath.Join("docs", "proposals")
	}
	docFilename := strings.TrimSpace(stringValue(args, "doc_filename"))
	ticketDir := stringValue(args, "ticket_dir")
	if _, present := args["ticket_dir"]; !present && stringValue(args, "ticket_repo") == "" {
		ticketDir = filepath.Join("issues", "features")
	}
	ticketRepo := strings.TrimSpace(stringValue(args, "ticket_repo"))

	var designRel, title string
	if strings.TrimSpace(changeTarget) != "" {
		designRel = relativeTo(workdir, changeTarget)
		title = strings.TrimSpace(titleInput)
		if title == "" {
			title = slugInput
		}
	} else {
		source := filepath.Join(workspace, "004-proposal.md")
		draft, err := os.ReadFile(source)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.proposal.publish: read draft: %v", err)}, nil
		}
		draftText := string(draft)
		draftTitle := proposalTitleFromDraft(draftText)
		title = strings.TrimSpace(titleInput)
		if title == "" {
			title = draftTitle
		}
		if title == "" {
			title = slugInput
		}
		baseDir := resolveProposalPath(workdir, durable)
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			return Result{}, fmt.Errorf("host.proposal.publish: create durable directory: %w", err)
		}
		var destination string
		if docFilename != "" {
			destination = filepath.Join(baseDir, docFilename+".md")
		} else {
			slug := proposalSlug(draftTitle)
			if slug == "proposal" {
				slug = proposalSlug(slugInput)
			}
			destination = availableProposalPath(baseDir, slug)
		}
		if err := os.Rename(source, destination); err != nil {
			return Result{Error: fmt.Sprintf("host.proposal.publish: publish draft: %v", err)}, nil
		}
		designRel = relativeTo(workdir, destination)
	}

	ticketID, ticketPath, ticketURL := "", "", ""
	if ticketRepo != "" {
		body := proposalTicketBody(title, idea, designRel)
		created, err := GitHubTicketHandler(ctx, map[string]any{
			"op": "create", "repo": ticketRepo, "title": title, "body": body,
			"component": "proposal", "severity": "P3", "trace_ref": designRel,
		})
		if err != nil {
			return created, err
		}
		if created.Error != "" {
			return Result{Error: "host.proposal.publish: " + created.Error}, nil
		}
		ticketID = stringValue(created.Data, "id")
		ticketURL = stringValue(created.Data, "url")
	} else if strings.TrimSpace(ticketDir) != "" {
		var err error
		ticketID, ticketPath, err = writeProposalTicket(workdir, ticketDir, slugInput, title, idea, designRel)
		if err != nil {
			return Result{}, fmt.Errorf("host.proposal.publish: write feature ticket: %w", err)
		}
		ticketURL = designRel
	}

	return Result{Data: map[string]any{
		"design_file":  designRel,
		"ticket_id":    ticketID,
		"ticket_path":  ticketPath,
		"ticket_title": title,
		"ticket_url":   ticketURL,
	}}, nil
}

var proposalHeadingRE = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)

func proposalTitleFromDraft(draft string) string {
	match := proposalHeadingRE.FindStringSubmatch(draft)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func proposalSlug(text string) string {
	line := text
	if idx := strings.IndexByte(line, '\n'); idx >= 0 {
		line = line[:idx]
	}
	line = strings.ToLower(strings.TrimSpace(line))
	line = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(line, "-")
	parts := strings.Split(strings.Trim(line, "-"), "-")
	if len(parts) > 6 {
		parts = parts[:6]
	}
	if len(parts) == 0 || (len(parts) == 1 && parts[0] == "") {
		return "proposal"
	}
	return strings.Join(parts, "-")
}

func resolveProposalPath(workdir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workdir, path)
}

func relativeTo(workdir, path string) string {
	if !filepath.IsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(workdir, path)
	if err != nil {
		return path
	}
	return rel
}

func availableProposalPath(baseDir, slug string) string {
	path := filepath.Join(baseDir, slug+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	for i := 2; i < 100; i++ {
		path = filepath.Join(baseDir, fmt.Sprintf("%s-%d.md", slug, i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
	return filepath.Join(baseDir, slug+"-100.md")
}

func writeProposalTicket(workdir, ticketDir, slug, title, idea, designRel string) (string, string, error) {
	dir := resolveProposalPath(workdir, ticketDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	now := time.Now().UTC()
	base := fmt.Sprintf("F-%s-%s", now.Format("2006-01-02T15:04:05Z"), proposalSlug(slug))
	dest := availableTicketPath(dir, base)
	ticketID := strings.TrimSuffix(filepath.Base(dest), filepath.Ext(dest))
	filedAt := now.Format("2006-01-02T15:04:05Z")
	ticketTitle := strings.TrimSpace(title)
	if ticketTitle == "" {
		ticketTitle = slug
	}
	bodyIdea := strings.TrimSpace(idea)
	content := fmt.Sprintf("---\ntitle: %q\nstatus: open\nseverity: P2\nassignee: \"\"\nurl: %q\ncomponent: proposal\nfiled_at: %q\nproposal: %q\n---\n\n# %s\n\nImplement the accepted proposal:\n\n[%s](%s)\n\n", ticketTitle, designRel, filedAt, designRel, ticketTitle, designRel, designRel)
	if bodyIdea != "" {
		content += bodyIdea + "\n\n"
	}
	content += "## Source\n\nFiled automatically when the proposal was published. The linked\nproposal document carries the full Why / What changes / Impact spine —\nread it before starting implementation.\n"
	if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
		return "", "", err
	}
	return ticketID, relativeTo(workdir, dest), nil
}

func availableTicketPath(dir, base string) string {
	path := filepath.Join(dir, base+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	for i := 2; i < 100; i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d.md", base, i))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
	return filepath.Join(dir, base+"-100.md")
}

func proposalTicketBody(title, idea, designRel string) string {
	body := fmt.Sprintf("Implement the accepted proposal:\n\n[%s](%s)\n\n", designRel, designRel)
	if strings.TrimSpace(idea) != "" {
		body += strings.TrimSpace(idea) + "\n\n"
	}
	return body + "## Source\n\nFiled automatically when the proposal was published. The linked proposal carries the full Why / What changes / Impact spine — read it before starting implementation.\n"
}
