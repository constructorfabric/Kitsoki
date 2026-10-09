package bugdeck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SpecFilename is the name Produce writes the slidey spec under inside the work
// directory.
const SpecFilename = "deck.slidey.json"

// Renderer turns a slidey spec file into a single self-contained HTML deck. It
// is the only seam in this package that touches a subprocess, so it is injected
// to keep BuildSpec/Produce unit-testable without node or slidey installed.
type Renderer interface {
	// Bundle renders the spec at specPath into a standalone HTML file at outPath.
	// Implementations MUST inline all assets (the rrweb clip) so outPath is
	// independently serveable.
	Bundle(ctx context.Context, specPath, outPath string) error
}

// Produce is the deterministic, no-LLM pipeline: build the spec from evidence,
// stage it (+ the rrweb clip) under workdir, and render a self-contained HTML
// deck. It returns the absolute path to the written HTML.
//
// workdir must already exist (callers typically pass a fresh temp dir). The
// rendered deck embeds every asset, so it can be moved/served from anywhere.
func Produce(ctx context.Context, ev Evidence, r Renderer, workdir string) (htmlPath string, err error) {
	if r == nil {
		return "", fmt.Errorf("bugdeck: nil Renderer")
	}
	spec, clip, err := BuildSpec(ev)
	if err != nil {
		return "", err
	}
	specPath := filepath.Join(workdir, SpecFilename)
	if err := os.WriteFile(specPath, spec, 0o644); err != nil {
		return "", fmt.Errorf("write spec: %w", err)
	}
	if clip != nil {
		if err := os.WriteFile(filepath.Join(workdir, ClipFilename), clip, 0o644); err != nil {
			return "", fmt.Errorf("write clip: %w", err)
		}
	}
	outPath := filepath.Join(workdir, "deck.html")
	if err := r.Bundle(ctx, specPath, outPath); err != nil {
		return "", fmt.Errorf("render deck: %w", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		return "", fmt.Errorf("renderer reported success but no deck at %s: %w", outPath, err)
	}
	return outPath, nil
}

// SlideyRenderer is the production [Renderer]: it shells slidey's `bundle`
// subcommand, which emits one self-contained interactive HTML file (rrweb
// inlined, no narration audio, no server needed). It performs no LLM or
// otherwise paid call.
type SlideyRenderer struct {
	// Bin is the slidey entrypoint. When it ends in .js it is run via `node`;
	// otherwise it is treated as a slidey binary on PATH. Empty defaults to
	// resolving from Dir.
	Bin string
	// Dir is the slidey project directory (e.g. ~/code/slidey). When Bin is
	// empty, the renderer runs `node <Dir>/src/index.js`.
	Dir string
	// Run is the exec seam (injected in tests). nil uses the default runner.
	Run func(ctx context.Context, name string, args ...string) error
}

// Bundle implements [Renderer] by invoking `slidey bundle <spec> <out>`.
func (s SlideyRenderer) Bundle(ctx context.Context, specPath, outPath string) error {
	name, pre, err := s.command()
	if err != nil {
		return err
	}
	args := append(append([]string{}, pre...), "bundle", specPath, outPath)
	run := s.Run
	if run == nil {
		run = defaultRun
	}
	return run(ctx, name, args...)
}

// command resolves the executable + leading args for a `bundle` invocation.
func (s SlideyRenderer) command() (name string, pre []string, err error) {
	bin := strings.TrimSpace(s.Bin)
	if bin == "" {
		dir := strings.TrimSpace(s.Dir)
		if dir == "" {
			return "", nil, fmt.Errorf("bugdeck: SlideyRenderer needs Bin or Dir")
		}
		return "node", []string{filepath.Join(dir, "src", "index.js")}, nil
	}
	if strings.HasSuffix(bin, ".js") {
		return "node", []string{bin}, nil
	}
	return bin, nil, nil
}

// defaultRun executes name+args, folding any output into the error so a failed
// render surfaces slidey's diagnostics.
func defaultRun(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
