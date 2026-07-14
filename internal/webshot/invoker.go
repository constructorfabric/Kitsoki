// invoker.go — the default [BrowserInvoker]: shell the maintained
// tools/runstatus/web-shot.ts Playwright helper to rasterise a served URL to a
// PNG. Promoted from the skills' one-off tools/runstatus/snap.ts; see the
// proposal Open question 1 (shell the proven Playwright helper for parity rather
// than re-implement page-settle in chromedp).

package webshot

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// NodeInvoker rasterises a URL by shelling tools/runstatus/web-shot.ts. It is
// the real (browser-bound) [BrowserInvoker]; unit tests inject a stub instead.
type NodeInvoker struct {
	// RepoRoot is the kitsoki checkout whose tools/runstatus/web-shot.ts to run.
	// Required (the helper + its node_modules live there).
	RepoRoot string
	// Runner runs the assembled argv; defaults to [ExecRunner] (os/exec) when
	// nil. Injected so a test can assert the exact command without a real Node.
	Runner CommandRunner
}

// CommandRunner runs a prepared command (name + args, in dir, inheriting env)
// and returns combined output on failure. The seam that lets a test capture the
// argv the [NodeInvoker] would shell without launching Node/Playwright.
type CommandRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) error
}

// ExecRunner is the os/exec-backed [CommandRunner].
type ExecRunner struct{}

// Run executes name+args with cwd=dir, streaming stdio to the parent. On failure
// it wraps the error with the command for diagnosis.
func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr // helper logs go to stderr; the PNG is the artifact
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s %v: %w", name, args, err)
	}
	return nil
}

// Capture shells `node_modules/.bin/tsx web-shot.ts --url … --out … --viewport
// WxH` under RepoRoot/tools/runstatus. Calling the local binary directly avoids
// pnpm's workspace temp-file writes, which fail when the Kitsoki checkout is
// protected read-only, while still resolving tsx + @playwright/test from the
// runstatus package's installed dependencies.
func (n *NodeInvoker) Capture(ctx context.Context, req CaptureRequest) error {
	if n.RepoRoot == "" {
		return fmt.Errorf("webshot: NodeInvoker.RepoRoot is required")
	}
	runner := n.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	dir := filepath.Join(n.RepoRoot, "tools", "runstatus")
	tsx := filepath.Join(dir, "node_modules", ".bin", "tsx")
	args := []string{
		"web-shot.ts",
		"--url", req.URL,
		"--out", req.OutPath,
		"--viewport", req.Viewport.String(),
	}
	if req.SemanticOutPath != "" {
		args = append(args, "--semantic-out", req.SemanticOutPath)
	}
	if req.RRWebOutPath != "" {
		args = append(args, "--rrweb-out", req.RRWebOutPath)
	}
	if req.Action.Kind != "" {
		args = append(args, "--action", req.Action.Kind)
		if req.Action.ActionHandle != "" {
			args = append(args, "--action-handle", req.Action.ActionHandle)
		}
		if req.Action.Point != nil {
			args = append(args, "--point", fmt.Sprintf("%d,%d", req.Action.Point.X, req.Action.Point.Y))
		}
		if req.Action.Button != "" {
			args = append(args, "--button", req.Action.Button)
		}
		for _, mod := range req.Action.Modifiers {
			args = append(args, "--modifier", mod)
		}
	}
	for _, text := range req.AssertText {
		args = append(args, "--assert-text", text)
	}
	return runner.Run(ctx, dir, tsx, args...)
}

// tempPNGPath returns a fresh temp .png path the invoker writes to and Shot
// reads back. Kept tiny + injectable-free since it touches only os.TempDir.
func tempPNGPath() (string, error) {
	f, err := os.CreateTemp("", "kitsoki-webshot-*.png")
	if err != nil {
		return "", fmt.Errorf("webshot: temp file: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	return name, nil
}

func tempJSONPath() (string, error) {
	f, err := os.CreateTemp("", "kitsoki-webshot-*.json")
	if err != nil {
		return "", fmt.Errorf("webshot: temp semantic file: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	return name, nil
}

func removeFile(path string) { _ = os.Remove(path) }

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
