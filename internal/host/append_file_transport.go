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
//   - workdir   (string): root for relative committed-ticket thread paths.
//     Bugfix runs pass their per-run workspace here so `issues/bugs/...`
//     checkpoint mirrors do not write into the driving process's checkout.
//     Local artifact tickets under `.artifacts/issues/...` intentionally stay
//     rooted in the driving process's artifacts directory because that file is
//     the durable local ticket.
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

	workdir, _ := args["workdir"].(string)
	threadPath := appendFileThreadPath(thread, workdir)
	mirrorOnWriteFailure := appendFileShouldMirrorOnWriteFailure(thread, workdir, threadPath)

	res, fallbackable := appendFileTransportWrite(threadPath, thread, body, author, title, phaseID)
	if res.Error == "" || !fallbackable || !mirrorOnWriteFailure {
		return res, nil
	}

	mirrorPath := appendFileTempMirrorPath(thread)
	mirrorRes, _ := appendFileTransportWrite(mirrorPath, thread, body, author, title, phaseID)
	if mirrorRes.Error != "" {
		return Result{Error: fmt.Sprintf("%s; mirror fallback failed: %s", res.Error, mirrorRes.Error)}, nil
	}
	return mirrorRes, nil
}

func appendFileTransportWrite(threadPath, thread, body, author, title, phaseID string) (Result, bool) {
	// Make sure the directory exists.  Bug-file thread paths are
	// always under issues/bugs/ which the bug-create CLI creates,
	// but the transport itself shouldn't refuse a path that doesn't
	// exist yet — the dogfood path needs to be self-bootstrapping.
	if err := os.MkdirAll(filepath.Dir(threadPath), 0o755); err != nil {
		return Result{Error: fmt.Sprintf("host.append_to_file: mkdir: %v", err)}, true
	}

	// Read existing content (if any) and re-parse it as a bug file so
	// we can append a comment in canonical form.  If the file does
	// not exist yet, we create it with a minimal frontmatter
	// preamble so subsequent reads via host.local_files.ticket still
	// see a well-formed file.
	var bf *BugFile
	if _, statErr := os.Stat(threadPath); statErr == nil {
		var err error
		bf, err = readBugFile(threadPath)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.append_to_file: read: %v", err)}, os.IsPermission(err)
		}
	} else {
		bf = &BugFile{
			ID:   strings.TrimSuffix(filepath.Base(threadPath), ".md"),
			Path: threadPath,
			Front: map[string]any{
				"title":    strings.TrimSuffix(filepath.Base(threadPath), ".md"),
				"status":   "open",
				"filed_at": time.Now().UTC().Format(time.RFC3339),
			},
			Body: "# " + strings.TrimSuffix(filepath.Base(threadPath), ".md") + "\n",
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
	bf.Path = threadPath
	if err := writeBugFile(bf); err != nil {
		return Result{Error: fmt.Sprintf("host.append_to_file: write: %v", err)}, true
	}
	id := fmt.Sprintf("%s#%d", bf.ID, len(bf.Comments))
	return Result{Data: map[string]any{
		"ok":         true,
		"message_id": id,
	}}, false
}

func appendFileThreadPath(thread, workdir string) string {
	thread = strings.TrimSpace(thread)
	if isAppendFileLocalPath(thread) {
		if !filepath.IsAbs(thread) {
			if isAppendFileArtifactThread(thread) {
				return thread
			}
			if workdir = strings.TrimSpace(workdir); workdir != "" {
				// Only root the thread in the workdir when the workdir actually
				// exists. Appending must never RESURRECT a removed worktree as a
				// plain directory — the handler mkdirs the thread's parents, and
				// a phantom dir at the worktree path makes the idempotent
				// `git worktree add` re-create fail with "already exists"
				// (continue-after-worktree-loss, dogfood smoke).
				if st, err := os.Stat(workdir); err == nil && st.IsDir() {
					return filepath.Join(workdir, thread)
				}
				if isAppendFileBugThread(thread) {
					return appendFileTempMirrorPath(thread)
				}
			}
			if isAppendFileBugThread(thread) {
				return appendFileTempMirrorPath(thread)
			}
		}
		return thread
	}
	return appendFileTempMirrorPath(thread)
}

func appendFileShouldMirrorOnWriteFailure(thread, workdir, threadPath string) bool {
	thread = strings.TrimSpace(thread)
	workdir = strings.TrimSpace(workdir)
	if workdir == "" || thread == "" || filepath.IsAbs(thread) || !isAppendFileLocalPath(thread) {
		return false
	}
	return threadPath != appendFileTempMirrorPath(thread)
}

func appendFileTempMirrorPath(thread string) string {
	return filepath.Join(os.TempDir(), "kitsoki-append-to-file", sanitizeAppendThreadName(thread)+".md")
}

func isAppendFileLocalPath(thread string) bool {
	if strings.Contains(thread, "://") {
		return false
	}
	if filepath.IsAbs(thread) || strings.HasSuffix(thread, ".md") {
		return true
	}
	return strings.ContainsAny(thread, `/\`)
}

func isAppendFileBugThread(thread string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(thread)))
	for _, prefix := range []string{
		"issues/bugs/",
		".artifacts/issues/bugs/",
		".artifacts/issues/features/",
		".artifacts/issues/epics/",
	} {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

func isAppendFileArtifactThread(thread string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(thread)))
	for _, prefix := range []string{
		".artifacts/issues/bugs/",
		".artifacts/issues/features/",
		".artifacts/issues/epics/",
	} {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

func sanitizeAppendThreadName(thread string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, thread)
	name = strings.Trim(name, "-.")
	if name == "" {
		name = "thread"
	}
	if len(name) > 120 {
		name = name[:120]
		name = strings.Trim(name, "-.")
		if name == "" {
			name = "thread"
		}
	}
	return name
}
