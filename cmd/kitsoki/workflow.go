package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/dynamicworkflow"
)

func workflowCmd() *cobra.Command {
	var rootDir string
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Create, validate, run, inspect, and export dynamic workflow drafts",
	}
	cmd.PersistentFlags().StringVar(&rootDir, "root", "", "repo root for workflow artifacts (default: current working directory)")

	cmd.AddCommand(workflowCreateCmd(&rootDir))
	cmd.AddCommand(workflowValidateCmd(&rootDir))
	cmd.AddCommand(workflowRunCmd(&rootDir))
	cmd.AddCommand(workflowStatusCmd(&rootDir))
	cmd.AddCommand(workflowExportCmd(&rootDir))
	return cmd
}

func workflowService(rootDir string) *dynamicworkflow.Service {
	if strings.TrimSpace(rootDir) == "" {
		rootDir = "."
	}
	svc := dynamicworkflow.NewService(rootDir)
	svc.OutputDir = filepath.Join(rootDir, dynamicworkflow.DefaultOutputDir)
	svc.TemplateStoryDir = filepath.Join(rootDir, dynamicworkflow.DefaultTemplateStoryDir)
	return svc
}

func workflowCreateCmd(rootDir *string) *cobra.Command {
	var slug string
	var briefPath string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "create <goal>",
		Short: "Create a new dynamic workflow draft",
		Args: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(briefPath) != "" {
				return nil
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := workflowService(*rootDir)
			req := dynamicworkflow.CreateRequest{Goal: strings.Join(args, " "), Slug: slug}
			if strings.TrimSpace(briefPath) != "" {
				brief, err := dynamicworkflow.ReadCreateRequest(briefPath)
				if err != nil {
					return err
				}
				if strings.TrimSpace(req.Goal) != "" {
					brief.Goal = req.Goal
				}
				if strings.TrimSpace(slug) != "" {
					brief.Slug = slug
				}
				req = brief
			}
			receipt, err := svc.Create(cmd.Context(), req)
			if err != nil {
				return err
			}
			return renderWorkflowReceipt(cmd, receipt, jsonOut)
		},
	}
	cmd.Flags().StringVar(&slug, "slug", "", "workflow slug (default: derived from the goal)")
	cmd.Flags().StringVar(&briefPath, "brief", "", "YAML/JSON structured workflow brief with goal, defaults, and items")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the receipt as JSON")
	return cmd
}

func workflowValidateCmd(rootDir *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "validate <workflow-id>",
		Short: "Validate a generated dynamic workflow draft",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := workflowService(*rootDir)
			receipt, err := svc.ReadReceipt(args[0])
			if err != nil {
				return err
			}
			receipt.Validation = svc.ValidateDraft(receipt.AppPath, receipt.ManifestPath)
			if err := dynamicworkflow.WriteReceipt(filepath.Join(svc.OutputDir, receipt.WorkflowID, "receipt.json"), receipt); err != nil {
				return err
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
				return err
			}
			return renderWorkflowReceipt(cmd, receipt, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the receipt as JSON")
	return cmd
}

func workflowRunCmd(rootDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <workflow-id>",
		Short: "Run the generated workflow draft with kitsoki run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := workflowService(*rootDir)
			receipt, err := svc.ReadReceipt(args[0])
			if err != nil {
				return err
			}
			if !receipt.Validation.OK {
				return fmt.Errorf("workflow %s is not validated: %s", receipt.WorkflowID, strings.Join(receipt.Validation.Errors, "; "))
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable: %w", err)
			}
			basis := receipt.LaunchBasisPath
			if basis == "" {
				basis = filepath.Join(svc.OutputDir, receipt.WorkflowID, "launch.yaml")
			}
			child := exec.CommandContext(cmd.Context(), exe, "run", filepath.Join(receipt.AppPath, "app.yaml"), "--warp", basis)
			child.Stdout = cmd.OutOrStdout()
			child.Stderr = cmd.ErrOrStderr()
			child.Stdin = cmd.InOrStdin()
			return child.Run()
		},
	}
	return cmd
}

func workflowStatusCmd(rootDir *string) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status <workflow-id>",
		Short: "Show the workflow receipt and validation status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := workflowService(*rootDir)
			receipt, err := svc.ReadReceipt(args[0])
			if err != nil {
				return err
			}
			return renderWorkflowReceipt(cmd, receipt, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the receipt as JSON")
	return cmd
}

func workflowExportCmd(rootDir *string) *cobra.Command {
	var target string
	var allowBaseStory bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "export <workflow-id>",
		Short: "Export a validated workflow draft to a reusable story package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc := workflowService(*rootDir)
			receipt, err := svc.Export(cmd.Context(), args[0], dynamicworkflow.ExportRequest{
				TargetDir:      target,
				AllowBaseStory: allowBaseStory,
			})
			if err != nil {
				return err
			}
			return renderWorkflowReceipt(cmd, receipt, jsonOut)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "export destination (default: stories/<slug>)")
	cmd.Flags().BoolVar(&allowBaseStory, "allow-base-story", false, "allow export into internal/basestories/stories/<slug> (explicit operator approval)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the receipt as JSON")
	return cmd
}

func renderWorkflowReceipt(cmd *cobra.Command, receipt *dynamicworkflow.Receipt, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(receipt)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "workflow %s\n", receipt.WorkflowID)
	fmt.Fprintf(cmd.OutOrStdout(), "goal: %s\n", receipt.Goal)
	if receipt.ModelPolicy.Model != "" {
		tracePolicy := "trace model optional"
		if receipt.ModelPolicy.RequireTraceModel {
			tracePolicy = "trace model required"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "model policy: %s / %s / %s (%s)\n", receipt.ModelPolicy.Harness, receipt.ModelPolicy.Profile, receipt.ModelPolicy.Model, tracePolicy)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "draft: %s\n", receipt.DraftDir)
	fmt.Fprintf(cmd.OutOrStdout(), "manifest: %s\n", receipt.ManifestPath)
	fmt.Fprintf(cmd.OutOrStdout(), "story: %s\n", receipt.AppPath)
	if receipt.EventsPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "events: %s\n", receipt.EventsPath)
	}
	if receipt.Validation.OK {
		fmt.Fprintf(cmd.OutOrStdout(), "validation: ok\n")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "validation: %d error(s)\n", len(receipt.Validation.Errors))
		for _, err := range receipt.Validation.Errors {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", err)
		}
	}
	if receipt.LaunchCommand != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "launch: %s\n", receipt.LaunchCommand)
	}
	if receipt.ExportPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "export: %s\n", receipt.ExportPath)
	}
	if receipt.ExportReportPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "export report: %s\n", receipt.ExportReportPath)
	}
	if receipt.ExportReport != nil {
		if summary := dynamicworkflow.FormatStarterFlowReplay(receipt.ExportReport.StarterFlowReplay); summary != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "starter flow replay: %s\n", summary)
		}
	}
	return nil
}
