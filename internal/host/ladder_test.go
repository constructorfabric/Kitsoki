package host_test

// Table-driven, no-LLM tests for the generalized harness ladder
// (internal/host/ladder.go). Every test drives host.RunLadder directly with a
// synthetic host.LadderAttemptFunc — no subprocess, no ClaudeRunner stub, no
// real LLM — so the loop's infra-vs-capability routing, effort-first
// escalation, backoff persistence, and instrumentation are exercised in
// isolation from the decide/task handler bodies (those get their own
// end-to-end coverage in TestAgentDecide_Ladder_* / TestAgentTask_Ladder_*).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// twoModelConfig returns a small ladder: two models, three efforts, backed by
// a fresh on-disk backoff state file scoped to t (so tests never share
// backoff state with each other or with a real .artifacts/quota file).
func twoModelConfig(t *testing.T) host.LadderConfig {
	t.Helper()
	return host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "claude", Provider: "cheap", Model: "modelA"},
			{Backend: "claude", Provider: "strong", Model: "modelB"},
		},
		Efforts:   []string{"low", "medium", "high"},
		StatePath: filepath.Join(t.TempDir(), "ladder-state.json"),
	}
}

func infraResult(msg string) host.Result {
	return host.Result{Error: msg, FailureKind: host.FailureInfra}
}

func capabilityResult(msg string) host.Result {
	return host.Result{Error: msg, FailureKind: host.FailureCapability}
}

func fatalResult(msg string) host.Result {
	return host.Result{Error: msg, FailureKind: host.FailureFatal}
}

// TestRunLadder_InfraFailureFallsOverAndBacksOff verifies: an infra failure on
// rung 0 abandons the REST of that provider/harness lane's effort sweep (only
// one attempt against modelA, not three) and falls over to the next lane, which
// succeeds. The failing lane is marked in backoff (verified by later tests that
// share the same state file).
func TestRunLadder_InfraFailureFallsOverAndBacksOff(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	var attemptedModels []string
	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, ok := host.LadderRungFromContext(ctx)
		if !ok {
			t.Fatal("expected a ladder rung on ctx")
		}
		attemptedModels = append(attemptedModels, rung.Model)
		if rung.Model == "modelA" {
			return infraResult("503 service unavailable"), nil
		}
		return host.Result{Data: map[string]any{"ok": true}}, nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected success, got Result.Error=%q", res.Error)
	}
	if len(attemptedModels) != 2 || attemptedModels[0] != "modelA" || attemptedModels[1] != "modelB" {
		t.Fatalf("expected exactly one attempt against modelA then modelB, got %v", attemptedModels)
	}
	if len(summary.Attempts) != 2 {
		t.Fatalf("expected 2 total attempts (infra abandons the other 2 effort rungs on modelA), got %d", len(summary.Attempts))
	}
	if summary.Attempts[0].Failure != host.FailureInfra {
		t.Fatalf("expected first attempt classified infra, got %v", summary.Attempts[0].Failure)
	}
	if summary.Winner == nil || summary.Winner.Model != "modelB" {
		t.Fatalf("expected winner modelB, got %+v", summary.Winner)
	}
	if summary.EscalatedBy != "model" {
		t.Fatalf("expected escalated_by=model (different model than the previous attempt), got %q", summary.EscalatedBy)
	}
}

