package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/kittrial"
	"kitsoki/internal/kitworklist"
)

// The manifests below stage the S7 acceptance scenario
// (.context/kits-implementation-plan.md: "a staged upstream rev of
// dev-story absorbed by the kitsoki instance via worklist alone"): v2
// renames an exported intent (with a compat hint) and requires a new host —
// one mechanical rename, one declared-hosts addition, both surfaced by the
// trial worklist with concrete suggested actions.

const trialWidgetV1 = `app: { id: widget, version: 1.2.0 }
hosts: [host.run]
world:
  count: { type: int, default: 0 }
intents:
  bump: { description: "bump count", examples: [bump] }
exports:
  intents: [bump]
root: idle
states:
  idle:
    view: "idle"
    on:
      bump:
        - target: .
          effects:
            - set: { count: 1 }
`

const trialWidgetV2 = `app: { id: widget, version: 1.3.0 }
hosts: [host.run, host.local]
world:
  count: { type: int, default: 0 }
intents:
  nudge: { description: "bump count (renamed from bump)", examples: [nudge] }
exports:
  intents: [nudge]
root: idle
states:
  idle:
    view: "idle"
    on:
      nudge:
        - target: .
          effects:
            - set: { count: 1 }
`

const trialWidgetV2KitYAML = `schema: kit/v1
kit: widget
namespace: kitsoki
version: 1.3.0
provides:
  stories: [widget]
  story_dirs:
    widget: "."
compat:
  renamed:
    intents:
      bump: nudge
`

const trialConsumerV1 = `app: { id: proj-dev, version: 0.1.0 }
hosts:
  - host.run
imports:
  core:
    source: "@kitsoki/widget"
    entry: idle
    hosts: declared
    intents:
      import: [bump]
root: core
`

// trialConsumerV2 is the consumer after applying the worklist: the renamed
// intent import and the newly required host declaration.
const trialConsumerV2 = `app: { id: proj-dev, version: 0.1.0 }
hosts:
  - host.run
  - host.local
imports:
  core:
    source: "@kitsoki/widget"
    entry: idle
    hosts: declared
    intents:
      import: [nudge]
root: core
`

const trialFixtureV1 = `test_kind: flow
app: ../app.yaml
initial_state: core.idle
turns:
  - intent: { name: bump }
    expect_state: core.idle
    expect_world: { core__count: 1 }
expect_no_errors: true
`

const trialFixtureV2 = `test_kind: flow
app: ../app.yaml
initial_state: core.idle
turns:
  - intent: { name: nudge }
    expect_state: core.idle
    expect_world: { core__count: 1 }
expect_no_errors: true
`

const trialTaskCase = `kind: history_task.v1
id: widget/bump-route
lane: routing
source:
  corpus_ref: "test:bump-route"
  repo: example/proj
story:
  app: .kitsoki/stories/proj-dev/app.yaml
  entrypoint: core.idle
trainable_surface:
  weight_kind: fixture
input:
  prompt_or_ticket: "bump the counter"
oracle:
  kind: flow_fixture
  comparator: .kitsoki/stories/proj-dev/flows/bump_route.yaml
cost_policy:
  live_policy: no_cost
artifacts:
  root: .artifacts/kit-trial/cases/bump-route
`

