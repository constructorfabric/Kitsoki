package control

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// FileEntry is a path-only directory listing. Paths are always relative to the
// workspace and never reveal verifier overlays or a machine-local root.
type FileEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Symlink   bool   `json:"symlink,omitempty"`
	Size      int64  `json:"size,omitempty"`
}
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type SearchResult struct {
	Matches       []SearchMatch `json:"matches"`
	FilesExamined int           `json:"files_examined"`
	BytesExamined int64         `json:"bytes_examined"`
	SkippedFiles  int           `json:"skipped_files,omitempty"`
	Truncated     bool          `json:"truncated,omitempty"`
}

const (
	readMaxFileBytes      = 4 << 20
	writeMaxFileBytes     = 4 << 20
	commandMaxOutputBytes = 1 << 20
	searchDefaultMatches  = 100
	searchMaxMatches      = 500
	searchMaxEntries      = 20_000
	searchMaxFiles        = 10_000
	searchMaxBytes        = 16 << 20
	searchMaxFileBytes    = 1 << 20
	searchMaxLineBytes    = 4 << 10
)

var ignoredSearchDirectories = map[string]bool{
	".git":              true,
	".kitsoki-verifier": true,
	".cache":            true,
	".gradle":           true,
	".next":             true,
	".venv":             true,
	"__pycache__":       true,
	"build":             true,
	"coverage":          true,
	"dist":              true,
	"node_modules":      true,
	"target":            true,
	"vendor":            true,
	"venv":              true,
}