// TestRunLadder_InfraFailureSkipsSameProviderModelLadder verifies that an
// infra failure backs off the whole provider/harness lane, not just the exact
// model slot. A 429/quota/transport failure should jump to a different
// provider or harness instead of trying stronger models on the same broken
// lane.
func TestRunLadder_InfraFailureSkipsSameProviderModelLadder(t *testing.T) {
	t.Parallel()
	cfg := host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "claude", Provider: "claude-native", Model: "opus"},
			{Backend: "claude", Provider: "claude-native", Model: "sonnet"},
			{Backend: "codex", Provider: "codex-native", Model: "gpt-5.5"},
		},
		Efforts:   []string{"low", "high"},
		StatePath: filepath.Join(t.TempDir(), "ladder-state.json"),
	}

	var seen []string
	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		seen = append(seen, rung.Provider+"/"+rung.Model+"/"+rung.Effort)
		if rung.Provider == "claude-native" && rung.Model == "opus" {
			return infraResult("429 rate limited"), nil
		}
		return host.Result{}, nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" || summary.Winner == nil || summary.Winner.Provider != "codex-native" {
		t.Fatalf("expected fallback success on codex-native, res=%+v summary=%+v", res, summary)
	}
	wantSeen := []string{"claude-native/opus/low", "codex-native/gpt-5.5/low"}
	if fmt.Sprint(seen) != fmt.Sprint(wantSeen) {
		t.Fatalf("infra fallback should skip same-provider model/effort ladder;\n got  %v\n want %v", seen, wantSeen)
	}

	var secondSeen []string
	second := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		secondSeen = append(secondSeen, rung.Provider+"/"+rung.Model)
		return host.Result{}, nil
	}
	if _, err, summary := host.RunLadder(context.Background(), cfg, nil, second); err != nil || summary.Winner == nil {
		t.Fatalf("second ladder call failed: err=%v summary=%+v", err, summary)
	}
	if fmt.Sprint(secondSeen) != fmt.Sprint([]string{"codex-native/gpt-5.5"}) {
		t.Fatalf("provider/harness backoff should skip all claude-native models, got %v", secondSeen)
	}
}

// TestRunLadder_BackoffSkipsProvider verifies that a provider/harness lane
// marked in backoff by one RunLadder call is SKIPPED entirely by a subsequent
// call sharing the same on-disk state file — even though the skipped first
// model would otherwise have succeeded this time.
func TestRunLadder_BackoffSkipsProvider(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	// First walk: modelA infra-fails, gets backed off; modelB succeeds.
	first := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" {
			return infraResult("429 rate limited"), nil
		}
		return host.Result{}, nil
	}
	if _, err, summary := host.RunLadder(context.Background(), cfg, nil, first); err != nil || summary.Winner == nil {
		t.Fatalf("setup walk failed: err=%v summary=%+v", err, summary)
	}

	// Second walk, same statePath: modelA would now succeed if attempted —
	// prove it never is.
	calledA := false
	second := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" {
			calledA = true
		}
		return host.Result{}, nil // every rung "succeeds" if reached
	}
	res, err, summary := host.RunLadder(context.Background(), cfg, nil, second)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected success on modelB, got Result.Error=%q", res.Error)
	}
	if calledA {
		t.Fatal("modelA is in backoff and must not have been dispatched")
	}
	if len(summary.Attempts) != 1 || summary.Attempts[0].Rung.Model != "modelB" {
		t.Fatalf("expected exactly one attempt against modelB (modelA skipped), got %+v", summary.Attempts)
	}
	if summary.Winner == nil || summary.Winner.Model != "modelB" {
		t.Fatalf("expected winner modelB, got %+v", summary.Winner)
	}
}

// TestRunLadder_CapabilityFailureEscalatesEffortBeforeModel verifies the
// effort-first escalation rule: a capability failure sweeps EVERY effort on
// the SAME model (low, medium, high) before the ladder ever tries a
// different, stronger model.
func TestRunLadder_CapabilityFailureEscalatesEffortBeforeModel(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	var seen []string
	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		seen = append(seen, rung.Model+"/"+rung.Effort)
		if rung.Model == "modelA" {
			return capabilityResult("schema validation failed"), nil
		}
		return host.Result{}, nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected eventual success on modelB, got %q", res.Error)
	}
	wantOrder := []string{"modelA/low", "modelA/medium", "modelA/high", "modelB/low"}
	if fmt.Sprint(seen) != fmt.Sprint(wantOrder) {
		t.Fatalf("expected effort-first sweep on modelA before moving to modelB;\n got  %v\n want %v", seen, wantOrder)
	}
	if len(summary.Attempts) != 4 {
		t.Fatalf("expected 4 attempts, got %d", len(summary.Attempts))
	}
	for i, want := range []string{"", "effort", "effort", "model"} {
		if got := summary.Attempts[i].Rung.EscalatedBy; got != want {
			t.Errorf("attempt %d: escalated_by = %q, want %q", i, got, want)
		}
	}
	if summary.EscalatedBy != "model" {
		t.Fatalf("expected the winning rung's escalated_by=model, got %q", summary.EscalatedBy)
	}
}

