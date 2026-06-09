// Package host — host.artifacts_dir — local-file transport that lands
// each artifact in its own file under a configurable artifacts root.
//
// This complements host.append_to_file (which concatenates every phase
// artifact into a single bug file as comment blocks). The artifacts_dir
// variant writes one file per `thread:` so demo projects with a
// `.artifacts/` folder collect the bugfix / impl / cypilot pipeline
// outputs in discrete files, suitable for `expect_files:` regex asserts
// at the end of a flow.
//
// Wiring: stories/dev-story or a parent rebinds the transport iface to
// host.artifacts_dir via host_bindings. The cake demo's app does this.
//
// # Media artifact emission
//
// When args include "src_path" (a path to an existing file) and "kind"
// (one of video/image/pdf/html/slideshow), the handler switches to a
// media-emit path instead of the markdown-body path:
//   - The source file is copied to <artifacts_root>/<thread>.<ext>
//     (preserving the source extension).
//   - An artifact.emitted journal event is appended when a journal writer
//     is present in the context (injected via WithArtifactJournalWriter).
//   - Returns {ok, handle:{id,kind,mime,label}, path} instead of
//     {ok, path, message_id}.
//
// The existing markdown-body path is unchanged: pass "body" (no "src_path")
// to continue writing markdown chunks.
package host

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kitsoki/internal/journal"
)

// ── Journal writer context plumbing ──────────────────────────────────────────

// artifactJournalWriterKey is the context key for a journal.Writer injected
// so ArtifactsDirTransportHandler can record artifact.emitted events.
type artifactJournalWriterKey struct{}

// WithArtifactJournalWriter returns a child context carrying jw. When
// present, ArtifactsDirTransportHandler will append a journal.KindArtifactEmitted
// entry after every successful media-emit operation. Nil is a safe no-op.
func WithArtifactJournalWriter(ctx context.Context, jw journal.Writer) context.Context {
	if jw == nil {
		return ctx
	}
	return context.WithValue(ctx, artifactJournalWriterKey{}, jw)
}

// artifactJournalWriterFromCtx returns the journal.Writer previously injected
// with WithArtifactJournalWriter, or nil if none was injected.
func artifactJournalWriterFromCtx(ctx context.Context) journal.Writer {
	jw, _ := ctx.Value(artifactJournalWriterKey{}).(journal.Writer)
	return jw
}

// ── Handler ───────────────────────────────────────────────────────────────────

