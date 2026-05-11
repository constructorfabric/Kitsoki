package workspace_test

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/world"
	"kitsoki/internal/workspace"
)

func fakeWorkspaceHandler(ws *workspace.Workspace) host.Handler {
	return func(ctx context.Context, args map[string]any) (host.Result, error) {
		b, _ := json.Marshal(ws)
		var data map[string]any
		_ = json.Unmarshal(b, &data)
		return host.Result{Data: data}, nil
	}
}

func TestWorkspace_ToMapFromMap(t *testing.T) {
	ws := &workspace.Workspace{
		ID:       "ws-1",
		RootPath: "/home/user/projects/myapp",
		Repos: []workspace.Repo{
			{Path: "/home/user/projects/myapp", Branch: "main", Dirty: false},
		},
		IssueID: "PROJ-123",
		PRIDs:   []string{"PR-456"},
	}

	m := ws.ToMap()
	ws2 := workspace.FromMap(m)
	if ws2 == nil {
		t.Fatal("FromMap returned nil")
	}
	if ws2.ID != "ws-1" {
		t.Fatalf("expected id=ws-1, got %s", ws2.ID)
	}
	if len(ws2.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(ws2.Repos))
	}
	if ws2.Repos[0].Branch != "main" {
		t.Fatalf("expected branch=main, got %s", ws2.Repos[0].Branch)
	}
	if ws2.IssueID != "PROJ-123" {
		t.Fatalf("expected issue_id=PROJ-123, got %s", ws2.IssueID)
	}
}

func TestWorkspace_Validate(t *testing.T) {
	cases := []struct {
		name    string
		ws      workspace.Workspace
		wantErr bool
	}{
		{
			name: "valid",
			ws: workspace.Workspace{
				ID: "ws-1", RootPath: "/tmp",
				Repos: []workspace.Repo{{Path: "/tmp"}},
			},
		},
		{name: "missing id", ws: workspace.Workspace{RootPath: "/tmp", Repos: []workspace.Repo{{Path: "/tmp"}}}, wantErr: true},
		{name: "missing root_path", ws: workspace.Workspace{ID: "ws-1", Repos: []workspace.Repo{{Path: "/tmp"}}}, wantErr: true},
		{name: "no repos", ws: workspace.Workspace{ID: "ws-1", RootPath: "/tmp"}, wantErr: true},
		{name: "repo missing path", ws: workspace.Workspace{ID: "ws-1", RootPath: "/tmp", Repos: []workspace.Repo{{}}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.ws.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestWorkspace_Load(t *testing.T) {
	reg := host.NewRegistry()
	fakeWS := &workspace.Workspace{
		ID:       "ws-test",
		RootPath: "/tmp/myapp",
		Repos:    []workspace.Repo{{Path: "/tmp/myapp", Branch: "feature/x"}},
	}
	reg.Register("host.workspace_manager.get", fakeWorkspaceHandler(fakeWS))

	w := world.New()
	w2, loaded, err := workspace.Load(context.Background(), reg, "", w)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected loaded workspace")
	}
	if loaded.ID != "ws-test" {
		t.Fatalf("expected ws-test, got %s", loaded.ID)
	}

	// Verify $workspace is set in world.
	wsVal, ok := w2.Vars[workspace.WorldKey]
	if !ok {
		t.Fatal("expected $workspace in world vars")
	}
	wsFromWorld := workspace.FromMap(wsVal)
	if wsFromWorld == nil || wsFromWorld.ID != "ws-test" {
		t.Fatalf("expected ws-test from world, got %v", wsFromWorld)
	}
}

func TestWorkspace_SetAndClearInWorld(t *testing.T) {
	ws := &workspace.Workspace{ID: "ws-1", RootPath: "/tmp", Repos: []workspace.Repo{{Path: "/tmp"}}}
	w := world.New()
	w = workspace.SetInWorld(ws, w)
	if _, ok := w.Vars[workspace.WorldKey]; !ok {
		t.Fatal("expected $workspace after SetInWorld")
	}
	w = workspace.ClearFromWorld(w)
	if _, ok := w.Vars[workspace.WorldKey]; ok {
		t.Fatal("expected $workspace cleared after ClearFromWorld")
	}
}
