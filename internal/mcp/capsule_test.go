package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/artifactjob"
	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/storydigest"
	kitsokimcp "kitsoki/internal/mcp"
)

func connectCapsule(ctx context.Context, t *testing.T, srv *kitsokimcp.CapsuleServer) *mcpsdk.ClientSession {
	t.Helper()
	one, two := mcpsdk.NewInMemoryTransports()
	_, err := srv.Connect(ctx, one, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "v1"}, nil)
	cs, err := client.Connect(ctx, two, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

type capsuleLauncherFunc func(context.Context, executor.Prepared) (ci.Verdict, error)

func (f capsuleLauncherFunc) Launch(ctx context.Context, prepared executor.Prepared) (ci.Verdict, error) {
	return f(ctx, prepared)
}
func TestCapsuleMCPUsesOpaqueFreshWorkspaceHandles(t *testing.T) {
	project := t.TempDir()
	spec := filepath.Join(project, "capsules", "clean", "capsule.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("name: clean\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: initial.txt\n      content: initial\n    - action: commit\n      message: init\n"), 0o644))
	envSpec := filepath.Join(project, ".kitsoki", "environments", "ci.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(envSpec), 0o755))
	require.NoError(t, os.WriteFile(envSpec, []byte("schema: capsule-environment/v1\nid: ci\nnetwork: none\n"), 0o644))
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{Definitions: control.FileDefinitionStore{ProjectRoot: project}, Instances: control.FileInstanceStore{Root: root}, Providers: map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}}, Grant: control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage", "fs_write", "exec", "vcs_commit", "local_reconcile"}, Branches: []string{"main"}}}
	srv, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "agent", ProjectID: "fixture"})
	require.NoError(t, err)
	ctx := context.Background()
	cs := connectCapsule(ctx, t, srv)
	env, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.env.resolve", Arguments: map[string]any{"id": "ci"}})
	require.NoError(t, err)
	require.False(t, env.IsError, contentText(env))
	require.Contains(t, contentText(env), `"id":"ci"`)
	summary, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.summary", Arguments: map[string]any{"limit": 1}})
	require.NoError(t, err)
	require.False(t, summary.IsError, contentText(summary))
	require.Contains(t, contentText(summary), `"schema":"capsule-ci-provider-summary/v1"`)
	require.NotContains(t, contentText(summary), project, "summary must not leak host paths")
	created, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "run", "definition": "clean"}})
	require.NoError(t, err)
	require.False(t, created.IsError, contentText(created))
	var body struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &body))
	require.NotEmpty(t, body.Workspace.ID)
	written, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.write", Arguments: map[string]any{"workspace": body.Workspace, "path": "agent.txt", "contents": "safe"}})
	require.NoError(t, err)
	require.False(t, written.IsError, contentText(written))
	var changed struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(written)), &changed))
	require.Greater(t, changed.Workspace.Generation, body.Workspace.Generation)
	stale, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.read", Arguments: map[string]any{"workspace": body.Workspace, "path": "agent.txt"}})
	require.NoError(t, err)
	require.True(t, stale.IsError, "stale generation must not retain authority")
	read, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.read", Arguments: map[string]any{"workspace": changed.Workspace, "path": "agent.txt"}})
	require.NoError(t, err)
	require.False(t, read.IsError, contentText(read))
	require.Contains(t, contentText(read), "safe")
	require.NotContains(t, contentText(created), project, "machine path leaked through create")
	committed, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.vcs.commit", Arguments: map[string]any{"workspace": changed.Workspace, "message": "agent change"}})
	require.NoError(t, err)
	require.False(t, committed.IsError, contentText(committed))
	var committedHandle struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(committed)), &committedHandle))
	deniedPlan, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.plan", Arguments: map[string]any{"workspace": committedHandle.Workspace, "operation": "integrate", "target": "release"}})
	require.NoError(t, err)
	require.True(t, deniedPlan.IsError, "ungranted branch must not be reconciled")
	deniedPublish, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.plan", Arguments: map[string]any{"workspace": committedHandle.Workspace, "operation": "publish", "target": "main"}})
	require.NoError(t, err)
	require.True(t, deniedPublish.IsError, "publish requires an explicit remote_publish grant")
	planned, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.plan", Arguments: map[string]any{"workspace": committedHandle.Workspace, "operation": "integrate", "target": "main"}})
	require.NoError(t, err)
	require.False(t, planned.IsError, contentText(planned))
	var plan struct {
		Plan struct {
			Digest string `json:"digest"`
		} `json:"plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(planned)), &plan))
	applied, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.apply", Arguments: map[string]any{"plan_digest": plan.Plan.Digest}})
	require.NoError(t, err)
	require.False(t, applied.IsError, contentText(applied))
	var integrated struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(applied)), &integrated))
	require.Greater(t, integrated.Workspace.Generation, committedHandle.Workspace.Generation)
}

func TestCapsuleMCPOnlyWriterUsesScopedTools(t *testing.T) {
	project := t.TempDir()
	spec := filepath.Join(project, "capsules", "clean", "capsule.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("name: clean\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: initial.txt\n      content: initial\n    - action: commit\n      message: init\n"), 0o644))
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{Definitions: control.FileDefinitionStore{ProjectRoot: project}, Instances: control.FileInstanceStore{Root: root}, Providers: map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}}, Grant: control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage", "fs_write", "exec", "vcs_commit"}}}
	srv, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "writer", ProjectID: "fixture"})
	require.NoError(t, err)
	ctx := context.Background()
	cs := connectCapsule(ctx, t, srv)

	tools, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.NotEmpty(t, tools.Tools)
	for _, tool := range tools.Tools {
		require.True(t, strings.HasPrefix(tool.Name, "capsule."), "writer received non-capsule tool %q", tool.Name)
	}

	created, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "writer-run", "definition": "clean"}})
	require.NoError(t, err)
	require.False(t, created.IsError, contentText(created))
	var createdBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &createdBody))
	require.NotContains(t, contentText(created), project)

	rawDenied, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.exec.run", Arguments: map[string]any{"workspace": createdBody.Workspace, "argv": []string{"sh", "-c", "pwd"}}})
	require.NoError(t, err)
	require.True(t, rawDenied.IsError, "raw argv should require an explicit raw_exec grant")

	written, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.write", Arguments: map[string]any{"workspace": createdBody.Workspace, "path": "change.txt", "contents": "changed by capsule mcp\n"}})
	require.NoError(t, err)
	require.False(t, written.IsError, contentText(written))
	var writtenBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(written)), &writtenBody))
	preimage := sha256.Sum256([]byte("changed by capsule mcp\n"))
	patched, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.patch", Arguments: map[string]any{"workspace": writtenBody.Workspace, "path": "change.txt", "replacement": "patched by capsule mcp\n", "preimage_sha256": "sha256:" + hex.EncodeToString(preimage[:])}})
	require.NoError(t, err)
	require.False(t, patched.IsError, contentText(patched))
	var patchedBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(patched)), &patchedBody))
	rejectedPatch, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.patch", Arguments: map[string]any{"workspace": patchedBody.Workspace, "path": "change.txt", "replacement": "not applied\n", "preimage_sha256": "sha256:wrong"}})
	require.NoError(t, err)
	require.True(t, rejectedPatch.IsError, "stale patch preimage was accepted")

	status, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.vcs.status", Arguments: map[string]any{"workspace": patchedBody.Workspace}})
	require.NoError(t, err)
	require.False(t, status.IsError, contentText(status))
	require.Contains(t, contentText(status), "change.txt")
	require.NotContains(t, contentText(status), project, "status must not leak the host project path")

	committed, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.vcs.commit", Arguments: map[string]any{"workspace": patchedBody.Workspace, "message": "writer change"}})
	require.NoError(t, err)
	require.False(t, committed.IsError, contentText(committed))
	require.NotContains(t, contentText(committed), project)
}

func TestCapsuleMCPSyncConflictsMaterializesProjectRelativeArtifact(t *testing.T) {
	project := t.TempDir()
	spec := filepath.Join(project, "capsules", "clean", "capsule.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("name: clean\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: initial.txt\n      content: initial\n    - action: commit\n      message: init\n"), 0o644))
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{Definitions: control.FileDefinitionStore{ProjectRoot: project}, Instances: control.FileInstanceStore{Root: root}, Providers: map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}}, Grant: control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage", "local_reconcile"}, Branches: []string{"target"}}}
	srv, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "agent", ProjectID: "fixture"})
	require.NoError(t, err)
	ctx := context.Background()
	cs := connectCapsule(ctx, t, srv)

	created, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "diverged", "definition": "clean"}})
	require.NoError(t, err)
	require.False(t, created.IsError, contentText(created))
	var createdBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &createdBody))
	workspacePath, err := manager.WorkspacePath(ctx, createdBody.Workspace)
	require.NoError(t, err)
	runMCPGit(t, workspacePath, "config", "user.email", "capsule@example.invalid")
	runMCPGit(t, workspacePath, "config", "user.name", "Capsule Test")
	runMCPGit(t, workspacePath, "checkout", "-b", "target")
	require.NoError(t, os.WriteFile(filepath.Join(workspacePath, "target.txt"), []byte("target\n"), 0o644))
	runMCPGit(t, workspacePath, "add", "target.txt")
	runMCPGit(t, workspacePath, "commit", "-m", "target")
	runMCPGit(t, workspacePath, "checkout", "main")
	runMCPGit(t, workspacePath, "checkout", "-b", "candidate")
	require.NoError(t, os.WriteFile(filepath.Join(workspacePath, "candidate.txt"), []byte("candidate\n"), 0o644))
	runMCPGit(t, workspacePath, "add", "candidate.txt")
	runMCPGit(t, workspacePath, "commit", "-m", "candidate")

	planned, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.plan", Arguments: map[string]any{"workspace": createdBody.Workspace, "operation": "integrate", "target": "target"}})
	require.NoError(t, err)
	require.False(t, planned.IsError, contentText(planned))
	var planBody struct {
		Plan struct {
			Digest string `json:"digest"`
			Class  string `json:"class"`
		} `json:"plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(planned)), &planBody))
	require.Equal(t, "diverged", planBody.Plan.Class)

	conflicts, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.conflicts", Arguments: map[string]any{"plan_digest": planBody.Plan.Digest}})
	require.NoError(t, err)
	require.False(t, conflicts.IsError, contentText(conflicts))
	require.NotContains(t, contentText(conflicts), project, "conflict result must not leak host paths")
	var conflictBody struct {
		Path     string `json:"path"`
		Artifact struct {
			Schema            string   `json:"schema"`
			PlanDigest        string   `json:"plan_digest"`
			ContinuationToken string   `json:"continuation_token"`
			CandidatePaths    []string `json:"candidate_paths"`
			TargetPaths       []string `json:"target_paths"`
			OverlapPaths      []string `json:"overlap_paths"`
			RequiredInputs    []string `json:"required_inputs"`
		} `json:"artifact"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(conflicts)), &conflictBody))
	require.Equal(t, "capsule-sync-conflict/v1", conflictBody.Artifact.Schema)
	require.Equal(t, planBody.Plan.Digest, conflictBody.Artifact.PlanDigest)
	require.NotEmpty(t, conflictBody.Artifact.ContinuationToken)
	require.Contains(t, conflictBody.Artifact.CandidatePaths, "candidate.txt")
	require.Contains(t, conflictBody.Artifact.TargetPaths, "target.txt")
	require.NotEmpty(t, conflictBody.Artifact.RequiredInputs)
	require.False(t, filepath.IsAbs(conflictBody.Path), "artifact path must be project-relative")
	require.True(t, strings.HasPrefix(conflictBody.Path, ".capsules/sync/"), "artifact path should stay inside Capsule state: %s", conflictBody.Path)
	require.FileExists(t, filepath.Join(project, filepath.FromSlash(conflictBody.Path)))

	integration, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.integration", Arguments: map[string]any{"plan_digest": planBody.Plan.Digest}})
	require.NoError(t, err)
	require.False(t, integration.IsError, contentText(integration))
	require.NotContains(t, contentText(integration), project, "integration result must not leak host paths")
	var integrationBody struct {
		Path     string `json:"path"`
		Instance struct {
			Schema       string `json:"schema"`
			PlanDigest   string `json:"plan_digest"`
			InstancePath string `json:"instance_path"`
			State        string `json:"state"`
		} `json:"instance"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(integration)), &integrationBody))
	require.Equal(t, "capsule-sync-integration/v1", integrationBody.Instance.Schema)
	require.Equal(t, planBody.Plan.Digest, integrationBody.Instance.PlanDigest)
	require.False(t, filepath.IsAbs(integrationBody.Path), "integration artifact path must be project-relative")
	require.False(t, filepath.IsAbs(integrationBody.Instance.InstancePath), "integration instance path must be project-relative")
	require.True(t, strings.HasPrefix(integrationBody.Path, ".capsules/sync/"), "integration artifact should stay inside Capsule state: %s", integrationBody.Path)
	require.True(t, strings.HasPrefix(integrationBody.Instance.InstancePath, ".capsules/sync/"), "integration instance should stay inside Capsule state: %s", integrationBody.Instance.InstancePath)
	require.FileExists(t, filepath.Join(project, filepath.FromSlash(integrationBody.Path)))
	require.DirExists(t, filepath.Join(project, filepath.FromSlash(integrationBody.Instance.InstancePath)))
	integrationPath := filepath.Join(project, filepath.FromSlash(integrationBody.Instance.InstancePath))
	runMCPGit(t, integrationPath, "config", "user.email", "capsule@example.invalid")
	runMCPGit(t, integrationPath, "config", "user.name", "Capsule Test")
	runMCPGit(t, integrationPath, "commit", "-m", "resolved integration")
	continued, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.sync.continue", Arguments: map[string]any{"plan_digest": planBody.Plan.Digest, "resolver_decision": "artifact:resolver", "lost_work_review": "artifact:review", "validation_receipt": "receipt:validation"}})
	require.NoError(t, err)
	require.False(t, continued.IsError, contentText(continued))
	require.NotContains(t, contentText(continued), project, "continuation result must not leak host paths")
	var continuedBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(continued)), &continuedBody))
	require.Greater(t, continuedBody.Workspace.Generation, createdBody.Workspace.Generation)
}

