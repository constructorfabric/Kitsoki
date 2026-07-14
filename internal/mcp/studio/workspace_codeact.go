package studio

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	starlarkhost "kitsoki/internal/host/starlark"
	mcp "kitsoki/internal/mcp"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workspaceCodeactCapabilityDenied = "CAPABILITY_DENIED"
	workspaceCodeactPathDenied       = "PATH_DENIED"
	workspaceCodeactWorkspaceDenied  = "WORKSPACE_DENIED"
	workspaceCodeactEvaluationDenied = "EVALUATION_DENIED"
)

// WorkspaceCodeactInput intentionally omits a working directory. The caller
// can name only a server-held managed workspace, which fixes the evaluator's
// root before the snippet is parsed.
type WorkspaceCodeactInput struct {
	ObjectiveID           string         `json:"objective_id"`
	WorkspaceID           string         `json:"workspace_id"`
	Snippet               string         `json:"snippet"`
	Inputs                map[string]any `json:"inputs,omitempty"`
	World                 map[string]any `json:"world,omitempty"`
	RequestedCapabilities map[string]any `json:"requested_capabilities,omitempty"`
}

type WorkspaceCodeactMutationSummary struct {
	Count int      `json:"count"`
	Paths []string `json:"paths,omitempty"`
}

type WorkspaceCodeactInspectionSummary struct {
	Count int                            `json:"count"`
	Items []starlarkhost.InspectExchange `json:"items,omitempty"`
}

// WorkspaceCodeactReceipt ties every CodeAct result to the already-open
// objective and server-held workspace. Mutations retain the original guarded
// filesystem receipts, including preimage hashes and policy authorization.
type WorkspaceCodeactReceipt struct {
	Objective        Objective           `json:"objective"`
	Workspace        ManagedWorkspace    `json:"workspace"`
	Policy           PolicyDecision      `json:"policy"`
	MutationReceipts []FSMutationReceipt `json:"mutation_receipts,omitempty"`
}

type WorkspaceCodeactResult struct {
	OK                      bool                              `json:"ok"`
	Outputs                 map[string]any                    `json:"outputs"`
	RequestedCapabilities   []string                          `json:"requested_capabilities"`
	EffectiveCapabilities   []string                          `json:"effective_capabilities"`
	RequestedCapabilityHash string                            `json:"requested_capability_hash"`
	EffectiveCapabilityHash string                            `json:"effective_capability_hash"`
	InspectionSummary       WorkspaceCodeactInspectionSummary `json:"inspection_summary"`
	MutationSummary         WorkspaceCodeactMutationSummary   `json:"mutation_summary"`
	Receipt                 WorkspaceCodeactReceipt           `json:"receipt"`
}

// WorkspaceCodeactError is returned to an MCP caller as structured denial
// data rather than a generic Starlark traceback. Hashes make the rejected
// requested authority and the server-held effective ceiling auditable.
type WorkspaceCodeactError struct {
	Code                    string `json:"code"`
	ErrorMessage            string `json:"error"`
	RequestedCapabilityHash string `json:"requested_capability_hash,omitempty"`
	EffectiveCapabilityHash string `json:"effective_capability_hash,omitempty"`
}

func (e *WorkspaceCodeactError) Error() string { return e.ErrorMessage }

// WorkspaceCodeactService evaluates deterministic CodeAct snippets against a
// managed workspace. ceiling is held by the server and callers may request a
// subset only; it is never supplied as a filesystem root or escalation path.
type WorkspaceCodeactService struct {
	workspaces *ManagedWorkspaceService
	guard      *FSGuard
	ceiling    starlarkhost.CapabilitySpec
}

func NewWorkspaceCodeactService(workspaces *ManagedWorkspaceService, guard *FSGuard, ceiling starlarkhost.CapabilitySpec) (*WorkspaceCodeactService, error) {
	if workspaces == nil || guard == nil {
		return nil, errors.New("workspace CodeAct requires managed workspace service and filesystem guard")
	}
	if ceiling.AllowsHost() {
		return nil, errors.New("workspace CodeAct does not allow host capabilities")
	}
	return &WorkspaceCodeactService{workspaces: workspaces, guard: guard, ceiling: normalizedWorkspaceCodeactCapabilities(ceiling)}, nil
}

