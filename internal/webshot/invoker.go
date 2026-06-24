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

// Capture shells `pnpm exec tsx web-shot.ts --url … --out … --viewport WxH`
// under RepoRoot/tools/runstatus. pnpm exec resolves tsx + @playwright/test
// from that workspace package, matching how the skills run their specs.
func (n *NodeInvoker) Capture(ctx context.Context, req CaptureRequest) error {
	if n.RepoRoot == "" {
		return fmt.Errorf("webshot: NodeInvoker.RepoRoot is required")
	}
	runner := n.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	dir := filepath.Join(n.RepoRoot, "tools", "runstatus")
	args := []string{
		"exec", "tsx", "web-shot.ts",
		"--url", req.URL,
		"--out", req.OutPath,
		"--viewport", req.Viewport.String(),
	}
	for _, text := range req.AssertText {
		args = append(args, "--assert-text", text)
	}
	return runner.Run(ctx, dir, "pnpm", args...)
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

func removeFile(path string) { _ = os.Remove(path) }

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
