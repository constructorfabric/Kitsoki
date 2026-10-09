// Package host — host.tour.plan and host.tour.validate: the "generate" and
// "validate" steps of the on-demand tour serving loop
// (.context/2026-07-12-browser-mcp-tour-implementation-brief.md, P5:
// ask -> generate -> validate -> push -> play). Both are pure, deterministic,
// no-LLM functions — the "cheap path" from the brief, where an already-known
// feature's baked tour (features/<id>.yaml's tour: block) is planned and
// validated. The "expensive path" (author live against a headless replica
// via tools/browser-mcp when the baked catalog can't cover the question) is
// out of scope here; a caller that can't resolve a feature_id gets a clear
// "not found" Result rather than a fabricated tour.
//
// Story wiring resolves WHICH feature answers a question upstream (kitsoki's
// existing intent/semroute machinery); host.tour.plan's job starts one step
// later, at "given a resolved feature_id, build its v2 tour."
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"kitsoki/internal/tour"
)

// TourPlanHandler implements host.tour.plan. Args:
//
//	feature_id   string, required — the features/<feature_id>.yaml to plan from.
//	features_dir string, optional — defaults to "features" (repo-root-relative,
//	             matching how kitsoki normally runs with cwd at the repo root).
//
// Result.Data on success: {"tour": <tour format v2 JSON, as map[string]any>}.
// Result.Error (not a Go error) when the feature doesn't exist or has no
// tour block — an ordinary, expected "couldn't plan a tour for that"
// outcome the story can react to, not an infra failure.
func TourPlanHandler(ctx context.Context, args map[string]any) (Result, error) {
	featureID, _ := args["feature_id"].(string)
	if featureID == "" {
		return Result{Error: "host.tour.plan: feature_id argument is required"}, nil
	}
	featuresDir, _ := args["features_dir"].(string)
	if featuresDir == "" {
		featuresDir = "features"
	}
	featurePath := filepath.Join(featuresDir, featureID+".yaml")

	m, _, err := tour.LoadFeatureManifest(featurePath, "")
	if err != nil {
		return Result{Error: fmt.Sprintf("host.tour.plan: no plannable tour for feature %q: %v", featureID, err)}, nil
	}

	v2 := tour.ConvertToV2(m)
	v2.ID = featureID
	asMap, err := tourManifestV2ToMap(v2)
	if err != nil {
		return Result{}, fmt.Errorf("host.tour.plan: %w", err)
	}
	return Result{Data: map[string]any{"tour": asMap}}, nil
}

// TourValidateHandler implements host.tour.validate: the pre-push gate. It
// checks the v2 document is well-formed (TourManifestV2.Validate) and
// renderable by internal/tour's existing chromedp pipeline
// (ConvertV2ToV1) — a structural gate, not a live anchor-resolution replay
// (that's tools/browser-mcp's tour_replay, a headless-browser check outside
// what a Go server handler should do inline in the request path). Args:
//
//	tour map[string]any, required — a tour format v2 document (as returned
//	     by host.tour.plan, or any v2 JSON).
//
// Result.Data on success: {"ok": true, "v1_steps": [...]} — v1_steps is the
// TourStep[] array the existing player (tools/runstatus's TourOverlay.vue /
// window.__startTourWithSteps) already knows how to render, so the push
// step needs no new frontend format support.
func TourValidateHandler(ctx context.Context, args map[string]any) (Result, error) {
	raw, ok := args["tour"]
	if !ok {
		return Result{Error: "host.tour.validate: tour argument is required"}, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return Result{}, fmt.Errorf("host.tour.validate: re-marshal tour arg: %w", err)
	}
	var v2 tour.TourManifestV2
	if err := json.Unmarshal(data, &v2); err != nil {
		return Result{Error: fmt.Sprintf("host.tour.validate: tour is not valid tour-v2 JSON: %v", err)}, nil
	}
	if err := v2.Validate(); err != nil {
		return Result{Error: fmt.Sprintf("host.tour.validate: %v", err)}, nil
	}
	v1, err := tour.ConvertV2ToV1(&v2)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.tour.validate: not renderable: %v", err)}, nil
	}
	stepsJSON, err := json.Marshal(v1.Steps)
	if err != nil {
		return Result{}, fmt.Errorf("host.tour.validate: %w", err)
	}
	var steps []any
	if err := json.Unmarshal(stepsJSON, &steps); err != nil {
		return Result{}, fmt.Errorf("host.tour.validate: %w", err)
	}
	return Result{Data: map[string]any{"ok": true, "v1_steps": steps}}, nil
}

// tourManifestV2ToMap round-trips a TourManifestV2 through JSON into a plain
// map[string]any — the shape story world vars and Result.Data expect,
// matching how every other host handler in this package returns structured
// data (see e.g. TourValidateHandler's v1_steps above).
func tourManifestV2ToMap(v2 *tour.TourManifestV2) (map[string]any, error) {
	data, err := json.Marshal(v2)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
