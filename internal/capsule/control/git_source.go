package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	legacy "kitsoki/internal/capsule"
)

var fullCommit = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)

const (
	instanceSentinel    = ".kitsoki-capsule"
	instancePinSentinel = ".kitsoki-capsule-pin"
)

// GitSourceProvider materializes project-self and pinned Git definitions into
// manager-owned workspaces. It never receives credentials; a future remote
// adapter injects a narrow credential reference only into its clone/fetch call.
type GitSourceProvider struct {
	ProjectRoot string
	// CacheRoot holds content-addressed bare mirrors for pinned sources. When
	// empty, it defaults under <project>/.capsules/cache/git-sources.
	CacheRoot string
}

var _ WorkspaceProvider = GitSourceProvider{}
var _ WorkspaceMaterializationVerifier = GitSourceProvider{}

func (GitSourceProvider) Name() string { return "git" }
func (p GitSourceProvider) Create(ctx context.Context, def Definition, in Instance) (MaterializedWorkspace, error) {
	root, err := projectRoot(p.ProjectRoot)
	if err != nil {
		return MaterializedWorkspace{}, err
	}
	var source string
	switch def.Source.Kind {
	case SourceSelf:
		source = root
	case SourcePinned:
		source = strings.TrimSpace(def.Source.Ref)
		if source == "" {
			return MaterializedWorkspace{}, fmt.Errorf("git source: pinned ref is required")
		}
		if !fullCommit.MatchString(def.Source.Commit) {
			return MaterializedWorkspace{}, fmt.Errorf("git source: pinned commit must be a full immutable id")
		}
		if !filepath.IsAbs(source) && !strings.Contains(source, "://") {
			source = filepath.Join(root, source)
		}
	default:
		return MaterializedWorkspace{}, fmt.Errorf("git source: unsupported source %q", def.Source.Kind)
	}
	if err := os.MkdirAll(filepath.Dir(in.Path), 0o755); err != nil {
		return MaterializedWorkspace{}, err
	}
	cloneSource := source
	if def.Source.Kind == SourcePinned {
		cached, err := p.ensurePinnedCache(ctx, root, source, strings.ToLower(def.Source.Commit))
		if err != nil {
			return MaterializedWorkspace{}, err
		}
		cloneSource = cached
	}
	if _, err := runGit(ctx, "", "clone", "--no-local", cloneSource, in.Path); err != nil {
		return MaterializedWorkspace{}, err
	}
	if def.Source.Kind == SourcePinned {
		if _, err := runGit(ctx, in.Path, "checkout", "--detach", def.Source.Commit); err != nil {
			_ = os.RemoveAll(in.Path)
			return MaterializedWorkspace{}, err
		}
	}
	head, err := runGit(ctx, in.Path, "rev-parse", "HEAD")
	if err != nil {
		_ = os.RemoveAll(in.Path)
		return MaterializedWorkspace{}, err
	}
	branch, _ := runGit(ctx, in.Path, "branch", "--show-current")
	if err := excludeGitSourceMetadata(in.Path); err != nil {
		_ = os.RemoveAll(in.Path)
		return MaterializedWorkspace{}, err
	}
	if err := os.WriteFile(filepath.Join(in.Path, instanceSentinel), []byte(in.ID+"\n"), 0o644); err != nil {
		_ = os.RemoveAll(in.Path)
		return MaterializedWorkspace{}, err
	}
	verifierOverlays, err := materializeOverlays(root, in.Path, def.Overlays)
	if err != nil {
		_ = os.RemoveAll(in.Path)
		return MaterializedWorkspace{}, err
	}
	if err := writeGitSourceManifest(in.Path, source, def, in, strings.TrimSpace(head), strings.TrimSpace(branch)); err != nil {
		_ = os.RemoveAll(in.Path)
		return MaterializedWorkspace{}, err
	}
	return MaterializedWorkspace{Path: in.Path, SourceRef: def.Source.Ref, Head: strings.TrimSpace(head), Branch: strings.TrimSpace(branch), VerifierOverlays: verifierOverlays}, nil
}

func excludeGitSourceMetadata(workspace string) error {
	exclude := filepath.Join(workspace, ".git", "info", "exclude")
	file, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("git source: open local excludes: %w", err)
	}
	_, writeErr := fmt.Fprintf(file, "\n/%s\n/%s\n/%s\n", instanceSentinel, instancePinSentinel, legacy.ManifestFile)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("git source: write local excludes: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("git source: close local excludes: %w", closeErr)
	}
	return nil
}

