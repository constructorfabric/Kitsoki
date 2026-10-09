package studio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// GateStatus separates a red gate (a domain result) from inability to execute
// it. Callers must not turn a failed gate into a transport error and lose that
// distinction.
type GateStatus string

const (
	GatePassed         GateStatus = "passed"
	GateFailed         GateStatus = "failed"
	GateTimedOut       GateStatus = "timed_out"
	GateInfraError     GateStatus = "infra_error"
	GatePolicyRejected GateStatus = "policy_rejected"
	GateStale          GateStatus = "stale"
)

// GateCommandRunner is the execution seam. The default direct-exec runner has
// no shell; tests inject deterministic results and never invoke a live agent.
type GateCommandRunner interface {
	Run(context.Context, string, string, ...string) (GateCommandResult, error)
}

type GateCommandResult struct {
	ExitCode int
	Output   string
}

type directGateCommandRunner struct{}

func (directGateCommandRunner) Run(ctx context.Context, dir, program string, args ...string) (GateCommandResult, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	result := GateCommandResult{Output: string(output)}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err == nil {
		return result, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}

// GateWorkspaceProbe observes the managed workspace state before and after a
// gate. The fixed git rev-parse command is internal evidence collection, not
// caller-provided execution.
type GateWorkspaceProbe interface {
	Head(context.Context, ManagedWorkspace) (string, error)
}

type gitGateWorkspaceProbe struct{}

func (gitGateWorkspaceProbe) Head(ctx context.Context, workspace ManagedWorkspace) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = workspace.Path
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(output))
	if head == "" {
		return "", errors.New("git rev-parse returned an empty head")
	}
	return head, nil
}

type GateRunInput struct {
	ObjectiveID string            `json:"objective_id"`
	WorkspaceID string            `json:"workspace_id"`
	Gate        string            `json:"gate"`
	Args        map[string]string `json:"args,omitempty"`
}

type GateReceipt struct {
	Objective        Objective        `json:"objective"`
	Workspace        ManagedWorkspace `json:"workspace"`
	Gate             string           `json:"gate"`
	GateHash         string           `json:"gate_hash"`
	CatalogHash      string           `json:"catalog_hash"`
	PolicyHash       string           `json:"policy_hash"`
	WorkspaceHead    string           `json:"workspace_head"`
	OutputSHA256     string           `json:"output_sha256"`
	Evidence         Evidence         `json:"evidence"`
	ObjectiveReceipt Receipt          `json:"objective_receipt"`
	RecordedAt       time.Time        `json:"recorded_at"`
}

type GateRunResult struct {
	Status     GateStatus   `json:"status"`
	Gate       string       `json:"gate"`
	ExitCode   int          `json:"exit_code,omitempty"`
	Output     string       `json:"output,omitempty"`
	Error      string       `json:"error,omitempty"`
	BeforeHead string       `json:"before_head,omitempty"`
	AfterHead  string       `json:"after_head,omitempty"`
	Receipt    *GateReceipt `json:"receipt,omitempty"`
}

type GateCatalogResult struct {
	WorkspaceID string           `json:"workspace_id"`
	CatalogHash string           `json:"catalog_hash"`
	Gates       []GateDefinition `json:"gates"`
}

// GateRunner binds a declarative catalog to server-held managed workspaces and
// the objective evidence store. It has no mechanism to accept a caller command
// string or arbitrary argv.
type GateRunner struct {
	catalog    GateCatalog
	workspaces *ManagedWorkspaceService
	runner     GateCommandRunner
	probe      GateWorkspaceProbe
	now        func() time.Time
}

func NewGateRunner(catalog GateCatalog, workspaces *ManagedWorkspaceService, runner GateCommandRunner, probe GateWorkspaceProbe, now func() time.Time) (*GateRunner, error) {
	if len(catalog.definitions) == 0 || workspaces == nil {
		return nil, errors.New("gate catalog and managed workspace service are required")
	}
	if runner == nil {
		runner = directGateCommandRunner{}
	}
	if probe == nil {
		probe = gitGateWorkspaceProbe{}
	}
	if now == nil {
		now = time.Now
	}
	return &GateRunner{catalog: catalog, workspaces: workspaces, runner: runner, probe: probe, now: now}, nil
}

func (r *GateRunner) Catalog(workspaceID string) (GateCatalogResult, error) {
	if _, err := r.workspaces.workspace(workspaceID); err != nil {
		return GateCatalogResult{}, err
	}
	return GateCatalogResult{WorkspaceID: workspaceID, CatalogHash: r.catalog.Hash(), Gates: r.catalog.Definitions()}, nil
}

