package tui

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/dynamicworkflow"
	"kitsoki/internal/tui/blocks"
)

// WorkflowCommand implements `/workflow` — the thin TUI surface over the
// shared dynamic-workflow service. It keeps the TUI honest: create / validate /
// status / export are real service calls, while /run surfaces the launch
// command that the operator can execute from the shell when they want the live
// session URL.
func handleWorkflowSlash(m RootModel, args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.transcript.AppendBlock(workflowUsageBlock(m))
		return m, nil
	}

	root := discoverWorkflowRepoRoot()
	if root == "" {
		m.transcript.AppendBlock(workflowBlock(m, "(workflow: could not resolve repo root)"))
		return m, nil
	}
	svc := workflowService(root)

	switch strings.ToLower(args[0]) {
	case "create":
		goal, slug, err := parseWorkflowCreateArgs(args[1:])
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, err.Error()))
			return m, nil
		}
		receipt, err := svc.Create(context.Background(), dynamicworkflow.CreateRequest{Goal: goal, Slug: slug})
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("create failed: %v", err)))
			return m, nil
		}
		m.transcript.AppendBlock(renderWorkflowReceiptBlock(m, receipt))
		return m, nil

	case "validate":
		if len(args) < 2 {
			m.transcript.AppendBlock(workflowBlock(m, "usage: /workflow validate <workflow-id>"))
			return m, nil
		}
		receipt, err := svc.ReadReceipt(args[1])
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("validate failed: %v", err)))
			return m, nil
		}
		receipt.Validation = svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
		if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("validate failed: %v", err)))
			return m, nil
		}
		if err := dynamicworkflow.AppendWorkflowEvent(receipt.EventsPath, map[string]any{
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
		}); err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("validate failed: %v", err)))
			return m, nil
		}
		m.transcript.AppendBlock(renderWorkflowReceiptBlock(m, receipt))
		return m, nil

	case "status":
		if len(args) < 2 {
			m.transcript.AppendBlock(workflowBlock(m, "usage: /workflow status <workflow-id>"))
			return m, nil
		}
		receipt, err := svc.ReadReceipt(args[1])
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("status failed: %v", err)))
			return m, nil
		}
		m.transcript.AppendBlock(renderWorkflowReceiptBlock(m, receipt))
		return m, nil

	case "run", "launch":
		if len(args) < 2 {
			m.transcript.AppendBlock(workflowBlock(m, "usage: /workflow run <workflow-id>"))
			return m, nil
		}
		receipt, err := svc.ReadReceipt(args[1])
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("run failed: %v", err)))
			return m, nil
		}
		if !receipt.Validation.OK {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("workflow %s is not validated: %s", receipt.WorkflowID, strings.Join(receipt.Validation.Errors, "; "))))
			return m, nil
		}
		m.transcript.AppendBlock(renderWorkflowReceiptBlock(m, receipt))
		if receipt.LaunchCommand != "" {
			m.transcript.AppendBlock(workflowBlock(m, "launch: "+receipt.LaunchCommand))
		}
		return m, nil

	case "export":
		workflowID, opts, err := parseWorkflowExportArgs(args[1:])
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, err.Error()))
			return m, nil
		}
		receipt, err := svc.Export(context.Background(), workflowID, dynamicworkflow.ExportRequest{
			TargetDir:      opts.target,
			AllowBaseStory: opts.allowBaseStory,
		})
		if err != nil {
			m.transcript.AppendBlock(workflowBlock(m, fmt.Sprintf("export failed: %v", err)))
			return m, nil
		}
		m.transcript.AppendBlock(renderWorkflowReceiptBlock(m, receipt))
		if receipt.ExportReportPath != "" {
			m.transcript.AppendBlock(workflowBlock(m, "export report: "+receipt.ExportReportPath))
		}
		return m, nil

	default:
		m.transcript.AppendBlock(workflowBlock(m, "usage: /workflow [create|validate|status|run|export]"))
		return m, nil
	}
}

type workflowExportArgs struct {
	target         string
	allowBaseStory bool
}

