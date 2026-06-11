// Package host — host.slidey.render + host.contact_sheet — visual output producers.
//
// These handlers drive the two external tools used by the visual-outputs
// epic: the slidey declarative-video pipeline and ffmpeg's tile filter for
// contact-sheet generation. Both follow the same graceful-degradation
// contract as other shelling handlers: when the tool is not present on the
// host the handler returns a clear Result.Error (never a Go error / panic),
// so story `on_error:` arcs can branch appropriately rather than crashing.
//
// # host.slidey.render
//
// Validates a JSON scene spec with `node <slidey_home>/src/index.js --validate`
// then renders it to the requested format.  Tool discovery order:
//  1. $SLIDEY_HOME env var (explicit override).
//  2. PATH lookup for a `slidey` binary (wrapper script / global install).
//
// # host.contact_sheet
//
// Assembles a PNG montage from a directory of PNG frames using ffmpeg's
// tile filter. Suitable as a quick visual summary of a rendered video's
// key frames.
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ── host.slidey.render ────────────────────────────────────────────────────────

// SlideyRenderHandler implements host.slidey.render.
//
// Input args:
//   - spec_path   (string, required): path to the JSON scene spec.
//   - format      (string, required): one of "mp4", "pdf", "html".
//   - output_path (string, optional): destination file. Defaults to
//     <spec_path without extension>.<format>.
//
// Output Result.Data:
//   - ok        (bool):   true when the render completed with exit code 0.
//   - path      (string): absolute path of the rendered output file.
//   - mime      (string): MIME type for the chosen format.
//   - kind      (string): media kind — "video", "pdf", or "html".
//   - exit_code (int):    raw exit code from the slidey process.
//   - stdout    (string): combined stdout.
//   - stderr    (string): combined stderr.
//
// On missing tool or validation failure Result.Error is set (no Go error).
func SlideyRenderHandler(ctx context.Context, args map[string]any) (Result, error) {
	specPath, _ := args["spec_path"].(string)
	specPath = strings.TrimSpace(specPath)
	if specPath == "" {
		return Result{Error: "host.slidey.render: spec_path argument is required"}, nil
	}

	format, _ := args["format"].(string)
	format = strings.TrimSpace(strings.ToLower(format))
	switch format {
	case "mp4", "pdf", "html":
		// valid
	case "":
		return Result{Error: "host.slidey.render: format argument is required (mp4|pdf|html)"}, nil
	default:
		return Result{Error: fmt.Sprintf("host.slidey.render: unsupported format %q (must be mp4, pdf, or html)", format)}, nil
	}

	// Resolve output path.
	outputPath, _ := args["output_path"].(string)
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		ext := filepath.Ext(specPath)
		outputPath = strings.TrimSuffix(specPath, ext) + "." + format
	}

	// Discover the slidey entry point.
	slideyPath, useNode, err := resolveSlideyScript()
	if err != nil {
		return Result{Error: fmt.Sprintf("host.slidey.render: %v", err)}, nil
	}

	// buildArgs returns the program and argument slice for a slidey invocation.
	// When useNode is true the entry point is a raw .js file that must be run
	// via node; when false slideyPath is an executable wrapper invoked directly.
	buildArgs := func(extra ...string) (string, []string) {
		if useNode {
			return "node", append([]string{slideyPath}, extra...)
		}
		return slideyPath, extra
	}

	// Step 1 — validate.
	validateProg, validateArgs := buildArgs("--validate", specPath)
	valStdout, valStderr, valCode, valErr := cliExec(ctx, "", validateProg, validateArgs...)
	if valErr != nil {
		return Result{Error: fmt.Sprintf("host.slidey.render: validation exec: %v", valErr)}, nil
	}
	if valCode != 0 {
		combined := strings.TrimSpace(valStdout + "\n" + valStderr)
		return Result{Error: fmt.Sprintf("host.slidey.render: spec validation failed: %s", combined)}, nil
	}

	// Step 2 — render.
	renderProg, renderArgs := buildArgs(specPath, outputPath)
	stdout, stderr, exitCode, execErr := cliExec(ctx, "", renderProg, renderArgs...)
	if execErr != nil {
		return Result{Error: fmt.Sprintf("host.slidey.render: render exec: %v", execErr)}, nil
	}

	absOutput, _ := filepath.Abs(outputPath)
	mimeType, kind := slideyMIME(format)

	data := map[string]any{
		"ok":        exitCode == 0,
		"path":      absOutput,
		"mime":      mimeType,
		"kind":      kind,
		"exit_code": exitCode,
		"stdout":    stdout,
		"stderr":    stderr,
	}
	if exitCode != 0 {
		combined := strings.TrimSpace(stdout + "\n" + stderr)
		return Result{
			Error: fmt.Sprintf("host.slidey.render: render exited %d: %s", exitCode, combined),
			Data:  data,
		}, nil
	}

	// Emit the producer-agnostic chapter sidecar beside a rendered video
	// (mockup-video-studio epic, Slice 1). Best-effort: a sidecar failure
	// never fails an otherwise-successful render — chapters are additive
	// (a video without a sidecar still plays). Only meaningful for video.
	if kind == "video" {
		if sidecar, sErr := writeSlideyChapters(specPath, absOutput); sErr == nil {
			data["chapters_path"] = sidecar
		} else {
			data["chapters_error"] = sErr.Error()
		}
	}

	return Result{Data: data}, nil
}

