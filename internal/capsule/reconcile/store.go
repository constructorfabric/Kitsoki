package reconcile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// StoredPlan keeps the opaque workspace id beside an immutable reconciliation
// plan. Plan files are local operator state, not an MCP response surface.
type StoredPlan struct {
	WorkspaceID string `json:"workspace_id"`
	Plan        Plan   `json:"plan"`
}

// FilePlanStore retains plans so plan and apply may be separate CLI processes.
type FilePlanStore struct {
	ProjectRoot string
}

func (s FilePlanStore) Write(record StoredPlan) error {
	if record.WorkspaceID == "" || record.Plan.Digest == "" {
		return fmt.Errorf("capsule reconcile: workspace id and plan digest are required")
	}
	if record.Plan.Digest != planDigest(record.Plan) {
		return fmt.Errorf("capsule reconcile: invalid plan digest")
	}
	dir := filepath.Join(s.ProjectRoot, ".capsules", "sync")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, record.Plan.Digest+".json"), append(raw, '\n'), 0o600)
}

func (s FilePlanStore) Get(digest string) (StoredPlan, error) {
	if digest == "" || filepath.Base(digest) != digest {
		return StoredPlan{}, fmt.Errorf("capsule reconcile: invalid plan digest")
	}
	raw, err := os.ReadFile(filepath.Join(s.ProjectRoot, ".capsules", "sync", digest+".json"))
	if err != nil {
		return StoredPlan{}, err
	}
	var record StoredPlan
	if err := json.Unmarshal(raw, &record); err != nil {
		return StoredPlan{}, err
	}
	if record.WorkspaceID == "" || record.Plan.Digest != digest || record.Plan.Digest != planDigest(record.Plan) {
		return StoredPlan{}, fmt.Errorf("capsule reconcile: invalid stored plan")
	}
	return record, nil
}
