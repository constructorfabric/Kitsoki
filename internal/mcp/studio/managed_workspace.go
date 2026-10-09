package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// WorkspaceCommandRunner is the only process seam used by managed workspace
// tools. Production delegates every lifecycle operation to dev-workspace.sh;
// tests use a deterministic fake and never construct a repository by hand.
type WorkspaceCommandRunner interface {
	Run(context.Context, string, ...string) (WorkspaceCommandResult, error)
}

type WorkspaceCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type execWorkspaceCommandRunner struct{}

func (execWorkspaceCommandRunner) Run(ctx context.Context, command string, args ...string) (WorkspaceCommandResult, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdout, err := cmd.Output()
	result := WorkspaceCommandResult{Stdout: string(stdout)}
	if exit, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exit.ExitCode()
		result.Stderr = string(exit.Stderr)
		return result, nil
	}
	return result, err
}

// ManagedWorkspace is a server-held clone-backed workspace identity. Callers pass
// its ID, never an arbitrary filesystem root, after create succeeds.
type ManagedWorkspace struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Branch      string `json:"branch,omitempty"`
	Base        string `json:"base,omitempty"`
	Target      string `json:"target,omitempty"`
	ObjectiveID string `json:"objective_id"`
}

type WorkspaceSnapshot struct {
	Workspace ManagedWorkspace `json:"workspace"`
	Output    string           `json:"output,omitempty"`
	ExitCode  int              `json:"exit_code"`
}

// WorkspaceReceipt makes the mutation's authority, workspace identity and
// observed before/after state explicit. ObjectiveReceipt remains additive in
// the objective store; this result is the operation-specific evidence.
type WorkspaceReceipt struct {
	Objective        Objective         `json:"objective"`
	Policy           PolicyDecision    `json:"policy"`
	ObjectiveReceipt Receipt           `json:"objective_receipt"`
	Workspace        ManagedWorkspace  `json:"workspace"`
	Before           WorkspaceSnapshot `json:"before"`
	After            WorkspaceSnapshot `json:"after"`
	RecordedAt       time.Time         `json:"recorded_at"`
}

type ManagedWorkspaceService struct {
	root       string
	repo       string
	scriptPath string
	runner     WorkspaceCommandRunner
	objectives *ObjectiveService
	now        func() time.Time
	mu         sync.RWMutex
	workspaces map[string]ManagedWorkspace
}

func NewManagedWorkspaceService(root, scriptPath string, runner WorkspaceCommandRunner, objectives *ObjectiveService, now func() time.Time) (*ManagedWorkspaceService, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(scriptPath) == "" || objectives == nil {
		return nil, errors.New("managed workspace root, script path, and objective service are required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace root: %w", err)
	}
	// Production accepts only the checked-in lifecycle script beneath the
	// conventional <repo>/.capsules/workspaces root. Tests retain an injected
	// runner and may use a synthetic script path; production always invokes an
	// argv vector through exec.CommandContext, never a shell string.
	var repoRoot string
	if runner == nil {
		repoRoot = filepath.Dir(filepath.Dir(absRoot))
		expected := filepath.Join(repoRoot, "scripts", "dev-workspace.sh")
		scriptAbs, err := filepath.Abs(scriptPath)
		if err != nil || filepath.Clean(scriptAbs) != filepath.Clean(expected) {
			return nil, errors.New("production managed workspace service requires the checked-in scripts/dev-workspace.sh")
		}
		info, err := os.Lstat(scriptAbs)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o111 == 0 {
			return nil, errors.New("checked-in scripts/dev-workspace.sh must be an executable non-symlink")
		}
		scriptPath = scriptAbs
		runner = execWorkspaceCommandRunner{}
	}
	if now == nil {
		now = time.Now
	}
	return &ManagedWorkspaceService{root: filepath.Clean(absRoot), repo: repoRoot, scriptPath: scriptPath, runner: runner, objectives: objectives, now: now, workspaces: map[string]ManagedWorkspace{}}, nil
}

type WorkspaceCreateInput struct {
	ObjectiveID string `json:"objective_id"`
	ID          string `json:"id"`
	Branch      string `json:"branch"`
	SessionID   string `json:"session_id,omitempty"`
	// Bootstrap defaults to true. Bounded callers that neither run Go nor
	// browser tooling may explicitly opt out so their disposable workspace
	// teardown does not carry bootstrap-only dependencies.
	Bootstrap   *bool  `json:"bootstrap,omitempty"`
}

