// Package kitgit implements the git source tier for kit resolution
// (docs/proposals/kits.md, S2): `source: git+https://host/org/repo@ref`.
//
// It generalizes the embed → content-addressed cache → materialize pattern
// that internal/basestories and internal/baseskills established for the
// built-in story/skill libraries (see internal/basestories/basestories.go's
// package doc). Those two packages extract from an embed.FS; this one
// extracts from a real git remote — the "never a fetcher" non-goal recorded
// there was scoped to the embedded-library resolver specifically, and S2
// deliberately introduces the first real fetcher tier alongside it (see the
// updated non-goal comment in basestories.go).
//
// # Cache key
//
// Materialize caches by resolved commit, not by (url, ref): the directory is
// ${XDG_CACHE_HOME:-~/.cache}/kitsoki/kits/git/<commit>/, mirroring the
// story/skill caches' idempotent-sentinel + atomic-rename discipline. When
// ref is already a full 40-hex commit SHA and that directory is already
// materialized, Materialize never touches the network — this is what makes
// a locked (`.kitsoki/kits.lock`-pinned) git kit reproducible offline. When
// ref is a tag/branch, resolving it to a commit always requires a fetch (a
// tag can move), so no cache short-circuit is attempted in that case.
package kitgit

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Runner runs an external command and reports stdout/stderr/error. It
// mirrors the internal/video.Runner and internal/host cliExec seams so
// tests substitute a fake without shelling out to a real git binary. dir is
// the working directory git runs in.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (stdout, stderr string, err error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, dir, name string, args ...string) (string, string, error)

// Run implements Runner.
func (f RunnerFunc) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	return f(ctx, dir, name, args...)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

// DefaultRunner is the production Runner, backed by os/exec running the
// real `git` binary. Tests inject a fake via the runner parameter on
// Materialize; production callers pass DefaultRunner.
var DefaultRunner Runner = execRunner{}

// sourceRE matches `git+<url>@<ref>`. The URL half is everything up to the
// LAST '@' so a URL containing '@' itself (e.g. an ssh-style
// git@github.com:org/repo.git) still parses correctly as long as a ref is
// also present — ssh-form URLs are not specially recognised beyond that.
var sourceRE = regexp.MustCompile(`^git\+(.+)@([^@/]+)$`)

