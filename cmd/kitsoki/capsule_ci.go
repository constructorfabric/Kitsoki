package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/hygiene"
	"kitsoki/internal/capsule/record"
	"kitsoki/internal/capsule/storydigest"
	"kitsoki/internal/capsule/storylauncher"
	"kitsoki/internal/host"
)

func capsuleCICmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ci", Short: "Plan, run, and inspect story-native Capsule CI"}
	cmd.AddCommand(capsuleCIPlanCmd(), capsuleCIRunCmd(), capsuleCIDoctorCmd(), capsuleCIStatusCmd(), capsuleCIDiagnoseCmd(), capsuleCISummaryCmd(), capsuleCICancelCmd(), capsuleCIGitHubCmd())
	return cmd
}
func ciInputs(ctx context.Context, project, workspace, pipeline string, trigger ci.Trigger) (*control.Manager, control.Instance, ci.Pipeline, executor.Envelope, string, error) {
	root, err := filepath.Abs(project)
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	project = root
	m, err := capsuleWorkspaceManager(project)
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	in, err := m.Instances.Get(ctx, workspace)
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	workspacePath, err := m.WorkspacePath(ctx, control.Handle{ID: in.ID, Generation: in.Generation})
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	// Every sealed input is resolved from the exact committed workspace that
	// SourceDigest names. Reading CI/story/environment files from the primary
	// checkout would let local drift produce an envelope the worker cannot
	// reproduce.
	cfg, err := ci.Load(workspacePath)
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	p, ok := cfg.Pipelines[pipeline]
	if !ok {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", fmt.Errorf("capsule ci: pipeline %q not found", pipeline)
	}
	story, err := storydigest.Compute(workspacePath, p.Story)
	if err != nil {
		return nil, control.Instance{}, ci.Pipeline{}, executor.Envelope{}, "", err
	}
	service := ci.Service{ProjectRoot: workspacePath, Env: environment.Resolver{ProjectRoot: workspacePath, Probe: environment.HostProbe()}}
	_, envelope, err := service.Plan(ctx, ci.RunRequest{Pipeline: pipeline, Workspace: control.Handle{ID: in.ID, Generation: in.Generation}, DefinitionDigest: in.DefinitionDigest, SourceDigest: in.Head, StoryDigest: story.Digest, Trigger: trigger})
	return m, in, p, envelope, workspacePath, err
}

func capsuleCIReadTrigger(cmd *cobra.Command, triggerPath, pipeline string) (ci.Trigger, error) {
	if triggerPath == "" {
		return ci.Trigger{Kind: "local", RequestedPipeline: pipeline}, nil
	}
	raw, err := readPayloadFile(cmd, triggerPath)
	if err != nil {
		return ci.Trigger{}, fmt.Errorf("capsule ci: read trigger: %w", err)
	}
	var trigger ci.Trigger
	if err := json.Unmarshal(raw, &trigger); err != nil {
		return ci.Trigger{}, fmt.Errorf("capsule ci: parse trigger: %w", err)
	}
	if trigger.Kind == "" {
		return ci.Trigger{}, fmt.Errorf("capsule ci: trigger kind is required")
	}
	if trigger.RequestedPipeline == "" {
		trigger.RequestedPipeline = pipeline
	}
	if trigger.RequestedPipeline != pipeline {
		return ci.Trigger{}, fmt.Errorf("capsule ci: trigger requested pipeline %q, command selected %q", trigger.RequestedPipeline, pipeline)
	}
	return trigger, nil
}