func TestCapsuleMCPCleanupIsGrantScopedAndProjectRelative(t *testing.T) {
	project := t.TempDir()
	ciDir := filepath.Join(project, ".capsules", "ci")
	require.NoError(t, os.MkdirAll(ciDir, 0o755))
	now := time.Now()
	for i := 0; i < 3; i++ {
		job := string(rune('a' + i))
		run := filepath.Join(ciDir, job+".run.json")
		trace := filepath.Join(ciDir, job+".trace.json")
		require.NoError(t, os.WriteFile(run, []byte(`{"result":{"job":{"status":"done"}}}`), 0o644))
		require.NoError(t, os.WriteFile(trace, []byte(job+" trace"), 0o644))
		when := now.Add(time.Duration(i) * time.Minute)
		require.NoError(t, os.Chtimes(run, when, when))
		require.NoError(t, os.Chtimes(trace, when, when))
	}
	cacheDir := filepath.Join(project, ".capsules", "cache", "old")
	require.NoError(t, os.MkdirAll(cacheDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "blob"), []byte("cache"), 0o644))

	manager := &control.Manager{Grant: control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{filepath.Join(project, ".capsules", "workspaces")}}}
	srv, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "agent", ProjectID: "fixture"})
	require.NoError(t, err)
	ctx := context.Background()
	cs := connectCapsule(ctx, t, srv)

	planned, err := cs.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.cleanup.plan", Arguments: map[string]any{"keep_runs": 1, "include_capsule_cache": true}})
	require.NoError(t, err)
	require.False(t, planned.IsError, contentText(planned))
	require.NotContains(t, contentText(planned), project, "cleanup plan must not leak host paths")
	var planBody struct {
		Plan struct {
			Schema     string `json:"schema"`
			Project    string `json:"project"`
			Candidates []struct {
				Path string `json:"path"`
			} `json:"candidates"`
		} `json:"plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(planned)), &planBody))
	require.Equal(t, "capsule-hygiene-plan/v1", planBody.Plan.Schema)
	require.Equal(t, "fixture", planBody.Plan.Project)
	require.Len(t, planBody.Plan.Candidates, 5)
	for _, candidate := range planBody.Plan.Candidates {
		require.False(t, filepath.IsAbs(candidate.Path), "candidate path should be project-relative: %s", candidate.Path)
		require.True(t, strings.HasPrefix(candidate.Path, ".capsules/"), "candidate path should remain scoped: %s", candidate.Path)
	}

	tools, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.NotContains(t, capsuleToolNames(tools), "capsule.cleanup.apply", "cleanup apply must not be registered without its effect")
	require.FileExists(t, filepath.Join(ciDir, "a.run.json"))

	manager.Grant.Effects = []string{"cleanup"}
	tools, err = cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.NotContains(t, capsuleToolNames(tools), "capsule.cleanup.apply", "post-startup grant mutation widened the server")

	allowedManager := &control.Manager{Grant: control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{filepath.Join(project, ".capsules", "workspaces")}, Effects: []string{"cleanup"}}}
	allowedServer, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: allowedManager, Owner: "cleaner", ProjectID: "fixture"})
	require.NoError(t, err)
	allowedClient := connectCapsule(ctx, t, allowedServer)
	allowedTools, err := allowedClient.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Contains(t, capsuleToolNames(allowedTools), "capsule.cleanup.apply")
	allowed, err := allowedClient.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.cleanup.apply", Arguments: map[string]any{"keep_runs": 1, "include_capsule_cache": true}})
	require.NoError(t, err)
	require.False(t, allowed.IsError, contentText(allowed))
	require.NotContains(t, contentText(allowed), project, "cleanup result must not leak host paths")
	require.NoFileExists(t, filepath.Join(ciDir, "a.run.json"))
	require.NoFileExists(t, filepath.Join(ciDir, "a.trace.json"))
	require.NoFileExists(t, filepath.Join(ciDir, "b.run.json"))
	require.NoFileExists(t, filepath.Join(ciDir, "b.trace.json"))
	require.FileExists(t, filepath.Join(ciDir, "c.run.json"))
	require.NoDirExists(t, cacheDir)
}

func capsuleToolNames(result *mcpsdk.ListToolsResult) []string {
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}

func TestCapsuleMCPRegistersOnlyStartupGrantedToolFamilies(t *testing.T) {
	project := t.TempDir()
	writeCapsuleMCPSecurityFixture(t, project)
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{
		Definitions: control.FileDefinitionStore{ProjectRoot: project},
		Instances:   control.FileInstanceStore{Root: root},
		Providers:   map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}},
		Grant:       control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage"}},
	}
	srv, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "agent"})
	require.NoError(t, err)
	ctx := context.Background()
	client := connectCapsule(ctx, t, srv)
	listed, err := client.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := capsuleToolNames(listed)
	require.Contains(t, names, "capsule.workspace.create")
	require.Contains(t, names, "capsule.fs.list")
	for _, denied := range []string{"capsule.fs.write", "capsule.exec.run", "capsule.vcs.commit", "capsule.sync.plan", "capsule.ci.plan", "capsule.ci.run", "capsule.ci.cancel", "capsule.cleanup.apply", "capsule.env.lock"} {
		require.NotContains(t, names, denied)
	}

	manager.Grant.Effects = append(manager.Grant.Effects, "fs_write", "exec", "vcs_commit", "local_reconcile", "ci_run", "cleanup", "env_write")
	listed, err = client.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	require.Equal(t, names, capsuleToolNames(listed), "post-startup mutation changed the registered tool set")
}

func TestCapsuleMCPRejectsEveryGuessedHandleFromAnotherOwner(t *testing.T) {
	project := t.TempDir()
	writeCapsuleMCPSecurityFixture(t, project)
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{
		Definitions: control.FileDefinitionStore{ProjectRoot: project},
		Instances:   control.FileInstanceStore{Root: root},
		Providers:   map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}},
		Grant: control.ScopeGrant{
			ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"},
			Effects: []string{"workspace_manage", "fs_write", "exec", "vcs_commit", "local_reconcile", "ci_run"}, Branches: []string{"main"},
		},
	}
	serverA, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner-a", Pipeline: "change", CIExecutor: "local"})
	require.NoError(t, err)
	serverB, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner-b", Pipeline: "change", CIExecutor: "host"})
	require.NoError(t, err)
	ctx := context.Background()
	clientA := connectCapsule(ctx, t, serverA)
	clientB := connectCapsule(ctx, t, serverB)

	created, err := clientA.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "owned-by-a", "definition": "clean"}})
	require.NoError(t, err)
	require.False(t, created.IsError, contentText(created))
	var createdBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &createdBody))
	handle := createdBody.Workspace

	requests := []struct {
		name string
		args map[string]any
	}{
		{"capsule.workspace.status", map[string]any{"workspace": handle}},
		{"capsule.workspace.close", map[string]any{"workspace": handle}},
		{"capsule.fs.list", map[string]any{"workspace": handle, "path": ""}},
		{"capsule.fs.read", map[string]any{"workspace": handle, "path": "initial.txt"}},
		{"capsule.fs.write", map[string]any{"workspace": handle, "path": "stolen.txt", "contents": "no"}},
		{"capsule.fs.search", map[string]any{"workspace": handle, "query": "initial"}},
		{"capsule.exec.run", map[string]any{"workspace": handle, "command_id": "missing"}},
		{"capsule.vcs.status", map[string]any{"workspace": handle}},
		{"capsule.vcs.diff", map[string]any{"workspace": handle}},
		{"capsule.vcs.commit", map[string]any{"workspace": handle, "message": "stolen"}},
		{"capsule.sync.plan", map[string]any{"workspace": handle, "operation": "integrate", "target": "main"}},
		{"capsule.ci.plan", map[string]any{"workspace": handle, "pipeline": "change"}},
		{"capsule.ci.run", map[string]any{"workspace": handle, "pipeline": "change"}},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			result, err := clientB.CallTool(ctx, &mcpsdk.CallToolParams{Name: request.name, Arguments: request.args})
			require.NoError(t, err)
			require.True(t, result.IsError, contentText(result))
			require.Contains(t, contentText(result), "denied")
		})
	}
	require.NoFileExists(t, filepath.Join(root, "owned-by-a", "stolen.txt"))

	otherPipeline, err := clientA.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.plan", Arguments: map[string]any{"workspace": handle, "pipeline": "other"}})
	require.NoError(t, err)
	require.True(t, otherPipeline.IsError)
	require.Contains(t, contentText(otherPipeline), "denied")
}

func TestCapsuleMCPValidatesPipelineExecutorAndOwnerAtStartup(t *testing.T) {
	project := t.TempDir()
	writeCapsuleMCPSecurityFixture(t, project)
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{
		Definitions: control.FileDefinitionStore{ProjectRoot: project},
		Instances:   control.FileInstanceStore{Root: root},
		Providers:   map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}},
		Grant:       control.ScopeGrant{Owner: "owner", ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"ci_run"}},
	}
	_, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "other"})
	require.Error(t, err)
	_, err = kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner", CIExecutor: "host"})
	require.Error(t, err)
	_, err = kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner", Pipeline: "missing", CIExecutor: "host"})
	require.Error(t, err)
	_, err = kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner", Pipeline: "change", CIExecutor: "container"})
	require.Error(t, err)
	_, err = kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "owner", Pipeline: "change", CIExecutor: "local"})
	require.NoError(t, err)
}

func TestCapsuleMCPRunPersistsObservableStoryClosure(t *testing.T) {
	project := t.TempDir()
	writeCapsuleMCPSecurityFixture(t, project)
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{
		Definitions: control.FileDefinitionStore{ProjectRoot: project},
		Instances:   control.FileInstanceStore{Root: root},
		Providers:   map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}},
		Grant: control.ScopeGrant{
			ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"},
			Effects: []string{"workspace_manage", "fs_write", "vcs_commit", "ci_run"},
		},
	}
	launcher := capsuleLauncherFunc(func(_ context.Context, prepared executor.Prepared) (ci.Verdict, error) {
		envelope := prepared.Envelope
		return ci.Verdict{
			Schema: ci.VerdictSchema, Pipeline: "change", Outcome: "passed", PromotionEligible: true,
			Checks:            []ci.Check{{ID: "deterministic", Kind: "test", Outcome: "passed", Evidence: []string{"artifact:test"}}},
			SourceDigest:      envelope.SourceDigest,
			StoryDigest:       envelope.StoryDigest,
			EnvironmentDigest: envelope.Environment.Digest,
			EnvelopeDigest:    envelope.Digest,
		}, nil
	})
	server, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{
		Manager: manager, Owner: "runner", Pipeline: "change", CIExecutor: "local",
		CILauncher: func(string) ci.Launcher { return launcher },
	})
	require.NoError(t, err)
	ctx := context.Background()
	client := connectCapsule(ctx, t, server)
	created, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "observable", "definition": "clean"}})
	require.NoError(t, err)
	require.False(t, created.IsError, contentText(created))
	var createdBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &createdBody))

	firstPlan, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.plan", Arguments: map[string]any{"workspace": createdBody.Workspace, "pipeline": "change"}})
	require.NoError(t, err)
	require.False(t, firstPlan.IsError, contentText(firstPlan))
	var first struct {
		Envelope executor.Envelope `json:"envelope"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(firstPlan)), &first))
	workspacePath, err := manager.WorkspacePath(ctx, createdBody.Workspace)
	require.NoError(t, err)
	closure, err := storydigest.Compute(workspacePath, "stories/ci/app.yaml")
	require.NoError(t, err)
	require.Equal(t, closure.Digest, first.Envelope.StoryDigest)

	written, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.write", Arguments: map[string]any{"workspace": createdBody.Workspace, "path": "stories/ci/prompts/review.md", "contents": "review v2\n"}})
	require.NoError(t, err)
	require.False(t, written.IsError, contentText(written))
	var writtenBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(written)), &writtenBody))
	committed, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.vcs.commit", Arguments: map[string]any{"workspace": writtenBody.Workspace, "message": "Update CI prompt"}})
	require.NoError(t, err)
	require.False(t, committed.IsError, contentText(committed))
	var committedBody struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(committed)), &committedBody))
	secondPlan, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.plan", Arguments: map[string]any{"workspace": committedBody.Workspace, "pipeline": "change"}})
	require.NoError(t, err)
	require.False(t, secondPlan.IsError, contentText(secondPlan))
	var second struct {
		Envelope executor.Envelope `json:"envelope"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(secondPlan)), &second))
	require.NotEqual(t, first.Envelope.StoryDigest, second.Envelope.StoryDigest, "prompt change did not alter the story closure digest")

	run, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.run", Arguments: map[string]any{"workspace": committedBody.Workspace, "pipeline": "change"}})
	require.NoError(t, err)
	require.False(t, run.IsError, contentText(run))
	var runBody struct {
		Result ci.RunResult `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(run)), &runBody))
	require.Equal(t, ci.RunStageFinished, runBody.Result.Stage)
	require.True(t, runBody.Result.Terminal)
	require.Equal(t, second.Envelope.StoryDigest, runBody.Result.Envelope.StoryDigest)

	jobID := string(runBody.Result.Job.ID)
	require.NotEmpty(t, jobID)
	require.FileExists(t, filepath.Join(project, ".capsules", "ci", jobID+".run.json"))
	require.FileExists(t, filepath.Join(project, ".capsules", "ci", jobID+".trace.json"))
	require.FileExists(t, filepath.Join(project, ".capsules", "ci", jobID+".receipt.json"))
	stored, err := (ci.FileRunStore{ProjectRoot: project}).Get(jobID)
	require.NoError(t, err)
	require.Equal(t, ci.RunStageFinished, stored.Result.Stage)
	require.True(t, stored.Result.Terminal)
	trace, err := os.ReadFile(filepath.Join(project, ".capsules", "ci", jobID+".trace.json"))
	require.NoError(t, err)
	require.Contains(t, string(trace), "capsule.executor.started")
}

