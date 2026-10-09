package ci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
)

func TestDoctorPerformsNoSpendReadinessPreflight(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	writeDoctorStory(t, root)
	provider := &failureInjectionProvider{}
	doctor := Doctor{
		ProjectRoot: root,
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Executors: ExecutorSelectorFunc(func(context.Context, string) (executor.Provider, error) {
			return provider, nil
		}),
		Workspace: WorkspaceProbeFunc(func(context.Context, string) (WorkspaceInspection, error) {
			return WorkspaceInspection{Path: filepath.Join(root, ".capsules", "workspaces", "w"), Head: "sha256:source", Branch: "agent/test"}, nil
		}),
		Hygiene: HygienePlannerFunc(func(context.Context, CleanupPolicy) (HygieneReport, error) {
			return HygieneReport{Candidates: 1, TotalBytes: 1024, DiskKnown: true, DiskCapacityBytes: 100 << 30, DiskFreeBytes: 50 << 30, DiskMinimumBytes: 10 << 30}, nil
		}),
		Now: func() time.Time { return time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC) },
	}
	report, err := doctor.Check(context.Background(), DoctorRequest{Pipeline: "change", Workspace: control.Instance{ID: "w", State: control.StateReady, Generation: 1, DefinitionDigest: "sha256:def", Head: "sha256:source"}, WorkspacePath: filepath.Join(root, ".capsules", "workspaces", "w")})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready || report.Schema != DoctorSchema || report.EnvelopeDigest == "" || report.PreparedID == "" {
		t.Fatalf("report %#v", report)
	}
	for _, id := range []string{"project-config", "pipeline", "story-closure", "workspace", "credentials", "disk-capacity", "hygiene-debt", "environment-lock", "executor-select", "executor-describe", "executor-capabilities", "executor-timeouts", "executor-prepare"} {
		check := doctorCheckByID(report.Checks, id)
		if check == nil || check.Outcome != "passed" {
			t.Fatalf("check %s: %#v", id, check)
		}
	}
	if provider.ran {
		t.Fatal("doctor invoked Provider.Run")
	}
}

func TestDoctorReportsCredentialDiskHygieneAndWorkspaceFailuresWithRemedies(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	writeDoctorStory(t, root)
	environmentPath := filepath.Join(root, ".kitsoki", "environments", "ci.yaml")
	raw, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environmentPath, append(raw, []byte("secret_refs: [CI_TOKEN]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	ciPath := filepath.Join(root, ".kitsoki", "ci.yaml")
	raw, err = os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("cleanup:\n  max_reclaimable_bytes: 100\n")...)
	if err := os.WriteFile(ciPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	doctor := Doctor{
		ProjectRoot: root,
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Executors:   ExecutorSelectorFunc(func(context.Context, string) (executor.Provider, error) { return &failureInjectionProvider{}, nil }),
		LookupEnv:   func(string) (string, bool) { return "", false },
		Workspace: WorkspaceProbeFunc(func(context.Context, string) (WorkspaceInspection, error) {
			return WorkspaceInspection{Path: "workspace", Head: "other", Dirty: true}, nil
		}),
		Hygiene: HygienePlannerFunc(func(context.Context, CleanupPolicy) (HygieneReport, error) {
			return HygieneReport{Candidates: 4, TotalBytes: 101, DiskKnown: true, DiskFreeBytes: 1, DiskMinimumBytes: 2, DiskBelowMinimum: true}, nil
		}),
	}
	report, err := doctor.Check(context.Background(), DoctorRequest{Pipeline: "change", Workspace: control.Instance{ID: "w", State: control.StateReady, Generation: 1, DefinitionDigest: "sha256:def", Head: "sha256:source"}, WorkspacePath: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ready {
		t.Fatalf("report unexpectedly ready: %#v", report)
	}
	for _, id := range []string{"workspace", "credentials", "disk-capacity", "hygiene-debt"} {
		check := doctorCheckByID(report.Checks, id)
		if check == nil || check.Outcome != "failed" || len(check.Remedies) == 0 {
			t.Fatalf("check %s: %#v", id, check)
		}
	}
	credential := doctorCheckByID(report.Checks, "credentials")
	if got := strings.ToLower(credential.Summary + strings.Join(credential.Remedies, " ")); strings.Contains(got, "super-secret") {
		t.Fatalf("credential value leaked: %#v", credential)
	}
}

func TestDoctorRejectsRemoteMissingRequiredWorkerEnvironmentWithoutRun(t *testing.T) {
	root := t.TempDir()
	requireFiles(t, root)
	writeDoctorStory(t, root)
	environmentPath := filepath.Join(root, ".kitsoki", "environments", "ci.yaml")
	raw, err := os.ReadFile(environmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environmentPath, append(raw, []byte("secret_refs: [SYNTHETIC_API_KEY]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	ciPath := filepath.Join(root, ".kitsoki", "ci.yaml")
	raw, err = os.ReadFile(ciPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), "    story: .kitsoki/stories/ci/app.yaml\n", "    story: .kitsoki/stories/ci/app.yaml\n    executor: vm\n", 1) + "remotes:\n  vm:\n    endpoint: https://worker.example.invalid\n")
	if err := os.WriteFile(ciPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	provider := &failureInjectionProvider{cap: executor.Capabilities{ID: "remote", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none"}, Cancellable: true}}
	doctor := Doctor{
		ProjectRoot: root,
		Env:         environment.Resolver{Probe: environment.ToolProbeFunc(func(context.Context, string) (string, error) { return "go1.25", nil })},
		Executors:   ExecutorSelectorFunc(func(context.Context, string) (executor.Provider, error) { return provider, nil }),
		LookupEnv:   func(string) (string, bool) { return "", false },
		Workspace: WorkspaceProbeFunc(func(context.Context, string) (WorkspaceInspection, error) {
			return WorkspaceInspection{Path: "workspace", Head: "sha256:source", Branch: "agent/test"}, nil
		}),
		Hygiene: HygienePlannerFunc(func(context.Context, CleanupPolicy) (HygieneReport, error) {
			return HygieneReport{DiskKnown: true, DiskFreeBytes: 50 << 30, DiskMinimumBytes: 10 << 30}, nil
		}),
	}
	report, err := doctor.Check(context.Background(), DoctorRequest{Pipeline: "change", Workspace: control.Instance{ID: "w", State: control.StateReady, Generation: 1, DefinitionDigest: "sha256:def", Head: "sha256:source"}, WorkspacePath: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	check := doctorCheckByID(report.Checks, "worker-environment")
	if report.Ready || check == nil || check.Outcome != "failed" || !strings.Contains(strings.Join(check.Remedies, " "), "--pass-env") {
		t.Fatalf("report %#v", report)
	}
	if provider.ran {
		t.Fatal("doctor invoked provider run")
	}
}

func doctorCheckByID(checks []DoctorCheck, id string) *DoctorCheck {
	for i := range checks {
		if checks[i].ID == id {
			return &checks[i]
		}
	}
	return nil
}

func writeDoctorStory(t *testing.T, root string) {
	t.Helper()
	raw := []byte("app:\n  id: ci\n  version: 0.1.0\n  title: CI\n  author: test\n  license: CC0\nintents:\n  look:\n    description: Look\n    examples: [look]\nroot: idle\nstates:\n  idle:\n    view:\n      - prose: ok\n    on:\n      look:\n        - target: idle\n")
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "stories", "ci", "app.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
