// Package host — host.local_files.ticket — file-backed ticket provider.
//
// Implements the `ticket` host_interface (see docs/architecture/hosts.md). The
// on-disk bug format is documented in docs/stories/bugs.md:
// YAML frontmatter, a markdown body, and `## Comment <ISO> by <author>`
// blocks appended at the bottom.
//
// One prefix-fallback handler dispatches all five ticket ops on the
// `op` arg the runtime registry rewrites into the args map. This keeps
// the registration story to a single name (`host.local_files.ticket`)
// while leaving room for per-op handlers if/when an op needs distinct
// behaviour.
//
// World context expected via args:
//   - root      (string): directory under which `issues/bugs/*.md` lives.
//     Falls back to the env var KITSOKI_TICKETS_ROOT (set per session)
//     or the process working directory.
//   - id        (string): the bug ID — the file's basename without `.md`.
package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/capsule"
)

// LocalFilesTicketHandler implements host.local_files.ticket (prefix-fallback).
//
// Required args:
//   - op (string): one of "search", "get", "comment", "transition", "assign", "unassign", "list_mine".
//
// Per-op args/returns follow the ticket iface contract.  See doc comments on each
// dispatch helper below for the precise shape.
//
// Optional args (all ops):
//   - root (string): override the tickets root.  When absent the handler
//     consults $KITSOKI_TICKETS_ROOT or, failing that, the process cwd.
//
// Returns Result.Error for expected, domain-level errors (missing file,
// unknown op, malformed frontmatter).  Returns a Go error only for
// infrastructure failures (e.g. cannot read a directory the caller
// pointed us at).
func LocalFilesTicketHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	op = strings.TrimSpace(op)
	if op == "" {
		return Result{Error: "host.local_files.ticket: op argument is required"}, nil
	}

	root, err := resolveTicketsRoot(args)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.local_files.ticket: %v", err)}, nil
	}

	switch op {
	case "search":
		return ticketSearch(root, args)
	case "get":
		return ticketGet(root, args)
	case "comment":
		return ticketComment(root, args, time.Now().UTC())
	case "transition":
		return ticketTransition(root, args)
	case "assign":
		return ticketSetAssignee(root, args, false)
	case "unassign":
		return ticketSetAssignee(root, args, true)
	case "list_mine":
		return ticketListMine(root, args)
	default:
		return Result{Error: fmt.Sprintf("host.local_files.ticket: unknown op %q", op)}, nil
	}
}

// resolveTicketsRoot resolves the directory under which `issues/bugs/`
// lives. Order of precedence: args.root -> $KITSOKI_TICKETS_ROOT -> cwd.
// Relative roots inside a managed capsule are anchored at the recorded source
// checkout, so readers and mutators use the same durable ticket pile as filers.
func resolveTicketsRoot(args map[string]any) (string, error) {
	root, _ := args["root"].(string)
	root = strings.TrimSpace(root)
	if root == "" {
		root = strings.TrimSpace(os.Getenv("KITSOKI_TICKETS_ROOT"))
	}
	if root == "" {
		root = "."
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root), nil
	}
	if sourcePath := capsule.ManagedSourcePathFromCWD(root); sourcePath != "" {
		return sourcePath, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return filepath.Clean(filepath.Join(cwd, root)), nil
}

// ticketKindDirs enumerates the issues/* subdirectories scanned by the
// local-files provider. Keys are the ticket-type label written into the
// row's `type` field; values are the on-disk directory name. Stable
// iteration order matters for tests that pin search/list output.
var ticketKindDirs = []struct{ Kind, Dir string }{
	{"bug", "bugs"},
	{"feature", "features"},
	{"epic", "epics"},
}

// findTicketPath locates a ticket file under issues/<kind>/ using the
// canonical flat shape (`<id>.md`). Returns ("", "", nil) when the id is
// not found in any of the type dirs; the caller turns that into a
// domain-level "not found" error.
func findTicketPath(root, id string) (path, kind string, err error) {
	for _, td := range ticketKindDirs {
		p := filepath.Join(root, "issues", td.Dir, id+".md")
		if _, statErr := os.Stat(p); statErr == nil {
			return p, td.Kind, nil
		} else if !os.IsNotExist(statErr) {
			return "", "", statErr
		}
	}
	return "", "", nil
}

// ─── Bug-file model & I/O ───────────────────────────────────────────────────

// BugComment is one `## Comment <iso> by <author>` block in the body.
type BugComment struct {
	Timestamp time.Time
	Author    string
	Body      string
}