func (s *WorkspaceCodeactService) Evaluate(ctx context.Context, input WorkspaceCodeactInput) (WorkspaceCodeactResult, error) {
	if strings.TrimSpace(input.ObjectiveID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.Snippet) == "" {
		return WorkspaceCodeactResult{}, &WorkspaceCodeactError{Code: ErrBadRequest, ErrorMessage: "objective_id, workspace_id, and snippet are required"}
	}
	workspace, err := s.workspaces.workspace(input.WorkspaceID)
	if err != nil {
		return WorkspaceCodeactResult{}, s.denial(workspaceCodeactWorkspaceDenied, err.Error(), starlarkhost.DefaultCapabilities())
	}
	if workspace.ObjectiveID != input.ObjectiveID {
		return WorkspaceCodeactResult{}, s.denial(workspaceCodeactWorkspaceDenied, "workspace is bound to another objective", starlarkhost.DefaultCapabilities())
	}
	objective, err := s.workspaces.objectives.Get(ctx, input.ObjectiveID)
	if err != nil {
		return WorkspaceCodeactResult{}, s.denial(workspaceCodeactWorkspaceDenied, err.Error(), starlarkhost.DefaultCapabilities())
	}
	requested, err := starlarkhost.ParseCapabilities(input.RequestedCapabilities)
	if err != nil {
		return WorkspaceCodeactResult{}, s.denial(workspaceCodeactCapabilityDenied, "invalid requested capabilities: "+err.Error(), starlarkhost.DefaultCapabilities())
	}
	requested = normalizedWorkspaceCodeactCapabilities(requested)
	if err := ensureCapabilitySubset(requested, s.ceiling); err != nil {
		return WorkspaceCodeactResult{}, s.denial(workspaceCodeactCapabilityDenied, err.Error(), requested)
	}

	inspector := newWorkspaceCodeactInspector(input.ObjectiveID, input.WorkspaceID, s.guard)
	evaluation, err := mcp.EvaluateCodeact(ctx, mcp.CodeactEvaluationConfig{
		WorkingDir: workspace.Path,
		Args: mcp.CodeactEvalArgs{
			Snippet: input.Snippet,
			Inputs:  input.Inputs,
			World:   input.World,
		},
		Capabilities: requested,
		Inspector:    inspector,
	})
	if err != nil {
		return WorkspaceCodeactResult{}, s.denial(classifyWorkspaceCodeactError(err), err.Error(), requested)
	}
	inspections, mutations := inspector.snapshot()
	policy := PolicyDecision{Allowed: true, PolicyHash: objective.Policy.EffectiveHash()}
	if len(mutations) > 0 {
		policy = mutations[len(mutations)-1].Policy
	}
	paths := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		paths = append(paths, mutation.Path)
	}
	return WorkspaceCodeactResult{
		OK:                      true,
		Outputs:                 evaluation.Outputs,
		RequestedCapabilities:   requested.CapabilityLabels(),
		EffectiveCapabilities:   requested.CapabilityLabels(),
		RequestedCapabilityHash: mcp.CodeactCapabilityHash(requested),
		EffectiveCapabilityHash: mcp.CodeactCapabilityHash(requested),
		InspectionSummary:       WorkspaceCodeactInspectionSummary{Count: len(inspections), Items: inspections},
		MutationSummary:         WorkspaceCodeactMutationSummary{Count: len(mutations), Paths: paths},
		Receipt: WorkspaceCodeactReceipt{
			Objective: objective, Workspace: workspace, Policy: policy, MutationReceipts: mutations,
		},
	}, nil
}

func (s *WorkspaceCodeactService) denial(code, message string, requested starlarkhost.CapabilitySpec) *WorkspaceCodeactError {
	return &WorkspaceCodeactError{
		Code: code, ErrorMessage: message,
		RequestedCapabilityHash: mcp.CodeactCapabilityHash(requested),
		EffectiveCapabilityHash: mcp.CodeactCapabilityHash(s.ceiling),
	}
}

// RegisterWorkspaceCodeactTool remains uncalled by NewServer. The final
// integration slice owns public registration and profile selection.
func RegisterWorkspaceCodeactTool(server *mcpsdk.Server, service *WorkspaceCodeactService) {
	if server == nil || service == nil {
		panic("workspace CodeAct tool requires server and service")
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "workspace.codeact", Description: "Evaluate one capability-scoped Starlark action in a server-held managed workspace.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input WorkspaceCodeactInput) (*mcpsdk.CallToolResult, any, error) {
		out, err := service.Evaluate(ctx, input)
		if err != nil {
			return workspaceCodeactToolError(err), nil, nil
		}
		return nil, out, nil
	})
}

func workspaceCodeactToolError(err error) *mcpsdk.CallToolResult {
	payload := WorkspaceCodeactError{Code: workspaceCodeactEvaluationDenied, ErrorMessage: err.Error()}
	var typed *WorkspaceCodeactError
	if errors.As(err, &typed) {
		payload = *typed
	}
	return &mcpsdk.CallToolResult{
		IsError:           true,
		StructuredContent: payload,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf(`{"ok":false,"code":%q,"error":%q,"requested_capability_hash":%q,"effective_capability_hash":%q}`,
			payload.Code, payload.ErrorMessage, payload.RequestedCapabilityHash, payload.EffectiveCapabilityHash)}},
	}
}

