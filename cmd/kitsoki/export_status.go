// export_status.go — implements the `kitsoki export-status` subcommand, which
// exports a run as a self-contained Snapshot artifact. The -o extension picks
// the format: ".html" emits the bundled runstatus SPA with the snapshot
// inlined (opens over file://), anything else emits raw Snapshot JSON.
//
// Two input modes:
//
//  1. --from-trace <path.jsonl> --app <app.yaml> [overrides]
//     Reads a recorded JSONL trace, loads the AppDef, and builds a Snapshot
//     via runstatus.SnapshotFromTrace. The fixture-generator path.
//
//  2. --from-snapshot <path.json>
//     Wraps an existing Snapshot JSON in the bundled SPA as a self-contained
//     HTML artifact (requires -o *.html). The Go replacement for the former
//     tools/runstatus/scripts/build-artifact.mjs.
//
// For a live, updating view of an in-progress run, see `kitsoki status serve`
// (cmd/kitsoki/status_serve.go) and docs/tracing/run-status-ui.md.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/web"
)

func exportStatusCmd() *cobra.Command {
	var (
		fromTrace    string
		fromSnapshot string
		appPath      string
		currentState string
		sessionID    string
		startedAt    string
		outPath      string
		withMermaid  bool
	)

	cmd := &cobra.Command{
		Use:   "export-status",
		Short: "Export a run snapshot as a self-contained JSON or HTML artifact",
		Long: `Export a kitsoki run as a self-contained Snapshot — either the raw
Snapshot JSON or a single-file HTML artifact with the runstatus SPA inlined.
The output format is chosen by the -o extension: ".html" emits the bundled
UI, anything else emits Snapshot JSON. HTML output needs the SPA bundled into
the binary (run 'make build', which runs 'pnpm build' under tools/runstatus/).

From a recorded trace (the fixture-generator path):
  kitsoki export-status --from-trace run.jsonl --app myapp.yaml -o status.snapshot.json
  kitsoki export-status --from-trace run.jsonl --app myapp.yaml -o status.html

  Options for --from-trace:
    --current-state <path>   override current state (default: derived from last trace event)
    --session-id <id>        override session ID (default: derived from trace)
    --started-at <iso8601>   override start time (default: earliest trace event time)

From an existing Snapshot JSON (wrap it as a self-contained HTML artifact —
this is the Go replacement for the old scripts/build-artifact.mjs):
  kitsoki export-status --from-snapshot status.snapshot.json -o status.html

For a live, updating view of an in-progress run, use 'kitsoki status serve'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outPath == "" {
				return fmt.Errorf("--out / -o is required")
			}
			if fromTrace != "" && fromSnapshot != "" {
				return fmt.Errorf("--from-trace and --from-snapshot are mutually exclusive")
			}

			// ── --from-snapshot mode (snapshot JSON → HTML artifact) ───────
			if fromSnapshot != "" {
				return runExportFromSnapshot(fromSnapshot, outPath)
			}

			// ── --from-trace mode ─────────────────────────────────────────
			if fromTrace != "" {
				return runExportFromTrace(fromTrace, appPath, currentState, sessionID, startedAt, outPath, withMermaid)
			}

			return fmt.Errorf("one of --from-trace or --from-snapshot is required " +
				"(for a live view use `kitsoki status serve`)")
		},
	}

	cmd.Flags().StringVar(&fromTrace, "from-trace", "", "path to a JSONL trace file produced by kitsoki run --trace")
	cmd.Flags().StringVar(&fromSnapshot, "from-snapshot", "", "path to an existing Snapshot JSON to wrap as a self-contained HTML artifact (requires -o *.html)")
	cmd.Flags().StringVar(&appPath, "app", "", "path to the app.yaml (required with --from-trace)")
	cmd.Flags().StringVar(&currentState, "current-state", "", "override current state path (dotted, e.g. bar.dark)")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "override session ID")
	cmd.Flags().StringVar(&startedAt, "started-at", "", "override session start time (RFC3339, e.g. 2026-05-25T10:00:00Z)")
	cmd.Flags().StringVarP(&outPath, "out", "o", "", "output file path (required); .html emits the bundled UI, otherwise Snapshot JSON")
	cmd.Flags().BoolVar(&withMermaid, "with-mermaid", true, "populate mermaid.source and mermaid.node_map (default true when --from-trace is used)")

	return cmd
}

// runExportFromTrace reads a JSONL trace and an app.yaml, synthesises a
// Snapshot, and writes it as indented JSON (or, for an .html out path, as a
// self-contained HTML artifact) to outPath.
func runExportFromTrace(tracePath, appPath, currentStateFlag, sessionIDFlag, startedAtFlag, outPath string, withMermaid bool) error {
	if appPath == "" {
		return fmt.Errorf("--app is required with --from-trace")
	}

	// ── Parse trace events ────────────────────────────────────────────────
	f, err := os.Open(tracePath)
	if err != nil {
		return fmt.Errorf("open trace %q: %w", tracePath, err)
	}
	defer func() { _ = f.Close() }()
	events, err := runstatus.ParseTrace(f, func(line int, perr error) {
		fmt.Fprintf(os.Stderr, "export-status: skip trace line %d: %v\n", line, perr)
	})
	if err != nil {
		return fmt.Errorf("parse trace %q: %w", tracePath, err)
	}

	// ── Load AppDef ───────────────────────────────────────────────────────
	// Use loadAppWithEnv so KITSOKI_APP_DIR is published before Load; this
	// matches the pattern used by runCmd, vizCmd, etc.
	def, err := loadAppWithEnv(appPath)
	if err != nil {
		return err
	}

	// ── Build Snapshot ────────────────────────────────────────────────────
	// SnapshotFromTrace aggregates task detail, synthesises the header (flag
	// overrides win), and renders the diagram (--with-mermaid). The UI degrades
	// gracefully when the diagram is empty, so --with-mermaid=false is a valid
	// way to produce a lighter fixture.
	snap := runstatus.SnapshotFromTrace(def, events, runstatus.HeaderOverrides{
		SessionID:    sessionIDFlag,
		CurrentState: currentStateFlag,
		StartedAt:    startedAtFlag,
	}, withMermaid)

	// ── HTML output ─────────────────────────────────────────────────────
	// When -o is *.html, marshal the in-memory snapshot and wrap it in the
	// bundled SPA. The trace path has no prompt sidecars (prompts are inline
	// in the events already), so no SidecarDir is set.
	if hasHTMLExt(outPath) {
		snapJSON, err := json.Marshal(snap)
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}
		return writeHTMLArtifact(snapJSON, runstatus.ArtifactOptions{
			Name:    artifactBaseName(outPath),
			Commit:  gitShort("HEAD"),
			Branch:  gitBranch(),
			BuiltAt: time.Now(),
		}, outPath)
	}

	// ── JSON output ───────────────────────────────────────────────────────
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("create output directory: %w", err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create output file %q: %w", outPath, err)
	}
	defer func() { _ = out.Close() }()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	return nil
}

// runExportFromSnapshot reads an existing Snapshot JSON file and wraps it in a
// self-contained HTML artifact. This is the Go replacement for the old
// tools/runstatus/scripts/build-artifact.mjs: it inlines prompt sidecars
// (resolved relative to the snapshot's directory) and injects the snapshot
// into the bundled SPA. Requires an .html output path.
func runExportFromSnapshot(snapshotPath, outPath string) error {
	if !hasHTMLExt(outPath) {
		return fmt.Errorf("--from-snapshot requires an .html output path (e.g. -o status.html); "+
			"got %q", outPath)
	}
	snapJSON, err := os.ReadFile(snapshotPath)
	if err != nil {
		return fmt.Errorf("read snapshot %q: %w", snapshotPath, err)
	}

	root := repoRoot(snapshotPath)
	opts := runstatus.ArtifactOptions{
		Name:         artifactBaseName(outPath),
		Commit:       gitShort("HEAD"),
		Branch:       gitBranch(),
		BuiltAt:      time.Now(),
		SidecarDir:   filepath.Dir(snapshotPath),
		RegenComment: runstatus.RegenComment(relTo(root, snapshotPath), relTo(root, outPath)),
	}
	return writeHTMLArtifact(snapJSON, opts, outPath)
}

// writeHTMLArtifact renders snapshotJSON into the bundled SPA and writes the
// resulting self-contained HTML to outPath (creating parent dirs).
func writeHTMLArtifact(snapshotJSON []byte, opts runstatus.ArtifactOptions, outPath string) error {
	index, err := web.IndexHTML()
	if err != nil {
		return err
	}
	html, err := runstatus.RenderArtifact(index, snapshotJSON, opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(outPath, html, 0o644); err != nil {
		return fmt.Errorf("write artifact %q: %w", outPath, err)
	}
	return nil
}

// hasHTMLExt reports whether path ends in .html (case-insensitive).
func hasHTMLExt(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".html")
}

// artifactBaseName returns the output file name without its extension, used as
// the banner "fixture:" label.
func artifactBaseName(outPath string) string {
	base := filepath.Base(outPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// gitShort returns `git rev-parse --short <rev>` or "" on failure.
func gitShort(rev string) string { return gitOutput("rev-parse", "--short", rev) }

// gitBranch returns the current branch name or "" on failure.
func gitBranch() string { return gitOutput("rev-parse", "--abbrev-ref", "HEAD") }

// gitOutput runs git with args and returns trimmed stdout, or "" on any error.
func gitOutput(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoRoot returns the git top-level for the directory containing ref, falling
// back to that directory when git is unavailable.
func repoRoot(ref string) string {
	dir := filepath.Dir(ref)
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	if out, err := cmd.Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return dir
}

// relTo returns target relative to base, or target unchanged if that fails.
func relTo(base, target string) string {
	absBase, err1 := filepath.Abs(base)
	absTarget, err2 := filepath.Abs(target)
	if err1 != nil || err2 != nil {
		return target
	}
	if rel, err := filepath.Rel(absBase, absTarget); err == nil {
		return rel
	}
	return target
}
