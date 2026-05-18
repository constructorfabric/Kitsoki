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
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ArtifactsDirTransportHandler implements host.artifacts_dir.
//
// Required args:
//   - thread (string): a path-safe filename under artifacts_root. Most
//     callers pass the phase_id (e.g. "reproducing_TKT-200_0") and the
//     handler tacks on `.md` if it's not already there.
//   - body   (string): the message body. Maps/slices are pretty-printed
//     as JSON for readability when the caller passes a structured
//     artifact (the same coercion host.transport.post uses).
//
// Optional args:
//   - artifacts_root (string): override the root. Falls back to
//     $KITSOKI_ARTIFACTS_ROOT, then cwd + "/.artifacts".
//   - title          (string): rendered as a `### <title>` line at the
//     top of the file.
//   - phase_id       (string): inlined at the foot as `_phase: <id>_`.
//   - author         (string): currently informational; the file format
//     here is a single markdown blob, not a bug-file comment chain.
//   - mode           (string): "append" (default) appends a separator +
//     the new content to an existing file; "replace" overwrites.
//
// Returns Result.Data with:
//   - ok         (bool):   true on successful write.
//   - path       (string): absolute path of the file written.
//   - message_id (string): "<basename-without-ext>#<append-counter>"
//     for parity with host.append_to_file's identifier shape.
func ArtifactsDirTransportHandler(ctx context.Context, args map[string]any) (Result, error) {
	thread, _ := args["thread"].(string)
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return Result{Error: "host.artifacts_dir: thread argument is required"}, nil
	}

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
	// root is honoured as-is; a bare name gets `.md` and is joined under
	// root.
	name := thread
	if !strings.HasSuffix(name, ".md") && !strings.ContainsAny(name, string(os.PathSeparator)+"\\") {
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

	id := fmt.Sprintf("%s#%d", strings.TrimSuffix(filepath.Base(path), ".md"), counter)
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
