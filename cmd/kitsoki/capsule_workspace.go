package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/control"
)

// capsuleWorkspaceCmd is the operator/automation CLI counterpart to the
// handle-scoped MCP lifecycle. It retains current script compatibility while
// letting any onboarded project use the native manager directly.
func capsuleWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Create and manage native Capsule workspaces"}
	cmd.AddCommand(capsuleWorkspaceCreateCmd(), capsuleWorkspaceListCmd(), capsuleWorkspaceStatusCmd(), capsuleWorkspaceExecCmd(), capsuleWorkspaceCommitCmd(), capsuleWorkspaceIntegrateCmd(), capsuleWorkspaceCloseCmd())
	return cmd
}

func capsuleWorkspaceExecCmd() *cobra.Command {
	var project, id, owner, commandID string
	var generation uint64
	var timeout time.Duration
	var jsonOut bool
	cmd := &cobra.Command{Use: "exec", Short: "Run one definition-declared command in a managed Capsule workspace", RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		result, err := capsuleWorkspaceExecute(cmd.Context(), manager, control.Handle{ID: id, Generation: generation}, owner, commandID, timeout)
		if err != nil {
			return err
		}
		view := capsuleWorkspaceExecutionResult{
			OK:              result.ExitCode == 0,
			ID:              id,
			Generation:      result.Handle.Generation,
			Owner:           owner,
			CommandID:       commandID,
			ExitCode:        result.ExitCode,
			Output:          result.Output,
			OutputTruncated: result.OutputTruncated,
			TimedOut:        result.TimedOut,
		}
		if err := capsuleWorkspaceWrite(cmd, view, jsonOut); err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("capsule workspace exec: command %q exited %d", commandID, result.ExitCode)
		}
		return nil
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().Uint64Var(&generation, "generation", 0, "exact workspace handle generation")
	cmd.Flags().StringVar(&owner, "owner", "", "lease owner")
	cmd.Flags().StringVar(&commandID, "command", "", "definition-declared command id")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "optional command timeout override")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("generation")
	_ = cmd.MarkFlagRequired("owner")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}