func writeGitSourceManifest(workspace, source string, def Definition, in Instance, head, branch string) error {
	network := strings.TrimSpace(def.Policy.Network)
	if network == "" {
		network = "none"
	}
	manifest := legacy.Manifest{
		CapsuleName: def.ID,
		SpecPath:    def.LegacyPath,
		Workspace:   workspace,
		OpenedAt:    in.CreatedAt,
		Source: legacy.ManifestSource{
			Repo:   source,
			Commit: def.Source.Commit,
			Head:   head,
			Branch: branch,
		},
		Network: network,
		Environment: map[string]string{
			"definition_digest": def.Digest,
			"provider":          "git",
			"workspace_id":      in.ID,
			"owner":             in.Lease.Owner,
		},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("git source: encode capsule manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, legacy.ManifestFile), append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("git source: write capsule manifest: %w", err)
	}
	return nil
}
// WorkspaceMaterialized implements WorkspaceMaterializationVerifier: the
// instance sentinel is written last in Create (after clone/checkout/overlay
// all succeeded), so its presence is durable proof the workspace at in.Path
// is real and complete, not merely a directory that happens to exist.
func (GitSourceProvider) WorkspaceMaterialized(in Instance) bool {
	_, err := os.Stat(filepath.Join(in.Path, instanceSentinel))
	return err == nil
}

func (GitSourceProvider) Close(_ context.Context, in Instance) error {
	if strings.TrimSpace(in.Path) == "" {
		return fmt.Errorf("git source: instance path is required")
	}
	if _, err := os.Stat(filepath.Join(in.Path, instanceSentinel)); err != nil {
		return fmt.Errorf("git source: refusing close without sentinel: %w", err)
	}
	return os.RemoveAll(in.Path)
}
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (p GitSourceProvider) ensurePinnedCache(ctx context.Context, projectRoot, source, commit string) (string, error) {
	cacheRoot := strings.TrimSpace(p.CacheRoot)
	if cacheRoot == "" {
		cacheRoot = filepath.Join(projectRoot, ".capsules", "cache", "git-sources")
	}
	if err := os.MkdirAll(cacheRoot, 0o755); err != nil {
		return "", err
	}
	cachePath := filepath.Join(cacheRoot, commit+".git")
	if gitCommitExists(ctx, cachePath, commit) {
		return cachePath, nil
	}
	tmp := cachePath + ".tmp"
	_ = os.RemoveAll(tmp)
	if _, err := runGit(ctx, "", "clone", "--mirror", source, tmp); err != nil {
		return "", err
	}
	if !gitCommitExists(ctx, tmp, commit) {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("git source: pinned commit %s is not present in source", commit)
	}
	_ = os.RemoveAll(cachePath)
	if err := os.Rename(tmp, cachePath); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	return cachePath, nil
}

func gitCommitExists(ctx context.Context, repo, commit string) bool {
	if strings.TrimSpace(repo) == "" {
		return false
	}
	if _, err := os.Stat(repo); err != nil {
		return false
	}
	_, err := runGit(ctx, repo, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

func materializeOverlays(projectRoot, workspace string, overlays []Overlay) ([]OverlayRef, error) {
	var verifier []OverlayRef
	for _, overlay := range overlays {
		src, err := projectRelativePath(projectRoot, overlay.Path)
		if err != nil {
			return nil, err
		}
		switch overlay.Visibility {
		case "workspace":
			dst := filepath.Join(workspace, filepath.Clean(overlay.Path))
			if err := copyOverlay(src, dst); err != nil {
				return nil, err
			}
		case "verifier":
			digest, err := digestPath(src)
			if err != nil {
				return nil, err
			}
			verifier = append(verifier, OverlayRef{Path: filepath.ToSlash(filepath.Clean(overlay.Path)), Digest: digest})
		}
	}
	sort.Slice(verifier, func(i, j int) bool { return verifier[i].Path < verifier[j].Path })
	return verifier, nil
}

func projectRelativePath(projectRoot, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(rel), "..") {
		return "", fmt.Errorf("capsule overlay: path %q escapes project", rel)
	}
	path := filepath.Join(projectRoot, rel)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root, err := projectRootPath(projectRoot)
	if err != nil {
		return "", err
	}
	clean := abs
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		clean = real
	}
	if relToRoot, err := filepath.Rel(root, clean); err != nil || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) || filepath.IsAbs(relToRoot) {
		return "", fmt.Errorf("capsule overlay: path %q escapes project", rel)
	}
	return abs, nil
}

func projectRootPath(projectRoot string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return root, nil
}

func copyOverlay(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			return copyFile(path, target)
		})
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func digestPath(path string) (string, error) {
	h := sha256.New()
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		if err := hashFile(h, path, ""); err != nil {
			return "", err
		}
		return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
	}
	var files []string
	if err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, p)
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, file := range files {
		rel, _ := filepath.Rel(path, file)
		if err := hashFile(h, file, filepath.ToSlash(rel)); err != nil {
			return "", err
		}
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(h io.Writer, path, rel string) error {
	if rel != "" {
		if _, err := io.WriteString(h, rel+"\n"); err != nil {
			return err
		}
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(h, in)
	return err
}