// TestRunLadder_SuccessAfterEffortBump verifies a pass that comes from an
// EFFORT bump (same model as the immediately preceding failed attempt) is
// labeled "effort", not "model".
func TestRunLadder_SuccessAfterEffortBump(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" && rung.Effort == "low" {
			return capabilityResult("not detailed enough"), nil
		}
		return host.Result{}, nil // modelA/medium succeeds
	}

	_, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if summary.Winner == nil || summary.Winner.Model != "modelA" || summary.Winner.Effort != "medium" {
		t.Fatalf("expected winner modelA/medium, got %+v", summary.Winner)
	}
	if summary.EscalatedBy != "effort" {
		t.Fatalf("expected escalated_by=effort (same model, higher effort), got %q", summary.EscalatedBy)
	}
}

// TestRunLadder_AllRungsInfraFail_TerminalGracefulError verifies that when
// every model infra-fails, RunLadder returns a graceful Result.Error (never a
// crash, never a raw propagated Go error) after walking every model exactly
// once (infra abandons the effort sweep per model).
func TestRunLadder_AllRungsInfraFail_TerminalGracefulError(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	once := func(context.Context, map[string]any) (host.Result, error) {
		return infraResult("connection refused"), nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("expected a graceful Result.Error, got a Go error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected a terminal Result.Error when every rung infra-fails")
	}
	if summary.Winner != nil {
		t.Fatalf("expected no winner, got %+v", summary.Winner)
	}
	if len(summary.Attempts) != len(cfg.Models) {
		t.Fatalf("expected exactly one attempt per model (%d), got %d", len(cfg.Models), len(summary.Attempts))
	}
}

// TestRunLadder_FatalStopsImmediately verifies a FailureFatal result (a
// config/argument error no rung can fix) halts the walk after exactly one
// attempt and returns the ORIGINAL error untouched — not wrapped in an
// "exhausted after N attempts" message, which would misdescribe a single
// immediate stop as an exhaustive sweep.
func TestRunLadder_FatalStopsImmediately(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	calls := 0
	once := func(context.Context, map[string]any) (host.Result, error) {
		calls++
		return fatalResult("host.agent.decide: schema: argument is required"), nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt before stopping, got %d", calls)
	}
	if res.Error != "host.agent.decide: schema: argument is required" {
		t.Fatalf("expected the original error untouched, got %q", res.Error)
	}
	if len(summary.Attempts) != 1 || summary.Attempts[0].Failure != host.FailureFatal {
		t.Fatalf("expected a single fatal attempt recorded, got %+v", summary.Attempts)
	}
}

// TestRunLadder_ContextCanceled_StopsImmediately verifies a Go-error /
// cancelled-context attempt stops the walk rather than burning through every
// remaining rung against a dead context.
func TestRunLadder_ContextCanceled_StopsImmediately(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	calls := 0
	once := func(context.Context, map[string]any) (host.Result, error) {
		calls++
		return host.Result{}, context.Canceled
	}

	_, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled propagated, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt before stopping, got %d", calls)
	}
	if len(summary.Attempts) != 1 {
		t.Fatalf("expected a single attempt recorded, got %+v", summary.Attempts)
	}
}

// TestRunLadder_GoErrorClassifiedInfra verifies a bare Go error (the
// "infra failures are returned as Go errors instead" contract) is classified
// infra even with no FailureKind to read, and that it rotates to the next
// model.
func TestRunLadder_GoErrorClassifiedInfra(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)

	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" {
			return host.Result{}, errors.New("claude binary not found")
		}
		return host.Result{}, nil
	}

	res, err, summary := host.RunLadder(context.Background(), cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error propagated (should have rotated instead): %v", err)
	}
	if res.Error != "" {
		t.Fatalf("expected eventual success, got %q", res.Error)
	}
	if summary.Attempts[0].Failure != host.FailureInfra {
		t.Fatalf("expected the bare Go error classified infra, got %v", summary.Attempts[0].Failure)
	}
}

