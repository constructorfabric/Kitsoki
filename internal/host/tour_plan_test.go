package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// repoRootForTourTest walks up from the test's working dir to the repo root
// (the dir holding features/), mirroring internal/tour's own test helper —
// internal/host's test cwd is the package dir.
func repoRootForTourTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, "features")); err == nil && st.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repo root (features/) not found from test cwd")
		}
		dir = parent
	}
}

func TestTourPlanHandler_PlansFromRealFeatureCatalog(t *testing.T) {
	root := repoRootForTourTest(t)
	featuresDir := filepath.Join(root, "features")
	if _, err := os.Stat(filepath.Join(featuresDir, "agent-actions.yaml")); err != nil {
		t.Skip("features/agent-actions.yaml not present")
	}

	result, err := TourPlanHandler(context.Background(), map[string]any{
		"feature_id":   "agent-actions",
		"features_dir": featuresDir,
	})
	if err != nil {
		t.Fatalf("TourPlanHandler: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("TourPlanHandler returned Error: %s", result.Error)
	}
	tourAny, ok := result.Data["tour"]
	if !ok {
		t.Fatal("Data[\"tour\"] missing")
	}
	tourMap, ok := tourAny.(map[string]any)
	if !ok {
		t.Fatalf("Data[\"tour\"] is %T, want map[string]any", tourAny)
	}
	if tourMap["version"] != float64(2) {
		t.Errorf("tour.version = %v, want 2", tourMap["version"])
	}
	if tourMap["id"] != "agent-actions" {
		t.Errorf("tour.id = %v, want agent-actions", tourMap["id"])
	}
	steps, ok := tourMap["steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("tour.steps = %v, want a non-empty array", tourMap["steps"])
	}
}

func TestTourPlanHandler_MissingFeatureIDIsAnExpectedError(t *testing.T) {
	result, err := TourPlanHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected Result.Error for a missing feature_id")
	}
}

func TestTourPlanHandler_UnknownFeatureIsAnExpectedError(t *testing.T) {
	root := repoRootForTourTest(t)
	result, err := TourPlanHandler(context.Background(), map[string]any{
		"feature_id":   "definitely-does-not-exist",
		"features_dir": filepath.Join(root, "features"),
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected Result.Error for an unknown feature_id")
	}
}

func TestTourValidateHandler_ValidatesAndConvertsAPlannedTour(t *testing.T) {
	root := repoRootForTourTest(t)
	featuresDir := filepath.Join(root, "features")
	if _, err := os.Stat(filepath.Join(featuresDir, "agent-actions.yaml")); err != nil {
		t.Skip("features/agent-actions.yaml not present")
	}

	planned, err := TourPlanHandler(context.Background(), map[string]any{
		"feature_id":   "agent-actions",
		"features_dir": featuresDir,
	})
	if err != nil || planned.Error != "" {
		t.Fatalf("plan: err=%v result=%+v", err, planned)
	}

	validated, err := TourValidateHandler(context.Background(), map[string]any{"tour": planned.Data["tour"]})
	if err != nil {
		t.Fatalf("TourValidateHandler: %v", err)
	}
	if validated.Error != "" {
		t.Fatalf("TourValidateHandler returned Error: %s", validated.Error)
	}
	if validated.Data["ok"] != true {
		t.Errorf("Data[\"ok\"] = %v, want true", validated.Data["ok"])
	}
	steps, ok := validated.Data["v1_steps"].([]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("v1_steps = %v, want a non-empty array", validated.Data["v1_steps"])
	}
}

func TestTourValidateHandler_RejectsMalformedTour(t *testing.T) {
	result, err := TourValidateHandler(context.Background(), map[string]any{
		"tour": map[string]any{"version": 2}, // no id, no steps
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected Result.Error for a malformed tour")
	}
}

func TestTourValidateHandler_RejectsUnrenderableAct(t *testing.T) {
	// act.kind=fill with no data.drive passthrough has no v1 equivalent
	// (see internal/tour/convert_v2_to_v1.go) — validate must surface that
	// as a loud Result.Error, not silently drop the step.
	result, err := TourValidateHandler(context.Background(), map[string]any{
		"tour": map[string]any{
			"version": 2,
			"id":      "x",
			"steps": []any{
				map[string]any{
					"id":     "s1",
					"kind":   "act",
					"target": map[string]any{"testid": "name-input"},
					"act":    map[string]any{"kind": "fill", "value": "hi"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected Result.Error for an unrenderable act step")
	}
}