func (r *GateRunner) Run(ctx context.Context, input GateRunInput) GateRunResult {
	result := GateRunResult{Gate: input.Gate}
	if strings.TrimSpace(input.ObjectiveID) == "" || strings.TrimSpace(input.WorkspaceID) == "" || strings.TrimSpace(input.Gate) == "" {
		return gateRejected(result, "objective_id, workspace_id, and gate are required")
	}
	workspace, err := r.workspaces.workspace(input.WorkspaceID)
	if err != nil {
		return gateRejected(result, err.Error())
	}
	if workspace.ObjectiveID != input.ObjectiveID {
		return gateRejected(result, "workspace is bound to another objective")
	}
	objective, err := r.workspaces.objectives.Get(ctx, input.ObjectiveID)
	if err != nil {
		return gateRejected(result, err.Error())
	}
	if objective.Status != ObjectiveOpen {
		return gateRejected(result, "objective is not open")
	}
	definition, ok := r.catalog.Definition(input.Gate)
	if !ok {
		return gateRejected(result, fmt.Sprintf("unknown gate %q", input.Gate))
	}
	args, err := definition.render(input.Args)
	if err != nil {
		return gateRejected(result, err.Error())
	}
	beforeHead, err := r.probe.Head(ctx, workspace)
	if err != nil {
		return gateInfra(result, fmt.Sprintf("read workspace head: %v", err))
	}
	result.BeforeHead = beforeHead
	gateCtx, cancel := context.WithTimeout(ctx, definition.Timeout)
	commandResult, err := r.runner.Run(gateCtx, workspace.Path, definition.Program, args...)
	cancel()
	result.ExitCode, result.Output = commandResult.ExitCode, commandResult.Output
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(gateCtx.Err(), context.DeadlineExceeded) {
		return gateTimedOut(result, "gate exceeded its declared timeout")
	}
	if err != nil {
		return gateInfra(result, err.Error())
	}
	afterHead, err := r.probe.Head(ctx, workspace)
	if err != nil {
		return gateInfra(result, fmt.Sprintf("read workspace head after gate: %v", err))
	}
	result.AfterHead = afterHead
	if beforeHead != afterHead {
		return gateStale(result, "workspace head changed while the gate ran")
	}
	if commandResult.ExitCode != 0 {
		result.Status = GateFailed
		return result
	}

	gateHash := hashGateDefinition(definition)
	outputHash := sha256Text([]byte(commandResult.Output))
	evidence, receipt, err := r.workspaces.objectives.RecordEvidence(ctx, input.ObjectiveID, RecordEvidenceInput{
		ID:      fmt.Sprintf("gate:%s:%s:%s", definition.Name, beforeHead, gateHash[:12]),
		Kind:    "gate",
		Summary: fmt.Sprintf("%s passed at %s (catalog=%s gate=%s output=%s)", definition.Name, beforeHead, r.catalog.Hash(), gateHash, outputHash),
	})
	if err != nil {
		return gateInfra(result, fmt.Sprintf("record gate evidence: %v", err))
	}
	result.Status = GatePassed
	result.Receipt = &GateReceipt{
		Objective: objective, Workspace: workspace, Gate: definition.Name, GateHash: gateHash, CatalogHash: r.catalog.Hash(),
		PolicyHash: objective.Policy.EffectiveHash(), WorkspaceHead: beforeHead, OutputSHA256: outputHash,
		Evidence: evidence, ObjectiveReceipt: receipt, RecordedAt: r.now().UTC(),
	}
	return result
}

func gateRejected(result GateRunResult, message string) GateRunResult {
	result.Status, result.Error = GatePolicyRejected, message
	return result
}
func gateInfra(result GateRunResult, message string) GateRunResult {
	result.Status, result.Error = GateInfraError, message
	return result
}
func gateTimedOut(result GateRunResult, message string) GateRunResult {
	result.Status, result.Error = GateTimedOut, message
	return result
}
func gateStale(result GateRunResult, message string) GateRunResult {
	result.Status, result.Error = GateStale, message
	return result
}

func hashGateDefinition(definition GateDefinition) string {
	catalog, err := NewGateCatalog([]GateDefinition{definition})
	if err != nil {
		panic(err)
	}
	return catalog.Hash()
}

// RegisterGateTools is intentionally unregistered from NewServer. The final
// integration profile owns public tool selection and host.run migration.
func RegisterGateTools(server *mcpsdk.Server, runner *GateRunner) {
	if server == nil || runner == nil {
		panic("gate tools require a server and runner")
	}
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "gate.catalog", Description: "List declarative, no-shell gates available for a server-held workspace."}, func(_ context.Context, _ *mcpsdk.CallToolRequest, input struct {
		WorkspaceID string `json:"workspace_id"`
	}) (*mcpsdk.CallToolResult, any, error) {
		out, err := runner.Catalog(input.WorkspaceID)
		if err != nil {
			return workspaceToolError(err), nil, nil
		}
		return nil, out, nil
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "gate.run", Description: "Run a named declared gate against a server-held workspace; never accepts a command string."}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, input GateRunInput) (*mcpsdk.CallToolResult, any, error) {
		return nil, runner.Run(ctx, input), nil
	})
}

func hashGateOutput(output string) string {
	sum := sha256.Sum256([]byte(output))
	return hex.EncodeToString(sum[:])
}