// TestRunLadder_TextHeuristicFallback verifies that when a caller leaves
// Result.FailureKind unset, RunLadder falls back to a text heuristic over
// Result.Error: an infra-shaped message (rate limit) still rotates models,
// while an unrecognized message defaults to capability (effort escalation).
func TestRunLadder_TextHeuristicFallback(t *testing.T) {
	t.Parallel()

	t.Run("infra-shaped text without explicit FailureKind", func(t *testing.T) {
		cfg := twoModelConfig(t)
		once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
			rung, _ := host.LadderRungFromContext(ctx)
			if rung.Model == "modelA" {
				return host.Result{Error: "429 too many requests"}, nil // no FailureKind set
			}
			return host.Result{}, nil
		}
		_, _, summary := host.RunLadder(context.Background(), cfg, nil, once)
		if summary.Attempts[0].Failure != host.FailureInfra {
			t.Fatalf("expected text heuristic to classify infra, got %v", summary.Attempts[0].Failure)
		}
		if len(summary.Attempts) != 2 {
			t.Fatalf("expected the infra classification to rotate to modelB after 1 attempt, got %d attempts", len(summary.Attempts))
		}
	})

	t.Run("unrecognized text defaults to capability", func(t *testing.T) {
		cfg := twoModelConfig(t)
		once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
			rung, _ := host.LadderRungFromContext(ctx)
			if rung.Model == "modelA" {
				return host.Result{Error: "verdict missing required field"}, nil // no FailureKind set
			}
			return host.Result{}, nil
		}
		_, _, summary := host.RunLadder(context.Background(), cfg, nil, once)
		if summary.Attempts[0].Failure != host.FailureCapability {
			t.Fatalf("expected unrecognized text to default to capability, got %v", summary.Attempts[0].Failure)
		}
		// Capability sweeps effort on modelA (3 rungs) before falling to modelB.
		if len(summary.Attempts) != 4 {
			t.Fatalf("expected effort sweep (3) + modelB (1) = 4 attempts, got %d", len(summary.Attempts))
		}
	})

	t.Run("zero-submit validator abandonment rotates lane", func(t *testing.T) {
		cfg := twoModelConfig(t)
		once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
			rung, _ := host.LadderRungFromContext(ctx)
			if rung.Model == "modelA" {
				return host.Result{Error: "validator: session abandoned without successful submit after 3 outer iteration(s), 0 attempt(s)"}, nil // no FailureKind set
			}
			return host.Result{}, nil
		}
		_, _, summary := host.RunLadder(context.Background(), cfg, nil, once)
		if summary.Attempts[0].Failure != host.FailureInfra {
			t.Fatalf("expected zero-submit abandonment to classify as infra, got %v", summary.Attempts[0].Failure)
		}
		if len(summary.Attempts) != 2 {
			t.Fatalf("expected infra classification to rotate to modelB after 1 attempt, got %d attempts", len(summary.Attempts))
		}
	})
}

// TestExpandLadder_ModelOuterEffortInner verifies the reference grid ordering:
// model outer (cheap→strong), effort inner (low→max) on the all-capability-
// failure path, with EscalatedBy computed prospectively for every rung.
func TestExpandLadder_ModelOuterEffortInner(t *testing.T) {
	t.Parallel()
	cfg := host.LadderConfig{
		Models: []host.LadderModel{
			{Backend: "codex", Model: "cheap"},
			{Backend: "claude", Model: "strong"},
		},
		Efforts: []string{"low", "high"},
	}
	rungs := host.ExpandLadder(cfg)
	if len(rungs) != 4 {
		t.Fatalf("expected 4 rungs (2 models x 2 efforts), got %d", len(rungs))
	}
	want := []struct {
		model, effort, escalatedBy string
	}{
		{"cheap", "low", ""},
		{"cheap", "high", "effort"},
		{"strong", "low", "model"},
		{"strong", "high", "effort"},
	}
	for i, w := range want {
		r := rungs[i]
		if r.Model != w.model || r.Effort != w.effort || r.EscalatedBy != w.escalatedBy {
			t.Errorf("rung %d = {model:%q effort:%q escalated_by:%q}, want {%q %q %q}",
				i, r.Model, r.Effort, r.EscalatedBy, w.model, w.effort, w.escalatedBy)
		}
	}
}