// ArtifactsDirTransportHandler implements host.artifacts_dir.
//
// Required args (markdown-body path):
//   - thread (string): a path-safe filename under artifacts_root. Most
//     callers pass the phase_id (e.g. "reproducing_TKT-200_0") and the
//     handler tacks on `.md` if it's not already there.
//   - body   (string): the message body. Maps/slices are pretty-printed
//     as JSON for readability when the caller passes a structured
//     artifact (the same coercion host.transport.post uses).
//
// Required args (media-emit path — activated when src_path is non-empty):
//   - thread   (string): destination filename stem under artifacts_root
//     (source extension is preserved; .md is NOT appended).
//   - src_path (string): absolute path of the source file to copy.
//   - kind     (string): one of video / image / pdf / html / slideshow.
//
// Optional args (both paths):
//   - artifacts_root (string): override the root. Falls back to
//     $KITSOKI_ARTIFACTS_ROOT, then cwd + "/.artifacts".
//   - mime  (string): MIME type override (media path); auto-detected when absent.
//   - label (string): human-readable display label (media path).
//
// Optional args (markdown-body path only):
//   - title          (string): rendered as a `### <title>` line at the
//     top of the file.
//   - phase_id       (string): inlined at the foot as `_phase: <id>_`.
//   - author         (string): currently informational; the file format
//     here is a single markdown blob, not a bug-file comment chain.
//   - mode           (string): "append" (default) appends a separator +
//     the new content to an existing file; "replace" overwrites.
//
// Returns Result.Data with (markdown-body path):
//   - ok         (bool):   true on successful write.
//   - path       (string): absolute path of the file written.
//   - message_id (string): "<basename-without-ext>#<append-counter>"
//     for parity with host.append_to_file's identifier shape.
//
// Returns Result.Data with (media-emit path):
//   - ok     (bool):   true on successful copy.
//   - path   (string): absolute path of the copied file.
//   - handle (map):    {id, kind, mime, label} for downstream binding.
func ArtifactsDirTransportHandler(ctx context.Context, args map[string]any) (Result, error) {
	thread, _ := args["thread"].(string)
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return Result{Error: "host.artifacts_dir: thread argument is required"}, nil
	}

	// ── Media-emit path ─────────────────────────────────────────────────────
	// Activated when src_path is present and non-empty.  The caller supplies a
	// source file (e.g. a rendered MP4 or PNG) and we copy it under the
	// artifacts root, then record an artifact.emitted journal event.
	srcPath, _ := args["src_path"].(string)
	srcPath = strings.TrimSpace(srcPath)
	if srcPath != "" {
		return handleMediaEmit(ctx, args, thread, srcPath)
	}

	// ── Markdown-body path (unchanged) ──────────────────────────────────────
	body := bodyArg(args, "body")
	if strings.TrimSpace(body) == "" {
		return Result{Error: "host.artifacts_dir: body argument is required"}, nil
	}

	root, err := resolveArtifactsRoot(args)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: %v", err)}, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: mkdir %s: %v", root, err)}, nil
	}

	// Resolve the file path. A thread that already names a path under
	// root is honoured as-is; a bare name without any extension gets
	// `.md` appended; a bare name that already carries an extension
	// (e.g. `foo.json`, `verdict.yaml`) is honoured verbatim so authors
	// can produce non-markdown artifacts via this same handler.
	name := thread
	hasExt := filepath.Ext(name) != ""
	hasSep := strings.ContainsAny(name, string(os.PathSeparator)+"\\")
	if !hasExt && !hasSep {
		name = name + ".md"
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, name)
	}

	mode, _ := args["mode"].(string)
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "append"
	}

	title, _ := args["title"].(string)
	phaseID, _ := args["phase_id"].(string)
	rendered := renderArtifact(title, body, phaseID)

	// Inspect existing content so we know whether to emit a separator
	// (append mode only) and how to number the message_id counter.
	existingBytes := int64(0)
	if info, statErr := os.Stat(path); statErr == nil {
		existingBytes = info.Size()
	} else if !os.IsNotExist(statErr) {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: stat %s: %v", path, statErr)}, nil
	}
	counter := 1
	if existingBytes > 0 {
		// Best-effort count of prior chunks via the canonical separator.
		// Stays advisory — used purely to build a unique message_id.
		raw, _ := os.ReadFile(path)
		counter = strings.Count(string(raw), "\n---\n") + 2
	}

	switch mode {
	case "replace":
		if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
			return Result{Error: fmt.Sprintf("host.artifacts_dir: write %s: %v", path, err)}, nil
		}
		counter = 1
	default:
		// append — write a "\n\n---\n\n" separator before the new chunk
		// when the file already has content.
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.artifacts_dir: open %s: %v", path, err)}, nil
		}
		defer f.Close()
		if existingBytes > 0 {
			if _, err := f.WriteString("\n\n---\n\n"); err != nil {
				return Result{Error: fmt.Sprintf("host.artifacts_dir: write sep: %v", err)}, nil
			}
		}
		if _, err := f.WriteString(rendered); err != nil {
			return Result{Error: fmt.Sprintf("host.artifacts_dir: write body: %v", err)}, nil
		}
	}

	base := filepath.Base(path)
	if ext := filepath.Ext(base); ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	id := fmt.Sprintf("%s#%d", base, counter)
	return Result{Data: map[string]any{
		"ok":         true,
		"path":       path,
		"message_id": id,
	}}, nil
}