func normalizedWorkspaceCodeactCapabilities(cap starlarkhost.CapabilitySpec) starlarkhost.CapabilitySpec {
	if cap.Stdlib == nil && !cap.World && !cap.NeedsHTTP() && !cap.NeedsInspector() && !cap.AllowsHost() {
		return starlarkhost.DefaultCapabilities()
	}
	if cap.Stdlib == nil {
		cap.Stdlib = starlarkhost.DefaultCapabilities().Stdlib
	}
	return cap
}

func ensureCapabilitySubset(requested, ceiling starlarkhost.CapabilitySpec) error {
	if requested.AllowsHost() {
		return errors.New("host capabilities are not available to workspace CodeAct")
	}
	for name, enabled := range requested.Stdlib {
		if enabled && !ceiling.Stdlib[name] {
			return fmt.Errorf("stdlib capability %q exceeds the server-held ceiling", name)
		}
	}
	if requested.World && !ceiling.World {
		return errors.New("world capability exceeds the server-held ceiling")
	}
	if err := ensureHTTPSubset(requested.HTTP, ceiling.HTTP); err != nil {
		return err
	}
	if err := ensurePatternsSubset("fs.read", requested.FS.ReadPatterns, ceiling.FS.ReadPatterns); err != nil {
		return err
	}
	if err := ensurePatternsSubset("fs.write", requested.FS.WritePatterns, ceiling.FS.WritePatterns); err != nil {
		return err
	}
	if requested.FS.MaxBytes > 0 && ceiling.FS.MaxBytes > 0 && requested.FS.MaxBytes > ceiling.FS.MaxBytes {
		return errors.New("fs.max_bytes exceeds the server-held ceiling")
	}
	if requested.FS.MaxBytes == 0 && ceiling.FS.MaxBytes > 0 && (len(requested.FS.ReadPatterns) > 0 || len(requested.FS.WritePatterns) > 0) {
		return errors.New("unbounded fs.max_bytes exceeds the server-held ceiling")
	}
	for _, name := range requested.Probe.Names {
		if !containsString(ceiling.Probe.Names, name) {
			return fmt.Errorf("probe capability %q exceeds the server-held ceiling", name)
		}
	}
	return nil
}

func ensureHTTPSubset(requested, ceiling starlarkhost.HTTPCapability) error {
	if !requested.Enabled {
		return nil
	}
	if !ceiling.Enabled {
		return errors.New("http capability exceeds the server-held ceiling")
	}
	if len(requested.Methods) == 0 && len(ceiling.Methods) > 0 {
		return errors.New("unbounded http methods exceed the server-held ceiling")
	}
	for _, method := range requested.Methods {
		if len(ceiling.Methods) > 0 && !containsString(ceiling.Methods, method) {
			return fmt.Errorf("http method %q exceeds the server-held ceiling", method)
		}
	}
	if len(requested.Hosts) == 0 && len(ceiling.Hosts) > 0 {
		return errors.New("unbounded http hosts exceed the server-held ceiling")
	}
	for _, host := range requested.Hosts {
		if len(ceiling.Hosts) > 0 && !containsString(ceiling.Hosts, host) {
			return fmt.Errorf("http host %q exceeds the server-held ceiling", host)
		}
	}
	return nil
}

