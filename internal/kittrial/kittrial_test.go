package kittrial

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"kitsoki/internal/store"
	"kitsoki/internal/taskcase"
	"kitsoki/internal/testrunner"
)

func event(kind store.EventKind, payload string) store.Event {
	return store.Event{Kind: kind, Payload: json.RawMessage(payload)}
}

func flowResultWithEvents(events ...store.Event) *testrunner.FlowResult {
	return &testrunner.FlowResult{Turns: []testrunner.TurnResult{{Events: events}}}
}

func TestDiffRuns(t *testing.T) {
	baseline := flowResultWithEvents(
		event(store.TransitionApplied, `{"to":"core.ticket_search"}`),
		event(store.HostDispatched, `{"namespace":"host.git"}`),
		event(store.HostDispatched, `{"namespace":"host.git"}`),
		event(store.TransitionApplied, `{"to":"core.bf.reproducing"}`),
	)
	staged := flowResultWithEvents(
		event(store.TransitionApplied, `{"to":"core.ticket_search"}`),
		event(store.HostDispatched, `{"namespace":"host.git"}`),
		event(store.HostDispatched, `{"namespace":"host.repo.map"}`),
		event(store.TransitionApplied, `{"to":"core.triage"}`),
		event(store.TransitionApplied, `{"to":"core.bf.reproducing"}`),
	)

	d := DiffRuns(baseline, staged)
	if d.Identical {
		t.Fatal("diff should not be identical")
	}
	if !reflect.DeepEqual(d.AddedStates, []string{"core.triage"}) {
		t.Errorf("AddedStates = %v", d.AddedStates)
	}
	if len(d.RemovedStates) != 0 {
		t.Errorf("RemovedStates = %v", d.RemovedStates)
	}
	if !reflect.DeepEqual(d.AddedHostCalls, []string{"host.repo.map"}) {
		t.Errorf("AddedHostCalls = %v", d.AddedHostCalls)
	}
	if !reflect.DeepEqual(d.DroppedHostCalls, []string{"host.git"}) {
		t.Errorf("DroppedHostCalls = %v (one fewer host.git call)", d.DroppedHostCalls)
	}

	same := DiffRuns(baseline, baseline)
	if !same.Identical {
		t.Errorf("self-diff should be identical, got %+v", same)
	}
}

func TestAuditFlowResultsSplitsLiveFromReplay(t *testing.T) {
	results := []testrunner.FlowResult{*flowResultWithEvents(
		event(store.AgentReturned, `{"meta":{"transport":"cassette","cost_usd":2.04}}`),
		event(store.AgentReturned, `{"meta":{"transport":"claude","cost_usd":0.5}}`),
		event(store.AgentReturned, `{"meta":{"cost_usd":0.25}}`), // no transport marker = live
		event(store.HostDispatched, `{"namespace":"host.git"}`),  // not an agent event
	)}
	a := AuditFlowResults(results)
	if a.ReplayedCalls != 1 || a.ReplayedRecordedCostUSD != 2.04 {
		t.Errorf("replay accounting = %+v", a)
	}
	if a.LiveCalls != 2 || a.CostUSD != 0.75 {
		t.Errorf("live accounting = %+v — recorded cassette cost must not count as spend", a)
	}
}

func TestLedgerRoundTripLookupAndOracleHash(t *testing.T) {
	root := t.TempDir()
	path := LedgerPath(root)

	fixture := filepath.Join(root, "fix.yaml")
	if err := os.WriteFile(fixture, []byte("test_kind: flow\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := taskcase.Oracle{Kind: taskcase.OracleFlowFixture, Comparator: "fix.yaml"}
	sha1, err := OracleSHA256(oracle, root)
	if err != nil {
		t.Fatal(err)
	}

	l, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Append(LedgerEntry{
		CaseID: "c1", Kit: "widget", KitTreeHash: "t1", InstanceDigest: "i1",
		Oracle: OracleRef{Kind: "flow_fixture", Ref: "fix.yaml", SHA256: sha1},
		Result: "fail", Mode: "replay", RecordedAt: "2026-07-11T00:00:00Z",
	})
	l.Append(LedgerEntry{
		CaseID: "c1", Kit: "widget", KitTreeHash: "t1", InstanceDigest: "i1",
		Oracle: OracleRef{Kind: "flow_fixture", Ref: "fix.yaml", SHA256: sha1},
		Result: "pass", Mode: "replay", RecordedAt: "2026-07-11T01:00:00Z",
	})
	if err := SaveLedger(path, l); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	hit := got.Lookup("c1", "t1", "i1", sha1)
	if hit == nil || hit.Result != "pass" {
		t.Fatalf("lookup should return the LATEST entry (pass), got %+v", hit)
	}
	if got.Lookup("c1", "t2", "i1", sha1) != nil {
		t.Fatal("a different kit tree hash must miss")
	}

	// Editing the comparator invalidates the oracle hash → old entries miss.
	if err := os.WriteFile(fixture, []byte("test_kind: flow\n# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sha2, err := OracleSHA256(oracle, root)
	if err != nil {
		t.Fatal(err)
	}
	if sha1 == sha2 {
		t.Fatal("comparator edit must change the oracle hash")
	}
	if got.Lookup("c1", "t1", "i1", sha2) != nil {
		t.Fatal("edited oracle must invalidate prior validations")
	}
}

func TestInstanceDigestOrderIndependent(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "app.yaml")
	b := filepath.Join(dir, "b", "app.yaml")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("app: {id: "+filepath.Base(filepath.Dir(p))+"}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d1, err := InstanceDigest([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	d2, err := InstanceDigest([]string{b, a})
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatal("instance digest must be order-independent")
	}
	if err := os.WriteFile(b, []byte("app: {id: changed}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d3, _ := InstanceDigest([]string{a, b})
	if d3 == d1 {
		t.Fatal("content change must change the digest")
	}
}