func capsuleWorkspaceManager(project string) (*control.Manager, error) {
	m, _, err := newCapsuleManager(project, "local", []string{"*"})
	return m, err
}
func capsuleWorkspaceCreateCmd() *cobra.Command {
	var project, id, definition, owner string
	var jsonOut bool
	cmd := &cobra.Command{Use: "create", Short: "Create or reacquire a managed Capsule workspace", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		h, err := m.Create(cmd.Context(), control.CreateRequest{ID: id, DefinitionID: definition, Owner: owner})
		if err != nil {
			return err
		}
		view, err := capsuleWorkspaceView(cmd.Context(), m, h)
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, view, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().StringVar(&definition, "definition", "", "capsule definition id")
	cmd.Flags().StringVar(&owner, "owner", "cli", "lease owner")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("definition")
	return cmd
}
func capsuleWorkspaceListCmd() *cobra.Command {
	var project string
	var jsonOut bool
	cmd := &cobra.Command{Use: "list", Short: "List managed Capsule workspaces", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		all, err := m.List(cmd.Context())
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, map[string]any{"workspaces": all}, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	return cmd
}
func capsuleWorkspaceStatusCmd() *cobra.Command {
	var project, id string
	var jsonOut bool
	cmd := &cobra.Command{Use: "status", Short: "Show a managed Capsule workspace", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		in, err := m.Instances.Get(cmd.Context(), id)
		if err != nil {
			return err
		}
		view, err := capsuleWorkspaceView(cmd.Context(), m, control.Handle{ID: in.ID, Generation: in.Generation})
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, view, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
func capsuleWorkspaceCommitCmd() *cobra.Command {
	var project, id, message string
	var jsonOut bool
	cmd := &cobra.Command{Use: "commit", Short: "Commit all local workspace changes", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		in, err := m.Instances.Get(cmd.Context(), id)
		if err != nil {
			return err
		}
		h, err := m.CommitVCS(cmd.Context(), control.Handle{ID: in.ID, Generation: in.Generation}, message)
		if err != nil {
			return err
		}
		view, err := capsuleWorkspaceView(cmd.Context(), m, h)
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, view, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().StringVar(&message, "message", "", "commit message")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("message")
	return cmd
}

func capsuleWorkspaceIntegrateCmd() *cobra.Command {
	var project, id, gate, owner string
	var teardown, jsonOut bool
	cmd := &cobra.Command{Use: "integrate", Short: "Integrate a provider-managed workspace through its declared protected lifecycle", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		in, err := m.Instances.Get(cmd.Context(), id)
		if err != nil {
			return err
		}
		result, err := capsuleWorkspaceIntegrate(cmd.Context(), m, in, gate, teardown, owner)
		if err != nil {
			return err
		}
		if result.CleanupError != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: workspace %s integrated, but teardown needs attention: %s\n", id, result.CleanupError)
		}
		return capsuleWorkspaceWrite(cmd, result, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().StringVar(&gate, "gate", "", "focused validation command declared by the development adapter")
	cmd.Flags().StringVar(&owner, "owner", "", "lease owner required when tearing down; defaults to the workspace lease owner")
	cmd.Flags().BoolVar(&teardown, "teardown", false, "close the workspace after successful integration")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
func capsuleWorkspaceCloseCmd() *cobra.Command {
	var project, id, owner string
	var generation uint64
	var jsonOut bool
	cmd := &cobra.Command{Use: "close", Short: "Close a managed Capsule workspace", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := capsuleWorkspaceManager(project)
		if err != nil {
			return err
		}
		in, err := m.Instances.Get(cmd.Context(), id)
		if err != nil {
			return err
		}
		result, err := capsuleWorkspaceClose(cmd.Context(), m, in, generation, owner)
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, result, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&id, "id", "", "workspace id")
	cmd.Flags().Uint64Var(&generation, "generation", 0, "optional exact workspace generation; zero accepts the current generation")
	cmd.Flags().StringVar(&owner, "owner", "", "lease owner; defaults to the workspace lease owner")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "print JSON")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}
func capsuleWorkspaceWrite(cmd *cobra.Command, v any, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
	}
	fmt.Fprintln(cmd.OutOrStdout(), v)
	return nil
}

type capsuleWorkspaceViewResult struct {
	ID           string        `json:"id"`
	Generation   uint64        `json:"generation"`
	DefinitionID string        `json:"definition_id"`
	Provider     string        `json:"provider"`
	Path         string        `json:"path"`
	SourceRef    string        `json:"source_ref,omitempty"`
	Head         string        `json:"head,omitempty"`
	Branch       string        `json:"branch,omitempty"`
	State        control.State `json:"state"`
	Owner        string        `json:"owner"`
}

type capsuleWorkspaceIntegrationResult struct {
	ID           string `json:"id"`
	Generation   uint64 `json:"generation"`
	Path         string `json:"path"`
	Owner        string `json:"owner"`
	Integrated   bool   `json:"integrated"`
	Closed       bool   `json:"closed"`
	CleanupError string `json:"cleanup_error,omitempty"`
}

type capsuleWorkspaceExecutionResult struct {
	OK              bool   `json:"ok"`
	ID              string `json:"id"`
	Generation      uint64 `json:"generation"`
	Owner           string `json:"owner"`
	CommandID       string `json:"command_id"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output,omitempty"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	TimedOut        bool   `json:"timed_out,omitempty"`
}

type capsuleWorkspaceCloseResult struct {
	Schema       string        `json:"schema"`
	OK           bool          `json:"ok"`
	Project      string        `json:"project"`
	ID           string        `json:"id"`
	DefinitionID string        `json:"definition_id"`
	Provider     string        `json:"provider"`
	Generation   uint64        `json:"generation"`
	Owner        string        `json:"owner"`
	State        control.State `json:"state"`
}

func capsuleWorkspaceExecute(ctx context.Context, manager *control.Manager, handle control.Handle, owner, commandID string, timeout time.Duration) (control.CommandResult, error) {
	if strings.TrimSpace(commandID) == "" {
		return control.CommandResult{}, fmt.Errorf("capsule workspace exec: command id is required")
	}
	if _, err := manager.StatusOwned(ctx, handle, owner); err != nil {
		return control.CommandResult{}, err
	}
	return manager.RunCommand(ctx, handle, commandID, nil, timeout)
}

func capsuleWorkspaceClose(ctx context.Context, manager *control.Manager, in control.Instance, expectedGeneration uint64, owner string) (capsuleWorkspaceCloseResult, error) {
	if expectedGeneration != 0 && in.Generation != expectedGeneration {
		return capsuleWorkspaceCloseResult{}, fmt.Errorf("%w: instance %q generation %d, got %d", control.ErrStale, in.ID, in.Generation, expectedGeneration)
	}
	effectiveOwner := strings.TrimSpace(owner)
	if effectiveOwner == "" {
		effectiveOwner = in.Lease.Owner
	}
	if err := manager.Close(ctx, control.Handle{ID: in.ID, Generation: in.Generation}, effectiveOwner); err != nil {
		return capsuleWorkspaceCloseResult{}, err
	}
	closed, err := manager.Instances.Get(ctx, in.ID)
	if err != nil {
		return capsuleWorkspaceCloseResult{}, fmt.Errorf("workspace closed, but final lifecycle state could not be read: %w", err)
	}
	if closed.State != control.StateClosed {
		return capsuleWorkspaceCloseResult{}, fmt.Errorf("workspace close returned state %q", closed.State)
	}
	return capsuleWorkspaceCloseResult{
		Schema:       "capsule-workspace-close/v1",
		OK:           true,
		Project:      manager.Grant.ProjectRoot,
		ID:           closed.ID,
		DefinitionID: closed.DefinitionID,
		Provider:     closed.Provider,
		Generation:   closed.Generation,
		Owner:        effectiveOwner,
		State:        closed.State,
	}, nil
}

func capsuleWorkspaceView(ctx context.Context, manager *control.Manager, handle control.Handle) (capsuleWorkspaceViewResult, error) {
	in, err := manager.Status(ctx, handle)
	if err != nil {
		return capsuleWorkspaceViewResult{}, err
	}
	path, err := manager.WorkspacePath(ctx, handle)
	if err != nil {
		return capsuleWorkspaceViewResult{}, err
	}
	relative, err := projectRelativeWorkspacePath(manager.Grant.ProjectRoot, path)
	if err != nil {
		return capsuleWorkspaceViewResult{}, err
	}
	return capsuleWorkspaceViewResult{
		ID:           in.ID,
		Generation:   in.Generation,
		DefinitionID: in.DefinitionID,
		Provider:     in.Provider,
		Path:         relative,
		SourceRef:    in.SourceRef,
		Head:         in.Head,
		Branch:       in.Branch,
		State:        in.State,
		Owner:        in.Lease.Owner,
	}, nil
}

func projectRelativeWorkspacePath(projectRoot, workspacePath string) (string, error) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("capsule workspace: path is outside project scope")
	}
	return filepath.ToSlash(relative), nil
}

func capsuleWorkspaceIntegrate(ctx context.Context, manager *control.Manager, in control.Instance, gate string, teardown bool, owner string) (capsuleWorkspaceIntegrationResult, error) {
	view, err := capsuleWorkspaceView(ctx, manager, control.Handle{ID: in.ID, Generation: in.Generation})
	if err != nil {
		return capsuleWorkspaceIntegrationResult{}, err
	}
	handle, err := manager.Integrate(ctx, control.Handle{ID: in.ID, Generation: in.Generation}, gate)
	if err != nil {
		return capsuleWorkspaceIntegrationResult{}, err
	}
	result := capsuleWorkspaceIntegrationResult{
		ID:         in.ID,
		Generation: handle.Generation,
		Path:       view.Path,
		Owner:      in.Lease.Owner,
		Integrated: true,
	}
	if !teardown {
		return result, nil
	}
	effectiveOwner := strings.TrimSpace(owner)
	if effectiveOwner == "" {
		effectiveOwner = in.Lease.Owner
	}
	result.Owner = effectiveOwner
	if err := manager.Close(ctx, handle, effectiveOwner); err != nil {
		result.CleanupError = err.Error()
		return result, nil
	}
	closed, err := manager.Instances.Get(ctx, in.ID)
	if err != nil {
		result.CleanupError = "workspace closed, but final lifecycle state could not be read: " + err.Error()
		return result, nil
	}
	result.Generation = closed.Generation
	result.Closed = true
	return result, nil
}
