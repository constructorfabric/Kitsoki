package studio

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrFSPathEscape = errors.New("workspace path escapes the managed root")
	ErrFSSymlink    = errors.New("workspace paths may not traverse symlinks")
	ErrFSPreimage   = errors.New("workspace file preimage does not match")
)

// FSGuard exposes the intentionally small filesystem plane for a server-held
// managed workspace. It refuses caller-supplied roots, traversal, and symlinks
// before performing a read or mutation.
type FSGuard struct {
	workspaces *ManagedWorkspaceService
	mu         sync.Mutex
}

func NewFSGuard(workspaces *ManagedWorkspaceService) (*FSGuard, error) {
	if workspaces == nil {
		return nil, errors.New("managed workspace service is required")
	}
	return &FSGuard{workspaces: workspaces}, nil
}

type FSListInput struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path,omitempty"`
}
type FSReadInput struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
}
type FSSearchInput struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path,omitempty"`
	Query       string `json:"query"`
}
type FSWriteInput struct {
	ObjectiveID    string `json:"objective_id"`
	WorkspaceID    string `json:"workspace_id"`
	Path           string `json:"path"`
	Content        string `json:"content"`
	PreimageSHA256 string `json:"preimage_sha256"`
}

// FSPatch uses full replacement content deliberately. The caller must first
// read the content and provide its digest, so a stale patch cannot overwrite a
// concurrent edit. A later CodeAct layer may compile richer patch syntax onto
// this same checked preimage primitive.
type FSPatchInput struct {
	ObjectiveID    string `json:"objective_id"`
	WorkspaceID    string `json:"workspace_id"`
	Path           string `json:"path"`
	Replacement    string `json:"replacement"`
	PreimageSHA256 string `json:"preimage_sha256"`
}

type FSEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	SHA256    string `json:"sha256,omitempty"`
}
type FSListResult struct {
	OK      bool      `json:"ok"`
	Entries []FSEntry `json:"entries"`
}
type FSReadResult struct {
	OK      bool   `json:"ok"`
	Path    string `json:"path"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}
type FSSearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type FSSearchResult struct {
	OK      bool            `json:"ok"`
	Matches []FSSearchMatch `json:"matches"`
}
type FSMutationReceipt struct {
	Objective        Objective        `json:"objective"`
	Policy           PolicyDecision   `json:"policy"`
	ObjectiveReceipt Receipt          `json:"objective_receipt"`
	Workspace        ManagedWorkspace `json:"workspace"`
	Path             string           `json:"path"`
	BeforeSHA256     string           `json:"before_sha256"`
	AfterSHA256      string           `json:"after_sha256"`
}
type FSMutationResult struct {
	OK      bool              `json:"ok"`
	Receipt FSMutationReceipt `json:"receipt"`
}

func (g *FSGuard) List(_ context.Context, input FSListInput) (FSListResult, error) {
	root, err := g.root(input.WorkspaceID)
	if err != nil {
		return FSListResult{}, err
	}
	dir, err := guardedPath(root, input.Path, false)
	if err != nil {
		return FSListResult{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return FSListResult{}, err
	}
	out := make([]FSEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return FSListResult{}, fmt.Errorf("%w: %s", ErrFSSymlink, entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return FSListResult{}, err
		}
		rel, _ := filepath.Rel(root, filepath.Join(dir, entry.Name()))
		item := FSEntry{Path: filepath.ToSlash(rel), Directory: info.IsDir()}
		if !info.IsDir() {
			b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return FSListResult{}, err
			}
			item.SHA256 = sha256Text(b)
		}
		out = append(out, item)
	}
	return FSListResult{OK: true, Entries: out}, nil
}

func (g *FSGuard) Read(_ context.Context, input FSReadInput) (FSReadResult, error) {
	root, err := g.root(input.WorkspaceID)
	if err != nil {
		return FSReadResult{}, err
	}
	path, err := guardedPath(root, input.Path, false)
	if err != nil {
		return FSReadResult{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return FSReadResult{}, err
	}
	return FSReadResult{OK: true, Path: filepath.ToSlash(input.Path), Content: string(b), SHA256: sha256Text(b)}, nil
}

func (g *FSGuard) Search(_ context.Context, input FSSearchInput) (FSSearchResult, error) {
	if input.Query == "" {
		return FSSearchResult{}, errors.New("query is required")
	}
	root, err := g.root(input.WorkspaceID)
	if err != nil {
		return FSSearchResult{}, err
	}
	start, err := guardedPath(root, input.Path, false)
	if err != nil {
		return FSSearchResult{}, err
	}
	matches := []FSSearchMatch{}
	err = filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s", ErrFSSymlink, path)
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for line, text := range strings.Split(string(b), "\n") {
			if strings.Contains(text, input.Query) {
				matches = append(matches, FSSearchMatch{Path: filepath.ToSlash(rel), Line: line + 1, Text: text})
			}
		}
		return nil
	})
	if err != nil {
		return FSSearchResult{}, err
	}
	return FSSearchResult{OK: true, Matches: matches}, nil
}

func (g *FSGuard) Write(ctx context.Context, input FSWriteInput) (FSMutationResult, error) {
	return g.replace(ctx, input.ObjectiveID, input.WorkspaceID, input.Path, input.Content, input.PreimageSHA256)
}
func (g *FSGuard) Patch(ctx context.Context, input FSPatchInput) (FSMutationResult, error) {
	return g.replace(ctx, input.ObjectiveID, input.WorkspaceID, input.Path, input.Replacement, input.PreimageSHA256)
}

func (g *FSGuard) replace(ctx context.Context, objectiveID, workspaceID, rel, content, expected string) (FSMutationResult, error) {
	if objectiveID == "" || expected == "" {
		return FSMutationResult{}, errors.New("objective_id and preimage_sha256 are required")
	}
	info, err := g.workspaces.workspace(workspaceID)
	if err != nil {
		return FSMutationResult{}, err
	}
	if info.ObjectiveID != objectiveID {
		return FSMutationResult{}, errors.New("workspace is bound to another objective")
	}
	root, err := g.root(workspaceID)
	if err != nil {
		return FSMutationResult{}, err
	}
	path, err := guardedPath(root, rel, true)
	if err != nil {
		return FSMutationResult{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return FSMutationResult{}, err
	}
	beforeHash := sha256Text(before)
	if !strings.EqualFold(beforeHash, expected) {
		return FSMutationResult{}, fmt.Errorf("%w: expected %s, found %s", ErrFSPreimage, expected, beforeHash)
	}
	decision, objective, objectiveReceipt, err := g.workspaces.objectives.AuthorizeMutation(ctx, objectiveID)
	if err != nil {
		return FSMutationResult{}, err
	}
	if !decision.Allowed {
		return FSMutationResult{}, PolicyViolationError{Decision: decision}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return FSMutationResult{}, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return FSMutationResult{}, err
	}
	afterHash := sha256Text([]byte(content))
	return FSMutationResult{OK: true, Receipt: FSMutationReceipt{Objective: objective, Policy: decision, ObjectiveReceipt: objectiveReceipt, Workspace: info, Path: filepath.ToSlash(rel), BeforeSHA256: beforeHash, AfterSHA256: afterHash}}, nil
}

func (g *FSGuard) root(workspaceID string) (string, error) {
	info, err := g.workspaces.workspace(workspaceID)
	if err != nil {
		return "", err
	}
	root, err := guardedPath(g.workspaces.root, info.ID, false)
	if err != nil {
		return "", err
	}
	if filepath.Clean(root) != filepath.Clean(info.Path) {
		return "", errors.New("workspace identity path does not match managed root")
	}
	return root, nil
}

func guardedPath(root, rel string, allowMissingLeaf bool) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) {
		return "", ErrFSPathEscape
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrFSPathEscape
	}
	path := filepath.Join(root, clean)
	if !contained(root, path) {
		return "", ErrFSPathEscape
	}
	current := root
	parts := strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == '\\' })
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) && allowMissingLeaf && i == len(parts)-1 {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s", ErrFSSymlink, current)
		}
	}
	return path, nil
}