// BugFile is the parsed shape of `issues/<kind>/<id>.md`.  Frontmatter
// is kept as a generic map so unknown keys survive round-trips (per
// docs/stories/bugs.md).
type BugFile struct {
	ID       string         // filename without `.md`
	Kind     string         // "bug" | "feature" | "epic" — set by the lister
	Path     string         // absolute path
	Front    map[string]any // YAML frontmatter (rich types preserved)
	Body     string         // markdown body BEFORE the comment block
	Comments []BugComment
}

// readBugFile parses a single bug file at path.  Returns a domain error
// (in Result.Error form) only as a sentinel via err.Error(); the public
// dispatchers wrap it as Result.Error.
func readBugFile(path string) (*BugFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	bodyText, comments := splitComments(body)
	id := ticketIDFromPath(path)
	return &BugFile{
		ID:       id,
		Path:     path,
		Front:    front,
		Body:     bodyText,
		Comments: comments,
	}, nil
}

func ticketIDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".md")
}

// splitFrontmatter slices the leading `---\n…\n---\n` block off raw and
// YAML-unmarshals it.  Returns ({}, body, nil) when there's no
// frontmatter (treating the whole file as body).
func splitFrontmatter(raw []byte) (map[string]any, string, error) {
	s := string(raw)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return map[string]any{}, s, nil
	}
	// Skip the first delimiter line.
	rest := s[4:]
	if strings.HasPrefix(s, "---\r\n") {
		rest = s[5:]
	}
	// Find the closing `---` on its own line.
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("bug file: missing closing --- delimiter")
	}
	front := rest[:end]
	// Skip past `\n---` and the following newline (if any).
	tail := rest[end+4:]
	tail = strings.TrimPrefix(tail, "\r")
	tail = strings.TrimPrefix(tail, "\n")

	var fm map[string]any
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return nil, "", fmt.Errorf("bug file: parse frontmatter: %w", err)
	}
	if fm == nil {
		fm = map[string]any{}
	}
	return fm, tail, nil
}

// splitComments cleaves the body into the markdown prose and a sequence
// of comment blocks.  A comment is a `## Comment <ISO> by <author>`
// heading followed by free text up to the next such heading or EOF.
func splitComments(body string) (string, []BugComment) {
	lines := strings.Split(body, "\n")
	// Find the index of the first `## Comment ` heading.
	firstIdx := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "## Comment ") {
			firstIdx = i
			break
		}
	}
	if firstIdx < 0 {
		return body, nil
	}

	prose := strings.Join(lines[:firstIdx], "\n")
	prose = strings.TrimRight(prose, "\n") + "\n"

	var comments []BugComment
	var cur *BugComment
	for i := firstIdx; i < len(lines); i++ {
		ln := lines[i]
		if strings.HasPrefix(ln, "## Comment ") {
			if cur != nil {
				cur.Body = strings.TrimRight(cur.Body, "\n")
				comments = append(comments, *cur)
			}
			ts, author := parseCommentHeading(ln)
			cur = &BugComment{Timestamp: ts, Author: author}
			continue
		}
		if cur != nil {
			cur.Body += ln + "\n"
		}
	}
	if cur != nil {
		cur.Body = strings.TrimRight(cur.Body, "\n")
		comments = append(comments, *cur)
	}
	return prose, comments
}

// parseCommentHeading extracts (timestamp, author) from a
// `## Comment 2026-05-14T10:32:05Z by brad` line.  Best-effort: a
// malformed heading yields a zero timestamp and the raw rest as author.
func parseCommentHeading(line string) (time.Time, string) {
	rest := strings.TrimPrefix(line, "## Comment ")
	rest = strings.TrimSpace(rest)
	// Split into timestamp and "by <author>".
	parts := strings.SplitN(rest, " by ", 2)
	if len(parts) == 1 {
		// Try parsing rest as a bare timestamp.
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[0])); err == nil {
			return t, ""
		}
		return time.Time{}, strings.TrimSpace(parts[0])
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[0]))
	if err != nil {
		return time.Time{}, strings.TrimSpace(parts[1])
	}
	return ts, strings.TrimSpace(parts[1])
}