func ensurePatternsSubset(label string, requested, ceiling []string) error {
	for _, pattern := range requested {
		allowed := false
		for _, maximum := range ceiling {
			if maximum == "**" || maximum == pattern || (strings.HasSuffix(maximum, "/**") && strings.HasPrefix(pattern, strings.TrimSuffix(maximum, "/**")+"/")) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%s pattern %q exceeds the server-held ceiling", label, pattern)
		}
	}
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func classifyWorkspaceCodeactError(err error) string {
	message := err.Error()
	if errors.Is(err, ErrFSPathEscape) || errors.Is(err, ErrFSSymlink) || strings.Contains(message, "escapes the managed root") || strings.Contains(message, "may not traverse symlinks") {
		return workspaceCodeactPathDenied
	}
	if strings.Contains(message, "not granted") || strings.Contains(message, "no inspector") {
		return workspaceCodeactCapabilityDenied
	}
	return workspaceCodeactEvaluationDenied
}

type workspaceCodeactInspector struct {
	objectiveID string
	workspaceID string
	guard       *FSGuard

	mu          sync.Mutex
	preimages   map[string]string
	inspections []starlarkhost.InspectExchange
	mutations   []FSMutationReceipt
}

func newWorkspaceCodeactInspector(objectiveID, workspaceID string, guard *FSGuard) *workspaceCodeactInspector {
	return &workspaceCodeactInspector{objectiveID: objectiveID, workspaceID: workspaceID, guard: guard, preimages: map[string]string{}}
}

func (i *workspaceCodeactInspector) Read(ctx context.Context, path string) ([]byte, error) {
	result, err := i.guard.Read(ctx, FSReadInput{WorkspaceID: i.workspaceID, Path: path})
	if err != nil {
		i.record("read", path, inspectStatus(err))
		return nil, err
	}
	i.mu.Lock()
	i.preimages[workspaceCodeactPathKey(path)] = result.SHA256
	i.inspections = append(i.inspections, starlarkhost.InspectExchange{Op: "read", Target: result.Path, Status: "ok"})
	i.mu.Unlock()
	return []byte(result.Content), nil
}

func (i *workspaceCodeactInspector) Exists(ctx context.Context, path string) (bool, error) {
	result, err := i.guard.Read(ctx, FSReadInput{WorkspaceID: i.workspaceID, Path: path})
	if errors.Is(err, os.ErrNotExist) {
		i.mu.Lock()
		i.preimages[workspaceCodeactPathKey(path)] = sha256Text(nil)
		i.inspections = append(i.inspections, starlarkhost.InspectExchange{Op: "exists", Target: filepath.ToSlash(path), Status: "missing"})
		i.mu.Unlock()
		return false, nil
	}
	if err != nil {
		i.record("exists", path, inspectStatus(err))
		return false, err
	}
	i.mu.Lock()
	i.preimages[workspaceCodeactPathKey(path)] = result.SHA256
	i.inspections = append(i.inspections, starlarkhost.InspectExchange{Op: "exists", Target: result.Path, Status: "ok"})
	i.mu.Unlock()
	return true, nil
}

func (i *workspaceCodeactInspector) Glob(_ context.Context, pattern string) ([]string, error) {
	root, err := i.guard.root(i.workspaceID)
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(pattern) || workspaceCodeactPathKey(pattern) == ".." || strings.HasPrefix(workspaceCodeactPathKey(pattern), "../") {
		return nil, ErrFSPathEscape
	}
	matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		rel, err := filepath.Rel(root, match)
		if err != nil {
			return nil, err
		}
		if _, err := guardedPath(root, rel, false); err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	i.record("glob", pattern, fmt.Sprintf("matched:%d", len(out)))
	return out, nil
}

func (i *workspaceCodeactInspector) Write(ctx context.Context, path string, content []byte) (string, error) {
	key := workspaceCodeactPathKey(path)
	i.mu.Lock()
	preimage, ok := i.preimages[key]
	i.mu.Unlock()
	if !ok {
		err := fmt.Errorf("%w: ctx.fs.write(%q) requires an earlier ctx.fs.read or ctx.fs.exists", ErrFSPreimage, path)
		i.record("write", path, inspectStatus(err))
		return "", err
	}
	result, err := i.guard.Patch(ctx, FSPatchInput{
		ObjectiveID: i.objectiveID, WorkspaceID: i.workspaceID, Path: path, Replacement: string(content), PreimageSHA256: preimage,
	})
	if err != nil {
		i.record("write", path, inspectStatus(err))
		return "", err
	}
	i.mu.Lock()
	i.preimages[key] = result.Receipt.AfterSHA256
	i.mutations = append(i.mutations, result.Receipt)
	i.inspections = append(i.inspections, starlarkhost.InspectExchange{Op: "write", Target: filepath.ToSlash(path), Status: "ok"})
	i.mu.Unlock()
	return filepath.ToSlash(path), nil
}

func (i *workspaceCodeactInspector) Probe(_ context.Context, name string, _ []string) (starlarkhost.ProbeResult, error) {
	return starlarkhost.ProbeResult{}, fmt.Errorf("ctx.probe %q is not available to workspace CodeAct", name)
}

func (i *workspaceCodeactInspector) record(op, target, status string) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.inspections = append(i.inspections, starlarkhost.InspectExchange{Op: op, Target: filepath.ToSlash(target), Status: status})
}

func (i *workspaceCodeactInspector) snapshot() ([]starlarkhost.InspectExchange, []FSMutationReceipt) {
	i.mu.Lock()
	defer i.mu.Unlock()
	inspections := append([]starlarkhost.InspectExchange(nil), i.inspections...)
	mutations := append([]FSMutationReceipt(nil), i.mutations...)
	return inspections, mutations
}

func workspaceCodeactPathKey(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func inspectStatus(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "denied"
	}
	return "error"
}
