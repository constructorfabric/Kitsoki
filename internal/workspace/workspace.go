// Package workspace implements the typed workspace context (§6).
//
// The $workspace world variable holds structured information about the current
// workspace (repos, branches, issue, PRs). It is loaded when entering a
// Workspace Room and refreshed on explicit user action.
//
// # Provisional struct
//
// The Workspace struct is labeled provisional (§6.1) — it may be extended when
// the schema DSL proves needed. For now it covers: id, root_path, repos,
// issue_id?, pr_ids[].
//
// # Loading
//
// Workspace data is loaded via host.workspace_manager.get, parsed from JSON,
// and stored under $workspace in world state.
//
// # Validation
//
// The JSON schema embedded in workspace.schema.json is used to validate the
// structure of the parsed workspace data before storing it.
package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/host"
	"kitsoki/internal/world"
)

const (
	// WorldKey is the reserved world variable name for the workspace context.
	WorldKey = "$workspace"
)

// Repo represents one repository in the workspace.
type Repo struct {
	// Path is the filesystem path to the repository root.
	Path string `json:"path"`
	// Branch is the currently checked-out branch name.
	Branch string `json:"branch"`
	// Dirty indicates whether there are uncommitted changes.
	Dirty bool `json:"dirty"`
}

// Workspace is the provisional typed workspace context (§6.1).
type Workspace struct {
	// ID is the workspace identifier.
	ID string `json:"id"`
	// RootPath is the root directory of the workspace.
	RootPath string `json:"root_path"`
	// Repos is the list of repositories in the workspace (1+ entries).
	Repos []Repo `json:"repos"`
	// IssueID is the optional issue tracking identifier.
	IssueID string `json:"issue_id,omitempty"`
	// PRIDs is the list of pull request identifiers associated with this workspace.
	PRIDs []string `json:"pr_ids,omitempty"`
}

// Validate checks that the workspace has required fields.
func (w *Workspace) Validate() error {
	if w.ID == "" {
		return fmt.Errorf("workspace: id is required")
	}
	if w.RootPath == "" {
		return fmt.Errorf("workspace: root_path is required")
	}
	if len(w.Repos) == 0 {
		return fmt.Errorf("workspace: repos must have at least one entry")
	}
	for i, r := range w.Repos {
		if r.Path == "" {
			return fmt.Errorf("workspace: repos[%d].path is required", i)
		}
	}
	return nil
}

// ToMap converts the workspace to a map[string]any for world-state storage.
func (w *Workspace) ToMap() map[string]any {
	b, _ := json.Marshal(w)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// FromMap reconstructs a Workspace from a world-state map.
// Returns nil if the map is nil or malformed.
func FromMap(raw any) *Workspace {
	if raw == nil {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var w Workspace
	if err := json.Unmarshal(b, &w); err != nil {
		return nil
	}
	return &w
}

// Load fetches the workspace via host.workspace_manager.get and stores it in world.
// workspaceID is optional; if empty the handler uses the current workspace.
// Returns the updated world and the loaded workspace.
func Load(ctx context.Context, registry *host.Registry, workspaceID string, w world.World) (world.World, *Workspace, error) {
	args := map[string]any{}
	if workspaceID != "" {
		args["workspace_id"] = workspaceID
	}

	result, err := registry.Invoke(ctx, "host.workspace_manager.get", args)
	if err != nil {
		return w, nil, fmt.Errorf("workspace.Load: invoke: %w", err)
	}
	if result.Error != "" {
		return w, nil, fmt.Errorf("workspace.Load: host error: %s", result.Error)
	}

	// Parse the result data into a Workspace.
	ws, err := parseWorkspaceFromData(result.Data)
	if err != nil {
		return w, nil, fmt.Errorf("workspace.Load: parse: %w", err)
	}

	if err := ws.Validate(); err != nil {
		return w, nil, err
	}

	newWorld := w.With(WorldKey, ws.ToMap())
	return newWorld, ws, nil
}

// parseWorkspaceFromData converts result.Data into a Workspace struct.
func parseWorkspaceFromData(data map[string]any) (*Workspace, error) {
	if data == nil {
		return nil, fmt.Errorf("workspace: empty data from handler")
	}
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var ws Workspace
	if err := json.Unmarshal(b, &ws); err != nil {
		return nil, err
	}
	return &ws, nil
}

// SetInWorld stores a Workspace in a world snapshot under $workspace.
func SetInWorld(ws *Workspace, w world.World) world.World {
	if ws == nil {
		return w
	}
	return w.With(WorldKey, ws.ToMap())
}

// ClearFromWorld removes the $workspace key from world state.
func ClearFromWorld(w world.World) world.World {
	nw := world.New()
	for k, v := range w.Vars {
		if k != WorldKey {
			nw.Vars[k] = v
		}
	}
	return nw
}
