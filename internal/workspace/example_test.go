// Runnable godoc examples for the workspace surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/workspace/...`.
package workspace_test

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/host"
	"kitsoki/internal/workspace"
	"kitsoki/internal/world"
)

// Example is the canonical Load worked example: a host handler returns a
// single-repo workspace with a linked issue, Load validates it and writes the
// snapshot under $workspace, and FromMap reads the typed value back out — the
// same trace shown in the package doc.
func Example() {
	// A handler that returns one workspace as map[string]any, the shape a
	// real host.workspace_manager.get would produce.
	reg := host.NewRegistry()
	reg.Register("host.workspace_manager.get",
		func(ctx context.Context, args map[string]any) (host.Result, error) {
			b, _ := json.Marshal(&workspace.Workspace{
				ID:       "ws-1",
				RootPath: "/home/u/app",
				Repos: []workspace.Repo{
					{Path: "/home/u/app", Branch: "main", Dirty: false},
				},
				IssueID: "PROJ-123",
			})
			var data map[string]any
			_ = json.Unmarshal(b, &data)
			return host.Result{Data: data}, nil
		})

	w, ws, err := workspace.Load(context.Background(), reg, "", world.New())
	if err != nil {
		panic(err)
	}

	// Read the snapshot back out of world the way a room's view would.
	back := workspace.FromMap(w.Get(workspace.WorldKey))

	fmt.Println("loaded id:", ws.ID)
	fmt.Println("from world id:", back.ID)
	fmt.Println("root_path:", back.RootPath)
	fmt.Println("repo[0]:", back.Repos[0].Path, "@", back.Repos[0].Branch)
	fmt.Println("issue_id:", back.IssueID)
	fmt.Println("pr_ids:", len(back.PRIDs))
	// Output:
	// loaded id: ws-1
	// from world id: ws-1
	// root_path: /home/u/app
	// repo[0]: /home/u/app @ main
	// issue_id: PROJ-123
	// pr_ids: 0
}

// ExampleWorkspace_ToMap shows the ToMap/FromMap round-trip in isolation: the
// optional pr_ids field is dropped by omitempty when empty and survives when
// set, while id/root_path/repos always round-trip.
func ExampleWorkspace_ToMap() {
	ws := &workspace.Workspace{
		ID:       "ws-2",
		RootPath: "/srv/app",
		Repos:    []workspace.Repo{{Path: "/srv/app", Branch: "dev"}},
		PRIDs:    []string{"PR-9"},
	}

	m := ws.ToMap()
	_, hasIssue := m["issue_id"] // empty -> omitted
	_, hasPRs := m["pr_ids"]     // set -> present

	round := workspace.FromMap(m)

	fmt.Println("has issue_id key:", hasIssue)
	fmt.Println("has pr_ids key:", hasPRs)
	fmt.Println("round-trip branch:", round.Repos[0].Branch)
	fmt.Println("round-trip pr:", round.PRIDs[0])
	// Output:
	// has issue_id key: false
	// has pr_ids key: true
	// round-trip branch: dev
	// round-trip pr: PR-9
}