// writeBugFile serialises bf back to disk, preserving unknown
// frontmatter keys.  Comments are appended verbatim after the body.
func writeBugFile(bf *BugFile) error {
	var sb strings.Builder
	sb.WriteString("---\n")
	frontBytes, err := yaml.Marshal(bf.Front)
	if err != nil {
		return fmt.Errorf("marshal frontmatter: %w", err)
	}
	sb.Write(frontBytes)
	if !strings.HasSuffix(string(frontBytes), "\n") {
		sb.WriteByte('\n')
	}
	sb.WriteString("---\n")
	body := strings.TrimRight(bf.Body, "\n")
	if body != "" {
		sb.WriteString(body)
		sb.WriteByte('\n')
	}
	for _, c := range bf.Comments {
		sb.WriteByte('\n')
		ts := c.Timestamp.UTC().Format(time.RFC3339)
		fmt.Fprintf(&sb, "## Comment %s by %s\n\n", ts, c.Author)
		sb.WriteString(strings.TrimRight(c.Body, "\n"))
		sb.WriteByte('\n')
	}
	return os.WriteFile(bf.Path, []byte(sb.String()), 0o644)
}

// listAllBugs returns every ticket under root/issues/{bugs,features,
// epics}/ using the canonical flat `*.md` shape. Each row carries its
// source-dir-derived `Kind` so callers can render and route by type.
// An absent type-dir yields nothing for that kind — a fresh repo with
// only bugs is fine.  Name retained for minimal-diff churn; the
// function now lists all three kinds.
func listAllBugs(root string) ([]*BugFile, []string, error) {
	var out []*BugFile
	var warnings []string
	for _, td := range ticketKindDirs {
		dir := filepath.Join(root, "issues", td.Dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				// Ticket directories commonly carry their own format README. It is
				// documentation, not an implicit open ticket.
				if strings.EqualFold(e.Name(), "README.md") {
					continue
				}
				bf, err := readBugFile(filepath.Join(dir, e.Name()))
				if err != nil {
					// Keep the healthy queue available, but make every skipped
					// candidate visible to the operator. Silently swallowing one
					// malformed report is indistinguishable from losing it.
					warnings = append(warnings, fmt.Sprintf("%s: %v", filepath.ToSlash(filepath.Join(dir, e.Name())), err))
					continue
				}
				bf.Kind = td.Kind
				out = append(out, bf)
			}
		}
	}
	// Multi-key sort: severity ASC (P0 first), then ID DESC. IDs are
	// ISO-timestamp-prefixed, so ID DESC is effectively filed_at DESC
	// — newest first within each severity bucket. The view used to
	// |reverse the host result to compensate for the old id-ASC
	// ordering; with the host returning the right order, that view-
	// side hack should go away.
	sort.SliceStable(out, func(i, j int) bool {
		si := severityRank(out[i].frontString("severity"))
		sj := severityRank(out[j].frontString("severity"))
		if si != sj {
			return si < sj
		}
		return out[i].ID > out[j].ID
	})
	return out, warnings, nil
}

// ─── Op dispatchers ─────────────────────────────────────────────────────────

// ticketSearch implements ticket.search.
//
// Input  args: query (string, substring match against title+body), limit (int).
// Output Data: tickets ([]{id,title,status,severity,assignee,url}).
func ticketSearch(root string, args map[string]any) (Result, error) {
	query, _ := args["query"].(string)
	query = strings.ToLower(strings.TrimSpace(query))
	limit := optInt(args, "limit", 0)

	bugs, warnings, err := listAllBugs(root)
	if err != nil {
		return Result{Error: fmt.Sprintf("list bugs: %v", err)}, nil
	}
	out := make([]map[string]any, 0, len(bugs))
	for _, b := range bugs {
		if query != "" {
			hay := strings.ToLower(b.titleString() + " " + b.Body)
			if !strings.Contains(hay, query) {
				continue
			}
		}
		row := bugSummary(b)
		annotateLocalTicketPath(b, row)
		out = append(out, row)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return Result{Data: map[string]any{"tickets": out, "provider_errors": warnings}}, nil
}

// ticketGet implements ticket.get.
//
// Input args:  id (string).
// Output Data: id, title, body, status, severity, assignee, url, comments.
func ticketGet(root string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.get: id argument is required"}, nil
	}
	path, kind, err := findTicketPath(root, id)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.get: %v", err)}, nil
	}
	if path == "" {
		return Result{Error: fmt.Sprintf("ticket.get: %s not found", id)}, nil
	}
	bf, err := readBugFile(path)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.get: %v", err)}, nil
	}
	bf.Kind = kind
	data := bugSummary(bf)
	annotateLocalTicketPath(bf, data)
	data["body"] = bf.Body
	cm := make([]map[string]any, 0, len(bf.Comments))
	for i, c := range bf.Comments {
		cm = append(cm, map[string]any{
			"id":         fmt.Sprintf("%s#%d", bf.ID, i+1),
			"author":     c.Author,
			"body":       c.Body,
			"created_at": c.Timestamp.UTC().Format(time.RFC3339),
		})
	}
	data["comments"] = cm
	return Result{Data: data}, nil
}