type WorkspaceActionInput struct {
	ObjectiveID string `json:"objective_id"`
	WorkspaceID string `json:"workspace_id"`
	Message     string `json:"message,omitempty"`
}

type WorkspaceMutationResult struct {
	OK      bool             `json:"ok"`
	Receipt WorkspaceReceipt `json:"receipt"`
}

func (s *ManagedWorkspaceService) Create(ctx context.Context, input WorkspaceCreateInput) (WorkspaceMutationResult, error) {
	if strings.TrimSpace(input.ObjectiveID) == "" || !validWorkspaceID(input.ID) || strings.TrimSpace(input.Branch) == "" {
		return WorkspaceMutationResult{}, errors.New("objective_id, safe id, and branch are required")
	}
	out, err := s.mutate(ctx, input.ObjectiveID, ManagedWorkspace{ID: input.ID, ObjectiveID: input.ObjectiveID}, func() (WorkspaceSnapshot, WorkspaceSnapshot, error) {
		before := WorkspaceSnapshot{Workspace: ManagedWorkspace{ID: input.ID, ObjectiveID: input.ObjectiveID}}
		args := []string{"create", "--id", input.ID, "--branch", input.Branch}
		bootstrap := input.Bootstrap == nil || *input.Bootstrap
		// MCP operating-system calibration workspaces only exercise a fixed
		// managed lifecycle and registered Go gates. They are disposable and
		// must remain cheap and reliably removable, so the strict calibration
		// namespace never installs bootstrap-only dependencies. This is enforced
		// server-side rather than trusting an agent to preserve the optional
		// bootstrap argument in a tool call.
		if strings.HasPrefix(input.ObjectiveID, "mcp-os-cal-") {
			bootstrap = false
		}
		if bootstrap {
			args = append(args, "--bootstrap")
		}
		args = append(args, "--json")
		if input.SessionID != "" {
			args = append(args, "--session-id", input.SessionID)
		}
		res, err := s.runWorkspaceCommand(ctx, args...)
		if err != nil {
			return before, WorkspaceSnapshot{}, err
		}
		if res.ExitCode != 0 {
			return before, WorkspaceSnapshot{}, commandFailure("create", res)
		}
		info, err := s.decodeWorkspace(input.ObjectiveID, res.Stdout)
		if err != nil {
			return before, WorkspaceSnapshot{}, err
		}
		if info.ID != input.ID {
			return before, WorkspaceSnapshot{}, errors.New("create returned a different workspace id")
		}
		s.mu.Lock()
		s.workspaces[info.ID] = info
		s.mu.Unlock()
		return before, WorkspaceSnapshot{Workspace: info, Output: res.Stdout, ExitCode: res.ExitCode}, nil
	})
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	out.Receipt.Workspace = out.Receipt.After.Workspace
	return out, nil
}

func (s *ManagedWorkspaceService) Status(ctx context.Context, workspaceID string) (WorkspaceSnapshot, error) {
	info, err := s.workspace(workspaceID)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	return s.status(ctx, info)
}

func (s *ManagedWorkspaceService) Commit(ctx context.Context, input WorkspaceActionInput) (WorkspaceMutationResult, error) {
	if strings.TrimSpace(input.Message) == "" {
		return WorkspaceMutationResult{}, errors.New("commit message is required")
	}
	return s.action(ctx, input, "commit", "--message", input.Message)
}

func (s *ManagedWorkspaceService) Merge(ctx context.Context, input WorkspaceActionInput) (WorkspaceMutationResult, error) {
	return s.action(ctx, input, "merge")
}

func (s *ManagedWorkspaceService) Teardown(ctx context.Context, input WorkspaceActionInput) (WorkspaceMutationResult, error) {
	return s.action(ctx, input, "teardown")
}

