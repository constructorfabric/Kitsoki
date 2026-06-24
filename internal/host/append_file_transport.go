// Package host — host.append_to_file — local-file transport for the
// dogfood path. See docs/case-studies/bug-fix.md.
//
// This is **not** an out-of-band transport (no Jira / GitHub / Slack
// post).  It is the "the bug file IS the conversation thread"
// implementation: every checkpoint artefact is appended to the file
// pointed at by `world.thread` as a `## Comment <ISO> by <author>`
// block.  This keeps the entire transcript inline so a `--continue`
// reattach (or even a plain `cat`) shows the full conversation
// without consulting an external service.
//
// Registered as a bare `host.append_to_file` handler — NOT as a
// `transport.Transport` in the internal/transport registry — because
// the contract names it explicitly:
//
//	host_interfaces:
//	  transport:
//	    default: host.append_to_file
package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AppendFileTransportHandler implements host.append_to_file.
//
// Required args:
//   - thread (string): path to the target file.  Created if missing
//     (with frontmatter `target_kind: thread` so it's still parseable
//     by the local-files ticket handler).
//   - body   (string): the message body.  Appended as a comment block.
//
// Optional args:
//   - author    (string): the comment author tag; default "kitsoki".
//   - title     (string): rendered as a `### <title>` line at the top
//     of the comment body.
//   - phase_id  (string): forwarded to the heading for traceability
//     (`## Comment <ts> by <author> (phase=<id>)`).
//
// Returns Result.Data with:
//   - ok         (bool):   true on successful append.
//   - message_id (string): "<basename-without-ext>#<comment-index>".
//
// Errors as Result.Error (not Go error) for domain failures so
// state-machine on_error: routing handles them.
func AppendFileTransportHandler(ctx context.Context, args map[string]any) (Result, error) {
	thread, _ := args["thread"].(string)
	body, _ := args["body"].(string)
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return Result{Error: "host.append_to_file: thread argument is required"}, nil
	}
	if strings.TrimSpace(body) == "" {
		return Result{Error: "host.append_to_file: body argument is required"}, nil
	}
	author, _ := args["author"].(string)
	if author == "" {
		author = "kitsoki"
	}
	title, _ := args["title"].(string)
	phaseID, _ := args["phase_id"].(string)

	// Make sure the directory exists.  Bug-file thread paths are
	// always under issues/bugs/ which the bug-create CLI creates,
	// but the transport itself shouldn't refuse a path that doesn't
	// exist yet — the dogfood path needs to be self-bootstrapping.
	if err := os.MkdirAll(filepath.Dir(thread), 0o755); err != nil {
		return Result{Error: fmt.Sprintf("host.append_to_file: mkdir: %v", err)}, nil
	}

	// Read existing content (if any) and re-parse it as a bug file so
	// we can append a comment in canonical form.  If the file does
	// not exist yet, we create it with a minimal frontmatter
	// preamble so subsequent reads via host.local_files.ticket still
	// see a well-formed file.
	var bf *BugFile
	if _, statErr := os.Stat(thread); statErr == nil {
		var err error
		bf, err = readBugFile(thread)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.append_to_file: read: %v", err)}, nil
		}
	} else {
		bf = &BugFile{
			ID:   strings.TrimSuffix(filepath.Base(thread), ".md"),
			Path: thread,
			Front: map[string]any{
				"title":    strings.TrimSuffix(filepath.Base(thread), ".md"),
				"status":   "open",
				"filed_at": time.Now().UTC().Format(time.RFC3339),
			},
			Body: "# " + strings.TrimSuffix(filepath.Base(thread), ".md") + "\n",
		}
	}

	commentBody := strings.TrimRight(body, "\n")
	if title != "" {
		commentBody = "### " + title + "\n\n" + commentBody
	}
	if phaseID != "" {
		// Inline a trailing tag rather than mutating the heading
		// regex — readBugFile only looks for the literal `## Comment `
		// prefix and the trailing `by <author>` segment, so we
		// stash phase_id at the foot of the body.
		commentBody = commentBody + "\n\n_phase: " + phaseID + "_"
	}

	bf.Comments = append(bf.Comments, BugComment{
		Timestamp: time.Now().UTC(),
		Author:    author,
		Body:      commentBody,
	})
	bf.Path = thread
	if err := writeBugFile(bf); err != nil {
		return Result{Error: fmt.Sprintf("host.append_to_file: write: %v", err)}, nil
	}
	id := fmt.Sprintf("%s#%d", bf.ID, len(bf.Comments))
	return Result{Data: map[string]any{
		"ok":         true,
		"message_id": id,
	}}, nil
}
