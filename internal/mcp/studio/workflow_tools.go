package studio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/dynamicworkflow"
)

// registerWorkflowTools wires workflow.* tools over the shared dynamic-workflow
// receipt service.
func (srv *Server) registerWorkflowTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "workflow.create",
		Description: "Create a new dynamic workflow draft from a goal and optional structured items. {goal, slug?, defaults?, items?} → receipt with workflow id, model_policy, draft package, manifest, validation report, and launch command.",
	}, srv.handleWorkflowCreate)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "workflow.validate",
		Description: "Re-validate an existing dynamic workflow draft. {workflow_id} → receipt with the latest validation report and launch command.",
	}, srv.handleWorkflowValidate)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "workflow.launch",
		Description: "Prepare a dynamic workflow for launch. {workflow_id} → receipt with the runnable kitsoki command and cached validation state.",
	}, srv.handleWorkflowLaunch)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "workflow.status",
		Description: "Read a dynamic workflow receipt. {workflow_id} → the same receipt shape returned by create/validate/launch.",
	}, srv.handleWorkflowStatus)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "workflow.export",
		Description: "Export a validated dynamic workflow draft to a reusable story package. {workflow_id, target?, allow_base_story?} → updated receipt plus starter artifacts.",
	}, srv.handleWorkflowExport)
}

type WorkflowCreateArgs struct {
	Goal     string                            `json:"goal"`
	Slug     string                            `json:"slug,omitempty"`
	Defaults *dynamicworkflow.ManifestDefaults `json:"defaults,omitempty"`
	Items    []dynamicworkflow.CreateItem      `json:"items,omitempty"`
}

type WorkflowIDArgs struct {
	WorkflowID     string `json:"workflow_id"`
	Target         string `json:"target,omitempty"`
	AllowBaseStory bool   `json:"allow_base_story,omitempty"`
}

func (srv *Server) workflowService() *dynamicworkflow.Service {
	root := discoverRepoRoot()
	return dynamicworkflow.NewService(root)
}

func discoverRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil || cwd == "" {
		return "."
	}
	cur := cwd
	for {
		if _, err := os.Stat(filepath.Join(cur, dynamicworkflow.DefaultTemplateStoryDir, "app.yaml")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cwd
		}
		cur = parent
	}
}

func (srv *Server) handleWorkflowCreate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkflowCreateArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Goal) == "" {
		return buildToolError(ErrBadRequest, "workflow.create: goal is required"), nil, nil
	}
	svc := srv.workflowService()
	if stale := workflowFreshnessError(svc.RootDir, "workflow.create"); stale != "" {
		return buildToolError(ErrBadRequest, stale), nil, nil
	}
	receipt, err := svc.Create(ctx, dynamicworkflow.CreateRequest{
		Goal:     args.Goal,
		Slug:     args.Slug,
		Defaults: args.Defaults,
		Items:    args.Items,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, receipt, nil
}

func (srv *Server) handleWorkflowValidate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkflowIDArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return buildToolError(ErrBadRequest, "workflow.validate: workflow_id is required"), nil, nil
	}
	svc := srv.workflowService()
	receipt, err := svc.ReadReceipt(args.WorkflowID)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	receipt.Validation = svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
	if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if err := appendWorkflowValidationEvent(receipt); err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, receipt, nil
}

func (srv *Server) handleWorkflowLaunch(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkflowIDArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return buildToolError(ErrBadRequest, "workflow.launch: workflow_id is required"), nil, nil
	}
	svc := srv.workflowService()
	if stale := workflowFreshnessError(svc.RootDir, "workflow.launch"); stale != "" {
		return buildToolError(ErrBadRequest, stale), nil, nil
	}
	receipt, err := svc.Launch(ctx, args.WorkflowID)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if srv.sess != nil {
		initialWorld, werr := dynamicworkflow.LaunchInitialWorld(svc.RootDir, receipt.LaunchBasisPath)
		if werr != nil {
			return buildToolError(ErrBadRequest, "workflow.launch: "+werr.Error()), nil, nil
		}
		handle, herr := srv.sess.OpenDrivingSession(ctx, OpenDrivingSessionParams{
			Mode:           HarnessReplay,
			StoryPath:      filepath.Join(receipt.AppPath, "app.yaml"),
			TracePath:      receipt.TracePath,
			InitialWorld:   initialWorld,
			ImportResolver: srv.importResolver,
		})
		if herr != nil {
			return buildToolError(ErrBadRequest, herr.Error()), nil, nil
		}
		receipt.SessionID = string(handle.SID)
		receipt.SessionHandle = handle.Key
		receipt.URL = "/s/" + string(handle.SID)
	}
	if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if err := dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
		"kind":            "dynamic.workflow.launched",
		"workflow_id":     receipt.WorkflowID,
		"at":              time.Now().UTC(),
		"app_path":        receipt.AppPath,
		"manifest_path":   receipt.ManifestPath,
		"trace_path":      receipt.TracePath,
		"session_id":      receipt.SessionID,
		"session_handle":  receipt.SessionHandle,
		"url":             receipt.URL,
		"receipt_path":    filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"),
		"receipt_hash":    dynamicworkflow.HashFile(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json")),
		"validation_path": receipt.ValidationPath,
		"validation_hash": dynamicworkflow.HashFile(receipt.ValidationPath),
	}); err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if receipt.URL != "" {
		if err := dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
			"kind":        "dynamic.workflow.url_assigned",
			"workflow_id": receipt.WorkflowID,
			"at":          time.Now().UTC(),
			"url":         receipt.URL,
			"server_id":   receipt.SessionID,
		}); err != nil {
			return buildToolError(ErrBadRequest, err.Error()), nil, nil
		}
	}
	return nil, receipt, nil
}

func (srv *Server) handleWorkflowStatus(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkflowIDArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return buildToolError(ErrBadRequest, "workflow.status: workflow_id is required"), nil, nil
	}
	receipt, err := srv.workflowService().ReadReceipt(args.WorkflowID)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, receipt, nil
}

func (srv *Server) handleWorkflowExport(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args WorkflowIDArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.WorkflowID) == "" {
		return buildToolError(ErrBadRequest, "workflow.export: workflow_id is required"), nil, nil
	}
	receipt, err := srv.workflowService().Export(ctx, args.WorkflowID, dynamicworkflow.ExportRequest{
		TargetDir:      args.Target,
		AllowBaseStory: args.AllowBaseStory,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, receipt, nil
}

func workflowFreshnessError(root, toolName string) string {
	ping := buildPingOK()
	head := pingGitOutput(root, "rev-parse", "HEAD")
	if head == "" || ping.Revision == "" || head == ping.Revision {
		return ""
	}
	return toolName + ": attached Studio MCP server is stale for this checkout: server revision " + ping.Revision + ", checkout HEAD " + head + ". Restart or reconnect the Kitsoki Studio MCP server for " + root + ", then retry."
}

func appendWorkflowValidationEvent(receipt *dynamicworkflow.Receipt) error {
	if receipt == nil || receipt.EventsPath == "" {
		return nil
	}
	return dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
		"kind":            "dynamic.workflow.validated",
		"workflow_id":     receipt.WorkflowID,
		"at":              time.Now().UTC(),
		"app_path":        receipt.AppPath,
		"manifest_path":   receipt.ManifestPath,
		"validation_path": receipt.ValidationPath,
		"validation_hash": dynamicworkflow.HashFile(receipt.ValidationPath),
		"ok":              receipt.Validation.OK,
		"errors":          receipt.Validation.Errors,
		"warnings":        receipt.Validation.Warnings,
	})
}