// resolveArtifactsRoot picks the directory under which artifact files
// land. Order of precedence: args.artifacts_root → $KITSOKI_ARTIFACTS_ROOT
// → cwd + "/.artifacts".
func resolveArtifactsRoot(args map[string]any) (string, error) {
	if v, _ := args["artifacts_root"].(string); strings.TrimSpace(v) != "" {
		return v, nil
	}
	if v := os.Getenv("KITSOKI_ARTIFACTS_ROOT"); v != "" {
		return v, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	return filepath.Join(cwd, ".artifacts"), nil
}

// handleMediaEmit implements the media-emit branch of ArtifactsDirTransportHandler.
// It is called when args["src_path"] is non-empty.
//
// Steps:
//  1. Validate src_path exists and is a regular file.
//  2. Resolve the artifacts root and ensure it exists.
//  3. Derive the destination path as <root>/<thread><src_ext>.
//  4. Validate the destination stays under the root (path-traversal guard).
//  5. Copy the source to the destination.
//  6. Build an ArtifactEvent and append it to the journal (if wired).
//  7. Return {ok, path, handle:{id,kind,mime,label}}.
func handleMediaEmit(ctx context.Context, args map[string]any, thread, srcPath string) (Result, error) {
	kind, _ := args["kind"].(string)
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return Result{Error: "host.artifacts_dir: kind argument is required when src_path is set (one of video/image/pdf/html/slideshow)"}, nil
	}
	switch kind {
	case "video", "image", "pdf", "html", "slideshow":
		// valid
	default:
		return Result{Error: fmt.Sprintf("host.artifacts_dir: unsupported kind %q (must be video/image/pdf/html/slideshow)", kind)}, nil
	}

	// Validate the source file.
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: src_path %q: %v", srcPath, err)}, nil
	}
	if !srcInfo.Mode().IsRegular() {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: src_path %q is not a regular file", srcPath)}, nil
	}

	root, err := resolveArtifactsRoot(args)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: %v", err)}, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: mkdir %s: %v", root, err)}, nil
	}

	// Destination path: <root>/<thread><src_ext>
	// The thread arg is used as the stem; we always preserve the source extension
	// so the file is recognised correctly by downstream tools.
	ext := filepath.Ext(srcPath)
	stem := thread
	// If thread already carries the same extension, don't double-append it.
	if filepath.Ext(stem) == ext {
		ext = ""
	}
	destName := stem + ext
	destPath := filepath.Join(root, destName)

	// Path-traversal guard: the resolved destination must be under root.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: abs root: %v", err)}, nil
	}
	absDestPath, err := filepath.Abs(destPath)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: abs dest: %v", err)}, nil
	}
	if !strings.HasPrefix(absDestPath, absRoot+string(os.PathSeparator)) {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: destination path escapes artifacts root: %q", destPath)}, nil
	}

	// Copy the source file to the destination.
	if err := copyFile(srcPath, absDestPath); err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: copy %q -> %q: %v", srcPath, absDestPath, err)}, nil
	}

	// Stat the destination to get the final size.
	destInfo, err := os.Stat(absDestPath)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.artifacts_dir: stat dest: %v", err)}, nil
	}

	// Build a stable handle ID.  We use a content-hash prefix of the destination
	// path (not the file itself — fast and deterministic) combined with the
	// stem name to give something human-readable.
	baseStem := strings.TrimSuffix(filepath.Base(absDestPath), filepath.Ext(absDestPath))
	id := artifactHandleID(baseStem, absDestPath)

	// Resolve MIME type: explicit arg wins, then extension-based detection,
	// then content sniff, then fallback to application/octet-stream.
	mimeType, _ := args["mime"].(string)
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = detectMIME(absDestPath)
	}

	label, _ := args["label"].(string)

	// Append a journal event when a writer is wired.
	if jw := artifactJournalWriterFromCtx(ctx); jw != nil {
		ev := journal.ArtifactEvent{
			ID:        id,
			Kind:      kind,
			Mime:      mimeType,
			Label:     label,
			Path:      absDestPath,
			Producer:  "host.artifacts_dir",
			SizeBytes: destInfo.Size(),
			CreatedAt: time.Now().UTC(),
		}
		body, merr := json.Marshal(ev)
		if merr == nil {
			_ = jw.Append(journal.Entry{
				Kind: journal.KindArtifactEmitted,
				Body: body,
			})
		}
	}

	handle := map[string]any{
		"id":    id,
		"kind":  kind,
		"mime":  mimeType,
		"label": label,
	}
	return Result{Data: map[string]any{
		"ok":     true,
		"path":   absDestPath,
		"handle": handle,
	}}, nil
}

// copyFile copies src to dst, creating dst if it does not exist or truncating
// it if it does.  Parent directory of dst must already exist.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// artifactHandleID returns a stable, human-readable handle ID for a media
// artifact, of the form "<stem>#<short-hash>" where the short hash is the
// first 8 hex characters of the SHA-256 of the destination path string.
// Using the path (not the file contents) keeps this fast and idempotent for
// the same destination regardless of file content changes.
func artifactHandleID(stem, destPath string) string {
	h := sha256.Sum256([]byte(destPath))
	return fmt.Sprintf("%s#%x", stem, h[:4])
}

// detectMIME returns a MIME type for path by:
//  1. Asking mime.TypeByExtension for the file extension.
//  2. Falling back to http.DetectContentType on the first 512 bytes.
//  3. Returning "application/octet-stream" if nothing is found.
func detectMIME(path string) string {
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}

// renderArtifact composes a single markdown blob for one artifact write.
// Mirrors host.append_to_file's body shape so a reader can move between
// the two transports without surprise.
func renderArtifact(title, body, phaseID string) string {
	var sb strings.Builder
	if title != "" {
		sb.WriteString("### ")
		sb.WriteString(title)
		sb.WriteString("\n\n")
	}
	sb.WriteString(strings.TrimRight(body, "\n"))
	if phaseID != "" {
		sb.WriteString("\n\n_phase: ")
		sb.WriteString(phaseID)
		sb.WriteString("_")
	}
	sb.WriteString("\n")
	return sb.String()
}

// jsonPretty is a package-private no-op reference that keeps the json
// import live for tooling that may want to grow this file's body
// coercion. The shared body coercion lives on transport_post.go's
// bodyArg helper.
var _ = json.MarshalIndent