// resolveSlideyScript discovers the slidey entry point. Discovery order:
//  1. $SLIDEY_HOME/src/index.js (explicit env override) — useNode=true.
//  2. `slidey` on PATH (wrapper script / global install) — useNode=false.
//
// The second return value useNode indicates whether the caller must prepend
// "node" when building the argv: true for a raw .js script (path 1), false
// for a PATH-installed executable wrapper (path 2).
//
// Returns an error when neither is available.
func resolveSlideyScript() (path string, useNode bool, err error) {
	if home := os.Getenv("SLIDEY_HOME"); home != "" {
		candidate := filepath.Join(home, "src", "index.js")
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, true, nil
		}
		return "", false, fmt.Errorf("slidey not found: SLIDEY_HOME=%q set but %q does not exist", home, candidate)
	}
	if p, lookErr := exec.LookPath("slidey"); lookErr == nil {
		// A `slidey` wrapper is on PATH — exec it directly without node.
		return p, false, nil
	}
	return "", false, fmt.Errorf("slidey not found: set SLIDEY_HOME to the slidey checkout or add `slidey` to PATH")
}

// slideyMIME returns the MIME type and media kind for a given render format.
func slideyMIME(format string) (mime, kind string) {
	switch format {
	case "mp4":
		return "video/mp4", "video"
	case "pdf":
		return "application/pdf", "pdf"
	case "html":
		return "text/html", "html"
	default:
		return "application/octet-stream", "unknown"
	}
}

// ── host.contact_sheet ────────────────────────────────────────────────────────