func TestCapsuleMCPRefreshAndCancelUseDurableRemoteController(t *testing.T) {
	var deletes int
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/capsules/capabilities":
			_ = json.NewEncoder(w).Encode(map[string]any{"capabilities": executor.Capabilities{ID: "remote", Placements: []string{"remote"}, Isolation: "supervised", Networks: []string{"none"}, Cancellable: true}})
		case r.URL.Path == "/v1/capsules/executions/remote-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"run": executor.ExecutionStatus{ExecutionID: "remote-1", Status: "running", Stage: "running_story"}})
		case r.URL.Path == "/v1/capsules/executions/remote-1" && r.Method == http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"run": executor.ExecutionStatus{ExecutionID: "remote-1", Status: "cancelling", Stage: "cancellation_requested"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer worker.Close()

	project := t.TempDir()
	writeCapsuleMCPSecurityFixture(t, project)
	remoteCI := strings.ReplaceAll(readMCPFixture(t, filepath.Join(project, ".kitsoki", "ci.yaml")), "executor: host", "executor: vm") + "remotes:\n  vm:\n    endpoint: " + worker.URL + "\n    ca_file: worker-ca.pem\n"
	require.NoError(t, os.WriteFile(filepath.Join(project, ".kitsoki", "ci.yaml"), []byte(remoteCI), 0o644))
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: worker.Certificate().Raw}))
	require.NoError(t, os.WriteFile(filepath.Join(project, "worker-ca.pem"), []byte(caPEM), 0o644))
	root := filepath.Join(project, ".capsules", "workspaces")
	manager := &control.Manager{
		Definitions: control.FileDefinitionStore{ProjectRoot: project},
		Instances:   control.FileInstanceStore{Root: root},
		Providers:   map[string]control.WorkspaceProvider{"synthetic": control.SyntheticProvider{ProjectRoot: project}},
		Grant:       control.ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{root}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage", "fs_write", "vcs_commit", "ci_run"}},
	}
	server, err := kitsokimcp.NewCapsuleServer(kitsokimcp.CapsuleConfig{Manager: manager, Owner: "runner", Pipeline: "change", CIExecutor: "vm"})
	require.NoError(t, err)
	ctx := context.Background()
	client := connectCapsule(ctx, t, server)
	created, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.workspace.create", Arguments: map[string]any{"id": "remote-control", "definition": "clean"}})
	require.NoError(t, err)
	var current struct {
		Workspace control.Handle `json:"workspace"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(created)), &current))
	for path, contents := range map[string]string{".kitsoki/ci.yaml": remoteCI, "worker-ca.pem": caPEM} {
		written, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.fs.write", Arguments: map[string]any{"workspace": current.Workspace, "path": path, "contents": contents}})
		require.NoError(t, err)
		require.False(t, written.IsError, contentText(written))
		require.NoError(t, json.Unmarshal([]byte(contentText(written)), &current))
	}
	committed, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.vcs.commit", Arguments: map[string]any{"workspace": current.Workspace, "message": "Configure remote worker"}})
	require.NoError(t, err)
	require.False(t, committed.IsError, contentText(committed))
	require.NoError(t, json.Unmarshal([]byte(contentText(committed)), &current))

	run := ci.RunResult{Job: artifactjob.Job{ID: "job-remote", Status: artifactjob.StatusRunning}, Pipeline: "change", Executor: "vm", Stage: ci.RunStageRunning, Envelope: executor.Envelope{Instance: current.Workspace}, Execution: executor.Result{ExecutionID: "remote-1"}}
	require.NoError(t, (ci.FileRunStore{ProjectRoot: project}).Write(ci.RunRecord{JobID: "job-remote", Result: run}))
	status, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.status", Arguments: map[string]any{"job": "job-remote", "refresh": true}})
	require.NoError(t, err)
	require.False(t, status.IsError, contentText(status))
	require.Contains(t, contentText(status), `"status":"running"`)
	cancelled, err := client.CallTool(ctx, &mcpsdk.CallToolParams{Name: "capsule.ci.cancel", Arguments: map[string]any{"job": "job-remote"}})
	require.NoError(t, err)
	require.False(t, cancelled.IsError, contentText(cancelled))
	require.Contains(t, contentText(cancelled), `"status":"cancelling"`)
	require.Equal(t, 1, deletes)
}

func writeCapsuleMCPSecurityFixture(t *testing.T, project string) {
	t.Helper()
	files := map[string]string{
		"capsules/clean/capsule.yaml": `name: clean
source:
  synthetic: true
  steps:
    - action: write
      path: initial.txt
      content: initial
    - action: write
      path: .kitsoki/environments/ci.yaml
      content: |
        schema: capsule-environment/v1
        id: ci
        network: live
    - action: write
      path: stories/ci/app.yaml
      content: |
        app:
          id: fixture-ci
          version: 0.1.0
          title: Fixture CI
          author: Test
          license: CC0
        intents:
          run:
            description: run
            examples: [run]
            priority: 1
        root: idle
        states:
          idle:
            terminal: true
            view:
              - prose: ready
    - action: write
      path: stories/ci/prompts/review.md
      content: review v1
    - action: write
      path: .kitsoki/ci.yaml
      content: |
        schema: capsule-ci/v1
        default_environment: ci
        pipelines:
          change:
            story: stories/ci/app.yaml
            triggers: [local]
            environment: ci
            executor: host
            permissions:
              network: live
              external_write: allow
            agents:
              policy: deny
            result:
              schema: capsule-ci-verdict/v1
          other:
            story: stories/ci/app.yaml
            triggers: [local]
            environment: ci
            executor: host
            permissions:
              network: live
              external_write: allow
            agents:
              policy: deny
            result:
              schema: capsule-ci-verdict/v1
    - action: commit
      message: init
`,
		".kitsoki/environments/ci.yaml": "schema: capsule-environment/v1\nid: ci\nnetwork: live\n",
		"stories/ci/app.yaml":           "app:\n  id: fixture-ci\n  version: 0.1.0\n  title: Fixture CI\n  author: Test\n  license: CC0\nintents:\n  run:\n    description: run\n    examples: [run]\n    priority: 1\nroot: idle\nstates:\n  idle:\n    terminal: true\n    view:\n      - prose: ready\n",
		"stories/ci/prompts/review.md":  "review v1\n",
		".kitsoki/ci.yaml":              "schema: capsule-ci/v1\ndefault_environment: ci\npipelines:\n  change:\n    story: stories/ci/app.yaml\n    triggers: [local]\n    environment: ci\n    executor: host\n    permissions:\n      network: live\n      external_write: allow\n    agents:\n      policy: deny\n    result:\n      schema: capsule-ci-verdict/v1\n  other:\n    story: stories/ci/app.yaml\n    triggers: [local]\n    environment: ci\n    executor: host\n    permissions:\n      network: live\n      external_write: allow\n    agents:\n      policy: deny\n    result:\n      schema: capsule-ci-verdict/v1\n",
	}
	for relative, contents := range files {
		path := filepath.Join(project, filepath.FromSlash(relative))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
}

func readMCPFixture(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}

func runMCPGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
