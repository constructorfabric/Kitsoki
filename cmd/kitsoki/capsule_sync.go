package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/reconcile"
	"kitsoki/internal/capsule/record"
)

func capsuleSyncCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "sync", Short: "Plan and apply stale-safe Capsule reconciliation"}
	cmd.AddCommand(capsuleSyncPlanCmd(), capsuleSyncApplyCmd(), capsuleSyncFetchCmd(), capsuleSyncConflictsCmd(), capsuleSyncIntegrationCmd(), capsuleSyncContinueCmd(), capsuleSyncAbortCmd())
	return cmd
}

func capsuleSyncPlanCmd() *cobra.Command {
	var project, workspace, target, operation, requiredGate string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Observe refs and save an immutable reconciliation plan",
		RunE: func(cmd *cobra.Command, args []string) error {
			op, err := capsuleReconcileOperation(operation)
			if err != nil {
				return err
			}
			manager, err := capsuleWorkspaceManager(project)
			if err != nil {
				return err
			}
			instance, err := manager.Instances.Get(cmd.Context(), workspace)
			if err != nil {
				return err
			}
			path, err := manager.WorkspacePath(cmd.Context(), control.Handle{ID: instance.ID, Generation: instance.Generation})
			if err != nil {
				return err
			}
			plan, err := (reconcile.Reconciler{VCS: reconcile.Git{}}).Plan(cmd.Context(), reconcile.PlanRequest{
				Workspace: path, TargetRef: target, Operation: op, Generation: instance.Generation, RequiredGate: requiredGate,
			})
			if err != nil {
				return err
			}
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			if err := (reconcile.FilePlanStore{ProjectRoot: root}).Write(reconcile.StoredPlan{WorkspaceID: workspace, Plan: plan}); err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, plan, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&workspace, "workspace", "", "managed workspace id")
	cmd.Flags().StringVar(&target, "target", "", "target local ref")
	cmd.Flags().StringVar(&operation, "operation", "integrate", "integrate|refresh|promote|publish")
	cmd.Flags().StringVar(&requiredGate, "required-gate", "", "configured CI receipt/gate requirement")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("workspace")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

func capsuleSyncApplyCmd() *cobra.Command {
	var project, digest, gateReceipt, localBareRemote string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply one saved plan only when refs and generation still match",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			stored, err := (reconcile.FilePlanStore{ProjectRoot: root}).Get(digest)
			if err != nil {
				return err
			}
			manager, err := capsuleWorkspaceManager(root)
			if err != nil {
				return err
			}
			instance, err := manager.Instances.Get(cmd.Context(), stored.WorkspaceID)
			if err != nil {
				return err
			}
			if instance.Generation != stored.Plan.Expected.Generation {
				return fmt.Errorf("capsule sync: stale workspace generation")
			}
			reconciler := reconcile.Reconciler{VCS: reconcile.Git{}, Gates: record.PromotionGate{ProjectRoot: root}}
			if localBareRemote != "" {
				reconciler.Publisher = reconcile.LocalBareRemotePublisher{Remote: localBareRemote}
			}
			result, err := reconciler.Apply(cmd.Context(), stored.Plan, gateReceipt)
			if err != nil {
				return err
			}
			handle, err := manager.MarkIntegrated(cmd.Context(), control.Handle{ID: instance.ID, Generation: instance.Generation})
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"result": result, "workspace": handle}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&digest, "plan", "", "plan digest")
	cmd.Flags().StringVar(&gateReceipt, "gate-receipt", "", "required CI receipt id")
	cmd.Flags().StringVar(&localBareRemote, "local-bare-remote", "", "credential-free local bare Git remote for publish plans")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("plan")
	return cmd
}

func capsuleSyncFetchCmd() *cobra.Command {
	var workspace, remotePath, remoteName, branch string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch one branch from a credential-free local bare remote",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(workspace)
			if err != nil {
				return err
			}
			result, err := (reconcile.LocalBareRemoteFetcher{Remote: remotePath, RemoteName: remoteName}).Fetch(cmd.Context(), root, branch)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"ok": true, "result": result}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", ".", "workspace path")
	cmd.Flags().StringVar(&remotePath, "local-bare-remote", "", "credential-free local bare Git remote")
	cmd.Flags().StringVar(&remoteName, "remote", "origin", "remote tracking namespace")
	cmd.Flags().StringVar(&branch, "branch", "main", "branch to fetch")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("local-bare-remote")
	return cmd
}

