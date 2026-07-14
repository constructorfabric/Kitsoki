package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	capsuleproject "kitsoki/internal/capsule/project"
	"kitsoki/internal/capsule/storylauncher"
	"kitsoki/internal/host"
	kitsokimcp "kitsoki/internal/mcp"
)

// capsuleMCPCommand is intentionally separate from the broad Studio MCP
// surface. An agent launched with this command receives only project-scoped
// Capsule handles, never the server checkout or arbitrary host tools.
func capsuleMCPCommand() *cobra.Command {
	var project, pipeline, executor, owner, fakeReceiptSigner string
	var branches, effects []string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start a project-scoped Capsule MCP server",
		Long: `Start a least-authority MCP server for one project's managed Capsule workspaces.

The startup scope is immutable: tool calls can narrow it but cannot add a
project, workspace root, definition, executor, command, remote, or credential.
This is the coding-agent MCP surface; it is intentionally separate from the
general-purpose Studio MCP server.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			manager, projectID, err := newCapsuleMCPManager(project, owner, branches, effects)
			if err != nil {
				return err
			}
			signer, err := capsuleFakeReceiptSigner(fakeReceiptSigner)
			if err != nil {
				return err
			}
			server, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: owner, ProjectID: projectID, Pipeline: pipeline, CIExecutor: executor, CILauncher: func(path string) ci.Launcher {
				root := storylauncher.ProjectRootForStory(path)
				return storylauncher.Launcher{StoryPath: path, ProjectRoot: root, AgentLaunchPolicy: host.AgentLaunchPolicy{Enabled: true, AllowedRoots: []string{root}}}
			}, Signer: signer})
			if err != nil {
				return err
			}
			return server.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "onboarded project root")
	cmd.Flags().StringVar(&pipeline, "pipeline", "", "single declared CI pipeline available to this server (omitted disables CI plan/run)")
	cmd.Flags().StringVar(&executor, "executor", "", "expected CI executor for --pipeline (local and host are aliases; must match the declaration)")
	cmd.Flags().StringVar(&owner, "owner", "mcp-agent", "immutable lease owner recorded on created workspaces")
	cmd.Flags().StringVar(&fakeReceiptSigner, "fake-receipt-signer", "", "deterministic local/test receipt signer id for projects requiring signed receipts")
	cmd.Flags().StringSliceVar(&branches, "branch", nil, "allowed local reconciliation target branch (repeatable; omitted denies sync)")
	cmd.Flags().StringSliceVar(&effects, "effect", defaultCapsuleMCPEffects(), "allowed mutating capability (repeatable; replaces the default coding-agent profile)")
	return cmd
}

func defaultCapsuleMCPEffects() []string {
	// A useful coding agent can create a scoped workspace, edit it, run only
	// declared commands, commit locally, and invoke its fixed CI story. Sync
	// promotion and deletion are deliberate additional grants, never defaults.
	return []string{"workspace_manage", "fs_write", "exec", "vcs_commit", "ci_run"}
}

func newCapsuleMCPManager(project, owner string, branches, effects []string) (*control.Manager, string, error) {
	root, err := filepath.Abs(project)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(owner) == "" {
		return nil, "", fmt.Errorf("capsule mcp: owner is required")
	}
	manager, err := capsuleproject.OpenScoped(root, capsuleproject.ScopeOptions{Owner: owner, Effects: effects, Branches: branches})
	if err != nil {
		return nil, "", err
	}
	return manager, filepath.Base(root), nil
}

// newCapsuleManager is the trusted in-process manager used by the Capsule
// workspace CLI. The MCP front door must use newCapsuleMCPManager instead.
func newCapsuleManager(project, executor string, branches []string) (*control.Manager, string, error) {
	root, err := filepath.Abs(project)
	if err != nil {
		return nil, "", err
	}
	if executor != "" && executor != "local" && executor != "synthetic" {
		return nil, "", fmt.Errorf("capsule mcp: executor %q is not configured", executor)
	}
	manager, err := capsuleproject.Open(root, branches)
	if err != nil {
		return nil, "", err
	}
	return manager, filepath.Base(root), nil
}
