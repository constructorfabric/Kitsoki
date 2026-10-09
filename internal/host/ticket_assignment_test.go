package host_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kitsoki/internal/host"
)

func TestLocalFilesTicket_AssignAndUnassign(t *testing.T) {
	root := seedTicketsRoot(t, map[string]string{"T-1.md": sampleBug})
	assign, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{"op": "assign", "root": root, "id": "T-1", "assignee": "brad"})
	if err != nil || assign.Error != "" || assign.Data["assignee"] != "brad" {
		t.Fatalf("assign = %#v, %v", assign, err)
	}
	get, _ := host.LocalFilesTicketHandler(context.Background(), map[string]any{"op": "get", "root": root, "id": "T-1"})
	if get.Data["assignee"] != "brad" {
		t.Fatalf("assignee = %#v", get.Data)
	}
	unassign, err := host.LocalFilesTicketHandler(context.Background(), map[string]any{"op": "unassign", "root": root, "id": "T-1"})
	if err != nil || unassign.Error != "" {
		t.Fatalf("unassign = %#v, %v", unassign, err)
	}
	get, _ = host.LocalFilesTicketHandler(context.Background(), map[string]any{"op": "get", "root": root, "id": "T-1"})
	if get.Data["assignee"] != "" {
		t.Fatalf("assignee after unassign = %#v", get.Data)
	}
	agent, _ := host.LocalFilesTicketHandler(context.Background(), map[string]any{"op": "assign", "root": root, "id": "T-1", "assignee": "agent:worker"})
	if agent.Error == "" {
		t.Fatal("agent principal was accepted")
	}
}

func TestGitHubTicket_AssignUsesNativePatch(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r/issues/7" {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		var body map[string][]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if got := body["assignees"]; len(got) != 1 || got[0] != "brad" {
			t.Fatalf("payload = %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restore()
	res, err := host.GitHubTicketHandler(context.Background(), map[string]any{"op": "assign", "repo": "o/r", "id": "7", "assignee": "brad"})
	if err != nil || res.Error != "" || res.Data["assignee"] != "brad" {
		t.Fatalf("assign = %#v, %v", res, err)
	}
}