// TestDefaultLadderConfig_IsAvailabilityFirst verifies the shipped default
// ladder's model ordering matches the operator-priority fallback design
// (claude → codex → synthetic-claude → synthetic-codex) and sweeps the full
// effort catalog.
func TestDefaultLadderConfig_IsAvailabilityFirst(t *testing.T) {
	t.Parallel()
	cfg := host.DefaultLadderConfig()
	if !cfg.Enabled() {
		t.Fatal("expected the default ladder to be enabled")
	}
	wantModels := []struct {
		backend  string
		provider string
		model    string
	}{
		{"claude", "claude-native", "opus"},
		{"codex", "codex-native", "gpt-5.5"},
		{"claude", "synthetic-claude", "hf:zai-org/GLM-5.2"},
		{"codex", "synthetic-codex", "hf:zai-org/GLM-5.2"},
	}
	if len(cfg.Models) != len(wantModels) {
		t.Fatalf("expected %d models, got %d", len(wantModels), len(cfg.Models))
	}
	for i, want := range wantModels {
		got := cfg.Models[i]
		if got.Backend != want.backend || got.Provider != want.provider || got.Model != want.model {
			t.Errorf("model %d = {%q %q %q}, want {%q %q %q}",
				i, got.Backend, got.Provider, got.Model, want.backend, want.provider, want.model)
		}
	}
	wantEfforts := []string{"low", "medium", "high", "xhigh", "max"}
	if fmt.Sprint(cfg.Efforts) != fmt.Sprint(wantEfforts) {
		t.Fatalf("efforts = %v, want %v", cfg.Efforts, wantEfforts)
	}
}

func TestRunLadder_InfraFailureEmitsFallbackNotice(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)
	sink := &captureSink{}
	stream := &recordingStreamSink{}
	ctx := host.WithAgentEventSink(context.Background(), sink)
	ctx = host.WithStreamSink(ctx, stream)
	ctx = host.WithLadderSessionState(ctx, host.NewLadderSessionState())
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("sess-ladder"),
		Turn:      app.TurnNumber(7),
		StatePath: app.StatePath("work.running"),
	})

	once := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" {
			return infraResult("429 rate limited"), nil
		}
		return host.Result{}, nil
	}
	res, err, summary := host.RunLadder(ctx, cfg, nil, once)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" || summary.Winner == nil {
		t.Fatalf("expected fallback success, res=%+v summary=%+v", res, summary)
	}

	var notice map[string]any
	for _, ev := range sink.events {
		if ev.Kind != store.AgentStreamEvent {
			continue
		}
		if err := json.Unmarshal(ev.Payload, &notice); err != nil {
			t.Fatalf("unmarshal notice: %v", err)
		}
		if notice["type"] == "ladder_fallback" {
			break
		}
		notice = nil
	}
	if notice == nil {
		t.Fatalf("expected a persisted ladder_fallback agent.stream event, got %+v", sink.events)
	}
	if notice["subtype"] != string(host.FailureInfra) || notice["model"] != "modelA" {
		t.Fatalf("unexpected fallback notice payload: %+v", notice)
	}
	if notice["severity"] != "warn" {
		t.Fatalf("fallback notice should use warning severity, got %+v", notice)
	}
	if text, _ := notice["text"].(string); text == "" ||
		!strings.Contains(text, "429 rate limited") ||
		!strings.Contains(text, "skipping its remaining effort/model ladder") {
		t.Fatalf("notice text should explain the visible 429 reason and lane skip, got %q", text)
	}

	gotTypes := make([]string, 0, len(stream.events))
	for _, ev := range stream.events {
		gotTypes = append(gotTypes, ev.Type)
	}
	wantTypes := []string{"ladder_attempt", "ladder_fallback", "ladder_attempt", "ladder_session_pinned", "ladder_success"}
	if fmt.Sprint(gotTypes) != fmt.Sprint(wantTypes) {
		t.Fatalf("live stream ladder events = %v, want %v", gotTypes, wantTypes)
	}
	if stream.events[0].Provider != "cheap" || stream.events[0].Backend != "claude" || stream.events[0].Model != "modelA" {
		t.Fatalf("attempt event should identify the first provider/backend/model, got %+v", stream.events[0])
	}
	if !strings.Contains(stream.events[1].Text, "Kitsoki harness: cheap / modelA via claude") ||
		!strings.Contains(stream.events[1].Text, "429 rate limited") ||
		!strings.Contains(stream.events[1].Text, "skipping its remaining effort/model ladder") {
		t.Fatalf("fallback event should carry source and error text, got %q", stream.events[1].Text)
	}
	if !strings.Contains(stream.events[2].Text, "strong / modelB via claude") {
		t.Fatalf("second attempt should name the next rung, got %q", stream.events[2].Text)
	}
	if stream.events[3].Severity != "warn" ||
		!strings.Contains(stream.events[3].Text, "until you change /provider, /model, or /effort") {
		t.Fatalf("session-pinned notice should warn and explain reset commands, got %+v", stream.events[3])
	}
}