// ParseSource parses a `git+<url>@<ref>` import source. ok is false when src
// does not have the git+ prefix, or has it but is malformed (no @ref).
func ParseSource(src string) (url, ref string, ok bool) {
	if !strings.HasPrefix(src, "git+") {
		return "", "", false
	}
	m := sourceRE.FindStringSubmatch(src)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// commitSHARE matches a full 40-hex-character git commit SHA.
var commitSHARE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Result is what Materialize resolved.
type Result struct {
	// Root is the absolute path of the materialized working tree (the
	// checked-out repository content at Commit, minus .git).
	Root string
	// Commit is the resolved full commit SHA.
	Commit string
	// TreeHash is the git tree object hash of Commit's root tree
	// (`git rev-parse <commit>^{tree}`) — the "tree-hash" the lockfile
	// records, reusing git's own content addressing rather than
	// re-implementing a directory hash.
	TreeHash string
}

// Materialize fetches ref from url and extracts it into the content-addressed
// cache, returning the materialized root plus the resolved commit and tree
// hash. ctx bounds the git invocations.
func Materialize(ctx context.Context, runner Runner, url, ref string) (Result, error) {
	if runner == nil {
		runner = DefaultRunner
	}
	base, err := cacheBaseDir()
	if err != nil {
		return Result{}, err
	}

	// Offline short-circuit: ref is already a resolved commit and the cache
	// already has it — no network needed.
	if commitSHARE.MatchString(ref) {
		root := filepath.Join(base, ref)
		if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr == nil {
			treeHash, thErr := readSentinelTreeHash(root)
			if thErr == nil {
				return Result{Root: root, Commit: ref, TreeHash: treeHash}, nil
			}
			// Sentinel unreadable — fall through and re-fetch.
		}
	}

	tmp, err := os.MkdirTemp(base, ".fetch-*")
	if err != nil {
		return Result{}, fmt.Errorf("kitgit: create temp fetch dir: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed away

	if _, _, err := runner.Run(ctx, tmp, "git", "init", "-q"); err != nil {
		return Result{}, fmt.Errorf("kitgit: git init: %w", err)
	}
	if _, _, err := runner.Run(ctx, tmp, "git", "remote", "add", "origin", url); err != nil {
		return Result{}, fmt.Errorf("kitgit: git remote add: %w", err)
	}
	if _, _, err := runner.Run(ctx, tmp, "git", "fetch", "--depth", "1", "origin", ref); err != nil {
		return Result{}, fmt.Errorf("kitgit: git fetch %s@%s: %w", url, ref, err)
	}
	if _, _, err := runner.Run(ctx, tmp, "git", "checkout", "-q", "FETCH_HEAD"); err != nil {
		return Result{}, fmt.Errorf("kitgit: git checkout %s@%s: %w", url, ref, err)
	}
	commitOut, _, err := runner.Run(ctx, tmp, "git", "rev-parse", "HEAD")
	if err != nil {
		return Result{}, fmt.Errorf("kitgit: git rev-parse HEAD: %w", err)
	}
	commit := strings.TrimSpace(commitOut)
	treeOut, _, err := runner.Run(ctx, tmp, "git", "rev-parse", "HEAD^{tree}")
	if err != nil {
		return Result{}, fmt.Errorf("kitgit: git rev-parse HEAD^{tree}: %w", err)
	}
	treeHash := strings.TrimSpace(treeOut)

	if err := os.RemoveAll(filepath.Join(tmp, ".git")); err != nil {
		return Result{}, fmt.Errorf("kitgit: strip .git from working tree: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".materialized"), []byte(commit+"\n"+treeHash+"\n"), 0o644); err != nil {
		return Result{}, fmt.Errorf("kitgit: write sentinel: %w", err)
	}

	root := filepath.Join(base, commit)
	if renErr := os.Rename(tmp, root); renErr != nil {
		// A concurrent winner already materialized this commit — reuse it.
		if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr == nil {
			if th, thErr := readSentinelTreeHash(root); thErr == nil {
				return Result{Root: root, Commit: commit, TreeHash: th}, nil
			}
		}
		return Result{}, fmt.Errorf("kitgit: publish cache dir %q: %w", root, renErr)
	}
	return Result{Root: root, Commit: commit, TreeHash: treeHash}, nil
}

// CachedResult looks up an already-materialized commit in the cache without
// touching the network — the seam `kitsoki kit verify` uses to check a
// locked git-sourced kit is still present and content-matches, without
// re-fetching. ok is false when the commit has no materialized cache entry
// (a fresh Materialize call is needed to populate it).
func CachedResult(commit string) (res Result, ok bool, err error) {
	base, err := cacheBaseDir()
	if err != nil {
		return Result{}, false, err
	}
	root := filepath.Join(base, commit)
	if _, statErr := os.Stat(filepath.Join(root, ".materialized")); statErr != nil {
		return Result{}, false, nil
	}
	treeHash, err := readSentinelTreeHash(root)
	if err != nil {
		return Result{}, false, err
	}
	return Result{Root: root, Commit: commit, TreeHash: treeHash}, true, nil
}

// readSentinelTreeHash reads the tree hash back out of a previously-written
// `.materialized` sentinel (see Materialize).
func readSentinelTreeHash(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".materialized"))
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("kitgit: malformed sentinel at %s", root)
	}
	return strings.TrimSpace(lines[1]), nil
}

// cacheBaseDir returns ${XDG_CACHE_HOME:-~/.cache}/kitsoki/kits/git, creating
// it if absent.
func cacheBaseDir() (string, error) {
	var base string
	if xdg := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); xdg != "" {
		base = xdg
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("kitgit: locate cache dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "kitsoki", "kits", "git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("kitgit: create cache dir %q: %w", dir, err)
	}
	return dir, nil
}

// DirTreeHash computes a deterministic content hash over a plain on-disk
// directory tree (path, size, and bytes of every regular file in stable
// lexical order) — the same mixing scheme internal/basestories.hashTree and
// internal/baseskills.hashTree use over their embed.FS trees, generalized to
// os.DirFS so non-git sources (local paths, the embedded-library fallback,
// on-disk @kitsoki checkouts) can produce a comparable "tree-hash" for the
// lockfile without a git repository backing them.
func DirTreeHash(root string) (string, error) {
	var files []string
	fsys := os.DirFS(root)
	if err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == ".materialized" || d.Name() == ".gitkeep" {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return "", fmt.Errorf("kitgit: walk %q: %w", root, err)
	}
	sort.Strings(files)

	h := sha256.New()
	var lenbuf [8]byte
	for _, name := range files {
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return "", fmt.Errorf("kitgit: read %q: %w", name, err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(b)))
		h.Write(lenbuf[:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