func (s *ManagedWorkspaceService) action(ctx context.Context, input WorkspaceActionInput, verb string, extra ...string) (WorkspaceMutationResult, error) {
	if input.ObjectiveID == "" || input.WorkspaceID == "" {
		return WorkspaceMutationResult{}, errors.New("objective_id and workspace_id are required")
	}
	info, err := s.workspace(input.WorkspaceID)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if info.ObjectiveID != input.ObjectiveID {
		return WorkspaceMutationResult{}, errors.New("workspace is bound to another objective")
	}
	return s.mutate(ctx, input.ObjectiveID, info, func() (WorkspaceSnapshot, WorkspaceSnapshot, error) {
		before, err := s.status(ctx, info)
		if err != nil {
			return WorkspaceSnapshot{}, WorkspaceSnapshot{}, err
		}
		args := append([]string{verb, info.Path}, extra...)
		if verb == "merge" {
			args = append(args, "--teardown")
		}
		res, err := s.runWorkspaceCommand(ctx, args...)
		if err != nil {
			return before, WorkspaceSnapshot{}, err
		}
		if res.ExitCode != 0 {
			return before, WorkspaceSnapshot{}, commandFailure(verb, res)
		}
		after := WorkspaceSnapshot{Workspace: info, Output: res.Stdout, ExitCode: res.ExitCode}
		if verb != "teardown" && verb != "merge" {
			after, err = s.status(ctx, info)
			if err != nil {
				return before, WorkspaceSnapshot{}, err
			}
		}
		if verb == "teardown" || verb == "merge" {
			s.mu.Lock()
			delete(s.workspaces, info.ID)
			s.mu.Unlock()
		}
		return before, after, nil
	})
}

func (s *ManagedWorkspaceService) mutate(ctx context.Context, objectiveID string, info ManagedWorkspace, fn func() (WorkspaceSnapshot, WorkspaceSnapshot, error)) (WorkspaceMutationResult, error) {
	decision, objective, objectiveReceipt, err := s.objectives.AuthorizeMutation(ctx, objectiveID)
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	if !decision.Allowed {
		return WorkspaceMutationResult{}, PolicyViolationError{Decision: decision}
	}
	before, after, err := fn()
	if err != nil {
		return WorkspaceMutationResult{}, err
	}
	return WorkspaceMutationResult{OK: true, Receipt: WorkspaceReceipt{Objective: objective, Policy: decision, ObjectiveReceipt: objectiveReceipt, Workspace: info, Before: before, After: after, RecordedAt: s.now().UTC()}}, nil
}

func (s *ManagedWorkspaceService) status(ctx context.Context, info ManagedWorkspace) (WorkspaceSnapshot, error) {
	res, err := s.runWorkspaceCommand(ctx, "status", info.Path, "--json")
	if err != nil {
		return WorkspaceSnapshot{}, err
	}
	if res.ExitCode != 0 {
		return WorkspaceSnapshot{}, commandFailure("status", res)
	}
	return WorkspaceSnapshot{Workspace: info, Output: res.Stdout, ExitCode: res.ExitCode}, nil
}

// runWorkspaceCommand pins production lifecycle operations to the trusted
// repository and workspace root selected when the service was constructed.
// Without these explicit arguments dev-workspace.sh would discover the
// subprocess CWD, which may be a downstream project attached to Studio MCP.
// Injected test runners keep their existing argument surface.
func (s *ManagedWorkspaceService) runWorkspaceCommand(ctx context.Context, args ...string) (WorkspaceCommandResult, error) {
	if s.repo != "" {
		args = append(args, "--repo", s.repo, "--root", s.root)
	}
	return s.runner.Run(ctx, s.scriptPath, args...)
}

func (s *ManagedWorkspaceService) decodeWorkspace(objectiveID, output string) (ManagedWorkspace, error) {
	var raw struct{ ID, Path, Branch, Base, Target string }
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return ManagedWorkspace{}, fmt.Errorf("decode dev-workspace response: %w", err)
	}
	if !validWorkspaceID(raw.ID) || raw.Path == "" {
		return ManagedWorkspace{}, errors.New("dev-workspace response lacks safe id or path")
	}
	abs, err := filepath.Abs(raw.Path)
	if err != nil {
		return ManagedWorkspace{}, err
	}
	if !contained(s.root, abs) || filepath.Dir(filepath.Clean(abs)) != s.root || filepath.Base(filepath.Clean(abs)) != raw.ID {
		return ManagedWorkspace{}, errors.New("dev-workspace returned a path outside the managed root")
	}
	return ManagedWorkspace{ID: raw.ID, Path: filepath.Clean(abs), Branch: raw.Branch, Base: raw.Base, Target: raw.Target, ObjectiveID: objectiveID}, nil
}

func (s *ManagedWorkspaceService) workspace(id string) (ManagedWorkspace, error) {
	s.mu.RLock()
	info, ok := s.workspaces[id]
	s.mu.RUnlock()
	if !ok {
		return ManagedWorkspace{}, errors.New("unknown managed workspace")
	}
	return info, nil
}