type CommandResult struct {
	Handle          Handle `json:"workspace"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	TimedOut        bool   `json:"timed_out,omitempty"`
}

// boundedCommandOutput retains a diagnostic prefix while reporting the byte
// count accepted by io.Writer. A noisy project command therefore cannot grow
// the Capsule server or CLI process without bound.
type boundedCommandOutput struct {
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedCommandOutput) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || written > 0
		return written, nil
	}
	if len(p) > remaining {
		_, _ = b.buffer.Write(p[:remaining])
		b.truncated = true
		return written, nil
	}
	_, _ = b.buffer.Write(p)
	return written, nil
}

func (b *boundedCommandOutput) String() string { return b.buffer.String() }

type VCSStatus struct {
	Handle    Handle `json:"workspace"`
	Branch    string `json:"branch"`
	Head      string `json:"head"`
	Dirty     bool   `json:"dirty"`
	Porcelain string `json:"porcelain,omitempty"`
}

func (m *Manager) ReadFile(ctx context.Context, h Handle, relative string) ([]byte, error) {
	path, err := m.resolve(ctx, h, relative, true)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("capsule fs: read target must be a regular file")
	}
	if info.Size() > readMaxFileBytes {
		return nil, fmt.Errorf("capsule fs: file exceeds %d byte read limit", readMaxFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, readMaxFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(raw) > readMaxFileBytes {
		return nil, fmt.Errorf("capsule fs: file exceeds %d byte read limit", readMaxFileBytes)
	}
	if !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, fmt.Errorf("capsule fs: binary file reads are not supported")
	}
	return raw, nil
}

func (m *Manager) ListFiles(ctx context.Context, h Handle, relative string) ([]FileEntry, error) {
	root, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return nil, err
	}
	path := ""
	if strings.TrimSpace(relative) == "" || filepath.Clean(relative) == "." {
		path = root
	} else {
		path, err = m.resolve(ctx, h, relative, true)
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == ".git" || entry.Name() == ".kitsoki-verifier" {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return nil, e
		}
		rel, e := filepath.Rel(root, filepath.Join(path, entry.Name()))
		if e != nil {
			return nil, e
		}
		out = append(out, FileEntry{Path: filepath.ToSlash(rel), Directory: entry.IsDir(), Symlink: entry.Type()&os.ModeSymlink != 0, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (m *Manager) SearchFiles(ctx context.Context, h Handle, query string, limit int) (SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return SearchResult{}, fmt.Errorf("capsule fs: query is required")
	}
	root, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return SearchResult{}, err
	}
	if limit <= 0 {
		limit = searchDefaultMatches
	}
	if limit > searchMaxMatches {
		limit = searchMaxMatches
	}
	result := SearchResult{Matches: []SearchMatch{}}
	entries := 0
	needle := []byte(query)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		entries++
		if entries > searchMaxEntries {
			result.Truncated = true
			return fs.SkipAll
		}
		if d.IsDir() && ignoredSearchDirectories[d.Name()] {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if result.FilesExamined >= searchMaxFiles {
			result.Truncated = true
			return fs.SkipAll
		}
		result.FilesExamined++
		if d.Type()&os.ModeSymlink != 0 {
			result.SkippedFiles++
			return nil
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > searchMaxFileBytes {
			result.SkippedFiles++
			return nil
		}
		if result.BytesExamined+info.Size() > searchMaxBytes {
			result.Truncated = true
			return fs.SkipAll
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			result.SkippedFiles++
			return nil
		}
		resolved, e := ResolveWorkspacePath(root, rel, true)
		if e != nil {
			result.SkippedFiles++
			return nil
		}
		file, e := os.Open(resolved)
		if e != nil {
			result.SkippedFiles++
			return nil
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, searchMaxFileBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(raw) > searchMaxFileBytes {
			result.SkippedFiles++
			return nil
		}
		if result.BytesExamined+int64(len(raw)) > searchMaxBytes {
			result.Truncated = true
			return fs.SkipAll
		}
		result.BytesExamined += int64(len(raw))
		if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
			result.SkippedFiles++
			return nil
		}
		for n, line := range bytes.Split(raw, []byte{'\n'}) {
			if at := bytes.Index(line, needle); at >= 0 {
				result.Matches = append(result.Matches, SearchMatch{Path: filepath.ToSlash(rel), Line: n + 1, Text: boundedSearchLine(line, at)})
				if len(result.Matches) >= limit {
					result.Truncated = true
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	return result, err
}

func boundedSearchLine(line []byte, matchAt int) string {
	if len(line) <= searchMaxLineBytes {
		return string(line)
	}
	start := matchAt - searchMaxLineBytes/4
	if start < 0 {
		start = 0
	}
	if start+searchMaxLineBytes > len(line) {
		start = len(line) - searchMaxLineBytes
	}
	end := start + searchMaxLineBytes
	for start < end && !utf8.RuneStart(line[start]) {
		start++
	}
	for end > start && !utf8.Valid(line[start:end]) {
		end--
	}
	return string(line[start:end])
}

func (m *Manager) WriteFile(ctx context.Context, h Handle, relative string, contents []byte) (Handle, error) {
	if m.Grant.Owner != "" && !m.Grant.Allows("effect", "fs_write") {
		return Handle{}, fmt.Errorf("%w: fs_write", ErrDenied)
	}
	if len(contents) > writeMaxFileBytes {
		return Handle{}, fmt.Errorf("capsule fs: contents exceed %d byte write limit", writeMaxFileBytes)
	}
	path, err := m.resolve(ctx, h, relative, false)
	if err != nil {
		return Handle{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Handle{}, err
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return Handle{}, err
	}
	return m.mark(ctx, h, StateDirty, "capsule.workspace.changed")
}

// RunCommand runs only a definition-declared argv unless both the definition
// and immutable grant opt into raw argv. There is no shell mode.
func (m *Manager) RunCommand(ctx context.Context, h Handle, commandID string, rawArgv []string, timeout time.Duration) (CommandResult, error) {
	in, err := m.Status(ctx, h)
	if err != nil {
		return CommandResult{}, err
	}
	if !m.Grant.Allows("effect", "exec") {
		return CommandResult{}, fmt.Errorf("%w: exec", ErrDenied)
	}
	def, err := m.Definitions.Get(ctx, in.DefinitionID)
	if err != nil {
		return CommandResult{}, err
	}
	argv := append([]string(nil), rawArgv...)
	if commandID != "" {
		cmd, ok := def.Policy.Commands[commandID]
		if !ok {
			return CommandResult{}, fmt.Errorf("%w: command %q", ErrDenied, commandID)
		}
		argv = append([]string(nil), cmd.Argv...)
		if timeout == 0 && cmd.Timeout != "" {
			timeout, err = time.ParseDuration(cmd.Timeout)
			if err != nil {
				return CommandResult{}, fmt.Errorf("capsule exec: command timeout: %w", err)
			}
		}
	} else if !def.Policy.RawArgv || !m.Grant.Allows("effect", "raw_exec") {
		return CommandResult{}, fmt.Errorf("%w: raw argv", ErrDenied)
	}
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return CommandResult{}, fmt.Errorf("capsule exec: argv is required")
	}
	path, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return CommandResult{}, err
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = path
	output := boundedCommandOutput{limit: commandMaxOutputBytes}
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if runCtx.Err() == context.DeadlineExceeded {
			exit = -1
		} else {
			return CommandResult{}, fmt.Errorf("capsule exec: %w", err)
		}
	}
	next, markErr := m.mark(ctx, h, StateDirty, "capsule.workspace.changed")
	if markErr != nil {
		return CommandResult{}, markErr
	}
	return CommandResult{Handle: next, ExitCode: exit, Output: output.String(), OutputTruncated: output.truncated, TimedOut: runCtx.Err() == context.DeadlineExceeded}, nil
}

func (m *Manager) StatusVCS(ctx context.Context, h Handle) (VCSStatus, error) {
	path, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return VCSStatus{}, err
	}
	out, err := git(ctx, path, "status", "--porcelain", "--branch")
	if err != nil {
		return VCSStatus{}, err
	}
	head, err := git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return VCSStatus{}, err
	}
	branch, err := git(ctx, path, "branch", "--show-current")
	if err != nil {
		return VCSStatus{}, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dirty := false
	for _, line := range lines {
		if line != "" && !strings.HasPrefix(line, "## ") {
			dirty = true
			break
		}
	}
	return VCSStatus{Handle: h, Branch: strings.TrimSpace(branch), Head: strings.TrimSpace(head), Dirty: dirty, Porcelain: out}, nil
}
func (m *Manager) DiffVCS(ctx context.Context, h Handle) (string, error) {
	path, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return "", err
	}
	return git(ctx, path, "diff", "--no-ext-diff", "HEAD")
}
func (m *Manager) CommitVCS(ctx context.Context, h Handle, message string) (Handle, error) {
	if strings.TrimSpace(message) == "" {
		return Handle{}, fmt.Errorf("capsule vcs: message is required")
	}
	if !m.Grant.Allows("effect", "vcs_commit") {
		return Handle{}, fmt.Errorf("%w: vcs_commit", ErrDenied)
	}
	path, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return Handle{}, err
	}
	if _, err = git(ctx, path, "add", "-A"); err != nil {
		return Handle{}, err
	}
	if _, err = git(ctx, path, "diff", "--cached", "--quiet"); err == nil {
		return Handle{}, fmt.Errorf("capsule vcs: no staged changes")
	}
	if _, err = git(ctx, path, "commit", "--signoff", "-m", message); err != nil {
		return Handle{}, err
	}
	head, err := git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return Handle{}, err
	}
	return m.markWith(ctx, h, StateCommitted, "capsule.workspace.committed", func(in *Instance) {
		in.Head = strings.TrimSpace(head)
	})
}

// MarkIntegrated advances lifecycle only after a reconciler completed its
// compare-and-swap update. It deliberately cannot perform Git operations.
func (m *Manager) MarkIntegrated(ctx context.Context, h Handle) (Handle, error) {
	return m.mark(ctx, h, StateIntegrated, "capsule.workspace.integrated")
}

// Integrate delegates a complete provider-owned integration lifecycle (such as
// the protected development compatibility adapter), then advances the handle
// only after that provider has succeeded. It cannot be used by an ungranted
// agent-facing server.
func (m *Manager) Integrate(ctx context.Context, h Handle, gate string) (Handle, error) {
	if !m.Grant.Allows("effect", "local_reconcile") {
		return Handle{}, fmt.Errorf("%w: local_reconcile", ErrDenied)
	}
	in, err := m.Status(ctx, h)
	if err != nil {
		return Handle{}, err
	}
	provider := m.Providers[in.Provider]
	integrator, ok := provider.(WorkspaceIntegrator)
	if !ok {
		return Handle{}, fmt.Errorf("capsule workspace: provider %q does not own integration", in.Provider)
	}
	definition, err := m.Definitions.Get(ctx, in.DefinitionID)
	if err != nil {
		return Handle{}, err
	}
	if err := integrator.Integrate(ctx, definition, in, gate); err != nil {
		return Handle{}, err
	}
	return m.MarkIntegrated(ctx, h)
}

func (m *Manager) resolve(ctx context.Context, h Handle, relative string, mustExist bool) (string, error) {
	root, err := m.WorkspacePath(ctx, h)
	if err != nil {
		return "", err
	}
	path, err := ResolveWorkspacePath(root, relative, mustExist)
	if err != nil {
		return "", err
	}
	// Writes resolve with mustExist=false so a new path can be created. If the
	// target already exists, resolve it again to prevent os.WriteFile from
	// following a final symlink outside the workspace or into verifier assets.
	if !mustExist {
		if _, statErr := os.Lstat(path); statErr == nil {
			path, err = ResolveWorkspacePath(root, relative, true)
			if err != nil {
				return "", err
			}
		} else if !os.IsNotExist(statErr) {
			return "", statErr
		} else {
			realParent, parentErr := filepath.EvalSymlinks(filepath.Dir(path))
			if parentErr != nil {
				return "", parentErr
			}
			path = filepath.Join(realParent, filepath.Base(path))
		}
	}
	if err := ensureAgentVisiblePath(root, path); err != nil {
		return "", err
	}
	return path, nil
}

func ensureAgentVisiblePath(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("capsule fs: path escapes workspace")
	}
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		if part == ".git" || part == ".kitsoki-verifier" {
			return fmt.Errorf("%w: agent-hidden workspace path", ErrDenied)
		}
	}
	return nil
}
func (m *Manager) mark(ctx context.Context, h Handle, state State, event string) (Handle, error) {
	return m.markWith(ctx, h, state, event, nil)
}

func (m *Manager) markWith(ctx context.Context, h Handle, state State, event string, update func(*Instance)) (Handle, error) {
	in, err := m.Instances.CompareAndSwap(ctx, h.ID, h.Generation, func(cur *Instance) error {
		if cur.State == StateClosed {
			return ErrInvalidState
		}
		cur.State = state
		if update != nil {
			update(cur)
		}
		return nil
	})
	if err != nil {
		return Handle{}, err
	}
	if err := m.emit(ctx, event, in); err != nil {
		return Handle{}, err
	}
	return Handle{ID: in.ID, Generation: in.Generation}, nil
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capsule vcs: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