// ContactSheetHandler implements host.contact_sheet.
//
// Assembles a PNG contact sheet (montage) from the PNG frames found in
// dir using ffmpeg's tile filter. Useful as a quick per-scene visual
// summary of a rendered video.
//
// Input args:
//   - dir         (string, required): directory containing PNG frames.
//   - glob        (string, optional): glob pattern for frame selection within
//     dir. Defaults to "*.png".
//   - cols        (int,    optional): number of columns in the tile grid.
//     Defaults to 4.
//   - tile_width  (int,    optional): width of each tile in pixels.
//     Defaults to 320.
//   - output_path (string, optional): destination PNG path. Defaults to
//     <dir>/contact_sheet.png.
//
// Output Result.Data:
//   - ok   (bool):   true when ffmpeg completed with exit code 0.
//   - path (string): absolute path of the generated PNG.
//   - mime (string): "image/png".
//   - kind (string): "image".
//
// Result.Error is set (no Go error) when ffmpeg is absent or fails.
func ContactSheetHandler(ctx context.Context, args map[string]any) (Result, error) {
	dir, _ := args["dir"].(string)
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return Result{Error: "host.contact_sheet: dir argument is required"}, nil
	}

	glob, _ := args["glob"].(string)
	if glob == "" {
		glob = "*.png"
	}

	cols := 4
	if v := intArg(args, "cols"); v > 0 {
		cols = v
	}

	tileWidth := 320
	if v := intArg(args, "tile_width"); v > 0 {
		tileWidth = v
	}

	outputPath, _ := args["output_path"].(string)
	outputPath = strings.TrimSpace(outputPath)
	if outputPath == "" {
		outputPath = filepath.Join(dir, "contact_sheet.png")
	}

	// Verify ffmpeg is available.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return Result{Error: "host.contact_sheet: ffmpeg not found — install ffmpeg and ensure it is on PATH"}, nil
	}

	// Resolve the matching frame files.
	pattern := filepath.Join(dir, glob)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.contact_sheet: glob %q: %v", pattern, err)}, nil
	}
	if len(matches) == 0 {
		return Result{Error: fmt.Sprintf("host.contact_sheet: no files matched %q", pattern)}, nil
	}

	// Build a concat demuxer input list so ffmpeg processes frames in sorted
	// order without relying on shell glob expansion.
	concatList, cleanupFn, err := writeConcatList(matches)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.contact_sheet: write concat list: %v", err)}, nil
	}
	defer cleanupFn()

	// ffmpeg tile filter invocation:
	//   ffmpeg -f concat -safe 0 -i <list> \
	//          -frames:v 1 \
	//          -vf "scale=<tile_width>:-1,tile=<cols>x<rows>" \
	//          <output_path>
	rows := (len(matches) + cols - 1) / cols
	tileFilter := fmt.Sprintf("scale=%d:-1,tile=%dx%d", tileWidth, cols, rows)

	ffmpegArgs := []string{
		"-f", "concat",
		"-safe", "0",
		"-i", concatList,
		"-frames:v", "1",
		"-vf", tileFilter,
		"-y", // overwrite output
		outputPath,
	}

	stdout, stderr, exitCode, execErr := cliExec(ctx, "", "ffmpeg", ffmpegArgs...)
	if execErr != nil {
		return Result{Error: fmt.Sprintf("host.contact_sheet: ffmpeg exec: %v", execErr)}, nil
	}

	absOutput, _ := filepath.Abs(outputPath)

	data := map[string]any{
		"ok":   exitCode == 0,
		"path": absOutput,
		"mime": "image/png",
		"kind": "image",
	}
	if exitCode != 0 {
		combined := strings.TrimSpace(stdout + "\n" + stderr)
		return Result{
			Error: fmt.Sprintf("host.contact_sheet: ffmpeg exited %d: %s", exitCode, combined),
			Data:  data,
		}, nil
	}
	return Result{Data: data}, nil
}

// writeConcatList writes a temporary ffmpeg concat demuxer input file
// listing each path. Returns the temp-file path and a cleanup function
// (always safe to call).
func writeConcatList(paths []string) (string, func(), error) {
	f, err := os.CreateTemp("", "kitsoki-contact-*.txt")
	if err != nil {
		return "", func() {}, err
	}
	for _, p := range paths {
		abs, aerr := filepath.Abs(p)
		if aerr != nil {
			abs = p
		}
		if _, werr := fmt.Fprintf(f, "file '%s'\n", abs); werr != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return "", func() {}, werr
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, err
	}
	name := f.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

// intArg extracts an int-valued arg, accepting both int and float64 (the
// shape YAML-decoded numbers arrive as after goccy/go-yaml unmarshalling).
func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}