func validWorkspaceID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\\`)
}
func contained(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func commandFailure(verb string, result WorkspaceCommandResult) error {
	return fmt.Errorf("dev-workspace %s exited %d: %s", verb, result.ExitCode, strings.TrimSpace(result.Stderr))
}

// RegisterManagedWorkspaceTools is intentionally not called by NewServer. The
// integration slice owns public registration and profile selection.
func RegisterManagedWorkspaceTools(server *mcpsdk.Server, service *ManagedWorkspaceService, guard *FSGuard) {
	if server == nil || service == nil || guard == nil {
		panic("managed workspace tools require server, service, and guard")
	}
	h := managedWorkspaceHandlers{service: service, guard: guard}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.create", Description: "Create a clone-backed managed workspace through scripts/dev-workspace.sh."}, h.create)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.status", Description: "Read status for a server-held managed workspace."}, h.status)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.commit", Description: "Commit a managed workspace through scripts/dev-workspace.sh."}, h.commit)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.merge", Description: "Merge a clean managed workspace to staging/local through scripts/dev-workspace.sh."}, h.merge)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.teardown", Description: "Tear down a managed workspace through scripts/dev-workspace.sh."}, h.teardown)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.list", Description: "List a guarded workspace directory."}, h.list)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.read", Description: "Read a guarded workspace file."}, h.read)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.search", Description: "Search guarded workspace files."}, h.search)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.write", Description: "Write a guarded workspace file with a required preimage hash."}, h.write)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "workspace.patch", Description: "Replace a guarded workspace file with a required preimage hash."}, h.patch)
}

type managedWorkspaceHandlers struct {
	service *ManagedWorkspaceService
	guard   *FSGuard
}

func (h managedWorkspaceHandlers) create(ctx context.Context, _ *mcpsdk.CallToolRequest, args WorkspaceCreateInput) (*mcpsdk.CallToolResult, any, error) {
	return workspaceToolResult(h.service.Create(ctx, args))
}
func (h managedWorkspaceHandlers) status(ctx context.Context, _ *mcpsdk.CallToolRequest, args struct {
	WorkspaceID string `json:"workspace_id"`
}) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.service.Status(ctx, args.WorkspaceID)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func (h managedWorkspaceHandlers) commit(ctx context.Context, _ *mcpsdk.CallToolRequest, args WorkspaceActionInput) (*mcpsdk.CallToolResult, any, error) {
	return workspaceToolResult(h.service.Commit(ctx, args))
}
func (h managedWorkspaceHandlers) merge(ctx context.Context, _ *mcpsdk.CallToolRequest, args WorkspaceActionInput) (*mcpsdk.CallToolResult, any, error) {
	return workspaceToolResult(h.service.Merge(ctx, args))
}
func (h managedWorkspaceHandlers) teardown(ctx context.Context, _ *mcpsdk.CallToolRequest, args WorkspaceActionInput) (*mcpsdk.CallToolResult, any, error) {
	return workspaceToolResult(h.service.Teardown(ctx, args))
}
func (h managedWorkspaceHandlers) list(ctx context.Context, _ *mcpsdk.CallToolRequest, args FSListInput) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.guard.List(ctx, args)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func (h managedWorkspaceHandlers) read(ctx context.Context, _ *mcpsdk.CallToolRequest, args FSReadInput) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.guard.Read(ctx, args)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func (h managedWorkspaceHandlers) search(ctx context.Context, _ *mcpsdk.CallToolRequest, args FSSearchInput) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.guard.Search(ctx, args)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func (h managedWorkspaceHandlers) write(ctx context.Context, _ *mcpsdk.CallToolRequest, args FSWriteInput) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.guard.Write(ctx, args)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func (h managedWorkspaceHandlers) patch(ctx context.Context, _ *mcpsdk.CallToolRequest, args FSPatchInput) (*mcpsdk.CallToolResult, any, error) {
	out, err := h.guard.Patch(ctx, args)
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func workspaceToolResult(out WorkspaceMutationResult, err error) (*mcpsdk.CallToolResult, any, error) {
	if err != nil {
		return workspaceToolError(err), nil, nil
	}
	return nil, out, nil
}
func workspaceToolError(err error) *mcpsdk.CallToolResult {
	var v PolicyViolationError
	if errors.As(err, &v) {
		return buildToolError(v.Decision.Code, v.Decision.Reason)
	}
	return buildToolError(ErrBadRequest, err.Error())
}

func sha256Text(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
