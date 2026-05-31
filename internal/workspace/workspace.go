package workspace

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/host"
	"kitsoki/internal/world"
)

const (
	// WorldKey is the reserved world variable name under which the workspace
	// snapshot lives in [world.World]. It carries the leading "$" so it sorts
	// and reads alongside the other engine-reserved world variables; rooms
	// reference it as "$workspace" in guards and templates. Exactly one
	// workspace occupies this key at a time.
	WorldKey = "$workspace"
)

// Repo represents one repository in the workspace. The zero Repo (empty Path)
// fails [Workspace.Validate]; Path is the only required field, while Branch
// and Dirty are advisory snapshot data captured at load time and may be stale.
type Repo struct {
	// Path is the filesystem path to the repository root.
	Path string `json:"path"`
	// Branch is the currently checked-out branch name.
	Branch string `json:"branch"`
	// Dirty indicates whether there are uncommitted changes.
	Dirty bool `json:"dirty"`
}

// Workspace is the typed projection of the `$workspace` world variable. It is
// deliberately provisional — it carries only the fields current rooms consume
// (see the package Reference) and is expected to gain fields additively, so
// the JSON tags rather than positional layout are the stable contract. The
// zero Workspace is invalid: obtain one through [Load] or build it explicitly
// with id, root_path, and at least one repo set.
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

// Validate reports whether the required fields are present: id, root_path,
// and at least one repo whose path is set. It checks presence only — not value
// formats, not cross-field rules, not a JSON Schema — because the struct is
// provisional and the host is the authority on what a valid workspace is; this
// guards against an empty or half-populated snapshot reaching world state, not
// against a semantically wrong one. The error message names the first missing
// field so a misconfigured handler is diagnosable from the log alone.
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

// ToMap projects the workspace into the plain map[string]any shape world
// state stores, emitting only strings, bools, and []any so the snapshot can
// cross the MCP boundary and round-trip through SQLite — the Go structs
// themselves never enter world. omitempty fields (issue_id, pr_ids) are
// dropped when empty, mirroring the JSON tags, so [FromMap] is its inverse
// for any validated Workspace.
func (w *Workspace) ToMap() map[string]any {
	repos := make([]any, len(w.Repos))
	for i, r := range w.Repos {
		repos[i] = map[string]any{
			"path":   r.Path,
			"branch": r.Branch,
			"dirty":  r.Dirty,
		}
	}
	m := map[string]any{
		"id":        w.ID,
		"root_path": w.RootPath,
		"repos":     repos,
	}
	// Optional fields (omitempty in the JSON tags).
	if w.IssueID != "" {
		m["issue_id"] = w.IssueID
	}
	if len(w.PRIDs) > 0 {
		prIDs := make([]any, len(w.PRIDs))
		for i, id := range w.PRIDs {
			prIDs[i] = id
		}
		m["pr_ids"] = prIDs
	}
	return m
}

// FromMap reconstructs a Workspace from whatever sits under [WorldKey] in
// world state. It is the tolerant inverse of [ToMap]: it reads the keys it
// recognises and silently ignores anything malformed, so it never errors and
// never panics on a partial or wrong-typed map — callers handle "absent or
// junk" uniformly via the nil return. It returns nil only when raw is not a
// map[string]any (including a nil interface), which is the signal for
// "no workspace loaded." Validation is not its job; pass the result to
// [Workspace.Validate] if you need the required-field guarantee.
func FromMap(raw any) *Workspace {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil
	}
	w := &Workspace{}
	w.ID, _ = m["id"].(string)
	w.RootPath, _ = m["root_path"].(string)
	w.IssueID, _ = m["issue_id"].(string)
	if repos, ok := m["repos"].([]any); ok {
		for _, item := range repos {
			re, ok := item.(map[string]any)
			if !ok {
				continue
			}
			r := Repo{}
			r.Path, _ = re["path"].(string)
			r.Branch, _ = re["branch"].(string)
			r.Dirty, _ = re["dirty"].(bool)
			w.Repos = append(w.Repos, r)
		}
	}
	if prIDs, ok := m["pr_ids"].([]any); ok {
		for _, item := range prIDs {
			if s, ok := item.(string); ok {
				w.PRIDs = append(w.PRIDs, s)
			}
		}
	}
	return w
}

// Load is the only path that populates `$workspace` from the host: it invokes
// host.workspace_manager.get, parses and validates the result, and writes the
// snapshot under [WorldKey]. It is also the only function here that validates,
// so a Workspace reaching world via Load is guaranteed to satisfy
// [Workspace.Validate]. workspaceID is optional — when empty the workspace_id
// argument is omitted entirely and the handler resolves the current workspace;
// this lets a room load "wherever I am" without knowing the id.
//
// Because [world.World] is immutable, Load returns a new world rather than
// mutating w. On any failure it returns the unchanged input world, a nil
// Workspace, and a wrapped error — distinguishing invoke failure, a non-empty
// host Result.Error, empty/unparseable data, and validation failure in the
// message so the cause is clear from the log.
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

// SetInWorld stores an already-constructed Workspace under [WorldKey] without
// a host round-trip — the path for tests and for callers that built the value
// themselves and have no reason to re-fetch it. It does NOT validate, so it
// will happily store an invalid Workspace; use [Load] when the required-field
// guarantee matters. A nil ws is a no-op: the input world is returned
// unchanged so callers can pass the result of an optional build straight
// through. The returned world is a new snapshot; w is not mutated.
func SetInWorld(ws *Workspace, w world.World) world.World {
	if ws == nil {
		return w
	}
	return w.With(WorldKey, ws.ToMap())
}

// ClearFromWorld returns a new world with [WorldKey] removed, used when
// leaving a Workspace Room so a stale snapshot does not linger for the next
// room's guards. It rebuilds the Vars map (rather than deleting in place)
// because [world.World] is immutable and shared across turns; mutating it
// would corrupt other snapshots. All other world variables are preserved.
func ClearFromWorld(w world.World) world.World {
	nw := world.New()
	for k, v := range w.Vars {
		if k != WorldKey {
			nw.Vars[k] = v
		}
	}
	return nw
}