func capsuleCIPlanCmd() *cobra.Command {
	var project, workspace, triggerPath string
	var jsonOut bool
	cmd := &cobra.Command{Use: "plan <pipeline>", Args: cobra.ExactArgs(1), Short: "Build a sealed story-native CI envelope", RunE: func(cmd *cobra.Command, args []string) error {
		trigger, err := capsuleCIReadTrigger(cmd, triggerPath, args[0])
		if err != nil {
			return err
		}
		_, _, _, env, _, err := ciInputs(cmd.Context(), project, workspace, args[0], trigger)
		if err != nil {
			return err
		}
		return capsuleWorkspaceWrite(cmd, env, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&workspace, "workspace", "", "managed workspace id")
	cmd.Flags().StringVar(&triggerPath, "trigger", "", "normalized capsule CI trigger JSON path, or - for stdin (default: local trigger)")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}
func capsuleCIRunCmd() *cobra.Command {
	var project, workspace, verdictPath, fakeReceiptSigner, triggerPath string
	var jsonOut bool
	cmd := &cobra.Command{Use: "run <pipeline>", Args: cobra.ExactArgs(1), Short: "Run declared Capsule CI with a story-produced typed verdict", RunE: func(cmd *cobra.Command, args []string) error {
		trigger, err := capsuleCIReadTrigger(cmd, triggerPath, args[0])
		if err != nil {
			return err
		}
		m, in, p, planned, workspacePath, err := ciInputs(cmd.Context(), project, workspace, args[0], trigger)
		if err != nil {
			return err
		}
		var launcher ci.Launcher
		if verdictPath != "" {
			raw, err := os.ReadFile(verdictPath)
			if err != nil {
				return fmt.Errorf("capsule ci: read verdict: %w", err)
			}
			var verdict ci.Verdict
			if err := json.Unmarshal(raw, &verdict); err != nil {
				return fmt.Errorf("capsule ci: parse verdict: %w", err)
			}
			launcher = ciLauncher{verdict: verdict}
		} else {
			launcher = storylauncher.Launcher{StoryPath: filepath.Join(workspacePath, p.Story), ProjectRoot: workspacePath, AgentLaunchPolicy: host.AgentLaunchPolicy{Enabled: true, AllowedRoots: []string{workspacePath}}}
		}
		cfg, err := ci.Load(workspacePath)
		if err != nil {
			return err
		}
		executors := ci.NewConfiguredExecutors(cfg)
		executors.ProjectRoot = workspacePath
		executors.Source = executor.SourceBundlerFunc(func(ctx context.Context, envelope executor.Envelope) (executor.SourceBundle, error) {
			return executor.GitBundle(ctx, workspacePath, envelope.SourceDigest, 0)
		})
		service := ci.Service{ProjectRoot: workspacePath, Jobs: artifactjob.NewMemoryStore(), Env: environment.Resolver{ProjectRoot: workspacePath, Probe: environment.HostProbe()}, Executors: executors, Launcher: launcher, Hygiene: capsuleCIHygienePlanner(project), Observer: record.FileRunObserver{ProjectRoot: project}}
		result, err := service.Run(cmd.Context(), ci.RunRequest{Pipeline: args[0], Workspace: control.Handle{ID: in.ID, Generation: in.Generation}, DefinitionDigest: in.DefinitionDigest, SourceDigest: in.Head, StoryDigest: planned.StoryDigest, Trigger: trigger})
		if err != nil {
			return persistCapsuleCIRunFailure(project, result, err)
		}
		signer, err := capsuleFakeReceiptSigner(fakeReceiptSigner)
		if err != nil {
			return err
		}
		stored, err := record.PersistWithOptions(project, result, record.PersistOptions{Signer: signer})
		if err != nil {
			return err
		}
		if err := (ci.FileRunStore{ProjectRoot: project}).Write(ci.RunRecord{JobID: string(result.Job.ID), Result: result, ReceiptID: stored.Receipt.ReceiptID, ReceiptVerification: stored.Verification.Status}); err != nil {
			return err
		}
		_ = m
		return capsuleWorkspaceWrite(cmd, result, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&workspace, "workspace", "", "managed workspace id")
	cmd.Flags().StringVar(&triggerPath, "trigger", "", "normalized capsule CI trigger JSON path, or - for stdin (default: local trigger)")
	cmd.Flags().StringVar(&verdictPath, "verdict", "", "optional externally produced capsule-ci-verdict/v1 JSON; omit to drive the declared story engine")
	cmd.Flags().StringVar(&fakeReceiptSigner, "fake-receipt-signer", "", "deterministic local/test receipt signer id for projects requiring signed receipts")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func persistCapsuleCIRunFailure(project string, result ci.RunResult, runErr error) error {
	if result.Job.ID == "" {
		return runErr
	}
	tracePath, traceErr := record.PersistTrace(project, result)
	writeErr := (ci.FileRunStore{ProjectRoot: project}).Write(ci.RunRecord{JobID: string(result.Job.ID), Result: result, DiagnosticError: runErr.Error()})
	if traceErr != nil && writeErr != nil {
		return fmt.Errorf("%w (also failed to persist capsule ci diagnostic trace: %v; run record: %v)", runErr, traceErr, writeErr)
	}
	if traceErr != nil {
		return fmt.Errorf("%w (also failed to persist capsule ci diagnostic trace: %v)", runErr, traceErr)
	}
	if writeErr != nil {
		return fmt.Errorf("%w (diagnostic trace: %s; also failed to persist run record: %v)", runErr, tracePath, writeErr)
	}
	return fmt.Errorf("%w (diagnostic trace: %s)", runErr, tracePath)
}

func capsuleCIHygienePlanner(project string) ci.HygienePlanner {
	return ci.HygienePlannerFunc(func(ctx context.Context, policy ci.CleanupPolicy) (ci.HygieneReport, error) {
		plan, err := hygiene.BuildPlan(ctx, hygiene.Options{ProjectRoot: project, KeepRuns: policy.KeepRuns, MinFreeBytes: 10 << 30, MeasureWorkspaceBytes: policy.MaxReclaimableBytes > 0, IncludeCapsuleCache: policy.IncludeCapsuleCache, IncludeGoBuildCache: policy.IncludeGoBuildCache})
		if err != nil {
			return ci.HygieneReport{}, err
		}
		return ci.HygieneReport{Schema: plan.Schema, Candidates: len(plan.Candidates), TotalBytes: plan.TotalBytes, DiskKnown: plan.Disk.Known, DiskCapacityBytes: plan.Disk.CapacityBytes, DiskFreeBytes: plan.Disk.FreeBytes, DiskMinimumBytes: plan.Disk.MinFreeBytes, DiskBelowMinimum: plan.Disk.BelowMinimum}, nil
	})
}

func capsuleCIDoctorCmd() *cobra.Command {
	var project, workspace string
	var jsonOut bool
	cmd := &cobra.Command{Use: "doctor <pipeline>", Args: cobra.ExactArgs(1), Short: "Run a no-spend Capsule CI readiness preflight", RunE: func(cmd *cobra.Command, args []string) error {
		root, err := filepath.Abs(project)
		if err != nil {
			return err
		}
		manager, err := capsuleWorkspaceManager(root)
		if err != nil {
			return err
		}
		instance, err := manager.Instances.Get(cmd.Context(), workspace)
		if err != nil {
			return err
		}
		workspacePath, err := manager.WorkspacePath(cmd.Context(), control.Handle{ID: instance.ID, Generation: instance.Generation})
		if err != nil {
			return err
		}
		cfg, err := ci.Load(workspacePath)
		if err != nil {
			// Doctor owns config failures as typed checks, but the configured
			// selector still needs a harmless empty catalog for that path.
			cfg = ci.Config{}
		}
		executors := ci.NewConfiguredExecutors(cfg)
		executors.ProjectRoot = workspacePath
		doctor := ci.Doctor{ProjectRoot: workspacePath, Env: environment.Resolver{ProjectRoot: workspacePath, Probe: environment.HostProbe()}, Executors: executors, Hygiene: capsuleCIHygienePlanner(root), Workspace: ci.GitWorkspaceProbe{}}
		report, err := doctor.Check(cmd.Context(), ci.DoctorRequest{Pipeline: args[0], Workspace: instance, WorkspacePath: workspacePath})
		if err != nil {
			return err
		}
		if jsonOut {
			err = capsuleWorkspaceWrite(cmd, report, true)
		} else {
			err = writeCapsuleCIDoctor(cmd, report)
		}
		if err != nil {
			return err
		}
		if !report.Ready {
			return ci.ErrDoctorNotReady
		}
		return nil
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&workspace, "workspace", "", "managed workspace id")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func writeCapsuleCIDoctor(cmd *cobra.Command, report ci.DoctorReport) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "ready: %t\n", report.Ready)
	fmt.Fprintf(out, "pipeline: %s\n", report.Pipeline)
	fmt.Fprintf(out, "workspace: %s\n", report.Workspace)
	for _, check := range report.Checks {
		fmt.Fprintf(out, "[%s] %s: %s\n", check.Outcome, check.ID, check.Summary)
		for _, remedy := range check.Remedies {
			fmt.Fprintf(out, "  remedy: %s\n", remedy)
		}
	}
	return nil
}
func capsuleCIStatusCmd() *cobra.Command {
	var project, job string
	var jsonOut, refresh bool
	cmd := &cobra.Command{Use: "status", Short: "Read persisted Capsule CI run records", RunE: func(cmd *cobra.Command, args []string) error {
		store := ci.FileRunStore{ProjectRoot: project}
		if job != "" {
			record, err := store.Get(job)
			if err != nil {
				return err
			}
			if refresh {
				controller, err := capsuleCIExecutionController(cmd.Context(), project, record)
				if err != nil {
					return err
				}
				status, err := controller.Status(cmd.Context(), record.Result.Execution.ExecutionID)
				if err != nil {
					return err
				}
				record, err = store.RecordExecutorStatus(job, status)
				if err != nil {
					return err
				}
			}
			return capsuleWorkspaceWrite(cmd, record, jsonOut)
		}
		if refresh {
			return fmt.Errorf("capsule ci status: --refresh requires --job")
		}
		all, err := store.List()
		if err != nil {
			return err
		}
		index, err := store.Index()
		if err != nil {
			return err
		}
		if jsonOut {
			return capsuleWorkspaceWrite(cmd, index, jsonOut)
		}
		return capsuleWorkspaceWrite(cmd, map[string]any{"runs": all}, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&job, "job", "", "job id")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "query the durable executor and persist its latest status (requires --job)")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}
func capsuleCISummaryCmd() *cobra.Command {
	var project string
	var limit int
	var jsonOut bool
	cmd := &cobra.Command{Use: "summary", Short: "Project a provider-safe Capsule CI status summary", RunE: func(cmd *cobra.Command, args []string) error {
		summary, err := (ci.FileRunStore{ProjectRoot: project}).ProviderSummary(limit)
		if err != nil {
			return err
		}
		if jsonOut {
			return capsuleWorkspaceWrite(cmd, summary, true)
		}
		_, err = cmd.OutOrStdout().Write([]byte(summary.Markdown))
		return err
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().IntVar(&limit, "limit", 5, "number of latest runs to include")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}

func capsuleCIDiagnoseCmd() *cobra.Command {
	var project, job string
	var latest bool
	var stallAfter time.Duration
	var jsonOut bool
	cmd := &cobra.Command{Use: "diagnose", Short: "Summarize one persisted Capsule CI failure and its local evidence", RunE: func(cmd *cobra.Command, args []string) error {
		if latest == (job != "") {
			return fmt.Errorf("capsule ci: choose exactly one of --job or --latest")
		}
		store := ci.FileRunStore{ProjectRoot: project}
		var diagnosis ci.RunDiagnosis
		var err error
		if latest {
			diagnosis, err = store.DiagnoseLatest(stallAfter)
		} else {
			diagnosis, err = store.DiagnoseAt(job, time.Now().UTC(), stallAfter)
		}
		if err != nil {
			return err
		}
		if jsonOut {
			return capsuleWorkspaceWrite(cmd, diagnosis, true)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "job: %s\n", diagnosis.Run.JobID)
		fmt.Fprintf(out, "status: %s\n", diagnosis.Run.Status)
		if diagnosis.Run.Pipeline != "" || diagnosis.Run.Outcome != "" {
			fmt.Fprintf(out, "pipeline: %s\n", diagnosis.Run.Pipeline)
			fmt.Fprintf(out, "outcome: %s\n", diagnosis.Run.Outcome)
		}
		if diagnosis.FailureKind != "" {
			fmt.Fprintf(out, "failure_kind: %s\n", diagnosis.FailureKind)
		}
		if diagnosis.FailureSummary != "" {
			fmt.Fprintf(out, "failure_summary: %s\n", diagnosis.FailureSummary)
		}
		if diagnosis.TerminalError != "" {
			fmt.Fprintf(out, "terminal_error: %s\n", diagnosis.TerminalError)
		}
		if diagnosis.LastExecutorEvent != nil {
			fmt.Fprintf(out, "last_executor_event: %s\n", diagnosis.LastExecutorEvent.Kind)
		}
		if diagnosis.Run.Stage != "" {
			fmt.Fprintf(out, "stage: %s\n", diagnosis.Run.Stage)
		}
		if !diagnosis.LastActivityAt.IsZero() {
			fmt.Fprintf(out, "last_activity_at: %s\n", diagnosis.LastActivityAt.Format(time.RFC3339Nano))
		}
		fmt.Fprintf(out, "executor_span_open: %t\n", diagnosis.ExecutorSpanOpen)
		fmt.Fprintf(out, "stalled: %t\n", diagnosis.Stalled)
		if diagnosis.StallReason != "" {
			fmt.Fprintf(out, "stall_reason: %s\n", diagnosis.StallReason)
		}
		for _, artifact := range diagnosis.Artifacts {
			fmt.Fprintf(out, "%s: %s\n", artifact.Kind, artifact.Path)
		}
		if len(diagnosis.NextCommands) > 0 {
			fmt.Fprintln(out, "next_commands:")
			for _, next := range diagnosis.NextCommands {
				fmt.Fprintf(out, "  %s\n", next)
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&job, "job", "", "job id")
	cmd.Flags().BoolVar(&latest, "latest", false, "diagnose the most recently updated run")
	cmd.Flags().DurationVar(&stallAfter, "stall-after", ci.DefaultStallAfter, "report non-terminal runs with no durable activity after this duration")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	return cmd
}

func capsuleCICancelCmd() *cobra.Command {
	var project, job string
	var jsonOut bool
	cmd := &cobra.Command{Use: "cancel", Short: "Cancel a persisted running or parked Capsule CI job", RunE: func(cmd *cobra.Command, args []string) error {
		store := ci.FileRunStore{ProjectRoot: project}
		record, err := store.Get(job)
		if err != nil {
			return err
		}
		if record.Result.Job.Status == artifactjob.StatusAwaitingInput {
			record, err = store.Cancel(job)
			if err != nil {
				return err
			}
			return capsuleWorkspaceWrite(cmd, record, jsonOut)
		}
		if record.Result.Job.Status != artifactjob.StatusRunning && record.Result.Job.Status != artifactjob.StatusInterrupted {
			return fmt.Errorf("capsule ci: job %s cannot be cancelled from %s", job, record.Result.Job.Status)
		}
		controller, err := capsuleCIExecutionController(cmd.Context(), project, record)
		if err != nil {
			return err
		}
		status, err := controller.RequestCancel(cmd.Context(), record.Result.Execution.ExecutionID)
		if err != nil {
			return err
		}
		record, err = store.RecordExecutorStatus(job, status)
		if err != nil {
			return err
		}
		if status.Status == "completed" || status.Status == "failed" {
			return fmt.Errorf("capsule ci: execution %s is already %s; cancellation was not applied", status.ExecutionID, status.Status)
		}
		return capsuleWorkspaceWrite(cmd, record, jsonOut)
	}}
	cmd.Flags().StringVar(&project, "project", ".", "project root")
	cmd.Flags().StringVar(&job, "job", "", "job id")
	cmd.Flags().BoolVar(&jsonOut, "json", true, "print JSON")
	_ = cmd.MarkFlagRequired("job")
	return cmd
}

func capsuleCIExecutionController(ctx context.Context, project string, run ci.RunRecord) (executor.ExecutionController, error) {
	if run.Result.Execution.ExecutionID == "" {
		return nil, fmt.Errorf("capsule ci: job %s has no durable execution id", run.JobID)
	}
	root, err := filepath.Abs(project)
	if err != nil {
		return nil, err
	}
	manager, err := capsuleWorkspaceManager(root)
	if err != nil {
		return nil, err
	}
	workspaceRoot, err := manager.WorkspacePath(ctx, run.Result.Envelope.Instance)
	if err != nil {
		return nil, fmt.Errorf("capsule ci: resolve job workspace for executor control: %w", err)
	}
	cfg, err := ci.Load(workspaceRoot)
	if err != nil {
		return nil, err
	}
	executorName := run.Result.Executor
	if executorName == "" {
		if pipeline, ok := cfg.Pipelines[run.Result.Pipeline]; ok {
			executorName = pipeline.Executor
		}
	}
	configured := ci.NewConfiguredExecutors(cfg)
	configured.ProjectRoot = workspaceRoot
	provider, err := configured.Select(ctx, executorName)
	if err != nil {
		return nil, err
	}
	capabilities, err := provider.Describe(ctx)
	if err != nil {
		return nil, err
	}
	if !capabilities.Cancellable {
		return nil, fmt.Errorf("capsule ci: executor %q does not support cancellation/status control", executorName)
	}
	controller, ok := provider.(executor.ExecutionController)
	if !ok {
		return nil, fmt.Errorf("capsule ci: executor %q does not expose durable status/cancellation", executorName)
	}
	return controller, nil
}

type ciLauncher struct{ verdict ci.Verdict }

func (l ciLauncher) Launch(context.Context, executor.Prepared) (ci.Verdict, error) {
	return l.verdict, nil
}