func capsuleSyncConflictsCmd() *cobra.Command {
	var project, digest string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "conflicts",
		Short: "Materialize a structured conflict artifact for a diverged reconciliation plan",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			stored, err := (reconcile.FilePlanStore{ProjectRoot: root}).Get(digest)
			if err != nil {
				return err
			}
			artifact, path, err := (reconcile.Reconciler{VCS: reconcile.Git{}}).MaterializeConflictArtifact(cmd.Context(), stored.Plan, root)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"artifact": artifact, "path": filepath.ToSlash(rel)}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&digest, "plan", "", "plan digest")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("plan")
	return cmd
}

func capsuleSyncIntegrationCmd() *cobra.Command {
	var project, digest string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "integration",
		Short: "Materialize a managed integration instance for a diverged reconciliation plan",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			stored, err := (reconcile.FilePlanStore{ProjectRoot: root}).Get(digest)
			if err != nil {
				return err
			}
			instance, path, err := (reconcile.Reconciler{VCS: reconcile.Git{}}).MaterializeIntegrationInstance(cmd.Context(), stored.Plan, root)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"instance": instance, "path": filepath.ToSlash(rel)}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&digest, "plan", "", "plan digest")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("plan")
	return cmd
}

func capsuleSyncContinueCmd() *cobra.Command {
	var project, digest, resolverDecision, lostWorkReview, validationReceipt string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "continue",
		Short: "Apply a resolved sync continuation from a managed integration instance",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			stored, err := (reconcile.FilePlanStore{ProjectRoot: root}).Get(digest)
			if err != nil {
				return err
			}
			result, err := (reconcile.Reconciler{VCS: reconcile.Git{}, Gates: record.PromotionGate{ProjectRoot: root}}).ApplyContinuation(cmd.Context(), reconcile.ContinuationApplyRequest{Plan: stored.Plan, ProjectRoot: root, ResolverDecision: resolverDecision, LostWorkReview: lostWorkReview, ValidationReceipt: validationReceipt})
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"ok": true, "result": result}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&digest, "plan", "", "plan digest")
	cmd.Flags().StringVar(&resolverDecision, "resolver-decision", "", "resolver decision artifact or receipt id")
	cmd.Flags().StringVar(&lostWorkReview, "lost-work-review", "", "independent lost-work review artifact or receipt id")
	cmd.Flags().StringVar(&validationReceipt, "validation-receipt", "", "validation receipt id")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("plan")
	_ = cmd.MarkFlagRequired("resolver-decision")
	_ = cmd.MarkFlagRequired("lost-work-review")
	_ = cmd.MarkFlagRequired("validation-receipt")
	return cmd
}

func capsuleSyncAbortCmd() *cobra.Command {
	var project, digest string
	var preserve, jsonOut bool
	cmd := &cobra.Command{
		Use:   "abort",
		Short: "Abort a managed sync continuation and optionally preserve a patch artifact",
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := filepath.Abs(project)
			if err != nil {
				return err
			}
			stored, err := (reconcile.FilePlanStore{ProjectRoot: root}).Get(digest)
			if err != nil {
				return err
			}
			result, err := (reconcile.Reconciler{VCS: reconcile.Git{}}).AbortContinuation(cmd.Context(), reconcile.AbortContinuationRequest{Plan: stored.Plan, ProjectRoot: root, Preserve: preserve})
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, map[string]any{"ok": true, "result": result}, jsonOut)
		},
	}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&digest, "plan", "", "plan digest")
	cmd.Flags().BoolVar(&preserve, "preserve", true, "write an abort patch artifact before removing the integration instance")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("plan")
	return cmd
}

func capsuleReconcileOperation(value string) (reconcile.Operation, error) {
	op := reconcile.Operation(value)
	if reconcile.ValidOperation(op) {
		return op, nil
	}
	return "", fmt.Errorf("capsule sync: unsupported operation %q", value)
}