func parseWorkflowCreateArgs(args []string) (goal, slug string, err error) {
	fs := flag.NewFlagSet("workflow create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&slug, "slug", "", "workflow slug")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	goal = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if goal == "" {
		return "", "", fmt.Errorf("usage: /workflow create [--slug <slug>] <goal>")
	}
	return goal, slug, nil
}

func parseWorkflowExportArgs(args []string) (workflowID string, opts workflowExportArgs, err error) {
	fs := flag.NewFlagSet("workflow export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.target, "target", "", "export target")
	fs.BoolVar(&opts.allowBaseStory, "allow-base-story", false, "allow export to internal/basestories")
	if len(args) == 0 {
		return "", workflowExportArgs{}, fmt.Errorf("usage: /workflow export <workflow-id> [--target <dir>] [--allow-base-story]")
	}
	workflowID = args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return "", workflowExportArgs{}, err
	}
	return workflowID, opts, nil
}

func discoverWorkflowRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return ""
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

func workflowService(root string) *dynamicworkflow.Service {
	svc := dynamicworkflow.NewService(root)
	svc.OutputDir = filepath.Join(root, dynamicworkflow.DefaultOutputDir)
	svc.TemplateStoryDir = filepath.Join(root, dynamicworkflow.DefaultTemplateStoryDir)
	return svc
}

func workflowUsageBlock(m RootModel) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	var sb strings.Builder
	sb.WriteString(r.SlashOutput("workflow"))
	sb.WriteString("\n")
	sb.WriteString(workflowBlock(m, "create: /workflow create [--slug <slug>] <goal>"))
	sb.WriteString("\n")
	sb.WriteString(workflowBlock(m, "validate: /workflow validate <workflow-id>"))
	sb.WriteString("\n")
	sb.WriteString(workflowBlock(m, "status: /workflow status <workflow-id>"))
	sb.WriteString("\n")
	sb.WriteString(workflowBlock(m, "run: /workflow run <workflow-id>"))
	sb.WriteString("\n")
	sb.WriteString(workflowBlock(m, "export: /workflow export <workflow-id> [--target <dir>] [--allow-base-story]"))
	return sb.String()
}

func workflowBlock(m RootModel, text string) string {
	return blocks.New(m.transcript.width, m.currentTheme()).SlashOutput(text)
}

func renderWorkflowReceiptBlock(m RootModel, receipt *dynamicworkflow.Receipt) string {
	if receipt == nil {
		return workflowBlock(m, "(workflow: empty receipt)")
	}
	var sb strings.Builder
	sb.WriteString(workflowBlock(m, "workflow "+receipt.WorkflowID))
	sb.WriteString("\n")
	sb.WriteString("goal: ")
	sb.WriteString(receipt.Goal)
	sb.WriteString("\n")
	sb.WriteString("draft: ")
	sb.WriteString(receipt.DraftDir)
	sb.WriteString("\n")
	sb.WriteString("manifest: ")
	sb.WriteString(receipt.ManifestPath)
	sb.WriteString("\n")
	sb.WriteString("story: ")
	sb.WriteString(receipt.AppPath)
	sb.WriteString("\n")
	if receipt.EventsPath != "" {
		sb.WriteString("events: ")
		sb.WriteString(receipt.EventsPath)
		sb.WriteString("\n")
	}
	if receipt.Validation.OK {
		sb.WriteString("validation: ok")
	} else {
		sb.WriteString(fmt.Sprintf("validation: %d error(s)", len(receipt.Validation.Errors)))
	}
	sb.WriteString("\n")
	if receipt.LaunchCommand != "" {
		sb.WriteString("launch: ")
		sb.WriteString(receipt.LaunchCommand)
		sb.WriteString("\n")
	}
	if receipt.SessionID != "" {
		sb.WriteString("session: ")
		sb.WriteString(receipt.SessionID)
		sb.WriteString("\n")
	}
	if receipt.URL != "" {
		sb.WriteString("url: ")
		sb.WriteString(receipt.URL)
		sb.WriteString("\n")
	}
	if receipt.ExportPath != "" {
		sb.WriteString("export: ")
		sb.WriteString(receipt.ExportPath)
		sb.WriteString("\n")
	}
	if receipt.ExportReportPath != "" {
		sb.WriteString("export report: ")
		sb.WriteString(receipt.ExportReportPath)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
