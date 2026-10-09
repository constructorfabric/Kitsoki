package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	SourceBundleSchema   = "capsule-source-bundle/v1"
	SourceBundleFormat   = "git-bundle"
	DefaultMaxBundleSize = int64(256 << 20)
)

var gitObjectID = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// SourceBundle is the portable, content-addressed source handoff used by a
// remote Capsule worker. Data is transported as an opaque request body rather
// than embedded in the sealed execution envelope, so retries can reuse the
// same source object without changing the envelope digest.
type SourceBundle struct {
	Schema string `json:"schema"`
	Format string `json:"format"`
	Head   string `json:"head"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
	Data   []byte `json:"-"`
}

type SourceBundler interface {
	Bundle(context.Context, Envelope) (SourceBundle, error)
}

type SourceBundlerFunc func(context.Context, Envelope) (SourceBundle, error)

func (f SourceBundlerFunc) Bundle(ctx context.Context, envelope Envelope) (SourceBundle, error) {
	return f(ctx, envelope)
}

// GitBundle builds a complete git bundle from the currently checked-out HEAD.
// It rejects dirty workspaces because uncommitted bytes are not represented by
// Envelope.SourceDigest and therefore could not be verified by the worker.
func GitBundle(ctx context.Context, workspace, expectedHead string, maxBytes int64) (SourceBundle, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBundleSize
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return SourceBundle{}, fmt.Errorf("capsule source: resolve workspace: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	head, err := gitOutput(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return SourceBundle{}, fmt.Errorf("capsule source: read HEAD: %w", err)
	}
	head = strings.TrimSpace(head)
	expectedHead = strings.TrimSpace(expectedHead)
	if !validGitObjectID(head) || !validGitObjectID(expectedHead) || head != expectedHead {
		return SourceBundle{}, fmt.Errorf("capsule source: workspace HEAD %q does not match sealed source %q", head, expectedHead)
	}
	status, err := gitOutput(ctx, root, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return SourceBundle{}, fmt.Errorf("capsule source: inspect workspace: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return SourceBundle{}, fmt.Errorf("capsule source: workspace has uncommitted or untracked files; commit the exact source before remote execution")
	}
	tmpDir, err := os.MkdirTemp("", "kitsoki-capsule-source-*")
	if err != nil {
		return SourceBundle{}, err
	}
	defer os.RemoveAll(tmpDir)
	path := filepath.Join(tmpDir, "source.bundle")
	if _, err := gitOutput(ctx, root, "bundle", "create", path, "HEAD"); err != nil {
		return SourceBundle{}, fmt.Errorf("capsule source: create git bundle: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return SourceBundle{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return SourceBundle{}, err
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		return SourceBundle{}, fmt.Errorf("capsule source: bundle size %d exceeds allowed maximum %d", info.Size(), maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return SourceBundle{}, err
	}
	if int64(len(data)) != info.Size() {
		return SourceBundle{}, fmt.Errorf("capsule source: bundle changed while reading")
	}
	sum := sha256.Sum256(data)
	return SourceBundle{
		Schema: SourceBundleSchema,
		Format: SourceBundleFormat,
		Head:   head,
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
		Size:   int64(len(data)),
		Data:   data,
	}, nil
}

func ValidateSourceBundle(bundle SourceBundle, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBundleSize
	}
	if bundle.Schema != SourceBundleSchema || bundle.Format != SourceBundleFormat {
		return fmt.Errorf("capsule source: unsupported bundle contract")
	}
	if !validGitObjectID(bundle.Head) {
		return fmt.Errorf("capsule source: invalid source HEAD")
	}
	if bundle.Size <= 0 || bundle.Size > maxBytes || bundle.Size != int64(len(bundle.Data)) {
		return fmt.Errorf("capsule source: invalid bundle size")
	}
	sum := sha256.Sum256(bundle.Data)
	want := "sha256:" + hex.EncodeToString(sum[:])
	if bundle.Digest != want {
		return fmt.Errorf("capsule source: bundle digest mismatch")
	}
	return nil
}

func validGitObjectID(value string) bool { return gitObjectID.MatchString(strings.TrimSpace(value)) }

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	argv := append([]string{"-C", root}, args...)
	out, err := exec.CommandContext(ctx, "git", argv...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