// ticketComment implements ticket.comment.
//
// Input args:  id (string), body (string), thread (string, optional —
// when set and points at a path, the comment is appended to *that file*
// instead of the bug under issues/bugs/.  This is the local-files
// dogfood path where `world.thread` IS the bug file).
//
// Output Data: ok (bool), comment_id (string).
func ticketComment(root string, args map[string]any, now time.Time) (Result, error) {
	id, _ := args["id"].(string)
	body, _ := args["body"].(string)
	thread, _ := args["thread"].(string)
	if strings.TrimSpace(body) == "" {
		return Result{Error: "ticket.comment: body argument is required"}, nil
	}

	// Resolve the target file: either thread (when it looks like a
	// path) or issues/<kind>/<id>.md, searching the three known kinds.
	var path string
	switch {
	case thread != "" && (strings.HasSuffix(thread, ".md") || filepath.IsAbs(thread)):
		path = thread
	case strings.TrimSpace(id) != "":
		p, _, ferr := findTicketPath(root, id)
		if ferr != nil {
			return Result{Error: fmt.Sprintf("ticket.comment: %v", ferr)}, nil
		}
		if p == "" {
			return Result{Error: fmt.Sprintf("ticket.comment: %s not found", id)}, nil
		}
		path = p
	default:
		return Result{Error: "ticket.comment: id or thread is required"}, nil
	}

	bf, err := readBugFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Error: fmt.Sprintf("ticket.comment: %s not found", path)}, nil
		}
		return Result{Error: fmt.Sprintf("ticket.comment: %v", err)}, nil
	}

	author, _ := args["author"].(string)
	if author == "" {
		author = "kitsoki"
	}
	bf.Comments = append(bf.Comments, BugComment{
		Timestamp: now.UTC(),
		Author:    author,
		Body:      strings.TrimRight(body, "\n"),
	})
	if err := writeBugFile(bf); err != nil {
		return Result{Error: fmt.Sprintf("ticket.comment: write: %v", err)}, nil
	}
	commentID := fmt.Sprintf("%s#%d", bf.ID, len(bf.Comments))
	return Result{Data: map[string]any{"ok": true, "comment_id": commentID}}, nil
}

// ticketTransition implements ticket.transition.
//
// Input args:  id (string), to (string).
// Output Data: ok (bool).
//
// Rewrites the `status:` key in frontmatter.  Other keys are preserved.
func ticketTransition(root string, args map[string]any) (Result, error) {
	id, _ := args["id"].(string)
	to, _ := args["to"].(string)
	if strings.TrimSpace(id) == "" {
		return Result{Error: "ticket.transition: id argument is required"}, nil
	}
	if strings.TrimSpace(to) == "" {
		return Result{Error: "ticket.transition: to argument is required"}, nil
	}
	path, _, err := findTicketPath(root, id)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.transition: %v", err)}, nil
	}
	if path == "" {
		return Result{Error: fmt.Sprintf("ticket.transition: %s not found", id)}, nil
	}
	bf, err := readBugFile(path)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.transition: %v", err)}, nil
	}
	bf.Front["status"] = to
	if err := writeBugFile(bf); err != nil {
		return Result{Error: fmt.Sprintf("ticket.transition: write: %v", err)}, nil
	}
	return Result{Data: map[string]any{"ok": true}}, nil
}

// ticketSetAssignee updates the provider-neutral assignee field. Agent
// principals are runtime-only identities and must not leak into ticket files.
func ticketSetAssignee(root string, args map[string]any, clear bool) (Result, error) {
	id := strings.TrimSpace(ghStr(args["id"]))
	if id == "" {
		return Result{Error: "ticket.assign: id argument is required"}, nil
	}
	assignee := strings.TrimSpace(ghStr(args["assignee"]))
	if !clear && assignee == "" {
		return Result{Error: "ticket.assign: assignee argument is required"}, nil
	}
	if strings.HasPrefix(strings.ToLower(assignee), "agent:") {
		return Result{Error: "ticket.assign: agent principals are runtime-only and cannot be synced to tickets"}, nil
	}
	path, _, err := findTicketPath(root, id)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.assign: %v", err)}, nil
	}
	if path == "" {
		return Result{Error: fmt.Sprintf("ticket.assign: %s not found", id)}, nil
	}
	bf, err := readBugFile(path)
	if err != nil {
		return Result{Error: fmt.Sprintf("ticket.assign: %v", err)}, nil
	}
	if clear {
		delete(bf.Front, "assignee")
	} else {
		bf.Front["assignee"] = assignee
	}
	if err := writeBugFile(bf); err != nil {
		return Result{Error: fmt.Sprintf("ticket.assign: write: %v", err)}, nil
	}
	return Result{Data: map[string]any{"ok": true, "assignee": assignee}}, nil
}