func TestRunLadder_SessionFallbackSticksUntilReset(t *testing.T) {
	t.Parallel()
	cfg := twoModelConfig(t)
	sessionState := host.NewLadderSessionState()
	ctx := host.WithLadderSessionState(context.Background(), sessionState)

	first := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		if rung.Model == "modelA" {
			return infraResult("429 rate limited"), nil
		}
		return host.Result{}, nil
	}
	if _, err, summary := host.RunLadder(ctx, cfg, nil, first); err != nil || summary.Winner == nil || summary.Winner.Model != "modelB" {
		t.Fatalf("setup fallback did not pin modelB: err=%v summary=%+v", err, summary)
	}

	// Fresh state path: modelA is not in backoff for this call. If the sticky
	// session fallback is not honored, modelA would be attempted and succeed.
	cfgFreshBackoff := cfg
	cfgFreshBackoff.StatePath = filepath.Join(t.TempDir(), "fresh-ladder-state.json")
	var secondAttempts []string
	second := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		secondAttempts = append(secondAttempts, rung.Model)
		return host.Result{}, nil
	}
	if _, err, summary := host.RunLadder(ctx, cfgFreshBackoff, nil, second); err != nil || summary.Winner == nil {
		t.Fatalf("sticky fallback call failed: err=%v summary=%+v", err, summary)
	}
	if fmt.Sprint(secondAttempts) != fmt.Sprint([]string{"modelB"}) {
		t.Fatalf("sticky fallback should start at modelB without retrying modelA, got %v", secondAttempts)
	}

	sessionState.Reset()
	var afterResetAttempts []string
	afterReset := func(ctx context.Context, _ map[string]any) (host.Result, error) {
		rung, _ := host.LadderRungFromContext(ctx)
		afterResetAttempts = append(afterResetAttempts, rung.Model)
		return host.Result{}, nil
	}
	if _, err, summary := host.RunLadder(ctx, cfgFreshBackoff, nil, afterReset); err != nil || summary.Winner == nil {
		t.Fatalf("post-reset ladder call failed: err=%v summary=%+v", err, summary)
	}
	if fmt.Sprint(afterResetAttempts) != fmt.Sprint([]string{"modelA"}) {
		t.Fatalf("reset should allow the ladder to start at modelA again, got %v", afterResetAttempts)
	}
}

// TestHarnessLadderFromContext_NoLadderIsNoOp verifies the ctx plumbing is a
// true no-op absent an installed ladder — the load-bearing invariant that
// keeps every pre-existing call site / test unaffected.
func TestHarnessLadderFromContext_NoLadderIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, ok := host.HarnessLadderFromContext(ctx); ok {
		t.Fatal("expected no ladder installed on a bare context")
	}
	// A disabled (zero-value) config must not install anything either.
	ctx2 := host.WithHarnessLadder(ctx, host.LadderConfig{})
	if _, ok := host.HarnessLadderFromContext(ctx2); ok {
		t.Fatal("expected WithHarnessLadder to no-op for a disabled config")
	}
	if _, ok := host.LadderRungFromContext(ctx); ok {
		t.Fatal("expected no rung installed on a bare context")
	}
}

type recordingStreamSink struct {
	events []host.StreamEvent
}

func (s *recordingStreamSink) OnStreamEvent(_ context.Context, ev host.StreamEvent) {
	s.events = append(s.events, ev)
}