// setupTrialProject builds the consumer project locked at widget v1 and
// mutates the upstream lib to v2, returning (libKitDir, projectRoot).
func setupTrialProject(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Isolate from any real ~/.kitsoki state (kit-dev overrides, repo pin).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KITSOKI_REPO", "")

	lib := t.TempDir()
	kitDir := filepath.Join(lib, "widget")
	if err := os.MkdirAll(kitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte(trialWidgetV1), 0o644); err != nil {
		t.Fatal(err)
	}

	target := t.TempDir()
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")
	if err := os.MkdirAll(filepath.Join(instDir, "flows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(trialConsumerV1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(trialFixtureV1), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, err := runKit(t, "add", kitDir, "--name", "widget", "--version", "^1.0.0", "--target", target); err != nil {
		t.Fatalf("kit add: %v\n%s", err, out)
	}

	// Upstream ships v2: renamed intent (compat hint) + new required host.
	if err := os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte(trialWidgetV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kitDir, "kit.yaml"), []byte(trialWidgetV2KitYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return kitDir, target
}

// TestKitLifecycle_StagedUpstreamRevAbsorbedViaWorklistAlone is the S7
// acceptance goal as an executable test: stage the upstream rev, let the
// trial's worklist name every required edit, apply exactly those edits,
// re-trial to green, accept — and prove reject leaves no residue and the
// validation ledger prevents re-validation.
func TestKitLifecycle_StagedUpstreamRevAbsorbedViaWorklistAlone(t *testing.T) {
	_, target := setupTrialProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")

	// Stage the candidate. The recorded ^1.0.0 constraint admits 1.3.0.
	if out, err := runKit(t, "update", "widget", "--target", target); err != nil {
		t.Fatalf("kit update: %v\n%s", err, out)
	}

	// Trial #1: blocked, with the worklist naming both edits.
	out, err := runKit(t, "trial", "widget", "--target", target)
	if err == nil {
		t.Fatalf("trial #1 should exit non-zero (blocked):\n%s", out)
	}
	if !strings.Contains(out, "blocked") {
		t.Fatalf("trial #1 output should say blocked:\n%s", out)
	}
	wl, err := kitworklist.Load(kitworklist.Path(target, "widget"))
	if err != nil || wl == nil {
		t.Fatalf("worklist should exist after trial #1 (err=%v)", err)
	}
	if wl.OpenErrors() == 0 {
		t.Fatalf("trial #1 worklist should have open errors: %+v", wl.Items)
	}
	joined := ""
	for _, it := range wl.Items {
		joined += it.Detail + " | " + it.SuggestedAction + "\n"
	}
	if !strings.Contains(joined, "host.local") {
		t.Errorf("worklist should name the missing host.local declaration:\n%s", joined)
	}
	if !strings.Contains(joined, "bump") || !strings.Contains(joined, "nudge") {
		t.Errorf("worklist should carry the bump->nudge rename hint:\n%s", joined)
	}

	// Accept must refuse a blocked trial.
	if out, err := runKit(t, "accept", "widget", "--target", target); err == nil {
		t.Fatalf("accept should refuse a blocked trial:\n%s", out)
	}

	// Apply exactly the worklist edits (rename import + declare the host;
	// the fixture rename is the same mechanical edit applied to evidence).
	if err := os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(trialConsumerV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(trialFixtureV2), 0o644); err != nil {
		t.Fatal(err)
	}
	// A baseline task case rides along: its no-cost oracle validates by
	// replay during trial #2 and must SKIP via the ledger in trial #3.
	casesDir := filepath.Join(target, ".kitsoki", "qa", "taskcases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casesDir, "bump-route.yaml"), []byte(trialTaskCase), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trial #2: ready. The fixture now fails at the LOCKED version (the
	// rename was applied to evidence) — that surfaces as an advisory
	// stale-baseline warning, never as an upgrade failure.
	out2, err := runKit(t, "trial", "widget", "--target", target)
	if err != nil {
		t.Fatalf("trial #2: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "ready") {
		t.Fatalf("trial #2 should be ready:\n%s", out2)
	}
	if !strings.Contains(out2, "stale at locked version") {
		t.Errorf("trial #2 should report the stale baseline leg:\n%s", out2)
	}
	if !strings.Contains(out2, "validated_replay") {
		t.Errorf("trial #2 should validate the baseline case by replay:\n%s", out2)
	}
	ledger, err := kittrial.LoadLedger(kittrial.LedgerPath(target))
	if err != nil || len(ledger.Entries) == 0 {
		t.Fatalf("trial #2 should record a ledger entry (err=%v, entries=%d)", err, len(ledger.Entries))
	}

	// Trial #3: the ledger prevents re-validation.
	out3, err := runKit(t, "trial", "widget", "--target", target)
	if err != nil {
		t.Fatalf("trial #3: %v\n%s", err, out3)
	}
	if !strings.Contains(out3, "skipped_already_validated") {
		t.Fatalf("trial #3 should skip the already-validated case:\n%s", out3)
	}
	ledgerAfter, _ := kittrial.LoadLedger(kittrial.LedgerPath(target))
	if len(ledgerAfter.Entries) != len(ledger.Entries) {
		t.Fatalf("a skipped case must not append ledger entries (%d -> %d)", len(ledger.Entries), len(ledgerAfter.Entries))
	}

	// Accept: lock promoted, constraint kept, acceptance receipt written,
	// staging gone.
	outAccept, err := runKit(t, "accept", "widget", "--target", target)
	if err != nil {
		t.Fatalf("kit accept: %v\n%s", err, outAccept)
	}
	lf, err := kitlock.Load(kitlock.Path(target))
	if err != nil {
		t.Fatal(err)
	}
	entry := lf.Kits["widget"]
	if entry == nil || entry.Version != "1.3.0" {
		t.Fatalf("lock should pin 1.3.0 after accept, got %+v", entry)
	}
	if entry.Constraint != "^1.0.0" {
		t.Errorf("accept should keep the recorded constraint, got %q", entry.Constraint)
	}
	if kitstage.Exists(kitstage.Path(target)) {
		t.Error("staged lockfile should be gone after accept")
	}
	if _, err := os.Stat(kitstage.WorkDir(target, "widget")); !os.IsNotExist(err) {
		t.Error("kit-update workdir should be gone after accept")
	}
	receipt, err := kittrial.LoadReceipt(kittrial.AcceptReceiptPath(target, "widget", "1.3.0"))
	if err != nil || receipt == nil {
		t.Fatalf("acceptance receipt should exist (err=%v)", err)
	}
	if receipt.Event != kittrial.EventAccept || receipt.Result != kittrial.ResultAccepted {
		t.Errorf("acceptance receipt = %s/%s", receipt.Event, receipt.Result)
	}
	if receipt.From.Version != "1.2.0" || receipt.To.Version != "1.3.0" {
		t.Errorf("receipt from/to = %s/%s", receipt.From.Version, receipt.To.Version)
	}
	if receipt.Spend.CostUSD != 0 {
		t.Errorf("the whole lifecycle must measure $0 live spend, got %v", receipt.Spend.CostUSD)
	}
}

// TestKitTrial_AcceptFailsClosedOnDigestDrift proves accept re-verifies the
// receipt's source digests: an instance edit after the trial invalidates it.
func TestKitTrial_AcceptFailsClosedOnDigestDrift(t *testing.T) {
	_, target := setupTrialProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")

	if out, err := runKit(t, "update", "widget", "--target", target); err != nil {
		t.Fatalf("kit update: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(trialConsumerV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(trialFixtureV2), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runKit(t, "trial", "widget", "--target", target); err != nil {
		t.Fatalf("trial: %v\n%s", err, out)
	}

	// Post-trial drift: the instance changes again.
	if err := os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(trialConsumerV2+"\n# drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runKit(t, "accept", "widget", "--target", target)
	if err == nil {
		t.Fatalf("accept should fail closed on digest drift:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "digest drift") {
		t.Errorf("error should explain the drift: %v\n%s", err, out)
	}
}