// ticketListMine implements ticket.list_mine.
//
// Input args:  filter (string — substring match against assignee).
// Output Data: tickets ([]).
//
// "Mine" is whatever assignee matches filter; with an empty filter, every
// bug is returned (callers normally pass world.assignee or $USER).
func ticketListMine(root string, args map[string]any) (Result, error) {
	filter, _ := args["filter"].(string)
	filter = strings.ToLower(strings.TrimSpace(filter))
	bugs, warnings, err := listAllBugs(root)
	if err != nil {
		return Result{Error: fmt.Sprintf("list bugs: %v", err)}, nil
	}
	out := make([]map[string]any, 0, len(bugs))
	for _, b := range bugs {
		assignee := strings.ToLower(b.frontString("assignee"))
		if filter != "" && !strings.Contains(assignee, filter) {
			continue
		}
		row := bugSummary(b)
		annotateLocalTicketPath(b, row)
		out = append(out, row)
	}
	return Result{Data: map[string]any{"tickets": out, "provider_errors": warnings}}, nil
}

// ─── Field accessors / projections ──────────────────────────────────────────

// bugSummary projects a BugFile into the ticket summary shape the
// contract pins: id/title/status/severity/assignee/url. A
// `type` key is added when the lister tagged the row by source dir
// so dev-story can route on `ticket_type`.
//
// `severity` is the on-disk frontmatter field per `issues/README.md`
// and `docs/stories/bugs.md`. The earlier
// summary shape projected `priority` instead; that field has been
// removed entirely — no consumer in this repo branched on
// `t.priority` after the 2026-05-20 dogfood cycle, and keeping a
// dead key alive only invited the kind of "branch on priority,
// silently get ” for every bug" mistake that produced the original
// defect.
func bugSummary(b *BugFile) map[string]any {
	status := strings.TrimSpace(b.frontString("status"))
	if status == "" {
		// Plain Markdown is a valid local ticket. Absence of workflow
		// frontmatter means newly filed/open, not invisible.
		status = "open"
	}
	out := map[string]any{
		"id":       b.ID,
		"title":    b.titleString(),
		"status":   status,
		"severity": b.frontString("severity"),
		"assignee": b.frontString("assignee"),
		"url":      b.frontString("url"),
		// repro_command — optional bug frontmatter carrying a deterministic
		// reproduction command. The bugfix story's `reproducing` room runs it
		// RED-first (non-zero exit = bug reproduces). Absent ⇒ "" ⇒ the story
		// falls through to the LLM-only reproducer (backward compatible).
		"repro_command": b.frontString("repro_command"),
	}
	if b.Kind != "" {
		out["type"] = b.Kind
	}
	return out
}

func annotateLocalTicketPath(b *BugFile, row map[string]any) {
	if b == nil || row == nil {
		return
	}
	path := filepath.ToSlash(b.Path)
	row["source"] = "local"
	row["source_id"] = "local"
	row["source_label"] = "Local"
	row["source_kind"] = "local"
	row["source_mode"] = "local"
	row["source_repo"] = ""
	row["ticket_repo"] = ""
	row["ref"] = "local:" + b.ID
	row["path"] = path
	if strings.TrimSpace(fmt.Sprint(row["url"])) == "" {
		row["url"] = path
	}
}

// severityRank maps a P0–P3 severity tag to a 0–3 sort weight, with
// anything else (empty, unknown) bucketed at 4 so unranked bugs sort
// after every explicit one. Whitespace-tolerant + case-insensitive
// so a hand-typed `severity: p0  ` doesn't slip through.
func severityRank(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "P0":
		return 0
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	}
	return 4
}

// frontString reads a string-valued frontmatter key, defaulting to "" when
// the key is absent or carries a non-string value.
func (b *BugFile) frontString(key string) string {
	if b == nil || b.Front == nil {
		return ""
	}
	if v, ok := b.Front[key].(string); ok {
		return v
	}
	return ""
}

// titleString prefers the `title:` frontmatter; falls back to the first
// markdown heading (`# Title`) in the body.
func (b *BugFile) titleString() string {
	if t := b.frontString("title"); t != "" {
		return t
	}
	for _, ln := range strings.Split(b.Body, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "# "))
		}
	}
	return b.ID
}
